// 面板数据模型（契约见 api/openapi.yaml 的 ServerView、TrafficView、MetricPoint、Silence）。
// 只解析 App 用到的字段；未知字段忽略，面板新增字段不影响旧版 App（设计 19.0.2）。
//
// 每个模型都保留原始 JSON（raw），离线缓存直接保存它，读取时用同一套解析，避免缓存格式与接口不一致。

int? _int(Object? v) => (v as num?)?.toInt();
double? _dbl(Object? v) => (v as num?)?.toDouble();

class DiskInfo {
  DiskInfo({required this.mount, required this.device, required this.total, required this.used, required this.usage, this.available});
  final String mount;
  final String device;
  final int total;
  final int used;
  final double usage;
  final int? available;

  factory DiskInfo.fromJson(Map<String, dynamic> j) => DiskInfo(
        mount: j['mount'] as String,
        device: (j['device'] as String?) ?? '',
        total: _int(j['total'])!,
        used: _int(j['used'])!,
        usage: _dbl(j['usage'])!,
        available: _int(j['available']),
      );
}

class Latest {
  Latest({required this.cpu, required this.mem, required this.memUsed, required this.memTotal, required this.disks,
      required this.rx, required this.tx, required this.load1, required this.cores, this.uptime});
  final double cpu;
  final double mem;
  final int memUsed;
  final int memTotal;
  final List<DiskInfo> disks;
  final int rx;
  final int tx;
  final double load1;
  final int cores;
  final int? uptime;

  factory Latest.fromJson(Map<String, dynamic> j) {
    final cpu = j['cpu'] as Map<String, dynamic>;
    final mem = j['memory'] as Map<String, dynamic>;
    final nets = (j['network'] as List<dynamic>?) ?? const [];
    return Latest(
      cpu: _dbl(cpu['usage'])!,
      cores: _int(cpu['cores']) ?? 0,
      load1: _dbl(cpu['load1']) ?? 0,
      mem: _dbl(mem['usage'])!,
      memUsed: _int(mem['used'])!,
      memTotal: _int(mem['total'])!,
      disks: ((j['disk'] as List<dynamic>?) ?? const []).map((d) => DiskInfo.fromJson(d as Map<String, dynamic>)).toList(),
      rx: nets.fold<int>(0, (a, n) => a + _int((n as Map<String, dynamic>)['rx_speed'])!),
      tx: nets.fold<int>(0, (a, n) => a + _int((n as Map<String, dynamic>)['tx_speed'])!),
      uptime: _int((j['system'] as Map<String, dynamic>?)?['uptime']),
    );
  }
}

class Forecast {
  Forecast({required this.daily, required this.total, required this.over});
  final int daily;
  final int total;
  final bool over;
}

class TrafficView {
  TrafficView({required this.cycleStart, required this.cycleEnd, required this.used, required this.limit, required this.unit, this.forecast});
  final String cycleStart;

  /// 下一周期开始日（不含），YYYY-MM-DD
  final String cycleEnd;
  final int used;

  /// 0 表示不限
  final int limit;
  final String unit;
  final Forecast? forecast;

  factory TrafficView.fromJson(Map<String, dynamic> j) {
    final f = j['forecast'] as Map<String, dynamic>?;
    return TrafficView(
      cycleStart: j['cycle_start'] as String,
      cycleEnd: j['cycle_end'] as String,
      used: _int(j['used'])!,
      limit: _int(j['limit'])!,
      unit: (j['unit'] as String?) ?? 'decimal',
      forecast: f == null ? null : Forecast(daily: _int(f['daily'])!, total: _int(f['total'])!, over: f['over'] as bool),
    );
  }
}

class AlertBrief {
  AlertBrief({required this.type, required this.severity, required this.message, required this.value, required this.firedAt, required this.silenced});
  final String type;
  final String severity;
  final String message;
  final double value;
  final int firedAt;
  final bool silenced;

  factory AlertBrief.fromJson(Map<String, dynamic> j) => AlertBrief(
        type: j['type'] as String,
        severity: j['severity'] as String,
        message: j['message'] as String,
        value: _dbl(j['value']) ?? 0,
        firedAt: _int(j['fired_at']) ?? 0,
        silenced: (j['silenced'] as bool?) ?? false,
      );
}

class Silence {
  Silence({required this.id, required this.kind, this.endsAt, required this.createdBy});
  final int id;
  final String kind;

  /// null 表示直到手动结束
  final int? endsAt;
  final String createdBy;

  factory Silence.fromJson(Map<String, dynamic> j) =>
      Silence(id: _int(j['id'])!, kind: j['kind'] as String, endsAt: _int(j['ends_at']), createdBy: (j['created_by'] as String?) ?? '');
}

class ServerView {
  ServerView({required this.raw, required this.id, required this.name, required this.status, required this.group, required this.country,
      required this.lastSeenAt, required this.traffic, required this.alerts, this.latest, this.maintenance, this.muted, this.expireDate = ''});
  final Map<String, dynamic> raw;
  final int id;
  final String name;

  /// online / unknown / offline / pending
  final String status;
  final String group;

  /// ISO 3166-1 两位国家代码，可为空
  final String country;
  final int lastSeenAt;
  final Latest? latest;
  final TrafficView traffic;
  final List<AlertBrief> alerts;
  final Silence? maintenance;
  final Silence? muted;
  final String expireDate;

  factory ServerView.fromJson(Map<String, dynamic> j) {
    final latest = j['latest'] as Map<String, dynamic>?;
    final m = j['maintenance'] as Map<String, dynamic>?;
    final mu = j['muted'] as Map<String, dynamic>?;
    return ServerView(
      raw: j,
      id: _int(j['id'])!,
      name: j['name'] as String,
      status: j['status'] as String,
      group: (j['group'] as String?) ?? '',
      country: (j['country'] as String?) ?? '',
      lastSeenAt: _int(j['last_seen_at']) ?? 0,
      latest: latest == null ? null : Latest.fromJson(latest),
      traffic: TrafficView.fromJson(j['traffic'] as Map<String, dynamic>),
      alerts: ((j['alerts'] as List<dynamic>?) ?? const []).map((a) => AlertBrief.fromJson(a as Map<String, dynamic>)).toList(),
      maintenance: m == null ? null : Silence.fromJson(m),
      muted: mu == null ? null : Silence.fromJson(mu),
      expireDate: (j['expire_date'] as String?) ?? '',
    );
  }
}

/// 历史曲线的一个点（GET /servers/{id}/metrics/history）。
class MetricPoint {
  MetricPoint({required this.ts, required this.cpu, required this.mem, required this.disk, required this.rx, required this.tx});
  final int ts;
  final double cpu;

  /// 内存使用率 0～100
  final double mem;

  /// 磁盘使用率 0～100
  final double disk;
  final int rx;
  final int tx;

  factory MetricPoint.fromJson(Map<String, dynamic> j) {
    final memTotal = _int(j['mem_total']) ?? 0;
    final diskTotal = _int(j['disk_total']) ?? 0;
    return MetricPoint(
      ts: _int(j['ts'])!,
      cpu: _dbl(j['cpu']) ?? 0,
      mem: memTotal > 0 ? (_int(j['mem_used'])! / memTotal) * 100 : 0,
      disk: diskTotal > 0 ? (_int(j['disk_used'])! / diskTotal) * 100 : 0,
      rx: _int(j['rx_speed']) ?? 0,
      tx: _int(j['tx_speed']) ?? 0,
    );
  }
}
