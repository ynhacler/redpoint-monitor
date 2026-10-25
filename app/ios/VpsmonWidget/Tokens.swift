// 自动生成，请勿手工编辑：修改 design/tokens.json 后在 web/ 中运行 npm run tokens（设计 41.2）
import SwiftUI
import UIKit

private func dynamic(_ light: (Double, Double, Double), _ dark: (Double, Double, Double)) -> Color {
    Color(UIColor { t in
        let c = t.userInterfaceStyle == .dark ? dark : light
        return UIColor(red: c.0, green: c.1, blue: c.2, alpha: 1)
    })
}

enum Tokens {
    /// 页面背景
    static let bg = dynamic((0.973, 0.980, 0.988), (0.059, 0.067, 0.082))
    /// 卡片、面板
    static let surface = dynamic((1.000, 1.000, 1.000), (0.090, 0.102, 0.129))
    /// 代码块、次级区域（41.2.1 之外的补充，用于命令块与说明框）
    static let surface2 = dynamic((0.945, 0.961, 0.976), (0.071, 0.082, 0.106))
    /// 分隔线、卡片边框
    static let border = dynamic((0.898, 0.906, 0.922), (0.149, 0.165, 0.200))
    /// 主要文字
    static let text = dynamic((0.059, 0.090, 0.165), (0.902, 0.910, 0.925))
    /// 次要文字、单位、说明
    static let textMuted = dynamic((0.392, 0.455, 0.545), (0.545, 0.576, 0.631))
    /// 主按钮、链接、选中态
    static let accent = dynamic((0.145, 0.388, 0.922), (0.231, 0.510, 0.965))
    /// 主按钮、危险按钮上的文字
    static let onAccent = dynamic((1.000, 1.000, 1.000), (1.000, 1.000, 1.000))
    /// 在线、正常（修订第 257 条：浅色 #15803D，白底对比度 5:1，满足 WCAG AA 小字）
    static let ok = dynamic((0.082, 0.502, 0.239), (0.133, 0.773, 0.369))
    /// 告警、未知、接近阈值（修订第 257 条：浅色 #B45309，白底对比度 5:1）
    static let warn = dynamic((0.706, 0.325, 0.035), (0.961, 0.620, 0.043))
    /// 离线、超限、失败
    static let bad = dynamic((0.863, 0.149, 0.149), (0.937, 0.267, 0.267))
    /// 待安装、维护中、已静音
    static let mutedState = dynamic((0.612, 0.639, 0.686), (0.612, 0.639, 0.686))
    /// 图表与分段环的第二类数据：CPU 系统时间、内存缓存
    static let series2 = dynamic((0.486, 0.227, 0.929), (0.655, 0.545, 0.980))
    /// 图表与分段环的第三类数据：软中断等次要分类
    static let series3 = dynamic((0.031, 0.569, 0.698), (0.133, 0.827, 0.933))
    /// 环形图、每核条的底轨
    static let track = dynamic((0.898, 0.906, 0.922), (0.165, 0.184, 0.227))
    /// 二维码模块：两种主题都是黑底白边，深色模式下也能被扫码识别
    static let qrDark = dynamic((0.000, 0.000, 0.000), (0.000, 0.000, 0.000))
    /// 二维码背景与静区
    static let qrLight = dynamic((1.000, 1.000, 1.000), (1.000, 1.000, 1.000))
    /// 订阅类别“影音娱乐”的固定颜色（修订第 233 条）
    static let catVideo = dynamic((0.918, 0.345, 0.047), (0.984, 0.573, 0.235))
    /// 订阅类别“效率工具”的固定颜色（修订第 233 条）
    static let catTools = dynamic((0.086, 0.639, 0.290), (0.133, 0.773, 0.369))
    /// 订阅类别“阅读学习”的固定颜色（修订第 233 条）
    static let catReading = dynamic((0.792, 0.541, 0.016), (0.980, 0.800, 0.082))
    /// 订阅类别“社交通讯”的固定颜色（修订第 233 条）
    static let catSocial = dynamic((0.753, 0.149, 0.827), (0.910, 0.475, 0.976))
    /// 订阅类别“健康运动”的固定颜色（修订第 233 条）
    static let catHealth = dynamic((0.396, 0.639, 0.051), (0.639, 0.902, 0.208))
    /// 订阅类别“游戏”的固定颜色（修订第 233 条）
    static let catGame = dynamic((0.882, 0.114, 0.282), (0.984, 0.443, 0.522))
    /// 订阅类别“云服务与开发”的固定颜色（修订第 233 条）
    static let catCloud = dynamic((0.031, 0.569, 0.698), (0.133, 0.827, 0.933))
    /// 订阅类别“SIM 电话卡”的固定颜色（修订第 233 条）
    static let catSim = dynamic((0.486, 0.227, 0.929), (0.655, 0.545, 0.980))
    /// 订阅类别“VPS”的固定颜色（修订第 233 条）
    static let catVps = dynamic((0.145, 0.388, 0.922), (0.231, 0.510, 0.965))
    /// 订阅类别“其他”的固定颜色（修订第 233 条）
    static let catOther = dynamic((0.420, 0.447, 0.502), (0.612, 0.639, 0.686))
    static let space1: CGFloat = 4
    static let space2: CGFloat = 8
    static let space3: CGFloat = 12
    static let space4: CGFloat = 16
    static let space5: CGFloat = 20
    static let space6: CGFloat = 24
    static let space7: CGFloat = 32
    static let space8: CGFloat = 48
    static let fontXs: CGFloat = 11
    static let fontSm: CGFloat = 12
    static let fontMd: CGFloat = 14
    static let fontLg: CGFloat = 16
    static let fontXl: CGFloat = 20
    static let fontNum: CGFloat = 28
}
