# vpsmon_native

VPS Monitor App 的本地 Android 插件（不发布到 pub.dev）：

- `dev.vpsmon/notify`：显示解密后的告警通知（按严重程度分通知渠道，按监控中心分组）
- `dev.vpsmon/widget`：保存桌面小组件快照并刷新小组件（设计 1.5.4）

做成插件而不是写在 MainActivity 中，是为了让 FCM 的后台处理器（独立的 Flutter 引擎）也能调用（设计 30.3.3）。
iOS 对应的部分在 `app/ios`（Notification Service Extension 与 WidgetKit 扩展）。
