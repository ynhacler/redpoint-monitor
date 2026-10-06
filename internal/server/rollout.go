package server

// 灰度升级（设计 29.16）：按顺序分批创建升级任务；每批全部结束后观察一段时间，
// 本批失败过多或已升级的节点离线时自动暂停，否则开始下一批，最后一批结束后完成。
//
// 【安全】灰度升级只是按批次创建普通的升级任务：目标版本同样必须是已同步并验签的官方版本，
// 节点上的 Agent 与 updater 独立验签、拒绝降级（设计 29.7、29.13）。只有 Web 管理员能创建与操作（约束 5）。
//
// 判断逻辑在纯函数 stageTargets、decideRollout 中（表格测试）；调度在 checkRollouts（每分钟）。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"vpsmon/internal/release"
)

// 灰度升级状态
const (
	RolloutRunning   = "running"
	RolloutPaused    = "paused" // 手动或自动暂停（原因在 reason）
	RolloutCompleted = "completed"
	RolloutCancelled = "cancelled"
)

// RolloutStage 是一批的累计规模：Count 台，或全部参与节点的 Percent%（向上取整）。
type RolloutStage struct {
	Count   int `json:"count,omitempty"`
	Percent int `json:"percent,omitempty"`
}

// rolloutSkip 是轮到时不能升级的节点与原因。
type rolloutSkip struct {
	ServerID int64  `json:"server_id"`
	Reason   string `json:"reason"`
}

// stageProgress 是一批的任务统计（接口返回）。
type stageProgress struct {
	Stage   int `json:"stage"`
	Target  int `json:"target"`
	Tasks   int `json:"tasks"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Active  int `json:"active"`
}

// UpgradeRollout 是一次灰度升级。
type UpgradeRollout struct {
	ID             int64           `json:"id"`
	TargetVersion  string          `json:"target_version"`
	Status         string          `json:"status"`
	Stages         []RolloutStage  `json:"stages"`
	ObserveMinutes int             `json:"observe_minutes"`
	MaxFailures    int             `json:"max_failures"`
	Total          int             `json:"total"`
	CurrentStage   int             `json:"current_stage"`
	StageDoneAt    int64           `json:"stage_done_at"`
	Reason         string          `json:"reason"`
	Skipped        []rolloutSkip   `json:"skipped"`
	Progress       []stageProgress `json:"progress"`
	CreatedBy      string          `json:"created_by"`
	CreatedAt      int64           `json:"created_at"`
	UpdatedAt      int64           `json:"updated_at"`

	serverIDs   []int64
	ackFailures int
	ackOffline  []int64
}

// stageTargets 返回每批结束时累计应升级的台数：不递减，不超过 total，最后一批总是 total。
func stageTargets(stages []RolloutStage, total int) []int {
	out := make([]int, len(stages))
	prev := 0
	for i, st := range stages {
		n := st.Count
		if st.Percent > 0 {
			n = int(math.Ceil(float64(total) * float64(st.Percent) / 100))
		}
		n = min(max(n, prev), total)
		if i == len(stages)-1 {
			n = total
		}
		out[i] = n
		prev = n
	}
	return out
}

// stageStat 是当前批的任务统计。
type stageStat struct {
	Tasks, Success, Failed, Active int
}

// 灰度升级的下一步
const (
	rolloutWait     = "wait"
	rolloutPause    = "pause"
	rolloutAdvance  = "advance"
	rolloutComplete = "complete"
)

// decideRollout 决定运行中的灰度升级下一步做什么（纯函数）。
//   - 本批还有进行中的任务：等待（doneAt 为 0）
//   - 本批全部结束：记录结束时间 doneAt；失败（失败 + 回滚）超过 maxFailures + ackFailures，或有已升级的节点离线：暂停
//   - 最后一批：完成；本批没有任务（全部被跳过）：不观察，直接下一批；否则观察期满后下一批
func decideRollout(stage, nStages, maxFailures, ackFailures int, observe time.Duration, stageDoneAt int64,
	st stageStat, offline []string, now time.Time) (action, reason string, doneAt int64) {
	if st.Active > 0 {
		return rolloutWait, "", 0
	}
	doneAt = stageDoneAt
	if doneAt == 0 {
		doneAt = now.Unix()
	}
	if st.Failed > maxFailures+ackFailures {
		return rolloutPause, fmt.Sprintf("第 %d 批有 %d 台升级失败或回滚（允许 %d 台），已自动暂停", stage, st.Failed, maxFailures), doneAt
	}
	if len(offline) > 0 {
		return rolloutPause, "已升级的节点离线：" + strings.Join(offline, "、") + "，已自动暂停", doneAt
	}
	if stage >= nStages {
		return rolloutComplete, "全部批次已完成", doneAt
	}
	if st.Tasks > 0 && now.Before(time.Unix(doneAt, 0).Add(observe)) {
		return rolloutWait, "", doneAt
	}
	return rolloutAdvance, "", doneAt
}

// ---- 存储 ----

const rolloutColumns = `id, target_version, status, stages, observe_minutes, max_failures, server_ids, current_stage, stage_done_at,
	reason, skipped, ack_failures, ack_offline, created_by, created_at, updated_at`

func scanRollout(sc interface{ Scan(...any) error }) (*UpgradeRollout, error) {
	var r UpgradeRollout
	var stages, ids, skipped, ackOffline string
	if err := sc.Scan(&r.ID, &r.TargetVersion, &r.Status, &stages, &r.ObserveMinutes, &r.MaxFailures, &ids, &r.CurrentStage,
		&r.StageDoneAt, &r.Reason, &skipped, &r.ackFailures, &ackOffline, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(stages), &r.Stages)
	_ = json.Unmarshal([]byte(ids), &r.serverIDs)
	_ = json.Unmarshal([]byte(skipped), &r.Skipped)
	_ = json.Unmarshal([]byte(ackOffline), &r.ackOffline)
	if r.Skipped == nil {
		r.Skipped = []rolloutSkip{}
	}
	r.Total = len(r.serverIDs)
	return &r, nil
}

func (s *Store) GetRollout(id int64) (*UpgradeRollout, error) {
	r, err := scanRollout(s.DB.QueryRow(`SELECT `+rolloutColumns+` FROM upgrade_rollouts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errorf(CodeNotFound, "灰度升级不存在")
	}
	return r, err
}

