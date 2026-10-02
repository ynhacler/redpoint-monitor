package server

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver" // pure Go (wasm), no CGO
	_ "github.com/ncruces/go-sqlite3/embed"
)

// Store wraps SQLite. Writes go through a single connection-limited pool in batches
// (see Server.flushLoop); reads use the same handle (WAL allows concurrent readers).
// TODO(M6): optional PostgreSQL backend behind an interface (design 3.5).
type Store struct {
	DB *sql.DB
}

func OpenStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dataDir, "monitor.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Migrations are append-only. Never edit an existing entry; add a new one.
var migrations = []string{
	`CREATE TABLE servers (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		traffic_limit_bytes INTEGER NOT NULL DEFAULT 0,  -- 0 = unlimited
		traffic_reset_day INTEGER NOT NULL DEFAULT 1,
		traffic_count_mode TEXT NOT NULL DEFAULT 'sum',
		created_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE agent_tokens (
		id INTEGER PRIMARY KEY,
		server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		token_hash TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL,
		revoked_at INTEGER
	);
	CREATE TABLE admin_tokens (
		id INTEGER PRIMARY KEY,
		token_hash TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE metrics_raw (
		server_id INTEGER NOT NULL,
		ts INTEGER NOT NULL,
		cpu REAL, load1 REAL,
		mem_used INTEGER, mem_total INTEGER, swap_used INTEGER,
		disk_used INTEGER, disk_total INTEGER,
		rx_speed INTEGER, tx_speed INTEGER,
		PRIMARY KEY (server_id, ts)
	) WITHOUT ROWID;
	CREATE TABLE traffic_counters (
		server_id INTEGER NOT NULL,
		iface TEXT NOT NULL,
		boot_id TEXT NOT NULL,
		ifindex INTEGER NOT NULL,
		rx INTEGER NOT NULL, tx INTEGER NOT NULL,
		PRIMARY KEY (server_id, iface)
	) WITHOUT ROWID;
	CREATE TABLE traffic_daily (
		server_id INTEGER NOT NULL,
		day TEXT NOT NULL,             -- YYYY-MM-DD in server local time
		rx INTEGER NOT NULL DEFAULT 0,
		tx INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (server_id, day)
	) WITHOUT ROWID;`,
}

func (s *Store) migrate() error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_version (v INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	_ = s.DB.QueryRow(`SELECT COALESCE(MAX(v),0) FROM schema_version`).Scan(&v)
	for i := v; i < len(migrations); i++ {
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (v) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateAdminToken() (string, error) {
	tok := NewToken(PrefixAdmin)
	_, err := s.DB.Exec(`INSERT INTO admin_tokens (token_hash, created_at) VALUES (?, ?)`, HashToken(tok), time.Now().Unix())
	return tok, err
}

func (s *Store) ValidAdminToken(tok string) bool {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM admin_tokens WHERE token_hash = ?`, HashToken(tok)).Scan(&n)
	return n > 0
}

// CreateServer adds a node and returns a fresh agent token (shown once, never stored).
func (s *Store) CreateServer(name string, limitBytes int64, resetDay int) (int64, string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO servers (name, traffic_limit_bytes, traffic_reset_day, created_at) VALUES (?, ?, ?, ?)`,
		name, limitBytes, resetDay, time.Now().Unix())
	if err != nil {
		return 0, "", err
	}
	id, _ := res.LastInsertId()
	tok := NewToken(PrefixAgent)
	if _, err := tx.Exec(`INSERT INTO agent_tokens (server_id, token_hash, created_at) VALUES (?, ?, ?)`,
		id, HashToken(tok), time.Now().Unix()); err != nil {
		return 0, "", err
	}
	return id, tok, tx.Commit()
}

// AgentServerID resolves an agent token to its server, or 0.
func (s *Store) AgentServerID(tok string) int64 {
	var id int64
	_ = s.DB.QueryRow(`SELECT server_id FROM agent_tokens WHERE token_hash = ? AND revoked_at IS NULL`, HashToken(tok)).Scan(&id)
	return id
}

type ServerRow struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	LimitBytes int64  `json:"traffic_limit_bytes"`
	ResetDay   int    `json:"traffic_reset_day"`
	CountMode  string `json:"traffic_count_mode"`
	LastSeenAt int64  `json:"last_seen_at"`
}

func (s *Store) ListServers() ([]ServerRow, error) {
	rows, err := s.DB.Query(`SELECT id, name, traffic_limit_bytes, traffic_reset_day, traffic_count_mode, last_seen_at FROM servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerRow
	for rows.Next() {
		var r ServerRow
		if err := rows.Scan(&r.ID, &r.Name, &r.LimitBytes, &r.ResetDay, &r.CountMode, &r.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TrafficSince sums daily traffic from the given day (inclusive).
func (s *Store) TrafficSince(serverID int64, since time.Time) (rx, tx uint64, err error) {
	err = s.DB.QueryRow(`SELECT COALESCE(SUM(rx),0), COALESCE(SUM(tx),0) FROM traffic_daily WHERE server_id = ? AND day >= ?`,
		serverID, since.Format("2006-01-02")).Scan(&rx, &tx)
	return
}

func (s *Store) LoadCounters() (map[int64]map[string]*Counter, error) {
	rows, err := s.DB.Query(`SELECT server_id, iface, boot_id, ifindex, rx, tx FROM traffic_counters`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]*Counter{}
	for rows.Next() {
		var sid int64
		var iface string
		c := &Counter{}
		if err := rows.Scan(&sid, &iface, &c.BootID, &c.IfIndex, &c.Rx, &c.Tx); err != nil {
			return nil, err
		}
		if out[sid] == nil {
			out[sid] = map[string]*Counter{}
		}
		out[sid][iface] = c
	}
	return out, rows.Err()
}

type MetricPoint struct {
	TS        int64   `json:"ts"`
	CPU       float64 `json:"cpu"`
	Load1     float64 `json:"load1"`
	MemUsed   uint64  `json:"mem_used"`
	MemTotal  uint64  `json:"mem_total"`
	SwapUsed  uint64  `json:"swap_used"`
	DiskUsed  uint64  `json:"disk_used"`
	DiskTotal uint64  `json:"disk_total"`
	RxSpeed   uint64  `json:"rx_speed"`
	TxSpeed   uint64  `json:"tx_speed"`
}

func (s *Store) Metrics(serverID int64, since time.Time) ([]MetricPoint, error) {
	rows, err := s.DB.Query(`SELECT ts, cpu, load1, mem_used, mem_total, swap_used, disk_used, disk_total, rx_speed, tx_speed
		FROM metrics_raw WHERE server_id = ? AND ts >= ? ORDER BY ts`, serverID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetricPoint{}
	for rows.Next() {
		var p MetricPoint
		if err := rows.Scan(&p.TS, &p.CPU, &p.Load1, &p.MemUsed, &p.MemTotal, &p.SwapUsed, &p.DiskUsed, &p.DiskTotal, &p.RxSpeed, &p.TxSpeed); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
