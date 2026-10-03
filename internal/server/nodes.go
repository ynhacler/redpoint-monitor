package server

import (
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Web 新建节点与安装命令（设计 19.11、27.1～27.4）。全部接口要求 Web 管理员权限（设计 17.2）。

// billingPeriods 是续费周期的可选值（设计 1.2.3、1.2.5）；空字符串表示未填。
var billingPeriods = map[string]bool{"": true, "monthly": true, "quarterly": true, "semiannually": true,
	"annually": true, "biennially": true, "triennially": true, "one_time": true}

var (
	hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,252})$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`) // ISO 4217
	countryPattern  = regexp.MustCompile(`^[A-Z]{2}$`) // ISO 3166-1 alpha-2
	// publicHost 是允许写进安装命令的面板主机名（含端口、IPv6 方括号）。
	// 【安全】安装命令会被复制到主机上以 root 执行，Host 头中的任何 shell 特殊字符都不允许进入命令。
	publicHost = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:[0-9]{1,5})?$|^\[[0-9A-Fa-f:.]+\](:[0-9]{1,5})?$`)
)

// createServerBody 是 POST /api/v1/servers 的请求体（设计 19.11、27.2）。
type createServerBody struct {
	Name             string   `json:"name"`
	ExpectedHostname string   `json:"expected_hostname"`
	ExpectedIPv4     string   `json:"expected_ipv4"`
	ExpectedIPv6     string   `json:"expected_ipv6"`
	Group            string   `json:"group"`
	Note             string   `json:"note"`
	Provider         string   `json:"provider"`
	Plan             string   `json:"plan"`
	Region           string   `json:"region"`
	Country          string   `json:"country"`           // ISO 3166-1 两位代码，大小写不敏感
	BandwidthMbps    *int     `json:"bandwidth_mbps"`    // 标称带宽 Mbps；空或 0 表示未填
	TrafficLimitGB   *float64 `json:"traffic_limit_gb"`  // 按 traffic_unit 口径的 GB / GiB；空或 0 表示不限
	TrafficUnit      string   `json:"traffic_unit"`      // decimal（默认）/ binary（设计 5.8）
	TrafficFactor    *float64 `json:"traffic_factor"`    // 统计系数 0.5～2，默认 1（设计 5.7）
	TrafficResetDay  *int     `json:"traffic_reset_day"` // 1～31，默认 1
	TrafficCountMode string   `json:"traffic_count_mode"`
	Price            *float64 `json:"price"` // 续费价格，最多两位小数
	Currency         string   `json:"currency"`
	BillingPeriod    string   `json:"billing_period"`
	ExpireDate       string   `json:"expire_date"` // YYYY-MM-DD
	EnrollTTL        string   `json:"enroll_ttl"`  // 1h / 24h / 7d，默认 24h
	VerifyMode       string   `json:"verify_mode"` // warn / strict，默认 warn
}

