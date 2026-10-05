package server

// 批量新建节点（设计 27.9）：全部校验通过才创建，每个节点独立的注册码；导出安装命令、CSV 与 Ansible。
//
// 【安全】
//   - 每台主机始终使用自己的注册码，不提供多台共用一个注册码的方式（27.9）
//   - 导出的 Ansible playbook 与安装命令相同：先下载、按已验签的哈希校验、再执行，不使用管道（约束 9、27.3）
//   - 导出中含注册码明文，只在本次响应中返回；注册码一次性、默认 24 小时有效
//   - CSV 单元格以 = + - @ 开头时加前缀，防止在表格软件中被当作公式执行

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxBatch = 200

// handleBatchCreate：POST /api/v1/servers/batch，admin。
func (s *Server) handleBatchCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items     []createServerBody `json:"items"`
		EnrollTTL string             `json:"enroll_ttl"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	if len(body.Items) == 0 || len(body.Items) > maxBatch {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed,
			Details: []FieldError{{Field: "items", Message: fmt.Sprintf("一次可新建 1～%d 个节点", maxBatch)}}})
		return
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

	existing := map[string]bool{}
	rows, err := s.store.ListServers()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	for _, row := range rows {
		existing[strings.ToLower(row.Name)] = true
	}
	var fe []FieldError
	inputs := make([]NodeInput, len(body.Items))
	seen := map[string]int{}
	for i := range body.Items {
		b := body.Items[i]
		b.EnrollTTL = "" // 以请求的 enroll_ttl 为准
		in, _, errs := b.validate()
		prefix := "items[" + strconv.Itoa(i) + "]."
		for _, e := range errs {
			fe = append(fe, FieldError{Field: prefix + e.Field, Message: e.Message})
		}
		key := strings.ToLower(in.Name)
		switch {
		case in.Name == "":
		case existing[key]:
			fe = append(fe, FieldError{Field: prefix + "name", Message: "名称已被使用"})
		default:
			if j, dup := seen[key]; dup {
				fe = append(fe, FieldError{Field: prefix + "name", Message: fmt.Sprintf("与第 %d 行重名", j+1)})
			}
			seen[key] = i
		}
		inputs[i] = in
	}
	if len(fe) > 0 {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}

	now := time.Now()
	codes := make([]string, len(inputs))
	ncs := make([]newCode, len(inputs))
	for i := range inputs {
		codes[i], ncs[i] = newEnrollCode(ttl, now)
	}
	ids, err := s.store.CreatePendingServers(inputs, ncs, now)
	if err == errNameTaken { // 并发新建了同名节点
		s.writeError(w, r, &APIError{Code: CodeConflict, Message: "名称已被使用，请刷新后重试"})
		return
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}

	views := make([]enrollCodeView, len(ids))
	for i, id := range ids {
		views[i] = enrollCodeView{ServerID: id, ServerName: inputs[i].Name, EnrollState: enrollPending, EnrollCode: codes[i],
			EnrollCodeHint: ncs[i].hint, EnrollStatus: codeActive, EnrollExpiresAt: ncs[i].expiresAt, Install: s.installCommand(r, codes[i])}
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "server.batch_create", Success: true,
		Details: map[string]any{"count": len(ids), "ids": ids, "ttl": ttl.String()}})
	panel := s.panelURL(r)
	exports := map[string]any{"commands": batchCommands(views), "csv": batchCSV(views), "ansible": nil}
	if src, ok := s.installerSource(panel); ok {
		exports["ansible"] = batchAnsible(views, inputs, src, panel)
	}
	writeJSONStatus(w, http.StatusCreated, map[string]any{"items": views, "exports": exports})
}

// CreatePendingServers 在同一事务中新建多个待安装节点与各自的注册码；任一失败则全部回滚。
func (s *Store) CreatePendingServers(ins []NodeInput, codes []newCode, now time.Time) ([]int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ids := make([]int64, len(ins))
	for i, in := range ins {
		res, err := tx.Exec(`INSERT INTO servers (name, enroll_state, expected_hostname, expected_ipv4, expected_ipv6,
				verify_mode, group_name, note, provider, plan, region, traffic_limit_bytes, traffic_reset_day,
				traffic_count_mode, price_cents, currency, billing_period, expire_date, created_at, updated_at, country,
				traffic_unit, traffic_factor, bandwidth_mbps, report_interval_s, traffic_timezone)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			in.Name, enrollPending, in.ExpectedHostname, in.ExpectedIPv4, in.ExpectedIPv6, in.VerifyMode,
			in.Group, in.Note, in.Provider, in.Plan, in.Region, in.LimitBytes, in.ResetDay, in.CountMode,
			in.PriceCents, in.Currency, in.BillingPeriod, in.ExpireDate, now.Unix(), now.Unix(), in.Country, in.Unit, in.Factor,
			in.BandwidthMbps, in.ReportIntervalS, in.TrafficTimezone)
		if isUniqueName(err) {
			return nil, errNameTaken
		}
		if err != nil {
			return nil, err
		}
		ids[i], _ = res.LastInsertId()
		if err := insertCode(tx, ids[i], codes[i], now); err != nil {
			return nil, err
		}
	}
	return ids, tx.Commit()
}

