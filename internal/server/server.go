package server

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"vpsmon/internal/protocol"
	"vpsmon/internal/release"
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
	// NoLoginCaptcha 关闭登录滑动验证码（默认开启，设计 17.4）；仅用于需要脚本登录的场景
	NoLoginCaptcha bool
	// NoReleaseSync 关闭自动同步官方 Agent 版本（离线 / 内网环境；仍可在 Web 中手动同步，设计 29.1）
	NoReleaseSync bool
	// 面板镜像（设计 27.5.3）：MirrorDir 为镜像目录（数据目录下的 releases/）；ReleaseMirror 为真时同步官方版本后一并镜像全部文件
	MirrorDir     string
	ReleaseMirror bool
}

type Server struct {
	store   *Store
	web     fs.FS
	log     *slog.Logger
	version string

	publicURL   string
	enrollLimit *enrollLimiter
	loginLimit  *enrollLimiter // 登录与重新验证：同一 IP 1 分钟失败 5 次锁定 15 分钟（设计 17.4）
	captcha     *captchaStore  // 登录滑动验证码；为 nil 表示已关闭
	alerts      *alertEngine   // 告警引擎（设计 16.7）

	// 官方版本同步（设计 29.1）：发布地址与验签公钥默认为官方值，测试中替换
	releaseBase   string
	releaseKeys   []release.PublicKey
	releaseHTTP   *http.Client
	noReleaseSync bool
	mirrorRoot    string       // 镜像目录；为空表示不提供镜像
	releaseMirror bool         // 同步后自动镜像
	mirrorHTTP    *http.Client // 下载构建用，超时更长
	mirrorCache   mirrorCache
	notify        *notifier     // 告警通知（设计 16.5）
	noise         *alertNoise   // 告警降噪：抖动、批量离线合并、面板自检（设计 16.4）
	quiet         *quietState   // 免打扰时段（设计 16.5）
	routeTable    []routeSpec   // 已注册路由及其允许的主体，供权限矩阵测试枚举（设计 17.5）
	ws            *wsHub        // WebSocket 事件推送（设计 20）
	cloud         *cloudState   // 云厂商账户同步（设计 44）
	apiKeys       apiKeyLimiter // 只读 API Key 的限流（设计 45.2）

	// mu 保护下面三个字段。持有时间很短（只做内存读写），持有期间不访问数据库，
	// flush 先在锁内取走 pending 再在锁外写库，因此不会因为慢查询阻塞上报。
	mu        sync.Mutex
	latest    map[int64]*snapshot           // 各节点最新上报，实时读取只走内存，不查数据库（设计 3.5）
	counters  map[int64]map[string]*Counter // 各节点各网卡上一次的内核累计计数（设计 5.5）
	nodeConfs map[int64]nodeConf            // 各节点的采样间隔与计费时区缓存，避免每份上报都查库；节点修改后清除（interval.go）
	traffic   trafficCache                  // 本周期流量的短期缓存（traffic_cache.go）
	watched   map[int64]time.Time           // 按需实时模式：正在被查看的节点及其到期时间（设计 46.2，interval.go）
	pending   []pendingWrite                // 等待批量写入的上报
}

type snapshot struct {
	ReceivedAt time.Time       // 最近一次收到上报的时间，决定在线状态（设计 22）
	At         time.Time       // Report 的采集时间
	Report     protocol.Report // 采集时间最新的一份上报
	// ClockSkew 是最近一次上报测得的 Agent 时钟偏差（秒，正数为 Agent 偏快）；无法测量时为 nil（设计 16.1、43.5）
	ClockSkew *float64
}

