// Package unlock 实现流媒体与网站解锁检测（设计 50，修订第 192 条）：从本机访问常用流媒体、AI 服务与网站，
// 判断能否使用以及服务认定的地区。
//
// 【安全】检测项全部内置在这里，面板只能请求“现在检测一次”（约束 1：Agent 不接受面板下发的网址或命令）。
// 只发 HTTPS GET，不提交任何账号信息；每项 10 秒超时，同时最多 4 项，整次检测 90 秒内结束。
// 判断依据是各服务公开页面的表现（跳转到“地区不可用”页面、页面中的地区代码、HTTP 403 等），为本项目独立实现；
// 服务改版后某项可能判断为“检测失败”，不影响其他项。
// 除通用检测项外，先从 Cloudflare 的 trace 得到本机所在地区，再追加该地区的当地服务（每个地区不超过 6 项，修订第 196 条）。
package unlock

import (
	"context"
	"encoding/json"
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

// Checks 是通用检测项的键（显示顺序与面板一致）。
var Checks = []string{"netflix", "disney", "youtube", "prime", "tiktok", "chatgpt", "claude", "google", "reddit", "steam",
	"yahoo", "truthsocial", "telegram"}

// Local 是各地区追加的当地服务（修订第 196 条），每个地区不超过 6 项；只收录有明确地区判断依据的服务，
// 没有收录的地区只检测通用项。
var Local = map[string][]string{
	"JP": {"abema", "radiko"},
	"HK": {"mytvsuper", "viu", "bilibili_hmt"},
	"MO": {"bilibili_hmt"},
	"TW": {"kktv", "bilibili_tw", "bilibili_hmt"},
	"KR": {"coupangplay"},
	"US": {"hulu"},
	"GB": {"bbc_iplayer"},
	"SG": {"viu"}, "MY": {"viu"}, "TH": {"viu"}, "ID": {"viu"}, "PH": {"viu"},
}

var checks = map[string]func(ctx context.Context, f *fetcher) protocol.UnlockItem{
	"netflix": checkNetflix, "disney": checkDisney, "youtube": checkYouTube, "prime": checkPrime, "tiktok": checkTikTok,
	"chatgpt": checkChatGPT, "claude": checkClaude, "google": checkGoogle, "reddit": checkReddit, "steam": checkSteam,
	"yahoo": checkYahoo, "truthsocial": checkTruthSocial, "telegram": checkTelegram,
	"abema": checkAbema, "radiko": checkRadiko, "mytvsuper": checkMyTVSuper, "viu": checkViu,
	"bilibili_hmt": checkBilibiliHMT, "bilibili_tw": checkBilibiliTW, "kktv": checkKKTV, "coupangplay": checkCoupangPlay,
	"hulu": checkHulu, "bbc_iplayer": checkBBCiPlayer,
}

// Run 执行通用检测项与本机所在地区的当地服务，按 Checks、Local 的顺序返回结果。client 为空时使用默认客户端。
func Run(ctx context.Context, client *http.Client) []protocol.UnlockItem {
	if client == nil {
		client = &http.Client{}
	}
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	type job struct{ key, local string }
	jobs := make([]job, 0, len(Checks)+6)
	for _, key := range Checks {
		jobs = append(jobs, job{key: key})
	}
	// 本机所在地区：取自 Cloudflare 的 trace（与 ChatGPT、Claude 判断地区的方式相同）
	tctx, tcancel := context.WithTimeout(ctx, itemTimeout)
	country := (&fetcher{client: client}).traceLoc(tctx, "www.cloudflare.com")
	tcancel()
	for _, key := range Local[country] {
		jobs = append(jobs, job{key: key, local: country})
	}
	out := make([]protocol.UnlockItem, len(jobs))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ictx, icancel := context.WithTimeout(ctx, itemTimeout)
			defer icancel()
			it := checks[j.key](ictx, &fetcher{client: client})
			it.Service, it.Local = j.key, j.local
			if len(it.Note) > 200 {
				it.Note = it.Note[:200]
			}
			out[i] = it
		}(i, j)
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
	reYahooHost    = regexp.MustCompile(`^https://([a-z]{2})\.yahoo\.com/`)
	reRadikoArea   = regexp.MustCompile(`class="(JP\d+|OUT)"`)
	reViuRegion    = regexp.MustCompile(`viu\.com/ott/([a-z]{2})(?:/|$)`)
	reBiliCode     = regexp.MustCompile(`^\{"code":(-?\d+)`)
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

// Yahoo：首页按地区跳转到当地站点（如 hk.yahoo.com），据此得到地区；403 / 451 为被拒绝。
// 欧洲经济区会先跳转到 Cookie 同意页（guce / consent），仍算可用。
func checkYahoo(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://www.yahoo.com/", true)
	if err != nil {
		return failed(err)
	}
	switch {
	case p.status == http.StatusForbidden || p.status == http.StatusUnavailableForLegalReasons:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "被拒绝访问"}
	case p.status == http.StatusOK:
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: find(reYahooHost, p.url)}
	}
	return unknown(p)
}

