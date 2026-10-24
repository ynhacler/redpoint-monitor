// 新增 / 修改订阅（修订第 217、218 条）：约束 5 的唯一写入例外。AK 勾选了“允许记录订阅”且授权全部节点的设备才显示入口；
// 新增时先选服务（与 Web 相同的目录，可搜索，也可自定义），再填写价格、周期、日期等；修改时直接打开表单。
// 校验在面板进行，字段错误显示在对应输入框下。保存前按“敏感操作前验证”验证（设计 1.5.11）。上传图标只在 Web 中进行。
// 修改是整体替换：表单里没有的字段（VPS 的 CPU、内存、硬盘、带宽、流量）沿用原值，不会被清空。
import 'package:flutter/material.dart';

import '../api.dart';
import '../app_lock.dart';
import '../catalog.gen.dart';
import '../errors.dart';
import '../format.dart';
import '../subscriptions.dart';
import '../theme.dart';
import '../widgets/brand_avatar.dart';

/// 新增（选服务 → 填写）或修改订阅；保存成功时以保存后的订阅出栈
class SubscriptionAddPage extends StatefulWidget {
  /// 创建页面；[initial] 不为空时为修改
  const SubscriptionAddPage({super.key, required this.api, this.initial});

  /// 当前监控中心
  final ApiClient api;

  /// 要修改的订阅
  final Subscription? initial;

  @override
  State<SubscriptionAddPage> createState() => _SubscriptionAddPageState();
}

const _trialOptions = [(0, '无试用期'), (7, '7 天'), (14, '14 天'), (30, '1 个月'), (90, '3 个月')];
const _remindOptions = [(0, '不提醒'), (1, '提前 1 天'), (2, '提前 2 天'), (3, '提前 3 天'), (7, '提前 7 天')];

String _ymd(DateTime d) => '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

class _SubscriptionAddPageState extends State<SubscriptionAddPage> {
  // 第一步：选择服务
  bool _picked = false;
  String _query = '';
  String _pickCat = 'hot';

  // 第二步：表单
  String _brand = '';
  final _name = TextEditingController();
  final _price = TextEditingController();
  final _note = TextEditingController();
  final _location = TextEditingController();
  final _ips = TextEditingController();
  String _category = 'video';
  String _currency = 'CNY';
  String _country = ''; // 国家 / 地区与支付方式必选（修订第 223 条）
  String _payment = '';
  String _cycle = 'monthly';
  int _trial = 0;
  int _remind = 1;
  DateTime _start = DateTime.now();
  DateTime? _end;
  final List<(TextEditingController, TextEditingController)> _phones = [];

  bool _saving = false;
  String? _error;
  Map<String, String> _fields = {};

  @override
  void initState() {
    super.initState();
    final x = widget.initial;
    if (x == null) return;
    _picked = true;
    _brand = x.brand;
    _name.text = x.name;
    _price.text = (x.priceCents / 100).toStringAsFixed(2);
    _note.text = x.note;
    _location.text = x.vps.location;
    _ips.text = x.vps.ips.join('\n');
    _category = categoryNames.containsKey(x.category) ? x.category : 'other';
    _currency = x.currency;
    _country = x.country;
    _payment = paymentNames.containsKey(x.payment) ? x.payment : '';
    _cycle = cycleNames.containsKey(x.cycle) ? x.cycle : 'monthly';
    _trial = _trialOptions.any((o) => o.$1 == x.trialDays) ? x.trialDays : 0;
    _remind = _remindOptions.any((o) => o.$1 == x.remindDays) ? x.remindDays : 1;
    _start = DateTime.tryParse(x.startDate) ?? DateTime.now();
    _end = x.endDate.isEmpty ? null : DateTime.tryParse(x.endDate);
    for (final p in x.phones) {
      _phones.add((TextEditingController(text: p.cc), TextEditingController(text: p.number)));
    }
  }

  @override
  void dispose() {
    for (final c in [_name, _price, _note, _location, _ips]) {
      c.dispose();
    }
    for (final (a, b) in _phones) {
      a.dispose();
      b.dispose();
    }
    super.dispose();
  }

