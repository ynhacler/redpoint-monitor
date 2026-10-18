package server

// 端口基线（修订第 170 条）：管理员为节点指定允许对公网开放的监听端口（如 22/tcp、443/tcp、55000/tcp），
// 面板把每次上报中对公网开放的监听端口与之比较：
//
//   - 不在基线中的为“未知监听端口”（如有人启动了 8080、3306）；默认启用，持续 1 分钟触发
//   - 基线中没有在监听的为“基线端口未监听”（服务停了或改了端口）；默认启用，持续 2 分钟触发
//
// “对公网开放”指监听在 0.0.0.0、:: 或公网地址；只在回环或内网（RFC 1918、CGNAT、ULA、链路本地）地址监听的不算，
// 与 Web 的暴露范围一致（设计 4.9.1）。这是本机 Agent 看到的“监听”，是否真能从公网访问还取决于防火墙与安全组。
// 没有设置基线的节点不评估。基线只给管理员看。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"vpsmon/internal/protocol"
)

const maxBaselinePorts = 200

// portBaselineView 是节点详情中的端口基线。
type portBaselineView struct {
	Ports     []string `json:"ports"`
	UpdatedAt int64    `json:"updated_at"`
}

// portBaselines 缓存各节点的基线（第一次用到时从数据库读取；nil 表示没有设置）。
type portBaselines struct {
	mu sync.Mutex
	m  map[int64]*portBaselineView
}

func newPortBaselines() *portBaselines { return &portBaselines{m: map[int64]*portBaselineView{}} }

// GetPortBaseline 读取节点的基线；没有设置时返回 nil。
func (s *Store) GetPortBaseline(serverID int64) (*portBaselineView, error) {
	var raw string
	v := &portBaselineView{}
	err := s.DB.QueryRow(`SELECT ports, updated_at FROM port_baseline WHERE server_id = ?`, serverID).Scan(&raw, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return v, json.Unmarshal([]byte(raw), &v.Ports)
}

// SetPortBaseline 保存基线；ports 为空时删除。
func (s *Store) SetPortBaseline(serverID int64, ports []string, now time.Time) error {
	if len(ports) == 0 {
		_, err := s.DB.Exec(`DELETE FROM port_baseline WHERE server_id = ?`, serverID)
		return err
	}
	b, _ := json.Marshal(ports)
	_, err := s.DB.Exec(`INSERT INTO port_baseline (server_id, ports, updated_at) VALUES (?,?,?)
		ON CONFLICT(server_id) DO UPDATE SET ports = excluded.ports, updated_at = excluded.updated_at`, serverID, string(b), now.Unix())
	return err
}

// baselineFor 返回节点的基线（带缓存）；没有设置时为 nil。
func (s *Server) baselineFor(sid int64) (*portBaselineView, error) {
	pb := s.pbase
	pb.mu.Lock()
	defer pb.mu.Unlock()
	if v, ok := pb.m[sid]; ok {
		return v, nil
	}
	v, err := s.store.GetPortBaseline(sid)
	if err != nil {
		return nil, err
	}
	pb.m[sid] = v
	return v, nil
}

var portKeyPattern = regexp.MustCompile(`^(\d{1,5})(?:/(tcp|udp))?$`)

// normalizePorts 校验并整理基线端口：“22” 视为 22/tcp；去重并按端口排序。
func normalizePorts(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		m := portKeyPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(p)))
		if m == nil {
			return nil, fmt.Errorf("端口格式应为 22/tcp 或 51820/udp：%q", p)
		}
		n, _ := strconv.Atoi(m[1])
		if n < 1 || n > 65535 {
			return nil, fmt.Errorf("端口应在 1～65535：%q", p)
		}
		proto := m[2]
		if proto == "" {
			proto = "tcp"
		}
		k := fmt.Sprintf("%d/%s", n, proto)
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sortPortKeys(out)
	return out, nil
}

