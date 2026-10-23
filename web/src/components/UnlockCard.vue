<script setup lang="ts">
// 流媒体与网站解锁检测（设计 50，修订第 192 条）：从节点访问 Netflix、Disney+、ChatGPT 等，判断能否使用与服务认定的地区。
// 只手动触发：管理员点“检测”，Agent 下一次上报时开始，约 1 分钟内出结果（详情页每 2 秒刷新，结果到达后自动显示）。
// 检测项内置在 Agent 中，面板只能请求“现在检测一次”（约束 1）。
// Agent 还按所在地区追加当地服务（item.local，修订第 196 条），单独列在“当地服务”下。
import { computed, ref } from 'vue'
import { errorText, requestUnlockCheck, type UnlockView } from '../api'
import { countryName } from '../countries'
import { fmtTime } from '../format'
import { isViewer } from '../store'

const props = defineProps<{
  serverId: number
  /** 节点详情接口返回的检测状态；旧版面板或尚未加载时为空 */
  unlock?: UnlockView
  online: boolean
}>()
const emit = defineEmits<{ requested: [] }>()

type Status = UnlockView['items'][number]['status']
const names: Record<string, string> = {
  netflix: 'Netflix', disney: 'Disney+', youtube: 'YouTube Premium', prime: 'Prime Video', tiktok: 'TikTok',
  chatgpt: 'ChatGPT', claude: 'Claude', google: 'Google 搜索', reddit: 'Reddit', steam: 'Steam 商店',
  yahoo: 'Yahoo', truthsocial: 'Truth Social', telegram: 'Telegram',
  abema: 'ABEMA', radiko: 'radiko', mytvsuper: 'myTV SUPER', viu: 'Viu', bilibili_hmt: 'bilibili 港澳台', bilibili_tw: 'bilibili 台湾',
  kktv: 'KKTV', coupangplay: 'Coupang Play', hulu: 'Hulu', bbc_iplayer: 'BBC iPlayer',
}
const statusText: Record<Status, string> = { yes: '可用', no: '不可用', partial: '部分可用', captcha: '需人机验证', failed: '检测失败' }

// 通用项与当地服务分开显示；当地服务的地区取自 item.local
const general = computed(() => props.unlock?.items.filter((it) => !it.local) ?? [])
const local = computed(() => props.unlock?.items.filter((it) => it.local) ?? [])
const localName = computed(() => (local.value[0]?.local ? countryName(local.value[0].local) : ''))

const busy = ref(false)
const error = ref('')
const pending = computed(() => busy.value || !!props.unlock?.pending)
async function check() {
  busy.value = true
  error.value = ''
  try {
    await requestUnlockCheck(props.serverId)
    emit('requested')
  } catch (e) {
    error.value = errorText(e, '请求失败')
  } finally {
    busy.value = false
  }
}
const hint = computed(() => {
  if (!props.online) return '节点离线'
  if (props.unlock && !props.unlock.supported) return 'Agent 版本不支持，升级后可用'
  return ''
})
</script>

<template>
  <section class="panel unlock">
    <div class="head">
      <h3>流媒体与网站解锁</h3>
      <span v-if="unlock?.checked_at" class="muted small">{{ fmtTime(unlock.checked_at) }} 检测</span>
      <button v-if="!isViewer" type="button" class="secondary small check" :disabled="pending || !!hint" :title="hint" @click="check">
        {{ pending ? '检测中…' : unlock?.checked_at ? '重新检测' : '检测' }}
      </button>
    </div>
    <p class="small muted note">
      从这台 VPS 访问各服务，判断能否使用以及服务认定的地区。只在点击时检测，约 1 分钟出结果。
      <template v-if="hint"> <b>{{ hint }}</b></template>
    </p>
    <p v-if="error" class="small bad">{{ error }}</p>
    <template v-if="unlock?.items.length">
      <template v-for="group in [{ key: 'general', title: '', items: general }, { key: 'local', title: `当地服务 · ${localName}`, items: local }]" :key="group.key">
        <h4 v-if="group.title && group.items.length" class="group">{{ group.title }}</h4>
        <ul v-if="group.items.length" class="items">
          <li v-for="it in group.items" :key="it.service">
            <span class="name">{{ names[it.service] ?? it.service }}</span>
            <span class="badge" :class="it.status">{{ statusText[it.status] }}</span>
            <span class="region num">{{ it.region ?? '' }}</span>
            <span v-if="it.note" class="small muted note-item" :title="it.note">{{ it.note }}</span>
          </li>
        </ul>
      </template>
    </template>
    <p v-else-if="pending" class="small muted">等待 Agent 检测…</p>
  </section>
</template>

<style scoped>
.unlock { padding: var(--space-4) var(--space-5); }
.head { display: flex; align-items: baseline; gap: var(--space-2); flex-wrap: wrap; }
.head h3 { margin: 0; }
.check { margin-left: auto; }
.note { margin: var(--space-1) 0 var(--space-3); }
.items { list-style: none; margin: 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: var(--space-2); }
.items li { display: grid; grid-template-columns: minmax(0, 1fr) auto 3.5em; grid-template-rows: auto auto; align-items: center; gap: 2px var(--space-2);
  padding: var(--space-2) var(--space-3); border-radius: var(--radius-sm); background: var(--surface-2); }
.name { font-weight: var(--weight-strong); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.badge { font-size: var(--font-xs); padding: 1px var(--space-2); border-radius: var(--radius-full); background: var(--surface); white-space: nowrap; }
.badge.yes { color: var(--ok); }
.badge.no { color: var(--bad); }
.badge.partial, .badge.captcha { color: var(--warn); }
.badge.failed { color: var(--text-muted); }
.region { text-align: right; color: var(--text-muted); font-size: var(--font-sm); }
.group { margin: var(--space-4) 0 var(--space-2); font-size: var(--font-sm); font-weight: var(--weight-strong); color: var(--text-muted); }
.note-item { grid-column: 1 / -1; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
</style>
