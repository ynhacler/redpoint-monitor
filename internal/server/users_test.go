package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 查看者账号（修订第 161 条）：新建、首次登录须改密码、只读权限、重置密码、删除。
func TestViewerAccounts(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)

	// 需要重新验证密码
	s.store.DB.Exec(`UPDATE sessions SET reauth_until = 0`)
	if rec := do(h, "POST", "/api/v1/users", admin, []byte(`{"username":"alice"}`)); rec.Code != 403 {
		t.Fatalf("未重新验证时应为 403，得到 %d", rec.Code)
	}
	admin = adminToken(t, s)
	for _, bad := range []string{`{"username":"ab"}`, `{"username":"has space"}`, `{"username":"admin"}`, `{"username":"ADMIN"}`} {
		if rec := do(h, "POST", "/api/v1/users", admin, []byte(bad)); rec.Code != 422 {
			t.Errorf("%s 应为 422，得到 %d", bad, rec.Code)
		}
	}
	rec := do(h, "POST", "/api/v1/users", admin, []byte(`{"username":"alice"}`))
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	var created struct {
		User     userView `json:"user"`
		Password string   `json:"password"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.User.Role != roleViewer || !created.User.MustChangePassword || len(created.Password) < 12 {
		t.Fatalf("新建结果不对：%+v", created)
	}
	if strings.Contains(do(h, "GET", "/api/v1/audit-logs", admin, nil).Body.String(), created.Password) {
		t.Fatal("操作日志中出现了初始密码")
	}

	// 用初始密码登录：登录信息显示查看者，须先改密码
	lr := login(h, "alice", created.Password, false, false)
	if lr.Code != 200 {
		t.Fatal(lr.Code, lr.Body)
	}
	var me meView
	json.Unmarshal(lr.Body.Bytes(), &me)
	if me.Role != roleViewer || !me.MustChangePassword {
		t.Fatalf("登录信息不对：%+v", me)
	}
	tok := sessionFrom(t, lr).Value
	if rec := do(h, "GET", "/api/v1/servers", tok, nil); rec.Code != 403 {
		t.Fatalf("改密码前应只能访问账号接口，得到 %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/v1/auth/password", tok,
		[]byte(`{"current_password":"`+created.Password+`","new_password":"alice new password"}`)); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}

	// 只读：可读节点与告警，不能修改、不能静音、不能读管理设置
	for _, p := range []string{"/api/v1/servers", "/api/v1/alerts", "/api/v1/latency-overview", "/api/v1/auth/sessions"} {
		if rec := do(h, "GET", p, tok, nil); rec.Code != 200 {
			t.Errorf("查看者 GET %s 应为 200，得到 %d", p, rec.Code)
		}
	}
	for _, c := range []struct{ m, p, b string }{
		{"POST", "/api/v1/servers", `{"name":"x"}`},
		{"GET", "/api/v1/notification-channels", ``},
		{"GET", "/api/v1/audit-logs", ``},
		{"GET", "/api/v1/users", ``},
		{"POST", "/api/v1/users", `{"username":"mallory"}`},
		{"POST", "/api/v1/silences", `{"scope_type":"global","duration_s":3600}`},
		{"GET", "/api/v1/cloud-accounts", ``},
	} {
		rec := do(h, c.m, c.p, tok, []byte(c.b))
		if rec.Code != 403 {
			t.Errorf("查看者 %s %s 应为 403，得到 %d", c.m, c.p, rec.Code)
		}
	}

	// 账号列表：管理员在前
	var list struct {
		Items []userView `json:"items"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/users", admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 2 || list.Items[0].Role != roleAdmin || list.Items[1].Username != "alice" || list.Items[1].MustChangePassword {
		t.Fatalf("账号列表不对：%+v", list.Items)
	}
	adminID := list.Items[0].ID

	// 管理员不能通过这些接口被重置或删除
	for _, c := range []struct{ m, p string }{{"POST", "/api/v1/users/%d/password"}, {"DELETE", "/api/v1/users/%d"}} {
		if rec := do(h, c.m, strings.Replace(c.p, "%d", itoa(adminID), 1), admin, nil); rec.Code != 404 {
			t.Errorf("%s 管理员应为 404，得到 %d", c.m, rec.Code)
		}
	}

	// 重置密码：旧会话退出，新密码首次登录须修改
	rec = do(h, "POST", "/api/v1/users/"+itoa(created.User.ID)+"/password", admin, nil)
	var reset struct {
		Password string `json:"password"`
	}
	json.Unmarshal(rec.Body.Bytes(), &reset)
	if rec.Code != 200 || reset.Password == "" || reset.Password == created.Password {
		t.Fatal(rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/servers", tok, nil); rec.Code != 401 {
		t.Fatalf("重置后旧会话应失效，得到 %d", rec.Code)
	}
	if lr := login(h, "alice", reset.Password, false, false); lr.Code != 200 || !strings.Contains(lr.Body.String(), `"must_change_password":true`) {
		t.Fatalf("新密码应能登录且须修改：%d %s", lr.Code, lr.Body)
	}

	// 删除：会话失效，账号消失
	tok2 := viewerToken(t, s, "alice")
	if rec := do(h, "DELETE", "/api/v1/users/"+itoa(created.User.ID), admin, nil); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/auth/me", tok2, nil); rec.Code != 401 {
		t.Fatalf("删除后会话应失效，得到 %d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/v1/users/"+itoa(created.User.ID), admin, nil); rec.Code != 404 {
		t.Fatalf("重复删除应为 404，得到 %d", rec.Code)
	}
	logs := do(h, "GET", "/api/v1/audit-logs", admin, nil).Body.String()
	for _, a := range []string{"user.create", "user.password_reset", "user.delete"} {
		if !strings.Contains(logs, a) {
			t.Errorf("操作日志缺少 %s", a)
		}
	}
	// 查看者自己的操作记为 viewer
	if !strings.Contains(logs, `"actor_type":"viewer"`) {
		t.Error("查看者的操作应记为 viewer")
	}
}

// 查看者数量上限。
func TestViewerLimit(t *testing.T) {
	s, _, _ := testServer(t)
	h, _ := HashPassword("x password 123")
	for i := range maxViewers {
		if _, err := s.store.CreateViewer("v"+itoa(int64(i))+"xx", h, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.store.CreateViewer("one-more", h, time.Now()); err != errTooManyViewers {
		t.Fatalf("超过上限应返回 errTooManyViewers，得到 %v", err)
	}
}
