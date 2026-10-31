package server

// 变更记录（修订第 273 条，设计 4.9.5）：把节点上与安全有关、平时不该变的配置的变化记为系统事件（kind=change），并按告警的
// 总开关、维护与静音发送通知。第一项是防火墙（Report.firewall 已有，不需要升级 Agent）：启用 / 停用、入站默认策略、规则增删。
// 上一次的状态存在 change_state 表（面板重启后不会把现状当作变化）；第一次看到某一项时只记录，不产生事件。
//
// 【安全】防火墙等只给管理员看（设计 4.9.2）：事件正文中的细节只返回给 Web 管理员；通知只写“有几项变化”，
// 细节不经推送与通知渠道发出（App 推送与第三方渠道都看不到规则内容）。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"vpsmon/internal/protocol"
)

// NotifyChange 是节点配置变更的通知种类（修订第 273 条）。
const NotifyChange = "change"

const (
	eventChange   = "change"   // 系统事件种类：节点配置变更
	changeFW      = "firewall" // change_state.item
	ruleKeyChange = "change"   // 静音时使用的规则键：静音“变更记录”即不再通知
	maxChangeList = 5          // 每一类变化在事件正文中最多列出的条数
)

// changeWatch 缓存各节点各项最近一次的状态（JSON），状态没变时不读写数据库。
type changeWatch struct {
	mu   sync.Mutex
	last map[int64]map[string]string
}

func newChangeWatch() *changeWatch { return &changeWatch{last: map[int64]map[string]string{}} }

// fwToolState 是一种防火墙用于比较的状态。规则规范化为一行文字后排序，顺序变化不算变更。
type fwToolState struct {
	Active   bool     `json:"active"`
	Policy   string   `json:"policy,omitempty"`
	Readable bool     `json:"readable"`
	Rules    []string `json:"rules,omitempty"`
}

// firewallState 把上报中的防火墙规范化为按名称索引的状态。
func firewallState(fw *protocol.Firewall) map[string]fwToolState {
	out := map[string]fwToolState{}
	for _, t := range fw.Tools {
		st := fwToolState{Active: t.Active, Policy: t.Policy, Readable: t.RulesReadable}
		for _, r := range t.Rules {
			st.Rules = append(st.Rules, ruleText(r))
		}
		slices.Sort(st.Rules)
		out[t.Name] = st
	}
	return out
}

// ruleText：allow tcp/22、deny 8000:9000、allow tcp/443 来自 1.2.3.4、ban sshd
func ruleText(r protocol.FirewallRule) string {
	s := r.Action
	switch {
	case r.Proto != "" && r.Port != "":
		s += " " + r.Proto + "/" + r.Port
	case r.Port != "":
		s += " " + r.Port
	case r.Proto != "":
		s += " " + r.Proto
	}
	if r.Service != "" {
		s += " " + r.Service
	}
	if r.From != "" {
		s += " 来自 " + r.From
	}
	return s
}

