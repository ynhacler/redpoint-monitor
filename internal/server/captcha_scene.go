package server

// 登录验证码的卡通背景（修订第 183 条）：每次随机生成一幅扁平配色、深色描边的卡通场景——
// 天空（白天 / 夕阳 / 夜晚 / 雪天）、太阳或月亮、云、层叠的山丘、树、小房子，以及地面上的小动物（猫、熊、小鸡、史莱姆）。
// 全部在服务端用标准库绘制，不下载、不内置任何图片（设计 1.8：不引入第三方资源）。
// 图形用有向距离（SDF）描述：内部为负；逐像素按距离做抗锯齿填充与描边。

import (
	"image"
	"image/color"
	"math"
	mrand "math/rand/v2"
)

// sdf 返回点到图形边界的有向距离（像素），内部为负。
type sdf func(x, y float64) float64

type sceneBox struct{ x0, y0, x1, y1 float64 } // 图形的大致范围，只在其中逐像素计算

var outlineInk = color.RGBA{43, 40, 58, 255}

// paint 用颜色 fill 填充图形；line > 0 时加深色描边（宽 line 像素）。
func paint(img *image.RGBA, f sdf, b sceneBox, fill color.RGBA, line float64) {
	r := img.Bounds()
	x0, y0 := max(r.Min.X, int(b.x0)-2), max(r.Min.Y, int(b.y0)-2)
	x1, y1 := min(r.Max.X, int(b.x1)+3), min(r.Max.Y, int(b.y1)+3)
	for py := y0; py < y1; py++ {
		for px := x0; px < x1; px++ {
			d := f(float64(px)+0.5, float64(py)+0.5)
			cover := clamp01(0.5 - d) // 抗锯齿：边界附近按距离过渡
			if cover <= 0 && (line <= 0 || d > line+0.5) {
				continue
			}
			c := img.RGBAAt(px, py)
			if cover > 0 {
				c = mix(c, fill, cover)
			}
			if line > 0 {
				if a := clamp01(line/2 + 0.5 - math.Abs(d+line/2)); a > 0 { // 描边在边界内侧
					c = mix(c, outlineInk, a)
				}
			}
			img.SetRGBA(px, py, c)
		}
	}
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func circle(cx, cy, r float64) sdf {
	return func(x, y float64) float64 { return math.Hypot(x-cx, y-cy) - r }
}

// ellipse 是近似的椭圆距离（按半径缩放后的圆），对卡通图形足够。
func ellipse(cx, cy, rx, ry float64) sdf {
	k := math.Min(rx, ry)
	return func(x, y float64) float64 { return (math.Hypot((x-cx)/rx, (y-cy)/ry) - 1) * k }
}

func roundRect(x0, y0, x1, y1, rad float64) sdf {
	cx, cy, hx, hy := (x0+x1)/2, (y0+y1)/2, (x1-x0)/2-rad, (y1-y0)/2-rad
	return func(x, y float64) float64 {
		dx, dy := math.Abs(x-cx)-hx, math.Abs(y-cy)-hy
		return math.Hypot(math.Max(dx, 0), math.Max(dy, 0)) + math.Min(math.Max(dx, dy), 0) - rad
	}
}

// triangle 的距离取三条边半平面距离的最大值（尖角处略有偏差，卡通图形可以接受）。
func triangle(ax, ay, bx, by, cx, cy float64) sdf {
	edge := func(px, py, qx, qy float64) func(x, y float64) float64 {
		nx, ny := qy-py, -(qx - px)
		l := math.Hypot(nx, ny)
		nx, ny = nx/l, ny/l
		// 法线朝外：第三个顶点应在内侧
		return func(x, y float64) float64 { return (x-px)*nx + (y-py)*ny }
	}
	e1, e2, e3 := edge(ax, ay, bx, by), edge(bx, by, cx, cy), edge(cx, cy, ax, ay)
	if e1(cx, cy) > 0 { // 顶点为顺时针：翻转
		e1, e2, e3 = edge(bx, by, ax, ay), edge(ax, ay, cx, cy), edge(cx, cy, bx, by)
	}
	return func(x, y float64) float64 { return math.Max(e1(x, y), math.Max(e2(x, y), e3(x, y))) }
}

func union(fs ...sdf) sdf {
	return func(x, y float64) float64 {
		d := math.Inf(1)
		for _, f := range fs {
			d = math.Min(d, f(x, y))
		}
		return d
	}
}

// hill 是起伏的地面：曲线以下为内部。
func hill(base, amp, freq, phase float64) sdf {
	return func(x, y float64) float64 { return (base - amp*math.Sin(x*freq+phase)) - y }
}

func rgb(hex uint32) color.RGBA {
	return color.RGBA{uint8(hex >> 16), uint8(hex >> 8), uint8(hex), 255}
}

type sceneTheme struct {
	skyTop, skyBottom color.RGBA
	hills             []color.RGBA // 由远到近
	night, snow       bool
	sun               color.RGBA
}

var sceneThemes = []sceneTheme{
	{skyTop: rgb(0x7ec8f5), skyBottom: rgb(0xd8f1ff), hills: []color.RGBA{rgb(0x9fd88a), rgb(0x6cc070), rgb(0x4aa65a)}, sun: rgb(0xffd34e)},              // 白天草地
	{skyTop: rgb(0xff9a6b), skyBottom: rgb(0xffe0a3), hills: []color.RGBA{rgb(0xc98bb0), rgb(0x9c6fa6), rgb(0x6f5a96)}, sun: rgb(0xfff1a8)},              // 夕阳
	{skyTop: rgb(0x1d2557), skyBottom: rgb(0x4a4b8f), hills: []color.RGBA{rgb(0x3b5f7a), rgb(0x2e4d66), rgb(0x223b52)}, night: true, sun: rgb(0xfff4c4)}, // 夜晚
	{skyTop: rgb(0xa9d6f2), skyBottom: rgb(0xeef7ff), hills: []color.RGBA{rgb(0xe3eef7), rgb(0xf4f9fd), rgb(0xffffff)}, snow: true, sun: rgb(0xffe27a)},  // 雪天
}

// drawScene 在 img 上绘制一幅随机卡通场景。
func drawScene(img *image.RGBA) {
	w, h := float64(img.Bounds().Dx()), float64(img.Bounds().Dy())
	th := sceneThemes[mrand.IntN(len(sceneThemes))]
	rf := func(a, b float64) float64 { return a + mrand.Float64()*(b-a) }

	// 天空渐变
	for py := 0; py < img.Bounds().Dy(); py++ {
		c := mix(th.skyTop, th.skyBottom, float64(py)/h)
		for px := 0; px < img.Bounds().Dx(); px++ {
			img.SetRGBA(px, py, c)
		}
	}
	if th.night { // 星星
		for range 24 {
			x, y := rf(0, w), rf(0, h*0.55)
			paint(img, circle(x, y, rf(0.6, 1.4)), sceneBox{x - 2, y - 2, x + 2, y + 2}, rgb(0xfff7d6), 0)
		}
	}
	// 太阳 / 月亮
	sx, sy, sr := rf(w*0.1, w*0.9), rf(h*0.15, h*0.35), rf(11, 16)
	paint(img, circle(sx, sy, sr+5), sceneBox{sx - sr - 6, sy - sr - 6, sx + sr + 6, sy + sr + 6}, mix(th.skyBottom, th.sun, 0.35), 0)
	paint(img, circle(sx, sy, sr), sceneBox{sx - sr, sy - sr, sx + sr, sy + sr}, th.sun, 1.4)
	if th.night { // 月牙：用天空色挖掉一部分
		paint(img, circle(sx+sr*0.45, sy-sr*0.25, sr*0.85), sceneBox{sx - sr, sy - sr*1.2, sx + sr*1.4, sy + sr}, mix(th.skyTop, th.skyBottom, sy/h), 0)
	}
	// 云
	for range 1 + mrand.IntN(3) {
		cx, cy, s := rf(0, w), rf(h*0.1, h*0.4), rf(7, 11)
		f := union(circle(cx, cy, s), circle(cx+s*1.1, cy+s*0.2, s*0.85), circle(cx-s*1.1, cy+s*0.25, s*0.8), circle(cx+s*0.3, cy-s*0.55, s*0.8))
		cloud := rgb(0xffffff)
		if th.night {
			cloud = rgb(0x8f93c7)
		}
		paint(img, f, sceneBox{cx - s*2.2, cy - s*1.5, cx + s*2.2, cy + s*1.3}, cloud, 1.2)
	}
	// 山丘：由远到近
	ground := 0.0
	for i, c := range th.hills {
		base := h*0.55 + float64(i)*h*0.12
		amp := rf(4, 10)
		paint(img, hill(base, amp, rf(0.015, 0.04), rf(0, 2*math.Pi)), sceneBox{0, base - amp - 1, w, h}, c, 1.2)
		ground = base
	}
	// 远处的房子与树（放在最后一层山丘之上）
	treeColor, trunk := rgb(0x3f9b55), rgb(0x8a5a3c)
	if th.snow {
		treeColor = rgb(0x5f9e8a)
	}
	for range 2 + mrand.IntN(3) {
		x, base := rf(8, w-8), ground-rf(14, 22)
		paint(img, roundRect(x-2, base-2, x+2, base+10, 1), sceneBox{x - 3, base - 3, x + 3, base + 11}, trunk, 1)
		if mrand.IntN(2) == 0 { // 圆树
			paint(img, union(circle(x, base-8, 9), circle(x-6, base-3, 6), circle(x+6, base-3, 6)), sceneBox{x - 13, base - 18, x + 13, base + 4}, treeColor, 1.2)
		} else { // 松树
			paint(img, union(triangle(x-11, base+2, x+11, base+2, x, base-14), triangle(x-8, base-6, x+8, base-6, x, base-20)),
				sceneBox{x - 12, base - 21, x + 12, base + 3}, treeColor, 1.2)
		}
	}
	if mrand.IntN(2) == 0 { // 小房子
		x, base := rf(20, w-40), ground-rf(10, 18)
		wall := []color.RGBA{rgb(0xfff2d9), rgb(0xffd6e0), rgb(0xd9ecff)}[mrand.IntN(3)]
		paint(img, roundRect(x, base-16, x+26, base+6, 1.5), sceneBox{x, base - 16, x + 26, base + 6}, wall, 1.2)
		paint(img, triangle(x-4, base-15, x+30, base-15, x+13, base-30), sceneBox{x - 5, base - 31, x + 31, base - 14}, rgb(0xe0594f), 1.2)
		paint(img, roundRect(x+4, base-10, x+11, base-3, 1), sceneBox{x + 4, base - 10, x + 11, base - 3}, rgb(0xffe08a), 1)
		paint(img, roundRect(x+15, base-8, x+22, base+6, 2), sceneBox{x + 15, base - 8, x + 22, base + 6}, rgb(0x9a6a4a), 1)
	}
	if th.snow { // 雪花
		for range 30 {
			x, y := rf(0, w), rf(0, h)
			paint(img, circle(x, y, rf(0.7, 1.5)), sceneBox{x - 2, y - 2, x + 2, y + 2}, rgb(0xffffff), 0)
		}
	} else { // 小花
		for range 6 {
			x, y := rf(0, w), rf(ground+4, h-3)
			paint(img, circle(x, y, 2), sceneBox{x - 3, y - 3, x + 3, y + 3}, []color.RGBA{rgb(0xffffff), rgb(0xffd34e), rgb(0xff8fb1)}[mrand.IntN(3)], 0.8)
		}
	}
	// 地面上的小动物
	for range 1 + mrand.IntN(2) {
		drawCritter(img, mrand.IntN(4), rf(28, w-28), rf(h*0.70, h*0.80), rf(1.15, 1.45))
	}
}

// drawCritter 在 (x, y)（身体中心）画一个小动物：0 猫、1 熊、2 小鸡、3 史莱姆；s 为缩放。
func drawCritter(img *image.RGBA, kind int, x, y, s float64) {
	box := sceneBox{x - 22*s, y - 24*s, x + 22*s, y + 16*s}
	eye := func(ex, ey float64) {
		paint(img, circle(ex, ey, 1.8*s), sceneBox{ex - 3, ey - 3, ex + 3, ey + 3}, outlineInk, 0)
		paint(img, circle(ex+0.6*s, ey-0.6*s, 0.6*s), sceneBox{ex - 1, ey - 2, ex + 2, ey + 1}, rgb(0xffffff), 0)
	}
	blush := func(bx, by float64) {
		paint(img, ellipse(bx, by, 2.6*s, 1.6*s), sceneBox{bx - 4, by - 3, bx + 4, by + 3}, rgb(0xff9fb4), 0)
	}
	// 身体下方的影子
	paint(img, ellipse(x, y+13*s, 14*s, 2.5*s), sceneBox{x - 15*s, y + 10*s, x + 15*s, y + 16*s}, mix(img.RGBAAt(int(x), int(min(y+13*s, float64(img.Bounds().Dy()-1)))), outlineInk, 0.25), 0)
	switch kind {
	case 0: // 猫
		fur := []color.RGBA{rgb(0xffb35c), rgb(0xd9d9e3), rgb(0x5a5a6e)}[mrand.IntN(3)]
		paint(img, union(ellipse(x, y+3*s, 12*s, 10*s), triangle(x-11*s, y-4*s, x-3*s, y-8*s, x-10*s, y-18*s),
			triangle(x+11*s, y-4*s, x+3*s, y-8*s, x+10*s, y-18*s)), box, fur, 1.4)
		eye(x-4.5*s, y+1*s)
		eye(x+4.5*s, y+1*s)
		blush(x-8*s, y+5*s)
		blush(x+8*s, y+5*s)
		paint(img, triangle(x-1.5*s, y+4*s, x+1.5*s, y+4*s, x, y+6*s), sceneBox{x - 3, y + 3, x + 3, y + 8}, rgb(0xff6f8e), 0)
	case 1: // 熊
		fur := []color.RGBA{rgb(0xa86b4a), rgb(0xf3e7d3), rgb(0x6b4a3a)}[mrand.IntN(3)]
		paint(img, union(circle(x-9*s, y-8*s, 4.5*s), circle(x+9*s, y-8*s, 4.5*s), ellipse(x, y+3*s, 12.5*s, 10.5*s)), box, fur, 1.4)
		paint(img, ellipse(x, y+6*s, 5*s, 3.6*s), sceneBox{x - 6*s, y + 2*s, x + 6*s, y + 10*s}, rgb(0xfff3e3), 1)
		paint(img, ellipse(x, y+4.6*s, 1.8*s, 1.2*s), sceneBox{x - 3, y + 3, x + 3, y + 7}, outlineInk, 0)
		eye(x-5*s, y)
		eye(x+5*s, y)
		blush(x-8.5*s, y+5*s)
		blush(x+8.5*s, y+5*s)
	case 2: // 小鸡
		paint(img, circle(x, y+2*s, 11*s), box, rgb(0xffe066), 1.4)
		paint(img, triangle(x-1*s, y-8*s, x+3*s, y-9*s, x+1*s, y-14*s), sceneBox{x - 2*s, y - 15*s, x + 4*s, y - 7*s}, rgb(0xffe066), 1)
		paint(img, triangle(x-3*s, y+2*s, x+3*s, y+2*s, x, y+6*s), sceneBox{x - 4*s, y + 1*s, x + 4*s, y + 7*s}, rgb(0xff9f43), 1)
		eye(x-4*s, y-1*s)
		eye(x+4*s, y-1*s)
		blush(x-7.5*s, y+4*s)
		blush(x+7.5*s, y+4*s)
	default: // 史莱姆
		goo := []color.RGBA{rgb(0x7ad7c1), rgb(0x9ab8ff), rgb(0xff9fc9)}[mrand.IntN(3)]
		paint(img, union(ellipse(x, y+5*s, 14*s, 8*s), circle(x, y-1*s, 9*s)), box, goo, 1.4)
		paint(img, ellipse(x-5*s, y-4*s, 2.4*s, 1.5*s), sceneBox{x - 8*s, y - 6*s, x - 2*s, y - 2*s}, rgb(0xffffff), 0)
		eye(x-4*s, y+2*s)
		eye(x+4*s, y+2*s)
		blush(x-8*s, y+6*s)
		blush(x+8*s, y+6*s)
	}
}
