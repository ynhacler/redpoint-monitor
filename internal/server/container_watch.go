package server

// 容器已停止（设计 33.4，修订第 166 条）：按主进程名记住每台节点曾经运行的容器数量（如 nginx ×1、node ×3），
// 运行中的少于这个数量即为“缺少”，告警引擎按“容器已停止”规则评估（默认持续 2 分钟触发，避开重建时的短暂空档）。
//
//   - 按主进程名而不是容器 ID 统计：容器重建后 ID 会变，进程名不变，不会误报。读不到进程名（hidepid）的容器不统计
//   - 主动停止或缩容后，在节点详情中点“不再提醒”，以当前运行的容器为准
//   - 记录保存在 container_expect 表中：面板重启后既不会漏报，也不会把仍在缺少的告警误判为恢复
//   - Agent 最多上报 50 个容器：列表满 50 个时只增加不减少，避免截断造成误报
//   - 已有记录的节点上报中没有 containers 字段时视为全部停止（没有容器时 Agent 省略该字段）；从未上报过容器的节点不评估

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"vpsmon/internal/protocol"
)

// containerExpect 是一个主进程名应有的数量，以及开始缺少的时间（0 表示不缺）。
type containerExpect struct {
	Count        int
	MissingSince int64
}

// containerMissing 是一个缺少的主进程名，用于节点详情。
type containerMissing struct {
	Label   string `json:"label"`
	Missing int    `json:"missing"`
	Since   int64  `json:"since"`
}

// containerWatch 缓存各节点的记录（第一次用到时从数据库读取）与最近一次的运行数量，以及容器的重启（修订第 167 条）。
type containerWatch struct {
	mu      sync.Mutex
	expect  map[int64]map[string]*containerExpect
	running map[int64]map[string]int
	// 重启：Agent 上报的是启动以来的累计次数；这里记下上一次的累计值，增加的部分按当时的时间记为重启事件（保留 24 小时，
	// 只在内存中——面板重启后从下一次增加开始重新计）。键为容器 ID 前 12 位
	lastRestarts map[int64]map[string]int
	restartAt    map[int64]map[string][]int64
	labels       map[int64]map[string]string // 容器 ID → 主进程名（没有时为 ID），用于告警说明
}

func newContainerWatch() *containerWatch {
	return &containerWatch{expect: map[int64]map[string]*containerExpect{}, running: map[int64]map[string]int{},
		lastRestarts: map[int64]map[string]int{}, restartAt: map[int64]map[string][]int64{}, labels: map[int64]map[string]string{}}
}

const (
	restartAlertWindow = 10 * time.Minute // “容器反复重启”统计的窗口
	restartKeep        = 24 * time.Hour   // 节点详情显示的重启次数统计的窗口
)

// trackRestarts 把累计次数的增加记为重启事件；调用方持有 w.mu。Agent 重启后累计值变小，只更新基线。
func (w *containerWatch) trackRestarts(sid int64, cs []protocol.Container, now time.Time) {
	last, at, labels := w.lastRestarts[sid], w.restartAt[sid], w.labels[sid]
	if last == nil {
		last, at, labels = map[string]int{}, map[string][]int64{}, map[string]string{}
		w.lastRestarts[sid], w.restartAt[sid], w.labels[sid] = last, at, labels
	}
	cut := now.Add(-restartKeep).Unix()
	for _, c := range cs {
		if prev, ok := last[c.ID]; ok && c.Restarts > prev {
			for range min(c.Restarts-prev, 100) {
				at[c.ID] = append(at[c.ID], now.Unix())
			}
		}
		last[c.ID] = c.Restarts
		labels[c.ID] = cmp.Or(c.Command, c.ID)
	}
	for id, ts := range at {
		keep := ts[:0]
		for _, t := range ts {
			if t >= cut {
				keep = append(keep, t)
			}
		}
		if len(keep) == 0 {
			delete(at, id)
		} else {
			at[id] = keep
		}
	}
}

