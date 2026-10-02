package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"vpsmon/internal/protocol"
)

const (
	onlineWithin  = 30 * time.Second // 在线判定阈值：3 个默认上报周期（设计 22）
	unknownWithin = 120 * time.Second
	rawRetention  = 24 * time.Hour
	flushEvery    = 3 * time.Second
	maxReportSize = 64 << 10 // 上报体上限 64 KB，超过返回 413（设计 43.2）
)

// Options 是创建 Server 时的可选配置。
type Options struct {
	Logger    *slog.Logger // 为 nil 时使用 slog.Default()
	Version   string       // 由 git describe 注入，/healthz 与启动日志中显示（设计 40.3.2）
	PublicURL string       // 面板对外地址，写进安装命令；为空时由请求推断（设计 27.3.1）
}

type Server struct {
	store   *Store
	web     fs.FS
	log     *slog.Logger
	version string

	publicURL   string
	enrollLimit *enrollLimiter
	routeTable  []routeSpec // 已注册路由及其允许的主体，供权限矩阵测试枚举（设计 17.5）

	// mu 保护下面三个字段。持有时间很短（只做内存读写），持有期间不访问数据库，
	// flush 先在锁内取走 pending 再在锁外写库，因此不会因为慢查询阻塞上报。
	mu       sync.Mutex
	latest   map[int64]*snapshot           // 各节点最新上报，实时读取只走内存，不查数据库（设计 3.5）
	counters map[int64]map[string]*Counter // 各节点各网卡上一次的内核累计计数（设计 5.5）
	pending  []pendingWrite                // 等待批量写入的上报
}

type snapshot struct {
	ReceivedAt time.Time
	Report     protocol.Report
}

type pendingWrite struct {
	serverID int64
	at       time.Time
	rep      protocol.Report
	rx, tx   uint64 // traffic delta to add to today's bucket
	counters map[string]Counter
}

// New 创建面板服务：从数据库恢复各网卡的上一次计数，保证重启面板后流量增量连续（设计 5.5）。
func New(store *Store, web fs.FS, opts Options) (*Server, error) {
	c, err := store.LoadCounters()
	if err != nil {
		return nil, err
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Server{store: store, web: web, log: opts.Logger, version: opts.Version,
		publicURL: strings.TrimRight(opts.PublicURL, "/"), enrollLimit: newEnrollLimiter(),
		latest: map[int64]*snapshot{}, counters: c}, nil
}

// Run starts background loops and the HTTP server; blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context, listen string) error {
	go s.flushLoop(ctx)
	go s.retentionLoop(ctx)

	srv := &http.Server{
		Addr:              listen,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	s.log.Info("listening", "component", "http", "addr", listen)
	err := srv.ListenAndServe()
	s.flush() // persist what is buffered
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// access 是路由允许的主体（设计 17.1、17.2）。
type access string

const (
	accessPublic access = "public" // 无需凭证：健康检查、静态页面
	accessEnroll access = "enroll" // 凭请求体中的注册码认证，由处理函数校验并单独限流（设计 27.6）
	accessAdmin  access = "admin"  // Web 管理员
	accessAgent  access = "agent"  // Agent Token，只能操作 Token 绑定的节点
)

// routeSpec 记录一条路由及其允许的主体。
type routeSpec struct {
	pattern string
	access  access
}

// routes 注册全部路由，外层统一套上 middleware（request_id、panic 恢复、请求日志）。
//
// 【安全】默认拒绝（设计 17.5）：路由只能通过 handle 注册，且必须声明允许的主体；
// 鉴权在 handle 包装的统一中间件中完成，业务代码不自行判断凭证类型。
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	s.routeTable = nil
	handle := func(pattern string, acc access, h http.HandlerFunc) {
		var wrapped http.Handler
		switch acc {
		case accessPublic, accessEnroll:
			wrapped = h
		case accessAdmin:
			wrapped = s.admin(h)
		case accessAgent:
			wrapped = s.agent(h)
		default:
			panic("route " + pattern + ": unknown access " + string(acc)) // 未声明主体的路由在启动时直接失败
		}
		s.routeTable = append(s.routeTable, routeSpec{pattern, acc})
		mux.Handle(pattern, wrapped)
	}

	handle("GET /healthz", accessPublic, s.handleHealthz)

	// Agent（设计 19.10）
	handle("POST /api/v1/agent/enroll", accessEnroll, s.handleEnroll)
	handle("POST /api/v1/agent/report", accessAgent, s.handleReport)

	// 节点（设计 19.5、19.11）
	handle("GET /api/v1/servers", accessAdmin, s.handleListServers)
	handle("POST /api/v1/servers", accessAdmin, s.handleCreateServer)
	handle("GET /api/v1/servers/{id}/metrics", accessAdmin, s.handleMetrics)
	handle("GET /api/v1/servers/{id}/install-command", accessAdmin, s.handleInstallCommand)
	handle("POST /api/v1/servers/{id}/enroll-code", accessAdmin, s.handleRegenerateCode)
	handle("DELETE /api/v1/servers/{id}/enroll-code", accessAdmin, s.handleRevokeCode)

	// /api/ 下未定义的路径返回 JSON 404；否则会落到下面的 SPA 回退，返回 200 的 HTML
	handle("/api/", accessPublic, func(w http.ResponseWriter, r *http.Request) { s.writeError(w, r, errNotFound) })
	handle("/", accessPublic, s.webHandler().ServeHTTP)
	return s.middleware(mux)
}

// handleHealthz：GET /healthz，无需认证。返回版本号，随时可确认面板运行的是哪次提交（设计 40.3.2）。
// 只用于存活检查，不返回任何节点或配置信息。
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"status": "ok", "version": s.version})
}

