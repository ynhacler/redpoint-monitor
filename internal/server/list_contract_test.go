package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// 所有列表接口返回 {"items": [...], "next_cursor": "..."}（设计 19.0.2）：遍历路由表中管理员可访问的 GET 接口，
// 凡是返回 items 的都必须同时有字符串 next_cursor，items 为数组（不是 null）。新增的列表接口自动纳入检查。
func TestListEnvelope(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	_, n, _ := createNode(t, h, admin, `{"name":"list-1"}`)
	id := itoa(n.ServerID)
	lists := 0
	for _, rt := range s.routeTable {
		method, path, _ := strings.Cut(rt.pattern, " ")
		if method != "GET" || rt.access != accessAdmin {
			continue
		}
		path = strings.ReplaceAll(path, "{id}", id)
		rec := do(h, "GET", path, admin, nil)
		if rec.Code != 200 {
			continue // 需要额外参数的接口（如 history 的 range）不在此检查
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			if strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "[") {
				t.Errorf("%s 返回裸数组，应为 {items, next_cursor}", rt.pattern)
			}
			continue
		}
		items, ok := body["items"]
		if !ok {
			continue
		}
		lists++
		var cur string
		if raw, ok := body["next_cursor"]; !ok || json.Unmarshal(raw, &cur) != nil {
			t.Errorf("%s 缺少字符串 next_cursor", rt.pattern)
		}
		if !strings.HasPrefix(strings.TrimSpace(string(items)), "[") {
			t.Errorf("%s 的 items 应为数组：%s", rt.pattern, items)
		}
	}
	if lists < 10 {
		t.Errorf("只检查到 %d 个列表接口，路由表遍历可能有误", lists)
	}
}
