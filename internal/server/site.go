package server

// 网站配置（设计 52，修订第 201 条）：应用名称、界面语言与登录验证码开关，在“系统 → 网站配置”中修改。
// 应用名称与语言在登录前就要用到（登录页、浏览器标签），所以 GET /api/v1/site 无需登录；其中只有显示用的信息。
// 登录验证码：启动参数 --no-login-captcha 优先（关闭后网页中不能打开）；关闭验证码不影响登录限速（设计 17.4）。

import (
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

const settingSite = "site"

// SiteSetting 是网站配置（api/openapi.yaml SiteSetting）。
type SiteSetting struct {
	AppName  string `json:"app_name"`
	Language string `json:"language"`
	Captcha  bool   `json:"captcha"`
}

// siteLanguages 是界面支持的语言：zh 简体中文，en English。
var siteLanguages = []string{"zh", "en"}

func defaultSite() SiteSetting { return SiteSetting{AppName: "VPS Monitor", Language: "zh", Captcha: true} }

type siteView struct {
	SiteSetting
	CaptchaForcedOff bool `json:"captcha_forced_off"` // 启动参数关闭了验证码，网页中不能打开
}

// siteCache 缓存网站配置，登录与每次页面加载都会读取，不必每次查库。
type siteCache struct{ v atomic.Pointer[SiteSetting] }

// siteSetting 返回网站配置；没有保存过时为默认值。
func (s *Server) siteSetting() SiteSetting {
	if v := s.site.v.Load(); v != nil {
		return *v
	}
	v := defaultSite()
	if _, err := s.store.GetSetting(settingSite, &v); err != nil {
		s.log.Error("read site setting failed", "component", "site", "err", err)
		return defaultSite()
	}
	s.site.v.Store(&v)
	return v
}

// captchaOn 判断登录是否需要验证码：启动参数没有关闭，且网站配置中打开。
func (s *Server) captchaOn() bool { return s.captcha != nil && s.siteSetting().Captcha }

func (s *Server) siteView() siteView {
	v := s.siteSetting()
	v.Captcha = s.captchaOn()
	return siteView{SiteSetting: v, CaptchaForcedOff: s.captcha == nil}
}

// handleGetSite：GET /api/v1/site，无需登录。应用名称、语言与是否需要验证码。
func (s *Server) handleGetSite(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.siteView())
}

// handlePutSite：PUT /api/v1/settings/site，admin。
func (s *Server) handlePutSite(w http.ResponseWriter, r *http.Request) {
	var in SiteSetting
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err)
		return
	}
	in.AppName = strings.TrimSpace(in.AppName)
	var fe []FieldError
	if n := utf8.RuneCountInString(in.AppName); n == 0 || n > 32 || strings.IndexFunc(in.AppName, unicode.IsControl) >= 0 {
		fe = append(fe, FieldError{Field: "app_name", Message: "应用名称为 1～32 个字符"})
	}
	if !slices.Contains(siteLanguages, in.Language) {
		fe = append(fe, FieldError{Field: "language", Message: "语言可选：zh、en"})
	}
	if fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	if s.captcha == nil {
		in.Captcha = false // 启动参数关闭了验证码：保存为关闭，避免以为已经打开
	}
	if err := s.store.PutSetting(settingSite, in, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.site.v.Store(&in)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "setting.update", Success: true,
		Details: map[string]any{"key": settingSite, "app_name": in.AppName, "language": in.Language, "captcha": in.Captcha}})
	writeJSON(w, s.siteView())
}
