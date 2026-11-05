package server

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

func TestMaskPublicName(t *testing.T) {
	for in, want := range map[string]string{
		"东京 1":                  "东京 1",
		"HK-CN2-02":             "HK-CN2-02",
		"jp 203.0.113.7":        "jp ***",
		"v6 2001:db8::1 sg":     "v6 *** sg",
		"us 13800138000":        "us ***",
		"tel +86 138-0013-8000": "tel ***",
		"me@example.com node":   "*** node",
	} {
		if got := maskPublicName(in); got != want {
			t.Errorf("%q → %q，应为 %q", in, got, want)
		}
	}
}

// 公开展示页（设计 45.3）：默认关闭；只公开选中的节点与白名单字段；取消后立即生效
func TestStatusPage(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	now := time.Now()
	pub, _, _ := s.store.CreateServer("hk 203.0.113.7", 1000, 1)
	off, _, _ := s.store.CreateServer("down", 0, 1)
	hidden, _, _ := s.store.CreateServer("secret-node", 0, 1)
	if _, err := s.store.DB.Exec(`UPDATE servers SET provider = 'SecretHost', note = 'secret-note', country = 'HK', price_cents = 4321 WHERE id = ?`, pub); err != nil {
		t.Fatal(err)
	}
	s.latest[pub] = &snapshot{ReceivedAt: now, At: now, Report: protocol.Report{
		AgentVersion: "9.9.9-secret",
		System:       protocol.System{Hostname: "secret-host", Uptime: 3600},
		CPU:          protocol.CPU{Usage: 12.34},
		Memory:       protocol.Memory{Usage: 50},
		Disk: []protocol.Disk{
			{Mount: "/", Device: "/dev/vda1", Total: 100, Used: 40, Available: 60},
			{Mount: "/srv", Device: "/dev/vda1", Total: 100, Used: 40, Available: 60},
		},
		Network: []protocol.NetIface{{Interface: "secret-eth", RxSpeed: 10, TxSpeed: 20}, {Interface: "eth1", RxSpeed: 1, TxSpeed: 2}},
	}}
	old := now.Add(-24 * time.Hour)
	s.latest[off] = &snapshot{ReceivedAt: old, At: old, Report: protocol.Report{CPU: protocol.CPU{Usage: 99}}}
	s.latest[hidden] = &snapshot{ReceivedAt: now, At: now}

	get := func() (int, string) {
		rec := do(h, "GET", "/api/v1/public/servers", "", nil)
		return rec.Code, rec.Body.String()
	}
	put := func(enabled bool, ids ...int64) {
		t.Helper()
		b, _ := json.Marshal(statusPageView{Enabled: enabled, ServerIDs: ids})
		if rec := do(h, "PUT", "/api/v1/settings/status-page", admin, b); rec.Code != 200 {
			t.Fatalf("保存：%d %s", rec.Code, rec.Body)
		}
	}

	if code, body := get(); code != 404 || strings.Contains(body, "hk") {
		t.Fatalf("默认应关闭：%d %s", code, body)
	}
	// 开启但没有选节点：什么都不公开
	put(true)
	if code, body := get(); code != 200 || !strings.Contains(body, `"items":[]`) {
		t.Fatalf("没有选节点：%d %s", code, body)
	}
	if rec := do(h, "PUT", "/api/v1/settings/status-page", admin, []byte(`{"enabled":true,"server_ids":[9999]}`)); rec.Code != 422 {
		t.Fatalf("不存在的节点应被拒绝：%d", rec.Code)
	}

	put(true, pub, off)
	code, body := get()
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, secret := range []string{"203.0.113", "secret", "SecretHost", "4321", "9.9.9", `"id"`} {
		if strings.Contains(body, secret) {
			t.Errorf("不应出现 %q：%s", secret, body)
		}
	}
	var out struct {
		Items     []map[string]any `json:"items"`
		UpdatedAt int64            `json:"updated_at"`
	}
	json.Unmarshal([]byte(body), &out)
	if len(out.Items) != 2 || out.UpdatedAt == 0 {
		t.Fatalf("应只有选中的两个节点：%s", body)
	}
	allowed := []string{"name", "country", "status", "cpu", "memory", "disk", "rx_speed", "tx_speed", "uptime", "traffic_pct"}
	for _, it := range out.Items {
		for k := range it {
			if !slices.Contains(allowed, k) {
				t.Errorf("字段 %q 不在白名单中", k)
			}
		}
	}
	var live, down map[string]any
	for _, it := range out.Items {
		if it["status"] == "online" {
			live = it
		} else {
			down = it
		}
	}
	if live == nil || down == nil {
		t.Fatalf("状态：%s", body)
	}
	if live["name"] != "hk ***" || live["country"] != "HK" || live["cpu"] != 12.3 || live["disk"] != 40.0 ||
		live["rx_speed"] != 11.0 || live["tx_speed"] != 22.0 || live["uptime"] != 3600.0 || live["traffic_pct"] != 0.0 {
		t.Errorf("在线节点：%v", live)
	}
	if _, ok := down["cpu"]; ok || down["status"] != "offline" {
		t.Errorf("离线节点不应带过时的指标：%v", down)
	}

	// 设置页读回；取消公开不等缓存过期
	var v statusPageView
	json.Unmarshal(do(h, "GET", "/api/v1/settings/status-page", admin, nil).Body.Bytes(), &v)
	if !v.Enabled || !slices.Equal(v.ServerIDs, []int64{pub, off}) {
		t.Fatalf("读回：%+v", v)
	}
	put(true, off)
	if _, body := get(); strings.Contains(body, "hk") {
		t.Fatalf("取消公开应立即生效：%s", body)
	}
	put(false, off)
	if code, _ := get(); code != 404 {
		t.Fatalf("关闭应立即生效：%d", code)
	}
}

// 【安全】节点 ID 删除后会复用：新节点不能继承已删除节点的公开状态
func TestStatusPageDeletedIDNotInherited(t *testing.T) {
	s, _, _ := testServer(t)
	id, _, _ := s.store.CreateServer("old", 0, 1)
	if err := s.store.SetPublicServers([]int64{id}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DeleteServer(id); err != nil {
		t.Fatal(err)
	}
	again, _, _ := s.store.CreateServer("new", 0, 1)
	if ids, _ := s.store.PublicServerIDs(); len(ids) != 0 {
		t.Fatalf("新节点（ID %d，旧 ID %d）不应公开：%v", again, id, ids)
	}
}

func TestStatusPageRateLimit(t *testing.T) {
	_, h, _ := testServer(t)
	for i := range statusPagePerMin {
		if rec := do(h, "GET", "/api/v1/public/servers", "", nil); rec.Code != 404 {
			t.Fatalf("第 %d 次：%d", i, rec.Code)
		}
	}
	rec := do(h, "GET", "/api/v1/public/servers", "", nil)
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("超过限额：%d", rec.Code)
	}
}