type pendingWrite struct {
	serverID int64
	at       time.Time // 指标点时间：Agent 的采集时间（补发的缓存数据）或收到时间
	seen     time.Time // 收到时间，写入 last_seen_at
	rep      protocol.Report
	rx, tx   uint64 // traffic delta to add to today's bucket
	counters map[string]Counter
	// trafficOnly：超过补发期限的旧上报只计流量，不写历史指标点（设计 1.6.14）
	trafficOnly bool
	loc         *time.Location // 节点的计费时区，决定流量归入哪一天（设计 5.4）
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
	alerts, err := newAlertEngine(store, time.Now())
	if err != nil {
		return nil, err
	}
	s := &Server{alerts: alerts, releaseBase: OfficialReleases, releaseKeys: release.TrustedKeys(),
		releaseHTTP: &http.Client{Timeout: 30 * time.Second}, noReleaseSync: opts.NoReleaseSync,
		mirrorRoot: opts.MirrorDir, releaseMirror: opts.ReleaseMirror && opts.MirrorDir != "", mirrorHTTP: &http.Client{Timeout: 10 * time.Minute},
		store: store, web: web, log: opts.Logger, version: opts.Version,
		publicURL: strings.TrimRight(opts.PublicURL, "/"), enrollLimit: newEnrollLimiter(),
		loginLimit: &enrollLimiter{perMinute: 20, maxFails: 5, failWindow: time.Minute, ban: 15 * time.Minute,
			now: time.Now, ips: map[string]*ipState{}},
		latest: map[int64]*snapshot{}, counters: c, nodeConfs: map[int64]nodeConf{}, watched: map[int64]time.Time{}, captcha: captchaFor(opts), ws: newWSHub(), cloud: newCloudState()}
	s.notify, s.noise = newNotifier(s), newAlertNoise()
	if s.quiet, err = newQuietState(store); err != nil {
		return nil, err
	}
	return s, nil
}

// Run starts background loops and the HTTP server; blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context, listen string) error {
	s.startBackground(ctx)
	srv := &http.Server{
		Addr:              listen,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		s.log.Info("listening", "component", "http", "addr", listen)
		errc <- srv.ListenAndServe()
	}()
	return s.serveUntil(ctx, errc, srv)
}

// startBackground 启动批量写入、维护、告警与版本同步等后台任务。
func (s *Server) startBackground(ctx context.Context) {
	go s.flushLoop(ctx)
	go s.runTask(ctx, "maintenance", s.maintenance)
	go s.runTask(ctx, "alerts", s.alertLoop)
	go s.runTask(ctx, "cloud", s.cloudLoop)
	go s.runTask(ctx, "ws-status", s.statusLoop)
	if !s.noReleaseSync {
		go s.runTask(ctx, "release-sync", s.releaseSyncLoop)
	}
}

// access 是路由允许的主体（设计 17.1、17.2）。
type access string

const (
	accessPublic access = "public" // 无需凭证：健康检查、静态页面
	accessEnroll access = "enroll" // 凭请求体中的注册码认证，由处理函数校验并单独限流（设计 27.6）
	accessAdmin  access = "admin"  // Web 管理员
	accessRead   access = "read"   // Web 管理员或只读 API Key（设计 45.2）：只用于读取类接口
	accessAgent  access = "agent"  // Agent Token，只能操作 Token 绑定的节点
)

// routeSpec 记录一条路由及其允许的主体。
type routeSpec struct {
	pattern string
	access  access
}

