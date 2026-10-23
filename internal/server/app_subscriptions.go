package server

// App 的订阅列表（设计 47，修订第 213 条）：GET /api/v1/app/subscriptions 与图标，只接受 App 设备凭证。
//
// 【安全】
//   - 只读（约束 5）：App 不能添加、修改或删除订阅，这里只有读取接口
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
