package server

// 响应与契约一致（设计 19.0.1）：测试中经 do() 发出的每个请求，2xx 的 JSON 响应都按 api/openapi.yaml
// （JSON 版 testdata/openapi.json，由 make api-types 生成）校验：类型、required、enum、未声明的字段。
// 违反契约的响应在 TestMain 结束时汇总并使测试失败——不需要逐个测试单独断言。

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

type contractRoute struct {
	method string
	re     *regexp.Regexp
	path   string
	schema map[string]any // 2xx application/json 响应的 schema；无 JSON 响应时为 nil
	status string
}

var (
	contractOnce   sync.Once
	contractDoc    map[string]any
	contractRoutes []contractRoute

	violationsMu sync.Mutex
	violations   = map[string]int{} // 去重：同一问题在多个测试中出现只报告一次
)

func loadContract() {
	b, err := os.ReadFile("testdata/openapi.json")
	if err != nil {
		panic("读取 testdata/openapi.json 失败（运行 make api-types）：" + err.Error())
	}
	if err := json.Unmarshal(b, &contractDoc); err != nil {
		panic(err)
	}
	for path, ops := range contractDoc["paths"].(map[string]any) {
		base := "/api/v1"
		if path == "/ws" {
			base = ""
		}
		segs := strings.Split(base+path, "/")
		for i, seg := range segs {
			if strings.HasPrefix(seg, "{") {
				segs[i] = `[^/]+`
			} else {
				segs[i] = regexp.QuoteMeta(seg)
			}
		}
		pattern := "^" + strings.Join(segs, "/") + "$"
		for method, op := range ops.(map[string]any) {
			o, ok := op.(map[string]any)
			if !ok {
				continue // parameters 等非操作项
			}
			resps, _ := o["responses"].(map[string]any)
			for code, r := range resps {
				if !strings.HasPrefix(code, "2") {
					continue
				}
				schema, _ := dig(r, "content", "application/json", "schema").(map[string]any)
				contractRoutes = append(contractRoutes, contractRoute{method: strings.ToUpper(method), re: regexp.MustCompile(pattern),
					path: path, schema: schema, status: code})
			}
		}
	}
}

func dig(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// checkContract 校验一个响应；不在契约中的路由（如 /healthz 之外的 SPA 页面）忽略。
func checkContract(method, urlPath string, status int, contentType string, body []byte) {
	contractOnce.Do(loadContract)
	if status < 200 || status >= 300 || !strings.HasPrefix(contentType, "application/json") {
		return
	}
	var found bool
	for _, rt := range contractRoutes {
		if rt.method != method || !rt.re.MatchString(urlPath) {
			continue
		}
		found = true
		if rt.status != fmt.Sprint(status) || rt.schema == nil {
			continue
		}
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			report(fmt.Sprintf("%s %s：响应不是 JSON", method, rt.path))
			return
		}
		for _, e := range validate(rt.schema, v, "") {
			report(fmt.Sprintf("%s %s %d：%s", method, rt.path, status, e))
		}
		return
	}
	if !found && strings.HasPrefix(urlPath, "/api/") {
		report(fmt.Sprintf("%s %s：契约中没有这个接口", method, urlPath))
	}
}

func report(msg string) {
	violationsMu.Lock()
	violations[msg]++
	violationsMu.Unlock()
}

func resolveRef(s map[string]any) map[string]any {
	for {
		ref, ok := s["$ref"].(string)
		if !ok {
			return s
		}
		parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
		next, _ := dig(contractDoc, parts...).(map[string]any)
		if next == nil {
			return map[string]any{}
		}
		s = next
	}
}

func jsonType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if x == float64(int64(x)) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

// validate 返回 v 不符合 schema 的地方（JSON 路径 + 原因）。只实现契约中用到的关键字。
func validate(schema map[string]any, v any, at string) []string {
	s := resolveRef(schema)
	if at == "" {
		at = "$"
	}
	if alts, ok := s["oneOf"].([]any); ok {
		for _, a := range alts {
			if len(validate(a.(map[string]any), v, at)) == 0 {
				return nil
			}
		}
		return []string{at + "：不符合 oneOf 中的任何一项"}
	}
	var types []string
	switch t := s["type"].(type) {
	case string:
		types = []string{t}
	case []any:
		for _, x := range t {
			types = append(types, x.(string))
		}
	}
	got := jsonType(v)
	if len(types) > 0 {
		ok := false
		for _, t := range types {
			if t == got || (t == "number" && got == "integer") {
				ok = true
			}
		}
		if !ok {
			return []string{fmt.Sprintf("%s：类型为 %s，契约为 %v", at, got, types)}
		}
	}
	if enum, ok := s["enum"].([]any); ok && v != nil {
		ok := false
		for _, e := range enum {
			if e == v {
				ok = true
			}
		}
		if !ok {
			return []string{fmt.Sprintf("%s：值 %v 不在 enum %v 中", at, v, enum)}
		}
	}
	var errs []string
	switch x := v.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		for _, r := range asStrings(s["required"]) {
			if _, ok := x[r]; !ok {
				errs = append(errs, fmt.Sprintf("%s：缺少 required 字段 %s", at, r))
			}
		}
		if props == nil {
			if ap, ok := s["additionalProperties"].(map[string]any); ok {
				for k, fv := range x {
					errs = append(errs, validate(ap, fv, at+"."+k)...)
				}
			}
			return errs // 自由对象（如 extra、details）
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ps, ok := props[k].(map[string]any)
			if !ok {
				errs = append(errs, fmt.Sprintf("%s：字段 %s 未在契约中声明", at, k))
				continue
			}
			errs = append(errs, validate(ps, x[k], at+"."+k)...)
		}
	case []any:
		if items, ok := s["items"].(map[string]any); ok {
			for i, it := range x {
				errs = append(errs, validate(items, it, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
	}
	return errs
}

func asStrings(v any) []string {
	var out []string
	if xs, ok := v.([]any); ok {
		for _, x := range xs {
			out = append(out, x.(string))
		}
	}
	return out
}

func TestMain(m *testing.M) {
	code := m.Run()
	if len(violations) > 0 {
		msgs := make([]string, 0, len(violations))
		for k := range violations {
			msgs = append(msgs, k)
		}
		sort.Strings(msgs)
		fmt.Fprintf(os.Stderr, "\n响应与契约不一致（设计 19.0.1），共 %d 处：\n  %s\n", len(msgs), strings.Join(msgs, "\n  "))
		code = 1
	}
	os.Exit(code)
}

// 校验器本身：类型、required、未声明字段、enum、可空
func TestContractValidator(t *testing.T) {
	contractOnce.Do(loadContract)
	schema := map[string]any{"type": "object", "required": []any{"a"}, "properties": map[string]any{
		"a": map[string]any{"type": "integer"},
		"b": map[string]any{"type": []any{"string", "null"}, "enum": []any{"x", "y"}},
	}}
	cases := map[string]int{
		`{"a": 1}`:               0,
		`{"a": 1, "b": null}`:    0,
		`{"a": 1.5}`:             1,
		`{"b": "x"}`:             1,
		`{"a": 1, "b": "z"}`:     1,
		`{"a": 1, "c": true}`:    1,
		`{"a": "1", "c": false}`: 2,
	}
	for in, want := range cases {
		var v any
		json.Unmarshal([]byte(in), &v)
		if got := validate(schema, v, ""); len(got) != want {
			t.Errorf("%s：%d 处问题 %v，应为 %d", in, len(got), got, want)
		}
	}
}
