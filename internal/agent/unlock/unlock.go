// Package unlock 实现流媒体与网站解锁检测（设计 50，修订第 192 条）：从本机访问常用流媒体、AI 服务与网站，
// 判断能否使用以及服务认定的地区。
//
// 【安全】检测项全部内置在这里，面板只能请求“现在检测一次”（约束 1：Agent 不接受面板下发的网址或命令）。
// 只发 HTTPS GET，不提交任何账号信息；每项 10 秒超时，同时最多 4 项，整次检测 90 秒内结束。
// 判断依据是各服务公开页面的表现（跳转到“地区不可用”页面、页面中的地区代码、HTTP 403 等），为本项目独立实现；
// 服务改版后某项可能判断为“检测失败”，不影响其他项。
package unlock

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"vpsmon/internal/protocol"
)

const (
	itemTimeout  = 10 * time.Second
	totalTimeout = 90 * time.Second
	parallel     = 4
	maxBody      = 1 << 20
	// 不少服务会拒绝非浏览器的请求头，这里用常见的桌面浏览器标识
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
)

// check 是一个检测项。
type check struct {
	key string
	run func(ctx context.Context, f *fetcher) protocol.UnlockItem
}

// Checks 是全部检测项的键（显示顺序与面板一致）。
var Checks = []string{"netflix", "disney", "youtube", "prime", "tiktok", "chatgpt", "claude", "google", "reddit", "steam"}

var checks = map[string]func(ctx context.Context, f *fetcher) protocol.UnlockItem{
	"netflix": checkNetflix, "disney": checkDisney, "youtube": checkYouTube, "prime": checkPrime, "tiktok": checkTikTok,
	"chatgpt": checkChatGPT, "claude": checkClaude, "google": checkGoogle, "reddit": checkReddit, "steam": checkSteam,
}

// Run 执行全部检测项，按 Checks 的顺序返回结果。client 为空时使用默认客户端。
func Run(ctx context.Context, client *http.Client) []protocol.UnlockItem {
	if client == nil {
		client = &http.Client{}
	}
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	out := make([]protocol.UnlockItem, len(Checks))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i, key := range Checks {
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ictx, icancel := context.WithTimeout(ctx, itemTimeout)
			defer icancel()
			it := checks[key](ictx, &fetcher{client: client})
			it.Service = key
			if len(it.Note) > 200 {
				it.Note = it.Note[:200]
			}
			out[i] = it
		}(i, key)
	}
	wg.Wait()
	return out
}

// ---- 请求 ----

type fetcher struct{ client *http.Client }

type page struct {
	status    int
	url       string // 跟随跳转后的最终地址
	loc       string // 不跟随跳转时的 Location
	body      string
	challenge bool // Cloudflare 的浏览器验证（cf-mitigated: challenge）：非浏览器请求拿不到页面，不代表地区不可用
}

