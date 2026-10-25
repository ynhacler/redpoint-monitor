// 工具（修订第 216、218 条）：底部导航的第三个标签，分页放“订阅”与“云平台”。
// 页面在第一次切到对应分页时才创建。
import 'package:flutter/material.dart';

import '../api.dart';
import '../models.dart';
import 'cloud_page.dart';
import 'subscriptions_page.dart';

/// 工具页：订阅 / 云平台
class ToolsPage extends StatelessWidget {
  /// 创建工具页
  const ToolsPage({super.key, this.initialTab = 0, required this.api, required this.onRevoked, this.privacy = false, this.cloud,
      this.serverNames = const {}, this.onOpenServer, this.subGrouped = false, this.onSubGrouped});

  /// 打开时的分页：0 订阅、1 云平台（修订第 242 条，从首页待处理跳转）
  final int initialTab;

  /// 当前监控中心
  final ApiClient api;

  /// 设备授权失效
  final VoidCallback onRevoked;

  /// 隐私模式
  final bool privacy;

  /// 已读取的云账户摘要（先显示，再刷新）
  final CloudSummary? cloud;

  /// 节点 ID → 名称，用于显示云实例关联的节点
  final Map<int, String> serverNames;

  /// 点云实例关联的节点时打开详情
  final ValueChanged<int>? onOpenServer;

  /// 订阅列表按类别分组（修订第 237 条，本机偏好）
  final bool subGrouped;

  /// 切换订阅列表的排列方式
  final ValueChanged<bool>? onSubGrouped;

  @override
  Widget build(BuildContext context) => DefaultTabController(
        length: 2,
        initialIndex: initialTab,
        child: Scaffold(
          appBar: AppBar(
            title: const Text('工具'),
            bottom: const TabBar(tabs: [Tab(text: '订阅'), Tab(text: '云平台')]),
          ),
          body: TabBarView(children: [
            SubscriptionsPage(api: api, onRevoked: onRevoked, privacy: privacy, embedded: true, grouped: subGrouped, onGrouped: onSubGrouped),
            CloudPage(api: api, onRevoked: onRevoked, initial: cloud, serverNames: serverNames, onOpenServer: onOpenServer, embedded: true),
          ]),
        ),
      );
}
