package server

import (
	"encoding/json"
	"os"
	"regexp"
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

// 错误码在 OpenAPI 契约（ErrorCode 枚举）中统一定义（设计 43.4）：apierror.go 中的每个 Code 都必须列在契约里，
// 否则由契约生成的 Web 类型（api.gen.ts）会缺少这个错误码。
func TestErrorCodesInContract(t *testing.T) {
	src, err := os.ReadFile("apierror.go")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, enum, ok := strings.Cut(string(spec), "    ErrorCode:")
	if !ok {
		t.Fatal("契约中没有 ErrorCode")
	}
	enum, _, _ = strings.Cut(enum, "]")
	codes := regexp.MustCompile(`Code\s*=\s*"([a-z_]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(codes) < 10 {
		t.Fatalf("只在 apierror.go 中找到 %d 个错误码，解析方式可能已失效", len(codes))
	}
	for _, m := range codes {
		if !regexp.MustCompile(`\b` + m[1] + `\b`).MatchString(enum) {
			t.Errorf("错误码 %s 没有列入 api/openapi.yaml 的 ErrorCode", m[1])
		}
	}
}
