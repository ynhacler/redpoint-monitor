// 网络（修订第 219 条）：底部导航的第二个标签，分页放“本机 IP”与“连通性测试”。
// 底部标签第一次切到时才创建；分页也在切到时才创建，打开 App 不会自动查询或测试。
import 'package:flutter/material.dart';

import 'connectivity_page.dart';
import 'ip_page.dart';

/// 网络页：本机 IP / 连通性测试
class NetworkPage extends StatelessWidget {
  /// 创建网络页
  const NetworkPage({super.key, this.privacy = false});

  /// 隐私模式：IP 打码
  final bool privacy;

  @override
  Widget build(BuildContext context) => DefaultTabController(
        length: 2,
        child: Scaffold(
          appBar: AppBar(
            title: const Text('网络'),
            bottom: const TabBar(tabs: [Tab(text: '本机 IP'), Tab(text: '连通性测试')]),
          ),
          body: TabBarView(children: [IpPage(privacy: privacy), const ConnectivityPage(embedded: true)]),
        ),
      );
}
