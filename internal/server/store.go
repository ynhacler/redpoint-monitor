package server

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver" // 纯 Go（SQLite 预先转换为 Go 代码），无需 CGO，也不在运行时编译
)

// Store wraps SQLite. Writes go through a single connection-limited pool in batches
// (see Server.flushLoop); reads use the same handle (WAL allows concurrent readers).
// TODO(P2): 通过接口支持可选的 PostgreSQL 后端（设计 3.5、36.3）。
type Store struct {
	DB  *sql.DB
	Dir string // 数据目录：云账户凭证的密钥 secret.key 也在这里（设计 44.2）
}

func OpenStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	// _txlock=immediate：显式事务（都是写事务）在 BEGIN 时就取得写锁，与其他写入冲突时按 busy_timeout 等待。
	// 默认的 deferred 事务先读后写时，若另一个写入已提交，SQLite 直接返回 “database is locked” 而不等待——
	// 压测中批量写入与降采样同时进行时出现过（设计 21、43.3）。
	dsn := "file:" + filepath.Join(dataDir, "monitor.db") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)" +
		"&_txlock=immediate"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{DB: db, Dir: dataDir}
	if err := s.enableIncrementalVacuum(); err != nil {
		return nil, fmt.Errorf("auto_vacuum: %w", err)
	}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// enableIncrementalVacuum 把数据库切换为 auto_vacuum = INCREMENTAL，使过期数据删除后
