package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"vpsmon/internal/cloud"
)

// 自动流量校准（设计 44.5）：打开要求已关联且有流量包；周期一致时校准并记入历史，不一致时只记录原因；
// 每天一次；取消关联时自动关闭
func TestCloudAutoCalibrate(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, v, _ := createCloudAccount(t, h, admin, awsAccountBody)
	_, node, _ := createNode(t, h, admin, `{"name":"ls"}`)
	now := time.Now()
	row, _ := s.store.GetServer(node.ServerID)
	cur, _ := s.trafficOf(*row, now)

	s.store.ReplaceCloudInstances(v.ID, []cloud.Instance{
		{ID: "ls-1", Name: "ls-1", Kind: "lightsail", TrafficLimit: 1e12},
		{ID: "i-ec2", Name: "ec2", Kind: "ec2"},
	}, now)
	s.store.SaveCloudTraffic(v.ID, []cloud.Instance{{ID: "ls-1", TrafficUsed: 120e9, TrafficLimit: 1e12, TrafficPeriodStart: cur.CycleStart}}, now)
	s.store.RecordCloudSync(v.ID, cloudSyncResult{TrafficSynced: true}, now)
	insts, _ := s.store.CloudInstances(v.ID)
	var ls, ec2 CloudInstance
	for _, in := range insts {
		if in.InstanceID == "ls-1" {
			ls = in
		} else {
			ec2 = in
		}
	}
	put := func(id int64, on bool) int {
		body := `{"enabled":false}`
		if on {
			body = `{"enabled":true}`
		}
		return do(h, "PUT", "/api/v1/cloud-instances/"+itoa(id)+"/auto-calibrate", admin, []byte(body)).Code
	}
	if c := put(ls.ID, true); c != 409 {
		t.Fatalf("未关联时不能打开：%d", c)
	}
	sid := node.ServerID
	s.store.SetCloudInstanceServer(ls.ID, &sid)
	s.store.SetCloudInstanceServer(ec2.ID, &sid)
	if c := put(ec2.ID, true); c != 409 {
		t.Fatalf("没有流量包时不能打开：%d", c)
	}
	if c := put(ls.ID, true); c != 200 {
		t.Fatalf("打开 %d", c)
	}

	s.autoCalibrate(now)
	adj, _ := s.store.ListAdjustments(node.ServerID, 10)
	if len(adj) != 1 || adj[0].Reported != 120e9 || !strings.Contains(adj[0].Note, "自动校准") {
		t.Fatalf("应按云厂商数值校准：%+v", adj)
	}
	in, _ := s.store.CloudInstanceByID(ls.ID)
	if !strings.HasPrefix(in.CalibrateStatus, "已按云厂商数值校准为 120 GB") || in.CalibratedAt != now.Unix() {
		t.Fatalf("状态 %+v", in)
	}
	var tv trafficView
	json.Unmarshal(do(h, "GET", "/api/v1/servers/"+itoa(node.ServerID)+"/traffic/current", admin, nil).Body.Bytes(), &tv)
	if tv.Used != 120e9 {
		t.Fatalf("节点流量应为校准值：%d", tv.Used)
	}

	// 同一天不重复
	s.autoCalibrate(now.Add(time.Hour))
	if adj, _ := s.store.ListAdjustments(node.ServerID, 10); len(adj) != 1 {
		t.Fatalf("每天只校准一次：%d", len(adj))
	}

	// 周期不一致：不校准，记录原因
	s.store.SaveCloudTraffic(v.ID, []cloud.Instance{{ID: "ls-1", TrafficUsed: 200e9, TrafficLimit: 1e12, TrafficPeriodStart: "2000-01-01"}}, now)
	later := now.Add(24 * time.Hour)
	s.store.RecordCloudSync(v.ID, cloudSyncResult{TrafficSynced: true}, later)
	s.autoCalibrate(later)
	if adj, _ := s.store.ListAdjustments(node.ServerID, 10); len(adj) != 1 {
		t.Fatalf("周期不一致时不应校准：%d", len(adj))
	}
	if in, _ := s.store.CloudInstanceByID(ls.ID); !strings.Contains(in.CalibrateStatus, "计费周期不一致") {
		t.Fatalf("应说明原因：%q", in.CalibrateStatus)
	}

	// 云厂商数据过旧
	s.store.SaveCloudTraffic(v.ID, []cloud.Instance{{ID: "ls-1", TrafficUsed: 200e9, TrafficLimit: 1e12, TrafficPeriodStart: cur.CycleStart}}, now)
	s.autoCalibrate(later.Add(48 * time.Hour))
	if in, _ := s.store.CloudInstanceByID(ls.ID); !strings.Contains(in.CalibrateStatus, "36 小时未同步") {
		t.Fatalf("数据过旧应不校准：%q", in.CalibrateStatus)
	}

	// 取消关联：自动关闭
	if rec := do(h, "PUT", "/api/v1/cloud-instances/"+itoa(ls.ID)+"/server", admin, []byte(`{"server_id":null}`)); rec.Code != 200 {
		t.Fatalf("取消关联 %d %s", rec.Code, rec.Body)
	}
	if in, _ := s.store.CloudInstanceByID(ls.ID); in.AutoCalibrate {
		t.Fatal("取消关联后应关闭自动校准")
	}
}
