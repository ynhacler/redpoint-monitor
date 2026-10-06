package dev.vpsmon.vpsmon_native

import android.content.Context
import io.flutter.embedding.engine.plugins.FlutterPlugin
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel

// VPS Monitor 的 Android 原生部分（设计 1.5.4、30.3.3）：告警通知与桌面小组件。
// 做成插件是为了让 FCM 后台处理器（独立的 Flutter 引擎）也能注册到这两个通道。
class VpsmonNativePlugin : FlutterPlugin, MethodChannel.MethodCallHandler {
    private lateinit var context: Context
    private lateinit var notify: MethodChannel
    private lateinit var widget: MethodChannel

    override fun onAttachedToEngine(binding: FlutterPlugin.FlutterPluginBinding) {
        context = binding.applicationContext
        notify = MethodChannel(binding.binaryMessenger, "dev.vpsmon/notify").also { it.setMethodCallHandler(this) }
        widget = MethodChannel(binding.binaryMessenger, "dev.vpsmon/widget").also { it.setMethodCallHandler(this) }
    }

    override fun onDetachedFromEngine(binding: FlutterPlugin.FlutterPluginBinding) {
        notify.setMethodCallHandler(null)
        widget.setMethodCallHandler(null)
    }

    override fun onMethodCall(call: MethodCall, result: MethodChannel.Result) {
        try {
            when (call.method) {
                "show" -> {
                    AlertNotifier.show(
                        context,
                        id = call.argument<Int>("id") ?: 0,
                        title = call.argument<String>("title") ?: "",
                        body = call.argument<String>("body") ?: "",
                        severity = call.argument<String>("severity") ?: "",
                        centerId = call.argument<String>("center_id") ?: "",
                        serverId = (call.argument<Number>("server_id"))?.toLong(),
                    )
                    result.success(null)
                }
                "update" -> {
                    val json = call.arguments as? String
                    if (json == null || !WidgetStore.saveSnapshot(context, json)) {
                        result.error("widget", "小组件数据写入失败", null)
                        return
                    }
                    StatusWidget.refreshAll(context)
                    result.success(null)
                }
                "clear" -> {
                    WidgetStore.clear(context)
                    StatusWidget.refreshAll(context)
                    result.success(null)
                }
                "alert" -> {
                    WidgetStore.saveAlert(
                        context,
                        title = call.argument<String>("title") ?: "",
                        severity = call.argument<String>("severity") ?: "warning",
                        ts = (call.argument<Number>("ts"))?.toLong() ?: (System.currentTimeMillis() / 1000),
                    )
                    StatusWidget.refreshAll(context)
                    result.success(null)
                }
                else -> result.notImplemented()
            }
        } catch (e: Exception) {
            result.error("native", e.message, null)
        }
    }
}
