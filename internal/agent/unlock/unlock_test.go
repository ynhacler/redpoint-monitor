package unlock

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

// fakeResp 是按地址返回的假响应；redirect 不为空时返回 302。
type fakeResp struct {
	status    int
	body      string
	redirect  string
	challenge bool
}

type fakeTransport map[string]fakeResp

func (f fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r, ok := f[req.URL.String()]
	if !ok {
		r = fakeResp{status: 404}
	}
	h := http.Header{}
	if r.challenge {
		h.Set("cf-mitigated", "challenge")
	}
	if r.redirect != "" {
		h.Set("Location", r.redirect)
		r.status = http.StatusFound
	}
	return &http.Response{StatusCode: r.status, Header: h, Body: io.NopCloser(strings.NewReader(r.body)), Request: req}, nil
}

func runWith(t *testing.T, ft fakeTransport) map[string]protocol.UnlockItem {
	t.Helper()
	out := map[string]protocol.UnlockItem{}
	for _, it := range Run(context.Background(), &http.Client{Transport: ft}) {
		out[it.Service] = it
	}
	return out
}

// 能用的节点：各项按页面判断为可用并取出地区
func TestUnlockAvailable(t *testing.T) {
	got := runWith(t, fakeTransport{
		"https://www.netflix.com/title/70143836":    {redirect: "https://www.netflix.com/jp/title/70143836"},
		"https://www.netflix.com/jp/title/70143836": {status: 200},
		"https://www.netflix.com/title/80057281":    {status: 200},
		"https://www.disneyplus.com/":               {status: 200},
		"https://www.youtube.com/premium":           {status: 200, body: `..."countryCode":"JP"...`},
		"https://www.primevideo.com/":               {status: 200, body: `{"currentTerritory":"JP"}`},
		"https://www.tiktok.com/":                   {status: 200, body: `{"region":"JP"}`},
		"https://chatgpt.com/cdn-cgi/trace":         {status: 200, body: "fl=1\nloc=JP\ntls=TLSv1.3\n"},
		"https://chatgpt.com/":                      {status: 200},
		"https://claude.ai/cdn-cgi/trace":           {status: 200, body: "loc=JP\n"},
		"https://claude.ai/":                        {redirect: "https://claude.ai/login"},
		"https://www.google.com/search?q=vps&hl=en": {status: 200},
		"https://www.reddit.com/":                   {status: 200},
		"https://store.steampowered.com/app/761830": {status: 200, body: `<meta itemprop="priceCurrency" content="JPY">`},
		"https://www.yahoo.com/":                    {redirect: "https://sg.yahoo.com/?p=us"},
		"https://sg.yahoo.com/?p=us":                {status: 200},
		"https://truthsocial.com/api/v1/instance":   {status: 200, body: `{"uri":"truthsocial.com"}`},
		"https://web.telegram.org/":                 {status: 200},
	})
	for key, want := range map[string]protocol.UnlockItem{
		"netflix": {Service: "netflix", Status: "yes", Region: "JP"}, "disney": {Service: "disney", Status: "yes"},
		"youtube": {Service: "youtube", Status: "yes", Region: "JP"}, "prime": {Service: "prime", Status: "yes", Region: "JP"},
		"tiktok": {Service: "tiktok", Status: "yes", Region: "JP"}, "chatgpt": {Service: "chatgpt", Status: "yes", Region: "JP"},
		"claude": {Service: "claude", Status: "yes", Region: "JP"}, "google": {Service: "google", Status: "yes"},
		"reddit": {Service: "reddit", Status: "yes"}, "steam": {Service: "steam", Status: "yes", Region: "JPY", Note: "商店货币"},
		"yahoo": {Service: "yahoo", Status: "yes", Region: "SG"}, "truthsocial": {Service: "truthsocial", Status: "yes"},
		"telegram": {Service: "telegram", Status: "yes"},
	} {
		if got[key] != want {
			t.Errorf("%s：%+v，期望 %+v", key, got[key], want)
		}
	}
	if len(got) != len(Checks) { // 得不到本机地区时只有通用项
		t.Fatalf("应有 %d 项：%d", len(Checks), len(got))
	}
}