// validate 校验并规范化输入，返回字段级错误（设计 43.4 validation_failed）。
func (b *createServerBody) validate() (NodeInput, time.Duration, []FieldError) {
	var errs []FieldError
	bad := func(field, msg string) { errs = append(errs, FieldError{Field: field, Message: msg}) }
	text := func(field, v string, max int) string {
		v = strings.TrimSpace(v)
		if utf8.RuneCountInString(v) > max {
			bad(field, "不能超过 "+strconv.Itoa(max)+" 个字符")
		}
		return v
	}

	in := NodeInput{
		Name:     text("name", b.Name, 64),
		Group:    text("group", b.Group, 32),
		Note:     text("note", b.Note, 500),
		Provider: text("provider", b.Provider, 64),
		Plan:     text("plan", b.Plan, 64),
		Region:   text("region", b.Region, 64),
	}
	if in.Name == "" {
		bad("name", "请填写名称")
	}
	in.Country = strings.ToUpper(strings.TrimSpace(b.Country))
	if in.Country != "" && !countryPattern.MatchString(in.Country) {
		bad("country", "国家 / 地区代码应为两位字母，如 JP、HK")
	}
	// 带宽由服务商标称，Agent 无法采集（设计 27.2）；上限 1 Tbps 足以覆盖独服与大带宽 VPS
	if b.BandwidthMbps != nil {
		if *b.BandwidthMbps < 0 || *b.BandwidthMbps > 1_000_000 {
			bad("bandwidth_mbps", "带宽应在 0～1000000 Mbps 之间")
		} else {
			in.BandwidthMbps = *b.BandwidthMbps
		}
	}
	if h := strings.TrimSpace(b.ExpectedHostname); h != "" {
		if !hostnamePattern.MatchString(h) {
			bad("expected_hostname", "主机名只能包含字母、数字、点、短横线和下划线")
		}
		in.ExpectedHostname = h
	}
	if v := strings.TrimSpace(b.ExpectedIPv4); v != "" {
		if ip := net.ParseIP(v); ip == nil || ip.To4() == nil || strings.Contains(v, ":") {
			bad("expected_ipv4", "IPv4 地址格式不正确")
		} else {
			in.ExpectedIPv4 = ip.String()
		}
	}
	if v := strings.Trim(strings.TrimSpace(b.ExpectedIPv6), "[]"); v != "" {
		if ip := net.ParseIP(v); ip == nil || !strings.Contains(v, ":") {
			bad("expected_ipv6", "IPv6 地址格式不正确")
		} else {
			in.ExpectedIPv6 = ip.String()
		}
	}

	in.VerifyMode = b.VerifyMode
	if in.VerifyMode == "" {
		in.VerifyMode = "warn"
	}
	if in.VerifyMode != "warn" && in.VerifyMode != "strict" {
		bad("verify_mode", "只能是 warn（仅提示）或 strict（不一致时拒绝）")
	}

	// 单位口径：服务商对 “1 TB” 的定义不一致，额度按节点选择的口径换算（设计 5.8）
	in.Unit = b.TrafficUnit
	if in.Unit == "" {
		in.Unit = UnitDecimal
	}
	if in.Unit != UnitDecimal && in.Unit != UnitBinary {
		bad("traffic_unit", "单位只能是 decimal（1 GB = 10⁹ 字节）或 binary（1 GiB = 2³⁰ 字节）")
	}
	if b.TrafficLimitGB != nil {
		gb := *b.TrafficLimitGB
		if gb < 0 || gb > 1e6 || math.IsNaN(gb) {
			bad("traffic_limit_gb", "月流量额度应在 0～1000000 GB 之间")
		} else {
			in.LimitBytes = GBToBytes(gb, in.Unit)
		}
	}
	// 系数用于修正固定比例偏差，0.5～2 足以覆盖实际情况，超出多半是输入错误（设计 5.7）
	in.Factor = 1
	if b.TrafficFactor != nil {
		in.Factor = *b.TrafficFactor
		if in.Factor < 0.5 || in.Factor > 2 || math.IsNaN(in.Factor) {
			bad("traffic_factor", "统计系数应在 0.5～2 之间")
		}
	}
	in.ResetDay = 1
	if b.TrafficResetDay != nil {
		in.ResetDay = *b.TrafficResetDay
		if in.ResetDay < 1 || in.ResetDay > 31 {
			bad("traffic_reset_day", "流量重置日应在 1～31 之间")
		}
	}
	in.CountMode = b.TrafficCountMode
	switch in.CountMode {
	case "":
		in.CountMode = ModeSum
	case ModeSum, ModeRx, ModeTx, ModeMax:
	default:
		bad("traffic_count_mode", "计费模式只能是 sum、rx、tx 或 max")
	}

	if b.Price != nil {
		p := *b.Price
		if p < 0 || p > 1e7 || math.IsNaN(p) {
			bad("price", "价格应在 0～10000000 之间")
		} else {
			in.PriceCents = int64(math.Round(p * 100))
		}
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(b.Currency))
	if in.Currency != "" && !currencyPattern.MatchString(in.Currency) {
		bad("currency", "币种使用 3 位字母代码，如 USD、CNY")
	}
	if in.PriceCents > 0 && in.Currency == "" {
		bad("currency", "填写价格时请同时填写币种")
	}
	in.BillingPeriod = b.BillingPeriod
	if !billingPeriods[in.BillingPeriod] {
		bad("billing_period", "续费周期不正确")
	}
	if d := strings.TrimSpace(b.ExpireDate); d != "" {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			bad("expire_date", "到期日期格式应为 YYYY-MM-DD")
		}
		in.ExpireDate = d
	}

	ttl, ok := enrollTTLs[b.EnrollTTL]
	if b.EnrollTTL == "" {
		ttl, ok = enrollTTLs["24h"], true
	}
	if !ok {
		bad("enroll_ttl", "注册码有效期只能是 1h、24h 或 7d")
	}
	return in, ttl, errs
}