func captchaFor(o Options) *captchaStore {
	if o.NoLoginCaptcha {
		return nil
	}
	return newCaptchaStore()
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
		case accessRead:
			wrapped = s.read(h)
		case accessAgent:
			wrapped = s.agent(h)
		default:
			panic("route " + pattern + ": unknown access " + string(acc)) // 未声明主体的路由在启动时直接失败
		}
		s.routeTable = append(s.routeTable, routeSpec{pattern, acc})
		mux.Handle(pattern, wrapped)
	}

	handle("GET /healthz", accessPublic, s.handleHealthz)
	// 面板镜像：主机下载安装脚本与构建（设计 27.5.3）。内容为官方签名的公开发布文件，无需凭证
	handle("GET /releases/{version}/{file}", accessPublic, s.handleMirror)

	// Web 登录（设计 19.1）。登录本身无需认证，单独限流
	handle("GET /api/v1/auth/captcha", accessPublic, s.handleCaptcha)
	handle("POST /api/v1/auth/login", accessPublic, s.handleLogin)
	handle("GET /api/v1/auth/me", accessAdmin, s.handleMe)
	handle("POST /api/v1/auth/logout", accessAdmin, s.handleLogout)
	handle("POST /api/v1/auth/password", accessAdmin, s.handleChangePassword)
	handle("POST /api/v1/auth/reauth", accessAdmin, s.handleReauth)
	handle("GET /api/v1/auth/sessions", accessAdmin, s.handleSessions)
	handle("DELETE /api/v1/auth/sessions/{id}", accessAdmin, s.handleRevokeSession)
	// 云厂商账户（设计 44.8）：添加、更换凭证、删除需重新验证密码（在处理函数中校验）
	handle("GET /api/v1/cloud-accounts", accessAdmin, s.handleCloudAccounts)
	handle("POST /api/v1/cloud-accounts", accessAdmin, s.handleCreateCloudAccount)
	handle("PUT /api/v1/cloud-accounts/{id}", accessAdmin, s.handleUpdateCloudAccount)
	handle("DELETE /api/v1/cloud-accounts/{id}", accessAdmin, s.handleDeleteCloudAccount)
	handle("POST /api/v1/cloud-accounts/{id}/sync", accessAdmin, s.handleSyncCloudAccount)
	handle("GET /api/v1/cloud-accounts/{id}/costs", accessAdmin, s.handleCloudCosts)
	handle("GET /api/v1/cloud-instances", accessAdmin, s.handleCloudInstances)
	handle("PUT /api/v1/cloud-instances/{id}/server", accessAdmin, s.handleLinkCloudInstance)
	handle("POST /api/v1/cloud-instances/{id}/apply-expire", accessAdmin, s.handleApplyCloudInstance)
	// 只读 API Key 的管理（设计 45.2）：只有 Web 管理员可以创建、查看、吊销；创建需重新验证密码
	handle("GET /api/v1/api-keys", accessAdmin, s.handleAPIKeys)
	handle("POST /api/v1/api-keys", accessAdmin, s.handleCreateAPIKey)
	handle("DELETE /api/v1/api-keys/{id}", accessAdmin, s.handleRevokeAPIKey)
	handle("GET /api/v1/version", accessRead, s.handleVersion)
	// 实时事件（设计 20）：只推送，不接收指令
	handle("GET /ws", accessRead, s.handleWS)

	// Agent（设计 19.10）
	handle("POST /api/v1/agent/enroll", accessEnroll, s.handleEnroll)
	handle("POST /api/v1/agent/report", accessAgent, s.handleReport)
	handle("GET /api/v1/agent/upgrade", accessAgent, s.handleAgentUpgrade)
	handle("POST /api/v1/agent/upgrade/status", accessAgent, s.handleAgentUpgradeStatus)
	handle("POST /api/v1/agent/unregister", accessAgent, s.handleUnregister) // 注销会吊销 Token，放在 Agent 路由最后（权限矩阵测试按顺序调用）

	// 节点（设计 19.5、19.11）
	handle("GET /api/v1/audit-logs", accessAdmin, s.handleAuditLogs)
	handle("GET /api/v1/audit-logs/export", accessAdmin, s.handleAuditExport)
	handle("GET /api/v1/alerts", accessRead, s.handleAlerts)
	handle("GET /api/v1/alert-rules", accessAdmin, s.handleAlertRules)
	handle("POST /api/v1/alert-rules", accessAdmin, s.handleCreateAlertRule)
	handle("POST /api/v1/alert-rules/preview", accessAdmin, s.handlePreviewAlertRule)
	handle("PUT /api/v1/alert-rules/{id}", accessAdmin, s.handleUpdateAlertRule)
	handle("DELETE /api/v1/alert-rules/{id}", accessAdmin, s.handleDeleteAlertRule)
	handle("GET /api/v1/agent-releases", accessAdmin, s.handleReleases)
	handle("GET /api/v1/upgrade-tasks", accessAdmin, s.handleUpgradeTasks)
	handle("POST /api/v1/upgrade-tasks", accessAdmin, s.handleCreateUpgradeTasks)
	handle("POST /api/v1/upgrade-tasks/{id}/cancel", accessAdmin, s.handleCancelUpgradeTask)
	handle("POST /api/v1/agent-releases/sync", accessAdmin, s.handleSyncReleases)
	handle("GET /api/v1/settings/quiet-hours", accessAdmin, s.handleGetQuietHours)
	handle("PUT /api/v1/settings/quiet-hours", accessAdmin, s.handlePutQuietHours)
	handle("GET /api/v1/notification-channels", accessAdmin, s.handleChannels)
	handle("POST /api/v1/notification-channels", accessAdmin, s.handleCreateChannel)
	handle("PUT /api/v1/notification-channels/{id}", accessAdmin, s.handleUpdateChannel)
	handle("DELETE /api/v1/notification-channels/{id}", accessAdmin, s.handleDeleteChannel)
	handle("POST /api/v1/notification-channels/{id}/test", accessAdmin, s.handleTestChannel)
	handle("GET /api/v1/notification-deliveries", accessAdmin, s.handleDeliveries)
	handle("GET /api/v1/silences", accessAdmin, s.handleSilences)
	handle("POST /api/v1/silences", accessAdmin, s.handleCreateSilence)
	handle("DELETE /api/v1/silences/{id}", accessAdmin, s.handleEndSilence)
	handle("GET /api/v1/servers", accessRead, s.handleListServers)
	handle("POST /api/v1/servers", accessAdmin, s.handleCreateServer)
	handle("GET /api/v1/servers/{id}", accessRead, s.handleGetServer)
	handle("PUT /api/v1/servers/{id}", accessAdmin, s.handleUpdateServer)
	handle("DELETE /api/v1/servers/{id}", accessAdmin, s.handleDeleteServer)
	handle("POST /api/v1/servers/{id}/revoke-agent-token", accessAdmin, s.handleRevokeAgentToken)
	handle("GET /api/v1/servers/{id}/metrics/history", accessRead, s.handleHistory)
	handle("GET /api/v1/servers/{id}/install-command", accessAdmin, s.handleInstallCommand)
	handle("POST /api/v1/servers/{id}/enroll-code", accessAdmin, s.handleRegenerateCode)
	handle("DELETE /api/v1/servers/{id}/enroll-code", accessAdmin, s.handleRevokeCode)
	handle("GET /api/v1/servers/{id}/traffic/current", accessRead, s.handleTrafficCurrent)
	handle("GET /api/v1/servers/{id}/traffic/daily", accessRead, s.handleTrafficDaily)
	handle("GET /api/v1/servers/{id}/traffic/monthly", accessRead, s.handleTrafficMonthly)
	handle("GET /api/v1/servers/{id}/traffic/adjustments", accessAdmin, s.handleAdjustments)
	handle("POST /api/v1/servers/{id}/traffic/calibrate", accessAdmin, s.handleCalibrate)

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
// 成功返回 204；Token 无效 401；请求体不是合法 JSON 400；超过 64 KB（压缩或解压后）413；不支持的 Content-Encoding 415。
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	sid := info(r).principalID
	// 声明接受 gzip 压缩的上报（RFC 7694）：新版 Agent 看到后才压缩，旧版面板不声明，新旧组合都兼容（设计 6.1）
	w.Header().Set("Accept-Encoding", "gzip")
	conf, confOK := s.nodeConfOf(sid)
	s.setIntervalHeader(w, sid, conf, confOK)
	body, err := reportBody(w, r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	defer body.Close()
	var rep protocol.Report
	if err := json.NewDecoder(body).Decode(&rep); err != nil {
		if ae := asAPIError(err); ae.Code == CodePayloadTooLarge {
			s.writeError(w, r, ae)
			return
		}
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	now := time.Now()
	at, _, old := pointTime(rep, now)
	s.ingest(sid, rep, at, now, old, conf.loc)
	// 时钟偏差由告警引擎按“Agent 时钟偏差”规则评估（设计 16.1），这里只记录测量值
	if skew, ok := clockSkew(rep, now); ok {
		s.mu.Lock()
		if sn := s.latest[sid]; sn != nil {
			sn.ClockSkew = &skew
		}
		s.mu.Unlock()
	}
	w.WriteHeader(http.StatusNoContent)
	// 实时事件（设计 45.2）：补发的旧数据不改变实时状态，不推送
	if !old {
		s.publishMetrics(sid, now)
	}
}

// reportBody 返回上报正文的读取器：按 Content-Encoding 解压，原始与解压后的大小都受 maxReportSize 限制，
// 压缩炸弹（很小的 gzip 解压出巨大内容）同样返回 413（设计 43.2）。不认识的编码返回 415。
func reportBody(w http.ResponseWriter, r *http.Request) (io.ReadCloser, error) {
	raw := http.MaxBytesReader(w, r.Body, maxReportSize)
	switch enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); enc {
	case "", "identity":
		return raw, nil
	case "gzip":
		zr, err := gzip.NewReader(raw)
		if err != nil {
			if ae := asAPIError(err); ae.Code == CodePayloadTooLarge {
				return nil, ae
			}
			return nil, &APIError{Code: CodeBadRequest, Cause: err}
		}
		return struct {
			io.Reader
			io.Closer
		}{&limitedReader{r: zr, n: maxReportSize}, zr}, nil
	default:
		return nil, errorf(CodeUnsupportedEncoding, "不支持的内容编码："+enc)
	}
}

