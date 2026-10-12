// 调整节点顺序（设计 1.5.6，修订第 149 条）：拖动右侧把手排列节点，保存为本机偏好（自定义顺序）。
// 顺序只保存在本机（App 只读，不写回面板）；之后新增的节点排在最后。
import 'package:flutter/material.dart';

import '../format.dart';
import '../models.dart';
import '../theme.dart';

class ReorderPage extends StatefulWidget {
  const ReorderPage({super.key, required this.items, required this.onSave});

  /// 按当前顺序排好的节点
  final List<ServerView> items;
  final ValueChanged<List<int>> onSave;

  @override
  State<ReorderPage> createState() => _ReorderPageState();
}

class _ReorderPageState extends State<ReorderPage> {
  late final List<ServerView> _list = [...widget.items];

  void _sortByName() => setState(() => _list.sort((a, b) => a.name.compareTo(b.name)));

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final colors = StatusColors.of(context);
    return Scaffold(
      appBar: AppBar(
        title: const Text('调整顺序'),
        actions: [
          TextButton(onPressed: _sortByName, child: const Text('按名称')),
          TextButton(
            onPressed: () {
              widget.onSave([for (final s in _list) s.id]);
              Navigator.of(context).pop();
            },
            child: const Text('完成'),
          ),
        ],
      ),
      body: Column(children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 10, 16, 6),
          child: Text('按住右侧把手拖动排列；排序方式会切换为“自定义顺序”，只保存在本机。', style: TextStyle(fontSize: 12, color: c.textMuted)),
        ),
        Expanded(
          child: ReorderableListView.builder(
            padding: const EdgeInsets.fromLTRB(12, 0, 12, 24),
            buildDefaultDragHandles: false,
            itemCount: _list.length,
            // onReorderItem 的 newIndex 已按移除旧位置后的列表调整
            onReorderItem: (from, to) => setState(() => _list.insert(to, _list.removeAt(from))),
            itemBuilder: (context, i) {
              final s = _list[i];
              return Card(
                key: ValueKey(s.id),
                child: ListTile(
                  leading: SizedBox(
                    width: 24,
                    child: Center(child: Text('${i + 1}', style: TextStyle(color: c.textMuted, fontFeatures: const [FontFeature.tabularFigures()]))),
                  ),
                  title: Row(children: [
                    if (flagEmoji(s.country).isNotEmpty) Padding(padding: const EdgeInsets.only(right: 6), child: Text(flagEmoji(s.country))),
                    Flexible(child: Text(s.name, overflow: TextOverflow.ellipsis)),
                    const SizedBox(width: 6),
                    Icon(Icons.circle, size: 8, color: colors.status(s.status)),
                  ]),
                  subtitle: s.group.isEmpty ? null : Text(s.group, style: TextStyle(fontSize: 12, color: c.textMuted)),
                  trailing: ReorderableDragStartListener(
                    index: i,
                    child: Padding(padding: const EdgeInsets.all(8), child: Icon(Icons.drag_handle, color: c.textMuted, semanticLabel: '拖动排序')),
                  ),
                ),
              );
            },
          ),
        ),
      ]),
    );
  }
}
