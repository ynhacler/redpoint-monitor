package server

// 通知的语言（设计 52.2，修订第 304 条）：通知跟随网站配置的语言，全站一种，各渠道与 App 推送相同。
// 文字以中文为源，和健康摘要一样按中文格式串查译文，没有译文时发中文。已保存的告警与事件不改写。
// 用户填写的名称、云厂商返回的错误不翻译。

import "strings"

// lang 返回网站配置的语言，用于生成通知与告警消息。
func (s *Server) lang() healthLang { return healthLang(s.siteSetting().Language) }

// t 翻译没有占位符的文字。
func (l healthLang) t(s string) string {
	if l != "en" {
		return s
	}
	if e, ok := healthEN[s]; ok {
		return e
	}
	if e, ok := notifyEN[s]; ok {
		return e
	}
	return s
}

// detail 翻译告警说明与检查错误。这些文字内部一直是中文（端口、代理等逻辑会解析），只在输出时替换其中的固定片段。
func (l healthLang) detail(s string) string {
	if l != "en" {
		return s
	}
	return detailEN.Replace(s)
}

// reach 翻译国内可达性结论。运营商名称只在这里替换：测速目标的名称由用户填写，不翻译。
func (l healthLang) reach(s string) string {
	if l != "en" {
		return s
	}
	return reachEN.Replace(s)
}

// withLang 设定渲染通知时用的语言，汇总包含的通知一并设定。
func (m notifyMessage) withLang(l healthLang) notifyMessage {
	m.Lang = l
	if len(m.Items) > 0 {
		items := make([]notifyMessage, len(m.Items))
		for i, x := range m.Items {
			items[i] = x.withLang(l)
		}
		m.Items = items
	}
	return m
}

var detailEN = strings.NewReplacer(
	"证书已过期或尚未生效", "Certificate expired or not yet valid",
	"证书不受信任（自签名或缺少中间证书）", "Certificate not trusted (self-signed or missing intermediate)",
	"证书中的域名与 ", "Certificate name does not match ", " 不匹配", "",
	"证书校验失败：", "Certificate verification failed: ",
	"最慢：", "slowest: ", "丢包最多：", "highest loss: ",
	" 端口 ", " port ", " 没有在监听", " not listening", " 没有在运行", " not running",
	"域名不存在或没有记录", "Domain not found or has no records", "DNS 解析失败：", "DNS lookup failed: ",
	"连接被拒绝（端口未开放或服务未运行）", "Connection refused (port closed or service not running)",
	"网络不可达", "Network unreachable", "超时", "Timed out", "重定向次数过多", "Too many redirects",
	"未知的检查类型", "Unknown check type", "状态码 ", "Status ", "（期望 ", " (expected ",
	"响应中没有找到", "Response does not contain ", "解析结果 ", "Lookup result ", " 不包含 ", " does not contain ",
	"没有 ", "No ", " 记录", " record", " 次", " times",
	"、", ", ", "；", "; ", "（", " (", "）", ")", "：", ": ",
)

var reachEN = strings.NewReplacer(
	"国内不可达（疑似被墙）", "Unreachable from mainland China (likely blocked)",
	" 国内不可达", " unreachable from mainland China", "不可达", " unreachable", "端口 ", "Port ",
	"国内线路不稳定", "Unstable route from mainland China", "国内可达", "Reachable from mainland China",
	"电信", "China Telecom", "联通", "China Unicom", "移动", "China Mobile", "、", ", ",
)

