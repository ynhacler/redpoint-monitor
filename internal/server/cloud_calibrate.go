package server

// 云厂商口径的自动流量校准（设计 44.5）：可选开关，默认关闭。打开后每天一次，用关联实例的云厂商本周期流量
// 作为“服务商数值”校准节点（5.7），记入校准历史。
//
// 只有口径能对上时才校准，否则记录原因、不改节点：
//   - 实例已关联节点，且云厂商返回了流量包用量（有流量包的实例：Lightsail、轻量应用服务器、OCI 出站流量除外）
//   - 云厂商的周期起点与节点本周期的起点相同（计费日、时区一致）
//   - 云厂商流量在 36 小时内同步过（数据有数小时延迟，太旧的不用）

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	autoCalibrateEvery = 23 * time.Hour // 每天一次（留 1 小时余量，配合每小时的检查）
	cloudTrafficStale  = 36 * time.Hour
)

// autoCalibrate 检查全部开启了自动校准的实例；由提醒循环每小时调用。
func (s *Server) autoCalibrate(now time.Time) {
	insts, err := s.store.CloudInstances(0)
	if err != nil {
		s.log.Error("list cloud instances for calibration failed", "component", "cloud", "err", err)
		return
	}
	accounts := map[int64]CloudAccount{}
	for _, in := range insts {
		if !in.AutoCalibrate || in.ServerID == nil || now.Unix()-in.CalibratedAt < int64(autoCalibrateEvery/time.Second) {
			continue
		}
		a, ok := accounts[in.AccountID]
		if !ok {
			if a, err = s.store.GetCloudAccount(in.AccountID); err != nil {
				continue
			}
			accounts[in.AccountID] = a
		}
		status := s.calibrateFromCloud(in, a, now)
		if err := s.store.RecordCloudCalibrate(in.ID, now, status); err != nil {
			s.log.Error("record cloud calibration failed", "component", "cloud", "err", err)
		}
	}
}

// calibrateFromCloud 校准一个实例关联的节点，返回结果说明（显示在云账户页）。
func (s *Server) calibrateFromCloud(in CloudInstance, a CloudAccount, now time.Time) string {
	if in.TrafficPeriodStart == "" || in.TrafficLimitBytes <= 0 {
		return "云厂商没有返回流量包用量，未校准"
	}
	if a.TrafficSyncedAt == 0 || now.Sub(time.Unix(a.TrafficSyncedAt, 0)) > cloudTrafficStale {
		return "云厂商流量超过 36 小时未同步，未校准"
	}
	row, err := s.store.GetServer(*in.ServerID)
	if err != nil {
		return "关联的节点不存在，未校准"
	}
	cur, err := s.trafficOf(*row, now)
	if err != nil {
		s.log.Error("traffic for calibration failed", "component", "cloud", "err", err)
		return "读取节点流量失败，未校准"
	}
	if cur.CycleStart != in.TrafficPeriodStart {
		return fmt.Sprintf("计费周期不一致（节点从 %s 开始，云厂商从 %s 开始），未校准；请调整节点的流量重置日与计费时区",
			cur.CycleStart, in.TrafficPeriodStart)
	}
	name := in.Name
	if name == "" {
		name = in.InstanceID
	}
	note := "自动校准：" + a.Name + " / " + name + "（云厂商口径）"
	_, adj, err := s.calibrate(*row, in.TrafficUsedBytes, note, now)
	if err != nil {
		s.log.Error("auto calibrate failed", "component", "cloud", "server_id", row.ID, "err", err)
		return "校准失败，将在明天重试"
	}
	if err := s.store.Audit(AuditEntry{ActorType: "system", Action: "traffic.calibrate", TargetType: "server", TargetID: row.ID,
		Success: true, Details: map[string]any{"source": "cloud", "cloud_instance": in.ID, "cycle_start": adj.CycleStart,
			"measured": adj.Measured, "reported": adj.Reported, "adjustment": adj.Adjustment}}, now); err != nil {
		s.log.Warn("audit auto calibration failed", "component", "audit", "err", err)
	}
	s.log.Info("traffic auto-calibrated from cloud", "component", "cloud", "server_id", row.ID, "adjustment", adj.Adjustment)
	return "已按云厂商数值校准为 " + fmtTrafficUnit(in.TrafficUsedBytes, row.TrafficUnit)
}

// handleCloudAutoCalibrate：PUT /api/v1/cloud-instances/{id}/auto-calibrate，admin。{"enabled": bool}
// 打开时要求实例已关联节点且有流量包；打开后在下一次每小时检查时执行第一次校准。
func (s *Server) handleCloudAutoCalibrate(w http.ResponseWriter, r *http.Request) {
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
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&b); err != nil || b.Enabled == nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "enabled", Message: "请指定 true 或 false"}}})
		return
	}
	if *b.Enabled && in.ServerID == nil {
		s.writeError(w, r, errorf(CodeConflict, "请先把实例关联到节点"))
		return
	}
	if *b.Enabled && in.TrafficLimitBytes <= 0 {
		s.writeError(w, r, errorf(CodeConflict, "该实例没有流量包，无法按云厂商口径校准"))
		return
	}
	if err := s.store.SetCloudAutoCalibrate(id, *b.Enabled); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if *b.Enabled {
		// 下一次检查（每小时）立即执行，不必等 24 小时
		if err := s.store.RecordCloudCalibrate(id, time.Unix(0, 0), "已开启，将在 1 小时内执行第一次校准"); err != nil {
			s.writeError(w, r, internalError(err))
			return
		}
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "cloud_instance.auto_calibrate", TargetType: "cloud_instance", TargetID: id,
		Success: true, Details: map[string]any{"enabled": *b.Enabled, "server_id": in.ServerID}})
	in, _ = s.store.CloudInstanceByID(id)
	writeJSON(w, in)
}