// Truth Social：公开的实例信息接口（客户端使用）能取到为可用；网页对非浏览器请求常返回 403，不作为依据。
func checkTruthSocial(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://truthsocial.com/api/v1/instance", true, "Accept", "application/json")
	if err != nil {
		return failed(err)
	}
	switch {
	case p.status == http.StatusOK && strings.Contains(p.body, "truthsocial.com"):
		return protocol.UnlockItem{Status: protocol.UnlockYes}
	case p.status == http.StatusForbidden || p.status == http.StatusUnavailableForLegalReasons:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "被拒绝访问"}
	}
	return unknown(p)
}

// Telegram：网页版能打开为可用；连接失败多为被封锁（如中国大陆），记为不可用并注明。
func checkTelegram(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://web.telegram.org/", true)
	if err != nil {
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "无法连接（可能被封锁）"}
	}
	if p.status == http.StatusOK {
		return protocol.UnlockItem{Status: protocol.UnlockYes}
	}
	return unknown(p)
}

// ---- 当地服务（修订第 196 条） ----

// ABEMA（日本）：IP 检查接口在日本返回 200 与国家代码；海外或被识别为代理时返回 403（anonymous_ip）。
func checkAbema(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://api.abema.io/v1/ip/check?device=android", true, "Accept", "application/json")
	if err != nil {
		return failed(err)
	}
	var v struct {
		ISOCountryCode string `json:"isoCountryCode"`
		Message        string `json:"message"`
	}
	_ = json.Unmarshal([]byte(p.body), &v)
	switch {
	case p.status == http.StatusOK && v.ISOCountryCode == "JP":
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: "JP"}
	case p.status == http.StatusOK:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Region: strings.ToUpper(v.ISOCountryCode), Note: "仅限日本"}
	case p.status == http.StatusForbidden && v.Message == "anonymous_ip":
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "被识别为代理或数据中心 IP"}
	case p.status == http.StatusForbidden:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "仅限日本"}
	}
	return unknown(p)
}

// radiko（日本）：/area 返回所在都道府县（class="JP13"）；海外为 class="OUT"。
func checkRadiko(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://radiko.jp/area", true)
	if err != nil {
		return failed(err)
	}
	switch area := find(reRadikoArea, p.body); {
	case p.status == http.StatusOK && area == "OUT":
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "仅限日本"}
	case p.status == http.StatusOK && area != "":
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: "JP", Note: area}
	}
	return unknown(p)
}

// myTV SUPER（香港）：会话接口返回国家代码与是否在服务地区。
func checkMyTVSuper(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://www.mytvsuper.com/api/auth/getSession/self/", true, "Accept", "application/json")
	if err != nil {
		return failed(err)
	}
	var v struct {
		CountryCode      string `json:"country_code"`
		SupportedCountry *bool  `json:"supported_country"`
	}
	if p.status != http.StatusOK || json.Unmarshal([]byte(p.body), &v) != nil || v.SupportedCountry == nil {
		return unknown(p)
	}
	if *v.SupportedCountry {
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: strings.ToUpper(v.CountryCode)}
	}
	return protocol.UnlockItem{Status: protocol.UnlockNo, Region: strings.ToUpper(v.CountryCode), Note: "所在地区不提供服务"}
}

