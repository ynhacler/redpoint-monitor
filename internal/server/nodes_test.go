package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGetAndUpdateServer(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, a, _ := createNode(t, h, admin, `{"name":"hk-1","provider":"DMIT","traffic_limit_gb":1000}`)
	createNode(t, h, admin, `{"name":"jp-1"}`)
	path := "/api/v1/servers/" + itoa(a.ServerID)

	rec := do(h, "GET", path, admin, nil)
	var v serverView
	json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != 200 || v.Name != "hk-1" || v.Provider != "DMIT" || v.Status != "pending" {
		t.Fatalf("GET 单个节点：%d %s", rec.Code, rec.Body)
	}

	rec = do(h, "PUT", path, admin, []byte(`{"name":"HK-1 新名字","provider":"DMIT","price":9.9,"currency":"usd","billing_period":"monthly","bandwidth_mbps":1000}`))
	json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != 200 || v.Name != "HK-1 新名字" || v.PriceCents != 990 || v.Currency != "USD" || v.BandwidthMbps != 1000 {
		t.Fatalf("修改后应返回最新节点：%d %s", rec.Code, rec.Body)
	}
	if v.LimitBytes != 0 {
		t.Errorf("PUT 整体替换：未提供的月流量应清空为不限，实际 %d", v.LimitBytes)
	}
	if v.EnrollState != enrollPending {
		t.Errorf("修改信息不应改变注册状态：%q", v.EnrollState)
	}

	cases := []struct {
		name, path, body string
		status           int
	}{
		{"名称与其他节点重复", path, `{"name":"jp-1"}`, 409},
		{"字段错误", path, `{"name":"x","traffic_reset_day":40}`, 422},
		{"未知字段", path, `{"name":"x","bogus":1}`, 400},
		{"节点不存在", "/api/v1/servers/999", `{"name":"x"}`, 404},
	}
	for _, c := range cases {
		if rec := do(h, "PUT", c.path, admin, []byte(c.body)); rec.Code != c.status {
			t.Errorf("%s：%d，应为 %d；%s", c.name, rec.Code, c.status, rec.Body)
		}
	}
	if rec := do(h, "GET", "/api/v1/servers/999", admin, nil); rec.Code != 404 {
		t.Errorf("不存在的节点应返回 404：%d", rec.Code)
	}
}

func TestDeleteServer(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, v, _ := createNode(t, h, admin, `{"name":"to-delete"}`)
	_, res := enroll(h, v.EnrollCode, "x", "m")
	if rec := do(h, "POST", "/api/v1/agent/report", res.AgentToken,
		[]byte(`{"system":{"boot_id":"b"},"network":[{"interface":"eth0","rx_bytes":1,"tx_bytes":1}]}`)); rec.Code != 204 {
		t.Fatal(rec.Code)
	}
	s.flush()
	// 再上报一次但不 flush：删除时这条待写入的记录应被丢弃
	do(h, "POST", "/api/v1/agent/report", res.AgentToken, []byte(`{"system":{"boot_id":"b"}}`))

	if rec := do(h, "DELETE", "/api/v1/servers/"+itoa(v.ServerID), admin, nil); rec.Code != 204 {
		t.Fatalf("删除应返回 204：%d %s", rec.Code, rec.Body)
	}
	s.flush()
	for _, table := range []string{"servers WHERE id", "metrics_raw WHERE server_id", "traffic_counters WHERE server_id",
		"agent_tokens WHERE server_id", "enroll_codes WHERE server_id"} {
		var n int
		s.store.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` = ?`, v.ServerID).Scan(&n)
		if n != 0 {
			t.Errorf("删除后 %s 仍有 %d 行", strings.Fields(table)[0], n)
		}
	}
	if rec := do(h, "POST", "/api/v1/agent/report", res.AgentToken, []byte(`{}`)); rec.Code != 401 {
		t.Errorf("删除节点后其 Agent 上报应得到 401：%d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/v1/servers/"+itoa(v.ServerID), admin, nil); rec.Code != 404 {
		t.Errorf("重复删除应返回 404：%d", rec.Code)
	}
	var n int
	s.store.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'server.delete'`).Scan(&n)
	if n != 1 {
		t.Errorf("删除应写入审计日志（设计 24.8）：%d", n)
	}
}

func TestServerCountry(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	code, v, _ := createNode(t, h, admin, `{"name":"jp-1","country":"jp"}`)
	if code != 201 {
		t.Fatalf("新建失败：%d", code)
	}
	if n, _ := s.store.GetServer(v.ServerID); n.Country != "JP" {
		t.Errorf("国家代码应规范化为大写：%q", n.Country)
	}
	if code, _, e := createNode(t, h, admin, `{"name":"x","country":"Japan"}`); code != 422 || e.Details[0].Field != "country" {
		t.Errorf("非两位代码应返回 422：%d %+v", code, e.Details)
	}
	rec := do(h, "PUT", "/api/v1/servers/"+itoa(v.ServerID), admin, []byte(`{"name":"jp-1","country":"HK"}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"country":"HK"`) {
		t.Errorf("修改国家：%d %s", rec.Code, rec.Body)
	}
}

// 自定义顺序（修订第 150 条）：按 ids 写入 1、2、3…，不在其中的置 0；重复或不存在的 ID 422；只有管理员能改。
func TestServerOrder(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var ids []int64
	for _, n := range []string{"a", "b", "c"} {
		_, v, _ := createNode(t, h, admin, `{"name":"`+n+`"}`)
		ids = append(ids, v.ServerID)
	}
	order := func() map[string]int {
		var list struct{ Items []serverView }
		json.Unmarshal(do(h, "GET", "/api/v1/servers", admin, nil).Body.Bytes(), &list)
		m := map[string]int{}
		for _, it := range list.Items {
			m[it.Name] = it.SortOrder
		}
		return m
	}
	if got := order(); got["a"] != 0 || got["b"] != 0 {
		t.Fatalf("默认未排：%v", got)
	}
	body := func(xs ...int64) []byte { b, _ := json.Marshal(map[string]any{"ids": xs}); return b }
	if rec := do(h, "PUT", "/api/v1/servers/order", admin, body(ids[2], ids[0], ids[1])); rec.Code != 204 {
		t.Fatalf("保存：%d %s", rec.Code, rec.Body)
	}
	if got := order(); got["c"] != 1 || got["a"] != 2 || got["b"] != 3 {
		t.Fatalf("顺序：%v", got)
	}
	// 只排一部分：其余置 0
	do(h, "PUT", "/api/v1/servers/order", admin, body(ids[1]))
	if got := order(); got["b"] != 1 || got["a"] != 0 || got["c"] != 0 {
		t.Fatalf("部分排序：%v", got)
	}
	for _, bad := range [][]byte{body(ids[0], ids[0]), body(ids[0], 99999)} {
		if rec := do(h, "PUT", "/api/v1/servers/order", admin, bad); rec.Code != 422 {
			t.Errorf("应拒绝 %s：%d", bad, rec.Code)
		}
	}
	if got := order(); got["b"] != 1 {
		t.Errorf("被拒绝的请求不应改变顺序：%v", got)
	}
	if rec := do(h, "PUT", "/api/v1/servers/order", "", body(ids[0])); rec.Code != 401 {
		t.Errorf("未登录：%d", rec.Code)
	}
}
