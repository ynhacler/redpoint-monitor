package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"vpsmon/internal/release"
)

// 远程升级任务（设计 29.2～29.20）。
//
// 【安全】面板只能“选择”一个已同步并验签的官方版本作为目标；下发给 Agent 的是官方签名的清单原文与签名，
// Agent 与特权 updater 都会用内置公钥独立验签、拒绝降级（设计 29.7）。这里没有任何执行命令或上传文件的能力
// （设计 29.21、CLAUDE.md 约束 1、2）。只有 Web 管理员能创建或取消任务；App 凭证不能（约束 5）。

// 升级任务状态（设计 29.10，按 Agent 侧的实际步骤简化）。
const (
	UpgradePending    = "pending"     // 已创建，等待 Agent 下次检查（默认每 5 分钟）
	UpgradeDelivered  = "delivered"   // Agent 已获取任务
	UpgradeStaged     = "staged"      // Agent 已下载并校验，交给特权 updater
	UpgradeSuccess    = "success"     // 新版本已成功上报
	UpgradeFailed     = "failed"      // 校验、下载或安装失败（未改动或已恢复旧版本）
	UpgradeRolledBack = "rolled_back" // 健康检查失败，已回滚
	UpgradeCancelled  = "cancelled"
)

// upgradeTaskTimeout：超过 1 小时仍未结束的任务判为失败（Agent 离线、下载过慢等）。
// upgradeClaimTimeout：Agent 每 5 分钟检查一次任务，15 分钟仍未领取说明它不会来取（离线、未启用远程升级或版本过旧）。
const (
	upgradeTaskTimeout  = time.Hour
	upgradeClaimTimeout = 15 * time.Minute
)

// minRemoteUpgradeVersion 是支持远程升级的最低 Agent 版本：更早的版本不会查询升级任务（设计 29.13）。
var minRemoteUpgradeVersion, _ = release.ParseVersion("0.3.0")

// remoteUpgradeBlocker 说明节点为什么不能远程升级；能升级（或无法判断）时返回空。
func remoteUpgradeBlocker(cur string, enabled *bool) string {
	if cv, err := release.ParseVersion(cur); err == nil && cv.Compare(minRemoteUpgradeVersion) < 0 {
		return "Agent " + cur + " 不支持远程升级（v0.3.0 起支持）：请在主机上执行 sudo vpsmon-agent upgrade 升级一次"
	}
	if enabled != nil && !*enabled {
		return "主机未启用远程升级：在主机上执行 sudo vpsmon-agent enable-remote-upgrade，或用 sudo vpsmon-agent upgrade 本机升级"
	}
	return ""
}

func upgradeActive(status string) bool {
	return status == UpgradePending || status == UpgradeDelivered || status == UpgradeStaged
}