// diffFirewall 比较两次的防火墙状态，返回每项变化一行与级别：停用、不再检测到、入站默认策略放宽为 allow 为警告，其余为提示。
// 规则只在两次都可读时比较（可读性变化多为权限调整，不算规则变化）。
func diffFirewall(old, cur map[string]fwToolState) ([]string, string) {
	var lines []string
	sev := SeverityInfo
	warn := func(s string) { lines, sev = append(lines, s), SeverityWarning }
	names := make([]string, 0, len(old)+len(cur))
	for n := range old {
		names = append(names, n)
	}
	for n := range cur {
		if _, ok := old[n]; !ok {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	for _, n := range names {
		o, had := old[n]
		c, has := cur[n]
		switch {
		case had && !has:
			if o.Active {
				warn(n + " 不再检测到（此前已启用）")
			} else {
				lines = append(lines, n+" 不再检测到")
			}
			continue
		case !had && has:
			lines = append(lines, n+" 新检测到（"+map[bool]string{true: "已启用", false: "未启用"}[c.Active]+"）")
			continue
		}
		if o.Active && !c.Active {
			warn(n + " 已停用")
		} else if !o.Active && c.Active {
			lines = append(lines, n+" 已启用")
		}
		if o.Policy != c.Policy && o.Policy != "" && c.Policy != "" {
			s := n + " 入站默认策略 " + o.Policy + " → " + c.Policy
			if c.Policy == "allow" {
				warn(s)
			} else {
				lines = append(lines, s)
			}
		}
		if o.Readable && c.Readable {
			added, removed := setDiff(o.Rules, c.Rules)
			if len(added) > 0 {
				lines = append(lines, n+" 新增规则："+listBrief(added))
			}
			if len(removed) > 0 {
				lines = append(lines, n+" 删除规则："+listBrief(removed))
			}
		}
	}
	return lines, sev
}

// setDiff 返回 cur 中新出现的与 old 中消失的元素（两者都已排序；重复的规则按次数计）。
func setDiff(old, cur []string) (added, removed []string) {
	count := map[string]int{}
	for _, s := range old {
		count[s]++
	}
	for _, s := range cur {
		if count[s] > 0 {
			count[s]--
		} else {
			added = append(added, s)
		}
	}
	for _, s := range old {
		if count[s] > 0 {
			count[s]--
			removed = append(removed, s)
		}
	}
	return added, removed
}

// listBrief：a、b、c 等 7 条
func listBrief(list []string) string {
	if len(list) <= maxChangeList {
		return strings.Join(list, "、")
	}
	return strings.Join(list[:maxChangeList], "、") + fmt.Sprintf(" 等 %d 条", len(list))
}

// trackChanges 由实时上报调用（补发的旧数据不调用）。没有某一项（旧版 Agent、本轮采集失败）时不比较，不当作被删除。
func (s *Server) trackChanges(sid int64, rep protocol.Report, now time.Time) {
	if rep.Firewall == nil {
		return
	}
	cur := firewallState(rep.Firewall)
	b, _ := json.Marshal(cur)
	prev, ok := s.changeSwap(sid, changeFW, string(b), now)
	if !ok {
		return // 没有变化，或第一次看到
	}
	var old map[string]fwToolState
	if err := json.Unmarshal([]byte(prev), &old); err != nil {
		return
	}
	lines, sev := diffFirewall(old, cur)
	if len(lines) == 0 {
		return // 只有规则顺序、可读性等不影响判断的变化
	}
	s.recordChange(sid, "防火墙", lines, sev, now)
}

// changeSwap 保存某一项的新状态，返回之前的状态；状态没变或之前没有记录时 ok 为 false。
func (s *Server) changeSwap(sid int64, item, state string, now time.Time) (prev string, ok bool) {
	w := s.changes
	w.mu.Lock()
	defer w.mu.Unlock()
	m := w.last[sid]
	if m == nil {
		m = map[string]string{}
		w.last[sid] = m
	}
	prev, known := m[item]
	if !known {
		var err error
		if prev, known, err = s.store.ChangeState(sid, item); err != nil {
			s.log.Error("load change state failed", "component", "changes", "server_id", sid, "err", err)
			return "", false
		}
	}
	if known && prev == state {
		m[item] = state
		return "", false
	}
	if err := s.store.SaveChangeState(sid, item, state, now); err != nil {
		s.log.Error("save change state failed", "component", "changes", "server_id", sid, "err", err)
		return "", false
	}
	m[item] = state
	return prev, known
}

// recordChange 记一条变更事件，并在告警开启、节点不在维护、没有静音“变更记录”时发送通知。
// 通知只写类别与条数，细节留在事件中只给管理员看。
func (s *Server) recordChange(sid int64, what string, lines []string, sev string, now time.Time) {
	row, err := s.store.GetServer(sid)
	if err != nil {
		return
	}
	title := fmt.Sprintf("%s %s有 %d 项变化", row.Name, what, len(lines))
	s.recordEvent(eventChange, sev, sid, title, strings.Join(lines, "\n"), now)
	s.log.Info("config changed", "component", "changes", "server_id", sid, "item", what, "count", len(lines))
	if !s.alertsEnabled() || s.alerts.silenceFor(*row, SilenceMaintenance, "", now) != nil ||
		s.alerts.silenceFor(*row, SilenceMute, ruleKeyChange, now) != nil {
		return
	}
	m := notifyMessage{Kind: NotifyChange, ServerID: sid, ServerName: row.Name, RuleKey: ruleKeyChange, Type: eventChange,
		Severity: sev, StartedAt: now, Message: fmt.Sprintf("%s有 %d 项变化（详情见 Web 节点详情 · 安全）", what, len(lines))}
	if s.publicURL != "" {
		m.Link = s.publicURL + "/servers/" + itoa64(sid) + "?tab=security"
	}
	s.send(m, now)
}

// ChangeState 读取某一项上次的状态；没有记录时 ok 为 false。
func (s *Store) ChangeState(sid int64, item string) (state string, ok bool, err error) {
	err = s.DB.QueryRow(`SELECT state FROM change_state WHERE server_id = ? AND item = ?`, sid, item).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return state, err == nil, err
}

// SaveChangeState 保存某一项的状态。
func (s *Store) SaveChangeState(sid int64, item, state string, now time.Time) error {
	_, err := s.DB.Exec(`INSERT INTO change_state (server_id, item, state, updated_at) VALUES (?,?,?,?)
		ON CONFLICT (server_id, item) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`, sid, item, state, now.Unix())
	return err
}
