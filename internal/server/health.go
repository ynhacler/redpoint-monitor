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

func fmtDurationZh(sec int64) string {
	switch {
	case sec < 60:
		return fmt.Sprintf("%d 秒", sec)
	case sec < 3600:
		return fmt.Sprintf("%d 分钟", sec/60)
	case sec < 86400:
		return fmt.Sprintf("%d 小时", sec/3600)
	}
	return fmt.Sprintf("%d 天", sec/86400)
}

// alertLabel 与 web/src/metrics.ts 的 alertLabel 一致。
func alertLabel(a alertBrief) string {
	p := pct(a.Value)
	switch a.Type {
	case "cpu":
		return "CPU " + p
	case "memory":
		return "内存 " + p
	case "disk":
		return "磁盘 " + p
	case "swap":
		return "Swap " + p
	case "load":
		return fmt.Sprintf("负载 %.1f×", a.Value)
	case "traffic":
		return "流量 " + p
	case "traffic_forecast":
		return "流量预计超额"
	case "agent_clock":
		return fmt.Sprintf("时钟偏差 %.0f 秒", a.Value)
	}
	return a.Message
}

// windowText：数据不满窗口时如实说明覆盖的时长，例如“过去 3 小时”。
func windowText(first, last int64, full time.Duration, unit string) string {
	span := time.Duration(last-first)*time.Second + 5*time.Minute
	if span >= full-time.Hour {
		return "过去 " + map[string]string{"h": "24 小时", "d": "30 天"}[unit]
	}
	if unit == "d" && span >= 24*time.Hour {
		return fmt.Sprintf("最近 %d 天", int(span.Hours()/24))
	}
	return fmt.Sprintf("过去 %d 小时", max(1, int(math.Round(span.Hours()))))
}

func healthSummary(in healthInput) healthView {
	v := healthView{Level: "ok", Status: "正常", Items: []healthItem{}}

	// ---- 总体结论 ----
	var bad, warn []string
	for _, a := range in.Alerts {
		if a.Type == "offline" || a.Silenced {
			continue
		}
		if a.Severity == "critical" {
			bad = append(bad, alertLabel(a))
		} else {
			warn = append(warn, alertLabel(a))
		}
	}
	switch {
	case in.Status == "pending":
		v.Level, v.Status = "muted", "待安装"
	case in.Maintenance:
		v.Level, v.Status = "muted", "维护中，不产生告警"
	case in.Status == "offline" && in.LastSeenAt == 0:
		v.Level, v.Status = "bad", "尚未上报"
	case in.Status == "offline":
		v.Level, v.Status = "bad", "离线 "+fmtDurationZh(in.Now.Unix()-in.LastSeenAt)
	case len(bad) > 0:
		v.Level, v.Status = "bad", "异常："+strings.Join(append(bad, warn...), "、")
	case len(warn) > 0:
		v.Level, v.Status = "warn", "需要关注："+strings.Join(warn, "、")
	case in.Status == "unknown":
		v.Level, v.Status = "warn", "上报延迟"
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
		w := windowText(day[0].TS, day[len(day)-1].TS, 24*time.Hour, "h")
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
			Text: fmt.Sprintf("%s平均 %s，峰值 %s", w, pct(avg), pct(peak))})
		if memN > 0 {
			var text string
			switch {
			case math.Round(memMin) == math.Round(memMax):
				text = fmt.Sprintf("%s稳定在 %s", w, pct(memMax))
			case memMax-memMin <= 10:
				text = fmt.Sprintf("%s稳定在 %s～%s", w, pct(memMin), pct(memMax))
			default:
				text = fmt.Sprintf("%s在 %s～%s 之间波动，峰值 %s", w, pct(memMin), pct(memMax), pct(memPeak))
			}
			v.Items = append(v.Items, healthItem{Key: "memory", Title: "内存", Level: levelOf(memMax, 75, 90), Text: text})
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
		text := fmt.Sprintf("已使用 %s（%s / %s）", pct(usage), fmtBytesSI(int64(used)), fmtBytesSI(int64(total)))
		if n := len(in.Points); n > 1 {
			first, last := in.Points[0], in.Points[n-1]
			spanDays := float64(last.TS-first.TS) / 86400
			if spanDays >= 7 && first.DiskTotal > 0 {
				w := windowText(first.TS, last.TS, 30*24*time.Hour, "d")
				growth := int64(last.DiskUsed) - int64(first.DiskUsed)
				switch {
				case math.Abs(float64(growth)) < float64(total)*0.001:
					text += "，" + w + "基本没有变化"
				case growth < 0:
					text += fmt.Sprintf("，%s减少 %s", w, fmtBytesSI(-growth))
				default:
					text += fmt.Sprintf("，%s增长 %s", w, fmtBytesSI(growth))
					free := float64(denom) - float64(used)
					if days := free / (float64(growth) / spanDays); days < 365 {
						text += fmt.Sprintf("，按此速度约 %.0f 天后写满", math.Max(1, math.Floor(days)))
						if days < 30 {
							level = worse(level, "warn")
						}
					}
				}
			}
		}
		v.Items = append(v.Items, healthItem{Key: "disk", Title: "磁盘", Level: level, Text: text})
	}

	// ---- 流量：本周期 ----
	t := in.Traffic
	if t.CycleStart != "" {
		item := healthItem{Key: "traffic", Title: "流量", Level: "ok"}
		if t.Limit > 0 {
			p := float64(t.Used) / float64(t.Limit) * 100
			item.Text = fmt.Sprintf("本周期已使用 %s / %s（%s）", fmtTrafficUnit(int64(t.Used), t.Unit), fmtTrafficUnit(t.Limit, t.Unit), pct(p))
			item.Level = levelOf(p, 80, 95)
		} else {
			item.Text = fmt.Sprintf("本周期已使用 %s（不限流量）", fmtTrafficUnit(int64(t.Used), t.Unit))
		}
		if f := t.Forecast; f != nil {
			item.Text += "，预计周期结束 " + fmtTrafficUnit(int64(f.Total), t.Unit)
			if f.Over {
				item.Text += "，将超出额度"
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
	if v.Latest != nil {
		in.Disks = v.Latest.Disk
	}
	writeJSON(w, healthSummary(in))
}
