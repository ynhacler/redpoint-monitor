package server

// 变更记录（修订第 273、274 条，设计 4.9.5）：把节点上与安全有关、平时不该变的配置的变化记为系统事件（kind=change），并按告警的
// 总开关、维护与静音发送通知。防火墙（Report.firewall，不需要升级 Agent）：启用 / 停用、入站默认策略、规则增删；
// 账号与权限组（Report.accounts）：可登录账号、UID 0、管理员组成员的增删；重要文件（Report.config_files）：新出现、删除、修改。
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
	changeAcct    = "accounts"
	changeFiles   = "files"
	ruleKeyChange = "change" // 静音时使用的规则键：静音“变更记录”即不再通知
	maxChangeList = 5        // 每一类变化在事件正文中最多列出的条数
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
	for _, n := range sortedKeys(old, cur) {
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
	if rep.Firewall != nil {
		trackItem(s, sid, changeFW, "防火墙", firewallState(rep.Firewall), diffFirewall, now)
	}
	if rep.Accounts != nil {
		trackItem(s, sid, changeAcct, "账号与权限组", accountState(rep.Accounts), diffAccounts, now)
	}
	if rep.ConfigFiles != nil {
		trackItem(s, sid, changeFiles, "重要文件", fileState(rep.ConfigFiles), diffFiles, now)
	}
}

// trackItem 保存某一项规范化后的状态，与上一次不同时比较并记录变更。
func trackItem[T any](s *Server, sid int64, item, what string, cur T, diff func(old, cur T) ([]string, string), now time.Time) {
	b, _ := json.Marshal(cur)
	prev, ok := s.changeSwap(sid, item, string(b), now)
	if !ok {
		return // 没有变化，或第一次看到
	}
	var old T
	if err := json.Unmarshal([]byte(prev), &old); err != nil {
		return
	}
	lines, sev := diff(old, cur)
	if len(lines) == 0 {
		return // 只有顺序、可读性等不影响判断的变化
	}
	s.recordChange(sid, what, lines, sev, now)
}

// acctState 是账号与权限组用于比较的状态：账号名 → UID 与 shell，组名 → 成员。
type acctState struct {
	Users  map[string]protocol.AccountUser `json:"users"`
	Groups map[string][]string             `json:"groups"`
}

func accountState(a *protocol.Accounts) acctState {
	st := acctState{Users: map[string]protocol.AccountUser{}, Groups: map[string][]string{}}
	for _, u := range a.Users {
		st.Users[u.Name] = u
	}
	for _, g := range a.Groups {
		m := slices.Clone(g.Members)
		slices.Sort(m)
		st.Groups[g.Name] = m
	}
	return st
}

// diffAccounts：新增 UID 0 账号、已有账号的 UID 变为 0、管理员组新增成员为警告，其余（新增 / 删除可登录账号、shell 变化、
// 组成员减少）为提示。
func diffAccounts(old, cur acctState) ([]string, string) {
	var lines []string
	sev := SeverityInfo
	warn := func(s string) { lines, sev = append(lines, s), SeverityWarning }
	for _, n := range sortedKeys(old.Users, cur.Users) {
		o, had := old.Users[n]
		c, has := cur.Users[n]
		switch {
		case !had:
			s := fmt.Sprintf("新增可登录账号 %s（UID %d）", n, c.UID)
			if c.UID == 0 {
				warn(s)
			} else {
				lines = append(lines, s)
			}
		case !has:
			lines = append(lines, "账号 "+n+" 已删除或不能再登录")
		case o.UID != c.UID:
			s := fmt.Sprintf("账号 %s 的 UID %d → %d", n, o.UID, c.UID)
			if c.UID == 0 {
				warn(s)
			} else {
				lines = append(lines, s)
			}
		case o.Shell != c.Shell:
			lines = append(lines, "账号 "+n+" 的登录 shell "+shellText(o.Shell)+" → "+shellText(c.Shell))
		}
	}
	for _, g := range sortedKeys(old.Groups, cur.Groups) {
		added, removed := setDiff(old.Groups[g], cur.Groups[g])
		if len(added) > 0 {
			warn(g + " 组新增成员：" + listBrief(added))
		}
		if len(removed) > 0 {
			lines = append(lines, g+" 组移除成员："+listBrief(removed))
		}
	}
	return lines, sev
}

// fileMeta 是重要文件用于比较的状态。
type fileMeta struct {
	Dir    bool   `json:"dir,omitempty"`
	Size   int64  `json:"size,omitempty"`
	MTime  int64  `json:"mtime,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

func fileState(files []protocol.ConfigFile) map[string]fileMeta {
	out := map[string]fileMeta{}
	for _, f := range files {
		out[f.Path] = fileMeta{Dir: f.Dir, Size: f.Size, MTime: f.MTime, SHA256: f.SHA256}
	}
	return out
}

// sensitiveFile：sudoers 与 ld.so.preload（常被用来劫持进程）的变化为警告级。
func sensitiveFile(p string) bool {
	return p == "/etc/ld.so.preload" || p == "/etc/sudoers" || strings.HasPrefix(p, "/etc/sudoers.d")
}

// diffFiles：有摘要时按内容比较（只改修改时间不算），否则按大小与修改时间；目录只看修改时间（其中有文件增删）。
func diffFiles(old, cur map[string]fileMeta) ([]string, string) {
	var lines []string
	sev := SeverityInfo
	add := func(p, s string) {
		lines = append(lines, s)
		if sensitiveFile(p) {
			sev = SeverityWarning
		}
	}
	for _, p := range sortedKeys(old, cur) {
		o, had := old[p]
		c, has := cur[p]
		switch {
		case !had:
			add(p, "新出现 "+p)
		case !has:
			add(p, "已删除 "+p)
		case o.Dir || c.Dir:
			if o.MTime != c.MTime {
				add(p, p+" 中的文件有变化")
			}
		case o.SHA256 != "" && c.SHA256 != "":
			if o.SHA256 != c.SHA256 {
				add(p, p+" 已修改")
			}
		case o.Size != c.Size || o.MTime != c.MTime:
			add(p, p+" 已修改")
		}
	}
	return lines, sev
}

// shellText：空的登录 shell 表示 /bin/sh。
func shellText(s string) string {
	if s == "" {
		return "（默认）"
	}
	return s
}

// sortedKeys 返回两个 map 的键的并集，排序。
func sortedKeys[V any](a, b map[string]V) []string {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
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
	// 【安全】账号名、文件路径与防火墙规则一样只在事件正文中（只给 Web 管理员），通知中只有类别与条数
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
