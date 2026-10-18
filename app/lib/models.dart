// 面板数据模型（契约见 api/openapi.yaml 的 ServerView、TrafficView、MetricPoint、Silence）。
// 只解析 App 用到的字段；未知字段忽略，面板新增字段不影响旧版 App（设计 19.0.2）。
//
// 每个模型都保留原始 JSON（raw），离线缓存直接保存它，读取时用同一套解析，避免缓存格式与接口不一致。

int? _int(Object? v) => (v as num?)?.toInt();
double? _dbl(Object? v) => (v as num?)?.toDouble();

/// 一个挂载点的磁盘用量
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

/// 节点最新一份上报中的指标（设计 4）
class Latest {
  Latest({required this.cpu, required this.mem, required this.memUsed, required this.memTotal, required this.disks,
      required this.rx, required this.tx, required this.load1, required this.cores, this.uptime, this.rxTotal = 0, this.txTotal = 0,
      this.tempC, this.io});
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

  /// 开机以来累计收发字节数（网卡计数，重启后清零）
  final int rxTotal;
  final int txTotal;

  /// CPU 温度；读不到时为 null
  final double? tempC;

  /// 磁盘读写（各磁盘之和）；旧版 Agent 没有时为 null
  final DiskIO? io;

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
      rxTotal: nets.fold<int>(0, (a, n) => a + (_int((n as Map<String, dynamic>)['rx_bytes']) ?? 0)),
      txTotal: nets.fold<int>(0, (a, n) => a + (_int((n as Map<String, dynamic>)['tx_bytes']) ?? 0)),
      uptime: _int((j['system'] as Map<String, dynamic>?)?['uptime']),
      tempC: _dbl(cpu['temp_c']),
      io: DiskIO.sum((j['disk_io'] as List<dynamic>?)?.cast<Map<String, dynamic>>()),
    );
  }
}

/// 磁盘读写速率与开机以来累计（各磁盘之和，设计 4.7）
class DiskIO {
  const DiskIO({required this.read, required this.write, required this.readTotal, required this.writeTotal});
  final int read;
  final int write;
  final int readTotal;
  final int writeTotal;

  static DiskIO? sum(List<Map<String, dynamic>>? list) {
    if (list == null || list.isEmpty) return null;
    var r = 0, w = 0, rt = 0, wt = 0;
    for (final d in list) {
      r += _int(d['read_speed']) ?? 0;
      w += _int(d['write_speed']) ?? 0;
      rt += _int(d['read_bytes']) ?? 0;
      wt += _int(d['write_bytes']) ?? 0;
    }
    return DiskIO(read: r, write: w, readTotal: rt, writeTotal: wt);
  }
}

/// 流量预测：日均与周期结束时的预计总量
class Forecast {
  Forecast({required this.daily, required this.total, required this.over});
  final int daily;
  final int total;
  final bool over;
}

/// 本计费周期的流量（设计 5）
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

/// 节点上的一条活动告警
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

/// 生效中的静音或维护（设计 8.4.1）
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

/// 节点列表中的一项（GET /servers）
class ServerView {
  ServerView({required this.raw, required this.id, required this.name, required this.status, required this.group, required this.country,
      required this.lastSeenAt, required this.traffic, required this.alerts, this.latest, this.maintenance, this.muted, this.expireDate = '',
      this.ipv4 = '', this.ipv6 = '', this.provider = '', this.plan = '', this.region = '', this.note = '', this.priceCents = 0,
      this.currency = '', this.billingPeriod = '', this.sortOrder = 0});
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

  // 资产信息（隐私模式下隐藏，设计 1.5.12）
  final String ipv4;
  final String ipv6;
  final String provider;
  final String plan;
  final String region;
  final String note;
  final int priceCents;
  final String currency;
  final String billingPeriod;

  /// 面板保存的自定义顺序（从 1 开始，0 表示未排；旧版面板没有该字段，修订第 150 条）
  final int sortOrder;

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
      ipv4: (j['ipv4'] as String?) ?? '',
      ipv6: (j['ipv6'] as String?) ?? '',
      provider: (j['provider'] as String?) ?? '',
      plan: (j['plan'] as String?) ?? '',
      region: (j['region'] as String?) ?? '',
      note: (j['note'] as String?) ?? '',
      priceCents: _int(j['price_cents']) ?? 0,
      currency: (j['currency'] as String?) ?? '',
      billingPeriod: (j['billing_period'] as String?) ?? '',
      sortOrder: _int(j['sort_order']) ?? 0,
    );
  }
}

