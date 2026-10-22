package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 节点 IP 信息（设计 49）：开关关闭时不发送任何请求；打开后按节点公网 IP 查询，每周一次、地址变化立即查询；失败保留上次结果
func TestIPInfo(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	var calls atomic.Int32
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(429)
			return
		}
		ip := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/json")
		io.WriteString(w, `{"ip":"`+ip+`","hostname":"one.one.one.one","city":"Tokyo","region":"Tokyo","country":"JP",
			"loc":"35.6,139.7","org":"AS13335 Cloudflare, Inc.","timezone":"Asia/Tokyo"}`)
	}))
	defer srv.Close()
	s.ipinfoBase = srv.URL

	id, _, _ := s.store.CreateServer("ip-host", 0, 1)
	s.store.DB.Exec(`UPDATE servers SET ipv4 = '203.0.113.7' WHERE id = ?`, id)
	private, _, _ := s.store.CreateServer("lan-host", 0, 1)
	s.store.DB.Exec(`UPDATE servers SET ipv4 = '10.0.0.5' WHERE id = ?`, private)
	now := time.Now()

	// 默认关闭：不发送任何请求，手动查询为 409
	s.ipinfoRound(context.Background(), now)
	if calls.Load() != 0 {
		t.Fatal("开关关闭时不应请求 ipinfo.io")
	}
	if rec := do(h, "POST", "/api/v1/servers/"+itoa(id)+"/ipinfo", admin, nil); rec.Code != 409 {
		t.Fatalf("关闭时手动查询应为 409：%d", rec.Code)
	}

	// 打开：只查公网地址的节点；ASN 与名称拆开
	rec := do(h, "PUT", "/api/v1/settings/ipinfo", admin, []byte(`{"enabled":true}`))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	time.Sleep(100 * time.Millisecond) // 打开时后台立即查询一轮
	s.ipinfoRound(context.Background(), now)
	x, _ := s.store.GetIPInfo(id)
	if x == nil || x.IP != "203.0.113.7" || x.ASN != "AS13335" || x.Org != "Cloudflare, Inc." || x.Country != "JP" || x.Error != "" {
		t.Fatalf("查询结果 %+v", x)
	}
	if lan, _ := s.store.GetIPInfo(private); lan != nil {
		t.Fatal("内网地址不应查询")
	}
	n := calls.Load()

	// 一周内不重复；超过一周或地址变化再查
	s.ipinfoRound(context.Background(), now.Add(6*24*time.Hour))
	if calls.Load() != n {
		t.Fatal("一周内不应重复查询")
	}
	s.ipinfoRound(context.Background(), now.Add(8*24*time.Hour))
	if calls.Load() != n+1 {
		t.Fatal("超过一周应重新查询")
	}
	s.store.DB.Exec(`UPDATE servers SET ipv4 = '198.51.100.9' WHERE id = ?`, id)
	s.ipinfoRound(context.Background(), now.Add(8*24*time.Hour))
	if x, _ := s.store.GetIPInfo(id); x.IP != "198.51.100.9" {
		t.Fatalf("地址变化应立即查询：%+v", x)
	}

	// 失败：记录原因，保留上次成功的信息
	fail.Store(true)
	rec = do(h, "POST", "/api/v1/servers/"+itoa(id)+"/ipinfo", admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &x)
	if rec.Code != 200 || !strings.Contains(x.Error, "429") || x.ASN != "AS13335" {
		t.Fatalf("失败时 %d %+v", rec.Code, x)
	}
	// 节点详情带 ip_info；查看者也能看到
	var v struct {
		IPInfo *IPInfo `json:"ip_info"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/servers/"+itoa(id), viewerToken(t, s, "viewer-ip"), nil).Body.Bytes(), &v)
	if v.IPInfo == nil || v.IPInfo.ASN != "AS13335" {
		t.Fatalf("节点详情 %+v", v.IPInfo)
	}
	if logs := do(h, "GET", "/api/v1/audit-logs?category=operation", admin, nil).Body.String(); !strings.Contains(logs, "setting.update") {
		t.Fatal("开关应记入操作日志")
	}
}
