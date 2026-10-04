package server

// 到期提醒与云账户提醒（设计 1.2.5、44.6）：按里程碑各发一次通知，经通知渠道送达（遵守免打扰，16.5）。
//
//	到期      节点到期日（以及未写入节点的云实例到期时间）：剩 30 / 14 / 7 / 3 / 1 天、当天、已过期各一次
//	费用      云账户本月费用（有预估时按预估）超过预算：每个账户每月一次
//	流量包    云厂商口径的流量包用到 90% / 95%：每个周期各一次
//	同步      云账户凭证失效，或连续同步失败超过 24 小时：每次故障一次
//
// 已发送的提醒记在 reminders_sent（按去重键），面板重启或停机期间错过的里程碑会补发一次（按所在区间），
// 不会重复。级别至少为“警告”：提示级在免打扰期间会被丢弃，且多数渠道默认只接收警告以上。

import (
	"context"
	"fmt"
	"time"

	"vpsmon/internal/cloud"
)

const NotifyReminder = "reminder"

// expireMilestones 是到期提醒的里程碑（剩余天数），从大到小。
var expireMilestones = []int{30, 14, 7, 3, 1, 0}

// expireBracket 返回剩余天数所在的提醒区间：满足 days ≤ m 的最小里程碑 m（如剩 5 天属于“7 天”区间）；
// 已过期返回 -1；超过 30 天返回 ok=false。按区间而不是恰好等于里程碑判断，停机期间错过的提醒会补发一次。
func expireBracket(days int) (m int, ok bool) {
	if days < 0 {
		return -1, true
	}
	if days > expireMilestones[0] {
		return 0, false
	}
	m = expireMilestones[0]
	for _, x := range expireMilestones {
		if days <= x {
			m = x
		}
	}
	return m, true
}

func expireSeverity(m int) string {
	if m <= 3 {
		return SeverityCritical
	}
	return SeverityWarning
}

func expireText(date string, days int) string {
	switch {
	case days < 0:
		return fmt.Sprintf("已于 %s 到期（%d 天前），请尽快续费或处理", date, -days)
	case days == 0:
		return fmt.Sprintf("今天（%s）到期", date)
	}
	return fmt.Sprintf("将于 %s 到期（%d 天后）", date, days)
}