/// 告警事件（GET /alerts，设计 19.9）：事件中心的一条（设计 1.5.17）。
class AlertEvent {
  AlertEvent({required this.id, required this.serverId, required this.serverName, required this.type, required this.severity,
      required this.state, required this.message, required this.firedAt, this.resolvedAt});
  final int id;
  final int serverId;
  final String serverName;
  final String type;
  final String severity;

  /// firing / resolved
  final String state;
  final String message;
  final int firedAt;
  final int? resolvedAt;

  factory AlertEvent.fromJson(Map<String, dynamic> j) => AlertEvent(
        id: _int(j['id'])!,
        serverId: _int(j['server_id']) ?? 0,
        serverName: (j['server_name'] as String?) ?? '',
        type: (j['type'] as String?) ?? '',
        severity: (j['severity'] as String?) ?? 'info',
        state: (j['state'] as String?) ?? 'firing',
        message: (j['message'] as String?) ?? '',
        firedAt: _int(j['fired_at']) ?? 0,
        resolvedAt: _int(j['resolved_at']),
      );
}

/// 运行中的容器（设计 33.4，修订第 164 条）：Agent 从 cgroup 读取，没有容器名；name 为用户在 Web 中起的名字（可为空）。
class ContainerInfo {
  ContainerInfo({required this.id, required this.runtime, this.cpu, required this.memory, this.memLimit = 0, this.pids = 0, this.name = '',
      this.command = '', this.restarts24h = 0});
  final String id;

  /// docker / podman
  final String runtime;

  /// 占整机 CPU 的百分比；首次采样为 null
  final double? cpu;
  final int memory;

  /// 0 表示不限
  final int memLimit;
  final int pids;
  final String name;

  /// 主进程名（如 nginx），容器重建后不变；旧版 Agent 或读不到时为空（修订第 165 条）
  final String command;

  /// 24 小时内的重启次数（面板统计，修订第 167 条）
  final int restarts24h;

  /// 显示的标识：用户起的名字 → 主进程名 → 容器 ID
  String get label => name.isNotEmpty ? name : (command.isNotEmpty ? command : id);

  /// 从节点详情的原始 JSON 读取（latest.containers 与 container_names）；旧版面板或没有容器时为空列表。
  static List<ContainerInfo> of(ServerView s) {
    final latest = s.raw['latest'] as Map<String, dynamic>?;
    final names = (s.raw['container_names'] as Map<String, dynamic>?) ?? const {};
    return [
      for (final c in (latest?['containers'] as List<dynamic>?) ?? const [])
        if (c is Map<String, dynamic>)
          ContainerInfo(
            id: (c['id'] as String?) ?? '',
            runtime: (c['runtime'] as String?) ?? 'docker',
            cpu: (c['cpu'] as num?)?.toDouble(),
            memory: _int(c['memory']) ?? 0,
            memLimit: _int(c['mem_limit']) ?? 0,
            pids: _int(c['pids']) ?? 0,
            name: (names[c['id']] as String?) ?? '',
            command: (c['command'] as String?) ?? '',
            restarts24h: _int((s.raw['container_restarts'] as Map<String, dynamic>?)?[c['id']]) ?? 0,
          ),
    ];
  }
}

/// 已停止的容器（修订第 166 条）：按主进程名比曾经运行的少了 missing 个，since 起；只在节点详情中返回。
class ContainerMissing {
  ContainerMissing({required this.label, required this.missing, required this.since});
  final String label;
  final int missing;
  final int since;

  /// 从节点详情的原始 JSON 读取；旧版面板或没有时为空列表。
  static List<ContainerMissing> of(ServerView s) => [
        for (final m in (s.raw['containers_missing'] as List<dynamic>?) ?? const [])
          if (m is Map<String, dynamic>)
            ContainerMissing(label: (m['label'] as String?) ?? '', missing: _int(m['missing']) ?? 1, since: _int(m['since']) ?? 0),
      ];
}

