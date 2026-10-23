// 连不上面板时的提示条（设计 1.5.10，修订第 211 条）：说明原因、正在显示哪个时间的数据，并可立即重试。
import 'package:flutter/material.dart';

import '../errors.dart';
import '../format.dart';
import '../theme.dart';

class StaleBanner extends StatelessWidget {
  const StaleBanner({super.key, required this.error, this.updated, this.note, this.onRetry});

  /// 失败原因（异常对象或已是说明文字）
  final Object error;

  /// 正在显示的数据取得的时间；null 表示还没有任何数据
  final DateTime? updated;

  /// 没有 updated 时的第二行说明（可选）
  final String? note;
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) {
    final c = appColors(context);
    final reason = error is String ? error as String : describeError(error);
    return Card(
      color: Color.alphaBlend(c.warn.withValues(alpha: 0.12), c.surface),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(color: c.warn.withValues(alpha: 0.4)),
      ),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 10, 4, 10),
        child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Padding(padding: const EdgeInsets.only(top: 2), child: Icon(Icons.cloud_off_outlined, size: 20, color: c.warn)),
          const SizedBox(width: 10),
          Expanded(
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text(reason, style: const TextStyle(fontWeight: FontWeight.w600)),
              if (updated != null || note != null) ...[
                const SizedBox(height: 2),
                Text(updated != null ? '显示的是 ${fmtStamp(updated!)} 的数据，会自动重试' : note!,
                    style: TextStyle(fontSize: 12, color: c.textMuted)),
              ],
            ]),
          ),
          if (onRetry != null) TextButton(onPressed: onRetry, child: const Text('重试')),
        ]),
      ),
    );
  }
}
