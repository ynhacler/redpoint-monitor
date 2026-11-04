<script setup lang="ts">
// 回程线路（设计 42.5，修订第 282 条）：Agent 每 6 小时追踪到电信、联通、移动各一个测速目标的路由，
// 面板按经过的骨干网地址段识别线路（CN2 GIA、4837、CMI 等）。识别结果只是建议，管理员可一键加到节点的线路标签。
// 点击一行展开逐跳列表；属于识别规则中骨干网的跳标出网络名称。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { bulkEditServers, errorText, getRoutes, type RoutesView, type ServerView } from '../api'
import { DASH, fmtAgo, fmtLatency } from '../format'
import { t } from '../i18n'
import { isPremiumLine } from '../lines'
import { isViewer, refresh } from '../store'

const props = defineProps<{
  /** 节点 */
  server: ServerView
}>()

const ispNames: Record<string, string> = { ct: '电信', cu: '联通', cm: '移动' }
const errorNames: Record<string, string> = {
  icmp_unavailable: '本机不允许非特权 ICMP', dns: '域名解析失败', blocked: '域名只解析到内网地址，已拒绝', error: '追踪失败',
}

const view = ref<RoutesView | null>(null)
const error = ref('')
const open = ref<number | null>(null)
const saving = ref(false)
const saveError = ref('')
let timer: ReturnType<typeof setTimeout> | undefined

async function load() {
  clearTimeout(timer)
  try {
    view.value = await getRoutes(props.server.id)
    error.value = ''
  } catch (e) {
    error.value = errorText(e, t('回程线路加载失败'))
  }
  timer = setTimeout(load, 5 * 60_000)
}
watch(() => props.server.id, () => { open.value = null; load() }, { immediate: true })
onBeforeUnmount(() => clearTimeout(timer))

const items = computed(() => view.value?.items ?? [])
const waiting = computed(() => items.value.length > 0 && items.value.every((x) => !x.at))
// 识别出、但还不在节点线路标签中的线路
const missing = computed(() => (view.value?.lines ?? []).filter((l) => !props.server.line_tags.includes(l)))

async function adopt() {
  saving.value = true
  saveError.value = ''
  try {
    await bulkEditServers({ ids: [props.server.id], add_line_tags: missing.value })
    await refresh()
  } catch (e) {
    saveError.value = errorText(e, t('保存失败'))
  } finally {
    saving.value = false
  }
}

function toggle(id: number) {
  open.value = open.value === id ? null : id
}
</script>