/// 系统事件（GET /system-events，修订第 163 条）：Agent 升级结果、到期提醒、面板自身告警；serverId 为 0 表示不属于某个节点。
class SystemEvent {
  SystemEvent({required this.id, required this.kind, required this.severity, required this.serverId, required this.serverName,
      required this.title, required this.message, required this.createdAt});
  final int id;

  /// upgrade / reminder / panel
  final String kind;
  final String severity;
  final int serverId;
  final String serverName;
  final String title;
  final String message;
  final int createdAt;

  factory SystemEvent.fromJson(Map<String, dynamic> j) => SystemEvent(
        id: _int(j['id'])!,
        kind: (j['kind'] as String?) ?? '',
        severity: (j['severity'] as String?) ?? 'info',
        serverId: _int(j['server_id']) ?? 0,
        serverName: (j['server_name'] as String?) ?? '',
        title: (j['title'] as String?) ?? '',
        message: (j['message'] as String?) ?? '',
        createdAt: _int(j['created_at']) ?? 0,
      );
}

/// 健康摘要（GET /servers/{id}/health，设计 1.5.7）：面板计算的结论与文字，与 Web 相同。
class HealthSummary {
  HealthSummary({required this.level, required this.status, required this.items});

  /// ok / warn / bad / muted
  final String level;
  final String status;
  final List<({String key, String title, String text, String level})> items;

  factory HealthSummary.fromJson(Map<String, dynamic> j) => HealthSummary(
        level: j['level'] as String,
        status: j['status'] as String,
        items: [
          for (final it in (j['items'] as List<dynamic>).cast<Map<String, dynamic>>())
            (key: it['key'] as String, title: it['title'] as String, text: it['text'] as String, level: it['level'] as String),
        ],
      );
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

/// 测速的一个目标及最近 1 小时汇总（GET /servers/{id}/latency，设计 42.3.2）。
class LatencyTarget {
  LatencyTarget({required this.id, required this.isp, required this.province, required this.address, required this.proto,
      required this.port, required this.enabled, required this.note, this.latency, this.loss, required this.lastAt, required this.lastError});
  final int id;

  /// ct / cu / cm；intl 为境外（province 为城市，note 为提供商）
  final String isp;
  final String province;
  final String address;
  final String proto;
  final int port;

  /// 这台节点是否还在测；false 表示只是之前的数据
  final bool enabled;
  final String note;
  final double? latency;
  final double? loss;
  final int lastAt;
  final String lastError;

  factory LatencyTarget.fromJson(Map<String, dynamic> j) => LatencyTarget(
        id: j['id'] as int,
        isp: j['isp'] as String,
        province: j['province'] as String,
        address: j['address'] as String,
        proto: j['proto'] as String,
        port: j['port'] as int,
        enabled: j['enabled'] as bool,
        note: (j['note'] as String?) ?? '',
        latency: (j['latency_ms'] as num?)?.toDouble(),
        loss: (j['loss'] as num?)?.toDouble(),
        lastAt: j['last_at'] as int,
        lastError: (j['last_error'] as String?) ?? '',
      );
}

/// 测速曲线上的一个点；latency 为 null 表示这一轮全部失败。
class LatencyPoint {
  LatencyPoint(this.ts, this.latency, this.loss);
  final int ts;
  final double? latency;
  final double loss;
}

/// 节点的测速：各目标汇总与曲线（GET /servers/{id}/latency，设计 42.3.2）
class LatencyView {
  LatencyView({required this.status, required this.mode, required this.intervalS, required this.targets, required this.series});

  /// on / off（节点本机关闭）/ unsupported（旧版 Agent）
  final String status;

  /// default / custom / off（较旧的面板没有该字段，按 default）
  final String mode;
  final int intervalS;
  final List<LatencyTarget> targets;
  final Map<int, List<LatencyPoint>> series;