  void _choose(String key) {
    final e = key.isEmpty ? null : catalog[key];
    setState(() {
      _picked = true;
      _brand = key;
      _name.text = e?.name ?? '';
      _category = e?.category ?? 'other';
      _currency = e?.currency ?? 'CNY';
      // 国家 / 地区按 SIM 卡的国家或币种预填（可改）；支付方式须自己选择
      _country = e?.country ?? defaultCountry(_currency);
      _cycle = _category == 'vps' ? 'annually' : 'monthly';
      if (_category == 'sim' && _phones.isEmpty) _phones.add((TextEditingController(text: e?.dial ?? '+86'), TextEditingController()));
    });
  }

  Map<String, dynamic> _body() {
    final old = widget.initial?.vps;
    final price = double.tryParse(_price.text.trim().replaceAll(',', ''));
    return {
      'name': _name.text.trim(),
      'brand': _brand,
      'category': _category,
      'price_cents': price == null ? -1 : (price * 100).round(),
      'currency': _currency,
      'cycle': _cycle,
      'trial_days': _trial,
      'start_date': _ymd(_start),
      'end_date': _end == null ? '' : _ymd(_end!),
      'remind_days': _remind,
      'note': _note.text.trim(),
      'country': _country,
      'payment': _payment,
      'phones': [
        if (_category == 'sim')
          for (final (cc, n) in _phones)
            if (n.text.trim().isNotEmpty) {'cc': cc.text.trim(), 'number': n.text.replaceAll(RegExp(r'\D'), '')},
      ],
      'vps': {
        'ips': [if (_category == 'vps') ..._ips.text.split(RegExp(r'[\s,，]+')).where((x) => x.isNotEmpty)],
        // 表单中没有的配置沿用原值（修改是整体替换）
        'cpu': old?.cpu ?? 0, 'memory_gb': old?.memoryGB ?? 0, 'disk_gb': old?.diskGB ?? 0,
        'bandwidth_mbps': old?.bandwidthMbps ?? 0, 'traffic_gb': old?.trafficGB ?? 0,
        'location': _category == 'vps' ? _location.text.trim() : '',
      },
    };
  }