// limitedReader 在读取超过 n 字节时返回 413 错误，而不是像 io.LimitReader 那样静默截断。
type limitedReader struct {
	r io.Reader
	n int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, &APIError{Code: CodePayloadTooLarge}
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}

const (
	maxBackfill = time.Hour        // 接受补发的最早时间：Agent 断网缓存最多保留 30 分钟，留出余量
	maxSkew     = 60 * time.Second // Agent 时钟超前超过此值视为时钟偏差（设计 43.5）
)

// pointTime 决定指标点的时间（设计 1.6.14 时间戳校验）。
//
// Agent 断网恢复后会补发缓存的上报，必须按采集时间入库，否则历史曲线会把这段数据堆在“现在”。
// 采集时间在 [now-1h, now+60s] 内时采用；超前过多说明 Agent 时钟偏差，使用收到时间，并返回 skewed=true。
// 早于 1 小时的返回 old=true（时间用收到时间）：这类上报来自 Agent 落盘保留的、重启前最后一份计数，
// 只用于补齐流量，不能当作实时状态或历史指标点（设计 1.6.14、5.5）。旧版 Agent 的 timestamp 就是发送时间，行为不变。
func pointTime(rep protocol.Report, now time.Time) (at time.Time, skewed, old bool) {
	if rep.Timestamp == 0 {
		return now, false, false
	}
	t := time.Unix(rep.Timestamp, 0)
	switch {
	case t.After(now.Add(maxSkew)):
		return now, true, false
	case t.Before(now.Add(-maxBackfill)):
		return now, false, true
	}
	return t, false, false
}