// restartsSince 返回各容器在 since 之后的重启次数；没有重启的不列出。
func (w *containerWatch) restartsSince(sid int64, since int64) map[string]int {
	out := map[string]int{}
	for id, ts := range w.restartAt[sid] {
		n := 0
		for _, t := range ts {
			if t >= since {
				n++
			}
		}
		if n > 0 {
			out[id] = n
		}
	}
	return out
}

// containerRestartView 是“容器反复重启”规则的值：10 分钟内重启最多的容器的次数与说明。
type containerRestartView struct {
	Max    int
	Detail string
}

// containerRestartInput 返回告警引擎的输入；从未上报过容器时 ok 为 false。
func (s *Server) containerRestartInput(sid int64, now time.Time) (*containerRestartView, bool) {
	w := s.cwatch
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.lastRestarts[sid]; !ok {
		return nil, false
	}
	v := &containerRestartView{}
	var parts []string
	counts := w.restartsSince(sid, now.Add(-restartAlertWindow).Unix())
	ids := make([]string, 0, len(counts))
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return counts[ids[i]] > counts[ids[j]] || counts[ids[i]] == counts[ids[j]] && ids[i] < ids[j]
	})
	for _, id := range ids {
		v.Max = max(v.Max, counts[id])
		parts = append(parts, fmt.Sprintf("%s %d 次", w.labels[sid][id], counts[id]))
	}
	v.Detail = strings.Join(parts, "、")
	return v, true
}

// containerRestarts 返回节点详情中各容器 24 小时内的重启次数；没有时为 nil。
func (s *Server) containerRestarts(sid int64, now time.Time) map[string]int {
	w := s.cwatch
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.restartsSince(sid, now.Add(-restartKeep).Unix())
	if len(out) == 0 {
		return nil
	}
	return out
}

