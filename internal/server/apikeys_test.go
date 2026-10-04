package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func createAPIKey(t *testing.T, h http.Handler, admin, body string) (int, string, APIKey, errorPayload) {
	t.Helper()
	rec := do(h, "POST", "/api/v1/api-keys", admin, []byte(body))
	var v struct {
		Key    string `json:"key"`
		APIKey APIKey `json:"api_key"`
	}
	var e errorPayload
	if rec.Code == http.StatusCreated {
		json.Unmarshal(rec.Body.Bytes(), &v)
	} else {
		e = decodeError(t, rec)
	}
	return rec.Code, v.Key, v.APIKey, e
}

func TestAPIKeyCreateAndUse(t *testing.T) {
	s, h, logs := testServer(t)
	admin := adminToken(t, s)

	// 【安全】创建需要重新验证密码
	u, _ := s.store.UserByName("admin")
	plain, _ := s.store.CreateSession(u.ID, "127.0.0.1", "test", time.Now())
	if code, _, _, e := createAPIKey(t, h, plain, `{"name":"grafana"}`); code != http.StatusForbidden || e.Code != CodeReauthRequired {
		t.Fatalf("未重新验证：%d %s", code, e.Code)
	}
	code, key, k, e := createAPIKey(t, h, admin, `{"name":"grafana"}`)
	if code != http.StatusCreated || !strings.HasPrefix(key, "api_") || k.ScopeType != "all" || k.Hint != key[len(key)-4:] || k.CreatedBy != "admin" {
		t.Fatalf("创建 %d %q %+v %+v", code, key, k, e)
	}
	// 【安全】只保存哈希；列表不返回完整 Key
	var stored string
	s.store.DB.QueryRow(`SELECT token_hash FROM api_keys WHERE id = ?`, k.ID).Scan(&stored)
	if stored != HashToken(key) {
		t.Fatal("应只保存 SHA-256 哈希")
	}
	if list := do(h, "GET", "/api/v1/api-keys", admin, nil).Body.String(); strings.Contains(list, key) {
		t.Fatal("【安全】列表中不应出现完整 Key")
	}

	// 读取接口可用；首次使用记入审计
	if rec := do(h, "GET", "/api/v1/version", key, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"version"`) {
		t.Fatalf("version %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/servers", key, nil); rec.Code != 200 {
		t.Fatalf("servers %d", rec.Code)
	}
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'api_key.first_use'`).Scan(&n)
	if n != 1 {
		t.Fatalf("首次使用应记入审计 1 次，实际 %d", n)
	}

	// 【安全】不能写、不能管理 Key、不能当作 Agent Token；只接受 Authorization 头
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/servers"}, {"GET", "/api/v1/api-keys"}, {"POST", "/api/v1/api-keys"},
		{"GET", "/api/v1/audit-logs"}, {"POST", "/api/v1/agent/report"}, {"GET", "/api/v1/auth/me"},
	} {
		if rec := do(h, c.method, c.path, key, []byte(`{}`)); rec.Code != http.StatusUnauthorized {
			t.Errorf("【安全】API Key 访问 %s %s 应为 401，得到 %d", c.method, c.path, rec.Code)
		}
	}
	req := httptest.NewRequest("GET", "/api/v1/servers?token="+key, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("【安全】查询串中的 Key 不应被接受：%d", rec.Code)
	}

	// 吊销后立即失效
	if rec := do(h, "DELETE", "/api/v1/api-keys/"+itoa(k.ID), admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("吊销 %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/servers", key, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("吊销后应为 401，得到 %d", rec.Code)
	}
	// 【安全】日志中不出现完整 Key
	if strings.Contains(logs.String(), key) {
		t.Fatal("【安全】日志中出现了完整 API Key")
	}
}