// ---- 导出 ----

func batchCommands(views []enrollCodeView) string {
	var b strings.Builder
	b.WriteString("# 每台主机执行自己的一条命令；注册码一次性使用\n")
	for _, v := range views {
		fmt.Fprintf(&b, "\n# %s\n%s\n", v.ServerName, v.Install.Command)
	}
	return b.String()
}

func batchCSV(views []enrollCodeView) string {
	var buf bytes.Buffer
	buf.WriteString("\ufeff") // BOM：Excel 按 UTF-8 打开中文名称
	w := csv.NewWriter(&buf)
	w.Write([]string{"名称", "注册码", "有效期至", "安装命令"})
	for _, v := range views {
		w.Write([]string{csvSafe(v.ServerName), v.EnrollCode, time.Unix(v.EnrollExpiresAt, 0).Format("2006-01-02 15:04"),
			csvSafe(v.Install.Command)})
	}
	w.Flush()
	return buf.String()
}

var ansibleHostUnsafe = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// yamlQuote 输出双引号 YAML 字符串（JSON 字符串是合法的 YAML 双引号标量）。
func yamlQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// batchAnsible 生成 inventory 与 playbook（设计 27.9）：与安装命令相同的下载 → 校验 → 执行。
func batchAnsible(views []enrollCodeView, ins []NodeInput, src installerSrc, panel string) string {
	var b strings.Builder
	b.WriteString("# ---- inventory.yml（vpsmon 导出）：脚本地址与哈希来自面板验签后的官方版本（设计 27.5.5）----\n")
	b.WriteString("# 注册码一次性使用，有效期见每台主机的 enroll_expires；请妥善保管本文件，用完删除\n")
	b.WriteString("all:\n  vars:\n")
	fmt.Fprintf(&b, "    vpsmon_server: %s\n", yamlQuote(panel))
	fmt.Fprintf(&b, "    agent_sh_url: %s\n", yamlQuote(src.script))
	fmt.Fprintf(&b, "    agent_sh_sha256: %s\n", yamlQuote(src.rel.InstallerSHA256))
	fmt.Fprintf(&b, "    agent_extra_args: %s\n", yamlQuote(strings.TrimSpace(src.mirror)))
	b.WriteString("  hosts:\n")
	used := map[string]bool{}
	for i, v := range views {
		alias := strings.Trim(ansibleHostUnsafe.ReplaceAllString(v.ServerName, "-"), "-.")
		// 主机别名须以字母开头：纯数字的键在 YAML 中会被解析为整数
		if alias == "" || used[alias] || !(alias[0] >= 'A' && alias[0] <= 'Z' || alias[0] >= 'a' && alias[0] <= 'z') {
			alias = "node-" + strconv.FormatInt(v.ServerID, 10)
		}
		used[alias] = true
		host := ins[i].ExpectedIPv4
		if host == "" {
			host = ins[i].ExpectedIPv6
		}
		if host == "" {
			host = ins[i].ExpectedHostname
		}
		fmt.Fprintf(&b, "    %s:\n", alias)
		if host != "" {
			fmt.Fprintf(&b, "      ansible_host: %s\n", yamlQuote(host))
		} else {
			b.WriteString("      # ansible_host: 请填写这台主机的地址\n")
		}
		fmt.Fprintf(&b, "      vpsmon_name: %s\n      enroll: %s\n      enroll_expires: %s\n", yamlQuote(v.ServerName),
			yamlQuote(v.EnrollCode), yamlQuote(time.Unix(v.EnrollExpiresAt, 0).Format(time.RFC3339)))
	}
	b.WriteString(`
# ---- install-agent.yml：先下载、再按哈希校验、后执行；checksum 不匹配时任务失败，后面的命令不会执行 ----
# 用法：ansible-playbook -i inventory.yml install-agent.yml
- hosts: all
  become: true
  tasks:
    - name: 下载安装脚本并校验哈希
      ansible.builtin.get_url:
        url: "{{ agent_sh_url }}"
        dest: /tmp/vpsmon-agent.sh
        checksum: "sha256:{{ agent_sh_sha256 }}"
        mode: "0700"
    - name: 安装并注册 Agent（每台主机使用自己的注册码）
      ansible.builtin.command: >
        sh /tmp/vpsmon-agent.sh --server {{ vpsmon_server }} --enroll {{ enroll }} {{ agent_extra_args }}
    - name: 删除安装脚本
      ansible.builtin.file:
        path: /tmp/vpsmon-agent.sh
        state: absent
`)
	return b.String()
}
