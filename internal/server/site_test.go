package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// 网站配置（设计 52）：登录前可读；修改后立即生效；关闭验证码后登录不需要验证码
func TestSiteSetting(t *testing.T) {
	s, h, _ := testServer(t)
	s.captcha = newCaptchaStore()
	admin := adminToken(t, s)
	var v siteView
	rec := do(h, "GET", "/api/v1/site", "", nil)
	json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != 200 || v.AppName != "VPS Monitor" || v.Language != "zh" || !v.Captcha || v.CaptchaForcedOff {
		t.Fatalf("默认值：%d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]string{
		"空名称": `{"app_name":"  ","language":"zh","captcha":true}`,
		"太长":  `{"app_name":"` + strings.Repeat("长", 33) + `","language":"zh","captcha":true}`,
		"控制符": `{"app_name":"a\u0007b","language":"zh","captcha":true}`,
		"语言":  `{"app_name":"flux","language":"fr","captcha":true}`,
	} {
		if rec := do(h, "PUT", "/api/v1/settings/site", admin, []byte(body)); rec.Code != 422 {
			t.Errorf("%s 应被拒绝：%d", name, rec.Code)
		}
	}
	rec = do(h, "PUT", "/api/v1/settings/site", admin, []byte(`{"app_name":" flux ","language":"en","captcha":false}`))
	json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != 200 || v.AppName != "flux" || v.Language != "en" || v.Captcha {
		t.Fatalf("保存：%d %s", rec.Code, rec.Body)
	}
	// 关闭验证码：获取验证码返回 404，登录不需要验证码
	if rec := do(h, "GET", "/api/v1/auth/captcha", "", nil); rec.Code != 404 {
		t.Fatalf("关闭后不应提供验证码：%d", rec.Code)
	}
	if rec := login(h, "admin", "correct horse battery", false, false); rec.Code == 400 && decodeError(t, rec).Code == CodeCaptchaFailed {
		t.Fatal("关闭验证码后登录不应要求验证码")
	}
	// 保存在数据库：新的缓存仍读到
	s.site.v.Store(nil)
	if got := s.siteSetting(); got.AppName != "flux" || got.Language != "en" {
		t.Fatalf("应从数据库读取：%+v", got)
	}
	// 启动参数关闭了验证码：网页中打开也保存为关闭
	s.captcha = nil
	rec = do(h, "PUT", "/api/v1/settings/site", admin, []byte(`{"app_name":"flux","language":"zh","captcha":true}`))
	json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != 200 || v.Captcha || !v.CaptchaForcedOff {
		t.Fatalf("启动参数优先：%d %s", rec.Code, rec.Body)
	}
}
