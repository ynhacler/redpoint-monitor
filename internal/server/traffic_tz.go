package server

import (
	"sync"
	"time"
	_ "time/tzdata" // 内嵌时区数据库：精简系统（Alpine 未装 tzdata、容器）上也能识别 IANA 时区名
)

// 节点的计费时区（设计 5.4）：服务商按自己的时区换月与计日，例如美国的服务商在洛杉矶时间每月 1 日 0 点重置。
// 每日流量按节点时区写入 traffic_daily；计费周期、最近 7 天日均、每日与月度汇总都按该时区计算。
// 修改时区只影响之后写入的流量，已有的按天汇总不重新划分（当个周期的边界可能有几小时误差）。

var locCache sync.Map // 时区名 → *time.Location；LoadLocation 每次都要解析时区数据

// validTimezone 判断是否为可用的 IANA 时区名。“Local” 依赖面板主机的设置，不允许（空字符串表示面板时区）。
func validTimezone(tz string) bool {
	if tz == "" || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// trafficLocation 返回节点的计费时区；未设置或无效时为面板本地时区。
func trafficLocation(row ServerRow) *time.Location {
	tz := row.TrafficTimezone
	if tz == "" {
		return time.Local
	}
	if v, ok := locCache.Load(tz); ok {
		return v.(*time.Location)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Local
	}
	locCache.Store(tz, loc)
	return loc
}
