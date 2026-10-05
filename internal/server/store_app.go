package server

// App 接入的持久化（设计 18.9、18.10）：配对 AK 与设备凭证。
//
// 【安全】AK、Access Token、Refresh Token 只保存 SHA-256 哈希（约束 4）；完整值只在创建 / 签发的响应中出现一次。

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
)

const (
	appAccessTTL    = 30 * time.Minute    // Access Token 有效期（设计 12.5）
	appRefreshTTL   = 90 * 24 * time.Hour // Refresh Token 有效期；每次刷新重新计算（设计 12.5）
	appReuseGrace   = time.Minute         // 轮换后旧 Refresh Token 的宽限期：上次响应丢失时允许重试
	appAccessKeyLen = 28                  // AK 随机字符数：28 × 5 bit = 140 bit ≥ 128 bit（设计 23.3）
)

// accessKeyFormat 匹配规范化后的 AK：MNT- 加 7 组 4 个 Crockford Base32 字符。
var accessKeyFormat = regexp.MustCompile(`^MNT(-[0-9A-Z]{4}){7}$`)

// newAccessKey 生成 AK，返回明文与脱敏提示（首尾两组，如 MNT-X7K9-****-W8QF）。
func newAccessKey() (key, hint string) {
	b := make([]byte, appAccessKeyLen)
	if _, err := rand.Read(b); err != nil {
		panic(err) // 系统随机源不可用时无法安全生成凭证，宁可失败
	}
	var sb strings.Builder
	sb.WriteString("MNT")
	for i, c := range b {
		if i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(crockford[c&31])
	}
	key = sb.String()
	return key, key[:8] + "-****-" + key[len(key)-4:]
}

// normalizeAccessKey 容忍手工输入：去掉空白、转为大写。
func normalizeAccessKey(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), ""))
}

// AppAccessKey 是一行 app_access_keys（不含哈希）。
type AppAccessKey struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Hint            string `json:"hint"`
	ScopeType       string `json:"scope_type"`
	ScopeValue      string `json:"scope_value"`
	AllowLowRiskOps bool   `json:"allow_low_risk_ops"`
	MaxDevices      int    `json:"max_devices"`
	PairedDevices   int    `json:"paired_devices"`
	ExpiresAt       int64  `json:"expires_at"`
	Status          string `json:"status"` // active / used / expired / revoked
	CreatedBy       string `json:"created_by"`
	CreatedAt       int64  `json:"created_at"`
	LastUsedAt      int64  `json:"last_used_at"`
	RevokedAt       int64  `json:"revoked_at"`
}

func (k *AppAccessKey) setStatus(now time.Time) {
	switch {
	case k.RevokedAt > 0:
		k.Status = "revoked"
	case k.PairedDevices >= k.MaxDevices:
		k.Status = "used"
	case k.ExpiresAt <= now.Unix():
		k.Status = "expired"
	default:
		k.Status = "active"
	}
}

const appKeyCols = `id, name, hint, scope_type, scope_value, allow_low_risk_ops, max_devices, paired_devices, expires_at,
	created_by, created_at, last_used_at, revoked_at`

func scanAppKey(sc interface{ Scan(...any) error }, now time.Time) (AppAccessKey, error) {
	var k AppAccessKey
	err := sc.Scan(&k.ID, &k.Name, &k.Hint, &k.ScopeType, &k.ScopeValue, &k.AllowLowRiskOps, &k.MaxDevices, &k.PairedDevices,
		&k.ExpiresAt, &k.CreatedBy, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt)
	k.setStatus(now)
	return k, err
}