// 可以用 PRAGMA incremental_vacuum 归还磁盘空间（设计 21）。
// 新数据库在建表前设置即可；已有数据库需要一次 VACUUM 才能生效，只在首次升级时执行。
func (s *Store) enableIncrementalVacuum() error {
	var mode int
	if err := s.DB.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return err
	}
	if mode == 2 {
		return nil
	}
	if _, err := s.DB.Exec(`PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
		return err
	}
	_, err := s.DB.Exec(`VACUUM`)
	return err
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

	// 迁移 4：降采样聚合表与进度（设计 18.4、21）。列名与 metrics_raw 相同的为平均值，*_max / *_min 为极值。
	`CREATE TABLE metrics_1m (
		server_id INTEGER NOT NULL,
		ts INTEGER NOT NULL,          -- 桶起点，Unix 秒
		n INTEGER NOT NULL,           -- 聚合的原始点数，用于逐级加权平均
		cpu REAL, cpu_max REAL, cpu_min REAL,
		cpu_p95 REAL,                 -- 只在 1 分钟级由原始点计算，更粗粒度取 max 近似（设计 21）
		load1 REAL, load1_max REAL,
		mem_used INTEGER, mem_used_max INTEGER, mem_total INTEGER,
		swap_used INTEGER, swap_used_max INTEGER,
		disk_used INTEGER, disk_used_max INTEGER, disk_total INTEGER,
		rx_speed INTEGER, rx_speed_max INTEGER, rx_speed_min INTEGER,
		tx_speed INTEGER, tx_speed_max INTEGER, tx_speed_min INTEGER,
		PRIMARY KEY (server_id, ts)
	) WITHOUT ROWID;
	CREATE TABLE metrics_5m (
		server_id INTEGER NOT NULL,
		ts INTEGER NOT NULL,          -- 桶起点，Unix 秒
		n INTEGER NOT NULL,           -- 聚合的原始点数，用于逐级加权平均
		cpu REAL, cpu_max REAL, cpu_min REAL,
		cpu_p95 REAL,                 -- 只在 1 分钟级由原始点计算，更粗粒度取 max 近似（设计 21）
		load1 REAL, load1_max REAL,
		mem_used INTEGER, mem_used_max INTEGER, mem_total INTEGER,
		swap_used INTEGER, swap_used_max INTEGER,
		disk_used INTEGER, disk_used_max INTEGER, disk_total INTEGER,
		rx_speed INTEGER, rx_speed_max INTEGER, rx_speed_min INTEGER,
		tx_speed INTEGER, tx_speed_max INTEGER, tx_speed_min INTEGER,
		PRIMARY KEY (server_id, ts)
	) WITHOUT ROWID;
	CREATE TABLE metrics_1h (
		server_id INTEGER NOT NULL,
		ts INTEGER NOT NULL,          -- 桶起点，Unix 秒
		n INTEGER NOT NULL,           -- 聚合的原始点数，用于逐级加权平均
		cpu REAL, cpu_max REAL, cpu_min REAL,
		cpu_p95 REAL,                 -- 只在 1 分钟级由原始点计算，更粗粒度取 max 近似（设计 21）
		load1 REAL, load1_max REAL,
		mem_used INTEGER, mem_used_max INTEGER, mem_total INTEGER,
		swap_used INTEGER, swap_used_max INTEGER,
		disk_used INTEGER, disk_used_max INTEGER, disk_total INTEGER,
		rx_speed INTEGER, rx_speed_max INTEGER, rx_speed_min INTEGER,
		tx_speed INTEGER, tx_speed_max INTEGER, tx_speed_min INTEGER,
		PRIMARY KEY (server_id, ts)
	) WITHOUT ROWID;
	CREATE TABLE downsample_state (
		level TEXT PRIMARY KEY,       -- metrics_1m / metrics_5m / metrics_1h
		done_until INTEGER NOT NULL   -- 此时间之前的桶已聚合完成，Unix 秒
	);`,
	// 迁移 5：磁盘读写速率（所有磁盘之和，字节/秒，设计 4.7）。旧数据为 NULL。
	`ALTER TABLE metrics_raw ADD COLUMN disk_read INTEGER;
	ALTER TABLE metrics_raw ADD COLUMN disk_write INTEGER;
	ALTER TABLE metrics_1m ADD COLUMN disk_read INTEGER;
	ALTER TABLE metrics_1m ADD COLUMN disk_read_max INTEGER;
	ALTER TABLE metrics_1m ADD COLUMN disk_write INTEGER;
	ALTER TABLE metrics_1m ADD COLUMN disk_write_max INTEGER;
	ALTER TABLE metrics_5m ADD COLUMN disk_read INTEGER;
	ALTER TABLE metrics_5m ADD COLUMN disk_read_max INTEGER;
	ALTER TABLE metrics_5m ADD COLUMN disk_write INTEGER;
	ALTER TABLE metrics_5m ADD COLUMN disk_write_max INTEGER;
	ALTER TABLE metrics_1h ADD COLUMN disk_read INTEGER;
	ALTER TABLE metrics_1h ADD COLUMN disk_read_max INTEGER;
	ALTER TABLE metrics_1h ADD COLUMN disk_write INTEGER;
	ALTER TABLE metrics_1h ADD COLUMN disk_write_max INTEGER;`,

	// 迁移 6：Web 登录（设计 17.4、18.1）。会话只保存令牌哈希；开发用 admin token 随之删除。
	// 升级后需在面板主机上执行 vpsmon-server admin reset-password 创建管理员账号。
	`CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		username TEXT NOT NULL UNIQUE COLLATE NOCASE,
		email TEXT NOT NULL DEFAULT '',
		password_hash TEXT NOT NULL,                   -- Argon2id PHC 字符串（设计 23.4）
		role TEXT NOT NULL DEFAULT 'admin',            -- MVP 只有单管理员（设计 35.2）
		status TEXT NOT NULL DEFAULT 'active',
		must_change_password INTEGER NOT NULL DEFAULT 0, -- 初始化 / 重置的随机密码，首次登录必须修改
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		last_login_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE sessions (
		id INTEGER PRIMARY KEY,
		token_hash TEXT NOT NULL UNIQUE,               -- 会话令牌 SHA-256，不保存明文
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL,                 -- 空闲超过 12 小时失效（设计 17.1）
		expires_at INTEGER NOT NULL,                   -- 最长 7 天
		reauth_until INTEGER NOT NULL DEFAULT 0,       -- 敏感操作重新验证后 10 分钟内有效（设计 17.4）
		client_ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX sessions_user ON sessions(user_id);
	DROP TABLE admin_tokens;`,

	// 迁移 7：节点所在国家 / 地区（设计 1.2.3），用于界面显示国旗；由用户手动选择，不做 IP 地理识别。
	`ALTER TABLE servers ADD COLUMN country TEXT NOT NULL DEFAULT '';  -- ISO 3166-1 两位代码，如 JP、HK；空表示未填`,

	// 迁移 8：流量单位口径、统计系数与手动校准（设计 5.7、5.8、18.12）
	`ALTER TABLE servers ADD COLUMN traffic_unit TEXT NOT NULL DEFAULT 'decimal'; -- decimal（10^9）/ binary（2^30）
	ALTER TABLE servers ADD COLUMN traffic_factor REAL NOT NULL DEFAULT 1.0;      -- 统计系数，长期修正固定比例偏差
	CREATE TABLE traffic_adjustments (
		id INTEGER PRIMARY KEY,
		server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		cycle_start TEXT NOT NULL,          -- 校准所属计费周期的开始日 YYYY-MM-DD；周期重置后不再生效
		measured_bytes INTEGER NOT NULL,    -- 校准时的统计值（已乘系数）
		reported_bytes INTEGER NOT NULL,    -- 用户填写的服务商面板数值
		adjustment_bytes INTEGER NOT NULL,  -- reported - measured
		note TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	CREATE INDEX traffic_adjustments_cycle ON traffic_adjustments(server_id, cycle_start);`,

	// 迁移 9：服务商标称带宽（设计 27.2）。Agent 采集不到端口速率，只能手动填写
	`ALTER TABLE servers ADD COLUMN bandwidth_mbps INTEGER NOT NULL DEFAULT 0;`,

	// 迁移 10：告警规则与告警事件（设计 16、18.7、18.8），写入 16.1 的全局默认规则。
	// rule_key 是同一条规则在全局 / 分组 / 节点三层中的标识，下层按 rule_key 覆盖上层（设计 16.2）。
	`CREATE TABLE alert_rules (
		id INTEGER PRIMARY KEY,
		rule_key TEXT NOT NULL,
		scope_type TEXT NOT NULL DEFAULT 'global',  -- global / group / server
		scope_id TEXT NOT NULL DEFAULT '',          -- 分组名或节点 ID；global 时为空
		type TEXT NOT NULL,
		operator TEXT NOT NULL DEFAULT '>',         -- > / >=
		threshold REAL NOT NULL,
		recover_threshold REAL NOT NULL,            -- 低于此值恢复（回差）
		duration_s INTEGER NOT NULL DEFAULT 0,
		recover_duration_s INTEGER NOT NULL DEFAULT 0,
		severity TEXT NOT NULL,                     -- info / warning / critical
		channels TEXT NOT NULL DEFAULT '[]',        -- 空表示按级别使用默认渠道（设计 16.5）
		repeat_interval_s INTEGER NOT NULL DEFAULT 0,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		UNIQUE (scope_type, scope_id, rule_key)
	);
	CREATE TABLE alert_events (
		id INTEGER PRIMARY KEY,
		rule_id INTEGER NOT NULL,
		rule_key TEXT NOT NULL,
		server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		type TEXT NOT NULL,
		severity TEXT NOT NULL,
		state TEXT NOT NULL,                        -- firing / resolved（pending 只在内存中，设计 16.3）
		value REAL NOT NULL,                        -- 触发时的值
		threshold REAL NOT NULL,
		message TEXT NOT NULL,
		started_at INTEGER NOT NULL,                -- 进入 pending 的时间
		fired_at INTEGER NOT NULL,
		resolved_at INTEGER NOT NULL DEFAULT 0,
		resolved_value REAL,
		last_notified_at INTEGER NOT NULL DEFAULT 0,
		notify_count INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX alert_events_server ON alert_events(server_id, id);
	CREATE INDEX alert_events_state ON alert_events(state, id);
	CREATE INDEX alert_events_fired ON alert_events(fired_at);
	INSERT INTO alert_rules (rule_key, type, operator, threshold, recover_threshold, duration_s, recover_duration_s,
		severity, repeat_interval_s, enabled, created_at, updated_at) VALUES
		('offline',          'offline',          '>',  120, 30,  0,   0,   'critical', 7200, 1, unixepoch(), unixepoch()),
		('cpu',              'cpu',              '>',  90,  80,  300, 120, 'warning',  0,    1, unixepoch(), unixepoch()),
		('memory',           'memory',           '>',  90,  85,  300, 120, 'warning',  0,    1, unixepoch(), unixepoch()),
		('disk',             'disk',             '>',  85,  80,  0,   0,   'warning',  0,    1, unixepoch(), unixepoch()),
		('disk_critical',    'disk',             '>',  95,  90,  0,   0,   'critical', 7200, 1, unixepoch(), unixepoch()),
		('swap',             'swap',             '>',  50,  40,  600, 0,   'warning',  0,    0, unixepoch(), unixepoch()),
		('load',             'load',             '>',  2,   1,   600, 0,   'warning',  0,    0, unixepoch(), unixepoch()),
		('traffic_80',       'traffic',          '>=', 80,  80,  0,   0,   'info',     0,    1, unixepoch(), unixepoch()),
		('traffic_90',       'traffic',          '>=', 90,  90,  0,   0,   'info',     0,    1, unixepoch(), unixepoch()),
		('traffic_95',       'traffic',          '>=', 95,  95,  0,   0,   'critical', 7200, 1, unixepoch(), unixepoch()),
		('traffic_100',      'traffic',          '>=', 100, 100, 0,   0,   'critical', 7200, 1, unixepoch(), unixepoch()),
		('traffic_forecast', 'traffic_forecast', '>',  100, 100, 0,   0,   'info',     0,    1, unixepoch(), unixepoch());`,

	// 迁移 11：静音与维护（设计 16.6、18.14）。到期自动失效；手动结束时把 ends_at 设为结束时间，保留记录。
	`CREATE TABLE silences (
		id INTEGER PRIMARY KEY,
		scope_type TEXT NOT NULL,          -- server / group / rule / global
		scope_id TEXT NOT NULL DEFAULT '', -- 节点 ID、分组名或 rule_key；global 时为空
		kind TEXT NOT NULL,                -- mute（照常评估、不通知）/ maintenance（节点级，不产生告警）
		reason TEXT NOT NULL DEFAULT '',
		starts_at INTEGER NOT NULL,
		ends_at INTEGER,                   -- 为空表示直到手动结束
		created_by TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	CREATE INDEX silences_ends ON silences(ends_at);`,

	// 迁移 12：已同步并验签的官方 Agent 版本（设计 29.1）。只保存验签通过的清单原文与签名，
	// 安装命令中的脚本哈希、可下载的构建都来自这里；面板不能修改清单内容（签名覆盖原始字节）。
	`CREATE TABLE agent_releases (
		version TEXT PRIMARY KEY,           -- 不带 v，如 0.2.0
		channel TEXT NOT NULL,              -- stable / beta
		manifest BLOB NOT NULL,             -- 清单原文（已验签）
		signature TEXT NOT NULL,            -- manifest.json.minisig
		key_id TEXT NOT NULL,               -- 签名所用官方公钥的 ID
		installer_file TEXT NOT NULL,
		installer_sha256 TEXT NOT NULL,
		released_at TEXT NOT NULL,
		synced_at INTEGER NOT NULL
	);`,

	// 迁移 13：远程升级任务（设计 29.10、29.14）。同一节点同时只有一个进行中的任务。
	`CREATE TABLE upgrade_tasks (
		id INTEGER PRIMARY KEY,
		server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
		target_version TEXT NOT NULL,       -- 已验签的官方版本，不带 v
		from_version TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,               -- pending / delivered / staged / installing / success / failed / rolled_back / cancelled
		reason TEXT NOT NULL DEFAULT '',    -- 失败或回滚原因（Agent 上报，已截断）
		created_by TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX upgrade_tasks_server ON upgrade_tasks(server_id, id);`,

	// 迁移 14：面板镜像（设计 27.5.3）。mirrored_at 非 0 表示该版本的全部文件已校验并保存在数据目录的 releases/ 下。
	`ALTER TABLE agent_releases ADD COLUMN mirrored_at INTEGER NOT NULL DEFAULT 0;`,

	// 迁移 15：通知渠道与投递记录（设计 16.5、18.15）。config 为渠道参数（含凭证，只用于发送，接口返回时脱敏）；
	// 投递记录冗余保存渠道名与节点名，渠道或节点删除后记录仍可读，保留 30 天。
	`CREATE TABLE notification_channels (
		id INTEGER PRIMARY KEY,
		type TEXT NOT NULL,                 -- telegram / webhook
		name TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		min_severity TEXT NOT NULL DEFAULT 'warning',
		notify_resolved INTEGER NOT NULL DEFAULT 1,
		config TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE notification_deliveries (
		id INTEGER PRIMARY KEY,
		channel_id INTEGER NOT NULL,
		channel_name TEXT NOT NULL,
		channel_type TEXT NOT NULL,
		event_id INTEGER NOT NULL DEFAULT 0,  -- 测试通知为 0
		server_name TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL,                   -- firing / resolved / repeat / test
		title TEXT NOT NULL,
		status TEXT NOT NULL,                 -- sent / failed / retrying
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',  -- 不含请求地址与凭证
		created_at INTEGER NOT NULL,
		sent_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX notification_deliveries_event ON notification_deliveries(event_id);
	CREATE INDEX notification_deliveries_created ON notification_deliveries(created_at);`,

	// 迁移 16：系统设置（设计 16.5 免打扰时段等）。value 为 JSON，按 key 读写。
	`CREATE TABLE settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at INTEGER NOT NULL
	);`,

	// 迁移 17：校准记录保存当时的原始收发字节（未乘系数、未按计费模式取值）。
	// 展示时按当前系数与计费模式重算偏差，校准后修改系数或模式不会重复修正（设计 5.7）。旧记录为 NULL，沿用固定偏差。
	`ALTER TABLE traffic_adjustments ADD COLUMN raw_rx INTEGER;
	ALTER TABLE traffic_adjustments ADD COLUMN raw_tx INTEGER;`,

	// 迁移 18：历史曲线增加 CPU steal / iowait 占比（%）与 TCP 连接数（设计 4.4、4.9、21）。
	// 旧版 Agent 不上报时为 NULL，聚合时只用有值的点，避免把“没有数据”画成 0。
	`ALTER TABLE metrics_raw ADD COLUMN steal REAL;
	ALTER TABLE metrics_raw ADD COLUMN iowait REAL;
	ALTER TABLE metrics_raw ADD COLUMN tcp INTEGER;
	ALTER TABLE metrics_1m ADD COLUMN steal REAL;
	ALTER TABLE metrics_1m ADD COLUMN steal_max REAL;
	ALTER TABLE metrics_1m ADD COLUMN iowait REAL;
	ALTER TABLE metrics_1m ADD COLUMN iowait_max REAL;
	ALTER TABLE metrics_1m ADD COLUMN tcp INTEGER;
	ALTER TABLE metrics_1m ADD COLUMN tcp_max INTEGER;
	ALTER TABLE metrics_5m ADD COLUMN steal REAL;
	ALTER TABLE metrics_5m ADD COLUMN steal_max REAL;
	ALTER TABLE metrics_5m ADD COLUMN iowait REAL;
	ALTER TABLE metrics_5m ADD COLUMN iowait_max REAL;
	ALTER TABLE metrics_5m ADD COLUMN tcp INTEGER;
	ALTER TABLE metrics_5m ADD COLUMN tcp_max INTEGER;
	ALTER TABLE metrics_1h ADD COLUMN steal REAL;
	ALTER TABLE metrics_1h ADD COLUMN steal_max REAL;
	ALTER TABLE metrics_1h ADD COLUMN iowait REAL;
	ALTER TABLE metrics_1h ADD COLUMN iowait_max REAL;
	ALTER TABLE metrics_1h ADD COLUMN tcp INTEGER;
	ALTER TABLE metrics_1h ADD COLUMN tcp_max INTEGER;`,

	// 迁移 19：默认规则“Agent 时钟偏差”（设计 16.1、43.5）：偏差 > 60 秒持续 5 分钟触发，< 30 秒持续 5 分钟恢复，级别提示。
	`INSERT INTO alert_rules (rule_key, type, operator, threshold, recover_threshold, duration_s, recover_duration_s,
		severity, repeat_interval_s, enabled, created_at, updated_at) VALUES
		('agent_clock', 'agent_clock', '>', 60, 30, 300, 300, 'info', 0, 1, unixepoch(), unixepoch());`,

	// 迁移 20：节点的采样（上报）间隔，秒；0 表示默认 10 秒（设计 4.2、6.1）
	`ALTER TABLE servers ADD COLUMN report_interval_s INTEGER NOT NULL DEFAULT 0;`,

	// 迁移 21：节点的计费时区（IANA 名称，如 America/Los_Angeles）；空表示面板本地时区（设计 5.4）。
	// 只影响之后写入的每日流量：已有的按天汇总不重新划分。
	`ALTER TABLE servers ADD COLUMN traffic_timezone TEXT NOT NULL DEFAULT '';`,

	// 迁移 22：云厂商账户（设计 44.7）。凭证为 AES-256-GCM 密文（密钥 DATA/secret.key，设计 44.2），只显示末 4 位。
	// 金额以“分”为整数，币种随账户；同步状态：各类数据上次成功时间、失败退避、凭证失效时停止自动同步。
	`CREATE TABLE cloud_accounts (
		id INTEGER PRIMARY KEY,
		provider TEXT NOT NULL,
		name TEXT NOT NULL UNIQUE,
		regions TEXT NOT NULL DEFAULT '',
		credential_enc BLOB NOT NULL,
		credential_hint TEXT NOT NULL DEFAULT '',
		budget_cents INTEGER NOT NULL DEFAULT 0,
		cost_interval_h INTEGER NOT NULL DEFAULT 12,
		enabled INTEGER NOT NULL DEFAULT 1,
		sync_cost INTEGER NOT NULL DEFAULT 1,
		sync_traffic INTEGER NOT NULL DEFAULT 1,
		cost_synced_at INTEGER NOT NULL DEFAULT 0,
		instances_synced_at INTEGER NOT NULL DEFAULT 0,
		traffic_synced_at INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		error_since INTEGER NOT NULL DEFAULT 0,
		fail_count INTEGER NOT NULL DEFAULT 0,
		next_try_at INTEGER NOT NULL DEFAULT 0,
		auth_failed INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE cloud_costs (
		account_id INTEGER NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
		period TEXT NOT NULL,
		amount_cents INTEGER NOT NULL,
		forecast_cents INTEGER,
		balance_cents INTEGER,
		currency TEXT NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (account_id, period)
	);
	CREATE TABLE cloud_instances (
		id INTEGER PRIMARY KEY,
		account_id INTEGER NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
		instance_id TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		region TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT '',
		public_ipv4 TEXT NOT NULL DEFAULT '',
		public_ipv6 TEXT NOT NULL DEFAULT '',
		plan TEXT NOT NULL DEFAULT '',
		expire_at INTEGER NOT NULL DEFAULT 0,
		renew_price_cents INTEGER NOT NULL DEFAULT 0,
		traffic_limit_bytes INTEGER NOT NULL DEFAULT 0,
		traffic_used_bytes INTEGER NOT NULL DEFAULT 0,
		traffic_period_start TEXT NOT NULL DEFAULT '',
		server_id INTEGER REFERENCES servers(id) ON DELETE SET NULL,
		updated_at INTEGER NOT NULL,
		UNIQUE (account_id, instance_id)
	);`,

	// 迁移 23：只读 API Key（设计 45.2）。只保存 SHA-256 哈希（约束 4），hint 为末 4 位；
	// scope_type：all / group / servers，scope_value 为分组名或逗号分隔的节点 ID。
	`CREATE TABLE api_keys (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		hint TEXT NOT NULL,
		scope_type TEXT NOT NULL DEFAULT 'all',
		scope_value TEXT NOT NULL DEFAULT '',
		expires_at INTEGER NOT NULL DEFAULT 0,
		created_by TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		last_used_at INTEGER NOT NULL DEFAULT 0,
		revoked_at INTEGER NOT NULL DEFAULT 0
	);`,
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
	Country       string `json:"country"`        // ISO 3166-1 两位代码（大写），空表示未填
	BandwidthMbps int    `json:"bandwidth_mbps"` // 服务商标称带宽（端口速率），Mbps；0 表示未填
	// ReportIntervalS 是采样（上报）间隔，秒；0 表示默认 10 秒（设计 4.2、6.1）
	ReportIntervalS int `json:"report_interval_s"`
	// TrafficTimezone 是计费时区（IANA 名称）；空表示面板本地时区（设计 5.4）
	TrafficTimezone string  `json:"traffic_timezone"`
	TrafficUnit     string  `json:"traffic_unit"`   // decimal / binary（设计 5.8）
	TrafficFactor   float64 `json:"traffic_factor"` // 统计系数，默认 1（设计 5.7）
	PriceCents      int64   `json:"price_cents"`    // 续费价格 × 100
	Currency        string  `json:"currency"`
	BillingPeriod   string  `json:"billing_period"`
	ExpireDate      string  `json:"expire_date"` // YYYY-MM-DD，空表示未填
	CreatedAt       int64   `json:"created_at"`
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

// MetricPoint 是历史曲线上的一个点（设计 19.7）。平均值字段与原始点同名，*_max 为桶内最大值；
// 原始粒度时 *_max 与平均值相同。
type MetricPoint struct {
	TS        int64   `json:"ts"` // 桶起点，Unix 秒
	CPU       float64 `json:"cpu"`
	CPUMax    float64 `json:"cpu_max"`
	Load1     float64 `json:"load1"`
	MemUsed   uint64  `json:"mem_used"`
	MemTotal  uint64  `json:"mem_total"`
	SwapUsed  uint64  `json:"swap_used"`
	DiskUsed  uint64  `json:"disk_used"`
	DiskTotal uint64  `json:"disk_total"`
	RxSpeed   uint64  `json:"rx_speed"`
	RxMax     uint64  `json:"rx_speed_max"`
	TxSpeed   uint64  `json:"tx_speed"`
	TxMax     uint64  `json:"tx_speed_max"`
	// 磁盘读写速率（所有磁盘之和，字节/秒，设计 4.7）；旧版 Agent 的时段为 null
	DiskRead     *uint64 `json:"disk_read"`
	DiskReadMax  *uint64 `json:"disk_read_max"`
	DiskWrite    *uint64 `json:"disk_write"`
	DiskWriteMax *uint64 `json:"disk_write_max"`
	// CPU steal / iowait 占比（0～100）与 TCP 连接数（设计 4.4、4.9）；旧版 Agent 的时段为 null
	Steal     *float64 `json:"steal"`
	StealMax  *float64 `json:"steal_max"`
	IOWait    *float64 `json:"iowait"`
	IOWaitMax *float64 `json:"iowait_max"`
	TCP       *uint64  `json:"tcp"`
	TCPMax    *uint64  `json:"tcp_max"`
}

// nullableF 把可空浮点数转为指针，JSON 中 NULL 输出为 null。
func nullableF(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

// nullable 把可空整数转为指针，JSON 中 NULL 输出为 null。
func nullable(v sql.NullInt64) *uint64 {
	if !v.Valid {
		return nil
	}
	u := uint64(v.Int64)
	return &u
}

// MetricsHistory 读取某一粒度表中 since 之后的点。table 只能是内部常量，不来自用户输入。
func (s *Store) MetricsHistory(serverID int64, table string, since time.Time) ([]MetricPoint, error) {
	cols := `ts, cpu, cpu_max, load1, mem_used, mem_total, swap_used, disk_used, disk_total, rx_speed, rx_speed_max,
		tx_speed, tx_speed_max, disk_read, disk_read_max, disk_write, disk_write_max,
		steal, steal_max, iowait, iowait_max, tcp, tcp_max`
	if table == "metrics_raw" {
		cols = `ts, cpu, cpu, load1, mem_used, mem_total, swap_used, disk_used, disk_total, rx_speed, rx_speed,
			tx_speed, tx_speed, disk_read, disk_read, disk_write, disk_write,
			steal, steal, iowait, iowait, tcp, tcp`
	}
	rows, err := s.DB.Query(`SELECT `+cols+` FROM `+table+` WHERE server_id = ? AND ts >= ? ORDER BY ts`, serverID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetricPoint{}
	for rows.Next() {
		var p MetricPoint
		var cpu, cpuMax, load1 sql.NullFloat64
		var mu, mt, su, du, dt, rx, rxm, tx, txm, dr, drm, dw, dwm, tcp, tcpm sql.NullInt64
		var st, stm, iw, iwm sql.NullFloat64
		if err := rows.Scan(&p.TS, &cpu, &cpuMax, &load1, &mu, &mt, &su, &du, &dt, &rx, &rxm, &tx, &txm,
			&dr, &drm, &dw, &dwm, &st, &stm, &iw, &iwm, &tcp, &tcpm); err != nil {
			return nil, err
		}
		p.Steal, p.StealMax, p.IOWait, p.IOWaitMax = nullableF(st), nullableF(stm), nullableF(iw), nullableF(iwm)
		p.TCP, p.TCPMax = nullable(tcp), nullable(tcpm)
		p.DiskRead, p.DiskReadMax, p.DiskWrite, p.DiskWriteMax = nullable(dr), nullable(drm), nullable(dw), nullable(dwm)
		p.CPU, p.CPUMax, p.Load1 = cpu.Float64, cpuMax.Float64, load1.Float64
		p.MemUsed, p.MemTotal, p.SwapUsed = uint64(mu.Int64), uint64(mt.Int64), uint64(su.Int64)
		p.DiskUsed, p.DiskTotal = uint64(du.Int64), uint64(dt.Int64)
		p.RxSpeed, p.RxMax, p.TxSpeed, p.TxMax = uint64(rx.Int64), uint64(rxm.Int64), uint64(tx.Int64), uint64(txm.Int64)
		out = append(out, p)
	}
	return out, rows.Err()
}