// get 发送 GET；follow 为 false 时不跟随跳转（用于读取 Location）。
func (f *fetcher) get(ctx context.Context, url string, follow bool, headers ...string) (page, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return page{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	c := *f.client
	if !follow {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	res, err := c.Do(req)
	if err != nil {
		return page{}, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, maxBody))
	return page{status: res.StatusCode, url: res.Request.URL.String(), loc: res.Header.Get("Location"), body: string(b),
		challenge: res.Header.Get("cf-mitigated") == "challenge"}, nil
}

// failed 把网络错误转成“检测失败”，说明简短。
func failed(err error) protocol.UnlockItem {
	note := "网络错误"
	if errors.Is(err, context.DeadlineExceeded) {
		note = "超时"
	}
	return protocol.UnlockItem{Status: protocol.UnlockFailed, Note: note}
}

func unknown(p page) protocol.UnlockItem {
	return protocol.UnlockItem{Status: protocol.UnlockFailed, Note: "无法判断（HTTP " + itoa(p.status) + "）"}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func find(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.ToUpper(m[1])
	}
	return ""
}

// traceLoc 读取 Cloudflare 的 /cdn-cgi/trace（loc=XX），得到服务认定的地区。
func (f *fetcher) traceLoc(ctx context.Context, host string) string {
	p, err := f.get(ctx, "https://"+host+"/cdn-cgi/trace", true)
	if err != nil || p.status != http.StatusOK {
		return ""
	}
	return find(reLoc, p.body)
}

var (
	reLoc          = regexp.MustCompile(`(?m)^loc=([A-Z]{2})$`)
	reNetflixPath  = regexp.MustCompile(`netflix\.com/([a-z]{2})(?:-[a-z]{2})?/title/`)
	reCountryCode  = regexp.MustCompile(`"(?:countryCode|INNERTUBE_CONTEXT_GL)":"([A-Z]{2})"`)
	reTerritory    = regexp.MustCompile(`"currentTerritory":"([A-Z]{2})"`)
	reTikTokRegion = regexp.MustCompile(`"region":"([A-Z]{2})"`)
	reSteamCur     = regexp.MustCompile(`"priceCurrency"\s+content="([A-Z]{3})"`)
)

// ---- 检测项 ----

// Netflix：一部授权剧（各地多有）与一部自制剧的详情页。授权剧能打开为完整解锁，只有自制剧能打开为“仅自制剧”。
// 非美国地区会跳转到带地区前缀的地址（/jp/title/…），据此得到地区。
func checkNetflix(ctx context.Context, f *fetcher) protocol.UnlockItem {
	licensed, err := f.get(ctx, "https://www.netflix.com/title/70143836", true)
	if err != nil {
		return failed(err)
	}
	original, err := f.get(ctx, "https://www.netflix.com/title/80057281", true)
	if err != nil {
		return failed(err)
	}
	region := find(reNetflixPath, licensed.url)
	if region == "" {
		region = find(reNetflixPath, original.url)
	}
	switch {
	case licensed.status == http.StatusOK:
		if region == "" {
			region = "US"
		}
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: region}
	case original.status == http.StatusOK:
		return protocol.UnlockItem{Status: protocol.UnlockPartial, Region: region, Note: "仅自制剧"}
	case licensed.status == http.StatusForbidden || original.status == http.StatusForbidden:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "被拒绝访问"}
	case licensed.status == http.StatusNotFound && original.status == http.StatusNotFound:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "所在地区不提供服务"}
	}
	return unknown(licensed)
}

// Disney+：不提供服务的地区跳转到 /unavailable 一类页面。
func checkDisney(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://www.disneyplus.com/", true)
	if err != nil {
		return failed(err)
	}
	switch {
	case strings.Contains(p.url, "unavailable") || p.status == http.StatusForbidden:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "所在地区不提供服务"}
	case p.status == http.StatusOK:
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: find(reCountryCode, p.body)}
	}
	return unknown(p)
}

// YouTube Premium：会员页中有“Premium is not available in your country”时不可用；地区来自页面中的 countryCode。
func checkYouTube(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://www.youtube.com/premium", true, "Cookie", "CONSENT=YES+cb; SOCS=CAI")
	if err != nil {
		return failed(err)
	}
	region := find(reCountryCode, p.body)
	switch {
	case strings.Contains(p.body, "Premium is not available in your country"):
		return protocol.UnlockItem{Status: protocol.UnlockNo, Region: region, Note: "所在地区不提供 Premium"}
	case p.status == http.StatusOK && region != "":
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: region}
	case p.status == http.StatusOK && strings.Contains(p.url, "consent."):
		return protocol.UnlockItem{Status: protocol.UnlockFailed, Note: "被要求同意 Cookie，无法判断"}
	}
	return unknown(p)
}

// Prime Video：首页中的 currentTerritory 为服务地区。
func checkPrime(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://www.primevideo.com/", true)
	if err != nil {
		return failed(err)
	}
	switch region := find(reTerritory, p.body); {
	case p.status == http.StatusForbidden:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "被拒绝访问"}
	case p.status == http.StatusOK && region != "":
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: region}
	case p.status == http.StatusOK:
		return protocol.UnlockItem{Status: protocol.UnlockYes}
	}
	return unknown(p)
}

// TikTok：首页中的 region 为服务认定的地区；被封锁时为 403 或“Access Denied”。
func checkTikTok(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://www.tiktok.com/", true)
	if err != nil {
		return failed(err)
	}
	switch region := find(reTikTokRegion, p.body); {
	case p.status == http.StatusForbidden || strings.Contains(p.body, "Access Denied"):
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "被拒绝访问"}
	case p.status == http.StatusOK && region != "":
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: region}
	case p.status == http.StatusOK:
		return protocol.UnlockItem{Status: protocol.UnlockYes}
	}
	return unknown(p)
}