// UpgradeTask 是一个节点的升级任务。
type UpgradeTask struct {
	ID            int64  `json:"id"`
	ServerID      int64  `json:"server_id"`
	ServerName    string `json:"server_name"`
	TargetVersion string `json:"target_version"`
	FromVersion   string `json:"from_version"`
	Status        string `json:"status"`
	Reason        string `json:"reason"`
	CreatedBy     string `json:"created_by"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
	RolloutID     int64  `json:"rollout_id"` // 所属灰度升级；0 表示单独创建（设计 29.16）
	Stage         int    `json:"stage"`
}

const upgradeTaskColumns = `t.id, t.server_id, COALESCE(s.name, ''), t.target_version, t.from_version, t.status, t.reason,
	t.created_by, t.created_at, t.updated_at, t.rollout_id, t.stage`

func scanUpgradeTask(sc interface{ Scan(...any) error }) (UpgradeTask, error) {
	var t UpgradeTask
	err := sc.Scan(&t.ID, &t.ServerID, &t.ServerName, &t.TargetVersion, &t.FromVersion, &t.Status, &t.Reason,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &t.RolloutID, &t.Stage)
	return t, err
}

// ListUpgradeTasks 返回任务，最新在前；serverID 为 0 表示全部节点。
func (s *Store) ListUpgradeTasks(serverID int64, limit int) ([]UpgradeTask, error) {
	q := `SELECT ` + upgradeTaskColumns + ` FROM upgrade_tasks t LEFT JOIN servers s ON s.id = t.server_id`
	args := []any{}
	if serverID > 0 {
		q += ` WHERE t.server_id = ?`
		args = append(args, serverID)
	}
	q += ` ORDER BY t.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UpgradeTask{}
	for rows.Next() {
		t, err := scanUpgradeTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ActiveUpgradeTask 返回节点进行中的任务；没有时返回 nil。
func (s *Store) ActiveUpgradeTask(serverID int64) (*UpgradeTask, error) {
	t, err := scanUpgradeTask(s.DB.QueryRow(`SELECT `+upgradeTaskColumns+` FROM upgrade_tasks t LEFT JOIN servers s ON s.id = t.server_id
		WHERE t.server_id = ? AND t.status IN (?,?,?) ORDER BY t.id DESC LIMIT 1`, serverID, UpgradePending, UpgradeDelivered, UpgradeStaged))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &t, err
}

// CreateUpgradeTask 新建任务；节点已有进行中的任务时返回 errUpgradeBusy。
func (s *Store) CreateUpgradeTask(serverID int64, target, from, by string, now time.Time) (int64, error) {
	return s.createUpgradeTask(serverID, target, from, by, 0, 0, now)
}

func (s *Store) createUpgradeTask(serverID int64, target, from, by string, rolloutID int64, stage int, now time.Time) (int64, error) {
	if t, err := s.ActiveUpgradeTask(serverID); err != nil {
		return 0, err
	} else if t != nil {
		return 0, errUpgradeBusy
	}
	res, err := s.DB.Exec(`INSERT INTO upgrade_tasks (server_id, target_version, from_version, status, created_by, created_at, updated_at,
		rollout_id, stage) VALUES (?,?,?,?,?,?,?,?,?)`, serverID, target, from, UpgradePending, by, now.Unix(), now.Unix(), rolloutID, stage)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetUpgradeStatus 更新任务状态；只更新属于 serverID 且仍在进行中的任务（Agent 只能改自己的任务）。
func (s *Store) SetUpgradeStatus(id, serverID int64, status, reason string, now time.Time) (bool, error) {
	q := `UPDATE upgrade_tasks SET status = ?, reason = ?, updated_at = ? WHERE id = ? AND status IN (?,?,?)`
	args := []any{status, reason, now.Unix(), id, UpgradePending, UpgradeDelivered, UpgradeStaged}
	if serverID > 0 {
		q += ` AND server_id = ?`
		args = append(args, serverID)
	}
	res, err := s.DB.Exec(q, args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ExpireUpgradeTasks 把超时仍未结束的任务判为失败。
// 15 分钟没有被领取的任务单独判为失败并说明原因：Agent 不会来取，没有必要等满 1 小时。
func (s *Store) ExpireUpgradeTasks(now time.Time) (int64, error) {
	res, err := s.DB.Exec(`UPDATE upgrade_tasks SET status = ?, reason = ?, updated_at = ?
		WHERE status = ? AND updated_at < ?`, UpgradeFailed,
		"Agent 15 分钟内没有领取任务：节点离线、主机未启用远程升级（sudo vpsmon-agent enable-remote-upgrade），"+
			"或 Agent 版本过旧（v0.3.0 之前不支持远程升级，请在主机上执行 sudo vpsmon-agent upgrade）",
		now.Unix(), UpgradePending, now.Add(-upgradeClaimTimeout).Unix())
	if err != nil {
		return 0, err
	}
	claimed, _ := res.RowsAffected()
	res, err = s.DB.Exec(`UPDATE upgrade_tasks SET status = ?, reason = ?, updated_at = ?
		WHERE status IN (?,?) AND updated_at < ?`, UpgradeFailed, "超时：1 小时内没有完成（节点离线或下载失败）", now.Unix(),
		UpgradeDelivered, UpgradeStaged, now.Add(-upgradeTaskTimeout).Unix())
	if err != nil {
		return claimed, err
	}
	n, _ := res.RowsAffected()
	return claimed + n, nil
}

var (
	errUpgradeBusy    = errorf(CodeConflict, "该节点已有进行中的升级任务")
	errNoUpgradeTask  = errorf(CodeNotFound, "升级任务不存在或已结束")
	errReleaseUnknown = errorf(CodeValidationFailed, "目标版本不是已同步并验签的官方版本")
)

// releaseByVersion 返回已验签的版本（每次重新验签）及其清单原文与签名。
func (s *Server) releaseByVersion(version string) (*agentRelease, []byte, []byte, error) {
	var data []byte
	var sig string
	var mirrored int64
	err := s.store.DB.QueryRow(`SELECT manifest, signature, mirrored_at FROM agent_releases WHERE version = ?`, version).Scan(&data, &sig, &mirrored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, errReleaseUnknown
	} else if err != nil {
		return nil, nil, nil, err
	}
	m, err := release.VerifyManifest(s.releaseKeys, data, []byte(sig))
	if err != nil {
		return nil, nil, nil, errReleaseUnknown
	}
	return &agentRelease{Version: m.Version, Channel: m.Channel, Mirrored: mirrored > 0 && s.mirrorRoot != ""}, data, []byte(sig), nil
}

// agentUpgradeInfo 返回节点最新上报的 Agent 版本与“是否启用远程升级”（旧版 Agent 不报为 nil）。
func (s *Server) agentUpgradeInfo(id int64) (string, *bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sn := s.latest[id]; sn != nil {
		return sn.Report.AgentVersion, sn.Report.System.RemoteUpgrade
	}
	return "", nil
}

// currentAgentVersion 返回节点最新上报的 Agent 版本；没有上报时为空。
func (s *Server) currentAgentVersion(id int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sn := s.latest[id]; sn != nil {
		return sn.Report.AgentVersion
	}
	return ""
}

// upgradeEligibility 检查节点能否升级到 v：返回节点、当前版本；不能升级时 why 说明原因（单独任务与灰度升级共用）。
func (s *Server) upgradeEligibility(id int64, v release.Version) (row *ServerRow, cur, why string) {
	row, err := s.store.GetServer(id)
	if err != nil {
		return nil, "", "节点不存在"
	}
	if row.EnrollState == enrollPending {
		return row, "", "尚未安装 Agent"
	}
	cur, remote := s.agentUpgradeInfo(id)
	if cv, err := release.ParseVersion(cur); err == nil && cv.Compare(v) >= 0 {
		return row, cur, "当前版本 " + cur + " 不低于目标版本"
	}
	return row, cur, remoteUpgradeBlocker(cur, remote)
}

// handleUpgradeTasks：GET /api/v1/upgrade-tasks?server_id=，admin。最近 100 个任务，最新在前。
func (s *Server) handleUpgradeTasks(w http.ResponseWriter, r *http.Request) {
	var sid int64
	if v := r.URL.Query().Get("server_id"); v != "" {
		var err error
		if sid, err = parsePositive(v); err != nil {
			s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "server_id", Message: "server_id 无效"}}})
			return
		}
	}
	list, err := s.store.ListUpgradeTasks(sid, 100)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, list, "", nil)
}

