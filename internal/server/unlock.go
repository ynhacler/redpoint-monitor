package server

// 流媒体与网站解锁检测（设计 50，修订第 192 条）：管理员在节点详情点“检测”，面板生成请求号并在 Agent 下一次上报的
// 响应头中下发（X-Unlock-Request）；Agent 用内置的检测项检测一次，结果随之后的上报带回。只手动触发，不定时检测。
// 【安全】面板只能请求“现在检测一次”，不能指定网址或内容（约束 1）；结果只来自本节点的 Agent Token。

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"vpsmon/internal/protocol"
)

// unlockPendingTTL：请求下发后这么久还没有结果视为超时，可以重新检测。
const unlockPendingTTL = 10 * time.Minute

// unlockSupport 记录各节点的 Agent 是否支持检测（上报请求头 X-Unlock），只在内存中。
type unlockSupport struct {
	mu sync.Mutex
	m  map[int64]bool
}

func (u *unlockSupport) set(sid int64, on bool) {
	u.mu.Lock()
	if u.m == nil {
		u.m = map[int64]bool{}
	}
	u.m[sid] = on
	u.mu.Unlock()
}

func (u *unlockSupport) get(sid int64) (on, known bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	on, known = u.m[sid]
	return
}

// unlockView 是节点详情中的检测状态（api/openapi.yaml UnlockView）。
type unlockView struct {
	Supported   bool                  `json:"supported"`    // Agent 支持检测（最近一次上报声明）
	Pending     bool                  `json:"pending"`      // 已请求、等待结果
	RequestedAt int64                 `json:"requested_at"` // 最近一次请求时间；0 表示从未检测
	CheckedAt   int64                 `json:"checked_at"`   // 最近一次结果的时间；0 表示还没有结果
	Items       []protocol.UnlockItem `json:"items"`
}

type unlockRow struct {
	request, done string
	requestedAt   int64
	checkedAt     int64
	items         []protocol.UnlockItem
}

func (s *Store) getUnlock(sid int64) (*unlockRow, error) {
	var x unlockRow
	var items string
	err := s.DB.QueryRow(`SELECT request, done_request, requested_at, checked_at, items FROM server_unlock WHERE server_id = ?`, sid).
		Scan(&x.request, &x.done, &x.requestedAt, &x.checkedAt, &items)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(items), &x.items)
	return &x, nil
}

func (x *unlockRow) pending(now time.Time) bool {
	return x != nil && x.request != "" && x.request != x.done && now.Unix()-x.requestedAt < int64(unlockPendingTTL/time.Second)
}

// syncUnlock 处理上报请求头：记录 Agent 是否支持；有待处理的请求时在响应头中下发请求号。
func (s *Server) syncUnlock(w http.ResponseWriter, r *http.Request, sid int64, now time.Time) {
	supported := r.Header.Get(protocol.HeaderUnlock) != ""
	s.unlock.set(sid, supported)
	if !supported {
		return
	}
	x, err := s.store.getUnlock(sid)
	if err != nil {
		s.log.Error("read unlock request failed", "component", "unlock", "err", err)
		return
	}
	if x.pending(now) {
		w.Header().Set(protocol.HeaderUnlockRequest, x.request)
	}
}

// saveUnlock 保存 Agent 带回的结果：格式正确、且是本节点最近一次请求的结果才保存。
func (s *Server) saveUnlock(sid int64, res *protocol.UnlockResult, now time.Time) {
	if !res.Valid() {
		s.log.Warn("invalid unlock result", "component", "unlock", "server_id", sid)
		return
	}
	x, err := s.store.getUnlock(sid)
	if err != nil || x == nil || x.request != res.Request {
		return
	}
	items, _ := json.Marshal(res.Items)
	if _, err := s.store.DB.Exec(`UPDATE server_unlock SET done_request = ?, checked_at = ?, items = ? WHERE server_id = ? AND request = ?`,
		res.Request, now.Unix(), string(items), sid, res.Request); err != nil {
		s.log.Error("save unlock result failed", "component", "unlock", "err", err)
	}
}

// unlockViewOf 返回节点详情中的检测状态。
func (s *Server) unlockViewOf(sid int64, now time.Time) (*unlockView, error) {
	x, err := s.store.getUnlock(sid)
	if err != nil {
		return nil, err
	}
	on, _ := s.unlock.get(sid)
	v := &unlockView{Supported: on, Items: []protocol.UnlockItem{}}
	if x != nil {
		v.Pending, v.RequestedAt, v.CheckedAt = x.pending(now), x.requestedAt, x.checkedAt
		if x.items != nil {
			v.Items = x.items
		}
	}
	return v, nil
}

// handleUnlockCheck：POST /api/v1/servers/{id}/unlock-check，admin。请求 Agent 检测一次；
// Agent 离线或版本不支持时 409；已在检测中时直接返回当前状态。
func (s *Server) handleUnlockCheck(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	row, err := s.store.GetServer(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	now := time.Now()
	on, known := s.unlock.get(id)
	switch {
	case !known || !s.online(id, now):
		s.writeError(w, r, errorf(CodeConflict, "节点离线，上线后再检测"))
		return
	case !on:
		s.writeError(w, r, errorf(CodeConflict, "节点的 Agent 版本不支持解锁检测，请先升级 Agent"))
		return
	}
	if x, err := s.store.getUnlock(id); err == nil && !x.pending(now) {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		req := hex.EncodeToString(b)
		if _, err := s.store.DB.Exec(`INSERT INTO server_unlock (server_id, request, requested_at) VALUES (?,?,?)
			ON CONFLICT (server_id) DO UPDATE SET request = excluded.request, requested_at = excluded.requested_at`, id, req, now.Unix()); err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
		s.audit(r, AuditEntry{ActorType: "admin", Action: "server.unlock_check", Success: true, Details: map[string]any{"id": id, "name": row.Name}})
	}
	v, err := s.unlockViewOf(id, now)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, v)
}

// online 判断节点最近 60 秒内有上报。
func (s *Server) online(sid int64, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn := s.latest[sid]
	return sn != nil && now.Sub(sn.ReceivedAt) < time.Minute
}
