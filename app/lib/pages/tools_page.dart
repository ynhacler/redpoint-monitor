// 工具（修订第 216 条）：底部导航的第三个标签，分页放“订阅”与“连通性测试”。
// 页面在第一次切到对应分页时才创建：连通性测试打开后才开始测。
import 'package:flutter/material.dart';

import '../api.dart';
import 'connectivity_page.dart';
import 'subscriptions_page.dart';

/// 工具页：订阅 / 连通性测试
class ToolsPage extends StatelessWidget {
  /// 创建工具页
  const ToolsPage({super.key, required this.api, required this.onRevoked, this.privacy = false});

  /// 当前监控中心
  final ApiClient api;

  /// 设备授权失效
  final VoidCallback onRevoked;

  /// 隐私模式
  final bool privacy;

  @override
  Widget build(BuildContext context) => DefaultTabController(
        length: 2,
        child: Scaffold(
          appBar: AppBar(
            title: const Text('工具'),
            bottom: const TabBar(tabs: [Tab(text: '订阅'), Tab(text: '连通性测试')]),
          ),
          body: TabBarView(children: [
            SubscriptionsPage(api: api, onRevoked: onRevoked, privacy: privacy, embedded: true),
            const ConnectivityPage(embedded: true),
          ]),
        ),
      );
}
