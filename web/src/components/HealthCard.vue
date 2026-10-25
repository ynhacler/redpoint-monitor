<script setup lang="ts">
// 健康摘要（设计 1.5.7）：面板把最近 24 小时 / 30 天的指标归纳成几句话，避免逐张图判断。
// 结论在面板计算（GET /servers/{id}/health），App 显示同样的文字；摘要变化慢，每分钟刷新一次。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, getHealth, type HealthSummary } from '../api'
import { t } from '../i18n'

const props = defineProps<{ serverId: number }>()
const health = ref<HealthSummary | null>(null)
const error = ref('')
// 默认只显示需要关注的项（最多 3 条，严重的在前）；全部正常时一句话概括，点“详情”展开全部（修订第 256 条，UI 精修 3.2）
const expanded = ref(false)
const rank: Record<string, number> = { bad: 0, warn: 1 }
const abnormal = computed(() => (health.value?.items ?? []).filter((it) => it.level === 'bad' || it.level === 'warn')
  .sort((a, b) => (rank[a.level] ?? 2) - (rank[b.level] ?? 2)))
const shown = computed(() => (expanded.value ? health.value?.items ?? [] : abnormal.value.slice(0, 3)))
const okNames = computed(() => (health.value?.items ?? []).filter((it) => it.level !== 'bad' && it.level !== 'warn').map((it) => it.title))
let timer: ReturnType<typeof setTimeout> | undefined

async function load() {
  clearTimeout(timer)
  try {
    health.value = await getHealth(props.serverId)
    error.value = ''
  } catch (e) {
    error.value = errorText(e, t('健康摘要加载失败'))
  }
  timer = setTimeout(load, 60_000)
}
watch(() => props.serverId, () => {
  health.value = null
  load()
}, { immediate: true })
onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <section class="panel health">
    <div class="head">
      <h3>{{ t('健康摘要') }}</h3>
      <span v-if="health" class="status small" :class="health.level">{{ health.status }}</span>
    </div>
    <p v-if="error" class="small bad">{{ error }}</p>
    <p v-if="health?.items.length && !shown.length" class="small muted ok-line">{{ t('{names}均正常', { names: okNames.join(t('、')) }) }}</p>
    <dl v-if="shown.length" class="items">
      <template v-for="it in shown" :key="it.key">
        <dt class="small"><span class="dot" :class="it.level" />{{ it.title }}</dt>
        <dd class="small">{{ it.text }}</dd>
      </template>
    </dl>
    <button v-if="health && health.items.length > shown.length || expanded" type="button" class="text small more" :aria-expanded="expanded"
      @click="expanded = !expanded">{{ expanded ? t('收起') : t('详情（{n} 项）', { n: health!.items.length }) }}</button>
    <p v-else-if="health" class="small muted">{{ t('还没有足够的历史数据。') }}</p>
  </section>
</template>

<style scoped>
.health { padding: var(--space-4) var(--space-5); }
.head { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); flex-wrap: wrap; margin-bottom: var(--space-3); }
.head h3 { margin: 0; font-size: var(--font-lg); }
.status { font-weight: var(--weight-strong); }
.status.ok { color: var(--ok); }
.status.warn { color: var(--warn); }
.status.bad { color: var(--bad); }
.status.muted { color: var(--text-muted); }
.ok-line { margin: 0; }
.more { padding: var(--space-1) 0 0; min-height: 32px; }
.items { display: grid; grid-template-columns: max-content 1fr; gap: var(--space-2) var(--space-4); margin: 0; }
.items dt { display: flex; align-items: center; gap: var(--space-2); color: var(--text-muted); }
.items dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
.dot { width: 8px; height: 8px; border-radius: var(--radius-full); background: var(--ok); flex: none; }
.dot.warn { background: var(--warn); }
.dot.bad { background: var(--bad); }
</style>
