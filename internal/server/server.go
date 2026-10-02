package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"vpsmon/internal/protocol"
)

// Online status thresholds (design ch. 22).
const (
	onlineWithin  = 30 * time.Second
	unknownWithin = 120 * time.Second
	rawRetention  = 24 * time.Hour
	flushEvery    = 3 * time.Second
)

type Server struct {
	store *Store
	web   fs.FS

	mu       sync.Mutex
	latest   map[int64]*snapshot          // in-memory realtime state; reads never hit the DB
	counters map[int64]map[string]*Counter // last kernel counters per server/iface
	pending  []pendingWrite
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

func New(store *Store, web fs.FS) (*Server, error) {
	c, err := store.LoadCounters()
	if err != nil {
		return nil, err
	}
	return &Server{store: store, web: web, latest: map[int64]*snapshot{}, counters: c}, nil
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
	log.Printf("vpsmon-server listening on http://%s", listen)
	err := srv.ListenAndServe()
	s.flush() // persist what is buffered
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("POST /api/v1/agent/report", s.handleReport)
	mux.Handle("GET /api/v1/servers", s.admin(s.handleListServers))
	mux.Handle("GET /api/v1/servers/{id}/metrics", s.admin(s.handleMetrics))
	mux.Handle("/", s.webHandler())
	return mux
}

// admin guards Web/App read APIs with the dev admin token.
// TODO(A2): 改为 Web 会话登录（设计 8.2）；TODO(C): App Device Token，只读范围（设计 12.5）。
func (s *Server) admin(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok := bearer(r); tok == "" || !s.store.ValidAdminToken(tok) {
			httpError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r)
	})
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	sid := s.store.AgentServerID(bearer(r))
	if sid == 0 {
		httpError(w, http.StatusUnauthorized, "invalid agent token")
		return
	}
	var rep protocol.Report
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&rep); err != nil {
		httpError(w, http.StatusBadRequest, "bad report")
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
			log.Printf("server %d iface %s: counter reset (reboot/NIC change), new baseline", sid, ni.Interface)
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
		log.Printf("flush: %v", err)
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
			log.Printf("flush metrics: %v", err)
			return
		}
		if p.rx > 0 || p.tx > 0 {
			// 按面板本地时区划分日期。TODO(A4): 按节点设置计费时区（设计 1.2.4）。
			if _, err := tx.Exec(`INSERT INTO traffic_daily (server_id, day, rx, tx) VALUES (?,?,?,?)
				ON CONFLICT(server_id, day) DO UPDATE SET rx = rx + excluded.rx, tx = tx + excluded.tx`,
				p.serverID, p.at.Format("2006-01-02"), p.rx, p.tx); err != nil {
				log.Printf("flush traffic: %v", err)
				return
			}
		}
		for iface, c := range p.counters {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO traffic_counters (server_id, iface, boot_id, ifindex, rx, tx)
				VALUES (?,?,?,?,?,?)`, p.serverID, iface, c.BootID, c.IfIndex, c.Rx, c.Tx); err != nil {
				log.Printf("flush counters: %v", err)
				return
			}
		}
		if _, err := tx.Exec(`UPDATE servers SET last_seen_at = ? WHERE id = ?`, p.at.Unix(), p.serverID); err != nil {
			log.Printf("flush last_seen: %v", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("flush commit: %v", err)
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
				log.Printf("retention: %v", err)
			}
		}
	}
}

type serverView struct {
	ServerRow
	Status  string           `json:"status"` // online | unknown | offline
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

func (s *Server) handleListServers(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.store.ListServers()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "db error")
		return
	}
	now := time.Now()
	out := make([]serverView, 0, len(rows))
	for _, row := range rows {
		v := serverView{ServerRow: row, Status: "offline"}
		s.mu.Lock()
		if snap := s.latest[row.ID]; snap != nil {
			rep := snap.Report
			v.Latest = &rep
			v.LastSeenAt = snap.ReceivedAt.Unix()
		}
		s.mu.Unlock()
		if v.LastSeenAt > 0 {
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

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "bad id")
		return
	}
	rng, err := time.ParseDuration(r.URL.Query().Get("range"))
	if err != nil || rng <= 0 || rng > rawRetention {
		rng = time.Hour
	}
	pts, err := s.store.Metrics(id, time.Now().Add(-rng))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "db error")
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
