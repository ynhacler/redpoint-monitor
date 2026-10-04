package server

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTrafficCache(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, n, _ := createNode(t, h, admin, `{"name":"tc-1"}`)
	row, _ := s.store.GetServer(n.ServerID)
	now := time.Now()
	day := now.Format("2006-01-02")
	s.store.DB.Exec(`INSERT INTO traffic_daily (server_id, day, rx, tx) VALUES (?,?,1000,0)`, n.ServerID, day)

	v1, _ := s.trafficCached(*row, now)
	s.store.DB.Exec(`UPDATE traffic_daily SET rx = 5000 WHERE server_id = ?`, n.ServerID)
	if v, _ := s.trafficCached(*row, now.Add(5*time.Second)); v.Used != v1.Used || v.Used != 1000 {
		t.Errorf("10 秒内应复用缓存：%d", v.Used)
	}
	if v, _ := s.trafficCached(*row, now.Add(trafficTTL)); v.Used != 5000 {
		t.Errorf("过期后应重新计算：%d", v.Used)
	}
	row.CountMode = ModeTx // 修改计费设置：缓存键变化，立即按新口径计算
	if v, _ := s.trafficCached(*row, now.Add(trafficTTL+time.Second)); v.Used != 0 {
		t.Errorf("计费设置变化后不应使用旧结果：%d", v.Used)
	}

	// 校准后，节点详情（走缓存）立即显示校准值
	base := "/api/v1/servers/" + itoa(n.ServerID)
	do(h, "GET", base, admin, nil) // 先让缓存中有旧值
	if rec := do(h, "POST", base+"/traffic/calibrate", admin, []byte(`{"used_gb":2}`)); rec.Code != 200 {
		t.Fatalf("校准失败：%d %s", rec.Code, rec.Body)
	}
	var sv serverView
	json.Unmarshal(do(h, "GET", base, admin, nil).Body.Bytes(), &sv)
	if sv.Traffic.Used != 2_000_000_000 {
		t.Errorf("校准后应立即显示新值：%d", sv.Traffic.Used)
	}
}
