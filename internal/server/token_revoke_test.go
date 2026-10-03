package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// 吊销 Agent Token（设计 17.2、23.2）：需重新验证密码；吊销后立即无法上报，节点回到待安装、历史保留；
// 之后凭新注册码更换 Token（rotate-token），注册码属于其他节点时拒绝且不做修改。
func TestRevokeAndRotateAgentToken(t *testing.T) {
	s, h, _ := testServer(t)
	newAdmin(t, s, "correct horse battery", false)
	tok := sessionFrom(t, login(h, "admin", "correct horse battery", false, false)).Value
	_, a, _ := createNode(t, h, tok, `{"name":"node-a"}`)
	_, b, _ := createNode(t, h, tok, `{"name":"node-b"}`)
	_, res := enroll(h, a.EnrollCode, "host-a", "machine-a")
	report := func(token string) int {
		return do(h, "POST", "/api/v1/agent/report", token, []byte(`{"system":{"boot_id":"x"}}`)).Code
	}
	if c := report(res.AgentToken); c != 204 {
		t.Fatalf("注册后应能上报：%d", c)
	}

	path := "/api/v1/servers/" + itoa(a.ServerID) + "/revoke-agent-token"
	if rec := do(h, "POST", path, tok, nil); rec.Code != 403 || decodeError(t, rec).Code != CodeReauthRequired {
		t.Fatalf("【安全】吊销 Token 需重新验证密码：%d %s", rec.Code, rec.Body)
	}
	do(h, "POST", "/api/v1/auth/reauth", tok, []byte(`{"password":"correct horse battery"}`))
	if rec := do(h, "POST", path, tok, nil); rec.Code != 204 {
		t.Fatalf("吊销：%d %s", rec.Code, rec.Body)
	}
	if c := report(res.AgentToken); c != 401 {
		t.Errorf("吊销后旧 Token 应立即失效：%d", c)
	}
	if row, _ := s.store.GetServer(a.ServerID); row.EnrollState != enrollPending {
		t.Errorf("吊销后节点应回到待安装：%s", row.EnrollState)
	}
	var logs struct{ Items []AuditLog }
	json.Unmarshal(do(h, "GET", "/api/v1/audit-logs?category=operation", tok, nil).Body.Bytes(), &logs)
	if len(logs.Items) == 0 || logs.Items[0].Action != "agent_token.revoke" {
		t.Errorf("吊销应记入操作日志：%+v", logs.Items)
	}

	// 生成新注册码；用节点 B 的身份去换 → 拒绝，注册码仍可用
	rec := do(h, "POST", "/api/v1/servers/"+itoa(a.ServerID)+"/enroll-code", tok, nil)
	var regen enrollCodeView
	json.Unmarshal(rec.Body.Bytes(), &regen)
	if regen.EnrollCode == "" {
		t.Fatalf("重新生成注册码：%d %s", rec.Code, rec.Body)
	}
	rotate := func(serverID int64) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"enroll_code": regen.EnrollCode, "hostname": "host-a", "machine_id_hash": "machine-a",
			"os": "ubuntu", "arch": "amd64", "agent_version": "v-test", "server_id": serverID})
		req := httptest.NewRequest("POST", "/api/v1/agent/enroll", strings.NewReader(string(body)))
		req.RemoteAddr = "203.0.113.7:4000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := rotate(b.ServerID); w.Code != 403 || !strings.Contains(w.Body.String(), "其他节点") {
		t.Fatalf("【安全】注册码属于其他节点时应拒绝：%d %s", w.Code, w.Body)
	}
	if row, _ := s.store.GetServer(b.ServerID); row.EnrollState != enrollPending {
		t.Error("被拒绝的请求不应改动节点 B")
	}
	w := rotate(a.ServerID)
	var got enrollResponse
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.AgentToken == "" || got.ServerID == 0 {
		t.Fatalf("更换 Token：%d %s", w.Code, w.Body)
	}
	if c := report(got.AgentToken); c != 204 {
		t.Errorf("新 Token 应能上报：%d", c)
	}

	// 审计按操作前缀筛选（vpsmon-server audit --action agent_token.）
	list, _, err := s.store.ListAudit(AuditQuery{Action: "agent_token.", Limit: 10})
	if err != nil || len(list) != 1 || list[0].Action != "agent_token.revoke" {
		t.Errorf("按前缀筛选：%+v %v", list, err)
	}
	if list, _, _ := s.store.ListAudit(AuditQuery{Action: "agent_token", Limit: 10}); len(list) != 0 {
		t.Errorf("不以 . 结尾时应精确匹配：%+v", list)
	}
}
