package server

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"sync"
	"time"
)

// 登录滑动拼图验证码（设计 17.4，修订记录第 24 条）。
//
// 自建实现：不接入任何第三方验证码服务（设计 1.8 禁止遥测与第三方 SDK）。服务端随机生成背景与缺口位置，
// 只把图片发给浏览器；正确位置只保存在服务端内存中，登录时校验滑块位置与拖动用时。
// 【安全】验证码只增加脚本暴力尝试的成本，不能替代登录限流（同一 IP 1 分钟失败 5 次锁定 15 分钟）。

const (
	captchaW, captchaH = 280, 140               // 背景图尺寸，像素；前端按原尺寸显示
	pieceSize          = 44                     // 拼图块边长
	captchaTTL         = 2 * time.Minute        // 有效期
	captchaTolerance   = 6.0                    // 允许的位置误差，像素
	captchaMinDrag     = 300 * time.Millisecond // 拖动用时下限：低于此值视为脚本
	captchaMax         = 10000                  // 内存中最多保存的挑战数，超出时清理过期项
)

// challenge 是一次验证码挑战；只能使用一次。
type challenge struct {
	x       float64 // 缺口左边缘的正确位置
	expires time.Time
}

type captchaStore struct {
	mu sync.Mutex
	m  map[string]challenge
}

func newCaptchaStore() *captchaStore { return &captchaStore{m: map[string]challenge{}} }

