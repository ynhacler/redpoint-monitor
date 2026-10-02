package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCaptchaVerify(t *testing.T) {
	c := newCaptchaStore()
	now := time.Now()
	v, err := c.newChallenge(now)
	if err != nil || len(v.ID) != 32 || !strings.HasPrefix(v.Background, "data:image/png;base64,") || v.Width != captchaW {
		t.Fatalf("生成验证码：%+v %v", v.ID, err)
	}
	cases := []struct {
		name   string
		dx     float64
		ms     int
		at     time.Time
		reuse  bool
		wantOK bool
	}{
		{"位置正确、正常拖动", 3, 800, now, false, true},
		{"位置偏差过大", 15, 800, now, false, false},
		{"拖动过快（脚本）", 0, 50, now, false, false},
		{"已过期", 0, 800, now.Add(captchaTTL + time.Second), false, false},
		{"同一挑战只能用一次：成功使用后再次提交", 0, 800, now, true, false},
	}
	for _, tc := range cases {
		nv, _ := c.newChallenge(now)
		id, x := nv.ID, c.m[nv.ID].x
		if tc.reuse && !c.verify(id, x, 800, now) { // 先正常使用一次
			t.Fatalf("%s：首次使用应通过", tc.name)
		}
		if got := c.verify(id, x+tc.dx, tc.ms, tc.at); got != tc.wantOK {
			t.Errorf("%s：%v，应为 %v", tc.name, got, tc.wantOK)
		}
	}
}

// 开启验证码时：未完成验证码不会核对密码；完成后可登录（设计 17.4）。
func TestLoginRequiresCaptcha(t *testing.T) {
	s, h, _ := testServer(t)
	s.captcha = newCaptchaStore()
	newAdmin(t, s, "correct horse battery", false)

	post := func(body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(string(b)))
		req.RemoteAddr = "198.51.100.30:1"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := post(map[string]any{"username": "admin", "password": "correct horse battery"})
	if rec.Code != 400 || decodeError(t, rec).Code != CodeCaptchaFailed {
		t.Fatalf("缺少验证码时即使密码正确也应拒绝：%d %s", rec.Code, rec.Body)
	}

	get := httptest.NewRequest("GET", "/api/v1/auth/captcha", nil)
	get.RemoteAddr = "198.51.100.30:1"
	grec := httptest.NewRecorder()
	h.ServeHTTP(grec, get)
	var v captchaView
	json.Unmarshal(grec.Body.Bytes(), &v)
	if grec.Code != 200 || grec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("获取验证码：%d", grec.Code)
	}
	if strings.Contains(grec.Body.String(), `"x"`) {
		t.Error("【安全】响应中不得包含正确位置")
	}
	x := s.captcha.m[v.ID].x
	rec = post(map[string]any{"username": "admin", "password": "correct horse battery",
		"captcha_id": v.ID, "captcha_x": x + 2, "captcha_ms": 900})
	if rec.Code != http.StatusOK {
		t.Errorf("完成验证码后应能登录：%d %s", rec.Code, rec.Body)
	}
}
