package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAuditLogs(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	login(h, "admin", "wrong password!", false, false)
	login(h, "admin", "correct horse battery", false, false)
	_, n, _ := createNode(t, h, admin, `{"name":"hk-1"}`)
	do(h, "PUT", "/api/v1/servers/"+itoa(n.ServerID), admin, []byte(`{"name":"hk-2"}`))

	type page struct {
		Items      []AuditLog `json:"items"`
		NextCursor string     `json:"next_cursor"`
	}
	get := func(q string) (int, page) {
		rec := do(h, "GET", "/api/v1/audit-logs"+q, admin, nil)
		var p page
		json.Unmarshal(rec.Body.Bytes(), &p)
		return rec.Code, p
	}

	code, p := get("?category=login")
	if code != 200 || len(p.Items) != 2 || p.Items[0].Result != "success" || p.Items[1].Result != "failure" ||
		p.Items[1].ActorID != "admin" || p.Items[1].ClientIP != "198.51.100.20" {
		t.Fatalf("登录日志应有一次失败、一次成功，最新在前：%d %+v", code, p.Items)
	}
	if !strings.Contains(string(p.Items[1].Details), "wrong_password") && !strings.Contains(string(p.Items[1].Details), "reason") {
		t.Errorf("登录失败应记录原因：%s", p.Items[1].Details)
	}
	if strings.Contains(string(p.Items[1].Details), "wrong password!") {
		t.Fatal("【安全】审计日志不得包含密码")
	}

	_, p = get("?category=operation")
	if len(p.Items) != 2 || p.Items[0].Action != "server.update" || p.Items[1].Action != "server.create" {
		t.Fatalf("操作日志应包含新建与修改：%+v", p.Items)
	}
	if p.Items[0].ActorID != "admin" || p.Items[0].TargetName != "hk-2" {
		t.Errorf("操作日志应记录操作者用户名与节点当前名称：%+v", p.Items[0])
	}

	_, p = get("?result=failure")
	if len(p.Items) != 1 {
		t.Errorf("按结果筛选：%+v", p.Items)
	}

	// 分页：每页 3 条，共 4 条
	_, p1 := get("?limit=3")
	if len(p1.Items) != 3 || p1.NextCursor == "" {
		t.Fatalf("第一页应有 3 条并返回游标：%+v", p1)
	}
	_, p2 := get("?limit=3&cursor=" + p1.NextCursor)
	if len(p2.Items) != 1 || p2.NextCursor != "" || p2.Items[0].ID >= p1.Items[2].ID {
		t.Errorf("第二页应为剩余 1 条且没有游标：%+v", p2)
	}

	for _, q := range []string{"?category=x", "?result=ok", "?cursor=abc", "?limit=0", "?limit=201"} {
		if code, _ := get(q); code != 422 {
			t.Errorf("%s 应返回 422，实际 %d", q, code)
		}
	}

	// 保留 1 年：过期记录被清理
	s.store.DB.Exec(`UPDATE audit_logs SET ts = ? WHERE action = 'server.create'`, time.Now().Add(-400*24*time.Hour).Unix())
	if n, err := s.store.PruneAudit(time.Now()); err != nil || n != 1 {
		t.Errorf("应清理 1 条过期审计记录：%d %v", n, err)
	}
}
