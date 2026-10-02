package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"vpsmon/internal/logging"
)

// 节点、注册码与审计日志的数据访问（设计 18.2、18.13、18.16）。
// 业务规则（校验、核对、限流）在 nodes.go / enroll.go 中；这里只负责事务与数据一致性。

// 存储层的具名错误，由处理函数映射为接口错误码（设计 43.3.1）。
var (
	errNameTaken     = errors.New("server name already exists")
	errNoServer      = errors.New("server not found")
	errEnrollInvalid = errors.New("invalid or expired enroll code")
)

// 节点注册状态（设计 27.7）。
const (
	enrollPending  = "pending"
	enrollEnrolled = "enrolled"
)

// 注册码状态（设计 27.4）。过期不单独落库，由 expires_at 判断，避免需要定时任务改状态。
const (
	codeActive  = "ACTIVE"
	codeUsed    = "USED"
	codeRevoked = "REVOKED"
	codeExpired = "EXPIRED" // 仅用于接口展示
)

// enrollRetryWindow：注册成功后 10 分钟内，同一主机用同一注册码重试视为幂等重试（设计 27.6.4）。
const enrollRetryWindow = 10 * time.Minute

// NodeInput 是新建节点时用户填写的信息（设计 27.2）。
type NodeInput struct {
	Name             string
	ExpectedHostname string
	ExpectedIPv4     string
	ExpectedIPv6     string
	VerifyMode       string // warn / strict
	Group            string
	Note             string
	Provider         string
	Plan             string
	Region           string
	LimitBytes       int64 // 月流量额度，字节；0 = 不限
	ResetDay         int   // 流量重置日 1～31
	CountMode        string
	PriceCents       int64 // 续费价格 × 100
	Currency         string
	BillingPeriod    string
	ExpireDate       string // YYYY-MM-DD
}

// newCode 是一条待写入的注册码：只有哈希与脱敏提示，明文只返回给调用方一次。
type newCode struct {
	hash, hint string
	expiresAt  int64
}

// isUniqueName 判断是否为 servers.name 唯一约束冲突。
func isUniqueName(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: servers.name")
}

// CreatePendingServer 在一个事务中新建“待安装”节点并写入其注册码（设计 27.1 第 ② 步）。
func (s *Store) CreatePendingServer(in NodeInput, code newCode, now time.Time) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO servers (name, enroll_state, expected_hostname, expected_ipv4, expected_ipv6,
			verify_mode, group_name, note, provider, plan, region, traffic_limit_bytes, traffic_reset_day,
			traffic_count_mode, price_cents, currency, billing_period, expire_date, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.Name, enrollPending, in.ExpectedHostname, in.ExpectedIPv4, in.ExpectedIPv6, in.VerifyMode,
		in.Group, in.Note, in.Provider, in.Plan, in.Region, in.LimitBytes, in.ResetDay, in.CountMode,
		in.PriceCents, in.Currency, in.BillingPeriod, in.ExpireDate, now.Unix(), now.Unix())
	if isUniqueName(err) {
		return 0, errNameTaken
	}
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if err := insertCode(tx, id, code, now); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func insertCode(tx *sql.Tx, serverID int64, c newCode, now time.Time) error {
	_, err := tx.Exec(`INSERT INTO enroll_codes (server_id, code_hash, code_hint, expires_at, created_at) VALUES (?,?,?,?,?)`,
		serverID, c.hash, c.hint, c.expiresAt, now.Unix())
	return err
}