// 当地服务（修订第 196 条）：按 Cloudflare trace 得到的地区追加，排在通用项之后并标明地区
func TestUnlockLocal(t *testing.T) {
	ft := fakeTransport{
		"https://www.cloudflare.com/cdn-cgi/trace":        {status: 200, body: "loc=JP\n"},
		"https://api.abema.io/v1/ip/check?device=android": {status: 200, body: `{"isoCountryCode":"JP"}`},
		"https://radiko.jp/area":                          {status: 200, body: `document.write('<span class="JP13">TOKYO JAPAN</span>');`},
	}
	items := Run(context.Background(), &http.Client{Transport: ft})
	if len(items) != len(Checks)+len(Local["JP"]) {
		t.Fatalf("应有 %d 项：%d", len(Checks)+len(Local["JP"]), len(items))
	}
	for i, key := range Local["JP"] {
		if it := items[len(Checks)+i]; it.Service != key || it.Local != "JP" || it.Status != "yes" || it.Region != "JP" {
			t.Errorf("%s：%+v", key, it)
		}
	}
	for _, it := range items[:len(Checks)] {
		if it.Local != "" {
			t.Errorf("通用项不应标地区：%+v", it)
		}
	}
	if !(&protocol.UnlockResult{Request: "r", Items: items}).Valid() {
		t.Fatal("结果格式应有效")
	}
	for country, keys := range Local {
		if len(keys) > 6 {
			t.Errorf("%s 的当地服务超过 6 项", country)
		}
		for _, k := range keys {
			if checks[k] == nil {
				t.Errorf("%s：没有检测函数 %s", country, k)
			}
		}
	}
}

// 当地服务在外地：各项给出明确的不可用原因
func TestUnlockLocalBlocked(t *testing.T) {
	f := &fetcher{client: &http.Client{Transport: fakeTransport{
		"https://api.abema.io/v1/ip/check?device=android": {status: 403, body: `{"message":"anonymous_ip"}`},
		"https://radiko.jp/area":                          {status: 200, body: `<span class="OUT">HONG KONG</span>`},
		"https://www.mytvsuper.com/api/auth/getSession/self/": {status: 200,
			body: `{"region":1,"country_code":"JP","supported_country":false}`},
		"https://www.viu.com/":                      {status: 403},
		"https://api.kktv.me/v3/ipcheck":            {status: 200, body: `{"data":{"country":"HK","is_allowed":false}}`},
		"https://www.coupangplay.com/":              {redirect: "https://www.coupangplay.com/not-available"},
		"https://www.coupangplay.com/not-available": {status: 200},
		"https://www.hulu.com/":                     {redirect: "https://www.disneyplus.com/"},
		"https://open.live.bbc.co.uk/mediaselector/6/select/version/2.0/mediaset/pc/vpid/bbc_one_london/format/json": {status: 200,
			body: `{"result":"geolocation"}`},
	}}}
	for _, key := range []string{"abema", "radiko", "mytvsuper", "viu", "kktv", "coupangplay", "hulu", "bbc_iplayer"} {
		if it := checks[key](context.Background(), f); it.Status != "no" || it.Note == "" {
			t.Errorf("%s：%+v", key, it)
		}
	}
	bili := &fetcher{client: &http.Client{Transport: fakeTransport{
		"https://api.bilibili.com/pgc/player/web/playurl?avid=50762638&cid=100279344&qn=0&type=&otype=json&ep_id=268176&fourk=1&fnver=0&fnval=16&module=bangumi": {
			status: 200, body: `{"code":-10403,"message":"抱歉您所在地区不可观看！"}`},
		"https://api.bilibili.com/pgc/player/web/playurl?avid=18281381&cid=29892777&qn=0&type=&otype=json&ep_id=183799&fourk=1&fnver=0&fnval=16&module=bangumi": {
			status: 200, body: `{"code":0,"message":"success"}`},
	}}}
	if it := checkBilibiliTW(context.Background(), bili); it.Status != "no" {
		t.Errorf("bilibili_tw：%+v", it)
	}
	if it := checkBilibiliHMT(context.Background(), bili); it.Status != "yes" {
		t.Errorf("bilibili_hmt：%+v", it)
	}
}

// Telegram 连接失败记为不可用（多为被封锁）
func TestUnlockTelegramBlocked(t *testing.T) {
	f := &fetcher{client: &http.Client{Transport: errTransport{}}}
	if it := checkTelegram(context.Background(), f); it.Status != "no" {
		t.Errorf("telegram：%+v", it)
	}
}