// aiBlocked 是 OpenAI 与 Anthropic 都不提供服务的主要地区（两家公开的支持地区列表之外）。
var aiBlocked = map[string]bool{"CN": true, "HK": true, "MO": true, "RU": true, "BY": true, "IR": true, "KP": true, "SY": true, "CU": true}

// byRegion 在页面被 Cloudflare 浏览器验证挡住时，按服务认定的地区判断（注明是按地区判断）。
func byRegion(region string) protocol.UnlockItem {
	switch {
	case region == "":
		return protocol.UnlockItem{Status: protocol.UnlockFailed, Note: "页面需要浏览器验证，且无法得到地区"}
	case aiBlocked[region]:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Region: region, Note: "所在地区不提供服务"}
	}
	return protocol.UnlockItem{Status: protocol.UnlockYes, Region: region, Note: "按地区判断"}
}

// ChatGPT：地区取自 chatgpt.com 的 Cloudflare trace；页面返回 unsupported_country 或地区不提供服务时为不可用。
func checkChatGPT(ctx context.Context, f *fetcher) protocol.UnlockItem {
	region := f.traceLoc(ctx, "chatgpt.com")
	p, err := f.get(ctx, "https://chatgpt.com/", false)
	if err != nil {
		return failed(err)
	}
	switch {
	case strings.Contains(p.body, "unsupported_country") || aiBlocked[region]:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Region: region, Note: "所在地区不提供服务"}
	case p.challenge:
		return byRegion(region)
	case region != "" && p.status < 500:
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: region}
	}
	return unknown(p)
}

// Claude：不提供服务的地区跳转到 /app-unavailable-in-region。
func checkClaude(ctx context.Context, f *fetcher) protocol.UnlockItem {
	region := f.traceLoc(ctx, "claude.ai")
	p, err := f.get(ctx, "https://claude.ai/", false)
	if err != nil {
		return failed(err)
	}
	switch {
	case strings.Contains(p.loc, "unavailable") || strings.Contains(p.url, "unavailable"):
		return protocol.UnlockItem{Status: protocol.UnlockNo, Region: region, Note: "所在地区不提供服务"}
	case p.challenge:
		return byRegion(region)
	case p.status == http.StatusForbidden:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Region: region, Note: "被拒绝访问"}
	case p.status < 500:
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: region}
	}
	return unknown(p)
}

// Google 搜索：IP 信誉差时跳转到 /sorry/ 或返回 429，要求人机验证。
func checkGoogle(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://www.google.com/search?q=vps&hl=en", false)
	if err != nil {
		return failed(err)
	}
	switch {
	case p.status == http.StatusTooManyRequests || strings.Contains(p.loc, "/sorry/") || strings.Contains(p.body, "unusual traffic"):
		return protocol.UnlockItem{Status: protocol.UnlockCaptcha, Note: "要求人机验证"}
	case p.status == http.StatusOK || (p.status/100 == 3 && !strings.Contains(p.loc, "/sorry/")):
		return protocol.UnlockItem{Status: protocol.UnlockYes}
	}
	return unknown(p)
}

// Reddit：常拒绝数据中心 IP（403）。
func checkReddit(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://www.reddit.com/", true)
	if err != nil {
		return failed(err)
	}
	switch {
	case p.status == http.StatusForbidden || p.status == http.StatusTooManyRequests:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "被拒绝访问（常见于数据中心 IP）"}
	case p.status == http.StatusOK:
		return protocol.UnlockItem{Status: protocol.UnlockYes}
	}
	return unknown(p)
}

// Steam：商店页的标价货币（priceCurrency）反映商店地区。
func checkSteam(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://store.steampowered.com/app/761830", true, "Cookie", "birthtime=0; mature_content=1")
	if err != nil {
		return failed(err)
	}
	if cur := find(reSteamCur, p.body); p.status == http.StatusOK && cur != "" {
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: cur, Note: "商店货币"}
	}
	if p.status == http.StatusOK {
		return protocol.UnlockItem{Status: protocol.UnlockYes}
	}
	return unknown(p)
}