// admin 用开发用 admin token 保护 Web / App 的读取接口。
// TODO(A2): 改为 Web 会话登录（设计 8.2）；TODO(C): App Device Token，只读范围（设计 12.5）。
//
// 【安全】查询凭证出错（如数据库故障）时按失败处理：返回 500 而不是放行，
// 也不伪装成 401，避免把故障误报为“Token 错误”（设计 43.1）。
func (s *Server) admin(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			s.writeError(w, r, errorf(CodeUnauthorized, ""))
			return
		}
		ok, err := s.store.ValidAdminToken(tok)
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		if !ok {
			s.writeError(w, r, errorf(CodeUnauthorized, ""))
			return
		}
		info(r).principal = "admin"
		h(w, r)
	})
}

// agent 用 Agent Token 认证，并把 Token 绑定的节点 ID 放进请求信息（设计 23.2）。
//
// 【安全】只按 Token 哈希查找节点，请求体中的任何节点标识都不可信（设计 1.6.6）；
// 查询出错时返回 500，不放行（设计 43.1）。
func (s *Server) agent(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sid, err := s.store.AgentServerID(bearer(r))
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		if sid == 0 {
			s.writeError(w, r, errorf(CodeUnauthorized, "Agent 凭证无效或已被吊销，请重新注册"))
			return
		}
		info(r).principal, info(r).principalID = "agent", sid
		h(w, r)
	})
}

// handleReport：POST /api/v1/agent/report，Agent Token 认证（设计 6.1）。
// 成功返回 204；Token 无效 401；请求体不是合法 JSON 400；超过 64 KB 413。
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	sid := info(r).principalID
	var rep protocol.Report
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReportSize)).Decode(&rep); err != nil {
		if ae := asAPIError(err); ae.Code == CodePayloadTooLarge {
			s.writeError(w, r, ae)
			return
		}
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	s.ingest(sid, rep, time.Now())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ingest(sid int64, rep protocol.Report, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.latest[sid] = &snapshot{ReceivedAt: now, Report: rep}

	if s.counters[sid] == nil {
		s.counters[sid] = map[string]*Counter{}
	}
	pw := pendingWrite{serverID: sid, at: now, rep: rep, counters: map[string]Counter{}}
	for _, ni := range rep.Network {
		cur := Counter{BootID: rep.System.BootID, IfIndex: ni.IfIndex, Rx: ni.RxBytes, Tx: ni.TxBytes}
		drx, dtx, reset := ComputeDelta(s.counters[sid][ni.Interface], cur)
		if reset {
			s.log.Info("traffic counter reset", "component", "traffic", "server_id", sid, "iface", ni.Interface,
				"boot_id_changed", s.counters[sid][ni.Interface] != nil && s.counters[sid][ni.Interface].BootID != cur.BootID)
		}
		pw.rx += drx
		pw.tx += dtx
		c := cur
		s.counters[sid][ni.Interface] = &c
		pw.counters[ni.Interface] = cur
	}
	s.pending = append(s.pending, pw)
}

func (s *Server) flushLoop(ctx context.Context) {
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.flush()
		}
	}
}

