package server

import (
	"fmt"
	"net/http"
	"time"
)

// 订阅的每月预算（修订第 312 条，参考 Wallos）：人民币，0 表示不设预算。
// 本月预计扣费（本月内全部扣费日按当前汇率换算为人民币）超过预算时，每月提醒一次；缺少汇率的币种不计入。

const settingSubBudget = "subscription_budget"

// SubBudget 是订阅预算设置。
type SubBudget struct {
	MonthlyCents int64 `json:"monthly_cents"` // 每月预算（人民币分），0 为不设
}

// maxSubBudget 是预算上限：一千万元。
const maxSubBudget = 1_000_000_000

func (s *Server) subBudget() SubBudget {
	var b SubBudget
	if _, err := s.store.GetSetting(settingSubBudget, &b); err != nil {
		s.log.Warn("read subscription budget failed", "component", "subscription", "err", err)
	}
	return b
}

// handleGetSubBudget：GET /api/v1/settings/subscription-budget，admin。
func (s *Server) handleGetSubBudget(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.subBudget())
}

// handlePutSubBudget：PUT /api/v1/settings/subscription-budget，admin。{monthly_cents}，0 为不设预算。
func (s *Server) handlePutSubBudget(w http.ResponseWriter, r *http.Request) {
	var b SubBudget
	if err := decodeJSON(w, r, &b); err != nil {
		s.writeError(w, r, err)
		return
	}
	if b.MonthlyCents < 0 || b.MonthlyCents > maxSubBudget {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "monthly_cents", Message: "预算应在 0 ～ 10,000,000 之间"}}})
		return
	}
	if err := s.store.PutSetting(settingSubBudget, b, time.Now()); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "subscription.budget", Success: true, Details: map[string]any{"monthly_cents": b.MonthlyCents}})
	writeJSON(w, b)
}

// chargesIn 返回订阅在 [from, to)（本地日期）内的扣费次数，与 Web 的 chargesBetween 一致。
func chargesIn(sub Subscription, from, to time.Time) int {
	n := 0
	for t := nextCharge(sub, from); !t.IsZero() && t.Before(to) && n < 400; t = nextCharge(sub, t.AddDate(0, 0, 1)) {
		n++
		if sub.Cycle == "once" {
			break
		}
	}
	return n
}

// monthSpendCny 返回 now 所在月份的预计扣费（人民币分）与缺少汇率的币种。
func (s *Server) monthSpendCny(list []Subscription, now time.Time) (int64, []string) {
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	to := from.AddDate(0, 1, 0)
	s.fx.mu.Lock()
	rates := make(map[string]float64, len(s.fx.rates))
	for k, v := range s.fx.rates {
		rates[k] = v
	}
	s.fx.mu.Unlock()
	var total int64
	var missing []string
	for _, x := range list {
		n := chargesIn(x, from, to)
		if n == 0 {
			continue
		}
		cents := float64(int64(n) * x.PriceCents)
		if x.Currency != "CNY" {
			r, ok := rates[x.Currency]
			if !ok || r <= 0 {
				missing = append(missing, x.Currency)
				continue
			}
			cents *= r
		}
		total += int64(cents + 0.5)
	}
	return total, missing
}

// subscriptionBudgetReminder 在本月预计扣费超过预算时提醒（每月一次，经通知渠道，遵守告警开关与免打扰）。
func (s *Server) subscriptionBudgetReminder(list []Subscription, now time.Time) {
	b := s.subBudget()
	if b.MonthlyCents <= 0 {
		return
	}
	total, _ := s.monthSpendCny(list, now)
	if total <= b.MonthlyCents {
		return
	}
	l := s.lang()
	link := ""
	if s.publicURL != "" {
		link = s.publicURL + "/subscriptions"
	}
	yuan := func(c int64) string { return fmt.Sprintf("¥%.2f", float64(c)/100) }
	s.remind("subscription_budget:"+now.Format("2006-01"), notifyMessage{ServerName: l.t("订阅预算"), Type: "subscription",
		Severity: SeverityWarning, Link: link,
		Message: l.f("本月订阅预计扣费 %s，超出每月预算 %s（多 %s）", yuan(total), yuan(b.MonthlyCents), yuan(total-b.MonthlyCents))}, now)
}
