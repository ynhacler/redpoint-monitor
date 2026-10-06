// 手机桌面小组件（设计 1.5.4）：小 / 中 / 大三种尺寸，显示 App 最近一次刷新的概览。
//
// 小组件不联网、不保存凭证：数据来自 App 写入 App Group 的快照；系统按刷新预算重新读取（通常 15～60 分钟），
// 因此始终显示“更新 HH:mm”，超过 2 小时标为“可能已过期”。点按打开 App 获取实时数据。
import SwiftUI
import WidgetKit

struct Entry: TimelineEntry {
    let date: Date
    let snapshot: WidgetSnapshot?
    let alert: WidgetAlert?
}

struct Provider: TimelineProvider {
    func placeholder(in context: Context) -> Entry { Entry(date: .now, snapshot: .sample, alert: nil) }

    func getSnapshot(in context: Context, completion: @escaping (Entry) -> Void) {
        completion(Entry(date: .now, snapshot: context.isPreview ? .sample : WidgetShared.loadSnapshot(), alert: WidgetShared.loadAlert()))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<Entry>) -> Void) {
        let e = Entry(date: .now, snapshot: WidgetShared.loadSnapshot(), alert: WidgetShared.loadAlert())
        // 内容只在 App / 通知扩展写入时变化（它们会主动请求刷新）；这里每 30 分钟重排一次，用于“已过期”提示
        completion(Timeline(entries: [e], policy: .after(.now.addingTimeInterval(30 * 60))))
    }
}

extension WidgetSnapshot {
    static let sample = WidgetSnapshot(updated_at: Int(Date().timeIntervalSince1970), total: 4, online: 3, offline: 1, attention: 1, servers: [
        .init(id: 1, name: "Oracle-SG", status: "offline", cpu: "—", mem: "—", rx: "—", tx: "—"),
        .init(id: 2, name: "DMIT-HK", status: "ok", cpu: "18%", mem: "43%", rx: "12 MB/s", tx: "2 MB/s"),
        .init(id: 3, name: "Zoro-JP", status: "ok", cpu: "9%", mem: "31%", rx: "1 MB/s", tx: "300 KB/s"),
        .init(id: 4, name: "US-DMIT", status: "ok", cpu: "27%", mem: "51%", rx: "4 MB/s", tx: "1 MB/s"),
    ])
}

func statusColor(_ s: String) -> Color {
    switch s {
    case "ok": return Tokens.ok
    case "warn": return Tokens.warn
    case "bad", "offline": return Tokens.bad
    default: return Tokens.mutedState // 维护中、待安装
    }
}

struct Dot: View {
    let status: String
    var body: some View { Circle().fill(statusColor(status)).frame(width: 8, height: 8) }
}

/// 设计 1.5.4：最后更新 HH:mm（24 小时制，不随地区变成 AM / PM）
private let hhmm: DateFormatter = {
    let f = DateFormatter()
    f.locale = Locale(identifier: "en_US_POSIX")
    f.dateFormat = "HH:mm"
    return f
}()

/// 底部：更新时间；超过 2 小时提示可能已过期
struct UpdatedLine: View {
    let snapshot: WidgetSnapshot
    let now: Date
    var body: some View {
        let at = Date(timeIntervalSince1970: TimeInterval(snapshot.updated_at))
        let stale = now.timeIntervalSince(at) > 2 * 3600
        let hm = hhmm.string(from: at)
        Text(stale ? "\(hm) · 可能已过期" : "更新 \(hm)")
            .font(.system(size: Tokens.fontXs))
            .foregroundStyle(stale ? Tokens.warn : Tokens.textMuted)
    }
}

struct PlaceholderView: View {
    var body: some View {
        VStack(spacing: Tokens.space1) {
            Text("VPS Monitor").font(.system(size: Tokens.fontSm, weight: .semibold))
            Text("打开 App 以更新").font(.system(size: Tokens.fontXs)).foregroundStyle(Tokens.textMuted)
        }
    }
}