// ReplaceEnrollCode 作废节点现有的可用注册码并写入新码（设计 27.4 [重新生成]、27.8 重装）。
func (s *Store) ReplaceEnrollCode(serverID int64, code newCode, now time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var one int
	if err := tx.QueryRow(`SELECT 1 FROM servers WHERE id = ?`, serverID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return errNoServer
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE enroll_codes SET status = ? WHERE server_id = ? AND status = ?`,
		codeRevoked, serverID, codeActive); err != nil {
		return err
	}
	if err := insertCode(tx, serverID, code, now); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeEnrollCodes 撤销节点所有可用的注册码；节点不存在时返回 errNoServer。
func (s *Store) RevokeEnrollCodes(serverID int64) error {
	if _, err := s.GetServer(serverID); err != nil {
		return err
	}
	_, err := s.DB.Exec(`UPDATE enroll_codes SET status = ? WHERE server_id = ? AND status = ?`,
		codeRevoked, serverID, codeActive)
	return err
}

// EnrollCodeInfo 是注册码的展示信息（不含明文）。
type EnrollCodeInfo struct {
	Hint      string
	Status    string // ACTIVE / USED / REVOKED / EXPIRED
	ExpiresAt int64
}

// LatestEnrollCode 返回节点最近一次生成的注册码；没有时返回 nil。
func (s *Store) LatestEnrollCode(serverID int64, now time.Time) (*EnrollCodeInfo, error) {
	var c EnrollCodeInfo
	err := s.DB.QueryRow(`SELECT code_hint, status, expires_at FROM enroll_codes WHERE server_id = ? ORDER BY id DESC LIMIT 1`,
		serverID).Scan(&c.Hint, &c.Status, &c.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c.Status == codeActive && c.ExpiresAt <= now.Unix() {
		c.Status = codeExpired
	}
	return &c, nil
}

// EnrollRequest 是 Agent 注册时提交的主机信息（设计 27.6.2），SourceIP 由面板从连接中观察。
type EnrollRequest struct {
	CodeHash      string
	Hostname      string
	MachineIDHash string
	OS, OSVersion string
	Arch          string
	AgentVersion  string
	SourceIP      string
}

// EnrollResult 是注册成功的结果；Token 明文只在此返回一次。
type EnrollResult struct {
	ServerID       int64
	ServerName     string
	Token          string
	Warnings       []string
	Retry          bool // 10 分钟内的幂等重试（设计 27.6.4）
	HostChanged    bool // machine_id 与上次注册不同：更换主机或重装系统（设计 27.8）
	RevokedTokens  int64
	PreviousMachID string
}

// verifyFunc 由业务层提供：根据节点设置与注册请求给出警告，或在严格模式下拒绝（设计 27.6.3）。
type verifyFunc func(n *ServerRow, req EnrollRequest) (warnings []string, reject error)

// Enroll 用注册码认领节点并签发 Agent Token（设计 27.6），全部在一个事务中完成。
//
// 【安全】注册码不存在、已使用、已撤销、已过期一律返回 errEnrollInvalid，
// 不向调用方区分原因，避免被用来探测注册码（设计 27.6.2）。
func (s *Store) Enroll(req EnrollRequest, verify verifyFunc, now time.Time) (*EnrollResult, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var (
		codeID, serverID, expiresAt, usedAt, issuedTokenID int64
		status, usedBy                                     string
	)
	err = tx.QueryRow(`SELECT id, server_id, status, expires_at, used_at, used_by_machine_hash, issued_token_id
		FROM enroll_codes WHERE code_hash = ?`, req.CodeHash).
		Scan(&codeID, &serverID, &status, &expiresAt, &usedAt, &usedBy, &issuedTokenID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errEnrollInvalid
	}
	if err != nil {
		return nil, err
	}
	retry := false
	switch {
	case status == codeActive && expiresAt > now.Unix():
	case status == codeUsed && req.MachineIDHash != "" && usedBy == req.MachineIDHash &&
		now.Sub(time.Unix(usedAt, 0)) <= enrollRetryWindow:
		// 注册响应在网络中丢失后的重试：签发新 Token 并吊销上一次签发的（设计 27.6.4）
		retry = true
	default:
		return nil, errEnrollInvalid
	}

	node, err := getServerTx(tx, serverID)
	if err != nil {
		return nil, err
	}
	warnings, reject := verify(node, req)
	if reject != nil {
		return nil, reject // 注册码保持可用：用户修正信息或改为“仅提示”后可再次使用
	}

	res := &EnrollResult{ServerID: serverID, ServerName: node.Name, Warnings: warnings, Retry: retry,
		PreviousMachID: node.MachineIDHash}
	res.HostChanged = node.EnrollState == enrollEnrolled && node.MachineIDHash != "" &&
		node.MachineIDHash != req.MachineIDHash

	// 【安全】新 Agent 注册成功后，该节点之前的 Token 全部吊销：重试时吊销上次签发的，
	// 重装 / 更换主机时吊销旧主机的（设计 27.6.4、27.8）。一个节点同一时间只有一个有效 Token。
	r, err := tx.Exec(`UPDATE agent_tokens SET revoked_at = ? WHERE server_id = ? AND revoked_at IS NULL`, now.Unix(), serverID)
	if err != nil {
		return nil, err
	}
	res.RevokedTokens, _ = r.RowsAffected()

	res.Token = NewToken(PrefixAgent)
	tr, err := tx.Exec(`INSERT INTO agent_tokens (server_id, token_hash, created_at) VALUES (?,?,?)`,
		serverID, HashToken(res.Token), now.Unix())
	if err != nil {
		return nil, err
	}
	tokenID, _ := tr.LastInsertId()

	ipv4, ipv6 := splitIP(req.SourceIP)
	if _, err := tx.Exec(`UPDATE enroll_codes SET status = ?, used_at = ?, used_by_machine_hash = ?, used_from_ip = ?,
			issued_token_id = ? WHERE id = ?`, codeUsed, now.Unix(), req.MachineIDHash, req.SourceIP, tokenID, codeID); err != nil {
		return nil, err
	}
	// 实际值与用户填写的 expected_* 分开保存（设计 18.2）；只更新本次观察到的地址族
	if _, err := tx.Exec(`UPDATE servers SET enroll_state = ?, hostname = ?, machine_id_hash = ?, enrolled_at = ?,
			ipv4 = CASE WHEN ? != '' THEN ? ELSE ipv4 END, ipv6 = CASE WHEN ? != '' THEN ? ELSE ipv6 END,
			updated_at = ? WHERE id = ?`,
		enrollEnrolled, req.Hostname, req.MachineIDHash, now.Unix(), ipv4, ipv4, ipv6, ipv6, now.Unix(), serverID); err != nil {
		return nil, err
	}
	return res, tx.Commit()
}

// Unregister 处理 Agent 卸载（设计 27.11）：吊销该节点全部 Token，节点回到“待安装”。
// 历史数据、流量统计保留，重新安装后继续使用（与设计 27.8 一致）。
func (s *Store) Unregister(serverID int64, now time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE agent_tokens SET revoked_at = ? WHERE server_id = ? AND revoked_at IS NULL`, now.Unix(), serverID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE servers SET enroll_state = ?, updated_at = ? WHERE id = ?`, enrollPending, now.Unix(), serverID); err != nil {
		return err
	}
	return tx.Commit()
}

// splitIP 按地址族拆分来源地址，返回 (ipv4, ipv6)，另一项为空。
func splitIP(ip string) (string, string) {
	if ip == "" {
		return "", ""
	}
	if strings.Contains(ip, ":") {
		return "", ip
	}
	return ip, ""
}

const serverColumns = `id, name, traffic_limit_bytes, traffic_reset_day, traffic_count_mode, last_seen_at,
	enroll_state, expected_hostname, expected_ipv4, expected_ipv6, verify_mode, hostname, ipv4, ipv6,
	machine_id_hash, enrolled_at, group_name, note, provider, plan, region, price_cents, currency,
	billing_period, expire_date, created_at`

type scanner interface{ Scan(dest ...any) error }

func scanServer(row scanner) (*ServerRow, error) {
	var r ServerRow
	err := row.Scan(&r.ID, &r.Name, &r.LimitBytes, &r.ResetDay, &r.CountMode, &r.LastSeenAt,
		&r.EnrollState, &r.ExpectedHostname, &r.ExpectedIPv4, &r.ExpectedIPv6, &r.VerifyMode, &r.Hostname,
		&r.IPv4, &r.IPv6, &r.MachineIDHash, &r.EnrolledAt, &r.Group, &r.Note, &r.Provider, &r.Plan, &r.Region,
		&r.PriceCents, &r.Currency, &r.BillingPeriod, &r.ExpireDate, &r.CreatedAt)
	return &r, err
}

// GetServer 按 ID 读取节点；不存在时返回 errNoServer。
func (s *Store) GetServer(id int64) (*ServerRow, error) {
	r, err := scanServer(s.DB.QueryRow(`SELECT `+serverColumns+` FROM servers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoServer
	}
	return r, err
}

