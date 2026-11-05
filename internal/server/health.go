package server

// 节点健康摘要（设计 1.5.7）：把最近的指标归纳成几句话，避免用户逐张图判断。
// 在面板计算，Web 与 App 显示同一结论；healthSummary 是纯函数，表驱动测试见 health_test.go。

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"vpsmon/internal/protocol"
)

// healthPoint 是 5 分钟聚合的一个桶（设计 21）。
type healthPoint struct {
	TS                            int64
	CPU, CPUMax                   float64
	MemUsed, MemUsedMax, MemTotal uint64
	DiskUsed, DiskTotal           uint64
}

type healthInput struct {
	Status      string // online / unknown / offline / pending
	LastSeenAt  int64
	Maintenance bool
	Alerts      []alertBrief
	Disks       []protocol.Disk // 最近一次上报；离线时可能为空
	Traffic     trafficView
	Points      []healthPoint // 最近 30 天，按时间升序
	Now         time.Time
	Lang        healthLang // 空为中文（App 与旧客户端）；en 为英文（设计 52.2）
}

// healthLang 是健康摘要的语言。文字以中文为源，f 按格式串查 healthEN，和 Web 的 t() 一样没有译文时用中文。
type healthLang string

func (l healthLang) f(format string, a ...any) string {
	if l == "en" {
		if e, ok := healthEN[format]; ok {
			format = e
		}
	}
	return fmt.Sprintf(format, a...)
}

// healthEN 是健康摘要的英文译文（设计 52.2），键为中文格式串，%s、%d 的个数与顺序要一致。
var healthEN = map[string]string{
	"%d 秒": "%d s", "%d 分钟": "%d min", "%d 小时": "%d h", "%d 天": "%d d",
	"内存 %s": "Memory %s", "磁盘 %s": "Disk %s", "负载 %.1f×": "Load %.1f×", "流量 %s": "Traffic %s",
	"流量预计超额": "Traffic forecast over limit", "时钟偏差 %.0f 秒": "Clock skew %.0f s",
	"过去 24 小时": "Past 24 hours", "过去 30 天": "Past 30 days", "最近 %d 天": "Last %d days", "过去 %d 小时": "Past %d h",
	"正常": "Normal", "待安装": "Not installed", "维护中，不产生告警": "In maintenance, no alerts", "尚未上报": "Never reported",
	"离线 %s": "Offline for %s", "异常：%s": "Problem: %s", "需要关注：%s": "Needs attention: %s", "上报延迟": "Report delayed",
	"、": ", ", "内存": "Memory", "磁盘": "Disk", "流量": "Traffic",
	"%s平均 %s，峰值 %s": "%s: average %s, peak %s", "%s稳定在 %s": "%s: steady at %s", "%s稳定在 %s～%s": "%s: steady at %s–%s",
	"%s在 %s～%s 之间波动，峰值 %s": "%s: ranged %s–%s, peak %s",
	"已使用 %s（%s / %s）":      "%s used (%s / %s)", "，%s基本没有变化": ", %s: almost no change", "，%s减少 %s": ", %s: down %s",
	"，%s增长 %s": ", %s: up %s", "，按此速度约 %.0f 天后写满": ", full in about %.0f days at this rate",
	"本周期已使用 %s / %s（%s）": "This cycle: %s / %s used (%s)", "本周期已使用 %s（不限流量）": "This cycle: %s used (unlimited)",
	"，预计周期结束 %s": ", forecast %s by cycle end", "，将超出额度": ", over the allowance",
}

type healthItem struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Text  string `json:"text"`
	Level string `json:"level"`
}

type healthView struct {
	Level  string       `json:"level"`
	Status string       `json:"status"`
	Items  []healthItem `json:"items"`
}

// 与 Web 的配色阈值一致（web/src/metrics.ts，设计 16.1）
func levelOf(v, warn, bad float64) string {
	switch {
	case v >= bad:
		return "bad"
	case v >= warn:
		return "warn"
	}
	return "ok"
}

