<script lang="ts">
import { ref as vref } from 'vue'

/** 百分比图的纵轴（修订第 234 条）：auto 按数据自动缩放（低负载时也看得清趋势），fixed 固定 0～100%；所有图表共用，记在本机 */
export const axisMode = vref<'auto' | 'fixed'>(readAxisMode())
function readAxisMode(): 'auto' | 'fixed' {
  try {
    return localStorage.getItem('vpsmon.chartAxis') === 'fixed' ? 'fixed' : 'auto'
  } catch {
    return 'auto'
  }
}

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
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { t } from '../i18n'

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
  /** 不显示图例（外部已有图例时使用）；默认多条线时显示 */
  hideLegend?: boolean
}>()

const el = ref<HTMLDivElement | null>(null)

// 本区间的平均与峰值（修订第 234 条）：只算有数据的点，缺失的点不当作 0
const stats = computed(() => props.kind === 'bar' ? [] : props.series.slice(0, 3).map((s) => {
  const v = s.data.map((d) => d[1]).filter((x): x is number => x != null && Number.isFinite(x))
  return { name: s.name, n: v.length, avg: v.length ? v.reduce((a, b) => a + b, 0) / v.length : 0, max: v.length ? Math.max(...v) : 0 }
}).filter((x) => x.n > 0))
const fixed = computed(() => !!props.max && axisMode.value === 'fixed')
function setAxis(m: 'auto' | 'fixed') {
  axisMode.value = m
  try {
    localStorage.setItem('vpsmon.chartAxis', m)
  } catch {
    /* 隐私模式等不可用时只在本页生效 */
  }
}
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
      grid: { left: 8, right: 12, top: props.series.length > 1 && !props.hideLegend ? 28 : 12, bottom: 4, containLabel: true },
      legend: props.series.length > 1 && !props.hideLegend
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
        // 百分比图：固定 0～100% 时按 1/4 等分（避免 90% 与 100% 挤在一起）；自动缩放时上限随数据，但不超过 max（修订第 234 条）
        max: fixed.value ? props.max : props.max ? (v: { max: number }) => Math.min(props.max!, niceMax(v.max)) : undefined,
        interval: fixed.value ? props.max! / 4 : undefined,
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
              // 只有一条线时浅色填充；多条线为纯折线，避免填充重叠后颜色混浊（修订第 251 条，UI-010）
              areaStyle: props.series.length === 1 ? { opacity: 0.08 } : undefined,
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
watch(() => [props.series, props.max, props.kind, axisMode.value], render, { deep: true })

/** 自动缩放的上限：比最大值略高的整齐数（1、2、5 × 10ⁿ），至少 1，避免几乎为 0 的数据被放大成满屏噪声 */
function niceMax(v: number): number {
  const x = Math.max(1, v * 1.15)
  const p = 10 ** Math.floor(Math.log10(x))
  return ([1, 2, 5, 10].find((m) => m * p >= x) ?? 10) * p
}
</script>

<template>
  <div class="panel chart">
    <div class="head">
      <h3>{{ title }}</h3>
      <div v-if="max" class="axis" role="group" :aria-label="t('纵轴')">
        <button type="button" :class="{ on: !fixed }" :aria-pressed="!fixed" @click="setAxis('auto')">{{ t('自动') }}</button>
        <button type="button" :class="{ on: fixed }" :aria-pressed="fixed" @click="setAxis('fixed')">0–{{ format(max) }}</button>
      </div>
    </div>
    <p v-if="stats.length" class="stats num">
      <span v-for="x in stats" :key="x.name">
        <template v-if="stats.length > 1 || series.length > 1">{{ x.name }}：</template>{{ t('平均 {avg} · 峰值 {max}', { avg: format(x.avg), max: format(x.max) }) }}
      </span>
      <span class="muted">{{ t('（本区间）') }}</span>
    </p>
    <div ref="el" class="canvas" />
  </div>
</template>

<style scoped>
.chart { padding: var(--space-4); min-width: 0; }
.head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); }
.axis { display: inline-flex; border: 1px solid var(--border); border-radius: var(--radius-sm); overflow: hidden; }
.axis button { background: transparent; border: 0; border-radius: 0; padding: 0 var(--space-2); font-size: var(--font-xs); line-height: 20px;
  color: var(--text-muted); }
.axis button + button { border-left: 1px solid var(--border); }
.axis button.on { background: var(--surface-2); color: var(--text); font-weight: var(--weight-strong); }
.stats { margin: 2px 0 0; font-size: var(--font-xs); color: var(--text-muted); display: flex; flex-wrap: wrap; gap: 0 var(--space-3); }
.chart h3 { font-size: var(--font-sm); line-height: var(--line-sm); color: var(--text-muted); font-weight: var(--weight-strong); }
.canvas { height: 180px; margin-top: var(--space-2); }
</style>