// notifyEN 是通知的英文译文，键为中文格式串，%s、%d 的个数与顺序要一致。
var notifyEN = map[string]string{
	// 标题与正文
	"✅ %d 台节点恢复上报：%s": "✅ %d nodes reporting again: %s", "%s %d 台节点同时离线：%s": "%s %d nodes offline at once: %s",
	"🌙 免打扰期间 %d 条告警通知": "🌙 %d alert notifications during quiet hours", "✅ %s 已恢复：%s": "✅ %s recovered: %s",
	"%s 仍未恢复 · %s %s": "%s Still not recovered · %s %s", "〰️ %s 状态频繁变化：%s": "〰️ %s is flapping: %s",
	"%s 状态已稳定，仍在告警 · %s %s": "%s Stable but still alerting · %s %s", "🔔 测试通知": "🔔 Test notification",
	"可能是面板网络或同一服务商故障": "Possibly a panel network problem or an outage at one provider", "全部：%s": "All: %s",
	"30 分钟内触发 %d 次，暂停这条告警的通知；稳定 30 分钟后恢复。": "Fired %d times in 30 minutes. Notifications for this alert are paused until it is stable for 30 minutes.",
	"已持续 %s": "Ongoing for %s", "开始时间 %s": "Started at %s", "持续时长 %s": "Lasted %s",
	"这是一条来自 VPS Monitor 的测试通知，收到说明渠道配置正确。": "This is a test notification from VPS Monitor. If you received it, the channel is set up correctly.",
	"%d 天 %d 小时": "%d d %d h", "%d 小时 %d 分钟": "%d h %d min", " 等": " etc.", "，": ", ",
	"免打扰时段（%s～%s）内的告警：": "Alerts during quiet hours (%s–%s):", "……另有 %d 条，详见告警页": "… and %d more, see the Alerts page",
	"……另有 %d 条未保存，详见告警页": "… and %d more not saved, see the Alerts page",

	// 告警消息
	"节点离线，已 %s未收到上报": "Node offline, no report for %s", "节点恢复上报": "Node reporting again",
	"CPU 使用率 %s（阈值 %s）": "CPU usage %s (threshold %s)", "内存使用率 %s（阈值 %s）": "Memory usage %s (threshold %s)",
	"磁盘 %s 使用率 %s（阈值 %s）": "Disk %s usage %s (threshold %s)", "Swap 使用率 %s（阈值 %s）": "Swap usage %s (threshold %s)",
	"负载为核数的 %.1f 倍（阈值 %.1f 倍）": "Load is %.1f× core count (threshold %.1f×)",
	"本周期流量已用 %s（阈值 %s）":        "Traffic used this cycle %s (threshold %s)", "预计周期结束用量为额度的 %s": "Forecast use at cycle end is %s of the limit",
	"进程没有在运行：%s": "Process not running: %s", "容器已停止：%s": "Container stopped: %s", "代理异常：%s": "Proxy problem: %s",
	"未知监听端口（不在端口基线中）：%s": "Unknown listening port (not in baseline): %s", "基线端口没有在监听：%s": "Baseline port not listening: %s",
	"容器反复重启（10 分钟内）：%s":         "Container restarting repeatedly (within 10 min): %s",
	"测速延迟 %.0f ms（阈值 %g ms），%s": "Latency %.0f ms (threshold %g ms), %s", "测速丢包率 %s（阈值 %s），%s": "Packet loss %s (threshold %s), %s",
	"Agent 时钟比面板%s %.0f 秒，请检查主机的时间同步（NTP）": "Agent clock is %s by %.0f s vs the panel; check host time sync (NTP)", "慢": "slow", "快": "fast",
	"%s（国内探针连续多轮探测失败，原因可能是封锁、运营商故障或路由问题）": "%s (mainland probes failed for several rounds; possible causes are blocking, carrier faults or routing issues)",

	// 服务监控与变更记录
	"网址": "URL", "端口": "Port", "域名解析": "DNS", "%s访问正常（%d ms）": "%s OK (%d ms)", "%s无法访问：%s（%s）": "%s unreachable: %s (%s)",
	"防火墙": "Firewall", "账号与权限组": "Accounts and groups", "重要文件": "Important files", "%s有 %d 项变化": "%s changes: %d",
	"有 %d 次来自新地址的登录": "Logins from new addresses: %d", "%s（详情见 Web 节点详情 · 安全）": "%s (details in Web: node details · Security)",

	// 到期与费用提醒
	"已于 %s 到期（%d 天前），请尽快续费或处理": "Expired on %s (%d days ago), please renew or act soon",
	"今天（%s）到期": "Expires today (%s)", "将于 %s 到期（%d 天后）": "Expires on %s (in %d days)",
	"云实例%s": "Cloud instance: %s", "SSL 证书%s": "SSL certificate: %s", "云账户“%s”": "Cloud account “%s”", "订阅“%s”": "Subscription “%s”", "证书 %s": "Certificate %s",
	"连续 24 小时同步失败": "Sync failing for 24 hours", "凭证失效或权限不足，已停止自动同步": "Credentials invalid or insufficient permissions; auto sync stopped", "%s：%s": "%s: %s",
	"%s 免费额度已用 %.0f%%（%s / %s），超出部分按量收费":    "%s free quota %.0f%% used (%s / %s); overage is billed by usage",
	"余额 %s %.2f，低于提醒阈值 %s %.2f，请及时充值以免实例停机": "Balance %s %.2f is below the alert threshold %s %.2f; top up to avoid instance shutdown",
	"本月已产生": "Spent this month", "本月预估": "Forecast this month", "%s %s %.2f，超过预算 %s %.2f": "%s %s %.2f, over budget %s %.2f",
	"流量包已用 %.0f%%（%s / %s）": "Traffic package %.0f%% used (%s / %s)", "%d 天后（%s）": "in %d days (%s)", "今天": "today", "将于%s扣费 %s %.2f": "Charges %s: %s %.2f",

	// 面板自身
	"⚠️ 全部 %d 台节点同时停止上报": "⚠️ All %d nodes stopped reporting at once",
	"优先检查面板一侧：网络、进程、反向代理。期间暂停离线告警，任一节点恢复上报后自动解除。": "Check the panel side first: network, process, reverse proxy. Offline alerts are paused until any node reports again.",
	"✅ 面板恢复接收上报（%d 台节点）": "✅ Panel receiving reports again (%d nodes)",
	"数据维护": "data maintenance", "告警评估": "alert evaluation", "云账户同步": "cloud account sync", "实时推送": "live updates",
	"到期提醒": "expiry reminders", "服务监控": "service monitoring", "面板心跳": "panel heartbeat", "版本同步": "release sync", "面板自身检查": "panel self-check",
	"后台任务反复出错：%s": "Background task keeps failing: %s", "后台任务运行正常：%s": "Background task running normally: %s",
	"相关功能可能暂时失效；在面板主机上执行 vpsmon-server diag 生成诊断包查看原因。": "Related features may not work for now. Run vpsmon-server diag on the panel host to create a diagnostic bundle.",
	"磁盘空间不足": "Low disk space", "磁盘空间已恢复": "Disk space recovered", "清理磁盘或缩短指标保留时间；空间耗尽后面板无法写入数据。": "Free up disk space or shorten metric retention. The panel cannot write data once the disk is full.",
	"数据库写入失败": "Database writes failing", "数据库写入已恢复": "Database writes recovered", "检查磁盘空间与数据目录权限；面板重启前未写入的上报会丢失。": "Check disk space and data directory permissions. Unwritten reports are lost if the panel restarts.",
	"App 推送失败": "App push failing", "App 推送已恢复": "App push recovered", "检查面板到 Push Relay 的网络与 --push-relay 设置；告警仍会通过其他渠道发送。": "Check the network from the panel to Push Relay and the --push-relay setting. Alerts are still sent through other channels.",
	"✅ 面板%s": "✅ Panel: %s", "⚠️ 面板异常：%s": "⚠️ Panel problem: %s", "%s。%s": "%s. %s",
	"10 分钟内崩溃 %d 次，已自动重启": "Crashed %d times in 10 minutes, restarted automatically", "连续 %d 次写入失败，上报暂存在内存中": "%d writes failed in a row; reports are held in memory",
	"连续 %d 次无法送达 Push Relay，App 收不到推送": "Could not reach Push Relay %d times in a row; the App is not receiving push", "数据目录所在磁盘剩余 %.1f%%（%s）": "%.1f%% free on the data directory disk (%s)",

	// 定期报告
	"周报": "Weekly report", "月报": "Monthly report", "📊 VPS Monitor %s（%s～%s）": "📊 VPS Monitor %s (%s–%s)",
	"%s %.0f%%（%s / %s，%s 重置）": "%s %.0f%% (%s / %s, resets %s)", "节点：共 %d 台，在线 %d": "Nodes: %d total, %d online", "，离线 %d（%s）": ", %d offline (%s)",
	"需要重启 %d 台（%s）": "%d need a reboot (%s)", "有安全更新 %d 台（%s）": "%d with security updates (%s)", "系统：%s": "System: %s",
	"告警：没有触发": "Alerts: none fired", "告警：触发 %d 次": "Alerts fired: %d", "（严重 %d 次）": " (%d critical)", "%s，最多：%s": "%s, most on: %s", "%s %d 次": "%s: %d", "已删除的节点": "deleted node",
	"离线累计：%s": "Total offline: %s", "本周期流量用量最高：": "Highest traffic use this cycle:", "%d 天内到期的节点：%s": "Nodes expiring within %d days: %s",
	"%s %s（%s）": "%s %s (%s)", "%d 天内扣费的订阅 %d 笔：%s": "Subscriptions charging within %d days (%d): %s", "%s（预估 %s）": "%s (forecast %s)", "云账户本月费用：%s": "Cloud costs this month: %s",
	"已过期 %d 天": "expired %d days ago", "今天到期": "expires today", "%s（%d 天后）": "%s (in %d days)",
}