// MarkReminder 记录去重键；已存在时返回 false（之前已发送）。
func (s *Store) MarkReminder(key string, now time.Time) (bool, error) {
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO reminders_sent (key, sent_at) VALUES (?, ?)`, key, now.Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// PruneReminders 删除 400 天前的记录（到期日与月份都已过去，不会再用到）。
func (s *Store) PruneReminders(now time.Time) error {
	_, err := s.DB.Exec(`DELETE FROM reminders_sent WHERE sent_at < ?`, now.AddDate(0, 0, -400).Unix())
	return err
}

// reminderLoop 每小时检查一次（启动 1 分钟后第一次）。
func (s *Server) reminderLoop(ctx context.Context) {
	wait := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = time.Hour
		s.checkReminders(time.Now())
	}
}

// remind 在去重键第一次出现时发送通知。
func (s *Server) remind(key string, m notifyMessage, now time.Time) {
	first, err := s.store.MarkReminder(key, now)
	if err != nil {
		s.log.Error("record reminder failed", "component", "reminder", "err", err)
		return
	}
	if !first {
		return
	}
	m.Kind = NotifyReminder
	s.log.Info("reminder", "component", "reminder", "key", key, "server", m.ServerName)
	s.send(m, now)
}

// checkReminders 检查全部提醒。
func (s *Server) checkReminders(now time.Time) {
	rows, err := s.store.ListServers()
	if err != nil {
		s.log.Error("list servers for reminders failed", "component", "reminder", "err", err)
		return
	}
	nodeExpire := map[int64]bool{}
	for _, row := range rows {
		if row.ExpireDate == "" {
			continue
		}
		loc := trafficLocation(row)
		exp, err := time.ParseInLocation("2006-01-02", row.ExpireDate, loc)
		if err != nil {
			continue
		}
		nodeExpire[row.ID] = true
		days := calendarDays(now.In(loc), exp)
		m, ok := expireBracket(days)
		if !ok {
			continue
		}
		msg := notifyMessage{ServerID: row.ID, ServerName: row.Name, Type: "expire", Severity: expireSeverity(m),
			Message: expireText(row.ExpireDate, days)}
		if s.publicURL != "" {
			msg.Link = s.publicURL + "/servers/" + itoa64(row.ID)
		}
		s.remind(fmt.Sprintf("expire:server:%d:%s:%d", row.ID, row.ExpireDate, m), msg, now)
	}
	s.checkCloudReminders(now, nodeExpire)
	if err := s.store.PruneReminders(now); err != nil {
		s.log.Warn("prune reminders failed", "component", "reminder", "err", err)
	}
}

// calendarDays 返回从 now 所在日期到 date 的日历天数（同一时区）。
func calendarDays(now, date time.Time) int {
	y, mo, d := now.Date()
	today := time.Date(y, mo, d, 0, 0, 0, 0, now.Location())
	return int(date.Sub(today).Round(time.Hour).Hours() / 24)
}

func (s *Server) checkCloudReminders(now time.Time, nodeExpire map[int64]bool) {
	accounts, err := s.store.ListCloudAccounts()
	if err != nil || len(accounts) == 0 {
		return
	}
	link := ""
	if s.publicURL != "" {
		link = s.publicURL + "/cloud"
	}
	for _, a := range accounts {
		if !a.Enabled {
			continue
		}
		label := "云账户“" + a.Name + "”"
		// 同步故障：凭证失效立即提醒；其他错误连续 24 小时后提醒
		if a.ErrorSince > 0 && (a.AuthFailed || now.Unix()-a.ErrorSince >= 24*3600) {
			why := "连续 24 小时同步失败"
			if a.AuthFailed {
				why = "凭证失效或权限不足，已停止自动同步"
			}
			s.remind(fmt.Sprintf("cloud_sync:%d:%d", a.ID, a.ErrorSince), notifyMessage{ServerName: label, Type: "cloud_sync",
				Severity: SeverityWarning, Message: why + "：" + a.LastError, Link: link}, now)
		}
		// 费用超预算：有预估时按预估（提前提醒），每月一次
		if a.BudgetCents > 0 {
			costs, err := s.store.CloudCosts(a.ID, 1)
			if err == nil && len(costs) > 0 && costs[0].Period == cloud.BillingMonth(a.Provider, now) {
				c := costs[0]
				v, what := c.AmountCents, "本月已产生"
				if c.ForecastCents != nil && *c.ForecastCents > v {
					v, what = *c.ForecastCents, "本月预估"
				}
				if v > a.BudgetCents {
					s.remind(fmt.Sprintf("cloud_cost:%d:%s", a.ID, c.Period), notifyMessage{ServerName: label, Type: "cloud_cost",
						Severity: SeverityWarning, Link: link, Message: fmt.Sprintf("%s %s %.2f，超过预算 %s %.2f",
							what, c.Currency, float64(v)/100, c.Currency, float64(a.BudgetCents)/100)}, now)
				}
			}
		}
	}

	insts, err := s.store.CloudInstances(0)
	if err != nil {
		return
	}
	for _, in := range insts {
		name := in.Name
		if name == "" {
			name = in.InstanceID
		}
		label := in.AccountName + " / " + name
		// 流量包用量（云厂商口径）
		if in.TrafficLimitBytes > 0 && in.TrafficPeriodStart != "" {
			pct := float64(in.TrafficUsedBytes) / float64(in.TrafficLimitBytes) * 100
			for _, step := range []struct {
				at  float64
				sev string
			}{{95, SeverityCritical}, {90, SeverityWarning}} {
				if pct >= step.at {
					s.remind(fmt.Sprintf("cloud_traffic:%d:%s:%.0f", in.ID, in.TrafficPeriodStart, step.at), notifyMessage{
						ServerName: label, Type: "cloud_traffic", Severity: step.sev, Link: link,
						Message: fmt.Sprintf("流量包已用 %.0f%%（%s / %s）", pct, fmtBytesSI(in.TrafficUsedBytes), fmtBytesSI(in.TrafficLimitBytes))}, now)
					break
				}
			}
		}
		// 实例到期：已关联且节点填写了到期日时由节点提醒，不重复
		if in.ExpireAt <= 0 || (in.ServerID != nil && nodeExpire[*in.ServerID]) {
			continue
		}
		exp := time.Unix(in.ExpireAt, 0).In(time.Local)
		date := exp.Format("2006-01-02")
		days := calendarDays(now.In(time.Local), time.Date(exp.Year(), exp.Month(), exp.Day(), 0, 0, 0, 0, time.Local))
		if m, ok := expireBracket(days); ok {
			s.remind(fmt.Sprintf("expire:instance:%d:%s:%d", in.ID, date, m), notifyMessage{ServerName: label, Type: "expire",
				Severity: expireSeverity(m), Message: "云实例" + expireText(date, days), Link: link}, now)
		}
	}
}

// fmtBytesSI 以十进制单位显示字节数（云厂商的流量包按 GB = 10⁹ 字节计）。
func fmtBytesSI(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	v := float64(n)
	i := 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	if v < 10 && i > 0 {
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.0f %s", v, units[i])
}
