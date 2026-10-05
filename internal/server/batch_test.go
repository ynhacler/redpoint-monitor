package server

import (
	"encoding/json"
	"strings"
	"testing"
)

type batchResp struct {
	Items   []enrollCodeView `json:"items"`
	Exports struct {
		Commands string  `json:"commands"`
		CSV      string  `json:"csv"`
		Ansible  *string `json:"ansible"`
	} `json:"exports"`
}

// 批量新建（设计 27.9）：全部校验通过才创建；每个节点独立注册码；三种导出
func TestBatchCreate(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	createNode(t, h, admin, `{"name":"exists"}`)

	// 任一项不合法：全部不创建，错误带行号
	rec := do(h, "POST", "/api/v1/servers/batch", admin, []byte(`{"items":[{"name":"hk-1"},{"name":"exists"},{"name":"hk-1"},{"name":"bad","expected_ipv4":"1.2.3"}]}`))
	if rec.Code != 422 {
		t.Fatalf("应校验 %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, f := range []string{`"items[1].name"`, `"items[2].name"`, `"items[3].expected_ipv4"`} {
		if !strings.Contains(body, f) {
			t.Errorf("缺少字段错误 %s：%s", f, body)
		}
	}
	if rows, _ := s.store.ListServers(); len(rows) != 1 {
		t.Fatalf("校验失败时不应创建任何节点：%d", len(rows))
	}
	if rec := do(h, "POST", "/api/v1/servers/batch", admin, []byte(`{"items":[]}`)); rec.Code != 422 {
		t.Fatalf("空列表 %d", rec.Code)
	}

	rec = do(h, "POST", "/api/v1/servers/batch", admin, []byte(`{"enroll_ttl":"7d","items":[
		{"name":"hk-1","expected_ipv4":"103.1.2.3","group":"hk","traffic_limit_gb":1000,"traffic_reset_day":5,"expire_date":"2027-01-01"},
		{"name":"=cmd|' /C calc'!A0"},
		{"name":"东京 1","expected_hostname":"tokyo.example.com"}]}`))
	if rec.Code != 201 {
		t.Fatalf("批量新建 %d %s", rec.Code, rec.Body)
	}
	var v batchResp
	json.Unmarshal(rec.Body.Bytes(), &v)
	if len(v.Items) != 3 {
		t.Fatalf("items %+v", v.Items)
	}
	codes := map[string]bool{}
	for _, it := range v.Items {
		if !enrollCodeFormat.MatchString(it.EnrollCode) || codes[it.EnrollCode] {
			t.Fatalf("【安全】每个节点应有独立的注册码：%+v", it)
		}
		codes[it.EnrollCode] = true
		if !strings.Contains(v.Exports.Commands, it.EnrollCode) {
			t.Errorf("命令导出缺少 %s", it.ServerName)
		}
	}
	row, _ := s.store.GetServer(v.Items[0].ServerID)
	if row.Group != "hk" || row.ExpireDate != "2027-01-01" || row.ResetDay != 5 {
		t.Fatalf("字段未写入 %+v", row)
	}
	if d := v.Items[0].EnrollExpiresAt - row.CreatedAt; d < 7*86400-60 || d > 7*86400+60 {
		t.Fatalf("enroll_ttl 7d：有效期 %d 秒", d)
	}
	// CSV：带表头、BOM，公式前缀
	if !strings.HasPrefix(v.Exports.CSV, "\ufeff名称,注册码,有效期至,安装命令") || !strings.Contains(v.Exports.CSV, `'=cmd`) {
		t.Fatalf("CSV %s", v.Exports.CSV)
	}
	// 没有已验签的官方版本：不提供 Ansible（安装命令为手动方式）
	if v.Exports.Ansible != nil {
		t.Fatal("没有已验签版本时 Ansible 导出应为 null")
	}
	rec = do(h, "POST", "/api/v1/servers/batch", admin, []byte(`{"items":[{"name":"hk-1"}]}`))
	if rec.Code != 422 {
		t.Fatalf("再次新建同名应拒绝：%d", rec.Code)
	}
}

func TestBatchAnsible(t *testing.T) {
	views := []enrollCodeView{{ServerID: 7, ServerName: "东京 1", EnrollCode: "ENR-AAAA-BBBB-CCCC-DDDD", EnrollExpiresAt: 1790000000},
		{ServerID: 8, ServerName: "hk-1", EnrollCode: "ENR-EEEE-FFFF-GGGG-HHHH", EnrollExpiresAt: 1790000000}}
	ins := []NodeInput{{ExpectedHostname: "tokyo.example.com"}, {ExpectedIPv4: "103.1.2.3"}}
	src := installerSrc{rel: &agentRelease{Version: "0.3.0", InstallerSHA256: "abc123"}, script: "https://example.com/agent-0.3.0.sh"}
	out := batchAnsible(views, ins, src, "https://m.example.com")
	for _, want := range []string{
		`agent_sh_sha256: "abc123"`, `checksum: "sha256:{{ agent_sh_sha256 }}"`, "    node-7:", `ansible_host: "tokyo.example.com"`,
		"    hk-1:", `ansible_host: "103.1.2.3"`, `enroll: "ENR-EEEE-FFFF-GGGG-HHHH"`, `vpsmon_name: "东京 1"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %q：\n%s", want, out)
		}
	}
	// 【安全】先下载校验再执行，不使用管道（约束 9）
	if strings.Contains(out, "| sh") || strings.Contains(out, "|sh") {
		t.Fatal("不应出现管道执行")
	}
}