// installView 是安装命令信息（设计 27.3）。
type installView struct {
	// Mode 为 "default"（下载 → 校验 → 执行）或 "manual"。
	// 面板尚未同步并验签任何官方版本时只提供 manual（设计 27.3.1）。
	Mode    string `json:"mode"`
	Command string `json:"command"`
	// ManualCommand 是手动方式的最后一步（二进制已在主机上时，设计 27.3.3）；两种模式都提供
	ManualCommand string `json:"manual_command"`
	// Release 是生成默认命令所用的已验签官方版本；未同步时为 null（设计 29.1）
	Release *agentRelease `json:"release"`
	Server  string        `json:"server"` // 写进命令的面板地址
}

// enrollCodeView 是注册码与安装命令的响应（新建节点、重新生成时返回明文，其余时候只返回提示）。
type enrollCodeView struct {
	ServerID        int64       `json:"server_id"`
	ServerName      string      `json:"server_name"`
	EnrollState     string      `json:"enroll_state"`
	EnrollCode      string      `json:"enroll_code,omitempty"` // 完整注册码只在生成时返回一次（设计 19.11）
	EnrollCodeHint  string      `json:"enroll_code_hint"`
	EnrollStatus    string      `json:"enroll_status"` // ACTIVE / USED / REVOKED / EXPIRED / NONE
	EnrollExpiresAt int64       `json:"enroll_expires_at"`
	Install         installView `json:"install"`
}

