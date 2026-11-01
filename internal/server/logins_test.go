package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// 登录记录（修订第 276 条）：第一次只作基准；之后来自新地址的登录记一条警告级变更并通知，已知地址与重复上报不算；
// 登录不进入实时状态；“登录”类别关闭时只记录。
func TestLogins(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	hook := newReceiver(t)
	if rec := do(h, "POST", "/api/v1/notification-channels", admin,
		[]byte(`{"type":"webhook","name":"all","min_severity":"info","config":{"url":"`+hook.srv.URL+`/all"}}`)); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	id, tok, _ := s.store.CreateServer("login-host", 0, 1)
	now := time.Now().Unix()
	post := func(logins string) {
		t.Helper()
		body := fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"logins":[%s]}`, now, logins)
		if rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(body)); rec.Code != 204 {
			t.Fatal(rec.Code, rec.Body)
		}
		s.notify.wg.Wait()
	}
	login := func(at int64, user, host string) string {
		return fmt.Sprintf(`{"time":%d,"user":%q,"host":%q,"line":"pts/0"}`, at, user, host)
	}
	changes := func() []SystemEvent {
		var out struct{ Items []SystemEvent }
		json.Unmarshal(do(h, "GET", "/api/v1/system-events?kind=change&server_id="+itoa64(id), admin, nil).Body.Bytes(), &out)
		return out.Items
	}

	post(login(now-3600, "bob", "198.51.100.20") + "," + login(now-1800, "root", "203.0.113.7"))
	if len(changes()) != 0 || hook.count() != 0 {
		t.Fatal("第一次收到的登录只作基准")
	}
	post(login(now-1800, "root", "203.0.113.7") + "," + login(now-60, "bob", "198.51.100.20") + "," + login(now-30, "alice", ""))
	if len(changes()) != 0 {
		t.Fatalf("重复与已知地址、本地控制台都不算：%+v", changes())
	}
	post(login(now-10, "root", "192.0.2.99"))
	got := changes()
	if len(got) != 1 || got[0].Severity != SeverityWarning || got[0].Title != "login-host 有 1 次来自新地址的登录" ||
		!strings.HasPrefix(got[0].Message, "root 从 192.0.2.99 登录（pts/0，") {
		t.Fatalf("新地址应记一条警告：%+v", got)
	}
	if hook.count() != 1 {
		t.Fatalf("应通知一次：%d", hook.count())
	}
	if _, body, _ := hook.last(); strings.Contains(body, "192.0.2.99") || strings.Contains(body, "root") {
		t.Errorf("通知不应含地址与账号：%s", body)
	}

	var list struct {
		Items      []LoginRecord
		NextCursor string `json:"next_cursor"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/servers/"+itoa64(id)+"/logins?limit=2", admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 2 || list.Items[0].Host != "192.0.2.99" || !list.Items[0].NewHost || list.Items[1].User != "alice" || list.NextCursor == "" {
		t.Fatalf("第一页：%+v", list)
	}
	json.Unmarshal(do(h, "GET", "/api/v1/servers/"+itoa64(id)+"/logins?limit=10&cursor="+list.NextCursor, admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 3 || list.NextCursor != "" || list.Items[0].NewHost {
		t.Fatalf("第二页：%+v", list)
	}
	if body := do(h, "GET", "/api/v1/servers/"+itoa64(id), admin, nil).Body.String(); strings.Contains(body, "logins") || strings.Contains(body, "192.0.2.99") {
		t.Errorf("节点详情不应含登录：%s", body)
	}
	if rec := do(h, "GET", "/api/v1/servers/"+itoa64(id)+"/logins?cursor=x", admin, nil); rec.Code != 422 {
		t.Errorf("cursor 无效应返回 422：%d", rec.Code)
	}

	// 只发 firewall 等其他字段的旧版页面保存时不会关掉“登录”；显式关闭后只记录不通知
	do(h, "PUT", "/api/v1/settings/change-notify", admin, []byte(`{"files":false}`))
	if !s.changes.notifyCfg().Logins {
		t.Fatal("省略的字段应保持原值")
	}
	do(h, "PUT", "/api/v1/settings/change-notify", admin, []byte(`{"logins":false}`))
	post(login(now-5, "root", "192.0.2.100"))
	if len(changes()) != 2 || hook.count() != 1 {
		t.Errorf("关闭后仍记录、不通知：%d 条事件，%d 次通知", len(changes()), hook.count())
	}
}
