<script setup lang="ts">
// 节点的静音与维护（设计 1.5.14、1.5.15、16.6）：详情页顶部的按钮与状态横幅。
// 静音：照常评估与记录，不通知、不计入“需要关注”；维护：继续采集，不产生告警（10 秒内生效）。
import { computed, ref } from 'vue'
import { createSilence, endSilence, errorText, type ServerView, type Silence } from '../api'
import { fmtTime } from '../format'
import { refresh } from '../store'

const props = defineProps<{
  /** 节点 */
  server: ServerView
}>()

const busy = ref(false)
const error = ref('')
const menu = ref<'' | 'mute' | 'maintenance'>('')
const reason = ref('')

const durations = [
  { v: '1h', t: '1 小时' }, { v: '8h', t: '8 小时' }, { v: '24h', t: '24 小时' }, { v: '', t: '直到手动恢复' },
] as const

async function run(f: () => Promise<unknown>) {
  busy.value = true
  error.value = ''
  try {
    await f()
    menu.value = ''
    reason.value = ''
    await refresh()
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
const start = (kind: Silence['kind'], duration: '' | '1h' | '8h' | '24h') =>
  run(() => createSilence({ kind, scope_type: 'server', scope_id: String(props.server.id), duration, reason: reason.value.trim() }))
const stop = (x: Silence) => run(() => endSilence(x.id))

const until = (x: Silence) => (x.ends_at ? `至 ${fmtTime(x.ends_at)}` : '直到手动恢复')
const scopeText = (x: Silence) =>
  x.scope_type === 'server' ? '' : x.scope_type === 'group' ? `（分组 ${x.scope_id}）` : x.scope_type === 'global' ? '（全部节点）' : ''
const m = computed(() => props.server.maintenance)
const mu = computed(() => props.server.muted)
</script>

<template>
  <div class="silence">
    <div class="buttons">
      <button v-if="!mu" type="button" class="btn secondary" :disabled="busy" @click="menu = menu === 'mute' ? '' : 'mute'">静音告警</button>
      <button v-if="!m" type="button" class="btn secondary" :disabled="busy" @click="menu = menu === 'maintenance' ? '' : 'maintenance'">维护模式</button>
    </div>

    <div v-if="menu" class="menu panel">
      <p class="small muted">
        {{ menu === 'mute' ? '静音期间照常记录告警，但不发送通知、不计入“需要关注”。' : '维护期间继续采集与保存数据，不产生任何告警；进行中的告警会结束。' }}
      </p>
      <input v-model="reason" maxlength="200" :placeholder="menu === 'mute' ? '原因（可选），如：迁移中' : '原因（可选），如：升级内核'" />
      <div class="choices">
        <button v-for="d in durations" :key="d.v" type="button" class="secondary" :disabled="busy" @click="start(menu as Silence['kind'], d.v)">{{ d.t }}</button>
        <button type="button" class="text" @click="menu = ''">取消</button>
      </div>
    </div>

    <div v-if="m" class="banner info">
      <span><b>维护中</b> · {{ until(m) }}<template v-if="m.reason"> · {{ m.reason }}</template> · 不产生告警</span>
      <button type="button" class="text" :disabled="busy" @click="stop(m)">结束维护</button>
    </div>
    <div v-if="mu" class="banner info">
      <span><b>告警已静音</b>{{ scopeText(mu) }} · {{ until(mu) }}<template v-if="mu.reason"> · {{ mu.reason }}</template></span>
      <button type="button" class="text" :disabled="busy" @click="stop(mu)">取消静音</button>
    </div>
    <p v-if="error" class="small err">{{ error }}</p>
  </div>
</template>

<style scoped>
.buttons { display: flex; gap: var(--space-2); flex-wrap: wrap; }
.menu { padding: var(--space-3) var(--space-4); margin-top: var(--space-2); display: flex; flex-direction: column; gap: var(--space-2); }
.menu p { margin: 0; }
.choices { display: flex; gap: var(--space-2); flex-wrap: wrap; }
.banner.info { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); margin: var(--space-3) 0 0;
  background: var(--surface-2); border-color: var(--border); color: var(--text); }
.err { color: var(--bad); }
</style>