func getServerTx(tx *sql.Tx, id int64) (*ServerRow, error) {
	r, err := scanServer(tx.QueryRow(`SELECT `+serverColumns+` FROM servers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoServer
	}
	return r, err
}

// AuditEntry 是一条审计记录（设计 18.16、24.8）。
type AuditEntry struct {
	ActorType  string // admin / agent / cli / system
	ActorID    string
	Action     string // 如 server.create
	TargetType string
	TargetID   int64
	Success    bool
	ClientIP   string
	UserAgent  string
	Details    map[string]any
}

// Audit 追加一条审计记录。只追加，不提供修改或删除（设计 24.8）。
//
// 【安全】details 序列化后统一经过脱敏，即使调用方误放入凭证也不会完整落库（设计 24.7）。
func (s *Store) Audit(e AuditEntry, now time.Time) error {
	details := "{}"
	if len(e.Details) > 0 {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return err
		}
		details = logging.Redact(string(b))
	}
	result := "failure"
	if e.Success {
		result = "success"
	}
	target := ""
	if e.TargetID != 0 {
		target = strconv.FormatInt(e.TargetID, 10)
	}
	ua := e.UserAgent
	if len(ua) > 256 {
		ua = ua[:256] // 防止超长 User-Agent 撑大审计表
	}
	_, err := s.DB.Exec(`INSERT INTO audit_logs (ts, actor_type, actor_id, action, target_type, target_id, result,
		client_ip, user_agent, details) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		now.Unix(), e.ActorType, e.ActorID, e.Action, e.TargetType, target, result, e.ClientIP, logging.Redact(ua), details)
	return err
}