// handleCreateUpgradeTasks：POST /api/v1/upgrade-tasks，admin（设计 29.14）。
// 请求 {"server_ids": [1, 2], "version": "0.3.0"}：为每个节点创建升级任务。版本必须是已同步并验签的官方版本；
// 不高于节点当前版本、待安装、已有进行中任务的节点会被跳过并说明原因。返回 {"created": [...], "skipped": [...]}。
func (s *Server) handleCreateUpgradeTasks(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ServerIDs []int64 `json:"server_ids"`
		Version   string  `json:"version"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	if len(b.ServerIDs) == 0 || len(b.ServerIDs) > 500 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "server_ids", Message: "请选择 1～500 个节点"}}})
		return
	}
	v, err := release.ParseVersion(b.Version)
	if err != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "version", Message: "版本号格式无效"}}})
		return
	}
	rel, _, _, err := s.releaseByVersion(v.String())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	by := "admin"
	if se := info(r).session; se != nil {
		by = "admin:" + se.User.Username
	}
	type skipped struct {
		ServerID int64  `json:"server_id"`
		Reason   string `json:"reason"`
	}
	created, skips := []UpgradeTask{}, []skipped{}
	now := time.Now()
	for _, id := range b.ServerIDs {
		row, cur, why := s.upgradeEligibility(id, v)
		if why != "" {
			skips = append(skips, skipped{id, why})
			continue
		}
		tid, err := s.store.CreateUpgradeTask(id, rel.Version, cur, by, now)
		if err != nil {
			skips = append(skips, skipped{id, asAPIError(err).Message})
			continue
		}
		created = append(created, UpgradeTask{ID: tid, ServerID: id, ServerName: row.Name, TargetVersion: rel.Version,
			FromVersion: cur, Status: UpgradePending, CreatedBy: by, CreatedAt: now.Unix(), UpdatedAt: now.Unix()})
		s.audit(r, AuditEntry{ActorType: "admin", Action: "upgrade_task.create", TargetType: "server", TargetID: id, Success: true,
			Details: map[string]any{"task_id": tid, "from": cur, "to": rel.Version}})
	}
	writeJSONStatus(w, http.StatusCreated, map[string]any{"created": created, "skipped": skips})
}

// handleCancelUpgradeTask：POST /api/v1/upgrade-tasks/{id}/cancel，admin。只能取消尚未交给 updater 的任务；成功 204。
func (s *Server) handleCancelUpgradeTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	res, err := s.store.DB.Exec(`UPDATE upgrade_tasks SET status = ?, updated_at = ? WHERE id = ? AND status IN (?,?)`,
		UpgradeCancelled, time.Now().Unix(), id, UpgradePending, UpgradeDelivered)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.writeError(w, r, errorf(CodeConflict, "任务不存在、已结束，或 Agent 已开始安装，不能取消"))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "upgrade_task.cancel", Success: true, Details: map[string]any{"task_id": id}})
	w.WriteHeader(http.StatusNoContent)
}

// handleAgentUpgrade：GET /api/v1/agent/upgrade，Agent Token（设计 29.4、29.5）。
// 有进行中的任务时返回目标版本的官方签名清单原文与签名（Agent 独立验签）；否则 {"upgrade": false}。
func (s *Server) handleAgentUpgrade(w http.ResponseWriter, r *http.Request) {
	sid := info(r).principalID
	t, err := s.store.ActiveUpgradeTask(sid)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if t == nil {
		writeJSON(w, map[string]any{"upgrade": false})
		return
	}
	rel, data, sig, err := s.releaseByVersion(t.TargetVersion)
	if err != nil {
		// 版本已不可用（数据库被改动或公钥轮换）：任务失败，不下发任何内容
		s.store.SetUpgradeStatus(t.ID, sid, UpgradeFailed, "目标版本的签名清单已不可用", time.Now())
		writeJSON(w, map[string]any{"upgrade": false})
		return
	}
	if t.Status == UpgradePending {
		s.store.SetUpgradeStatus(t.ID, sid, UpgradeDelivered, "", time.Now())
	}
	resp := map[string]any{"upgrade": true, "task_id": t.ID, "version": t.TargetVersion,
		"manifest": data, "manifest_signature": string(sig)}
	// 已镜像时让 Agent 从本面板下载（路径相对于 Agent 配置的面板地址；完整性由签名清单保证，设计 27.5.3）
	if rel.Mirrored {
		resp["mirror_path"] = "/releases"
	}
	writeJSON(w, resp)
}

// handleAgentUpgradeStatus：POST /api/v1/agent/upgrade/status，Agent Token（设计 29.12、29.20）。
// 请求 {"task_id", "status": "staged|success|failed|rolled_back", "reason"}；只能更新本节点进行中的任务。成功 204。
func (s *Server) handleAgentUpgradeStatus(w http.ResponseWriter, r *http.Request) {
	sid := info(r).principalID
	var b struct {
		TaskID int64  `json:"task_id"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	switch b.Status {
	case UpgradeStaged, UpgradeSuccess, UpgradeFailed, UpgradeRolledBack:
	default:
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "status", Message: "状态无效"}}})
		return
	}
	reason := strings.TrimSpace(b.Reason)
	if utf8.RuneCountInString(reason) > 300 {
		reason = string([]rune(reason)[:300])
	}
	ok, err := s.store.SetUpgradeStatus(b.TaskID, sid, b.Status, reason, time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if !ok {
		s.writeError(w, r, errNoUpgradeTask)
		return
	}
	if b.Status != UpgradeStaged {
		s.audit(r, AuditEntry{ActorType: "agent", ActorID: itoa64(sid), Action: "upgrade_task.result", TargetType: "server",
			TargetID: sid, Success: b.Status == UpgradeSuccess, Details: map[string]any{"task_id": b.TaskID, "status": b.Status, "reason": reason}})
	}
	w.WriteHeader(http.StatusNoContent)
}

func parsePositive(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, errors.New("invalid")
	}
	return n, nil
}
