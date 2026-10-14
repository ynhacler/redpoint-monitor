package dev.vpsmon.app

import io.flutter.embedding.android.FlutterFragmentActivity

// 通知与桌面小组件的原生部分在本地插件 vpsmon_native 中（后台推送处理器也能使用）。
// 生物识别锁（local_auth，设计 1.5.11）要求 FragmentActivity。
class MainActivity : FlutterFragmentActivity()
