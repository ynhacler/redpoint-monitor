package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type eventPage struct {
	Items      []SystemEvent `json:"items"`
	NextCursor string        `json:"next_cursor"`
}

// 系统事件（修订第 163 条）：升级结果、提醒、面板告警写入；按授权范围过滤；分页；180 天后删除。
func TestSystemEvents(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	tokyo, _, _ := s.store.CreateServer("tokyo-1", 0, 1)
	hk, _, _ := s.store.CreateServer("hk-2", 0, 1)
	now := time.Now()

	s.recordUpgrade(tokyo, "0.5.8", UpgradeSuccess, "", now)
	s.recordUpgrade(hk, "0.5.8", UpgradeRolledBack, "健康检查失败", now)
	s.remind("test-key", notifyMessage{Severity: SeverityWarning, ServerID: hk, ServerName: "hk-2", Message: "7 天后到期"}, now)
	s.remind("test-key", notifyMessage{Severity: SeverityWarning, ServerID: hk, ServerName: "hk-2", Message: "7 天后到期"}, now) // 去重：不再记录
	s.panelIssue(panelIssueDisk, true, "数据目录所在磁盘剩余 5.0%（1.0 GB）", 0)
	s.notify.wg.Wait()

	get := func(tok, q string) eventPage {
		t.Helper()
		rec := do(h, "GET", "/api/v1/system-events"+q, tok, nil)
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body)
		}
		var p eventPage
		json.Unmarshal(rec.Body.Bytes(), &p)
		return p
	}
	all := get(admin, "")
	if len(all.Items) != 4 {
		t.Fatalf("应有 4 条事件：%+v", all.Items)
	}
	// 最新在前：面板告警（不属于节点）→ 提醒 → 回滚 → 成功
	e := all.Items
	if e[0].Kind != eventPanel || e[0].ServerID != 0 || !strings.Contains(e[0].Title, "磁盘空间不足") || !strings.Contains(e[0].Message, "剩余 5.0%") {
		t.Errorf("面板事件不对：%+v", e[0])
	}
	if e[1].Kind != eventReminder || e[1].ServerName != "hk-2" {
		t.Errorf("提醒事件不对：%+v", e[1])
	}
	if e[2].Kind != eventUpgrade || e[2].Severity != SeverityWarning || !strings.Contains(e[2].Title, "已回滚") || e[2].Message != "健康检查失败" {
		t.Errorf("回滚事件不对：%+v", e[2])
	}
	if e[3].Severity != SeverityInfo || !strings.Contains(e[3].Title, "tokyo-1 Agent 已升级到 v0.5.8") {
		t.Errorf("升级成功事件不对：%+v", e[3])
	}

	// 分页
	p1 := get(admin, "?limit=3")
	if len(p1.Items) != 3 || p1.NextCursor == "" {
		t.Fatalf("第一页不对：%d %q", len(p1.Items), p1.NextCursor)
	}
	p2 := get(admin, "?limit=3&cursor="+p1.NextCursor)
	if len(p2.Items) != 1 || p2.NextCursor != "" || p2.Items[0].ID != e[3].ID {
		t.Fatalf("第二页不对：%+v", p2)
	}

	// 【安全】只读 API Key：指定节点时只看到该节点的事件，看不到面板事件与其他节点
	key, _ := s.store.CreateAPIKey(&APIKey{Name: "tokyo", ScopeType: "servers", ScopeValue: itoa(tokyo)}, now)
	scoped := get(key, "")
	if len(scoped.Items) != 1 || scoped.Items[0].ServerID != tokyo {
		t.Fatalf("范围受限的 Key 应只看到 tokyo-1 的 1 条：%+v", scoped.Items)
	}
	// 范围过滤后分页仍然连续
	scopedPage := get(key, "?limit=1")
	if len(scopedPage.Items) != 1 || scopedPage.NextCursor != "" {
		t.Fatalf("过滤后的分页不对：%+v", scopedPage)
	}
	allKey, _ := s.store.CreateAPIKey(&APIKey{Name: "all", ScopeType: "all"}, now)
	if n := len(get(allKey, "").Items); n != 4 {
		t.Fatalf("全部节点范围应看到 4 条，实际 %d", n)
	}
	// 查看者看到全部
	if n := len(get(viewerToken(t, s, "viewer-1"), "").Items); n != 4 {
		t.Fatalf("查看者应看到 4 条，实际 %d", n)
	}

	// 节点删除后事件保留，名称为空
	s.store.DB.Exec(`DELETE FROM servers WHERE id = ?`, tokyo)
	if last := get(admin, "").Items[3]; last.ServerID != tokyo || last.ServerName != "" {
		t.Fatalf("删除节点后事件应保留：%+v", last)
	}
	// 180 天后删除
	if n, err := s.store.PruneSystemEvents(now.Add(181 * 24 * time.Hour)); err != nil || n != 4 {
		t.Fatalf("应删除 4 条：%d %v", n, err)
	}
}

// 升级任务超时同样记为系统事件。
func TestSystemEventUpgradeTimeout(t *testing.T) {
	s, _, _ := testServer(t)
	id, _, _ := s.store.CreateServer("sg-3", 0, 1)
	if _, err := s.store.CreateUpgradeTask(id, "0.5.8", "0.5.7", "admin", time.Now()); err != nil {
		t.Fatal(err)
	}
	expired, err := s.store.ExpireUpgradeTasks(time.Now().Add(16 * time.Minute))
	if err != nil || len(expired) != 1 || expired[0].ServerID != id || expired[0].TargetVersion != "0.5.8" || expired[0].Reason == "" {
		t.Fatalf("过期任务不对：%+v %v", expired, err)
	}
}