  Future<void> _save() async {
    final lock = AppLockScope.of(context);
    if (lock != null && !await lock.guard(widget.initial == null ? '新增订阅' : '修改订阅')) return;
    setState(() {
      _saving = true;
      _error = null;
      _fields = {};
    });
    try {
      final x = widget.initial == null
          ? await widget.api.createSubscription(_body())
          : await widget.api.updateSubscription(widget.initial!.id, _body());
      if (mounted) Navigator.of(context).pop(x);
    } on ApiException catch (e) {
      if (mounted) {
        setState(() {
          _fields = e.fields;
          _error = e.fields.isEmpty ? e.message : '请检查标出的项目';
        });
      }
    } catch (e) {
      if (mounted) setState(() => _error = describeError(e));
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  // 字段错误：嵌套字段（phones[0].number、vps.ips[1]）按前缀归到所在的输入框
  String? _err(String field) {
    for (final e in _fields.entries) {
      if (e.key == field || e.key.startsWith('$field[') || e.key.startsWith('$field.')) return e.value;
    }
    return null;
  }

  // 国家 / 地区：可搜索的列表，常用的在前
  Future<void> _pickCountry() async {
    final picked = await showModalBottomSheet<String>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (ctx) => _CountrySheet(selected: _country),
    );
    if (picked != null && mounted) setState(() => _country = picked);
  }

  @override
  Widget build(BuildContext context) => _picked ? _form(context) : _pick(context);

  // ---- 第一步：选择服务 ----

  Widget _pick(BuildContext context) {
    final c = appColors(context);
    final q = _query.trim().toLowerCase();
    final entries = catalog.entries.where((e) {
      if (q.isNotEmpty) return e.value.name.toLowerCase().contains(q) || e.key.contains(q);
      return _pickCat == 'hot' ? e.value.hot : e.value.category == _pickCat;
    }).toList();
    final cats = [('hot', '常用'), for (final e in categoryNames.entries) if (e.key != 'other') (e.key, e.value)];
    return Scaffold(
      appBar: AppBar(title: const Text('选择服务'), actions: [
        TextButton.icon(onPressed: () => _choose(''), icon: const Icon(Icons.edit_outlined, size: 18), label: const Text('自定义')),
      ]),
      body: Column(children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(12, 4, 12, 0),
          child: TextField(
            decoration: InputDecoration(
              hintText: '输入名称搜索',
              prefixIcon: const Icon(Icons.search),
              isDense: true,
              filled: true,
              fillColor: c.surface,
              border: OutlineInputBorder(borderRadius: BorderRadius.circular(12), borderSide: BorderSide(color: c.border)),
              enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(12), borderSide: BorderSide(color: c.border)),
            ),
            onChanged: (v) => setState(() => _query = v),
          ),
        ),
        if (q.isEmpty)
          SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
            child: Row(children: [
              for (final (k, n) in cats)
                Padding(
                  padding: const EdgeInsets.only(right: 6),
                  child: ChoiceChip(label: Text(n), selected: _pickCat == k, visualDensity: VisualDensity.compact,
                      onSelected: (_) => setState(() => _pickCat = k)),
                ),
            ]),
          ),
        Expanded(
          child: entries.isEmpty
              ? Center(child: Text('没有找到，点右上角“自定义”添加。', style: TextStyle(color: c.textMuted)))
              : ListView.separated(
                  padding: const EdgeInsets.fromLTRB(12, 4, 12, 24),
                  itemCount: entries.length,
                  separatorBuilder: (_, __) => const SizedBox(height: 6),
                  itemBuilder: (_, i) {
                    final (k, e) = (entries[i].key, entries[i].value);
                    return Material(
                      color: c.surface,
                      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10), side: BorderSide(color: c.border)),
                      clipBehavior: Clip.antiAlias,
                      child: InkWell(
                        onTap: () => _choose(k),
                        child: Padding(
                          padding: const EdgeInsets.fromLTRB(10, 8, 10, 8),
                          child: Row(children: [
                            BrandAvatar(logo: brandLogos[k], glyph: brandIcons[e.icon ?? k], color: e.color, letter: e.letter ?? firstLetter(e.name), size: 34),
                            const SizedBox(width: 12),
                            Expanded(child: Text(e.name, style: const TextStyle(fontWeight: FontWeight.w600))),
                            if (e.dial != null) Text(e.dial!, style: TextStyle(fontSize: 12, color: c.textMuted)),
                            const SizedBox(width: 6),
                            Icon(Icons.add, size: 18, color: c.textMuted),
                          ]),
                        ),
                      ),
                    );
                  },
                ),
        ),
      ]),
    );
  }

  // ---- 第二步：填写 ----

  Widget _form(BuildContext context) {
    final c = appColors(context);
    final e = _brand.isEmpty ? null : catalog[_brand];
    final muted = TextStyle(fontSize: 12, color: c.textMuted);
    InputDecoration deco(String label, String field, {String? hint, String? helper}) =>
        InputDecoration(labelText: label, hintText: hint, helperText: helper, errorText: _err(field), isDense: true);
    Widget gap() => const SizedBox(height: 14);
    Future<DateTime?> pickDate(DateTime initial) =>
        showDatePicker(context: context, initialDate: initial, firstDate: DateTime(2000), lastDate: DateTime(2100));

    return Scaffold(
      appBar: AppBar(
        title: Text(widget.initial == null ? '新增订阅' : '修改订阅'),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back),
          onPressed: () => widget.initial == null ? setState(() => _picked = false) : Navigator.of(context).pop(),
        ),
      ),
      // 换了服务后整个表单重建，下拉框取新的初始值
      body: ListView(key: ValueKey('form-$_brand'), padding: const EdgeInsets.fromLTRB(16, 8, 16, 32), children: [
        Row(children: [
          BrandAvatar(
            logo: e == null ? null : brandLogos[_brand],
            glyph: e == null ? null : brandIcons[e.icon ?? _brand],
            color: e?.color ?? paletteColor(_name.text),
            letter: e?.letter ?? firstLetter(_name.text.isEmpty ? '?' : _name.text),
            size: 48,
          ),
          const SizedBox(width: 12),
          Expanded(child: Text(_name.text.isEmpty ? '自定义订阅' : _name.text, style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700))),
        ]),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 10), child: Text(_error!, style: TextStyle(color: c.bad))),
        gap(),
        TextField(controller: _name, maxLength: 64, decoration: deco('名称', 'name', hint: '如 Netflix'), onChanged: (_) => setState(() {})),
        DropdownButtonFormField<String>(
          initialValue: _category,
          decoration: deco('类别', 'category'),
          items: [for (final x in categoryNames.entries) DropdownMenuItem(value: x.key, child: Text(x.value))],
          onChanged: (v) => setState(() {
            _category = v!;
            if (_category == 'sim' && _phones.isEmpty) _phones.add((TextEditingController(text: '+86'), TextEditingController()));
          }),
        ),
        gap(),
        Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          SizedBox(
            width: 104,
            child: DropdownButtonFormField<String>(
              initialValue: currencies.contains(_currency) ? _currency : 'CNY',
              decoration: deco('币种', 'currency'),
              items: [for (final x in currencies) DropdownMenuItem(value: x, child: Text(x))],
              onChanged: (v) => setState(() {
                _currency = v!;
                if (_country.isEmpty) _country = defaultCountry(_currency);
              }),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: TextField(
              controller: _price,
              keyboardType: const TextInputType.numberWithOptions(decimal: true),
              decoration: deco('价格', 'price_cents', hint: '0.00'),
            ),
          ),
        ]),
        gap(),
        // 国家 / 地区（可搜索）与支付方式（修订第 223 条）
        Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Expanded(
            child: InkWell(
              onTap: _pickCountry,
              child: InputDecorator(
                decoration: deco('国家 / 地区', 'country').copyWith(suffixIcon: const Icon(Icons.arrow_drop_down)),
                child: Text(_country.isEmpty ? '请选择' : '${flagEmoji(_country)} ${countryLabel(_country)}'.trim(),
                    maxLines: 1, overflow: TextOverflow.ellipsis, style: _country.isEmpty ? TextStyle(color: c.textMuted) : null),
              ),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: DropdownButtonFormField<String>(
              initialValue: _payment.isEmpty ? null : _payment,
              isExpanded: true,
              hint: const Text('请选择'),
              decoration: deco('支付方式', 'payment'),
              items: [for (final x in paymentNames.entries) DropdownMenuItem(value: x.key, child: Text(x.value))],
              onChanged: (v) => setState(() => _payment = v ?? ''),
            ),
          ),
        ]),
        gap(),
        Row(children: [
          Expanded(
            child: DropdownButtonFormField<String>(
              initialValue: _cycle,
              decoration: deco('订阅周期', 'cycle'),
              items: [for (final x in cycleNames.entries) DropdownMenuItem(value: x.key, child: Text(x.value))],
              onChanged: (v) => setState(() => _cycle = v!),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: DropdownButtonFormField<int>(
              initialValue: _trial,
              decoration: deco('免费试用', 'trial_days'),
              items: [for (final (v, t) in _trialOptions) DropdownMenuItem(value: v, child: Text(t))],
              onChanged: (v) => setState(() => _trial = v!),
            ),
          ),
        ]),
        gap(),
        Row(children: [
          Expanded(
            child: InkWell(
              onTap: () async {
                final d = await pickDate(_start);
                if (d != null) setState(() => _start = d);
              },
              child: InputDecorator(decoration: deco(_category == 'vps' ? '购买日期' : '开始订阅', 'start_date'), child: Text(_ymd(_start))),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: InkWell(
              onTap: () async {
                final d = await pickDate(_end ?? _start);
                if (d != null) setState(() => _end = d);
              },
              child: InputDecorator(
                decoration: deco('结束订阅', 'end_date').copyWith(
                  suffixIcon: _end == null ? null : IconButton(icon: const Icon(Icons.clear, size: 18), onPressed: () => setState(() => _end = null)),
                ),
                child: Text(_end == null ? '仍在订阅' : _ymd(_end!), style: _end == null ? TextStyle(color: c.textMuted) : null),
              ),
            ),
          ),
        ]),
        gap(),
        DropdownButtonFormField<int>(
          initialValue: _remind,
          decoration: deco('扣费提醒', 'remind_days'),
          items: [for (final (v, t) in _remindOptions) DropdownMenuItem(value: v, child: Text(t))],
          onChanged: (v) => setState(() => _remind = v!),
        ),
        if (_category == 'sim') ...[
          gap(),
          Text('手机号', style: muted),
          for (final (i, (cc, n)) in _phones.indexed)
            Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
              SizedBox(width: 84, child: TextField(controller: cc, decoration: deco('区号', 'phones[$i].cc'))),
              const SizedBox(width: 8),
              Expanded(
                  child: TextField(controller: n, keyboardType: TextInputType.phone, decoration: deco('号码', 'phones[$i].number'))),
              IconButton(icon: const Icon(Icons.remove_circle_outline), onPressed: () => setState(() => _phones.removeAt(i))),
            ]),
          if (_phones.length < 10)
            Align(
              alignment: Alignment.centerLeft,
              child: TextButton.icon(
                onPressed: () => setState(() => _phones.add((TextEditingController(text: e?.dial ?? '+86'), TextEditingController()))),
                icon: const Icon(Icons.add),
                label: const Text('添加号码'),
              ),
            ),
        ],
        if (_category == 'vps') ...[
          gap(),
          TextField(controller: _location, maxLength: 40, decoration: deco('机房 / 地区', 'vps.location', hint: '如 东京、Los Angeles')),
          TextField(
            controller: _ips,
            minLines: 1,
            maxLines: 4,
            decoration: deco('IP 地址', 'vps.ips', hint: '多个地址用空格或换行分隔', helper: 'CPU、内存、硬盘等配置可在 Web 中补充'),
          ),
        ],
        gap(),
        TextField(controller: _note, maxLength: 200, maxLines: 3, minLines: 1, decoration: deco('备注', 'note')),
        const SizedBox(height: 8),
        FilledButton(onPressed: _saving ? null : _save, child: Text(_saving ? '保存中…' : '保存')),
        const SizedBox(height: 8),
        Text('上传图标请在 Web 的“订阅”页进行。', style: muted, textAlign: TextAlign.center),
      ]),
    );
  }
}

