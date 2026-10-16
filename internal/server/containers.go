package server

// 容器（设计 33.4，修订第 164 条）：Agent 从 cgroup 读到的运行中容器随上报进入实时状态（Report.Containers），
// 只在节点详情中返回，不入库。Agent 拿不到容器名，用户在面板中给容器 ID 起名，保存在 container_names 表中。

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxContainerNames  = 100
	maxContainerNameLn = 40
)

var containerIDPattern = regexp.MustCompile(`^[0-9a-f]{12}$`)

// ContainerNames 返回节点的容器名称（容器 ID 前 12 位 → 名称）。
func (s *Store) ContainerNames(serverID int64) (map[string]string, error) {
	rows, err := s.DB.Query(`SELECT container_id, name FROM container_names WHERE server_id = ?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// SetContainerNames 整体替换节点的容器名称。
func (s *Store) SetContainerNames(serverID int64, names map[string]string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM container_names WHERE server_id = ?`, serverID); err != nil {
		return err
	}
	for id, name := range names {
		if _, err := tx.Exec(`INSERT INTO container_names (server_id, container_id, name) VALUES (?,?,?)`, serverID, id, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// handleContainerNames：PUT /api/v1/servers/{id}/container-names，admin。{names: {容器 ID: 名称}}，整体替换；名称为空表示删除。
func (s *Server) handleContainerNames(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if _, err := s.store.GetServer(id); errors.Is(err, errNoServer) {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	} else if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	var body struct {
		Names map[string]string `json:"names"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	names := map[string]string{}
	for cid, name := range body.Names {
		cid, name = strings.ToLower(strings.TrimSpace(cid)), strings.TrimSpace(name)
		if !containerIDPattern.MatchString(cid) {
			s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "names", Message: "容器 ID 应为 12 位十六进制"}}})
			return
		}
		if utf8.RuneCountInString(name) > maxContainerNameLn {
			s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "names", Message: "名称最多 40 个字符"}}})
			return
		}
		if name != "" {
			names[cid] = name
		}
	}
	if len(names) > maxContainerNames {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "names", Message: "最多给 100 个容器命名"}}})
		return
	}
	if err := s.store.SetContainerNames(id, names); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", ActorID: info(r).session.User.Username, Action: "server.container_names",
		TargetType: "server", TargetID: id, Success: true, Details: map[string]any{"count": len(names)}})
	writeJSON(w, map[string]any{"names": names})
}
