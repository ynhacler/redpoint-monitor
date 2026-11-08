package server

// 告警推送开关（修订第 175 条；修订第 316 条起只管推送）：关闭时告警照常判定、记录并在 Web 显示，
// 但不发送通知（渠道与 App 推送）、定期报告与心跳；订阅提醒另有开关（push_settings.go）。默认关闭，可在 Web“推送”页打开，也可
// 在面板主机上用 `vpsmon-server admin alerts on|off --data DIR` 切换，面板运行中也会在 10 秒内生效。

import (
	"sync/atomic"
	"time"
)

const settingAlerts = "alerts"

// AlertsSetting 是告警总开关的设置项。
type AlertsSetting struct {
	Enabled bool `json:"enabled"`
}

// alertsSwitch 缓存开关状态，评估循环每轮从设置表重新读取。
type alertsSwitch struct{ on atomic.Bool }

// alertsEnabled 报告告警是否开启。
func (s *Server) alertsEnabled() bool { return s.alertsOn.on.Load() }

// reloadAlertsSwitch 从设置表读取开关；读取失败时保持原状态。
func (s *Server) reloadAlertsSwitch() {
	var v AlertsSetting
	if _, err := s.store.GetSetting(settingAlerts, &v); err != nil {
		s.log.Error("read alerts setting failed", "component", "alert", "err", err)
		return
	}
	if s.alertsOn.on.Swap(v.Enabled) != v.Enabled {
		s.log.Info("alerts switched", "component", "alert", "enabled", v.Enabled)
	}
}

// SetAlertsEnabled 保存告警总开关（供 `vpsmon-server admin alerts` 使用）。
func (s *Store) SetAlertsEnabled(on bool) error {
	return s.PutSetting(settingAlerts, AlertsSetting{Enabled: on}, time.Now())
}
