package server

// 告警分组（修订第 318 条）：通知渠道可以只接收其中几组，如 Telegram 只收“离线”与“到期”。
// 分组按通知的 Type 归类；没有列出的类型（Agent 时钟、变更通知等）归入“其他”。
// 面板自检、测试通知与定期报告不分组，所有渠道都发；订阅提醒按订阅推送设置的渠道发送，不看分组。

// alertGroupKeys 是全部分组，顺序即 Web 上的显示顺序。
var alertGroupKeys = []string{"offline", "resource", "traffic", "expire", "network", "service", "port", "cloud", "other"}

var alertGroupByType = map[string]string{
	AlertOffline: "offline",
	AlertCPU:     "resource", AlertMemory: "resource", AlertDisk: "resource", AlertSwap: "resource", AlertLoad: "resource",
	AlertTraffic: "traffic", AlertTrafficForecast: "traffic", "cloud_traffic": "traffic",
	"expire": "expire", "ssl_expire": "expire", "ssl_invalid": "expire",
	AlertProbeLatency: "network", AlertProbeLoss: "network", AlertReach: "network",
	AlertProcess: "service", AlertContainer: "service", AlertContainerLoop: "service", AlertProxy: "service", "service": "service",
	AlertPortNew: "port", AlertPortGone: "port",
	"cloud_balance": "cloud", "cloud_cost": "cloud", "cloud_sync": "cloud", "cloud_instance": "cloud",
}

// alertGroupOf 返回通知类型所属的分组。
func alertGroupOf(typ string) string {
	if g, ok := alertGroupByType[typ]; ok {
		return g
	}
	return "other"
}