/// 选择国家 / 地区（修订第 223 条）：按名称或代码搜索；不搜索时常用的在前
class _CountrySheet extends StatefulWidget {
  const _CountrySheet({required this.selected});
  final String selected;

  @override
  State<_CountrySheet> createState() => _CountrySheetState();
}

class _CountrySheetState extends State<_CountrySheet> {
  String _q = '';

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final q = _q.trim().toLowerCase();
    final codes = q.isEmpty
        ? [...commonCountries, ...countryNames.keys.where((k) => !commonCountries.contains(k))]
        : countryNames.keys.where((k) => k.toLowerCase().contains(q) || countryNames[k]!.toLowerCase().contains(q)).toList();
    return SizedBox(
      height: MediaQuery.of(context).size.height * 0.75,
      child: Column(children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
          child: TextField(
            autofocus: false,
            decoration: const InputDecoration(hintText: '搜索国家 / 地区（名称或代码，如 美国、US）', prefixIcon: Icon(Icons.search), isDense: true),
            onChanged: (v) => setState(() => _q = v),
          ),
        ),
        Expanded(
          child: ListView.builder(
            itemCount: codes.length,
            itemBuilder: (_, i) {
              final k = codes[i];
              return ListTile(
                dense: true,
                leading: Text(flagEmoji(k), style: const TextStyle(fontSize: 20)),
                title: Text(countryNames[k] ?? k),
                trailing: k == widget.selected ? Icon(Icons.check, color: c.accent) : Text(k, style: TextStyle(color: c.textMuted)),
                onTap: () => Navigator.pop(context, k),
              );
            },
          ),
        ),
      ]),
    );
  }
}