  factory LatencyView.fromJson(Map<String, dynamic> j) => LatencyView(
        status: j['status'] as String,
        mode: (j['mode'] as String?) ?? 'default',
        intervalS: (j['interval_s'] as int?) ?? 300,
        targets: [for (final t in (j['targets'] as List<dynamic>)) LatencyTarget.fromJson(t as Map<String, dynamic>)],
        series: {
          for (final e in (j['series'] as Map<String, dynamic>).entries)
            int.parse(e.key): [
              for (final p in (e.value as List<dynamic>).cast<Map<String, dynamic>>())
                LatencyPoint(p['ts'] as int, (p['latency_ms'] as num?)?.toDouble(), (p['loss'] as num).toDouble()),
            ],
        },
      );
}

/// 测速对比中的一个目标（ProbeTarget 的子集）。
class OverviewTarget {
  OverviewTarget({required this.id, required this.isp, required this.province, required this.address, required this.proto,
      required this.port, required this.note});
  final int id;
  final String isp;
  final String province;
  final String address;
  final String proto;
  final int port;
  final String note;

  /// 显示名：国内为“上海电信”，境外为“东京 Vultr”（与 Web 测速对比一致）
  String get name => isp == 'intl' ? (note.isEmpty ? province : '$province $note') : '$province${const {'ct': '电信', 'cu': '联通', 'cm': '移动'}[isp] ?? ''}';

  factory OverviewTarget.fromJson(Map<String, dynamic> j) => OverviewTarget(
        id: j['id'] as int,
        isp: j['isp'] as String,
        province: j['province'] as String,
        address: j['address'] as String,
        proto: j['proto'] as String,
        port: (j['port'] as int?) ?? 0,
        note: (j['note'] as String?) ?? '',
      );
}

/// 测速对比的一格：最近 1 小时延迟中位数（全部失败时为 null）与平均丢包率。
class OverviewCell {
  OverviewCell(this.serverId, this.latency, this.loss);
  final int serverId;
  final double? latency;
  final double loss;
}

/// 测速对比（GET /latency-overview，设计 42.3.5）：全部节点到各目标最近 1 小时的延迟与丢包。
class LatencyOverview {
  LatencyOverview({required this.servers, required this.targets, required this.cells});

  /// 节点 ID → （名称，分组）
  final Map<int, (String, String)> servers;
  final List<OverviewTarget> targets;

  /// 目标 ID → 有结果的节点
  final Map<int, List<OverviewCell>> cells;

  factory LatencyOverview.fromJson(Map<String, dynamic> j) {
    final cells = <int, List<OverviewCell>>{};
    for (final c in (j['cells'] as List<dynamic>).cast<Map<String, dynamic>>()) {
      cells.putIfAbsent(c['target_id'] as int, () => []).add(
          OverviewCell(c['server_id'] as int, (c['latency_ms'] as num?)?.toDouble(), (c['loss'] as num).toDouble()));
    }
    return LatencyOverview(
      servers: {
        for (final s in (j['servers'] as List<dynamic>).cast<Map<String, dynamic>>())
          s['id'] as int: (s['name'] as String, (s['group'] as String?) ?? ''),
      },
      targets: [for (final t in (j['targets'] as List<dynamic>)) OverviewTarget.fromJson(t as Map<String, dynamic>)],
      cells: cells,
    );
  }

  /// 有结果的分组（排序后）
  List<String> get groups =>
      {for (final l in cells.values) for (final c in l) servers[c.serverId]?.$2 ?? ''}.where((g) => g.isNotEmpty).toList()..sort();