// Viu（香港、东南亚）：首页跳转到带地区的地址（/ott/hk），据此得到服务地区。
func checkViu(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://www.viu.com/", true)
	if err != nil {
		return failed(err)
	}
	if region := find(reViuRegion, p.url); p.status == http.StatusOK && region != "" {
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: region}
	}
	if p.status == http.StatusForbidden {
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "被拒绝访问"}
	}
	return unknown(p)
}

// bilibili 港澳台 / 仅台湾：请求一集限定地区番剧的播放地址；code 0 为可看，-10403 为所在地区不可观看。
func checkBilibili(ctx context.Context, f *fetcher, url string) protocol.UnlockItem {
	p, err := f.get(ctx, url, true, "Accept", "application/json")
	if err != nil {
		return failed(err)
	}
	m := reBiliCode.FindStringSubmatch(p.body)
	switch {
	case m == nil:
		return unknown(p)
	case m[1] == "0":
		return protocol.UnlockItem{Status: protocol.UnlockYes}
	case m[1] == "-10403":
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "所在地区不可观看"}
	}
	return protocol.UnlockItem{Status: protocol.UnlockFailed, Note: "无法判断（code " + m[1] + "）"}
}

func checkBilibiliHMT(ctx context.Context, f *fetcher) protocol.UnlockItem {
	return checkBilibili(ctx, f, "https://api.bilibili.com/pgc/player/web/playurl?avid=18281381&cid=29892777&qn=0&type=&otype=json&ep_id=183799&fourk=1&fnver=0&fnval=16&module=bangumi")
}

func checkBilibiliTW(ctx context.Context, f *fetcher) protocol.UnlockItem {
	return checkBilibili(ctx, f, "https://api.bilibili.com/pgc/player/web/playurl?avid=50762638&cid=100279344&qn=0&type=&otype=json&ep_id=268176&fourk=1&fnver=0&fnval=16&module=bangumi")
}

// KKTV（台湾）：IP 检查接口返回国家代码与是否允许观看。
func checkKKTV(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://api.kktv.me/v3/ipcheck", true, "Accept", "application/json")
	if err != nil {
		return failed(err)
	}
	var v struct {
		Data struct {
			Country   string `json:"country"`
			IsAllowed *bool  `json:"is_allowed"`
		} `json:"data"`
	}
	if p.status != http.StatusOK || json.Unmarshal([]byte(p.body), &v) != nil || v.Data.IsAllowed == nil {
		return unknown(p)
	}
	if *v.Data.IsAllowed {
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: strings.ToUpper(v.Data.Country)}
	}
	return protocol.UnlockItem{Status: protocol.UnlockNo, Region: strings.ToUpper(v.Data.Country), Note: "仅限台湾"}
}

// Coupang Play（韩国）：海外跳转到 /not-available。
func checkCoupangPlay(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://www.coupangplay.com/", true)
	if err != nil {
		return failed(err)
	}
	switch {
	case strings.Contains(p.url, "not-available"):
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "仅限韩国"}
	case p.status == http.StatusOK:
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: "KR"}
	}
	return unknown(p)
}

// Hulu（美国）：美国以外跳转到 Disney+。
func checkHulu(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://www.hulu.com/", false)
	if err != nil {
		return failed(err)
	}
	switch {
	case strings.Contains(p.loc, "disneyplus.com") || p.status == http.StatusForbidden:
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "仅限美国"}
	case p.status == http.StatusOK || p.status/100 == 3:
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: "US"}
	}
	return unknown(p)
}

// BBC iPlayer（英国）：播放源选择接口在英国以外返回 "result":"geolocation"。
func checkBBCiPlayer(ctx context.Context, f *fetcher) protocol.UnlockItem {
	p, err := f.get(ctx, "https://open.live.bbc.co.uk/mediaselector/6/select/version/2.0/mediaset/pc/vpid/bbc_one_london/format/json", true)
	if err != nil {
		return failed(err)
	}
	switch {
	case strings.Contains(p.body, `"result":"geolocation"`):
		return protocol.UnlockItem{Status: protocol.UnlockNo, Note: "仅限英国"}
	case p.status == http.StatusOK && strings.Contains(p.body, `"media"`):
		return protocol.UnlockItem{Status: protocol.UnlockYes, Region: "GB"}
	}
	return unknown(p)
}
