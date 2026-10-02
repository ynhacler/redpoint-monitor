package server

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver" // pure Go (wasm), no CGO
	_ "github.com/ncruces/go-sqlite3/embed"
)

// Store wraps SQLite. Writes go through a single connection-limited pool in batches
// (see Server.flushLoop); reads use the same handle (WAL allows concurrent readers).
// TODO(P2): 通过接口支持可选的 PostgreSQL 后端（设计 3.5、36.3）。
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

	// 迁移 2：节点的安装注册字段与 VPS 信息（设计 18.2、27.2、1.2.3）。
	// 已有节点都是通过 add-server 直接签发 Token 的，因此 enroll_state 默认为 enrolled。
	`ALTER TABLE servers ADD COLUMN enroll_state TEXT NOT NULL DEFAULT 'enrolled'; -- pending / enrolled（设计 27.7）
	ALTER TABLE servers ADD COLUMN expected_hostname TEXT NOT NULL DEFAULT '';     -- 用户填写，注册时核对
	ALTER TABLE servers ADD COLUMN expected_ipv4 TEXT NOT NULL DEFAULT '';
	ALTER TABLE servers ADD COLUMN expected_ipv6 TEXT NOT NULL DEFAULT '';
	ALTER TABLE servers ADD COLUMN verify_mode TEXT NOT NULL DEFAULT 'warn';       -- warn / strict（设计 27.6.3）
	ALTER TABLE servers ADD COLUMN hostname TEXT NOT NULL DEFAULT '';              -- Agent 注册时的实际值
	ALTER TABLE servers ADD COLUMN ipv4 TEXT NOT NULL DEFAULT '';                  -- 注册请求的实际来源地址
	ALTER TABLE servers ADD COLUMN ipv6 TEXT NOT NULL DEFAULT '';
	ALTER TABLE servers ADD COLUMN machine_id_hash TEXT NOT NULL DEFAULT '';       -- sha256(/etc/machine-id)，识别更换主机
	ALTER TABLE servers ADD COLUMN enrolled_at INTEGER NOT NULL DEFAULT 0;         -- Unix 秒
	ALTER TABLE servers ADD COLUMN group_name TEXT NOT NULL DEFAULT '';            -- 分组 / 标签，如“香港”
	ALTER TABLE servers ADD COLUMN note TEXT NOT NULL DEFAULT '';
	ALTER TABLE servers ADD COLUMN provider TEXT NOT NULL DEFAULT '';              -- 供应商
	ALTER TABLE servers ADD COLUMN plan TEXT NOT NULL DEFAULT '';                  -- 套餐
	ALTER TABLE servers ADD COLUMN region TEXT NOT NULL DEFAULT '';                -- 地区
	ALTER TABLE servers ADD COLUMN price_cents INTEGER NOT NULL DEFAULT 0;         -- 续费价格 × 100，避免浮点误差
	ALTER TABLE servers ADD COLUMN currency TEXT NOT NULL DEFAULT '';              -- ISO 4217，如 USD、CNY
	ALTER TABLE servers ADD COLUMN billing_period TEXT NOT NULL DEFAULT '';        -- monthly / quarterly / … / one_time
	ALTER TABLE servers ADD COLUMN expire_date TEXT NOT NULL DEFAULT '';           -- 到期日 YYYY-MM-DD，空表示未填
	ALTER TABLE servers ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;`,

	// 迁移 3：注册码与审计日志（设计 18.13、18.16）。
	`CREATE TABLE enroll_codes (
		id INTEGER PRIMARY KEY,
		server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE, -- 绑定的节点
		code_hash TEXT NOT NULL UNIQUE,            -- 注册码 SHA-256，不保存明文
		code_hint TEXT NOT NULL,                   -- 界面脱敏展示，如 ENR-7KQ2-****
		status TEXT NOT NULL DEFAULT 'ACTIVE',     -- ACTIVE / USED / REVOKED；过期由 expires_at 判断
		expires_at INTEGER NOT NULL,               -- Unix 秒
		used_at INTEGER NOT NULL DEFAULT 0,
		used_by_machine_hash TEXT NOT NULL DEFAULT '',
		used_from_ip TEXT NOT NULL DEFAULT '',
		issued_token_id INTEGER NOT NULL DEFAULT 0, -- 签发的 agent_tokens.id，用于 10 分钟内重试幂等（设计 27.6.4）
		created_at INTEGER NOT NULL
	);
	CREATE INDEX enroll_codes_server ON enroll_codes(server_id);
	CREATE TABLE audit_logs (
		id INTEGER PRIMARY KEY,
		ts INTEGER NOT NULL,                       -- Unix 秒
		actor_type TEXT NOT NULL,                  -- admin / agent / cli / system（app_device 在阶段 C 加入）
		actor_id TEXT NOT NULL DEFAULT '',
		action TEXT NOT NULL,                      -- 如 server.create、enroll_code.regenerate、agent.enroll
		target_type TEXT NOT NULL DEFAULT '',
		target_id TEXT NOT NULL DEFAULT '',
		result TEXT NOT NULL,                      -- success / failure
		client_ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '',
		details TEXT NOT NULL DEFAULT '{}'         -- JSON，写入前已脱敏（设计 24.7）
	);
	CREATE INDEX audit_logs_ts ON audit_logs(ts);`,
}

