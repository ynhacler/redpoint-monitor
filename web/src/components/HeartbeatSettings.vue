<script setup lang="ts">
// 面板心跳（设计 1.10.3，修订第 159 条）：面板按间隔请求外部监控服务给的地址；面板停止或断网时心跳中断，由那边告警。
// 地址中常含令牌：保存后只显示协议与主机，输入框留空表示保留原地址。
import { onMounted, reactive, ref } from 'vue'
import { ApiError, errorText, getHeartbeat, saveHeartbeat, testHeartbeat, type HeartbeatView } from '../api'
import { fmtDateTime } from '../format'

const intervals = [1, 2, 5, 10, 15, 30, 60]
const view = ref<HeartbeatView | null>(null)
const form = reactive({ enabled: false, url: '', minutes: 5 })
const errors = ref<Record<string, string>>({})
const busy = ref<'' | 'save' | 'test'>('')
const saved = ref(false)
const error = ref('')

function fill(v: HeartbeatView) {
  view.value = v
  Object.assign(form, { enabled: v.enabled, url: '', minutes: Math.round(v.interval / 60) })
}

onMounted(async () => {
  try {
    fill(await getHeartbeat())
  } catch (e) {
    error.value = errorText(e, '加载失败')
  }
})

async function run(kind: 'save' | 'test') {
  busy.value = kind
  saved.value = false
  errors.value = {}
  error.value = ''
  try {
    if (kind === 'save' || form.url) fill(await saveHeartbeat({ enabled: form.enabled, url: form.url.trim(), interval: form.minutes * 60 }))
    if (kind === 'save') saved.value = true
    else fill(await testHeartbeat())
  } catch (e) {
    if (e instanceof ApiError && e.details.length) errors.value = Object.fromEntries(e.details.map((d) => [d.field, d.message]))
    else error.value = errorText(e, kind === 'save' ? '保存失败' : '发送失败')
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <form class="panel heartbeat" novalidate @submit.prevent="run('save')">
    <div class="head">
      <label class="check title"><input v-model="form.enabled" type="checkbox" />面板心跳</label>
      <span v-if="view?.last_at" class="badge" :class="view.last_ok ? 'good' : 'bad'">
        {{ view.last_ok ? '最近一次成功' : '最近一次失败' }} · {{ fmtDateTime(view.last_at) }}
      </span>
    </div>
    <p class="muted small">
      面板自己停止或断网时，上面的告警都发不出去。在 Healthchecks.io、Uptime Kuma（推送监控）、Better Stack 等外部服务建一个心跳监控，
      把它给的地址填在这里：面板按间隔访问这个地址，超时没收到时由那边通知你。请求不带任何节点数据。
    </p>
    <div class="fields" :class="{ off: !form.enabled }">
      <label class="url">地址
        <input v-model="form.url" type="url" inputmode="url" autocomplete="off" spellcheck="false"
          :placeholder="view?.url_set ? `已保存：${view.url}（留空保留）` : 'https://hc-ping.com/…'" />
        <small v-if="errors.url" class="err">{{ errors.url }}</small>
        <small v-else class="muted">只支持 HTTPS；地址中常含令牌，保存后只显示主机</small>
      </label>
      <label>间隔
        <select v-model.number="form.minutes">
          <option v-for="m in intervals" :key="m" :value="m">每 {{ m }} 分钟</option>
        </select>
        <small v-if="errors.interval" class="err">{{ errors.interval }}</small>
        <small v-else class="muted">外部服务的宽限期应大于这个间隔</small>
      </label>
    </div>
    <p v-if="view?.last_error" class="err small">最近一次：{{ view.last_error }}</p>
    <p v-if="error" class="err small">{{ error }}</p>
    <div class="actions">
      <button type="submit" :disabled="!!busy">{{ busy === 'save' ? '保存中…' : '保存' }}</button>
      <button type="button" class="secondary" :disabled="!!busy || (!view?.url_set && !form.url)" @click="run('test')">
        {{ busy === 'test' ? '发送中…' : '立即发送' }}
      </button>
      <span v-if="saved" class="ok small">已保存</span>
    </div>
  </form>
</template>

<style scoped>
.heartbeat { padding: var(--space-4); margin-bottom: var(--space-4); }
.head { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; }
.title { font-weight: var(--weight-strong); }
.check { display: inline-flex; align-items: center; gap: var(--space-2); }
.check input { width: auto; }
.badge { font-size: var(--font-xs); padding: 1px var(--space-2); border-radius: var(--radius-full); }
.badge.good { background: color-mix(in srgb, var(--ok) 12%, transparent); color: var(--ok); }
.badge.bad { background: color-mix(in srgb, var(--bad) 12%, transparent); color: var(--bad); }
.heartbeat > p { margin: var(--space-2) 0 var(--space-3); }
.fields { display: grid; grid-template-columns: minmax(0, 1fr) 170px; gap: var(--space-3); }
.fields.off { opacity: .55; }
.fields label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); min-width: 0; }
.actions { display: flex; align-items: center; gap: var(--space-3); margin-top: var(--space-3); flex-wrap: wrap; }
.err { color: var(--bad); }
.ok { color: var(--ok); }
@media (max-width: 600px) {
  .fields { grid-template-columns: minmax(0, 1fr); }
}
</style>