<template>
  <section class="panel routes">
    <div class="head">
      <h3>{{ t('回程线路') }}</h3>
      <span class="small muted sub">{{ t('每 6 小时追踪一次') }}</span>
      <button v-if="!isViewer && missing.length" type="button" class="btn secondary small" :disabled="saving" @click="adopt">
        {{ t('加到线路标签：{lines}', { lines: missing.join('、') }) }}
      </button>
    </div>
    <p v-if="error" class="small bad">{{ error }}</p>
    <template v-else-if="view">
      <p v-if="saveError" class="small bad">{{ saveError }}</p>
      <p v-if="view.status === 'off'" class="small muted note">{{ t('节点本机已关闭网络探测（/etc/vpsmon-agent/no-probe 存在），删除该文件后开启。') }}</p>
      <p v-else-if="!items.length" class="small muted note">
        {{ t('在测速目标中添加电信、联通或移动的目标后，自动追踪到这些目标的路由并识别线路。') }}
        <RouterLink v-if="!isViewer" to="/monitor">{{ t('去设置') }}</RouterLink>
      </p>
      <p v-else-if="waiting" class="small muted note">{{ t('Agent 收到目标后约 2 分钟追踪第一次（需要 Agent v0.9.9 或更高版本）。') }}</p>

      <ul v-if="items.length && !waiting" class="list">
        <li v-for="x in items" :key="x.target_id">
          <button type="button" class="row" :aria-expanded="open === x.target_id" :disabled="!x.hops.length" @click="toggle(x.target_id)">
            <span class="isp">{{ t(ispNames[x.isp] ?? x.isp) }}</span>
            <span class="where muted">{{ x.province || x.address }}</span>
            <span v-if="x.line" class="badge" :class="{ premium: isPremiumLine(x.line) }">{{ x.line }}</span>
            <span v-else-if="x.error" class="small warn">{{ t(errorNames[x.error] ?? x.error) }}</span>
            <span v-else-if="x.at" class="small muted">{{ t('未识别') }}</span>
            <span v-else class="small muted">{{ t('等待追踪') }}</span>
            <span class="small muted when">{{ x.at ? fmtAgo(x.at) : '' }}</span>
          </button>
          <table v-if="open === x.target_id" class="hops">
            <thead><tr><th scope="col">#</th><th scope="col">{{ t('地址') }}</th><th scope="col">{{ t('网络') }}</th><th scope="col">{{ t('延迟') }}</th></tr></thead>
            <tbody>
              <tr v-for="h in x.hops" :key="h.ttl" :class="{ known: h.net }">
                <td class="num">{{ h.ttl }}</td>
                <td class="ip">{{ h.ip || '*' }}</td>
                <td>{{ h.net ? `${t(h.net)} · AS${h.asn}` : DASH }}</td>
                <td class="num">{{ h.rtt_ms != null ? fmtLatency(h.rtt_ms) : DASH }}</td>
              </tr>
            </tbody>
          </table>
          <p v-if="open === x.target_id && !x.reached" class="small muted hop-note">{{ t('没有收到目标本身的回复（目标或最后几跳不回应 ICMP），识别只依据已收到的跳。') }}</p>
        </li>
      </ul>
      <p v-if="items.length && !waiting" class="small muted foot">{{ t('按经过的骨干网地址段识别（如 59.43 为 CN2），仅供参考；点击一行查看逐跳路由。') }}</p>
    </template>
  </section>
</template>

<style scoped>
.head { display: flex; align-items: center; gap: var(--space-3); margin-bottom: var(--space-3); flex-wrap: wrap; }
.head h3 { margin: 0; }
.head .sub { margin-right: auto; }
.note { margin: 0; }
.list { list-style: none; margin: 0; padding: 0; }
.list li { border-bottom: 1px solid var(--border); }
.row { display: flex; align-items: center; gap: var(--space-3); width: 100%; min-height: 40px; padding: var(--space-2) 0;
  background: none; border: 0; color: inherit; font: inherit; font-size: var(--font-sm); text-align: left; cursor: pointer; }
.row:disabled { cursor: default; }
.row:not(:disabled):hover { background: var(--surface-2); }
.row:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
.isp { flex: none; width: 3em; font-weight: var(--weight-strong); }
.where { flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.when { flex: none; margin-left: auto; padding-right: var(--space-1); }
.badge { flex: none; font-size: var(--font-xs); line-height: 18px; padding: 0 6px; border-radius: var(--radius-sm);
  border: 1px solid var(--border); color: var(--text-muted); font-weight: var(--weight-strong); white-space: nowrap; }
.badge.premium { color: var(--accent); border-color: var(--accent); }
.warn { color: var(--warn); }
.hops { width: 100%; border-collapse: collapse; font-size: var(--font-xs); margin: 0 0 var(--space-2); }
.hops th, .hops td { padding: 3px var(--space-2); text-align: left; white-space: nowrap; }
.hops thead th { color: var(--text-muted); font-weight: var(--weight-regular); }
.hops .num { text-align: right; font-family: var(--font-mono); }
.hops .ip { font-family: var(--font-mono); }
.hops tr.known td { color: var(--accent); }
.hop-note { margin: 0 0 var(--space-2); }
.foot { margin: var(--space-3) 0 0; }
@media (max-width: 600px) {
  .hops td:nth-child(3) { white-space: normal; }
}
</style>