func (s *Store) migrate() error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_version (v INTEGER NOT NULL)`); err != nil {
		return err
	}
	v, err := s.SchemaVersion()
	if err != nil {
		return err
	}
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

// SchemaVersion 返回已执行的迁移数，启动日志中记录（设计 24.6）。
func (s *Store) SchemaVersion() (int, error) {
	var v int
	err := s.DB.QueryRow(`SELECT COALESCE(MAX(v),0) FROM schema_version`).Scan(&v)
	return v, err
}

func (s *Store) CreateAdminToken() (string, error) {
	tok := NewToken(PrefixAdmin)
	_, err := s.DB.Exec(`INSERT INTO admin_tokens (token_hash, created_at) VALUES (?, ?)`, HashToken(tok), time.Now().Unix())
	return tok, err
}

// ValidAdminToken 判断开发用 admin token 是否有效。
// 【安全】查询失败时返回 error 而不是 false，调用方据此返回 500，不把故障伪装成“Token 错误”（设计 43.1）。
func (s *Store) ValidAdminToken(tok string) (bool, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM admin_tokens WHERE token_hash = ?`, HashToken(tok)).Scan(&n)
	return n > 0, err
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

// AgentServerID 根据 Agent Token 查找所属节点；Token 不存在或已吊销时返回 0。
// 【安全】查询失败时返回 error，调用方按失败处理（设计 43.1）。
func (s *Store) AgentServerID(tok string) (int64, error) {
	if tok == "" {
		return 0, nil
	}
	var id int64
	err := s.DB.QueryRow(`SELECT server_id FROM agent_tokens WHERE token_hash = ? AND revoked_at IS NULL`, HashToken(tok)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// ServerRow 是节点的持久化信息（设计 18.2），也是 GET /api/v1/servers 每一项的基础字段。
type ServerRow struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	LimitBytes int64  `json:"traffic_limit_bytes"` // 0 = 不限
	ResetDay   int    `json:"traffic_reset_day"`
	CountMode  string `json:"traffic_count_mode"` // sum / rx / tx / max（设计 1.2.4）
	LastSeenAt int64  `json:"last_seen_at"`

	// 安装与注册（设计 27）
	EnrollState      string `json:"enroll_state"` // pending / enrolled
	ExpectedHostname string `json:"expected_hostname"`
	ExpectedIPv4     string `json:"expected_ipv4"`
	ExpectedIPv6     string `json:"expected_ipv6"`
	VerifyMode       string `json:"verify_mode"` // warn / strict
	Hostname         string `json:"hostname"`    // 注册时的实际值
	IPv4             string `json:"ipv4"`
	IPv6             string `json:"ipv6"`
	MachineIDHash    string `json:"-"` // 【安全】主机指纹不对外返回，只用于识别更换主机
	EnrolledAt       int64  `json:"enrolled_at"`

	// VPS 信息（设计 1.2.3、27.2）
	Group         string `json:"group"`
	Note          string `json:"note"`
	Provider      string `json:"provider"`
	Plan          string `json:"plan"`
	Region        string `json:"region"`
	PriceCents    int64  `json:"price_cents"` // 续费价格 × 100
	Currency      string `json:"currency"`
	BillingPeriod string `json:"billing_period"`
	ExpireDate    string `json:"expire_date"` // YYYY-MM-DD，空表示未填
	CreatedAt     int64  `json:"created_at"`
}

// ListServers 返回全部节点，按名称排序。
func (s *Store) ListServers() ([]ServerRow, error) {
	rows, err := s.DB.Query(`SELECT ` + serverColumns + ` FROM servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerRow
	for rows.Next() {
		r, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
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
