package server

// 公网可达检测（修订第 171 条）：Agent 只看得到“监听”，是否真能从公网访问还取决于防火墙与云安全组。
// 管理员在节点详情中手动触发：面板对节点的公网地址逐个 TCP 连接它报告的、对公网开放的监听端口。
//
// 【安全】
//   - 只连接节点自己的公网地址（注册时记录的 IPv4，其次预期 IPv4、IPv6；内网地址不检测）与它自己报告的 TCP 监听端口，
//     最多 50 个；不接受任意目标与端口范围，不是端口扫描器
//   - 连接成功即断开，不发送任何数据；UDP 不检测（无法可靠判断）
//   - 只手动触发、只给管理员：自动定时检测会在节点日志里留下大量连接记录，fail2ban 等可能封禁面板地址
//
// 结果反映的是从面板所在网络能否访问；面板与节点在同一内网时可能比真实的公网暴露更多。只保存在内存中。

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	exposureMaxPorts    = 50
	exposureTimeout     = 3 * time.Second
	exposureConcurrency = 10
)

// exposureResult 是一个端口的检测结果。
type exposureResult struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"`
	State string `json:"state"` // open / refused / timeout / error / skipped
	Ms    int64  `json:"ms,omitempty"`
}

// portExposure 是一次检测的结果。
type portExposure struct {
	CheckedAt int64            `json:"checked_at"`
	Target    string           `json:"target"`
	Results   []exposureResult `json:"results"`
}

// exposureState 保存各节点最近一次的结果，并防止同一节点同时检测。
type exposureState struct {
	mu      sync.Mutex
	last    map[int64]*portExposure
	running map[int64]bool
	// dial 建立 TCP 连接（测试中替换）
	dial func(ctx context.Context, addr string) (net.Conn, error)
}

func newExposureState() *exposureState {
	d := &net.Dialer{}
	return &exposureState{last: map[int64]*portExposure{}, running: map[int64]bool{},
		dial: func(ctx context.Context, addr string) (net.Conn, error) { return d.DialContext(ctx, "tcp", addr) }}
}

// exposureTarget 选出节点的公网地址：注册时的 IPv4、预期 IPv4、IPv6 中第一个公网地址。
func exposureTarget(row ServerRow) (string, bool) {
	for _, a := range []string{row.IPv4, row.ExpectedIPv4, row.IPv6, row.ExpectedIPv6} {
		a = strings.TrimSpace(a)
		if ip, err := netip.ParseAddr(a); err == nil && !ip.IsUnspecified() && publicAddr(a) {
			return ip.String(), true
		}
	}
	return "", false
}

// classifyDial 把连接错误归类：被拒绝、超时或其他。
func classifyDial(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	}
	return "error"
}

// checkExposure 逐个连接端口（并发、每个最多 3 秒）。
func (s *Server) checkExposure(ctx context.Context, target string, ports []string) []exposureResult {
	out := make([]exposureResult, len(ports))
	sem := make(chan struct{}, exposureConcurrency)
	var wg sync.WaitGroup
	for i, k := range ports {
		n, proto, _ := strings.Cut(k, "/")
		port, _ := strconv.Atoi(n)
		out[i] = exposureResult{Port: port, Proto: proto}
		if proto != "tcp" {
			out[i].State = "skipped"
			continue
		}
		wg.Add(1)
		go func(i, port int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, exposureTimeout)
			defer cancel()
			start := time.Now()
			conn, err := s.exposure.dial(cctx, net.JoinHostPort(target, strconv.Itoa(port)))
			if err != nil {
				out[i].State = classifyDial(err)
				return
			}
			conn.Close() // 连接成功即断开，不发送任何数据
			out[i].State, out[i].Ms = "open", max(time.Since(start).Milliseconds(), 1)
		}(i, port)
	}
	wg.Wait()
	return out
}

// handleExposureCheck：POST /api/v1/servers/{id}/exposure-check，admin。检测并返回结果。
func (s *Server) handleExposureCheck(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	row, err := s.store.GetServer(id)
	if errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	} else if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	target, ok := exposureTarget(*row)
	if !ok {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "target", Message: "节点没有公网 IP，无法从外部检测"}}})
		return
	}
	s.mu.Lock()
	var ports []string
	ok = false
	if sn := s.latest[id]; sn != nil {
		rep := sn.Report
		ports, ok = exposedPorts(&rep)
	}
	s.mu.Unlock()
	if !ok {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "ports", Message: "还没有收到这台节点的监听端口（节点离线或 Agent 版本过旧）"}}})
		return
	}
	if len(ports) > exposureMaxPorts {
		ports = ports[:exposureMaxPorts]
	}
	e := s.exposure
	e.mu.Lock()
	if e.running[id] {
		e.mu.Unlock()
		s.writeError(w, r, errorf(CodeConflict, "正在检测，请稍候"))
		return
	}
	e.running[id] = true
	e.mu.Unlock()
	res := &portExposure{CheckedAt: time.Now().Unix(), Target: target, Results: s.checkExposure(r.Context(), target, ports)}
	e.mu.Lock()
	e.last[id], e.running[id] = res, false
	e.mu.Unlock()
	open := 0
	for _, x := range res.Results {
		if x.State == "open" {
			open++
		}
	}
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: info(r).session.User.Username, Action: "server.exposure_check",
		TargetType: "server", TargetID: id, Success: true, Details: map[string]any{"ports": len(ports), "open": open}})
	writeJSON(w, res)
}

// lastExposure 返回节点最近一次的检测结果；没有时为 nil。
func (s *Server) lastExposure(id int64) *portExposure {
	s.exposure.mu.Lock()
	defer s.exposure.mu.Unlock()
	return s.exposure.last[id]
}
