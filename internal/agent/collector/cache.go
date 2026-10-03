package collector

import "time"

// 降低每轮采集的开销（设计 4.2：平均 CPU < 0.5%，空闲接近 0）：
// 很少变化的信息（系统版本、温度传感器位置、整盘列表）缓存一段时间再刷新，而不是每 10 秒重新读取目录与文件。

// refreshEvery 是缓存信息的刷新间隔：系统升级、热插拔磁盘、加载传感器驱动后，最迟这么久反映到上报中。
const refreshEvery = 10 * time.Minute

// cached 保存一个按时间刷新的值。零值即可使用：首次 get 时加载。
type cached[T any] struct {
	v      T
	at     time.Time
	loaded bool
}

// get 在未加载或超过 ttl 时调用 load 刷新，返回当前值。
func (c *cached[T]) get(now time.Time, ttl time.Duration, load func() T) T {
	if !c.loaded || now.Sub(c.at) >= ttl {
		c.v, c.at, c.loaded = load(), now, true
	}
	return c.v
}

// invalidate 让下一次 get 重新加载（例如发现磁盘集合变化）。
func (c *cached[T]) invalidate() { c.loaded = false }

// sameKeys 判断两个 map 的键集合是否相同，用于发现磁盘增减而提前刷新缓存。
func sameKeys[V1, V2 any](a map[string]V1, b map[string]V2) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