  /// 某个目标下的节点排名：延迟从低到高，同延迟按丢包；全部失败的排在最后；group 非空时只看该分组。
  List<OverviewCell> ranking(int targetId, {String group = ''}) {
    final l = [
      for (final c in cells[targetId] ?? const <OverviewCell>[])
        if (servers.containsKey(c.serverId) && (group.isEmpty || servers[c.serverId]!.$2 == group)) c,
    ];
    l.sort((a, b) {
      if (a.latency == null || b.latency == null) {
        if (a.latency != b.latency) return a.latency == null ? 1 : -1;
        return a.loss.compareTo(b.loss);
      }
      final d = a.latency!.compareTo(b.latency!);
      return d != 0 ? d : a.loss.compareTo(b.loss);
    });
    return l;
  }
}

/// 云厂商名称（与 Web 云账户页一致）
const cloudProviderNames = {
  'aws': 'AWS', 'aliyun_cn': '阿里云', 'aliyun_intl': '阿里云国际', 'tencent_cn': '腾讯云', 'tencent_intl': '腾讯云国际', 'oci': 'Oracle Cloud',
};

/// 云实例类型名称（与 Web 云账户页的 kindNames 一致）
const cloudKindNames = {'ec2': 'EC2', 'lightsail': 'Lightsail', 'ecs': 'ECS', 'swas': '轻量', 'cvm': 'CVM', 'lighthouse': '轻量',
  'oci': 'OCI', 'oci_egress': '租户'};

/// 云账户本月的费用、预估与余额（分）
class CloudCost {
  CloudCost({required this.period, required this.amountCents, this.forecastCents, this.balanceCents, required this.currency});
  final String period;
  final int amountCents;
  final int? forecastCents;
  final int? balanceCents;
  final String currency;
}

/// 云账户（GET /app/cloud，设计 44.8）：只读摘要，不含凭证。
class CloudAccountBrief {
  CloudAccountBrief({required this.id, required this.provider, required this.name, required this.enabled, required this.syncing,
      required this.authFailed, required this.lastError, required this.lastWarning, required this.nextTryAt, required this.syncedAt,
      required this.instanceCount, required this.budgetCents, this.cost});
  final int id;
  final String provider;
  final String name;
  final bool enabled;
  final bool syncing;
  final bool authFailed;
  final String lastError;
  final String lastWarning;
  final int nextTryAt;
  final int syncedAt;
  final int instanceCount;
  final int budgetCents;
  final CloudCost? cost;

  String get providerName => cloudProviderNames[provider] ?? provider;

  /// 预算用量：优先按本月预估，没有预估时按已产生（与 Web 一致）；没有预算或费用时为 null
  double? get budgetPct {
    if (budgetCents <= 0 || cost == null) return null;
    return (cost!.forecastCents ?? cost!.amountCents) / budgetCents * 100;
  }

  /// 状态级别：bad（凭证失效）、warn（同步失败、部分区域跳过、超预算）、ok
  String get level {
    if (!enabled || syncing) return 'ok';
    if (authFailed) return 'bad';
    if (lastError.isNotEmpty || lastWarning.isNotEmpty || (budgetPct ?? 0) >= 100) return 'warn';
    return 'ok';
  }

  factory CloudAccountBrief.fromJson(Map<String, dynamic> j) {
    final c = j['current_cost'] as Map<String, dynamic>?;
    return CloudAccountBrief(
      id: j['id'] as int,
      provider: j['provider'] as String,
      name: j['name'] as String,
      enabled: j['enabled'] as bool,
      syncing: (j['syncing'] as bool?) ?? false,
      authFailed: (j['auth_failed'] as bool?) ?? false,
      lastError: (j['last_error'] as String?) ?? '',
      lastWarning: (j['last_warning'] as String?) ?? '',
      nextTryAt: (j['next_try_at'] as int?) ?? 0,
      syncedAt: (j['synced_at'] as int?) ?? 0,
      instanceCount: (j['instance_count'] as int?) ?? 0,
      budgetCents: (j['budget_cents'] as int?) ?? 0,
      cost: c == null
          ? null
          : CloudCost(period: c['period'] as String, amountCents: c['amount_cents'] as int, forecastCents: c['forecast_cents'] as int?,
              balanceCents: c['balance_cents'] as int?, currency: (c['currency'] as String?) ?? ''),
    );
  }
}

/// 云实例的只读摘要（设计 44.8）
class CloudInstanceBrief {
  CloudInstanceBrief({required this.id, required this.accountId, required this.name, required this.region, required this.kind,
      required this.state, required this.plan, required this.expireAt, required this.trafficUsed, required this.trafficLimit, this.serverId});
  final int id;
  final int accountId;
  final String name;
  final String region;
  final String kind;
  final String state;
  final String plan;
  final int expireAt;
  final int trafficUsed;
  final int trafficLimit;
  final int? serverId;

  factory CloudInstanceBrief.fromJson(Map<String, dynamic> j) => CloudInstanceBrief(
        id: j['id'] as int,
        accountId: j['account_id'] as int,
        name: (j['name'] as String?) ?? '',
        region: (j['region'] as String?) ?? '',
        kind: (j['kind'] as String?) ?? '',
        state: (j['state'] as String?) ?? '',
        plan: (j['plan'] as String?) ?? '',
        expireAt: (j['expire_at'] as int?) ?? 0,
        trafficUsed: (j['traffic_used_bytes'] as int?) ?? 0,
        trafficLimit: (j['traffic_limit_bytes'] as int?) ?? 0,
        serverId: j['server_id'] as int?,
      );
}

/// GET /app/cloud：limited 为 true 表示本机授权范围不是“全部节点”，看不到云账户。
class CloudSummary {
  CloudSummary({required this.limited, required this.accounts, required this.instances});
  final bool limited;
  final List<CloudAccountBrief> accounts;
  final List<CloudInstanceBrief> instances;

