package server

// App 的订阅（设计 47，修订第 213、217 条）：GET /api/v1/app/subscriptions 与图标，POST 新增；只接受 App 设备凭证。
//
// 【安全】
//   - 约束 5 的例外只有“记录订阅”（修订第 217、218 条）：AK 勾选了“允许记录订阅”（默认关闭）且授权范围为“全部节点”的设备
//     可以新增、修改、删除订阅（不能上传图标），不能改任何其他配置。每次写入都记审计日志（操作者为设备名）
//   - 订阅属于整个面板，不按节点划分：只有授权范围为“全部节点”的设备能看到，其他范围返回 limited（图标返回 404）

import (
	"net/http"
	"time"
)

type appSubscriptionsView struct {
	Limited bool           `json:"limited"`
	Items   []Subscription `json:"items"`
}

// fullScope 判断请求的 App 设备是否为“全部节点”授权。
func fullScope(r *http.Request) bool {
	d := info(r).device
	return d != nil && d.ScopeType == "all"
}

// handleAppSubscriptions：GET /api/v1/app/subscriptions，App 设备。排序与 Web 相同（下次扣费日在前）。
func (s *Server) handleAppSubscriptions(w http.ResponseWriter, r *http.Request) {
	out := appSubscriptionsView{Items: []Subscription{}}
	if !fullScope(r) {
		out.Limited = true
		writeJSON(w, out)
		return
	}
	list, err := s.store.ListSubscriptions()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	now := time.Now()
	for i := range list {
		list[i] = withSchedule(list[i], now)
	}
	sortSubscriptions(list)
	out.Items = append(out.Items, list...)
	writeJSON(w, out)
}

// handleAppSubscriptionLogo：GET /api/v1/app/subscriptions/{id}/logo，App 设备；非“全部节点”授权返回 404。
func (s *Server) handleAppSubscriptionLogo(w http.ResponseWriter, r *http.Request) {
	if !fullScope(r) {
		s.writeError(w, r, errorf(CodeNotFound, "没有上传图标"))
		return
	}
	s.handleSubscriptionLogo(w, r)
}

// handleAppCreateSubscription：POST /api/v1/app/subscriptions，accessAppSubs（修订第 217 条）。请求体与 Web 新增订阅相同。
func (s *Server) handleAppCreateSubscription(w http.ResponseWriter, r *http.Request) {
	s.createSubscription(w, r, "app")
}

// handleAppUpdateSubscription：PUT /api/v1/app/subscriptions/{id}，accessAppSubs（修订第 218 条）。整体替换，请求体与 Web 相同。
func (s *Server) handleAppUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	s.updateSubscription(w, r, "app")
}

// handleAppDeleteSubscription：DELETE /api/v1/app/subscriptions/{id}，accessAppSubs（修订第 218 条）。
func (s *Server) handleAppDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	s.deleteSubscription(w, r, "app")
}
