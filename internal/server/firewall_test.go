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
