package server

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// 防火墙只给管理员（修订第 168 条）：节点详情中管理员能看到；查看者、只读 API Key 与节点列表都省略。
func TestFirewallAdminOnly(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	id, tok, _ := s.store.CreateServer("fw-host", 0, 1)
	body := fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"firewall":{"tools":[
		{"name":"ufw","active":true,"policy":"deny","rules_readable":true,"rules":[{"action":"allow","proto":"tcp","port":"22"}]}]}}`, time.Now().Unix())
	if rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(body)); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}
	path := "/api/v1/servers/" + itoa(id)
	if got := do(h, "GET", path, admin, nil).Body.String(); !strings.Contains(got, `"firewall"`) || !strings.Contains(got, `"port":"22"`) {
		t.Fatalf("管理员应能看到防火墙：%s", got)
	}
	key, _ := s.store.CreateAPIKey(&APIKey{Name: "k", ScopeType: "all"}, time.Now())
	for name, cred := range map[string]string{"查看者": viewerToken(t, s, "viewer-fw"), "API Key": key} {
		if got := do(h, "GET", path, cred, nil).Body.String(); strings.Contains(got, `"firewall"`) {
			t.Errorf("%s 不应看到防火墙：%s", name, got)
		}
	}
	if got := do(h, "GET", "/api/v1/servers", admin, nil).Body.String(); strings.Contains(got, `"firewall"`) {
		t.Errorf("节点列表不应带防火墙：%s", got)
	}
}

// 省略未变化字段（修订第 271 条）：面板声明支持；省略的端口、防火墙、代理用上一份补全，之后与完整上报相同；
// 没有上一份时返回 409，不按缺字段的上报处理。
func TestReportUnchanged(t *testing.T) {
	s, h, _ := testServer(t)
	id, tok, _ := s.store.CreateServer("delta-host", 0, 1)
	now := time.Now().Unix()
	partial := fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"unchanged":["ports","firewall","proxies"]}`, now)
	rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(partial))
	if rec.Code != 409 {
		t.Fatalf("没有上一份时应返回 409：%d %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("X-Report-Features"); got != "unchanged" {
		t.Errorf("X-Report-Features = %q", got)
	}
	full := fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"ports":[{"port":22,"proto":"tcp","addrs":["0.0.0.0"]}],
		"firewall":{"tools":[{"name":"ufw","active":true}]},"proxies":[{"name":"xray","running":true}]}`, now)
	if rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(full)); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}
	partial = fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"unchanged":["ports","firewall","proxies","future_field"]}`, now+10)
	if rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(partial)); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}
	snap := s.latest[id]
	if snap == nil || len(snap.Report.Ports) != 1 || snap.Report.Firewall == nil || len(snap.Report.Proxies) != 1 || snap.Report.Unchanged != nil {
		t.Errorf("应补全省略的字段：%+v", snap)
	}
}