// flush writes buffered reports in a single transaction (design 3.5 write strategy).
func (s *Server) flush() {
	s.mu.Lock()
	batch := s.pending
	s.pending = nil
	s.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	tx, err := s.store.DB.Begin()
	if err != nil {
		s.log.Error("flush begin failed", "component", "store", "err", err)
		return
	}
	defer tx.Rollback()
	for _, p := range batch {
		rep := p.rep
		var diskUsed, diskTotal, rxs, txs uint64
		for _, d := range rep.Disk {
			if d.Mount == "/" {
				diskUsed, diskTotal = d.Used, d.Total
			}
		}
		for _, n := range rep.Network {
			rxs += n.RxSpeed
			txs += n.TxSpeed
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO metrics_raw
			(server_id, ts, cpu, load1, mem_used, mem_total, swap_used, disk_used, disk_total, rx_speed, tx_speed)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			p.serverID, p.at.Unix(), rep.CPU.Usage, rep.CPU.Load1, rep.Memory.Used, rep.Memory.Total,
			rep.Swap.Used, diskUsed, diskTotal, rxs, txs); err != nil {
			s.log.Error("flush metrics failed", "component", "store", "err", err)
			return
		}
		if p.rx > 0 || p.tx > 0 {
			// 按面板本地时区划分日期。TODO(A4): 按节点设置计费时区（设计 1.2.4）。
			if _, err := tx.Exec(`INSERT INTO traffic_daily (server_id, day, rx, tx) VALUES (?,?,?,?)
				ON CONFLICT(server_id, day) DO UPDATE SET rx = rx + excluded.rx, tx = tx + excluded.tx`,
				p.serverID, p.at.Format("2006-01-02"), p.rx, p.tx); err != nil {
				s.log.Error("flush traffic failed", "component", "store", "err", err)
				return
			}
		}
		for iface, c := range p.counters {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO traffic_counters (server_id, iface, boot_id, ifindex, rx, tx)
				VALUES (?,?,?,?,?,?)`, p.serverID, iface, c.BootID, c.IfIndex, c.Rx, c.Tx); err != nil {
				s.log.Error("flush counters failed", "component", "store", "err", err)
				return
			}
		}
		if _, err := tx.Exec(`UPDATE servers SET last_seen_at = ? WHERE id = ?`, p.at.Unix(), p.serverID); err != nil {
			s.log.Error("flush last_seen failed", "component", "store", "err", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.log.Error("flush commit failed", "component", "store", "err", err)
	}
}

// retentionLoop deletes expired raw points.
// TODO(A3): 删除前先降采样到 metrics_1m / 5m / 1h（设计 21）。
func (s *Server) retentionLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cutoff := time.Now().Add(-rawRetention).Unix()
			if _, err := s.store.DB.Exec(`DELETE FROM metrics_raw WHERE ts < ?`, cutoff); err != nil {
				s.log.Error("retention failed", "component", "store", "err", err)
			}
		}
	}
}

type serverView struct {
	ServerRow
	Status  string           `json:"status"` // online / unknown / offline / pending（设计 22、27.7）
	Latest  *protocol.Report `json:"latest,omitempty"`
	Traffic trafficView      `json:"traffic"`
}

type trafficView struct {
	CycleStart string `json:"cycle_start"`
	Rx         uint64 `json:"rx"`
	Tx         uint64 `json:"tx"`
	Used       uint64 `json:"used"`  // after applying count mode
	Limit      int64  `json:"limit"` // 0 = unlimited
}

// handleListServers：GET /api/v1/servers，admin 认证。实时状态取自内存，不读指标表（设计 3.5）。
// TODO(A0): 列表改为 {"items", "next_cursor"} 格式（设计 19.0.2），与 Web、App 同步修改。
func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListServers()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	now := time.Now()
	out := make([]serverView, 0, len(rows))
	for _, row := range rows {
		v := serverView{ServerRow: row, Status: "offline"}
		if row.EnrollState == enrollPending {
			v.Status = "pending" // 待安装：单独显示，不参与在线判断，不触发离线告警（设计 27.7）
		}
		s.mu.Lock()
		if snap := s.latest[row.ID]; snap != nil {
			rep := snap.Report
			v.Latest = &rep
			v.LastSeenAt = snap.ReceivedAt.Unix()
		}
		s.mu.Unlock()
		if v.LastSeenAt > 0 && row.EnrollState != enrollPending {
			age := now.Sub(time.Unix(v.LastSeenAt, 0))
			switch {
			case age <= onlineWithin:
				v.Status = "online"
			case age <= unknownWithin:
				v.Status = "unknown"
			}
		}
		start := CycleStart(now, row.ResetDay)
		rx, tx, _ := s.store.TrafficSince(row.ID, start)
		v.Traffic = trafficView{CycleStart: start.Format("2006-01-02"), Rx: rx, Tx: tx,
			Used: CountedBytes(row.CountMode, rx, tx), Limit: row.LimitBytes}
		out = append(out, v)
	}
	writeJSON(w, out)
}

// handleMetrics：GET /api/v1/servers/{id}/metrics?range=1h，admin 认证。range 非法时按 1 小时处理。
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.writeError(w, r, errorf(CodeBadRequest, "节点编号格式不正确"))
		return
	}
	rng, err := time.ParseDuration(r.URL.Query().Get("range"))
	if err != nil || rng <= 0 || rng > rawRetention {
		rng = time.Hour
	}
	pts, err := s.store.Metrics(id, time.Now().Add(-rng))
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, pts)
}

func (s *Server) webHandler() http.Handler {
	if _, err := fs.Stat(s.web, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(`<p>Web 尚未构建。开发时运行 <code>make dev-web</code> 打开 http://localhost:5173 ，发布前运行 <code>make web</code>。</p>`))
		})
	}
	files := http.FileServerFS(s.web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// SPA fallback: unknown paths serve index.html
		if r.URL.Path != "/" {
			if _, err := fs.Stat(s.web, r.URL.Path[1:]); err != nil {
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}
