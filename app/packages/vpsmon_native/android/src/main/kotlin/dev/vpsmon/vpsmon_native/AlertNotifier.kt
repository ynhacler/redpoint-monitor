package dev.vpsmon.vpsmon_native

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build

// 告警通知（设计 15、30.3.3）：内容是 App 用本机私钥解密后的明文，只用于显示这条通知。
// 按严重程度分三个通知渠道，用户可在系统设置中分别调整；同一监控中心的通知归为一组。
object AlertNotifier {
    private const val CRITICAL = "vpsmon_alert_critical"
    private const val WARNING = "vpsmon_alert_warning"
    private const val INFO = "vpsmon_alert_info"

    private fun ensureChannels(nm: NotificationManager) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        nm.createNotificationChannel(NotificationChannel(CRITICAL, "严重告警", NotificationManager.IMPORTANCE_HIGH).apply {
            description = "服务器离线、资源耗尽等需要立即处理的告警"
        })
        nm.createNotificationChannel(NotificationChannel(WARNING, "警告", NotificationManager.IMPORTANCE_DEFAULT).apply {
            description = "接近阈值、流量将超、即将到期等"
        })
        nm.createNotificationChannel(NotificationChannel(INFO, "提示与恢复", NotificationManager.IMPORTANCE_LOW).apply {
            description = "告警恢复、每日摘要等"
        })
    }

    fun show(context: Context, id: Int, title: String, body: String, severity: String, centerId: String, serverId: Long?) {
        val nm = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        ensureChannels(nm)
        val channel = when (severity) {
            "critical" -> CRITICAL
            "warning" -> WARNING
            else -> INFO
        }
        // 点按打开 App（附带中心与节点，App 据此打开对应详情，设计 15.1）
        val launch = context.packageManager.getLaunchIntentForPackage(context.packageName)?.apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP
            putExtra("center_id", centerId)
            if (serverId != null) putExtra("server_id", serverId)
        }
        val pi = launch?.let {
            PendingIntent.getActivity(context, id, it, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        }
        @Suppress("DEPRECATION")
        val b = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) Notification.Builder(context, channel) else Notification.Builder(context)
        b.setSmallIcon(R.drawable.vpsmon_stat_alert)
            .setContentTitle(title)
            .setContentText(body)
            .setStyle(Notification.BigTextStyle().bigText(body))
            .setAutoCancel(true)
            .setWhen(System.currentTimeMillis())
            .setShowWhen(true)
            .setCategory(if (severity == "critical") Notification.CATEGORY_ALARM else Notification.CATEGORY_STATUS)
            // 锁屏只显示“服务器告警”，不显示节点名称与内容（隐私，设计 1.5.12）
            .setVisibility(Notification.VISIBILITY_PRIVATE)
            .setPublicVersion(
                (if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) Notification.Builder(context, channel) else @Suppress("DEPRECATION") Notification.Builder(context))
                    .setSmallIcon(R.drawable.vpsmon_stat_alert)
                    .setContentTitle("服务器告警")
                    .build()
            )
        if (centerId.isNotEmpty()) b.setGroup(centerId)
        if (pi != null) b.setContentIntent(pi)
        b.setColor(context.getColor(
            if (severity == "critical") R.color.vpsmon_bad else if (severity == "warning") R.color.vpsmon_warn else R.color.vpsmon_ok))
        nm.notify(id, b.build())
    }
}
