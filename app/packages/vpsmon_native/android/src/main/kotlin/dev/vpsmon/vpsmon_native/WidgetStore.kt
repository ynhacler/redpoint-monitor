package dev.vpsmon.vpsmon_native

import android.content.Context
import org.json.JSONObject

// 桌面小组件的数据（设计 1.5.4）：App 写入的概览快照与通知处理器记录的最近告警，保存在本 App 私有的 SharedPreferences。
// 【安全】只有名称、状态与格式化好的数字，没有凭证、私钥、IP、价格与供应商（快照由 app/lib/home_widget.dart 生成）。
object WidgetStore {
    private const val PREFS = "vpsmon_widget"
    private const val SNAPSHOT = "snapshot_v1"
    private const val ALERT = "alert_v1"

    data class Row(val id: Long, val name: String, val status: String, val cpu: String, val mem: String, val rx: String, val tx: String)
    data class Snapshot(val updatedAt: Long, val total: Int, val online: Int, val offline: Int, val attention: Int, val rows: List<Row>)
    data class Alert(val title: String, val severity: String, val ts: Long)

    private fun prefs(c: Context) = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    fun saveSnapshot(c: Context, json: String): Boolean {
        if (json.length > 64 * 1024) return false
        parse(json) ?: return false
        prefs(c).edit().putString(SNAPSHOT, json).apply()
        return true
    }

    fun clear(c: Context) {
        prefs(c).edit().remove(SNAPSHOT).remove(ALERT).apply()
    }

    fun saveAlert(c: Context, title: String, severity: String, ts: Long) {
        val j = JSONObject().put("title", title.take(120)).put("severity", severity).put("ts", ts)
        prefs(c).edit().putString(ALERT, j.toString()).apply()
    }

    fun loadSnapshot(c: Context): Snapshot? = prefs(c).getString(SNAPSHOT, null)?.let { parse(it) }

    fun loadAlert(c: Context): Alert? = prefs(c).getString(ALERT, null)?.let {
        try {
            val j = JSONObject(it)
            Alert(j.optString("title"), j.optString("severity", "warning"), j.optLong("ts"))
        } catch (_: Exception) {
            null
        }
    }

    private fun parse(json: String): Snapshot? = try {
        val j = JSONObject(json)
        val arr = j.optJSONArray("servers")
        val rows = (0 until (arr?.length() ?: 0)).map { i ->
            val r = arr!!.getJSONObject(i)
            Row(r.optLong("id"), r.optString("name"), r.optString("status"), r.optString("cpu", "—"), r.optString("mem", "—"),
                r.optString("rx", "—"), r.optString("tx", "—"))
        }
        Snapshot(j.getLong("updated_at"), j.optInt("total"), j.optInt("online"), j.optInt("offline"), j.optInt("attention"), rows)
    } catch (_: Exception) {
        null
    }
}
