package server

// 云实例与节点关联（设计 44.5）：按公网 IP 建议关联，由用户确认，不自动修改节点。
// 关联后节点详情显示云厂商口径的状态、规格、流量包与到期时间；到期时间可一键写入节点资产信息。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var errNoCloudInstance = errorf(CodeNotFound, "云实例不存在或已删除")

// CloudInstanceByID 读取一行 cloud_instances。
func (s *Store) CloudInstanceByID(id int64) (CloudInstance, error) {
	var c CloudInstance
	err := s.DB.QueryRow(`SELECT i.id, i.account_id, a.name, a.provider, i.instance_id, i.name, i.region, i.kind, i.state,
		i.public_ipv4, i.public_ipv6, i.plan, i.expire_at, i.renew_price_cents, i.traffic_limit_bytes, i.traffic_used_bytes,
		i.traffic_period_start, i.server_id, i.updated_at
		FROM cloud_instances i JOIN cloud_accounts a ON a.id = i.account_id WHERE i.id = ?`, id).Scan(
		&c.ID, &c.AccountID, &c.AccountName, &c.Provider, &c.InstanceID, &c.Name, &c.Region, &c.Kind, &c.State,
		&c.PublicIPv4, &c.PublicIPv6, &c.Plan, &c.ExpireAt, &c.RenewPriceCents, &c.TrafficLimitBytes, &c.TrafficUsedBytes,
		&c.TrafficPeriodStart, &c.ServerID, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return c, errNoCloudInstance
	}
	return c, err
}

// SetCloudInstanceServer 关联（serverID 非 nil）或取消关联。
func (s *Store) SetCloudInstanceServer(id int64, serverID *int64) error {
	_, err := s.DB.Exec(`UPDATE cloud_instances SET server_id = ? WHERE id = ?`, serverID, id)
	return err
}

// SetServerExpireDate 只修改节点的到期日（YYYY-MM-DD）。
func (s *Store) SetServerExpireDate(id int64, date string) error {
	_, err := s.DB.Exec(`UPDATE servers SET expire_date = ? WHERE id = ?`, date, id)
	return err
}

// suggestLinks 为未关联的实例按公网 IP 找到唯一对应的节点（节点注册时的 IP 或填写的预期 IP）。
// 同一个 IP 对应多个节点时不建议，避免误关联。
func suggestLinks(insts []CloudInstance, rows []ServerRow) {
	byIP := map[string][]int64{}
	for _, r := range rows {
		seen := map[string]bool{}
		for _, ip := range []string{r.IPv4, r.IPv6, r.ExpectedIPv4, r.ExpectedIPv6} {
			ip = strings.ToLower(strings.TrimSpace(ip))
			if ip != "" && !seen[ip] {
				seen[ip] = true
				byIP[ip] = append(byIP[ip], r.ID)
			}
		}
	}
	for i := range insts {
		in := &insts[i]
		if in.ServerID != nil {
			continue
		}
		var cand int64
		for _, ip := range []string{in.PublicIPv4, in.PublicIPv6} {
			ids := byIP[strings.ToLower(strings.TrimSpace(ip))]
			if ip == "" || len(ids) != 1 {
				continue
			}
			if cand != 0 && cand != ids[0] {
				cand = 0 // IPv4 与 IPv6 指向不同节点：不建议
				break
			}
			cand = ids[0]
		}
		if cand != 0 {
			id := cand
			in.SuggestedServerID = &id
		}
	}
}

// handleLinkCloudInstance：PUT /api/v1/cloud-instances/{id}/server，admin。body：{"server_id": 节点 ID 或 null}。
func (s *Server) handleLinkCloudInstance(w http.ResponseWriter, r *http.Request) {
	id, err := cloudPathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	in, err := s.store.CloudInstanceByID(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var b struct {
		ServerID *int64 `json:"server_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&b); err != nil {
		s.writeError(w, r, &APIError{Code: CodeBadRequest, Cause: err})
		return
	}
	name := ""
	if b.ServerID != nil {
		row, err := s.store.GetServer(*b.ServerID)
		if err != nil {
			s.writeError(w, r, &APIError{Code: CodeValidationFailed,
				Details: []FieldError{{Field: "server_id", Message: "节点不存在"}}})
			return
		}
		name = row.Name
	}
	if err := s.store.SetCloudInstanceServer(id, b.ServerID); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	action := "cloud_instance.link"
	if b.ServerID == nil {
		action = "cloud_instance.unlink"
	}
	details := map[string]any{"instance_id": in.InstanceID, "account": in.AccountName, "server_name": name}
	target := int64(0)
	if b.ServerID != nil {
		target = *b.ServerID
	} else if in.ServerID != nil {
		target = *in.ServerID
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: action, TargetType: "server", TargetID: target, Success: true, Details: details})
	in, _ = s.store.CloudInstanceByID(id)
	writeJSON(w, in)
}

// handleApplyCloudInstance：POST /api/v1/cloud-instances/{id}/apply-expire，admin。
// 把关联实例的到期时间写入节点的到期日（按节点的计费时区取日期，设计 5.4）。
func (s *Server) handleApplyCloudInstance(w http.ResponseWriter, r *http.Request) {
	id, err := cloudPathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	in, err := s.store.CloudInstanceByID(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if in.ServerID == nil {
		s.writeError(w, r, errorf(CodeConflict, "实例尚未关联节点"))
		return
	}
	if in.ExpireAt <= 0 {
		s.writeError(w, r, errorf(CodeConflict, "该实例没有到期时间（按需付费）"))
		return
	}
	row, err := s.store.GetServer(*in.ServerID)
	if err != nil {
		s.writeError(w, r, errorf(CodeNotFound, "关联的节点不存在或已删除"))
		return
	}
	date := time.Unix(in.ExpireAt, 0).In(trafficLocation(*row)).Format("2006-01-02")
	if err := s.store.SetServerExpireDate(row.ID, date); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "server.update", TargetType: "server", TargetID: row.ID, Success: true,
		Details: map[string]any{"expire_date": date, "from": "cloud_instance", "instance_id": in.InstanceID}})
	writeJSON(w, map[string]string{"expire_date": date})
}

// cloudInstanceQuery 解析 GET /cloud-instances 的筛选参数。
func cloudInstanceQuery(r *http.Request) (accountID, serverID int64, err error) {
	parse := func(name string) (int64, error) {
		v := r.URL.Query().Get(name)
		if v == "" {
			return 0, nil
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return 0, errorf(CodeBadRequest, name+" 格式不正确")
		}
		return n, nil
	}
	if accountID, err = parse("account_id"); err != nil {
		return
	}
	serverID, err = parse("server_id")
	return
}
