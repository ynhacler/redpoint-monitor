package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

// points 生成从 now-span 到 now 每 5 分钟一个点；f 返回每个点的取值（i 从 0 开始）。
func points(now time.Time, span time.Duration, f func(i int, p *healthPoint)) []healthPoint {
	var out []healthPoint
	n := int(span / (5 * time.Minute))
	for i := 0; i <= n; i++ {
		p := healthPoint{TS: now.Add(-span + time.Duration(i)*5*time.Minute).Unix(), MemTotal: 1000, DiskTotal: 100e9}
		f(i, &p)
		out = append(out, p)
	}
	return out
}

func itemOf(v healthView, key string) *healthItem {
	for i := range v.Items {
		if v.Items[i].Key == key {
			return &v.Items[i]
		}
	}
	return nil
}

func TestHealthSummary(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	cycle := trafficView{CycleStart: "2026-10-01", CycleEnd: "2026-11-01", Used: 638e9, Limit: 1e12, Unit: "decimal",
		Forecast: &forecastView{Total: 891e9}}
	steady := points(now, 30*24*time.Hour, func(i int, p *healthPoint) {
		p.CPU, p.CPUMax = 12, 12
		if i%100 == 0 {
			p.CPUMax = 68
		}
		p.MemUsed, p.MemUsedMax = uint64(410+i%8*10), 480 // 41%～48%
		p.DiskUsed = uint64(66.8e9 + float64(i)*4.2e9/8640)
	})
	disks := []protocol.Disk{
		{Mount: "/", Device: "/dev/vda1", Total: 100e9, Used: 71e9, Available: 29e9},
		{Mount: "/srv", Device: "/dev/vda1", Total: 100e9, Used: 71e9, Available: 29e9}, // 同一设备只算一次
	}

	cases := []struct {
		name   string
		in     healthInput
		level  string
		status string
		want   map[string]string // key → 文本应包含的片段
		levels map[string]string
	}{
		{
			name:   "正常：设计 1.5.7 的示例",
			in:     healthInput{Status: "online", LastSeenAt: now.Unix(), Disks: disks, Traffic: cycle, Points: steady, Now: now},
			level:  "ok",
			status: "正常",
			want: map[string]string{
				"cpu":     "过去 24 小时平均 12%，峰值 68%",
				"memory":  "过去 24 小时稳定在 41%～48%",
				"disk":    "已使用 71%（71 GB / 100 GB），过去 30 天增长 4.2 GB",
				"traffic": "本周期已使用 638 GB / 1 TB（64%），预计周期结束 891 GB",
			},
			levels: map[string]string{"cpu": "ok", "memory": "ok", "disk": "ok", "traffic": "ok"},
		},
		{
			name: "需要关注：告警（已静音与离线告警不计）",
			in: healthInput{Status: "online", LastSeenAt: now.Unix(), Traffic: cycle, Now: now, Alerts: []alertBrief{
				{Type: "cpu", Severity: "warning", Value: 96.2},
				{Type: "memory", Severity: "warning", Value: 91, Silenced: true},
			}},
			level: "warn", status: "需要关注：CPU 96%",
		},
		{
			name: "异常：严重告警在前",
			in: healthInput{Status: "online", LastSeenAt: now.Unix(), Now: now, Alerts: []alertBrief{
				{Type: "cpu", Severity: "warning", Value: 92}, {Type: "disk", Severity: "critical", Value: 96},
			}},
			level: "bad", status: "异常：磁盘 96%、CPU 92%",
		},
		{
			name:  "离线：显示离线时长，磁盘取最后一个聚合点",
			in:    healthInput{Status: "offline", LastSeenAt: now.Add(-4 * time.Minute).Unix(), Points: steady, Now: now},
			level: "bad", status: "离线 4 分钟",
			want: map[string]string{"disk": "已使用 71%"},
		},
		{
			name:  "维护中优先于离线",
			in:    healthInput{Status: "offline", LastSeenAt: 1, Maintenance: true, Now: now},
			level: "muted", status: "维护中，不产生告警",
		},
		{
			name: "数据不足 24 小时：如实说明；内存波动大",
			in: healthInput{Status: "online", LastSeenAt: now.Unix(), Now: now, Points: points(now, 3*time.Hour, func(i int, p *healthPoint) {
				p.CPU, p.CPUMax = 95, 99
				p.MemUsed, p.MemUsedMax = uint64(300+i*15), 950
			})},
			level: "ok", status: "正常",
			want: map[string]string{
				"cpu":    "过去 3 小时平均 95%，峰值 99%",
				"memory": "在 30%～84% 之间波动，峰值 95%",
			},
			levels: map[string]string{"cpu": "bad", "memory": "warn"},
		},
		{
			name: "磁盘快速增长：估算写满天数并提示",
			in: healthInput{Status: "online", LastSeenAt: now.Unix(), Now: now,
				Disks:  []protocol.Disk{{Mount: "/", Total: 100e9, Used: 80e9, Available: 20e9}},
				Points: points(now, 10*24*time.Hour, func(i int, p *healthPoint) { p.DiskUsed = uint64(60e9 + float64(i)*20e9/2880) })},
			level: "ok", status: "正常",
			want:   map[string]string{"disk": "最近 10 天增长 20 GB，按此速度约 10 天后写满"},
			levels: map[string]string{"disk": "warn"},
		},
		{
			name:  "不满 7 天不计算增长；不限流量；超额预测",
			in:    healthInput{Status: "online", LastSeenAt: now.Unix(), Now: now, Disks: disks, Traffic: trafficView{CycleStart: "2026-10-01", Used: 5e9, Unit: "binary", Forecast: &forecastView{Total: 9e9, Over: true}}},
			level: "ok", status: "正常",
			want: map[string]string{"disk": "已使用 71%（71 GB / 100 GB）", "traffic": "本周期已使用 4.7 GiB（不限流量），预计周期结束 8.4 GiB，将超出额度"},
		},
		{
			name:  "待安装",
			in:    healthInput{Status: "pending", Now: now},
			level: "muted", status: "待安装",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := healthSummary(c.in)
			if v.Level != c.level || v.Status != c.status {
				t.Fatalf("结论 %s / %q，应为 %s / %q", v.Level, v.Status, c.level, c.status)
			}
			for k, frag := range c.want {
				it := itemOf(v, k)
				if it == nil || !strings.Contains(it.Text, frag) {
					t.Errorf("%s：%+v，应包含 %q", k, it, frag)
				}
			}
			for k, l := range c.levels {
				if it := itemOf(v, k); it == nil || it.Level != l {
					t.Errorf("%s 级别 %+v，应为 %s", k, it, l)
				}
			}
			if c.name == "不满 7 天不计算增长；不限流量；超额预测" && strings.Contains(itemOf(v, "disk").Text, "增长") {
				t.Error("不满 7 天不应计算增长")
			}
		})
	}
}

// 接口：范围内可读；App 设备与 API Key 范围外 404
func TestHealthAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, a, _ := createNode(t, h, admin, `{"name":"a","group":"jp"}`)
	_, b, _ := createNode(t, h, admin, `{"name":"b","group":"sg"}`)
	rec := do(h, "GET", "/api/v1/servers/"+itoa(a.ServerID)+"/health", admin, nil)
	var v healthView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != 200 || v.Status != "待安装" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	dev := pairTestDevice(t, s, false, "group", "jp").access
	if rec := do(h, "GET", "/api/v1/servers/"+itoa(a.ServerID)+"/health", dev, nil); rec.Code != 200 {
		t.Fatalf("范围内 %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/servers/"+itoa(b.ServerID)+"/health", dev, nil); rec.Code != 404 {
		t.Fatalf("范围外应为 404：%d", rec.Code)
	}
}