// panelURL 返回写进安装命令的面板对外地址：优先使用 --public-url，
// 否则由请求推断（在 Caddy 后面时 Host 为对外域名，X-Forwarded-Proto 为 https）。
func (s *Server) panelURL(r *http.Request) string {
	if s.publicURL != "" {
		return s.publicURL
	}
	scheme := "http"
	if r.TLS != nil || (fromTrustedProxy(r) && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	if !publicHost.MatchString(r.Host) {
		return "https://YOUR-PANEL-ADDRESS" // Host 头异常时给出占位符，提示管理员配置 --public-url
	}
	return scheme + "://" + r.Host
}

// installCommand 生成安装命令（设计 27.3.1）。code 为明文注册码或脱敏提示。
//
// 有已验签的官方正式版时生成默认命令：下载按版本固定的安装脚本 → 按已验签清单中的 SHA256 校验 → 执行。
// 哈希不一致时 sha256sum -c 失败，后面的命令不会执行。没有已验签版本时只提供手动方式（设计 27.3.3）。
// 【安全】不生成 curl | sh，也不由面板分发二进制（设计 27.3、CLAUDE.md 约束 2、9）。
func (s *Server) installCommand(r *http.Request, code string) installView {
	url := s.panelURL(r)
	v := installView{Mode: "manual", Server: url,
		ManualCommand: "sudo vpsmon-agent install --server " + url + " --enroll " + code}
	v.Command = v.ManualCommand
	if rel := s.latestStable(); rel != nil {
		v.Mode, v.Release = "default", rel
		// 已镜像时脚本与构建都从本面板下载（设计 27.5.3）；哈希仍来自已验签的清单
		script := strings.TrimRight(s.releaseBase, "/") + "/download/v" + rel.Version + "/" + rel.InstallerFile
		mirror := ""
		if rel.Mirrored && s.mirrorRoot != "" {
			script = url + "/releases/v" + rel.Version + "/" + rel.InstallerFile
			mirror = " --mirror " + url + "/releases"
		}
		v.Command = "curl -fsSLo agent.sh " + script +
			" && echo \"" + rel.InstallerSHA256 + "  agent.sh\" | sha256sum -c -" +
			" && sudo sh agent.sh --server " + url + " --enroll " + code + mirror
	}
	return v
}

// handleCreateServer：POST /api/v1/servers，admin。新建“待安装”节点并生成注册码（设计 19.11、27.2）。
// 成功 201；字段错误 422；名称重复 409。
func (s *Server) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	var body createServerBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields() // Web 表单的字段写错时直接报错，而不是被静默忽略
	if err := dec.Decode(&body); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	in, ttl, errs := body.validate()
	if len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	now := time.Now()
	code, nc := newEnrollCode(ttl, now)
	id, err := s.store.CreatePendingServer(in, nc, now)
	if errors.Is(err, errNameTaken) {
		s.writeError(w, r, &APIError{Code: CodeConflict, Message: "名称已被使用",
			Details: []FieldError{{Field: "name", Message: "名称已被使用"}}})
		return
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "server.create", TargetType: "server", TargetID: id,
		Success: true, Details: map[string]any{"name": in.Name, "code_hint": nc.hint, "ttl": ttl.String()}})
	w.Header().Set("Location", "/api/v1/servers/"+strconv.FormatInt(id, 10))
	writeJSONStatus(w, http.StatusCreated, enrollCodeView{ServerID: id, ServerName: in.Name, EnrollState: enrollPending,
		EnrollCode: code, EnrollCodeHint: nc.hint, EnrollStatus: codeActive, EnrollExpiresAt: nc.expiresAt,
		Install: s.installCommand(r, code)})
}

// handleGetServer：GET /api/v1/servers/{id}，admin。单个节点，格式与列表中的一项相同（设计 19.5）。
func (s *Server) handleGetServer(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	row, err := s.store.GetServer(id)
	if errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	v, err := s.viewOf(*row, time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeJSON(w, v)
}

// handleUpdateServer：PUT /api/v1/servers/{id}，admin。整体替换可编辑信息（字段同新建，enroll_ttl 忽略）；
// 未提供的可选字段视为清空。成功 200 返回最新节点；字段错误 422；名称重复 409；不存在 404。
func (s *Server) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var body createServerBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	body.EnrollTTL = "" // 修改信息不涉及注册码
	in, _, errs := body.validate()
	if len(errs) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: errs})
		return
	}
	switch err := s.store.UpdateServer(id, in, time.Now()); {
	case errors.Is(err, errNoServer):
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	case errors.Is(err, errNameTaken):
		s.writeError(w, r, &APIError{Code: CodeConflict, Message: "名称已被使用",
			Details: []FieldError{{Field: "name", Message: "名称已被使用"}}})
		return
	case err != nil:
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "server.update", TargetType: "server", TargetID: id,
		Success: true, Details: map[string]any{"name": in.Name}})
	s.handleGetServer(w, r)
}

