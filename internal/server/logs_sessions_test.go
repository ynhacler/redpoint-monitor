package server

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 审计日志按主体、操作、时间筛选（设计 24.8）。
func TestAuditFilters(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	createNode(t, h, admin, `{"name":"f-1"}`)
	createNode(t, h, admin, `{"name":"f-2"}`)
	s.store.Audit(AuditEntry{ActorType: "cli", Action: "admin.reset_password", Success: true}, time.Now())
	get := func(q string) []AuditLog {
		var out struct{ Items []AuditLog }
		rec := do(h, "GET", "/api/v1/audit-logs?"+q, admin, nil)
		if rec.Code != 200 {
			t.Fatalf("%s：%d %s", q, rec.Code, rec.Body)
		}
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Items
	}
	if n := len(get("action=server.")); n < 2 {
		t.Errorf("按操作前缀筛选：%d", n)
	}
	for _, l := range get("actor=cli") {
		if l.ActorType != "cli" {
			t.Errorf("按主体筛选：%+v", l)
		}
	}
	if len(get("actor=cli")) != 1 {
		t.Error("应只有一条 cli 记录")
	}
	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	if n := len(get("from=" + future)); n != 0 {
		t.Errorf("时间范围之后不应有记录：%d", n)
	}
	for _, bad := range []string{"actor=root", "from=abc", "action=" + strings.Repeat("x", 65)} {
		if rec := do(h, "GET", "/api/v1/audit-logs?"+bad, admin, nil); rec.Code != 422 {
			t.Errorf("%s 应返回 422：%d", bad, rec.Code)
		}
	}
}

// CSV 导出：BOM、表头、筛选生效；【安全】公式起始字符被转义（CSV 注入）。
func TestAuditExport(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	createNode(t, h, admin, `{"name":"=HYPERLINK(\"http://evil\",\"x\")"}`)
	rec := do(h, "GET", "/api/v1/audit-logs/export?action=server.create", admin, nil)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("应下载 CSV：%d %v", rec.Code, rec.Header())
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "\xEF\xBB\xBF时间,结果,主体") {
		t.Errorf("应以 BOM 与表头开始：%q", body[:min(40, len(body))])
	}
	if !strings.Contains(body, `"'=HYPERLINK(`) || strings.Contains(body, `,"=HYPERLINK(`) {
		t.Errorf("【安全】以 = 开头的单元格应加单引号：\n%s", body)
	}
	if strings.Count(body, "\n") != 2 {
		t.Errorf("筛选后应只有表头与一条记录：\n%s", body)
	}
	if got := csvSafe("-1+1"); got != "'-1+1" || csvSafe("hk-1") != "hk-1" {
		t.Error("csvSafe")
	}
}

// 当前登录会话：列出、标出当前会话、踢出后该会话失效；不能踢出不存在的会话（设计 24.8、17.4）。
func TestSessions(t *testing.T) {
	s, h, _ := testServer(t)
	me := adminToken(t, s)
	other := adminToken(t, s) // 另一个浏览器的登录
	var list struct{ Items []SessionView }
	json.Unmarshal(do(h, "GET", "/api/v1/auth/sessions", me, nil).Body.Bytes(), &list)
	if len(list.Items) != 2 {
		t.Fatalf("应有 2 个会话：%+v", list.Items)
	}
	var otherID int64
	current := 0
	for _, v := range list.Items {
		if v.Current {
			current++
		} else {
			otherID = v.ID
		}
	}
	if current != 1 || otherID == 0 {
		t.Fatalf("应标出且只标出当前会话：%+v", list.Items)
	}
	if rec := do(h, "DELETE", "/api/v1/auth/sessions/"+itoa(otherID), me, nil); rec.Code != 204 {
		t.Fatalf("踢出失败：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/v1/auth/me", other, nil); rec.Code != 401 {
		t.Errorf("被踢出的会话应失效：%d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/v1/auth/sessions/999999", me, nil); rec.Code != 404 {
		t.Errorf("不存在的会话应 404：%d", rec.Code)
	}
	var logs struct{ Items []AuditLog }
	json.Unmarshal(do(h, "GET", "/api/v1/audit-logs?category=login&action=auth.session_revoke", me, nil).Body.Bytes(), &logs)
	if len(logs.Items) != 1 {
		t.Errorf("踢出应记入登录日志：%d", len(logs.Items))
	}
	// 踢出当前会话等同退出登录：清除 Cookie
	json.Unmarshal(do(h, "GET", "/api/v1/auth/sessions", me, nil).Body.Bytes(), &list)
	rec := do(h, "DELETE", "/api/v1/auth/sessions/"+itoa(list.Items[0].ID), me, nil)
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if rec.Code != 204 || !cleared {
		t.Errorf("踢出当前会话应清除 Cookie：%d %v", rec.Code, rec.Result().Cookies())
	}
}
