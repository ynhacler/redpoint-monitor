package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// panelHook 建一个接收全部通知的 Webhook 渠道（最低级别设为严重：面板自身告警不受级别限制，照样收到）。
func panelHook(t *testing.T, s *Server) *fakeReceiver {
	t.Helper()
	hook := newReceiver(t)
	if rec := do(s.routes(), "POST", "/api/v1/notification-channels", adminToken(t, s),
		[]byte(`{"type":"webhook","name":"ops","min_severity":"critical","config":{"url":"`+hook.srv.URL+`/hook"}}`)); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	return hook
}

func lastPayload(t *testing.T, s *Server, hook *fakeReceiver) map[string]any {
	t.Helper()
	s.notify.wg.Wait()
	_, body, _ := hook.last()
	var p map[string]any
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// 后台任务 10 分钟内崩溃 3 次：发一次告警（不重复），稳定 10 分钟后发恢复通知（设计 16.1）。
func TestPanelIssueTaskCrash(t *testing.T) {
	s, _, _ := testServer(t)
	hook := panelHook(t, s)
	var pending []func()
	s.health.after = func(_ time.Duration, f func()) { pending = append(pending, f) }

	now := time.Now()
	for i := range 4 {
		s.taskCrashed("alerts", now.Add(time.Duration(i)*time.Second))
	}
	s.notify.wg.Wait()
	if hook.count() != 1 {
		t.Fatalf("应只发 1 条告警，实际 %d 条", hook.count())
	}
	p := lastPayload(t, s, hook)
	if p["kind"] != NotifyPanelIssue || p["issue"] != "task:alerts" || !strings.Contains(p["text"].(string), "告警评估") {
		t.Fatalf("告警内容不对：%v", p)
	}
	// 检查时期间又崩溃过：不恢复，继续等待；之后稳定才恢复
	pending[0]()
	s.notify.wg.Wait()
	if hook.count() != 1 || len(pending) != 2 {
		t.Fatalf("期间有新的崩溃不应恢复：通知 %d 条，检查 %d 次", hook.count(), len(pending))
	}
	pending[1]()
	if p := lastPayload(t, s, hook); p["kind"] != NotifyPanelIssueResolved || !strings.Contains(p["title"].(string), "后台任务运行正常") {
		t.Fatalf("应发恢复通知：%v", p)
	}
	// 窗口外的零星崩溃不告警
	s.taskCrashed("cloud", now)
	s.taskCrashed("cloud", now.Add(11*time.Minute))
	s.taskCrashed("cloud", now.Add(22*time.Minute))
	s.notify.wg.Wait()
	if hook.count() != 2 {
		t.Fatalf("窗口外的崩溃不应告警，实际 %d 条", hook.count())
	}
}

// 磁盘空间：< 10% 告警一次，10%～12% 之间保持，≥ 12% 恢复；读取失败不改变状态。
func TestPanelIssueDisk(t *testing.T) {
	s, _, _ := testServer(t)
	hook := panelHook(t, s)
	var pct float64
	var fail bool
	s.health.statDisk = func() (float64, uint64, error) {
		if fail {
			return 0, 0, errors.New("io")
		}
		return pct, 3 << 30, nil
	}
	steps := []struct {
		pct   float64
		fail  bool
		count int
	}{{50, false, 0}, {8, false, 1}, {7, false, 1}, {11, false, 1}, {0, true, 1}, {12.5, false, 2}, {11, false, 2}}
	for i, st := range steps {
		pct, fail = st.pct, st.fail
		s.checkDisk()
		s.notify.wg.Wait()
		if hook.count() != st.count {
			t.Fatalf("第 %d 步（%.1f%%）：通知 %d 条，应为 %d", i, st.pct, hook.count(), st.count)
		}
		if i == 1 {
			if p := lastPayload(t, s, hook); !strings.Contains(p["text"].(string), "剩余 8.0%（3.0 GB）") {
				t.Fatalf("说明不对：%v", p["text"])
			}
		}
	}
}

// 写库与推送：连续失败达到次数后告警一次，成功即恢复。
func TestPanelIssueDBAndPush(t *testing.T) {
	s, _, _ := testServer(t)
	hook := panelHook(t, s)
	s.dbWriteResult(false)
	s.dbWriteResult(false)
	s.notify.wg.Wait()
	if hook.count() != 0 {
		t.Fatal("失败 2 次不应告警")
	}
	s.dbWriteResult(false)
	s.dbWriteResult(false)
	if p := lastPayload(t, s, hook); hook.count() != 1 || p["issue"] != "db" {
		t.Fatalf("连续失败 3 次应告警一次：%d %v", hook.count(), p)
	}
	s.dbWriteResult(true)
	s.dbWriteResult(true)
	if p := lastPayload(t, s, hook); hook.count() != 2 || p["kind"] != NotifyPanelIssueResolved {
		t.Fatalf("成功后应恢复一次：%d %v", hook.count(), p)
	}

	for range pushFailAlertAt - 1 {
		s.pushResult(false)
	}
	s.pushResult(true) // 中途成功：重新计数
	for range pushFailAlertAt - 1 {
		s.pushResult(false)
	}
	s.notify.wg.Wait()
	if hook.count() != 2 {
		t.Fatal("没有连续失败 5 次不应告警")
	}
	s.pushResult(false)
	if p := lastPayload(t, s, hook); hook.count() != 3 || p["issue"] != "push" || !strings.Contains(p["text"].(string), "Push Relay") {
		t.Fatalf("推送连续失败应告警：%d %v", hook.count(), p)
	}
}

// runTask：panic 后自动重启，并计入崩溃统计。
func TestRunTaskRestartsAndCounts(t *testing.T) {
	s, _, _ := testServer(t)
	s.health.after = func(time.Duration, func()) {}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runs atomic.Int32
	done := make(chan struct{})
	go s.runTask(ctx, "services", func(ctx context.Context) {
		if runs.Add(1) <= 2 {
			panic("boom")
		}
		close(done)
		<-ctx.Done()
	})
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("任务没有被重启")
	}
	s.health.mu.Lock()
	n := len(s.health.taskFails["services"])
	s.health.mu.Unlock()
	if n != 2 {
		t.Fatalf("应记录 2 次崩溃，实际 %d", n)
	}
}

// 真实的数据目录剩余空间可以读取（Linux / macOS）。
func TestDiskFree(t *testing.T) {
	pct, free, err := diskFree(t.TempDir())
	if err != nil || pct <= 0 || pct > 100 || free == 0 {
		t.Fatalf("diskFree：%.1f%% %d %v", pct, free, err)
	}
}