func worse(a, b string) string {
	rank := map[string]int{"ok": 0, "warn": 1, "bad": 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func pct(n float64) string {
	if n > 0 && n < 1 {
		return "<1%"
	}
	return fmt.Sprintf("%.0f%%", math.Round(n))
}

// fmtTrafficUnit 按节点的流量单位显示（设计 5.8）：decimal 用 GB，binary 用 GiB；与 web/src/format.ts 一致。
func fmtTrafficUnit(n int64, unit string) string {
	if unit != "binary" {
		return fmtBytesSI(n)
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v < 10 && i > 0 && math.Round(v*10) != math.Round(v)*10 {
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.0f %s", v, units[i])
}

func fmtDuration(sec int64, l healthLang) string {
	switch {
	case sec < 60:
		return l.f("%d 秒", sec)
	case sec < 3600:
		return l.f("%d 分钟", sec/60)
	case sec < 86400:
		return l.f("%d 小时", sec/3600)
	}
	return l.f("%d 天", sec/86400)
}

// alertLabel 与 web/src/metrics.ts 的 alertLabel 一致。
func alertLabel(a alertBrief, l healthLang) string {
	p := pct(a.Value)
	switch a.Type {
	case "cpu":
		return "CPU " + p
	case "memory":
		return l.f("内存 %s", p)
	case "disk":
		return l.f("磁盘 %s", p)
	case "swap":
		return "Swap " + p
	case "load":
		return l.f("负载 %.1f×", a.Value)
	case "traffic":
		return l.f("流量 %s", p)
	case "traffic_forecast":
		return l.f("流量预计超额")
	case "agent_clock":
		return l.f("时钟偏差 %.0f 秒", a.Value)
	}
	return a.Message
}

// windowText：数据不满窗口时如实说明覆盖的时长，例如“过去 3 小时”。
func windowText(first, last int64, full time.Duration, unit string, l healthLang) string {
	span := time.Duration(last-first)*time.Second + 5*time.Minute
	if span >= full-time.Hour {
		return l.f(map[string]string{"h": "过去 24 小时", "d": "过去 30 天"}[unit])
	}
	if unit == "d" && span >= 24*time.Hour {
		return l.f("最近 %d 天", int(span.Hours()/24))
	}
	return l.f("过去 %d 小时", max(1, int(math.Round(span.Hours()))))
}

func healthSummary(in healthInput) healthView {
	l := in.Lang
	v := healthView{Level: "ok", Status: l.f("正常"), Items: []healthItem{}}

	// ---- 总体结论 ----
	var bad, warn []string
	for _, a := range in.Alerts {
		if a.Type == "offline" || a.Silenced {
			continue
		}
		if a.Severity == "critical" {
			bad = append(bad, alertLabel(a, l))
		} else {
			warn = append(warn, alertLabel(a, l))
		}
	}
	switch {
	case in.Status == "pending":
		v.Level, v.Status = "muted", l.f("待安装")
	case in.Maintenance:
		v.Level, v.Status = "muted", l.f("维护中，不产生告警")
	case in.Status == "offline" && in.LastSeenAt == 0:
		v.Level, v.Status = "bad", l.f("尚未上报")
	case in.Status == "offline":
		v.Level, v.Status = "bad", l.f("离线 %s", fmtDuration(in.Now.Unix()-in.LastSeenAt, l))
	case len(bad) > 0:
		v.Level, v.Status = "bad", l.f("异常：%s", strings.Join(append(bad, warn...), l.f("、")))
	case len(warn) > 0:
		v.Level, v.Status = "warn", l.f("需要关注：%s", strings.Join(warn, l.f("、")))
	case in.Status == "unknown":
		v.Level, v.Status = "warn", l.f("上报延迟")
	}

	// ---- CPU 与内存：过去 24 小时 ----
	since := in.Now.Add(-24 * time.Hour).Unix()
	var day []healthPoint
	for _, p := range in.Points {
		if p.TS >= since {
			day = append(day, p)
		}
	}
	if len(day) > 0 {
		w := windowText(day[0].TS, day[len(day)-1].TS, 24*time.Hour, "h", l)
		var sum, peak float64
		memMin, memMax, memPeak := math.Inf(1), math.Inf(-1), 0.0
		memN := 0
		for _, p := range day {
			sum += p.CPU
			peak = math.Max(peak, p.CPUMax)
			if p.MemTotal > 0 {
				m := float64(p.MemUsed) / float64(p.MemTotal) * 100
				memMin, memMax = math.Min(memMin, m), math.Max(memMax, m)
				memPeak = math.Max(memPeak, float64(p.MemUsedMax)/float64(p.MemTotal)*100)
				memN++
			}
		}
		avg := sum / float64(len(day))
		v.Items = append(v.Items, healthItem{Key: "cpu", Title: "CPU", Level: levelOf(avg, 70, 90),
			Text: l.f("%s平均 %s，峰值 %s", w, pct(avg), pct(peak))})
		if memN > 0 {
			var text string
			switch {
			case math.Round(memMin) == math.Round(memMax):
				text = l.f("%s稳定在 %s", w, pct(memMax))
			case memMax-memMin <= 10:
				text = l.f("%s稳定在 %s～%s", w, pct(memMin), pct(memMax))
			default:
				text = l.f("%s在 %s～%s 之间波动，峰值 %s", w, pct(memMin), pct(memMax), pct(memPeak))
			}
			v.Items = append(v.Items, healthItem{Key: "memory", Title: l.f("内存"), Level: levelOf(memMax, 75, 90), Text: text})
		}
	}

	// ---- 磁盘：当前使用率与最近 30 天增长 ----
	var used, total, avail uint64
	hasAvail := true
	seen := map[string]bool{}
	for _, d := range in.Disks {
		key := d.Device
		if key == "" {
			key = d.Mount
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		used, total = used+d.Used, total+d.Total
		if d.Available == 0 {
			hasAvail = false
		}
		avail += d.Available
	}
	if total == 0 && len(in.Points) > 0 { // 离线：用最后一个聚合点
		last := in.Points[len(in.Points)-1]
		used, total, hasAvail = last.DiskUsed, last.DiskTotal, false
	}
	if total > 0 {
		denom := total
		if hasAvail {
			denom = used + avail
		}
		usage := float64(used) / float64(denom) * 100
		level := levelOf(usage, 85, 95)
		text := l.f("已使用 %s（%s / %s）", pct(usage), fmtBytesSI(int64(used)), fmtBytesSI(int64(total)))
		if n := len(in.Points); n > 1 {
			first, last := in.Points[0], in.Points[n-1]
			spanDays := float64(last.TS-first.TS) / 86400
			if spanDays >= 7 && first.DiskTotal > 0 {
				w := windowText(first.TS, last.TS, 30*24*time.Hour, "d", l)
				growth := int64(last.DiskUsed) - int64(first.DiskUsed)
				switch {
				case math.Abs(float64(growth)) < float64(total)*0.001:
					text += l.f("，%s基本没有变化", w)
				case growth < 0:
					text += l.f("，%s减少 %s", w, fmtBytesSI(-growth))
				default:
					text += l.f("，%s增长 %s", w, fmtBytesSI(growth))
					free := float64(denom) - float64(used)
					if days := free / (float64(growth) / spanDays); days < 365 {
						text += l.f("，按此速度约 %.0f 天后写满", math.Max(1, math.Floor(days)))
						if days < 30 {
							level = worse(level, "warn")
						}
					}
				}
			}
		}
		v.Items = append(v.Items, healthItem{Key: "disk", Title: l.f("磁盘"), Level: level, Text: text})
	}

	// ---- 流量：本周期 ----
	t := in.Traffic
	if t.CycleStart != "" {
		item := healthItem{Key: "traffic", Title: l.f("流量"), Level: "ok"}
		if t.Limit > 0 {
			p := float64(t.Used) / float64(t.Limit) * 100
			item.Text = l.f("本周期已使用 %s / %s（%s）", fmtTrafficUnit(int64(t.Used), t.Unit), fmtTrafficUnit(t.Limit, t.Unit), pct(p))
			item.Level = levelOf(p, 80, 95)
		} else {
			item.Text = l.f("本周期已使用 %s（不限流量）", fmtTrafficUnit(int64(t.Used), t.Unit))
		}
		if f := t.Forecast; f != nil {
			item.Text += l.f("，预计周期结束 %s", fmtTrafficUnit(int64(f.Total), t.Unit))
			if f.Over {
				item.Text += l.f("，将超出额度")
				item.Level = worse(item.Level, "warn")
			}
		}
		v.Items = append(v.Items, item)
	}
	return v
}

// HealthPoints 读取最近 30 天的 5 分钟聚合（设计 21），按时间升序。
func (s *Store) HealthPoints(serverID int64, since time.Time) ([]healthPoint, error) {
	rows, err := s.DB.Query(`SELECT ts, COALESCE(cpu, 0), COALESCE(cpu_max, 0), COALESCE(mem_used, 0), COALESCE(mem_used_max, 0),
		COALESCE(mem_total, 0), COALESCE(disk_used, 0), COALESCE(disk_total, 0)
		FROM metrics_5m WHERE server_id = ? AND ts >= ? ORDER BY ts`, serverID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []healthPoint
	for rows.Next() {
		var p healthPoint
		if err := rows.Scan(&p.TS, &p.CPU, &p.CPUMax, &p.MemUsed, &p.MemUsedMax, &p.MemTotal, &p.DiskUsed, &p.DiskTotal); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// handleHealth：GET /api/v1/servers/{id}/health，read（Web、API Key、App 设备，范围外 404）。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	row, err := s.store.GetServer(id)
	if errors.Is(err, errNoServer) || (err == nil && !scopeAllows(r, *row)) { // 范围外按不存在处理（设计 17.3）
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	now := time.Now()
	v, err := s.viewOf(*row, now)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	pts, err := s.store.HealthPoints(id, now.Add(-30*24*time.Hour))
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	in := healthInput{Status: v.Status, LastSeenAt: v.LastSeenAt, Maintenance: v.Maintenance != nil, Alerts: v.Alerts,
		Traffic: v.Traffic, Points: pts, Now: now}
	if r.URL.Query().Get("lang") == "en" { // 其他值按中文，不报错
		in.Lang = "en"
	}
	if v.Latest != nil {
		in.Disks = v.Latest.Disk
	}
	writeJSON(w, healthSummary(in))
}
