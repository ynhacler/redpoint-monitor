package server

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTrafficAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, n, _ := createNode(t, h, admin, `{"name":"hk-1","traffic_limit_gb":1000,"traffic_unit":"binary",
		"traffic_factor":1.5,"traffic_count_mode":"tx"}`)
	base := "/api/v1/servers/" + itoa(n.ServerID)

	row, err := s.store.GetServer(n.ServerID)
	if err != nil || row.TrafficUnit != UnitBinary || row.TrafficFactor != 1.5 || row.LimitBytes != 1000<<30 {
		t.Fatalf("单位、系数、额度（二进制口径）未正确保存：%+v %v", row, err)
	}
	today := time.Now().Format("2006-01-02")
	if _, err := s.store.DB.Exec(`INSERT INTO traffic_daily (server_id, day, rx, tx) VALUES (?,?,?,?)`,
		n.ServerID, today, 5000, 1000); err != nil {
		t.Fatal(err)
	}

	var cur trafficView
	rec := do(h, "GET", base+"/traffic/current", admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &cur)
	if rec.Code != 200 || cur.Measured != 1500 || cur.Used != 1500 || cur.Adjustment != 0 || cur.CycleEnd == "" {
		t.Fatalf("统计值应为 tx × 系数 = 1500：%d %s", rec.Code, rec.Body)
	}

	// 校准：服务商显示 1 GiB → 偏差 = 2^30 − 1500
	rec = do(h, "POST", base+"/traffic/calibrate", admin, []byte(`{"used_gb":1,"note":"面板显示"}`))
	json.Unmarshal(rec.Body.Bytes(), &cur)
	if rec.Code != 200 || cur.Used != 1<<30 || cur.Adjustment != 1<<30-1500 || cur.CalibratedAt == 0 {
		t.Fatalf("校准后展示值应等于填写值：%d %s", rec.Code, rec.Body)
	}
	// 再次校准覆盖而不是累加
	do(h, "POST", base+"/traffic/calibrate", admin, []byte(`{"used_gb":2}`))
	rec = do(h, "GET", base, admin, nil)
	var v serverView
	json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Traffic.Used != 2<<30 {
		t.Errorf("第二次校准应覆盖第一次：used=%d", v.Traffic.Used)
	}

	// 校准后修改系数或计费模式：校准那一刻仍等于服务商数值，不把系数与偏差重复叠加（设计 5.7）
	for _, set := range []string{`traffic_factor = 1.0`, `traffic_count_mode = 'sum'`} {
		if _, err := s.store.DB.Exec(`UPDATE servers SET `+set+` WHERE id = ?`, n.ServerID); err != nil {
			t.Fatal(err)
		}
		json.Unmarshal(do(h, "GET", base+"/traffic/current", admin, nil).Body.Bytes(), &cur)
		if cur.Used != 2<<30 {
			t.Errorf("%s 后已用量应仍为校准值 2 GiB：%+v", set, cur)
		}
	}

	var list struct{ Items []Adjustment }
	json.Unmarshal(do(h, "GET", base+"/traffic/adjustments", admin, nil).Body.Bytes(), &list)
	if len(list.Items) != 2 || list.Items[0].Reported != 2<<30 || list.Items[1].Note != "面板显示" ||
		list.Items[0].RawRx == nil || *list.Items[0].RawRx != 5000 || *list.Items[0].RawTx != 1000 {
		t.Errorf("校准历史应有两条且最新在前：%+v", list.Items)
	}

	var daily struct{ Items []dailyItem }
	rec = do(h, "GET", base+"/traffic/daily?days=7", admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &daily)
	if rec.Code != 200 || len(daily.Items) != 7 || daily.Items[6].Day != today || daily.Items[6].Used != 6000 || daily.Items[0].Used != 0 {
		t.Errorf("每日流量应补齐 7 天、今天在最后：%s", rec.Body)
	}

	var monthly struct{ Items []cycleItem }
	rec = do(h, "GET", base+"/traffic/monthly?cycles=3", admin, nil)
	json.Unmarshal(rec.Body.Bytes(), &monthly)
	if rec.Code != 200 || len(monthly.Items) != 3 || monthly.Items[0].Used != 2<<30 || monthly.Items[1].Used != 0 ||
		monthly.Items[1].CycleEnd != monthly.Items[0].CycleStart {
		t.Errorf("按周期汇总：当前周期含校准、周期首尾相接：%s", rec.Body)
	}

	cases := []struct {
		name, method, path, body string
		status                   int
	}{
		{"缺少 used_gb", "POST", base + "/traffic/calibrate", `{}`, 422},
		{"负数", "POST", base + "/traffic/calibrate", `{"used_gb":-1}`, 422},
		{"未知字段", "POST", base + "/traffic/calibrate", `{"used_gb":1,"x":1}`, 400},
		{"节点不存在", "POST", "/api/v1/servers/999/traffic/calibrate", `{"used_gb":1}`, 404},
		{"days 超范围", "GET", base + "/traffic/daily?days=0", "", 422},
		{"cycles 非数字", "GET", base + "/traffic/monthly?cycles=a", "", 422},
		{"系数超范围", "POST", "/api/v1/servers", `{"name":"x","traffic_factor":3}`, 422},
		{"单位错误", "POST", "/api/v1/servers", `{"name":"x","traffic_unit":"tb"}`, 422},
	}
	for _, c := range cases {
		var body []byte
		if c.body != "" {
			body = []byte(c.body)
		}
		if rec := do(h, c.method, c.path, admin, body); rec.Code != c.status {
			t.Errorf("%s：%d，应为 %d；%s", c.name, rec.Code, c.status, rec.Body)
		}
	}
}

// 多次校准比例稳定时建议系数；迁移 17 之前没有原始字节的记录沿用固定偏差（设计 5.7）。
func TestTrafficFactorSuggestionAPI(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, n, _ := createNode(t, h, admin, `{"name":"hk-2"}`)
	base := "/api/v1/servers/" + itoa(n.ServerID)
	cycle := CycleStart(time.Now(), 1).Format("2006-01-02")
	now := time.Now().Unix()
	day := int64(24 * 3600)
	gb := int64(1e9)
	// 统计值偏低 3%：三次校准分别相隔 5 天
	for i, g := range []int64{100, 200, 300} {
		raw := g * gb
		if _, err := s.store.AddAdjustment(n.ServerID, Adjustment{CycleStart: "2000-01-01", Measured: raw,
			Reported: raw * 103 / 100, RawRx: &raw, RawTx: new(int64), CreatedAt: now - int64(2-i)*5*day}); err != nil {
			t.Fatal(err)
		}
	}
	var cur trafficView
	json.Unmarshal(do(h, "GET", base+"/traffic/current", admin, nil).Body.Bytes(), &cur)
	if cur.FactorSuggestion != 1.03 {
		t.Errorf("应建议系数 1.03：%+v", cur)
	}

	// 旧记录：没有原始字节，本周期偏差按记录的固定值
	if _, err := s.store.DB.Exec(`INSERT INTO traffic_adjustments (server_id, cycle_start, measured_bytes, reported_bytes,
		adjustment_bytes, created_at) VALUES (?,?,0,700,700,?)`, n.ServerID, cycle, now); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(do(h, "GET", base+"/traffic/current", admin, nil).Body.Bytes(), &cur)
	if cur.Adjustment != 700 || cur.Used != 700 {
		t.Errorf("旧校准记录应沿用固定偏差：%+v", cur)
	}
}
