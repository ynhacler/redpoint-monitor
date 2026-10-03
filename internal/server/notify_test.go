package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

const testBotToken = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsawQ"

// fakeReceiver 记录收到的请求（模拟 Telegram Bot API 与 Webhook 接收端）。
type fakeReceiver struct {
	mu     sync.Mutex
	srv    *httptest.Server
	paths  []string
	bodies []string
	sigs   []string
	status int
}

func newReceiver(t *testing.T) *fakeReceiver {
	f := &fakeReceiver{status: 200}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.bodies = append(f.bodies, string(b))
		f.sigs = append(f.sigs, r.Header.Get("X-Vpsmon-Signature"))
		st := f.status
		f.mu.Unlock()
		w.WriteHeader(st)
		if st != 200 {
			w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeReceiver) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.bodies) }
func (f *fakeReceiver) last() (string, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.bodies) - 1
	return f.paths[n], f.bodies[n], f.sigs[n]
}

// 渠道增删改：校验、凭证脱敏、修改时凭证留空保持原值（设计 16.5、24.7）。
func TestNotificationChannelsAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)

	bad := []string{
		`{"type":"telegram","name":"tg","config":{"bot_token":"nope","chat_id":"1"}}`,
		`{"type":"telegram","name":"tg","config":{"bot_token":"` + testBotToken + `","chat_id":"x y"}}`,
		`{"type":"webhook","name":"wh","config":{"url":"http://example.com/hook"}}`,
		`{"type":"webhook","name":"wh","config":{"url":"https://user:pw@example.com/hook"}}`,
		`{"type":"email","name":"x","config":{}}`,
		`{"type":"webhook","name":"","config":{"url":"https://example.com"}}`,
	}
	for _, b := range bad {
		if rec := do(h, "POST", "/api/v1/notification-channels", admin, []byte(b)); rec.Code != 422 {
			t.Errorf("应拒绝 %s：%d %s", b, rec.Code, rec.Body)
		}
	}

	rec := do(h, "POST", "/api/v1/notification-channels", admin,
		[]byte(`{"type":"telegram","name":"我的 TG","min_severity":"critical","config":{"bot_token":"`+testBotToken+`","chat_id":"-100123"}}`))
	if rec.Code != 201 {
		t.Fatalf("创建 Telegram：%d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "fSeofSAs0K5PALDs") {
		t.Fatal("【安全】响应中不得出现完整 Bot Token")
	}
	var tg channelView
	json.Unmarshal(rec.Body.Bytes(), &tg)
	if tg.Config["bot_token"] != "123456789:AA…sawQ" || tg.MinSeverity != "critical" || !tg.Enabled || !tg.NotifyResolved {
		t.Errorf("创建结果：%+v", tg)
	}

	rec = do(h, "POST", "/api/v1/notification-channels", admin,
		[]byte(`{"type":"webhook","name":"hook","config":{"url":"https://hooks.example.com/T000/B000/secretpath","secret":"s3cret"}}`))
	var wh channelView
	json.Unmarshal(rec.Body.Bytes(), &wh)
	if rec.Code != 201 || wh.Config["url"] != "https://hooks.example.com/…" || wh.Config["has_secret"] != true ||
		strings.Contains(rec.Body.String(), "secretpath") || strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("【安全】Webhook 地址路径与密钥应脱敏：%d %s", rec.Code, rec.Body)
	}

	// 修改：凭证留空保持原值；可清除签名密钥；类型不可修改
	if rec := do(h, "PUT", "/api/v1/notification-channels/"+itoa(tg.ID), admin,
		[]byte(`{"name":"TG 改名","enabled":false,"config":{}}`)); rec.Code != 200 {
		t.Fatalf("修改：%d %s", rec.Code, rec.Body)
	}
	got, _ := s.store.GetChannel(tg.ID)
	if got.Name != "TG 改名" || got.Enabled || got.Config.BotToken != testBotToken || got.Config.ChatID != "-100123" {
		t.Errorf("修改后：%+v", got)
	}
	do(h, "PUT", "/api/v1/notification-channels/"+itoa(wh.ID), admin, []byte(`{"name":"hook","config":{"clear_secret":true}}`))
	if got, _ := s.store.GetChannel(wh.ID); got.Config.Secret != "" || got.Config.URL == "" {
		t.Errorf("清除密钥：%+v", got.Config)
	}
	if rec := do(h, "PUT", "/api/v1/notification-channels/"+itoa(wh.ID), admin, []byte(`{"type":"telegram","name":"x","config":{}}`)); rec.Code != 422 {
		t.Errorf("类型不可修改：%d", rec.Code)
	}

	// 列表、删除、审计
	var list struct{ Items []channelView }
	json.Unmarshal(do(h, "GET", "/api/v1/notification-channels", admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 2 {
		t.Fatalf("列表：%+v", list.Items)
	}
	if rec := do(h, "DELETE", "/api/v1/notification-channels/"+itoa(wh.ID), admin, nil); rec.Code != 204 {
		t.Errorf("删除：%d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/v1/notification-channels/"+itoa(wh.ID), admin, nil); rec.Code != 404 {
		t.Errorf("重复删除应 404：%d", rec.Code)
	}
	logs, _, _ := s.store.ListAudit(AuditQuery{Action: "notification_channel.", Limit: 10})
	if len(logs) != 5 || strings.Contains(string(logs[0].Details), "s3cret") {
		t.Errorf("渠道修改应记入审计（不含凭证）：%d", len(logs))
	}
}

// 测试通知：Telegram 请求格式、Webhook 签名、失败原因（不含 Token）与投递记录。
func TestNotificationTestSend(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	tgAPI := newReceiver(t)
	hook := newReceiver(t)
	s.notify.tgAPI = tgAPI.srv.URL

	create := func(body string) int64 {
		rec := do(h, "POST", "/api/v1/notification-channels", admin, []byte(body))
		var v channelView
		json.Unmarshal(rec.Body.Bytes(), &v)
		if rec.Code != 201 {
			t.Fatalf("创建：%d %s", rec.Code, rec.Body)
		}
		return v.ID
	}
	tg := create(`{"type":"telegram","name":"tg","config":{"bot_token":"` + testBotToken + `","chat_id":"42"}}`)
	wh := create(`{"type":"webhook","name":"wh","config":{"url":"` + hook.srv.URL + `/in","secret":"k"}}`)

	test := func(id int64) map[string]any {
		var r map[string]any
		json.Unmarshal(do(h, "POST", "/api/v1/notification-channels/"+itoa(id)+"/test", admin, nil).Body.Bytes(), &r)
		return r
	}
	if r := test(tg); r["ok"] != true {
		t.Fatalf("Telegram 测试：%v", r)
	}
	path, body, _ := tgAPI.last()
	var msg map[string]any
	json.Unmarshal([]byte(body), &msg)
	if path != "/bot"+testBotToken+"/sendMessage" || msg["chat_id"] != "42" || !strings.Contains(msg["text"].(string), "测试通知") {
		t.Errorf("Telegram 请求：%s %s", path, body)
	}

	if r := test(wh); r["ok"] != true {
		t.Fatalf("Webhook 测试：%v", r)
	}
	_, body, sig := hook.last()
	mac := hmac.New(sha256.New, []byte("k"))
	mac.Write([]byte(body))
	if sig != "sha256="+hex.EncodeToString(mac.Sum(nil)) || !strings.Contains(body, `"kind":"test"`) || !strings.Contains(body, `"version":1`) {
		t.Errorf("Webhook 签名或内容不正确：%s %s", sig, body)
	}

	// 失败：返回原因（Telegram 的 description），且不含 Token
	tgAPI.status = 400
	r := test(tg)
	if r["ok"] != false || !strings.Contains(r["error"].(string), "chat not found") || strings.Contains(r["error"].(string), "AAHdq") {
		t.Errorf("失败原因：%v", r)
	}
	// 连接失败：错误中不含请求地址（Telegram 地址含 Token）
	s.notify.tgAPI = "http://127.0.0.1:1"
	if r := test(tg); r["ok"] != false || strings.Contains(r["error"].(string), "AAHdq") || strings.Contains(r["error"].(string), "/bot") {
		t.Errorf("【安全】连接失败的错误不得包含地址与 Token：%v", r)
	}

	var ds struct{ Items []Delivery }
	json.Unmarshal(do(h, "GET", "/api/v1/notification-deliveries", admin, nil).Body.Bytes(), &ds)
	if len(ds.Items) != 4 || ds.Items[0].Status != DeliveryFailed || ds.Items[2].Status != DeliverySent || ds.Items[0].Kind != NotifyTest {
		t.Errorf("投递记录：%+v", ds.Items)
	}
	for _, d := range ds.Items {
		if strings.Contains(d.LastError, "AAHdq") {
			t.Error("【安全】投递记录不得包含 Token")
		}
	}
}

// 告警通知：按级别筛选、静音不发送、恢复通知、重复提醒、失败重试（设计 16.4、16.5）。
func TestAlertNotifications(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	hook := newReceiver(t)
	s.notify.backoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	s.publicURL = "https://monitor.example.com"
	_, v, _ := createNode(t, h, admin, `{"name":"hk-1"}`)
	enroll(h, v.EnrollCode, "hk-1", "m-1")
	id := v.ServerID
	// 警告级以上的 Webhook；只收严重的第二个渠道
	do(h, "POST", "/api/v1/notification-channels", admin, []byte(`{"type":"webhook","name":"all","min_severity":"warning","config":{"url":"`+hook.srv.URL+`/all"}}`))
	do(h, "POST", "/api/v1/notification-channels", admin, []byte(`{"type":"webhook","name":"crit","min_severity":"critical","config":{"url":"`+hook.srv.URL+`/crit"}}`))
	s.store.DB.Exec(`UPDATE alert_rules SET repeat_interval_s = 600 WHERE rule_key = 'cpu'`)

	t0 := time.Now().Add(time.Hour)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	report := func(sec int, cpu float64) {
		s.mu.Lock()
		s.latest[id] = &snapshot{ReceivedAt: at(sec), At: at(sec), Report: protocol.Report{
			CPU: protocol.CPU{Usage: cpu, Cores: 2}, Memory: protocol.Memory{Usage: 30}, Disk: []protocol.Disk{{Mount: "/", Usage: 40}}}}
		s.mu.Unlock()
	}
	eval := func(sec int) {
		t.Helper()
		if err := s.evaluateAlerts(at(sec)); err != nil {
			t.Fatal(err)
		}
		s.notify.wg.Wait()
	}

	report(0, 95)
	eval(0)
	report(300, 95)
	eval(300) // CPU（警告）触发
	if hook.count() != 1 {
		t.Fatalf("警告级告警只应发到 min_severity=warning 的渠道：%d", hook.count())
	}
	path, body, _ := hook.last()
	if path != "/all" || !strings.Contains(body, `"kind":"firing"`) || !strings.Contains(body, "hk-1") ||
		!strings.Contains(body, "CPU 使用率 95%") || !strings.Contains(body, `"url":"https://monitor.example.com/servers/`) {
		t.Errorf("firing 通知内容：%s %s", path, body)
	}

	// 未到重复间隔不提醒；到达后提醒一次
	report(600, 95)
	eval(600)
	if hook.count() != 1 {
		t.Errorf("未到重复间隔不应提醒：%d", hook.count())
	}
	report(901, 95)
	eval(901)
	if _, body, _ := hook.last(); hook.count() != 2 || !strings.Contains(body, `"kind":"repeat"`) || !strings.Contains(body, "仍未恢复") {
		t.Errorf("到达间隔应重复提醒：%d %s", hook.count(), body)
	}

	// 静音期间不发送（重复提醒与恢复都不发），计时照常推进
	do(h, "POST", "/api/v1/silences", admin, []byte(`{"kind":"mute","scope_type":"server","scope_id":"`+itoa(id)+`"}`))
	report(1600, 95)
	eval(1600)
	if hook.count() != 2 {
		t.Errorf("静音期间不应发送：%d", hook.count())
	}
	var sil struct{ Items []Silence }
	json.Unmarshal(do(h, "GET", "/api/v1/silences", admin, nil).Body.Bytes(), &sil)
	do(h, "DELETE", "/api/v1/silences/"+itoa(sil.Items[0].ID), admin, nil)

	// 恢复：发送恢复通知（失败重试后成功）
	hook.status = 500
	report(1700, 40)
	eval(1700)
	go func() { time.Sleep(time.Millisecond); hook.mu.Lock(); hook.status = 200; hook.mu.Unlock() }()
	report(1830, 40)
	eval(1830)
	_, body, _ = hook.last()
	if !strings.Contains(body, `"kind":"resolved"`) || !strings.Contains(body, "已恢复") {
		t.Errorf("恢复通知：%s", body)
	}
	ds, _ := s.store.ListDeliveries(0, 10)
	if len(ds) != 3 || ds[0].Kind != NotifyResolved || ds[0].Status != DeliverySent || ds[0].EventID == 0 {
		t.Errorf("投递记录：%+v", ds)
	}
}