// 节点范围：范围外的节点按不存在处理（404），列表与告警只含范围内的节点
func TestAPIKeyScope(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, a, _ := createNode(t, h, admin, `{"name":"hk-1","group":"亚洲"}`)
	_, b, _ := createNode(t, h, admin, `{"name":"us-1","group":"美洲"}`)
	_, c, _ := createNode(t, h, admin, `{"name":"jp-1","group":"亚洲"}`)

	check := func(name, key string, visible []int64, hidden []int64) {
		t.Helper()
		var list struct{ Items []struct{ ID int64 } }
		json.Unmarshal(do(h, "GET", "/api/v1/servers", key, nil).Body.Bytes(), &list)
		got := map[int64]bool{}
		for _, it := range list.Items {
			got[it.ID] = true
		}
		for _, id := range visible {
			if !got[id] {
				t.Errorf("%s：列表中应有节点 %d", name, id)
			}
			for _, p := range []string{"", "/metrics/history", "/traffic/current", "/traffic/daily", "/traffic/monthly"} {
				if rec := do(h, "GET", "/api/v1/servers/"+itoa(id)+p, key, nil); rec.Code != 200 {
					t.Errorf("%s：节点 %d%s 应可访问，得到 %d", name, id, p, rec.Code)
				}
			}
		}
		for _, id := range hidden {
			if got[id] {
				t.Errorf("%s：列表中不应有节点 %d", name, id)
			}
			for _, p := range []string{"", "/metrics/history", "/traffic/current", "/traffic/daily", "/traffic/monthly"} {
				if rec := do(h, "GET", "/api/v1/servers/"+itoa(id)+p, key, nil); rec.Code != http.StatusNotFound {
					t.Errorf("%s：范围外的节点 %d%s 应为 404，得到 %d", name, id, p, rec.Code)
				}
			}
		}
	}
	_, group, _, _ := createAPIKey(t, h, admin, `{"name":"亚洲","scope_type":"group","group":"亚洲"}`)
	check("分组", group, []int64{a.ServerID, c.ServerID}, []int64{b.ServerID})
	_, one, _, _ := createAPIKey(t, h, admin, `{"name":"美洲","scope_type":"servers","server_ids":[`+itoa(b.ServerID)+`]}`)
	check("指定节点", one, []int64{b.ServerID}, []int64{a.ServerID, c.ServerID})

	// 告警只含范围内节点
	now := time.Now()
	for _, id := range []int64{a.ServerID, b.ServerID} {
		s.store.DB.Exec(`INSERT INTO alert_events (rule_id, rule_key, server_id, type, severity, state, value, threshold, message, started_at, fired_at)
			VALUES (1, 'cpu', ?, 'cpu', 'warning', 'firing', 99, 90, 'cpu', ?, ?)`, id, now.Unix(), now.Unix())
	}
	var alerts struct{ Items []AlertEvent }
	json.Unmarshal(do(h, "GET", "/api/v1/alerts", one, nil).Body.Bytes(), &alerts)
	if len(alerts.Items) != 1 || alerts.Items[0].ServerID != b.ServerID {
		t.Fatalf("告警 %+v", alerts.Items)
	}

	// 校验
	for body, field := range map[string]string{
		`{"name":""}`: "name", `{"name":"x","scope_type":"group"}`: "group", `{"name":"x","scope_type":"servers","server_ids":[9999]}`: "server_ids",
		`{"name":"x","scope_type":"other"}`: "scope_type", `{"name":"x","expires_in_days":-1}`: "expires_in_days",
	} {
		if code, _, _, e := createAPIKey(t, h, admin, body); code != 422 || e.Details[0].Field != field {
			t.Errorf("%s：%d %+v，期望字段 %s", body, code, e, field)
		}
	}
}

func TestAPIKeyExpiryAndRateLimit(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, key, k, _ := createAPIKey(t, h, admin, `{"name":"x","expires_in_days":1}`)
	if k.ExpiresAt == 0 {
		t.Fatal("应有过期时间")
	}
	// 过期后失效
	s.store.DB.Exec(`UPDATE api_keys SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Second).Unix(), k.ID)
	if rec := do(h, "GET", "/api/v1/version", key, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("过期后应为 401，得到 %d", rec.Code)
	}
	// 每分钟 120 次
	_, key2, _, _ := createAPIKey(t, h, admin, `{"name":"y"}`)
	for i := 0; i < apiKeyRatePerMinute; i++ {
		if rec := do(h, "GET", "/api/v1/version", key2, nil); rec.Code != 200 {
			t.Fatalf("第 %d 次 %d", i+1, rec.Code)
		}
	}
	rec := do(h, "GET", "/api/v1/version", key2, nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("超过限额应为 429 并带 Retry-After，得到 %d", rec.Code)
	}
}