// captchaView 是发给浏览器的挑战：背景（带缺口）与拼图块图片，以及拼图块的纵向位置。
type captchaView struct {
	ID         string `json:"id"`
	Background string `json:"background"` // data:image/png;base64,…
	Piece      string `json:"piece"`
	PieceY     int    `json:"piece_y"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	PieceSize  int    `json:"piece_size"`
}

// newChallenge 生成一次挑战并保存正确位置。
func (c *captchaStore) newChallenge(now time.Time) (captchaView, error) {
	// 缺口不贴左边（滑块起点）也不贴右边
	x := pieceSize + 20 + mrand.IntN(captchaW-2*pieceSize-30)
	y := 10 + mrand.IntN(captchaH-pieceSize-20)
	bg, piece := drawPuzzle(x, y)
	bgURL, err := pngDataURL(bg)
	if err != nil {
		return captchaView{}, err
	}
	pieceURL, err := pngDataURL(piece)
	if err != nil {
		return captchaView{}, err
	}
	idb := make([]byte, 16)
	if _, err := rand.Read(idb); err != nil {
		return captchaView{}, err
	}
	id := hex.EncodeToString(idb)

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= captchaMax {
		for k, v := range c.m {
			if now.After(v.expires) {
				delete(c.m, k)
			}
		}
		if len(c.m) >= captchaMax {
			return captchaView{}, errorf(CodeRateLimited, "验证码请求过于频繁，请稍后再试")
		}
	}
	c.m[id] = challenge{x: float64(x), expires: now.Add(captchaTTL)}
	return captchaView{ID: id, Background: bgURL, Piece: pieceURL, PieceY: y,
		Width: captchaW, Height: captchaH, PieceSize: pieceSize}, nil
}

// verify 校验并作废挑战（无论成功与否都只能用一次）。
func (c *captchaStore) verify(id string, x float64, dragMs int, now time.Time) bool {
	c.mu.Lock()
	ch, ok := c.m[id]
	delete(c.m, id)
	c.mu.Unlock()
	return ok && now.Before(ch.expires) && math.Abs(x-ch.x) <= captchaTolerance &&
		time.Duration(dragMs)*time.Millisecond >= captchaMinDrag
}

func pngDataURL(img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// inPiece 判断点是否在拼图形状内：方块加右侧一个半圆凸起，增加自动识别缺口的难度。
func inPiece(px, py int) bool {
	const body = pieceSize - 8 // 方块部分边长，右侧留给凸起
	if px >= 0 && px < body && py >= 4 && py < pieceSize-4 {
		return true
	}
	dx, dy := float64(px-body), float64(py-pieceSize/2)
	return dx*dx+dy*dy <= 7*7
}

// drawPuzzle 绘制随机背景，在 (x, y) 处挖出缺口，并返回对应的拼图块。
func drawPuzzle(x, y int) (*image.RGBA, *image.RGBA) {
	bg := image.NewRGBA(image.Rect(0, 0, captchaW, captchaH))
	// 背景：随机色调的平滑渐变 + 若干随机圆斑 + 少量噪点，每次都不同
	h0 := mrand.Float64() * 360
	type blob struct {
		cx, cy, r float64
		c         color.RGBA
	}
	blobs := make([]blob, 7)
	for i := range blobs {
		blobs[i] = blob{mrand.Float64() * captchaW, mrand.Float64() * captchaH, 18 + mrand.Float64()*40,
			hsl(math.Mod(h0+mrand.Float64()*120, 360), 0.45, 0.45+mrand.Float64()*0.25)}
	}
	for py := 0; py < captchaH; py++ {
		for px := 0; px < captchaW; px++ {
			t := float64(px+py) / float64(captchaW+captchaH)
			c := hsl(math.Mod(h0+t*80, 360), 0.35, 0.55+0.15*math.Sin(float64(px)/23))
			for _, b := range blobs {
				d := math.Hypot(float64(px)-b.cx, float64(py)-b.cy)
				if d < b.r {
					c = mix(c, b.c, 0.6*(1-d/b.r))
				}
			}
			n := uint8(mrand.IntN(16))
			c.R, c.G, c.B = sat(c.R, n), sat(c.G, n), sat(c.B, n)
			bg.SetRGBA(px, py, c)
		}
	}
	// 拼图块：从背景截取形状内的像素，边缘加亮色描边便于看清
	piece := image.NewRGBA(image.Rect(0, 0, pieceSize, pieceSize))
	for py := 0; py < pieceSize; py++ {
		for px := 0; px < pieceSize; px++ {
			if !inPiece(px, py) {
				continue
			}
			c := bg.RGBAAt(x+px, y+py)
			if !inPiece(px-1, py) || !inPiece(px+1, py) || !inPiece(px, py-1) || !inPiece(px, py+1) {
				c = color.RGBA{255, 255, 255, 230}
			}
			piece.SetRGBA(px, py, c)
		}
	}
	// 缺口：把背景对应区域压暗
	for py := 0; py < pieceSize; py++ {
		for px := 0; px < pieceSize; px++ {
			if inPiece(px, py) {
				c := bg.RGBAAt(x+px, y+py)
				bg.SetRGBA(x+px, y+py, mix(c, color.RGBA{0, 0, 0, 255}, 0.55))
			}
		}
	}
	return bg, piece
}

func sat(v, n uint8) uint8 {
	if int(v)+int(n) > 255 {
		return 255
	}
	return v + n
}

func mix(a, b color.RGBA, t float64) color.RGBA {
	f := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }
	return color.RGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), 255}
}

// hsl 转 RGB；h 为 0～360，s、l 为 0～1。
func hsl(h, s, l float64) color.RGBA {
	c := (1 - math.Abs(2*l-1)) * s
	hp := h / 60
	x := c * (1 - math.Abs(math.Mod(hp, 2)-1))
	var r, g, b float64
	switch {
	case hp < 1:
		r, g = c, x
	case hp < 2:
		r, g = x, c
	case hp < 3:
		g, b = c, x
	case hp < 4:
		g, b = x, c
	case hp < 5:
		r, b = x, c
	default:
		r, b = c, x
	}
	m := l - c/2
	return color.RGBA{uint8((r + m) * 255), uint8((g + m) * 255), uint8((b + m) * 255), 255}
}

// handleCaptcha：GET /api/v1/auth/captcha，无需认证。生成一次登录验证码（设计 17.4）。
// 与登录共用 IP 限流，防止被用来消耗面板 CPU。
// 面板以 --no-login-captcha 启动时返回 404，页面据此不显示滑块。
func (s *Server) handleCaptcha(w http.ResponseWriter, r *http.Request) {
	if s.captcha == nil {
		s.writeError(w, r, errorf(CodeNotFound, "未启用登录验证码"))
		return
	}
	if ok, wait := s.loginLimit.allow(clientIP(r)); !ok {
		s.writeError(w, r, &APIError{Code: CodeRateLimited, Message: "请求过于频繁，请稍后再试", RetryAfter: wait})
		return
	}
	v, err := s.captcha.newChallenge(time.Now())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, v)
}
