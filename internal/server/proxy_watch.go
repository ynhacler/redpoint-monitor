package server

// 代理异常（修订第 169 条）：Agent 上报识别到的代理程序（只给管理员看），面板记住每台节点曾经运行的代理，
// “代理异常”规则的值为问题数：
//
//   - 曾经运行、现在没有运行的代理（如 xray 停了）；主动停用或卸载后在节点详情中点“不再提醒”
//   - 正在运行、但对外的入站端口没有在监听（只看非回环地址的入站）
//
// 记录保存在 proxy_seen 表中（面板重启后既不漏报，也不会把仍未运行的误判为恢复）。从未上报过代理的节点（旧版 Agent）不评估。

import (
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

// proxyMissing 是曾经运行、现在没有运行的代理。
type proxyMissing struct {
	Name  string `json:"name"`
	Since int64  `json:"since"`
}

// proxyWatch 缓存各节点的记录（代理名 → 发现没有运行的时间，0 表示在运行）与最近一次上报的问题说明。
type proxyWatch struct {
	mu       sync.Mutex
	seen     map[int64]map[string]int64
	problems map[int64][]string // 最近一次上报中的问题（用于告警说明）；键不存在表示面板启动后还没收到上报
}

func newProxyWatch() *proxyWatch {
	return &proxyWatch{seen: map[int64]map[string]int64{}, problems: map[int64][]string{}}
}

// LoadProxySeen 读取节点的记录。
func (s *Store) LoadProxySeen(serverID int64) (map[string]int64, error) {
	rows, err := s.DB.Query(`SELECT name, missing_since FROM proxy_seen WHERE server_id = ?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var name string
		var since int64
		if err := rows.Scan(&name, &since); err != nil {
			return nil, err
		}
		out[name] = since
	}
	return out, rows.Err()
}

// SaveProxySeen 整体替换节点的记录（只在变化时写入）。
func (s *Store) SaveProxySeen(serverID int64, m map[string]int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM proxy_seen WHERE server_id = ?`, serverID); err != nil {
		return err
	}
	for name, since := range m {
		if _, err := tx.Exec(`INSERT INTO proxy_seen (server_id, name, missing_since) VALUES (?,?,?)`, serverID, name, since); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// updateProxySeen 用一次上报更新记录并列出问题（纯函数，便于测试）。返回记录是否变化。
func updateProxySeen(m map[string]int64, ps []protocol.Proxy, now int64) (changed bool, problems []string) {
	running := map[string]bool{}
	for _, p := range ps {
		if !p.Running {
			continue
		}
		running[p.Name] = true
		if since, ok := m[p.Name]; !ok || since != 0 {
			m[p.Name], changed = 0, true
		}
		var down []string
		for _, in := range p.Inbounds {
			if !in.Local && !in.Listening {
				down = append(down, fmt.Sprintf("%d/%s", in.Port, in.Network))
			}
		}
		if len(down) > 0 {
			problems = append(problems, p.Name+" 端口 "+strings.Join(down, "、")+" 没有在监听")
		}
	}
	var stopped []string
	for name, since := range m {
		if running[name] {
			continue
		}
		if since == 0 {
			m[name], changed = now, true
		}
		stopped = append(stopped, name)
	}
	sort.Strings(stopped)
	for _, name := range stopped {
		problems = append(problems, name+" 没有在运行")
	}
	return changed, problems
}

// seenFor 返回节点的记录（第一次时从数据库读取）；调用方持有 w.mu。
func (s *Server) seenFor(sid int64) (map[string]int64, error) {
	w := s.pwatch
	if m, ok := w.seen[sid]; ok {
		return m, nil
	}
	m, err := s.store.LoadProxySeen(sid)
	if err != nil {
		return nil, err
	}
	w.seen[sid] = m
	return m, nil
}

// trackProxies 由实时上报调用（补发的旧数据不调用）。
func (s *Server) trackProxies(sid int64, rep protocol.Report, now time.Time) {
	w := s.pwatch
	w.mu.Lock()
	defer w.mu.Unlock()
	m, err := s.seenFor(sid)
	if err != nil {
		s.log.Error("load proxy records failed", "component", "proxies", "server_id", sid, "err", err)
		return
	}
	if rep.Proxies == nil && len(m) == 0 {
		return // 从未上报过代理（或旧版 Agent）：不评估
	}
	changed, problems := updateProxySeen(m, rep.Proxies, now.Unix())
	w.problems[sid] = problems
	if changed {
		if err := s.store.SaveProxySeen(sid, m); err != nil {
			s.log.Error("save proxy records failed", "component", "proxies", "server_id", sid, "err", err)
		}
	}
}

// proxyAlertView 是“代理异常”规则的值。
type proxyAlertView struct {
	Problems int
	Detail   string
}

// proxyAlertInput 返回告警引擎的输入；从未上报过代理、或面板启动后还没收到上报时 ok 为 false。
func (s *Server) proxyAlertInput(sid int64) (*proxyAlertView, bool) {
	w := s.pwatch
	w.mu.Lock()
	defer w.mu.Unlock()
	p, ok := w.problems[sid]
	if !ok {
		return nil, false
	}
	return &proxyAlertView{Problems: len(p), Detail: strings.Join(p, "；")}, true
}

// proxiesMissing 返回节点详情中曾经运行、现在没有运行的代理；没有时为 nil。
func (s *Server) proxiesMissing(sid int64) ([]proxyMissing, error) {
	w := s.pwatch
	w.mu.Lock()
	defer w.mu.Unlock()
	m, err := s.seenFor(sid)
	if err != nil {
		return nil, err
	}
	var out []proxyMissing
	for name, since := range m {
		if since != 0 {
			out = append(out, proxyMissing{Name: name, Since: since})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// handleForgetProxies：POST /api/v1/servers/{id}/proxies/forget，admin。{name?}：忘记没有运行的代理（省略时全部）。成功 204。
func (s *Server) handleForgetProxies(w http.ResponseWriter, r *http.Request) {
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
		Name string `json:"name"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
			s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
			return
		}
	}
	pw := s.pwatch
	pw.mu.Lock()
	m, err := s.seenFor(id)
	if err == nil {
		for name, since := range m {
			if since != 0 && (body.Name == "" || body.Name == name) {
				delete(m, name)
			}
		}
		// 告警说明随之更新：去掉已忘记的“没有在运行”
		if p, ok := pw.problems[id]; ok {
			keep := p[:0]
			for _, x := range p {
				name, isStop := strings.CutSuffix(x, " 没有在运行")
				if isStop && (body.Name == "" || body.Name == name) {
					continue
				}
				keep = append(keep, x)
			}
			pw.problems[id] = keep
		}
		err = s.store.SaveProxySeen(id, m)
	}
	pw.mu.Unlock()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: info(r).session.User.Username, Action: "server.proxies_forget",
		TargetType: "server", TargetID: id, Success: true, Details: map[string]any{"name": body.Name}})
	w.WriteHeader(http.StatusNoContent)
}
