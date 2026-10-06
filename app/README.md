# VPS Monitor App

Flutter App（iOS / Android），只读查看自建面板上的服务器，接收端到端加密的告警推送（设计 12、30）。

```bash
make app-setup   # 依赖
make app-run     # 运行（选择模拟器）
make app-apk     # Android 调试包
make ios-push-check  # iOS 推送解密与面板测试向量互通
```

本地面板：`make dev`；iOS 模拟器填 `http://127.0.0.1:8080`，Android 模拟器填 `http://10.0.2.2:8080`
（只有调试构建允许这些明文地址）。

## 目录

- `lib/`：界面与逻辑；`fcm.dart` Android 推送，`home_widget.dart` 桌面小组件快照
- `packages/vpsmon_native/`：Android 原生部分（告警通知、桌面小组件），本地插件，后台推送处理器也能使用
- `ios/NotificationService/`：iOS 推送解密扩展；`ios/VpsmonWidget/`：iOS 小组件

## 推送凭证（只在官方构建中）

系统推送只有 App 用户有，经官方 Relay 发出；自建面板的用户不需要任何推送凭证（设计 30.2）。

- Android：把 Firebase 项目的 `google-services.json` 放到 `android/app/`（不提交）；没有时推送自动关闭
- 发布签名：`android/key.properties`（不提交），字段 storeFile / storePassword / keyAlias / keyPassword