// LoadContainerExpect 读取节点的记录。
func (s *Store) LoadContainerExpect(serverID int64) (map[string]*containerExpect, error) {
	rows, err := s.DB.Query(`SELECT label, count, missing_since FROM container_expect WHERE server_id = ?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*containerExpect{}
	for rows.Next() {
		var label string
		e := &containerExpect{}
		if err := rows.Scan(&label, &e.Count, &e.MissingSince); err != nil {
			return nil, err
		}
		out[label] = e
	}
	return out, rows.Err()
}

// SaveContainerExpect 整体替换节点的记录（变化很少，只在变化时写入）。
func (s *Store) SaveContainerExpect(serverID int64, m map[string]*containerExpect) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM container_expect WHERE server_id = ?`, serverID); err != nil {
		return err
	}
	for label, e := range m {
		if _, err := tx.Exec(`INSERT INTO container_expect (server_id, label, count, missing_since) VALUES (?,?,?,?)`,
			serverID, label, e.Count, e.MissingSince); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// runningCounts 按主进程名统计运行中的容器；没有进程名的不统计。
func runningCounts(cs []protocol.Container) map[string]int {
	out := map[string]int{}
	for _, c := range cs {
		if c.Command != "" {
			out[c.Command]++
		}
	}
	return out
}

// updateExpect 用一次上报更新记录（纯函数，便于测试）：数量只增不减；少于记录时记下开始缺少的时间，回到记录时清除。
// truncated 为 true 时（列表满 50 个）不判断缺少。返回记录是否变化。
func updateExpect(m map[string]*containerExpect, running map[string]int, truncated bool, now int64) bool {
	changed := false
	for label, n := range running {
		e := m[label]
		if e == nil {
			m[label] = &containerExpect{Count: n}
			changed = true
			continue
		}
		if n > e.Count {
			e.Count, changed = n, true
		}
		if n >= e.Count && e.MissingSince != 0 {
			e.MissingSince, changed = 0, true
		}
	}
	if !truncated {
		for label, e := range m {
			if running[label] < e.Count && e.MissingSince == 0 {
				e.MissingSince, changed = now, true
			}
		}
	}
	return changed
}

// missingOf 列出缺少的主进程名，按开始缺少的时间、名称排序。
func missingOf(m map[string]*containerExpect, running map[string]int) []containerMissing {
	var out []containerMissing
	for label, e := range m {
		if e.MissingSince != 0 && running[label] < e.Count {
			out = append(out, containerMissing{Label: label, Missing: e.Count - running[label], Since: e.MissingSince})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Since != out[j].Since {
			return out[i].Since < out[j].Since
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// expectFor 返回节点的记录（第一次时从数据库读取）；调用方持有 w.mu。
func (s *Server) expectFor(sid int64) (map[string]*containerExpect, error) {
	w := s.cwatch
	if m, ok := w.expect[sid]; ok {
		return m, nil
	}
	m, err := s.store.LoadContainerExpect(sid)
	if err != nil {
		return nil, err
	}
	w.expect[sid] = m
	return m, nil
}

// trackContainers 由实时上报调用（补发的旧数据不调用）。
func (s *Server) trackContainers(sid int64, rep protocol.Report, now time.Time) {
	w := s.cwatch
	w.mu.Lock()
	defer w.mu.Unlock()
	if rep.Containers != nil || w.lastRestarts[sid] != nil {
		w.trackRestarts(sid, rep.Containers, now) // 容器反复重启（修订第 167 条）
	}
	m, err := s.expectFor(sid)
	if err != nil {
		s.log.Error("load container records failed", "component", "containers", "server_id", sid, "err", err)
		return
	}
	if rep.Containers == nil && len(m) == 0 {
		return // 从未上报过容器（或旧版 Agent）：不评估
	}
	running := runningCounts(rep.Containers)
	w.running[sid] = running
	if updateExpect(m, running, len(rep.Containers) >= 50, now.Unix()) {
		if err := s.store.SaveContainerExpect(sid, m); err != nil {
			s.log.Error("save container records failed", "component", "containers", "server_id", sid, "err", err)
		}
	}
}

// containerAlertInput 返回告警引擎的输入：缺少的容器总数与说明；没有记录时 ok 为 false（不评估）。
func (s *Server) containerAlertInput(sid int64) (*containerAlertView, bool) {
	w := s.cwatch
	w.mu.Lock()
	defer w.mu.Unlock()
	m, err := s.expectFor(sid)
	if err != nil || len(m) == 0 {
		return nil, false
	}
	running, ok := w.running[sid]
	if !ok {
		return nil, false // 面板启动后还没收到这台节点的上报
	}
	v := &containerAlertView{}
	var parts []string
	for _, x := range missingOf(m, running) {
		v.Missing += x.Missing
		if x.Missing > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", x.Label, x.Missing))
		} else {
			parts = append(parts, x.Label)
		}
	}
	v.Detail = strings.Join(parts, "、")
	return v, true
}

// containerAlertView 是“容器已停止”规则的值。
type containerAlertView struct {
	Missing int
	Detail  string
}

// containersMissing 返回节点详情中的已停止容器；没有时为 nil。
func (s *Server) containersMissing(sid int64) ([]containerMissing, error) {
	w := s.cwatch
	w.mu.Lock()
	defer w.mu.Unlock()
	m, err := s.expectFor(sid)
	if err != nil || len(m) == 0 {
		return nil, err
	}
	return missingOf(m, w.running[sid]), nil
}

// handleForgetContainers：POST /api/v1/servers/{id}/containers/forget，admin。{label?}：以当前运行的容器为准，
// 省略 label 时全部。成功 204。
func (s *Server) handleForgetContainers(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if _, err := s.store.GetServer(id); errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	} else if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	var body struct {
		Label string `json:"label"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
			s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
			return
		}
	}
	cw := s.cwatch
	cw.mu.Lock()
	m, err := s.expectFor(id)
	if err == nil {
		running := cw.running[id]
		for label, e := range m {
			if body.Label != "" && label != body.Label {
				continue
			}
			if n := running[label]; n > 0 {
				e.Count, e.MissingSince = n, 0
			} else {
				delete(m, label)
			}
		}
		err = s.store.SaveContainerExpect(id, m)
	}
	cw.mu.Unlock()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: info(r).session.User.Username, Action: "server.containers_forget",
		TargetType: "server", TargetID: id, Success: true, Details: map[string]any{"label": body.Label}})
	w.WriteHeader(http.StatusNoContent)
}