// handleDeleteServer：DELETE /api/v1/servers/{id}，admin。删除节点及其全部历史数据，不可恢复，成功 204。
// 该节点的 Agent Token 随之删除，主机上的 Agent 之后上报会得到 401。
// 【安全】敏感操作：需在 10 分钟内重新输入过密码（POST /api/v1/auth/reauth，设计 17.4），否则 403 reauth_required。
func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := requireReauth(r); err != nil {
		s.writeError(w, r, err)
		return
	}
	name, err := s.store.DeleteServer(id)
	if errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	// 清理内存状态；同时丢弃尚未写库的上报，避免删除后又写入孤立的指标行
	s.mu.Lock()
	delete(s.latest, id)
	delete(s.counters, id)
	kept := s.pending[:0]
	for _, p := range s.pending {
		if p.serverID != id {
			kept = append(kept, p)
		}
	}
	s.pending = kept
	s.mu.Unlock()
	s.audit(r, AuditEntry{ActorType: "admin", Action: "server.delete", TargetType: "server", TargetID: id,
		Success: true, Details: map[string]any{"name": name}})
	s.log.Info("server deleted", "component", "servers", "server_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// pathID 解析路径中的节点 ID。
func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errorf(CodeBadRequest, "节点编号格式不正确")
	}
	return id, nil
}

// handleInstallCommand：GET /api/v1/servers/{id}/install-command，admin。
// 不返回已生成过的完整注册码（设计 19.11）：命令中只有脱敏提示，丢失后需重新生成。
func (s *Server) handleInstallCommand(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	node, err := s.store.GetServer(id)
	if errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	v := enrollCodeView{ServerID: id, ServerName: node.Name, EnrollState: node.EnrollState, EnrollStatus: "NONE"}
	c, err := s.store.LatestEnrollCode(id, time.Now())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	hint := "ENR-…"
	if c != nil {
		v.EnrollCodeHint, v.EnrollStatus, v.EnrollExpiresAt, hint = c.Hint, c.Status, c.ExpiresAt, c.Hint
	}
	v.Install = s.installCommand(r, hint)
	writeJSON(w, v)
}

// handleRegenerateCode：POST /api/v1/servers/{id}/enroll-code，admin。
// 生成新注册码，旧码立即失效（设计 27.4）；对已注册节点即“重新安装 / 更换主机”（设计 27.8）。
// 请求体可选：{"enroll_ttl": "24h"}。
func (s *Server) handleRegenerateCode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var body struct {
		EnrollTTL string `json:"enroll_ttl"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
			s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
			return
		}
	}
	ttl, ok := enrollTTLs[body.EnrollTTL]
	if body.EnrollTTL == "" {
		ttl, ok = enrollTTLs["24h"], true
	}
	if !ok {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed,
			Details: []FieldError{{Field: "enroll_ttl", Message: "注册码有效期只能是 1h、24h 或 7d"}}})
		return
	}
	now := time.Now()
	code, nc := newEnrollCode(ttl, now)
	if err := s.store.ReplaceEnrollCode(id, nc, now); errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	} else if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	node, err := s.store.GetServer(id)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "enroll_code.regenerate", TargetType: "server", TargetID: id,
		Success: true, Details: map[string]any{"code_hint": nc.hint, "ttl": ttl.String(), "enroll_state": node.EnrollState}})
	writeJSON(w, enrollCodeView{ServerID: id, ServerName: node.Name, EnrollState: node.EnrollState,
		EnrollCode: code, EnrollCodeHint: nc.hint, EnrollStatus: codeActive, EnrollExpiresAt: nc.expiresAt,
		Install: s.installCommand(r, code)})
}

// handleRevokeCode：DELETE /api/v1/servers/{id}/enroll-code，admin。撤销注册码，成功 204（设计 27.4）。
func (s *Server) handleRevokeCode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.store.RevokeEnrollCodes(id); errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	} else if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "enroll_code.revoke", TargetType: "server", TargetID: id, Success: true})
	w.WriteHeader(http.StatusNoContent)
}

// writeJSONStatus 以指定状态码返回 JSON（如 201 Created）。
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
