// 新增订阅（修订第 217 条）：约束 5 的唯一写入例外。AK 勾选了“允许记录订阅”且授权全部节点的设备才显示入口；
// 先选服务（与 Web 相同的目录，可搜索，也可自定义），再填写价格、周期、日期等。校验在面板进行，字段错误显示在对应输入框下。
// 保存前按“敏感操作前验证”验证（设计 1.5.11）。只能新增：修改、删除与上传图标在 Web 中进行。
import 'package:flutter/material.dart';

import '../api.dart';
import '../app_lock.dart';
import '../catalog.gen.dart';
import '../errors.dart';
import '../theme.dart';
import '../widgets/brand_avatar.dart';

/// 新增订阅：选服务 → 填写；保存成功时以新订阅出栈
class SubscriptionAddPage extends StatefulWidget {
  /// 创建页面
  const SubscriptionAddPage({super.key, required this.api});

  /// 当前监控中心
  final ApiClient api;

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
      _cycle = _category == 'vps' ? 'annually' : 'monthly';
      if (_category == 'sim' && _phones.isEmpty) _phones.add((TextEditingController(text: e?.dial ?? '+86'), TextEditingController()));
    });
  }

  Map<String, dynamic> _body() {
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
      'phones': [
        if (_category == 'sim')
          for (final (cc, n) in _phones)
            if (n.text.trim().isNotEmpty) {'cc': cc.text.trim(), 'number': n.text.replaceAll(RegExp(r'\D'), '')},
      ],
      'vps': {
        'ips': [if (_category == 'vps') ..._ips.text.split(RegExp(r'[\s,，]+')).where((x) => x.isNotEmpty)],
        'cpu': 0, 'memory_gb': 0, 'disk_gb': 0, 'bandwidth_mbps': 0, 'traffic_gb': 0,
        'location': _category == 'vps' ? _location.text.trim() : '',
      },
    };
  }

  Future<void> _save() async {
    final lock = AppLockScope.of(context);
    if (lock != null && !await lock.guard('新增订阅')) return;
    setState(() {
      _saving = true;
      _error = null;
      _fields = {};
    });
    try {
      final x = await widget.api.createSubscription(_body());
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
        title: const Text('新增订阅'),
        leading: IconButton(icon: const Icon(Icons.arrow_back), onPressed: () => setState(() => _picked = false)),
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
              onChanged: (v) => setState(() => _currency = v!),
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
        Text('只能在 App 中新增；修改、删除与上传图标请在 Web 的“订阅”页进行。', style: muted, textAlign: TextAlign.center),
      ]),
    );
  }
}
