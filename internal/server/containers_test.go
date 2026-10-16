package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// 容器（设计 33.4，修订第 164 条）：上报的容器只在详情中返回并带上名称，列表中省略；名称整体替换、校验、随节点删除。
func TestContainersInDetail(t *testing.T) {
	s, h, _ := testServer(t)
	admin := adminToken(t, s)
	id, tok, _ := s.store.CreateServer("docker-host", 0, 1)
	body := fmt.Sprintf(`{"timestamp":%d,"system":{"boot_id":"b"},"containers":[
		{"id":"3f9a1c2b7d4e","runtime":"docker","cpu":12.5,"memory":310000000,"mem_limit":536870912,"pids":23},
		{"id":"a81d0e55c6f2","runtime":"podman","memory":96000000}]}`, time.Now().Unix())
	if rec := do(h, "POST", "/api/v1/agent/report", tok, []byte(body)); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}

	// 命名：校验 ID 与长度
	path := "/api/v1/servers/" + itoa(id) + "/container-names"
	for _, bad := range []string{`{"names":{"xyz":"web"}}`, `{"names":{"3f9a1c2b7d4e":"` + strings.Repeat("长", 41) + `"}}`} {
		if rec := do(h, "PUT", path, admin, []byte(bad)); rec.Code != 422 {
			t.Errorf("%s 应为 422，得到 %d", bad, rec.Code)
		}
	}
	rec := do(h, "PUT", path, admin, []byte(`{"names":{"3F9A1C2B7D4E":" nginx ","a81d0e55c6f2":""}}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"3f9a1c2b7d4e":"nginx"`) || strings.Contains(rec.Body.String(), "a81d0e55c6f2") {
		t.Fatalf("保存结果不对：%d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", "/api/v1/servers/999/container-names", admin, []byte(`{"names":{}}`)); rec.Code != 404 {
		t.Fatalf("不存在的节点应为 404，得到 %d", rec.Code)
	}

	// 详情：容器与名称
	var v struct {
		Latest struct {
			Containers []struct {
				ID  string   `json:"id"`
				CPU *float64 `json:"cpu"`
			} `json:"containers"`
		} `json:"latest"`
		Names map[string]string `json:"container_names"`
	}
	json.Unmarshal(do(h, "GET", "/api/v1/servers/"+itoa(id), admin, nil).Body.Bytes(), &v)
	if len(v.Latest.Containers) != 2 || v.Names["3f9a1c2b7d4e"] != "nginx" || v.Latest.Containers[1].CPU != nil {
		t.Fatalf("详情不对：%+v", v)
	}
	// 列表：省略容器与名称
	list := do(h, "GET", "/api/v1/servers", admin, nil).Body.String()
	if strings.Contains(list, "containers") || strings.Contains(list, "container_names") {
		t.Fatalf("列表不应带容器：%s", list)
	}

	// 删除节点时一并删除名称
	if rec := do(h, "DELETE", "/api/v1/servers/"+itoa(id), admin, nil); rec.Code >= 300 {
		t.Fatal(rec.Code, rec.Body)
	}
	if names, _ := s.store.ContainerNames(id); len(names) != 0 {
		t.Fatalf("节点删除后应删除容器名称：%v", names)
	}
}
