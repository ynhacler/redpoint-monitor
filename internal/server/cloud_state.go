package server

// 云实例状态归一化（修订第 232 条，需求说明书 CLOUD-01）：各云厂商的状态写法不同（running / Running / RUNNING、SHUTDOWN 等），
// 面板统一换成一组固定的值，Web 与 App 按它显示中文；原样的 state 仍然返回，用于详情提示与排错。

import "strings"

// cloudStateStale 是实例状态视为可能过期的时长：两个实例同步周期（cloudInstancesEvery）。
const cloudStateStale = 2 * cloudInstancesEvery

// cloudStates 把各厂商的状态（小写、去掉空格与下划线）映射为归一化的状态。
// AWS EC2 / Lightsail：pending running stopping stopped shutting-down terminated rebooting；
// 阿里云 ECS / 轻量：Pending Starting Running Stopping Stopped Resetting Upgrading Disabled Expired Expiring Deleting；
// 腾讯云 CVM / 轻量：PENDING LAUNCH_FAILED RUNNING STOPPED STARTING STOPPING REBOOTING SHUTDOWN TERMINATING DELETING FREEZING ISOLATED；
// Oracle：PROVISIONING RUNNING STARTING STOPPING STOPPED CREATING_IMAGE TERMINATING TERMINATED。
var cloudStates = map[string]string{
	"running": "running", "active": "running",
	"stopped": "stopped", "shutdown": "stopped", "terminated": "stopped", "shutoff": "stopped",
	"starting": "starting", "pending": "pending", "provisioning": "pending", "creating": "pending", "creatingimage": "pending",
	"stopping": "stopping", "shutting-down": "stopping", "shuttingdown": "stopping",
	"rebooting": "rebooting", "resetting": "rebooting", "upgrading": "rebooting", "restarting": "rebooting",
	"expired": "expired", "expiring": "expired", "isolated": "expired", "disabled": "expired",
	"freezing": "frozen", "frozen": "frozen",
	"deleting": "deleting", "terminating": "deleting", "released": "deleting",
	"launchfailed": "error", "failed": "error", "error": "error",
}

// normalizeCloudState 返回归一化的状态；不认识的为 unknown。
func normalizeCloudState(raw string) string {
	k := strings.NewReplacer("_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(raw)))
	if v, ok := cloudStates[k]; ok {
		return v
	}
	return "unknown"
}