// CreateAppAccessKey 生成 AK 并保存哈希，返回完整 AK（只此一次）。
func (s *Store) CreateAppAccessKey(k *AppAccessKey, now time.Time) (string, error) {
	key, hint := newAccessKey()
	k.Hint, k.CreatedAt = hint, now.Unix()
	res, err := s.DB.Exec(`INSERT INTO app_access_keys (name, key_hash, hint, scope_type, scope_value, allow_low_risk_ops,
		max_devices, expires_at, created_by, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		k.Name, HashToken(key), k.Hint, k.ScopeType, k.ScopeValue, k.AllowLowRiskOps, k.MaxDevices, k.ExpiresAt, k.CreatedBy, k.CreatedAt)
	if err != nil {
		return "", err
	}
	k.ID, _ = res.LastInsertId()
	k.setStatus(now)
	return key, nil
}

// ListAppAccessKeys 按创建时间倒序返回全部 AK。
func (s *Store) ListAppAccessKeys(now time.Time) ([]AppAccessKey, error) {
	rows, err := s.DB.Query(`SELECT ` + appKeyCols + ` FROM app_access_keys ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppAccessKey{}
	for rows.Next() {
		k, err := scanAppKey(rows, now)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

var errNoAppKey = errorf(CodeNotFound, "AK 不存在或已吊销")

// RevokeAppAccessKey 吊销 AK；revokeDevices 时同时吊销用它配对的设备，返回被吊销的设备 ID。
func (s *Store) RevokeAppAccessKey(id int64, revokeDevices bool, now time.Time) (AppAccessKey, []int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return AppAccessKey{}, nil, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE app_access_keys SET revoked_at = ? WHERE id = ? AND revoked_at = 0`, now.Unix(), id)
	if err != nil {
		return AppAccessKey{}, nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return AppAccessKey{}, nil, errNoAppKey
	}
	devices := []int64{}
	if revokeDevices {
		rows, err := tx.Query(`UPDATE app_devices SET revoked_at = ?, revoked_by = 'access_key', push_public_key = ''
			WHERE access_key_id = ? AND revoked_at = 0 RETURNING id`, now.Unix(), id)
		if err != nil {
			return AppAccessKey{}, nil, err
		}
		for rows.Next() {
			var d int64
			if err := rows.Scan(&d); err != nil {
				rows.Close()
				return AppAccessKey{}, nil, err
			}
			devices = append(devices, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return AppAccessKey{}, nil, err
		}
	}
	k, err := scanAppKey(tx.QueryRow(`SELECT `+appKeyCols+` FROM app_access_keys WHERE id = ?`, id), now)
	if err != nil {
		return AppAccessKey{}, nil, err
	}
	return k, devices, tx.Commit()
}

// AppDevice 是一行 app_devices（不含 Token 哈希）。
type AppDevice struct {
	ID              int64  `json:"id"`
	AccessKeyID     int64  `json:"access_key_id"`
	AccessKeyName   string `json:"access_key_name"`
	Name            string `json:"name"`
	Platform        string `json:"platform"`
	AppVersion      string `json:"app_version"`
	ScopeType       string `json:"scope_type"`
	ScopeValue      string `json:"scope_value"`
	AllowLowRiskOps bool   `json:"allow_low_risk_ops"`
	Status          string `json:"status"` // active / expired / revoked
	PairedAt        int64  `json:"paired_at"`
	LastSeenAt      int64  `json:"last_seen_at"`
	RevokedAt       int64  `json:"revoked_at"`
	RevokedBy       string `json:"revoked_by"`

	accessExpiresAt  int64
	refreshExpiresAt int64
}

func (d AppDevice) scope() *apiScope {
	return APIKey{ScopeType: d.ScopeType, ScopeValue: d.ScopeValue}.scope()
}

const appDeviceCols = `d.id, d.access_key_id, COALESCE(k.name, ''), d.name, d.platform, d.app_version, d.scope_type, d.scope_value,
	d.allow_low_risk_ops, d.paired_at, d.last_seen_at, d.revoked_at, d.revoked_by, d.access_expires_at, d.refresh_expires_at`

const appDeviceFrom = ` FROM app_devices d LEFT JOIN app_access_keys k ON k.id = d.access_key_id`

func scanAppDevice(sc interface{ Scan(...any) error }, now time.Time) (AppDevice, error) {
	var d AppDevice
	err := sc.Scan(&d.ID, &d.AccessKeyID, &d.AccessKeyName, &d.Name, &d.Platform, &d.AppVersion, &d.ScopeType, &d.ScopeValue,
		&d.AllowLowRiskOps, &d.PairedAt, &d.LastSeenAt, &d.RevokedAt, &d.RevokedBy, &d.accessExpiresAt, &d.refreshExpiresAt)
	switch {
	case d.RevokedAt > 0:
		d.Status = "revoked"
	case d.refreshExpiresAt <= now.Unix():
		d.Status = "expired"
	default:
		d.Status = "active"
	}
	return d, err
}

// appTokens 是签发给设备的一对凭证（明文只在响应中出现一次）。
type appTokens struct {
	access, refresh string
	accessExp       int64
	refreshExp      int64
}

func newAppTokens(now time.Time) appTokens {
	return appTokens{access: NewToken(PrefixDevice), refresh: NewToken(PrefixRefresh),
		accessExp: now.Add(appAccessTTL).Unix(), refreshExp: now.Add(appRefreshTTL).Unix()}
}

// PairRequest 是配对时设备自报的信息。
type PairRequest struct {
	KeyHash       string
	Name          string
	Platform      string
	AppVersion    string
	PushPublicKey string
}

var errAccessKeyInvalid = errors.New("access key invalid")

// PairDevice 校验 AK（未吊销、未过期、未用完）并创建设备，在同一事务中占用一个配对名额。
// 设备的授权范围从 AK 复制（设计 18.10）。AK 无效时返回 errAccessKeyInvalid，不区分原因。
func (s *Store) PairDevice(req PairRequest, now time.Time) (AppDevice, appTokens, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return AppDevice{}, appTokens{}, err
	}
	defer tx.Rollback()
	k, err := scanAppKey(tx.QueryRow(`SELECT `+appKeyCols+` FROM app_access_keys WHERE key_hash = ?`, req.KeyHash), now)
	if errors.Is(err, sql.ErrNoRows) {
		return AppDevice{}, appTokens{}, errAccessKeyInvalid
	}
	if err != nil {
		return AppDevice{}, appTokens{}, err
	}
	if k.Status != "active" {
		return AppDevice{}, appTokens{}, errAccessKeyInvalid
	}
	// 条件更新防止并发配对超过名额
	res, err := tx.Exec(`UPDATE app_access_keys SET paired_devices = paired_devices + 1, last_used_at = ?
		WHERE id = ? AND paired_devices < max_devices AND revoked_at = 0`, now.Unix(), k.ID)
	if err != nil {
		return AppDevice{}, appTokens{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return AppDevice{}, appTokens{}, errAccessKeyInvalid
	}
	t := newAppTokens(now)
	res, err = tx.Exec(`INSERT INTO app_devices (access_key_id, name, platform, app_version, scope_type, scope_value,
		allow_low_risk_ops, access_hash, access_expires_at, refresh_hash, refresh_expires_at, push_public_key, paired_at, last_seen_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, k.ID, req.Name, req.Platform, req.AppVersion, k.ScopeType, k.ScopeValue,
		k.AllowLowRiskOps, HashToken(t.access), t.accessExp, HashToken(t.refresh), t.refreshExp, req.PushPublicKey, now.Unix(), now.Unix())
	if err != nil {
		return AppDevice{}, appTokens{}, err
	}
	id, _ := res.LastInsertId()
	d, err := scanAppDevice(tx.QueryRow(`SELECT `+appDeviceCols+appDeviceFrom+` WHERE d.id = ?`, id), now)
	if err != nil {
		return AppDevice{}, appTokens{}, err
	}
	return d, t, tx.Commit()
}

// LookupDeviceByAccess 按 Access Token 查找设备（含已吊销、已过期的，由调用方区分原因）；不存在时返回 nil。
func (s *Store) LookupDeviceByAccess(tok string, now time.Time) (*AppDevice, error) {
	d, err := scanAppDevice(s.DB.QueryRow(`SELECT `+appDeviceCols+appDeviceFrom+` WHERE d.access_hash = ?`, HashToken(tok)), now)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// GetAppDevice 按 ID 读取设备。
func (s *Store) GetAppDevice(id int64, now time.Time) (*AppDevice, error) {
	d, err := scanAppDevice(s.DB.QueryRow(`SELECT `+appDeviceCols+appDeviceFrom+` WHERE d.id = ?`, id), now)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errorf(CodeNotFound, "设备不存在")
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// refreshOutcome 是一次刷新的结果。
type refreshOutcome int

const (
	refreshOK      refreshOutcome = iota
	refreshInvalid                // 不认识的 Token，或设备已吊销 / 过期
	refreshReused                 // 轮换后超过宽限期仍使用旧 Token：视为泄露，设备已被吊销
)

// RefreshDevice 用 Refresh Token 轮换凭证（设计 12.5）：旧 Token 立即失效。
// 旧 Token 在轮换后 appReuseGrace 内再次出现（上次响应丢失）时重新签发；超过宽限期则吊销设备。
// 条件更新（refresh_hash 未变）保证并发刷新只有一个成功。
func (s *Store) RefreshDevice(tok, appVersion string, now time.Time) (AppDevice, appTokens, refreshOutcome, error) {
	h := HashToken(tok)
	var (
		id                    int64
		revokedAt, refreshExp int64
		curHash               string
		rotatedAt             int64
		matchedPrev           bool
	)
	err := s.DB.QueryRow(`SELECT id, revoked_at, refresh_expires_at, refresh_hash, rotated_at FROM app_devices
		WHERE refresh_hash = ?`, h).Scan(&id, &revokedAt, &refreshExp, &curHash, &rotatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		matchedPrev = true
		err = s.DB.QueryRow(`SELECT id, revoked_at, refresh_expires_at, refresh_hash, rotated_at FROM app_devices
			WHERE prev_refresh_hash = ?`, h).Scan(&id, &revokedAt, &refreshExp, &curHash, &rotatedAt)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return AppDevice{}, appTokens{}, refreshInvalid, nil
	}
	if err != nil {
		return AppDevice{}, appTokens{}, 0, err
	}
	if revokedAt > 0 || refreshExp <= now.Unix() {
		return AppDevice{}, appTokens{}, refreshInvalid, nil
	}
	if matchedPrev && now.Unix()-rotatedAt > int64(appReuseGrace/time.Second) {
		// 【安全】旧 Refresh Token 在宽限期后再次出现：可能已被复制，吊销设备（双方都需要重新配对）
		_, err := s.DB.Exec(`UPDATE app_devices SET revoked_at = ?, revoked_by = 'refresh_reuse', push_public_key = ''
			WHERE id = ? AND revoked_at = 0`, now.Unix(), id)
		return AppDevice{ID: id}, appTokens{}, refreshReused, err
	}
	t := newAppTokens(now)
	// 宽限期内重试时 prev 仍是这个旧 Token，rotated_at 不变：宽限期从第一次轮换算起，反复重试不会延长
	rotated := now.Unix()
	if matchedPrev {
		rotated = rotatedAt
	}
	q := `UPDATE app_devices SET access_hash = ?, access_expires_at = ?, refresh_hash = ?, refresh_expires_at = ?,
		prev_refresh_hash = ?, rotated_at = ?, last_seen_at = ?, app_version = COALESCE(NULLIF(?, ''), app_version)
		WHERE id = ? AND refresh_hash = ? AND revoked_at = 0`
	args := []any{HashToken(t.access), t.accessExp, HashToken(t.refresh), t.refreshExp, h, rotated, now.Unix(), appVersion,
		id, curHash}
	res, err := s.DB.Exec(q, args...)
	if err != nil {
		return AppDevice{}, appTokens{}, 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return AppDevice{}, appTokens{}, refreshInvalid, nil // 并发刷新已抢先轮换
	}
	d, err := s.GetAppDevice(id, now)
	if err != nil {
		return AppDevice{}, appTokens{}, 0, err
	}
	return *d, t, refreshOK, nil
}

// ListAppDevices 返回全部设备：有效的在前，其次按配对时间倒序。
func (s *Store) ListAppDevices(now time.Time) ([]AppDevice, error) {
	rows, err := s.DB.Query(`SELECT ` + appDeviceCols + appDeviceFrom + ` ORDER BY d.revoked_at <> 0, d.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppDevice{}
	for rows.Next() {
		d, err := scanAppDevice(rows, now)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevokeAppDevice 吊销设备：Token 立即失效，Push 公钥清空（设计 19.4）。by 记录原因（admin / app）。
func (s *Store) RevokeAppDevice(id int64, by string, now time.Time) (*AppDevice, error) {
	res, err := s.DB.Exec(`UPDATE app_devices SET revoked_at = ?, revoked_by = ?, push_public_key = ''
		WHERE id = ? AND revoked_at = 0`, now.Unix(), by, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errorf(CodeNotFound, "设备不存在或已吊销")
	}
	return s.GetAppDevice(id, now)
}

// TouchAppDevice 更新最近使用时间（调用方节流，每分钟最多一次）。
func (s *Store) TouchAppDevice(id int64, now time.Time) error {
	_, err := s.DB.Exec(`UPDATE app_devices SET last_seen_at = ? WHERE id = ?`, now.Unix(), id)
	return err
}

// AppDeviceActive 用于 WebSocket 的周期校验：设备未吊销且 Refresh Token 未过期。
func (s *Store) AppDeviceActive(id int64, now time.Time) bool {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM app_devices WHERE id = ? AND revoked_at = 0 AND refresh_expires_at > ?`,
		id, now.Unix()).Scan(&n)
	return err == nil && n == 1
}
