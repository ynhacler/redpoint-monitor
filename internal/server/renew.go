package server

// 已续费（修订第 224 条）：节点的到期日按计费周期、订阅的结束日期按订阅周期往后推一个周期，从原来的日期算起（不是从今天）。
// Web 管理员与 AK 允许记录订阅的 App 设备都可以操作（App 走 accessAppSubs，约束 5 的唯一写入例外，与记录订阅同一开关）；
// 每次续费记审计日志。一次性付费、没有到期日 / 结束日期的不能续费。

import (
	"net/http"
	"time"
)

// periodMonths 是节点计费周期对应的月数（one_time 不能续费）。
var periodMonths = map[string]int{"monthly": 1, "quarterly": 3, "semiannually": 6, "annually": 12, "biennially": 24, "triennially": 36}

// renewServerDate 把节点的到期日往后推一个计费周期；不能续费时返回说明。
func renewServerDate(expire, period string) (string, error) {
	if expire == "" {
		return "", errorf(CodeConflict, "节点没有填写到期日，无法续费：先在“编辑”中填写到期日与计费周期")
	}
	n, ok := periodMonths[period]
	if !ok {
		return "", errorf(CodeConflict, "节点的计费周期不是按月 / 季 / 年等周期续费（一次性或未填写），无法自动顺延")
	}
	d, err := time.Parse("2006-01-02", expire)
	if err != nil {
		return "", errorf(CodeConflict, "节点的到期日格式不正确")
	}
	return addMonthsClamped(d, n).Format("2006-01-02"), nil
}

// renewSubEnd 把订阅的结束日期往后推一个订阅周期；不能续费时返回说明。
func renewSubEnd(end, cycle string) (string, error) {
	if end == "" {
		return "", errorf(CodeConflict, "这个订阅没有结束日期：会按周期自动扣费，不需要标记续费")
	}
	d, err := time.Parse("2006-01-02", end)
	if err != nil {
		return "", errorf(CodeConflict, "结束日期格式不正确")
	}
	switch cycle {
	case "once":
		return "", errorf(CodeConflict, "一次性付费的订阅不能续费")
	case "weekly":
		return d.AddDate(0, 0, 7).Format("2006-01-02"), nil
	}
	return addMonthsClamped(d, cycleMonths(cycle)).Format("2006-01-02"), nil
}

// renewServer 把节点的到期日顺延一个计费周期（Web 管理员与 App 设备共用）。
func (s *Server) renewServer(w http.ResponseWriter, r *http.Request, actor string) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	row, err := s.store.GetServer(id)
	if err != nil {
		s.writeError(w, r, errorf(CodeNotFound, "节点不存在或已删除"))
		return
	}
	next, err := renewServerDate(row.ExpireDate, row.BillingPeriod)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.store.SetServerExpireDate(id, next); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: actor, ActorID: appActor(r), Action: "server.renew", TargetType: "server", TargetID: id, Success: true,
		Details: map[string]any{"from": row.ExpireDate, "to": next, "billing_period": row.BillingPeriod}})
	writeJSON(w, map[string]string{"expire_date": next, "previous": row.ExpireDate})
}

// renewSubscription 把订阅的结束日期顺延一个订阅周期（Web 管理员与 App 设备共用）。
func (s *Server) renewSubscription(w http.ResponseWriter, r *http.Request, actor string) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	x, err := s.store.GetSubscription(id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	next, err := renewSubEnd(x.EndDate, x.Cycle)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	prev := x.EndDate
	x.EndDate = next
	now := time.Now()
	if err := s.store.SaveSubscription(&x, now); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: actor, ActorID: appActor(r), Action: "subscription.renew", Success: true,
		Details: map[string]any{"id": id, "name": x.Name, "from": prev, "to": next}})
	writeJSON(w, withSchedule(x, now))
}

// handleRenewServer：POST /api/v1/servers/{id}/renew，admin。
func (s *Server) handleRenewServer(w http.ResponseWriter, r *http.Request) {
	s.renewServer(w, r, "admin")
}

// handleRenewSubscription：POST /api/v1/subscriptions/{id}/renew，admin。
func (s *Server) handleRenewSubscription(w http.ResponseWriter, r *http.Request) {
	s.renewSubscription(w, r, "admin")
}

// handleAppRenewServer：POST /api/v1/app/servers/{id}/renew，accessAppSubs（修订第 224 条）。
func (s *Server) handleAppRenewServer(w http.ResponseWriter, r *http.Request) {
	s.renewServer(w, r, "app")
}

// handleAppRenewSubscription：POST /api/v1/app/subscriptions/{id}/renew，accessAppSubs（修订第 224 条）。
func (s *Server) handleAppRenewSubscription(w http.ResponseWriter, r *http.Request) {
	s.renewSubscription(w, r, "app")
}