struct SmallView: View {
    let s: WidgetSnapshot
    let now: Date
    var body: some View {
        VStack(alignment: .leading, spacing: Tokens.space1) {
            count("ok", s.online)
            count("bad", s.offline)
            count("warn", s.attention)
            Spacer(minLength: 0)
            Text(s.attention == 0 ? "服务器状态正常" : "\(s.attention) 台需要关注")
                .font(.system(size: Tokens.fontSm, weight: .semibold))
                .foregroundStyle(s.attention == 0 ? Tokens.text : Tokens.bad)
            UpdatedLine(snapshot: s, now: now)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    func count(_ status: String, _ n: Int) -> some View {
        HStack(spacing: Tokens.space2) {
            Dot(status: status)
            Text("\(n)").font(.system(size: Tokens.fontLg, weight: .semibold)).monospacedDigit()
        }
    }
}

struct RowView: View {
    let r: WidgetSnapshot.Row
    let showNet: Bool
    var body: some View {
        HStack(spacing: Tokens.space2) {
            Dot(status: r.status)
            Text(r.name).font(.system(size: Tokens.fontSm)).lineLimit(1).frame(maxWidth: .infinity, alignment: .leading)
            Text(r.cpu).frame(width: 36, alignment: .trailing)
            Text(r.mem).frame(width: 36, alignment: .trailing)
            if showNet { Text(r.rx == "—" ? r.rx : "↓\(r.rx)").lineLimit(1).frame(width: 70, alignment: .trailing) }
        }
        .font(.system(size: Tokens.fontXs).monospacedDigit())
    }
}

struct ListView: View {
    let s: WidgetSnapshot
    let alert: WidgetAlert?
    let now: Date
    let rows: Int
    let showNet: Bool
    var body: some View {
        VStack(alignment: .leading, spacing: Tokens.space1) {
            HStack(spacing: Tokens.space2) {
                Dot(status: "ok"); Text("\(s.online)")
                Dot(status: "bad"); Text("\(s.offline)")
                Dot(status: "warn"); Text("\(s.attention)")
                Spacer()
                UpdatedLine(snapshot: s, now: now)
            }
            .font(.system(size: Tokens.fontSm, weight: .semibold).monospacedDigit())
            HStack(spacing: Tokens.space2) {
                Text("服务器").frame(maxWidth: .infinity, alignment: .leading).padding(.leading, 10)
                Text("CPU").frame(width: 36, alignment: .trailing)
                Text("内存").frame(width: 36, alignment: .trailing)
                if showNet { Text("下载").frame(width: 70, alignment: .trailing) }
            }
            .font(.system(size: Tokens.fontXs))
            .foregroundStyle(Tokens.textMuted)
            ForEach(s.servers.prefix(rows)) { RowView(r: $0, showNet: showNet) }
            Spacer(minLength: 0)
            // 最近 24 小时内的告警（通知扩展写入）
            if let a = alert, now.timeIntervalSince1970 - Double(a.ts) < 86400 {
                HStack(spacing: Tokens.space1) {
                    Dot(status: a.severity == "critical" ? "bad" : "warn")
                    Text(a.title).lineLimit(1)
                }
                .font(.system(size: Tokens.fontXs))
                .foregroundStyle(Tokens.textMuted)
            }
        }
    }
}

struct WidgetView: View {
    @Environment(\.widgetFamily) var family
    let entry: Entry
    var body: some View {
        Group {
            if let s = entry.snapshot {
                switch family {
                case .systemSmall: SmallView(s: s, now: entry.date)
                case .systemMedium: ListView(s: s, alert: nil, now: entry.date, rows: 4, showNet: true)
                default: ListView(s: s, alert: entry.alert, now: entry.date, rows: 8, showNet: true)
                }
            } else {
                PlaceholderView()
            }
        }
        .containerBackground(Tokens.surface, for: .widget)
    }
}

@main
struct VpsmonWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: WidgetShared.kind, provider: Provider()) { WidgetView(entry: $0) }
            .configurationDisplayName("VPS Monitor")
            .description("服务器状态概览；数据随 App 刷新，点按打开 App 查看实时数据。")
            .supportedFamilies([.systemSmall, .systemMedium, .systemLarge])
    }
}
