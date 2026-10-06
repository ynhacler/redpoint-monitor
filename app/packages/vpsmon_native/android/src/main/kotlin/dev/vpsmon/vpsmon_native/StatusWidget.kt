package dev.vpsmon.vpsmon_native

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.RemoteViews
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

// 桌面小组件（设计 1.5.4）：窄时显示计数与状态句，宽时显示服务器列表（行数随高度变化，最多 8 行）。
// 数据只来自 App 写入的快照；系统按 updatePeriodMillis（30 分钟）重绘，用于“可能已过期”提示。点按打开 App。
class StatusWidget : AppWidgetProvider() {
    override fun onUpdate(context: Context, manager: AppWidgetManager, ids: IntArray) {
        for (id in ids) render(context, manager, id)
    }

    override fun onAppWidgetOptionsChanged(context: Context, manager: AppWidgetManager, id: Int, newOptions: Bundle) {
        render(context, manager, id)
    }

    companion object {
        private const val STALE_SECONDS = 2 * 3600
        private val hhmm = SimpleDateFormat("HH:mm", Locale.ROOT)

        fun refreshAll(context: Context) {
            val m = AppWidgetManager.getInstance(context)
            for (id in m.getAppWidgetIds(ComponentName(context, StatusWidget::class.java))) render(context, m, id)
        }

        private fun statusColor(s: String) = when (s) {
            "ok" -> R.color.vpsmon_ok
            "warn" -> R.color.vpsmon_warn
            "bad", "offline" -> R.color.vpsmon_bad
            else -> R.color.vpsmon_muted_state // 维护中、待安装
        }

        fun render(context: Context, manager: AppWidgetManager, id: Int) {
            val opts = manager.getAppWidgetOptions(id)
            val width = opts.getInt(AppWidgetManager.OPTION_APPWIDGET_MIN_WIDTH, 110)
            val height = opts.getInt(AppWidgetManager.OPTION_APPWIDGET_MAX_HEIGHT, 110)
            val snap = WidgetStore.loadSnapshot(context)
            val now = System.currentTimeMillis() / 1000
            val small = width < 200
            val v = RemoteViews(context.packageName, if (small) R.layout.vpsmon_widget_small else R.layout.vpsmon_widget_list)

            // 点按打开 App
            context.packageManager.getLaunchIntentForPackage(context.packageName)?.let {
                it.flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP
                v.setOnClickPendingIntent(R.id.vpsmon_root,
                    PendingIntent.getActivity(context, 0, it, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE))
            }

            if (snap == null) {
                v.setViewVisibility(R.id.vpsmon_content, View.GONE)
                v.setViewVisibility(R.id.vpsmon_empty, View.VISIBLE)
                manager.updateAppWidget(id, v)
                return
            }
            v.setViewVisibility(R.id.vpsmon_content, View.VISIBLE)
            v.setViewVisibility(R.id.vpsmon_empty, View.GONE)

            v.setTextViewText(R.id.vpsmon_online, snap.online.toString())
            v.setTextViewText(R.id.vpsmon_offline, snap.offline.toString())
            v.setTextViewText(R.id.vpsmon_attention, snap.attention.toString())

            // 最后更新 HH:mm；超过 2 小时提示可能已过期
            val at = hhmm.format(Date(snap.updatedAt * 1000))
            val stale = now - snap.updatedAt > STALE_SECONDS
            v.setTextViewText(R.id.vpsmon_updated, if (stale) "$at · 可能已过期" else "更新 $at")
            v.setTextColor(R.id.vpsmon_updated, context.getColor(if (stale) R.color.vpsmon_warn else R.color.vpsmon_text_muted))

            if (small) {
                v.setTextViewText(R.id.vpsmon_summary, if (snap.attention == 0) "服务器状态正常" else "${snap.attention} 台需要关注")
                v.setTextColor(R.id.vpsmon_summary, context.getColor(if (snap.attention == 0) R.color.vpsmon_text else R.color.vpsmon_bad))
            } else {
                // 标题与表头约 64dp，每行约 22dp，底部告警约 20dp
                val rows = ((height - 84) / 22).coerceIn(1, 8)
                v.removeAllViews(R.id.vpsmon_rows)
                for (r in snap.rows.take(rows)) {
                    val row = RemoteViews(context.packageName, R.layout.vpsmon_widget_row)
                    row.setTextColor(R.id.vpsmon_row_dot, context.getColor(statusColor(r.status)))
                    row.setTextViewText(R.id.vpsmon_row_name, r.name)
                    row.setTextViewText(R.id.vpsmon_row_cpu, r.cpu)
                    row.setTextViewText(R.id.vpsmon_row_mem, r.mem)
                    row.setTextViewText(R.id.vpsmon_row_rx, if (r.rx == "—") r.rx else "↓${r.rx}")
                    v.addView(R.id.vpsmon_rows, row)
                }
                // 最近 24 小时内的告警（推送处理器写入）
                val a = WidgetStore.loadAlert(context)
                if (a != null && now - a.ts < 86400) {
                    v.setViewVisibility(R.id.vpsmon_alert, View.VISIBLE)
                    v.setTextViewText(R.id.vpsmon_alert, "● ${a.title}")
                    v.setTextColor(R.id.vpsmon_alert, context.getColor(if (a.severity == "critical") R.color.vpsmon_bad else R.color.vpsmon_warn))
                } else {
                    v.setViewVisibility(R.id.vpsmon_alert, View.GONE)
                }
            }
            manager.updateAppWidget(id, v)
        }
    }
}