func (s *Store) ListRollouts(limit int) ([]*UpgradeRollout, error) {
	rows, err := s.DB.Query(`SELECT `+rolloutColumns+` FROM upgrade_rollouts ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*UpgradeRollout{}
	for rows.Next() {
		r, err := scanRollout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// activeRollouts 返回运行中的灰度升级（调度只处理这些）。
func (s *Store) activeRollouts() ([]*UpgradeRollout, error) {
	rows, err := s.DB.Query(`SELECT `+rolloutColumns+` FROM upgrade_rollouts WHERE status = ? ORDER BY id`, RolloutRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*UpgradeRollout
	for rows.Next() {
		r, err := scanRollout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) saveRollout(r *UpgradeRollout, now time.Time) error {
	skipped, _ := json.Marshal(r.Skipped)
	ack, _ := json.Marshal(r.ackOffline)
	r.UpdatedAt = now.Unix()
	_, err := s.DB.Exec(`UPDATE upgrade_rollouts SET status = ?, current_stage = ?, stage_done_at = ?, reason = ?, skipped = ?,
		ack_failures = ?, ack_offline = ?, updated_at = ? WHERE id = ?`,
		r.Status, r.CurrentStage, r.StageDoneAt, r.Reason, string(skipped), r.ackFailures, string(ack), r.UpdatedAt, r.ID)
	return err
}

// rolloutTasks 返回灰度升级的全部任务。
func (s *Store) rolloutTasks(id int64) ([]UpgradeTask, error) {
	rows, err := s.DB.Query(`SELECT `+upgradeTaskColumns+` FROM upgrade_tasks t LEFT JOIN servers s ON s.id = t.server_id
		WHERE t.rollout_id = ? ORDER BY t.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UpgradeTask
	for rows.Next() {
		t, err := scanUpgradeTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// statOf 统计某一批的任务。
func statOf(tasks []UpgradeTask, stage int) stageStat {
	var st stageStat
	for _, t := range tasks {
		if t.Stage != stage {
			continue
		}
		st.Tasks++
		switch {
		case t.Status == UpgradeSuccess:
			st.Success++
		case t.Status == UpgradeFailed || t.Status == UpgradeRolledBack:
			st.Failed++
		case upgradeActive(t.Status):
			st.Active++
		}
	}
	return st
}

// fillProgress 计算每批的计划台数与任务统计。
func (r *UpgradeRollout) fillProgress(tasks []UpgradeTask) {
	targets := stageTargets(r.Stages, r.Total)
	r.Progress = make([]stageProgress, len(targets))
	prev := 0
	for i, t := range targets {
		st := statOf(tasks, i+1)
		r.Progress[i] = stageProgress{Stage: i + 1, Target: t - prev, Tasks: st.Tasks, Success: st.Success, Failed: st.Failed, Active: st.Active}
		prev = t
	}
}

// ---- 调度 ----

// startStage 为第 stage 批创建任务：按顺序取尚未处理的节点，直到累计任务数达到本批目标；不能升级的节点记入 skipped。
// 返回本批新建的任务数，以及是否还有未处理的节点。
func (s *Server) startStage(r *UpgradeRollout, stage int, tasks []UpgradeTask, now time.Time) (created int, remaining bool, err error) {
	v, err := release.ParseVersion(r.TargetVersion)
	if err != nil {
		return 0, false, err
	}
	done := map[int64]bool{}
	for _, t := range tasks {
		done[t.ServerID] = true
	}
	for _, sk := range r.Skipped {
		done[sk.ServerID] = true
	}
	target := stageTargets(r.Stages, r.Total)[stage-1]
	total := len(tasks)
	for _, id := range r.serverIDs {
		if done[id] {
			continue
		}
		if total >= target {
			return created, true, nil
		}
		done[id] = true
		_, cur, why := s.upgradeEligibility(id, v)
		if why == "" {
			if _, err := s.store.createUpgradeTask(id, r.TargetVersion, cur, r.CreatedBy, r.ID, stage, now); err != nil {
				why = asAPIError(err).Message
			} else {
				created++
				total++
				continue
			}
		}
		r.Skipped = append(r.Skipped, rolloutSkip{ServerID: id, Reason: why})
	}
	return created, false, nil
}

// offlineUpgraded 返回本次已升级成功、但现在离线的节点名称（不含已确认继续的节点）。
func (s *Server) offlineUpgraded(r *UpgradeRollout, tasks []UpgradeTask, now time.Time) ([]string, []int64) {
	ack := map[int64]bool{}
	for _, id := range r.ackOffline {
		ack[id] = true
	}
	var names []string
	var ids []int64
	for _, t := range tasks {
		if t.Status != UpgradeSuccess || ack[t.ServerID] {
			continue
		}
		row, err := s.store.GetServer(t.ServerID)
		if err != nil {
			continue // 节点已删除
		}
		if v, err := s.viewOf(*row, now); err == nil && v.Status == "offline" {
			names = append(names, row.Name)
			ids = append(ids, t.ServerID)
		}
	}
	return names, ids
}

// checkRollouts 推进运行中的灰度升级（每分钟由维护循环调用）。
func (s *Server) checkRollouts(now time.Time) {
	list, err := s.store.activeRollouts()
	if err != nil {
		s.log.Error("rollout list failed", "component", "upgrade", "err", err)
		return
	}
	for _, r := range list {
		if err := s.stepRollout(r, now); err != nil {
			s.log.Error("rollout step failed", "component", "upgrade", "rollout", r.ID, "err", err)
		}
	}
}

func (s *Server) stepRollout(r *UpgradeRollout, now time.Time) error {
	tasks, err := s.store.rolloutTasks(r.ID)
	if err != nil {
		return err
	}
	st := statOf(tasks, r.CurrentStage)
	offline, _ := s.offlineUpgraded(r, tasks, now)
	action, reason, doneAt := decideRollout(r.CurrentStage, len(r.Stages), r.MaxFailures, r.ackFailures,
		time.Duration(r.ObserveMinutes)*time.Minute, r.StageDoneAt, st, offline, now)
	changed := doneAt != r.StageDoneAt
	r.StageDoneAt = doneAt
	switch action {
	case rolloutPause:
		r.Status, r.Reason, changed = RolloutPaused, reason, true
	case rolloutComplete:
		r.Status, r.Reason, changed = RolloutCompleted, reason, true
	case rolloutAdvance:
		r.CurrentStage++
		r.StageDoneAt, r.ackFailures, changed = 0, 0, true
		created, remaining, err := s.startStage(r, r.CurrentStage, tasks, now)
		if err != nil {
			return err
		}
		// 节点已全部处理（其余被跳过）：后面的批次没有可升级的节点
		if !remaining && created == 0 {
			r.Status, r.Reason = RolloutCompleted, "没有更多可升级的节点"
		}
	}
	if !changed {
		return nil
	}
	if err := s.store.saveRollout(r, now); err != nil {
		return err
	}
	if action == rolloutPause || action == rolloutComplete || r.Status == RolloutCompleted {
		act := "upgrade_rollout.auto_pause"
		if r.Status == RolloutCompleted {
			act = "upgrade_rollout.complete"
		}
		_ = s.store.Audit(AuditEntry{ActorType: "system", Action: act, Success: r.Status == RolloutCompleted,
			Details: map[string]any{"rollout_id": r.ID, "stage": r.CurrentStage, "reason": r.Reason}}, now)
		s.log.Info("rollout "+r.Status, "component", "upgrade", "rollout", r.ID, "stage", r.CurrentStage, "reason", r.Reason)
	}
	return nil
}

// ---- 接口 ----

var errRolloutBusy = errorf(CodeConflict, "已有进行中的灰度升级：请先完成或取消")

// handleRollouts：GET /api/v1/upgrade-rollouts，admin。最近 20 个，最新在前。
func (s *Server) handleRollouts(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListRollouts(20)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	for _, ro := range list {
		tasks, err := s.store.rolloutTasks(ro.ID)
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		ro.fillProgress(tasks)
	}
	writeList(w, list, "", nil)
}

// handleCreateRollout：POST /api/v1/upgrade-rollouts，admin（设计 29.16）。创建后立即开始第一批。
func (s *Server) handleCreateRollout(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ServerIDs      []int64        `json:"server_ids"`
		Version        string         `json:"version"`
		Stages         []RolloutStage `json:"stages"`
		ObserveMinutes *int           `json:"observe_minutes"`
		MaxFailures    *int           `json:"max_failures"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	var errs []FieldError
	bad := func(f, m string) { errs = append(errs, FieldError{Field: f, Message: m}) }
	seen := map[int64]bool{}
	ids := []int64{}
	for _, id := range b.ServerIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) < 2 || len(ids) > 500 {
		bad("server_ids", "灰度升级需要 2～500 个节点；单个节点请直接升级")
	}
	v, err := release.ParseVersion(b.Version)
	if err != nil {
		bad("version", "版本号格式无效")
	}
	if len(b.Stages) < 2 || len(b.Stages) > 5 {
		bad("stages", "请设置 2～5 批")
	}
	for i, st := range b.Stages {
		if (st.Count > 0) == (st.Percent > 0) || st.Count < 0 || st.Percent < 0 || st.Percent > 100 {
			bad(fmt.Sprintf("stages[%d]", i), "每批填写台数或百分比（1～100）之一")
		}
	}
	observe, maxFail := 30, 0
	if b.ObserveMinutes != nil {
		observe = *b.ObserveMinutes
	}
	if b.MaxFailures != nil {
		maxFail = *b.MaxFailures
	}
	if observe < 5 || observe > 1440 {
		bad("observe_minutes", "观察时间为 5～1440 分钟")
	}
	if maxFail < 0 || maxFail > 100 {
		bad("max_failures", "允许失败台数为 0～100")
	}
	if len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	rel, _, _, err := s.releaseByVersion(v.String())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var n int
	if err := s.store.DB.QueryRow(`SELECT COUNT(*) FROM upgrade_rollouts WHERE status IN (?,?)`, RolloutRunning, RolloutPaused).Scan(&n); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if n > 0 {
		s.writeError(w, r, errRolloutBusy)
		return
	}
	by := "admin"
	if se := info(r).session; se != nil {
		by = "admin:" + se.User.Username
	}
	now := time.Now()
	stages, _ := json.Marshal(b.Stages)
	idsJSON, _ := json.Marshal(ids)
	res, err := s.store.DB.Exec(`INSERT INTO upgrade_rollouts (target_version, status, stages, observe_minutes, max_failures, server_ids,
		created_by, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		rel.Version, RolloutRunning, string(stages), observe, maxFail, string(idsJSON), by, now.Unix(), now.Unix())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	id, _ := res.LastInsertId()
	ro, err := s.store.GetRollout(id)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	created, remaining, err := s.startStage(ro, 1, nil, now)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	tasks, _ := s.store.rolloutTasks(id)
	if !remaining && created == 0 {
		ro.Status, ro.Reason = RolloutCompleted, "没有可升级的节点"
	}
	if err := s.store.saveRollout(ro, now); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	ro.fillProgress(tasks)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "upgrade_rollout.create", Success: true,
		Details: map[string]any{"rollout_id": id, "version": rel.Version, "servers": len(ids), "stages": len(b.Stages)}})
	writeJSONStatus(w, http.StatusCreated, ro)
}

// handleRolloutAction：POST /api/v1/upgrade-rollouts/{id}/{action}，admin。action 为 pause / resume / cancel；成功 204。
func (s *Server) handleRolloutAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	action := r.PathValue("action")
	ro, err := s.store.GetRollout(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	now := time.Now()
	switch action {
	case "pause":
		if ro.Status != RolloutRunning {
			s.writeError(w, r, errorf(CodeConflict, "只有运行中的灰度升级可以暂停"))
			return
		}
		ro.Status, ro.Reason = RolloutPaused, "手动暂停"
	case "resume":
		if ro.Status != RolloutPaused {
			s.writeError(w, r, errorf(CodeConflict, "只有已暂停的灰度升级可以继续"))
			return
		}
		// 继续即表示管理员已确认当前的失败与离线节点：之后只有新的失败或新离线的节点会再次暂停；观察期重新开始
		tasks, err := s.store.rolloutTasks(id)
		if err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		ro.ackFailures = statOf(tasks, ro.CurrentStage).Failed
		_, offIDs := s.offlineUpgraded(ro, tasks, now)
		ro.ackOffline = append(ro.ackOffline, offIDs...)
		ro.Status, ro.Reason, ro.StageDoneAt = RolloutRunning, "", 0
	case "cancel":
		if ro.Status != RolloutRunning && ro.Status != RolloutPaused {
			s.writeError(w, r, errorf(CodeConflict, "灰度升级已结束"))
			return
		}
		if _, err := s.store.DB.Exec(`UPDATE upgrade_tasks SET status = ?, updated_at = ? WHERE rollout_id = ? AND status IN (?,?)`,
			UpgradeCancelled, now.Unix(), id, UpgradePending, UpgradeDelivered); err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		ro.Status, ro.Reason = RolloutCancelled, "手动取消"
	default:
		s.writeError(w, r, errorf(CodeNotFound, "未知操作"))
		return
	}
	if err := s.store.saveRollout(ro, now); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "upgrade_rollout." + action, Success: true,
		Details: map[string]any{"rollout_id": id, "stage": ro.CurrentStage}})
	w.WriteHeader(http.StatusNoContent)
}