func sortPortKeys(ks []string) {
	sort.Slice(ks, func(i, j int) bool {
		pi, qi, _ := strings.Cut(ks[i], "/")
		pj, qj, _ := strings.Cut(ks[j], "/")
		ni, _ := strconv.Atoi(pi)
		nj, _ := strconv.Atoi(pj)
		if ni != nj {
			return ni < nj
		}
		return qi < qj
	})
}

// publicAddr 判断监听地址是否对公网开放：0.0.0.0、::、* 或公网地址（不含回环、内网、CGNAT、ULA、链路本地）。
func publicAddr(a string) bool {
	a = strings.Trim(a, "[]")
	if a == "*" || a == "" {
		return true
	}
	ip, err := netip.ParseAddr(a)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || !ip.IsGlobalUnicast() {
		return false
	}
	return !netip.MustParsePrefix("100.64.0.0/10").Contains(ip) // CGNAT（Tailscale 等）
}

// exposedPorts 返回上报中对公网开放的监听端口（“端口/协议”，排序）；上报没有监听端口字段时 ok 为 false（旧版 Agent）。
func exposedPorts(rep *protocol.Report) (keys []string, ok bool) {
	if rep == nil || rep.Ports == nil {
		return nil, false
	}
	for _, p := range rep.Ports {
		if slices.ContainsFunc(p.Addrs, publicAddr) {
			keys = append(keys, fmt.Sprintf("%d/%s", p.Port, p.Proto))
		}
	}
	sortPortKeys(keys)
	return keys, true
}

// portDiff 比较基线与当前：不在基线中的（新增）与基线中没有在监听的（消失）。
func portDiff(baseline, current []string) (unknown, gone []string) {
	in := map[string]bool{}
	for _, k := range current {
		in[k] = true
	}
	allowed := map[string]bool{}
	for _, k := range baseline {
		allowed[k] = true
		if !in[k] {
			gone = append(gone, k)
		}
	}
	for _, k := range current {
		if !allowed[k] {
			unknown = append(unknown, k)
		}
	}
	return
}

// portAlertView 是端口基线两条规则的值。
type portAlertView struct {
	Unknown, Gone []string
}

// portAlertInput 返回告警引擎的输入：没有基线或上报中没有监听端口时 ok 为 false。
func (s *Server) portAlertInput(sid int64, rep *protocol.Report) (*portAlertView, bool) {
	cur, ok := exposedPorts(rep)
	if !ok {
		return nil, false
	}
	b, err := s.baselineFor(sid)
	if err != nil || b == nil {
		return nil, false
	}
	u, g := portDiff(b.Ports, cur)
	return &portAlertView{Unknown: u, Gone: g}, true
}

// handlePortBaseline：PUT /api/v1/servers/{id}/port-baseline，admin。{ports}；空数组表示取消。
func (s *Server) handlePortBaseline(w http.ResponseWriter, r *http.Request) {
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
		Ports []string `json:"ports"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	ports, err := normalizePorts(body.Ports)
	if err == nil && len(ports) > maxBaselinePorts {
		err = errors.New("基线最多 200 个端口")
	}
	if err != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "ports", Message: err.Error()}}})
		return
	}
	now := time.Now()
	if err := s.store.SetPortBaseline(id, ports, now); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	view := &portBaselineView{Ports: ports, UpdatedAt: now.Unix()}
	s.pbase.mu.Lock()
	if len(ports) == 0 {
		s.pbase.m[id] = nil
		view = &portBaselineView{Ports: []string{}, UpdatedAt: now.Unix()}
	} else {
		s.pbase.m[id] = view
	}
	s.pbase.mu.Unlock()
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: info(r).session.User.Username, Action: "server.port_baseline",
		TargetType: "server", TargetID: id, Success: true, Details: map[string]any{"ports": strings.Join(ports, ",")}})
	writeJSON(w, view)
}
