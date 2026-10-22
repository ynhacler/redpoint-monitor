package protocol

// 流媒体与网站解锁检测（设计 50，修订第 192 条）：从节点访问常用流媒体与网站，判断能否使用、所在地区。
// 检测项内置在 Agent 中（Agent 不接受面板下发的网址，约束 1）；面板只能请求“现在检测一次”：
//
//	Agent → 面板  请求头 X-Unlock: 1，表示支持检测（旧版 Agent 不带，面板据此提示升级）
//	面板 → Agent  响应头 X-Unlock-Request: <请求号>，有待处理的检测时下发
//	Agent         对每个新请求号只检测一次（后台进行，约 1 分钟内完成），结果随下一份上报带回（Report.Unlock）

// 请求头与响应头
const (
	HeaderUnlock        = "X-Unlock"
	HeaderUnlockRequest = "X-Unlock-Request"
	// UnlockVersion 是 Agent 支持的检测版本
	UnlockVersion = "1"
)

// 检测结果的状态
const (
	UnlockYes       = "yes"     // 可以使用
	UnlockNo        = "no"      // 不可用（地区不支持、被封锁）
	UnlockPartial   = "partial" // 部分可用（如 Netflix 只能看自制剧）
	UnlockCaptcha   = "captcha" // 可以访问但要求人机验证（如 Google 搜索）
	UnlockFailed    = "failed"  // 检测失败（网络错误、超时、页面结构变化）
	MaxUnlockItems  = 32
	maxUnlockString = 64
)

// UnlockResult 是一次检测的结果。
type UnlockResult struct {
	Request   string       `json:"request"`    // 面板下发的请求号
	CheckedAt int64        `json:"checked_at"` // 完成时间（Unix 秒，Agent 时钟）
	Items     []UnlockItem `json:"items"`
}

// UnlockItem 是一个检测项。
type UnlockItem struct {
	Service string `json:"service"`          // 检测项键，如 netflix、chatgpt
	Status  string `json:"status"`           // yes / no / partial / captcha / failed
	Region  string `json:"region,omitempty"` // 服务认定的地区（两位代码，如 JP）或货币（Steam），可能为空
	Note    string `json:"note,omitempty"`   // 补充说明（如失败原因），简短
}

// Valid 检查结果的格式（面板只保存格式正确的结果）。
func (u *UnlockResult) Valid() bool {
	if u == nil || u.Request == "" || len(u.Request) > maxUnlockString || len(u.Items) > MaxUnlockItems {
		return false
	}
	for _, it := range u.Items {
		switch it.Status {
		case UnlockYes, UnlockNo, UnlockPartial, UnlockCaptcha, UnlockFailed:
		default:
			return false
		}
		if it.Service == "" || len(it.Service) > maxUnlockString || len(it.Region) > maxUnlockString || len(it.Note) > 200 {
			return false
		}
	}
	return true
}