type errTransport struct{}

func (errTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF }

// 受限的节点：各项按页面判断为不可用 / 仅自制剧 / 需要验证
func TestUnlockBlocked(t *testing.T) {
	got := runWith(t, fakeTransport{
		"https://www.netflix.com/title/70143836":    {status: 404},
		"https://www.netflix.com/title/80057281":    {status: 200},
		"https://www.disneyplus.com/":               {redirect: "https://www.disneyplus.com/unavailable"},
		"https://www.disneyplus.com/unavailable":    {status: 200},
		"https://www.youtube.com/premium":           {status: 200, body: "Premium is not available in your country"},
		"https://www.primevideo.com/":               {status: 403},
		"https://www.tiktok.com/":                   {status: 403},
		"https://chatgpt.com/cdn-cgi/trace":         {status: 200, body: "loc=HK\n"},
		"https://chatgpt.com/":                      {status: 403, body: `{"error":{"code":"unsupported_country"}}`},
		"https://claude.ai/cdn-cgi/trace":           {status: 200, body: "loc=HK\n"},
		"https://claude.ai/":                        {redirect: "https://claude.ai/app-unavailable-in-region"},
		"https://www.google.com/search?q=vps&hl=en": {redirect: "https://www.google.com/sorry/index?continue=x"},
		"https://www.reddit.com/":                   {status: 403},
	})
	want := map[string]string{"netflix": "partial", "disney": "no", "youtube": "no", "prime": "no", "tiktok": "no", "chatgpt": "no",
		"claude": "no", "google": "captcha", "reddit": "no", "steam": "failed"}
	for key, st := range want {
		if got[key].Status != st {
			t.Errorf("%s：%+v，期望状态 %s", key, got[key], st)
		}
	}
	if got["chatgpt"].Region != "HK" || got["netflix"].Note != "仅自制剧" {
		t.Errorf("地区或说明不对：%+v %+v", got["chatgpt"], got["netflix"])
	}
}

// Runner：每个请求号只检测一次；结果只取出一次；检测进行中时忽略新的请求号
func TestRunner(t *testing.T) {
	var runs atomic.Int32
	done := make(chan struct{}, 4)
	r := &Runner{Now: func() time.Time { return time.Unix(1700000000, 0) }}
	r.run = func(context.Context, *http.Client) []protocol.UnlockItem {
		runs.Add(1)
		defer func() { done <- struct{}{} }()
		return []protocol.UnlockItem{{Service: "netflix", Status: "yes"}}
	}
	r.Request("a1")
	<-done
	r.Request("a1") // 同一请求号不再检测
	time.Sleep(20 * time.Millisecond)
	res := r.Drain()
	if res == nil || res.Request != "a1" || res.CheckedAt != 1700000000 || len(res.Items) != 1 || !res.Valid() {
		t.Fatalf("结果 %+v", res)
	}
	if r.Drain() != nil {
		t.Fatal("结果只应取出一次")
	}
	r.Request("b2")
	<-done
	if runs.Load() != 2 || r.Drain().Request != "b2" {
		t.Fatalf("新请求号应再检测：%d", runs.Load())
	}
	r.Request("") // 空请求号忽略
	if runs.Load() != 2 {
		t.Fatal("空请求号不应检测")
	}
}

// Cloudflare 浏览器验证挡住页面时按地区判断，不当作不可用
func TestUnlockChallenge(t *testing.T) {
	got := runWith(t, fakeTransport{
		"https://chatgpt.com/cdn-cgi/trace": {status: 200, body: "loc=US\n"},
		"https://chatgpt.com/":              {status: 403, challenge: true, body: "<html>Just a moment... challenge-platform vpn</html>"},
		"https://claude.ai/cdn-cgi/trace":   {status: 200, body: "loc=CN\n"},
		"https://claude.ai/":                {status: 403, challenge: true},
	})
	if c := got["chatgpt"]; c.Status != "yes" || c.Region != "US" || c.Note != "按地区判断" {
		t.Errorf("chatgpt：%+v", c)
	}
	if c := got["claude"]; c.Status != "no" || c.Region != "CN" {
		t.Errorf("claude：%+v", c)
	}
}