// clockSkew 计算 Agent 时钟相对面板的偏差（秒，正数为 Agent 偏快；设计 43.5）。
//
// 新版 Agent 带 sent_at（发送时刻），偏差 = sent_at − 收到时间，快慢都能识别，补发的旧数据也能测；
// 网络传输的延迟只有亚秒级，相对 60 秒的阈值可以忽略。旧版 Agent 只有采集时间，
// 只能识别“采集时间超前收到时间”的偏快情况；偏慢与断网补发无法区分，返回 ok=false。
func clockSkew(rep protocol.Report, now time.Time) (float64, bool) {
	if rep.SentAt > 0 {
		return float64(rep.SentAt - now.Unix()), true
	}
	if rep.Timestamp > now.Unix() {
		return float64(rep.Timestamp - now.Unix()), true
	}
	return 0, false
}

// ingest 把一份上报写入内存状态并排队等待批量写库。at 为指标点时间，now 为收到时间。
// 补发的旧数据不会覆盖更新的实时状态；同一时间点重复上报在写库时覆盖（主键去重），
// 流量增量为 0，不会重复计算。trafficOnly 的上报只更新网卡计数与流量，不改实时状态、不写指标点。
func (s *Server) ingest(sid int64, rep protocol.Report, at, now time.Time, trafficOnly bool, loc *time.Location) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := s.latest[sid]
	stale := snap != nil && at.Before(snap.At)
	if trafficOnly {
		if snap != nil {
			snap.ReceivedAt = now // 收到上报说明 Agent 在线
		}
	} else if !stale {
		ns := &snapshot{ReceivedAt: now, At: at, Report: rep}
		if snap != nil {
			ns.ClockSkew = snap.ClockSkew // 由 handleReport 随后更新；旧版 Agent 测不到时沿用上次的值
			// 扩展指标（含虚拟化类型）每分钟才上报一次：其余上报沿用上一次的值，详情页始终能显示（设计 46.3）
			if ns.Report.Extra == nil {
				ns.Report.Extra = snap.Report.Extra
			}
		}
		s.latest[sid] = ns
	} else {
		snap.ReceivedAt = now
	}

	if s.counters[sid] == nil {
		s.counters[sid] = map[string]*Counter{}
	}
	pw := pendingWrite{serverID: sid, at: at, seen: now, rep: rep, counters: map[string]Counter{}, trafficOnly: trafficOnly, loc: loc}
	// 乱序补发仍写入历史指标，但不能回退累计计数基线，否则后续上报会重复计费。
	for _, ni := range rep.Network {
		if stale {
			break
		}
		cur := Counter{BootID: rep.System.BootID, IfIndex: ni.IfIndex, Rx: ni.RxBytes, Tx: ni.TxBytes, Bits: rep.System.CounterBits}
		prev := s.counters[sid][ni.Interface]
		if trafficOnly && !continuesCounter(prev, cur) {
			// 重启前最后一份计数只用于补齐同一次启动内的缺口；不连续时宁可少算，也不能回退基线或整笔重复计入
			continue
		}
		drx, dtx, reset := ComputeDelta(prev, cur)
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

// continuesCounter 判断 cur 是否是 prev 在同一次启动、同一块网卡上的后续计数（递增或 32 位回绕）。
func continuesCounter(prev *Counter, cur Counter) bool {
	if prev == nil || prev.BootID != cur.BootID || prev.IfIndex != cur.IfIndex {
		return false
	}
	_, okRx := protocol.CounterDelta(prev.Rx, cur.Rx, cur.Bits)
	_, okTx := protocol.CounterDelta(prev.Tx, cur.Tx, cur.Bits)
	return okRx && okTx
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
	committed := false
	defer func() {
		if !committed {
			// 写库失败时保留原批次，并排在并发到达的新上报之前，确保计数与指标按序落盘。
			s.mu.Lock()
			s.pending = append(batch, s.pending...)
			s.mu.Unlock()
		}
	}()
	tx, err := s.store.DB.Begin()
	if err != nil {
		s.log.Error("flush begin failed", "component", "store", "err", err)
		return
	}
	defer tx.Rollback()
	for _, p := range batch {
		// 超过补发期限的旧上报只计流量，不写历史指标点（设计 1.6.14）
		if !p.trafficOnly {
			rep := p.rep
			var diskUsed, diskTotal, rxs, txs, ioRead, ioWrite uint64
			for _, d := range rep.Disk {
				if d.Mount == "/" {
					diskUsed, diskTotal = d.Used, d.Total
				}
			}
			for _, n := range rep.Network {
				rxs += n.RxSpeed
				txs += n.TxSpeed
			}
			// 磁盘读写速率为所有磁盘之和；旧版 Agent 不上报 IO，写入 NULL 而不是 0，避免把“没有数据”画成“空闲”
			var ioR, ioW any
			if len(rep.DiskIO) > 0 {
				for _, d := range rep.DiskIO {
					ioRead += d.ReadSpeed
					ioWrite += d.WriteSpeed
				}
				ioR, ioW = ioRead, ioWrite
			}
			// steal / iowait 与 TCP 连接数：旧版 Agent 不上报（或首次采样没有占比）时写入 NULL（设计 4.4、4.9）
			var steal, iowait, tcp any
			if b := rep.CPU.Breakdown; b != nil {
				steal, iowait = b.Steal, b.IOWait
			}
			if rep.Conns != nil {
				tcp = rep.Conns.TCP
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO metrics_raw
				(server_id, ts, cpu, load1, mem_used, mem_total, swap_used, disk_used, disk_total, rx_speed, tx_speed,
				disk_read, disk_write, steal, iowait, tcp)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				p.serverID, p.at.Unix(), rep.CPU.Usage, rep.CPU.Load1, rep.Memory.Used, rep.Memory.Total,
				rep.Swap.Used, diskUsed, diskTotal, rxs, txs, ioR, ioW, steal, iowait, tcp); err != nil {
				s.log.Error("flush metrics failed", "component", "store", "err", err)
				return
			}
		}
		if p.rx > 0 || p.tx > 0 {
			// 按节点的计费时区划分日期（设计 5.4）；未设置时为面板本地时区
			day := p.at
			if p.loc != nil {
				day = day.In(p.loc)
			}
			if _, err := tx.Exec(`INSERT INTO traffic_daily (server_id, day, rx, tx) VALUES (?,?,?,?)
				ON CONFLICT(server_id, day) DO UPDATE SET rx = rx + excluded.rx, tx = tx + excluded.tx`,
				p.serverID, day.Format("2006-01-02"), p.rx, p.tx); err != nil {
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
		if _, err := tx.Exec(`UPDATE servers SET last_seen_at = MAX(last_seen_at, ?) WHERE id = ?`, p.seen.Unix(), p.serverID); err != nil {
			s.log.Error("flush last_seen failed", "component", "store", "err", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.log.Error("flush commit failed", "component", "store", "err", err)
		return
	}
	committed = true
}

// runTask 运行后台任务：捕获 panic 并记录堆栈，按 1 秒、2 秒、4 秒…最长 1 分钟的间隔自动重启（设计 43.3.1）。
// 任务正常返回（ctx 取消）时结束。TODO(A5): 连续失败时触发面板自身告警（设计 16.1）。
func (s *Server) runTask(ctx context.Context, name string, fn func(context.Context)) {
	backoff := time.Second
	for {
		done := func() (finished bool) {
			defer func() {
				if v := recover(); v != nil {
					s.log.Error("background task panicked", "component", "task", "task", name,
						"panic", v, "stack", string(debug.Stack()))
				}
			}()
			fn(ctx)
			return true
		}()
		if done || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// maintenance 每分钟逐级降采样；每 10 分钟清理过期数据；每小时归还磁盘空间（设计 21）。
func (s *Server) maintenance(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for tick := 0; ; tick++ {
		now := time.Now()
		if n, err := s.store.Downsample(now); err != nil {
			s.log.Error("downsample failed", "component", "store", "err", err)
		} else {
			s.log.Debug("downsampled", "component", "store", "1m", n["metrics_1m"], "5m", n["metrics_5m"], "1h", n["metrics_1h"])
		}
		if tick%10 == 0 {
			if err := s.store.PruneSessions(now); err != nil {
				s.log.Error("session prune failed", "component", "auth", "err", err)
			}
			if n, err := s.store.ExpireUpgradeTasks(now); err != nil {
				s.log.Error("upgrade task expiry failed", "component", "upgrade", "err", err)
			} else if n > 0 {
				s.log.Info("upgrade tasks timed out", "component", "upgrade", "tasks", n)
			}
			if n, err := s.store.PruneAlertEvents(now); err != nil {
				s.log.Error("alert prune failed", "component", "alert", "err", err)
			} else if n > 0 {
				s.log.Info("expired alert events deleted", "component", "alert", "rows", n)
			}
			if n, err := s.store.PruneDeliveries(now); err != nil {
				s.log.Error("delivery prune failed", "component", "notify", "err", err)
			} else if n > 0 {
				s.log.Info("expired notification deliveries deleted", "component", "notify", "rows", n)
			}
			if n, err := s.store.PruneAudit(now); err != nil {
				s.log.Error("audit prune failed", "component", "audit", "err", err)
			} else if n > 0 {
				s.log.Info("expired audit logs deleted", "component", "audit", "rows", n)
			}
			if n, err := s.store.PruneExpired(now); err != nil {
				s.log.Error("retention failed", "component", "store", "err", err)
			} else if n > 0 {
				s.log.Info("expired metrics deleted", "component", "store", "rows", n)
			}
		}
		if tick%60 == 0 {
			if err := s.store.Vacuum(); err != nil {
				s.log.Error("incremental vacuum failed", "component", "store", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type serverView struct {
	ServerRow
	Status  string           `json:"status"` // online / unknown / offline / pending（设计 22、27.7）
	Latest  *protocol.Report `json:"latest,omitempty"`
	Traffic trafficView      `json:"traffic"`
	Alerts  []alertBrief     `json:"alerts"` // 活动告警，严重在前；“需要关注”据此判断（设计 9、16）
	// 生效中的维护与节点级静音（节点、分组或全部）；没有时省略（设计 16.6）
	Maintenance *Silence `json:"maintenance,omitempty"`
	Muted       *Silence `json:"muted,omitempty"`
}

// handleListServers：GET /api/v1/servers，admin 认证。实时状态取自内存，不读指标表（设计 3.5）。
// 返回 {"items", "next_cursor"}（设计 19.0.2）；一次返回全部节点（总览的统计与筛选需要全量），next_cursor 恒为空。
func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListServers()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	// API Key 只看到其范围内的节点（设计 45.2）
	rows = slices.DeleteFunc(rows, func(row ServerRow) bool { return !scopeAllows(r, row) })
	now := time.Now()
	out := make([]serverView, 0, len(rows))
	for _, row := range rows {
		v, err := s.viewOf(row, now)
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		// 列表每 3 秒轮询一次：每核使用率、监听端口与扩展指标只在详情页使用，列表中省略以减小响应
		out = append(out, listView(v))
	}
	writeList(w, out, "", nil)
}

// viewOf 组装一个节点的展示数据：持久化信息 + 内存中的实时状态 + 本周期流量。
func (s *Server) viewOf(row ServerRow, now time.Time) (serverView, error) {
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
		online, unknown := statusWindows(reportInterval(row)) // 按节点的采样间隔放宽（设计 22）
		switch {
		case age <= online:
			v.Status = "online"
		case age <= unknown:
			v.Status = "unknown"
		}
	}
	t, err := s.trafficCached(row, now)
	if err != nil {
		return v, err
	}
	v.Traffic = t
	v.Alerts = s.alerts.firingFor(row)
	v.Maintenance = s.alerts.silenceFor(row, SilenceMaintenance, "", now)
	v.Muted = s.alerts.silenceFor(row, SilenceMute, "", now)
	return v, nil
}

// historyRanges：可选的时间范围与对应的粒度表，点数都在 2200 以内，便于图表直接绘制（设计 19.7、41.5）。
var historyRanges = map[string]struct {
	d     time.Duration
	table string
	step  int64
}{
	"1h":  {time.Hour, "metrics_raw", 10},
	"6h":  {6 * time.Hour, "metrics_raw", 10},
	"24h": {24 * time.Hour, "metrics_1m", 60},
	"7d":  {7 * 24 * time.Hour, "metrics_5m", 300},
	"30d": {30 * 24 * time.Hour, "metrics_1h", 3600},
}

// historyView 是历史指标响应：{"range", "resolution", "items"}（列表格式见设计 19.0.2）。
type historyView struct {
	Range      string        `json:"range"`
	Resolution int64         `json:"resolution"` // 点的间隔，秒
	Items      []MetricPoint `json:"items"`
	NextCursor string        `json:"next_cursor"` // 列表约定（设计 19.0.2）；按时间范围一次返回，恒为空
}

// handleHistory：GET /api/v1/servers/{id}/metrics/history?range=1h，admin（设计 19.7）。
// range 为 1h / 6h / 24h / 7d / 30d，默认 1h；其他值返回 422。
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !s.requireServerInScope(w, r, id) {
		return
	}
	name := r.URL.Query().Get("range")
	if name == "" {
		name = "1h"
	}
	rg, ok := historyRanges[name]
	if !ok {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed,
			Details: []FieldError{{Field: "range", Message: "时间范围只能是 1h、6h、24h、7d 或 30d"}}})
		return
	}
	if _, err := s.store.GetServer(id); errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	} else if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	pts, err := s.store.MetricsHistory(id, rg.table, time.Now().Add(-rg.d))
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, historyView{Range: name, Resolution: rg.step, Items: pts})
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
