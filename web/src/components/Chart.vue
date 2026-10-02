<script lang="ts">
/** 一条曲线 */
export interface Series {
  /** 图例名称 */
  name: string
  /** [时间（毫秒）, 值]；值为 null 时断开 */
  data: [number, number | null][]
}
</script>

<script setup lang="ts">
// 详情页折线图（设计 41.3 Chart、41.5）：统一主题，折线 1.5px、无数据点标记、面积透明度 ≤ 10%，
// 网格线为 border 色虚线，同一图表最多 3 条线，颜色依次 accent / ok / warn；悬停显示同一时刻的全部数值。
// 颜色从 CSS 令牌读取，切换深浅色时自动重绘。
import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import * as echarts from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

// 按需引入，只打包折线图与柱状图需要的模块
echarts.use([BarChart, LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

const props = defineProps<{
  /** 图表标题 */
  title: string
  /** 最多 3 条（设计 41.5） */
  series: Series[]
  /** 把数值格式化为坐标轴与提示中的文字 */
  format: (v: number) => string
  /** 纵轴最大值，如百分比图固定为 100 */
  max?: number
  /** bar：柱状图（每日流量），横轴按天；默认折线 */
  kind?: 'line' | 'bar'
}>()

const el = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | undefined
let observer: ResizeObserver | undefined
let themeQuery: MediaQueryList | undefined

const token = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(`--${name}`).trim()

function render() {
  if (!chart) return
  const colors = [token('accent'), token('ok'), token('warn')]
  const muted = token('text-muted')
  const border = token('border')
  const fs = parseInt(token('font-xs')) || 11
  chart.setOption(
    {
      animation: false,
      color: colors,
      grid: { left: 8, right: 12, top: 28, bottom: 4, containLabel: true },
      legend: props.series.length > 1
        ? { top: 0, right: 0, itemWidth: 12, itemHeight: 2, textStyle: { color: muted, fontSize: fs } }
        : undefined,
      tooltip: {
        trigger: 'axis',
        backgroundColor: token('surface'),
        borderColor: border,
        textStyle: { color: token('text'), fontSize: fs + 1 },
        valueFormatter: (v: unknown) => (typeof v === 'number' ? props.format(v) : '—'),
      },
      xAxis: {
        type: 'time',
        axisLine: { lineStyle: { color: border } },
        axisTick: { show: false },
        axisLabel: { color: muted, fontSize: fs, hideOverlap: true },
        splitLine: { show: false },
      },
      yAxis: {
        type: 'value',
        min: 0,
        max: props.max,
        // 网格线最多 4 条（设计 41.5）；固定上限（百分比）时按 1/4 等分，避免 90% 与 100% 两个刻度挤在一起
        interval: props.max ? props.max / 4 : undefined,
        splitNumber: 3,
        axisLabel: { color: muted, fontSize: fs, formatter: (v: number) => props.format(v) },
        splitLine: { lineStyle: { color: border, type: 'dashed' } },
      },
      series: props.series.slice(0, 3).map((s) =>
        props.kind === 'bar'
          ? { name: s.name, type: 'bar', data: s.data, barMaxWidth: 16, itemStyle: { borderRadius: [2, 2, 0, 0] } }
          : {
              name: s.name,
              type: 'line',
              data: s.data,
              showSymbol: false,
              connectNulls: false,
              lineStyle: { width: 1.5 },
              areaStyle: { opacity: 0.08 },
            },
      ),
    },
    { notMerge: true },
  )
}

onMounted(() => {
  if (!el.value) return
  chart = echarts.init(el.value)
  render()
  observer = new ResizeObserver(() => chart?.resize())
  observer.observe(el.value)
  // 深浅色切换：系统主题变化或页面手动切换（data-theme 属性变化）时重绘
  themeQuery = window.matchMedia('(prefers-color-scheme: dark)')
  themeQuery.addEventListener('change', render)
  window.addEventListener('vpsmon-theme', render)
})
onBeforeUnmount(() => {
  observer?.disconnect()
  themeQuery?.removeEventListener('change', render)
  window.removeEventListener('vpsmon-theme', render)
  chart?.dispose()
})
watch(() => [props.series, props.max, props.kind], render, { deep: true })
</script>

<template>
  <div class="panel chart">
    <h3>{{ title }}</h3>
    <div ref="el" class="canvas" />
  </div>
</template>

<style scoped>
.chart { padding: var(--space-4); min-width: 0; }
.chart h3 { font-size: var(--font-sm); line-height: var(--line-sm); color: var(--text-muted); font-weight: var(--weight-strong); }
.canvas { height: 180px; margin-top: var(--space-2); }
</style>
