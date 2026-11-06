package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
)

// 每条译文的占位符要和中文一致，否则运行时才会出现 %!s(MISSING)。
func TestNotifyENVerbs(t *testing.T) {
	verbs := regexp.MustCompile(`%[.0-9]*[a-z]`)
	for zh, en := range notifyEN {
		if a, b := verbs.FindAllString(zh, -1), verbs.FindAllString(en, -1); !slices.Equal(a, b) {
			t.Errorf("%q 与 %q 的占位符不同", zh, en)
		}
	}
}

// 代码里经 l.f / l.t 的中文都要有译文：漏掉时英文通知会夹中文，而且编译和运行都不报错。
func TestNotifyENComplete(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	call := regexp.MustCompile(`\b(?:l|lang\(\)|m\.Lang)\.[ft]\("([^"]*)"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range call.FindAllStringSubmatch(string(b), -1) {
			if hasHan(x[1]) && healthLang("en").t(x[1]) == x[1] {
				t.Errorf("%s：%q 没有译文", f, x[1])
			}
		}
	}
}

func hasHan(s string) bool { return strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0 }

// 英文通知里不能漏中文：覆盖全部告警类型与每种通知的模板。
func TestNotifyEnglishText(t *testing.T) {
	const l = healthLang("en")
	now := time.Unix(1_800_000_000, 0)
	start := now.Add(-90 * time.Minute)
	rules := []struct {
		typ    string
		detail string
	}{
		{AlertOffline, ""}, {AlertCPU, ""}, {AlertMemory, ""}, {AlertDisk, "/data"}, {AlertSwap, ""}, {AlertLoad, ""}, {AlertTraffic, ""},
		{AlertTrafficForecast, ""}, {AlertProcess, "nginx、redis"}, {AlertContainer, "web ×2、db"}, {AlertContainerLoop, "web 3 次、db 2 次"},
		{AlertProxy, "xray 端口 443/tcp、80/tcp 没有在监听；sing-box 没有在运行"}, {AlertPortNew, "tcp:8080、tcp:9090"}, {AlertPortGone, "tcp:22"},
		{AlertProbeLatency, "最慢：Tokyo 400 ms"}, {AlertProbeLoss, "丢包最多：Tokyo 100%"}, {AlertAgentClock, "慢"}, {AlertAgentClock, "快"},
		{AlertReach, "国内不可达（疑似被墙）"}, {AlertReach, "电信、移动不可达"}, {AlertReach, "端口 443、80 国内不可达"}, {AlertReach, "国内线路不稳定"},
	}
	var list []notifyMessage
	for _, r := range rules {
		msg := alertMessage(AlertRule{Type: r.typ, Threshold: 90}, 95, r.detail, l)
		if hasHan(msg) {
			t.Errorf("%s：%q", r.typ, msg)
		}
		list = append(list, notifyMessage{Kind: NotifyFiring, ServerName: "hk-1", Severity: SeverityCritical, Message: msg, StartedAt: start})
	}
	for _, e := range []string{"超时", "域名不存在或没有记录", "DNS 解析失败：no such host", "连接被拒绝（端口未开放或服务未运行）", "网络不可达", "重定向次数过多",
		"状态码 500（期望 200-399）", "响应中没有找到“ok”", "没有 AAAA 记录", "解析结果 1.1.1.1 不包含 2.2.2.2", "证书已过期或尚未生效",
		"证书不受信任（自签名或缺少中间证书）", "证书中的域名与 a.example 不匹配", "证书校验失败：bad"} {
		if d := l.detail(e); hasHan(d) {
			t.Errorf("%q → %q", e, d)
		}
	}
	base := notifyMessage{ServerName: "hk-1", Severity: SeverityWarning, Message: "CPU usage 95% (threshold 90%)", StartedAt: start, ResolvedAt: now}
	for _, k := range []string{NotifyFiring, NotifyResolved, NotifyRepeat, NotifyFlapping, NotifyStillFiring, NotifyTest, NotifyReminder, NotifyChange,
		NotifyPanelDown, NotifyPanelUp} {
		m := base
		m.Kind, m.Count = k, 12
		list = append(list, m)
	}
	for _, typ := range []string{panelIssueTaskPf + "alerts", panelIssueDisk, panelIssueDB, panelIssuePush} {
		list = append(list, notifyMessage{Kind: NotifyPanelIssue, Type: typ, Message: "detail"},
			notifyMessage{Kind: NotifyPanelIssueResolved, Type: typ})
	}
	names := []string{"a", "b", "c", "d"}
	list = append(list, notifyMessage{Kind: NotifyFiring, Severity: SeverityCritical, Servers: names, Count: 4},
		notifyMessage{Kind: NotifyResolved, Servers: names, Count: 4},
		notifyMessage{Kind: NotifyQuietSummary, Count: 30, StartedAt: start, ResolvedAt: now, Items: []notifyMessage{base, {Kind: NotifyResolved, ServerName: "jp", Message: "ok", StartedAt: start, ResolvedAt: now}}})
	for _, m := range list {
		m = m.withLang(l)
		if s := m.text(now); hasHan(s) {
			t.Errorf("%s：%q", m.Kind, s)
		}
	}
	for _, s := range []string{expireText("2026-12-01", -3, l), expireText("2026-12-01", 0, l), expireText("2026-12-01", 7, l),
		digestDays(-1, "2026-12-01", l), digestDays(0, "2026-12-01", l), digestDays(3, "2026-12-01", l)} {
		if hasHan(s) {
			t.Errorf("%q", s)
		}
	}
	// 默认语言的文字不变
	m := notifyMessage{Kind: NotifyRepeat, ServerName: "hk-1", Severity: SeverityWarning, Message: "m"}
	if got := m.title(); got != "🟠 仍未恢复 · hk-1 m" {
		t.Errorf("%q", got)
	}
	if got := alertMessage(AlertRule{Type: AlertReach}, 3, "电信、移动不可达", "zh"); !strings.HasPrefix(got, "电信、移动不可达（国内探针") {
		t.Errorf("%q", got)
	}
}

// 切换网站语言后，通知立即用新语言：渠道收到的文字、定期报告预览都跟随。
func TestNotifyFollowsSiteLanguage(t *testing.T) {
	s, _, _ := testServer(t)
	var got []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Title string `json:"title"`
		}
		json.NewDecoder(r.Body).Decode(&p)
		got = append(got, p.Title)
	}))
	defer hook.Close()
	c := NotifyChannel{Name: "hook", Type: ChannelWebhook, Enabled: true, MinSeverity: SeverityInfo}
	c.Config.URL = hook.URL
	if err := s.store.SaveChannel(&c, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.notify.http = hook.Client()
	send := func() {
		s.notify.dispatch(notifyMessage{Kind: NotifyTest, Severity: SeverityInfo})
		s.notify.wg.Wait()
	}
	send()
	v := s.siteSetting()
	v.Language = "en"
	s.site.v.Store(&v)
	send()
	if !slices.Equal(got, []string{"🔔 测试通知", "🔔 Test notification"}) {
		t.Errorf("%q", got)
	}
	m, err := s.buildDigest(defaultDigest(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s := m.text(time.Now()); hasHan(s) {
		t.Errorf("定期报告：%q", s)
	}
}
