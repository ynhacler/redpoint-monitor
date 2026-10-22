package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"vpsmon/internal/protocol"
)

// 解锁检测（设计 50）：旧版 Agent 不支持时 409；请求后在上报响应头中下发请求号，结果随上报带回并显示在节点详情；
// 不是最近一次请求的结果不保存；查看者看得到结果、不能发起检测
func TestUnlockCheck(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	id, tok, _ := s.store.CreateServer("unlock-1", 0, 1)
	url := "/api/v1/servers/" + itoa(id) + "/unlock-check"
	report := func(supported bool, body string) http.Header {
		t.Helper()
		if body == "" {
			body = `{"timestamp":` + strconv.FormatInt(time.Now().Unix(), 10) + `,"system":{"boot_id":"b"}}`
		}
		req := httptest.NewRequest("POST", "/api/v1/agent/report", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		if supported {
			req.Header.Set(protocol.HeaderUnlock, protocol.UnlockVersion)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 204 {
			t.Fatalf("上报失败：%d %s", rec.Code, rec.Body)
		}
		return rec.Header()
	}

	// 离线：409；旧版 Agent（不带请求头）：409 提示升级
	if rec := do(h, "POST", url, admin, nil); rec.Code != 409 {
		t.Fatalf("离线应为 409：%d", rec.Code)
	}
	report(false, "")
	if rec := do(h, "POST", url, admin, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), "升级") {
		t.Fatalf("旧版 Agent 应提示升级：%d %s", rec.Code, rec.Body)
	}

	// 新版 Agent：没有请求时不下发
	if hd := report(true, ""); hd.Get(protocol.HeaderUnlockRequest) != "" {
		t.Fatal("没有请求时不应下发")
	}
	rec := do(h, "POST", url, admin, nil)
	var v unlockView
	json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != 200 || !v.Supported || !v.Pending || v.RequestedAt == 0 {
		t.Fatalf("请求：%d %+v", rec.Code, v)
	}
	// 检测中再点：不生成新的请求号
	req1 := report(true, "").Get(protocol.HeaderUnlockRequest)
	do(h, "POST", url, admin, nil)
	if req1 == "" || report(true, "").Get(protocol.HeaderUnlockRequest) != req1 {
		t.Fatalf("检测中不应生成新的请求号：%q", req1)
	}

	// 别的请求号的结果不保存；本次的保存
	result := func(reqID string) string {
		return `{"timestamp":` + strconv.FormatInt(time.Now().Unix(), 10) + `,"system":{"boot_id":"b"},"unlock":{"request":"` + reqID +
			`","checked_at":1,"items":[{"service":"netflix","status":"yes","region":"JP"},{"service":"chatgpt","status":"no","region":"HK"}]}}`
	}
	report(true, result("other"))
	if x, _ := s.store.getUnlock(id); x.done != "" {
		t.Fatal("其他请求号的结果不应保存")
	}
	report(true, result(req1))
	if hd := report(true, ""); hd.Get(protocol.HeaderUnlockRequest) != "" {
		t.Fatal("收到结果后不应再下发")
	}
	var detail struct {
		Unlock *unlockView `json:"unlock"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/servers/"+itoa(id), viewerToken(t, s, "viewer-unlock"), nil).Body.Bytes(), &detail)
	if u := detail.Unlock; u == nil || u.Pending || u.CheckedAt == 0 || len(u.Items) != 2 || u.Items[0].Region != "JP" {
		t.Fatalf("节点详情 %+v", detail.Unlock)
	}
	// 结果不进入实时状态
	s.mu.Lock()
	if sn := s.latest[id]; sn != nil && sn.Report.Unlock != nil {
		t.Error("检测结果不应进入实时状态")
	}
	s.mu.Unlock()
	if rec := do(h, "POST", url, viewerToken(t, s, "viewer-unlock2"), nil); rec.Code != 403 {
		t.Fatalf("查看者不能发起检测：%d", rec.Code)
	}
	// 格式不对的结果不保存
	do(h, "POST", url, admin, nil)
	req2 := report(true, "").Get(protocol.HeaderUnlockRequest)
	report(true, strings.Replace(result(req2), `"status":"yes"`, `"status":"maybe"`, 1))
	if x, _ := s.store.getUnlock(id); x.done == req2 {
		t.Fatal("格式不对的结果不应保存")
	}
}