  factory CloudSummary.fromJson(Map<String, dynamic> j) => CloudSummary(
        limited: (j['limited'] as bool?) ?? false,
        accounts: [for (final a in (j['accounts'] as List<dynamic>)) CloudAccountBrief.fromJson(a as Map<String, dynamic>)],
        instances: [for (final i in (j['instances'] as List<dynamic>)) CloudInstanceBrief.fromJson(i as Map<String, dynamic>)],
      );

  /// 需要关注的账户数（凭证失效、同步失败、区域跳过、超预算）
  int get attention => accounts.where((a) => a.level != 'ok').length;
}

/// 国内探针的一个探测项（设计 42.2，修订第 156 条）
class ReachCheck {
  ReachCheck({required this.family, required this.port, this.latency, required this.loss, required this.error});
  final int family;

  /// 0 为 Ping
  final int port;
  final double? latency;
  final double loss;
  final String error;

  String get name => '${port == 0 ? 'Ping' : 'TCP $port'}${family == 6 ? ' · IPv6' : ''}';

  factory ReachCheck.fromJson(Map<String, dynamic> j) => ReachCheck(
        family: j['family'] as int,
        port: j['port'] as int,
        latency: (j['latency_ms'] as num?)?.toDouble(),
        loss: (j['loss'] as num).toDouble(),
        error: (j['error'] as String?) ?? '',
      );
}

/// 一台国内探针对节点的判定
class ReachProbeResult {
  ReachProbeResult({required this.probeId, required this.name, required this.isp, required this.status, required this.since,
      required this.badPorts, required this.lastAt, required this.checks});
  final int probeId;
  final String name;
  final String isp;

  /// ok / unstable / port / blocked
  final String status;
  final int since;
  final List<int> badPorts;
  final int lastAt;
  final List<ReachCheck> checks;

  factory ReachProbeResult.fromJson(Map<String, dynamic> j) => ReachProbeResult(
        probeId: j['probe_id'] as int,
        name: (j['name'] as String?) ?? '',
        isp: (j['isp'] as String?) ?? '',
        status: j['status'] as String,
        since: (j['since'] as int?) ?? 0,
        badPorts: [for (final p in (j['bad_ports'] as List<dynamic>? ?? const [])) p as int],
        lastAt: (j['last_at'] as int?) ?? 0,
        checks: [for (final c in (j['checks'] as List<dynamic>? ?? const [])) ReachCheck.fromJson(c as Map<String, dynamic>)],
      );
}

/// 节点的国内可达性（GET /servers/{id}/reach，设计 42.2、42.3.1）
class ReachView {
  ReachView({required this.configured, required this.status, required this.text, required this.probes, required this.series});
  final bool configured;

  /// ok / unstable / port / partial / blocked / offline；还没有结果时为空
  final String status;
  final String text;
  final List<ReachProbeResult> probes;

  /// 探针 ID → 24 小时延迟曲线
  final Map<int, List<LatencyPoint>> series;

  factory ReachView.fromJson(Map<String, dynamic> j) => ReachView(
        configured: (j['configured'] as bool?) ?? false,
        status: (j['status'] as String?) ?? '',
        text: (j['text'] as String?) ?? '',
        probes: [for (final p in (j['probes'] as List<dynamic>? ?? const [])) ReachProbeResult.fromJson(p as Map<String, dynamic>)],
        series: {
          for (final e in ((j['series'] as Map<String, dynamic>?) ?? const {}).entries)
            int.parse(e.key): [
              for (final p in (e.value as List<dynamic>).cast<Map<String, dynamic>>())
                LatencyPoint(p['ts'] as int, (p['latency_ms'] as num?)?.toDouble(), (p['loss'] as num).toDouble()),
            ],
        },
      );
}
