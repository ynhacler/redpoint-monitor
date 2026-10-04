<script setup lang="ts">
// 通知渠道（设计 16.5、31）：Telegram 与 Webhook，由面板直接发送。
// 凭证只在填写时提交，之后只显示脱敏后的值；修改时留空表示保持原值。
import { onMounted, reactive, ref } from 'vue'
import {
  ApiError, createChannel, deleteChannel, errorText, listChannels, listDeliveries, testChannel, updateChannel, type ChannelType, type Delivery, type NotifyChannel, type NotifyChannelInput,
} from '../api'
import { severityNames } from '../alertRules'
import { fmtDateTime } from '../format'
import QuietHoursSettings from './QuietHoursSettings.vue'


const channels = ref<NotifyChannel[]>([])
const deliveries = ref<Delivery[]>([])
const error = ref('')
const loaded = ref(false)

function fail(e: unknown) {
  error.value = errorText(e)
}

async function load() {
  try {
    const [c, d] = await Promise.all([listChannels(), listDeliveries(30)])
    channels.value = c.items
    deliveries.value = d.items
  } catch (e) {
    fail(e)
  } finally {
    loaded.value = true
  }
}
onMounted(load)

// ---- 新增 / 编辑表单 ----
const editing = ref<number | 'new' | null>(null)
const form = reactive({
  type: 'telegram' as ChannelType, name: '', enabled: true, min_severity: 'warning' as NotifyChannel['min_severity'],
  notify_resolved: true, bot_token: '', chat_id: '', url: '', secret: '', clear_secret: false,
})
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)

function openNew(type: ChannelType) {
  Object.assign(form, { type, name: type === 'telegram' ? 'Telegram' : 'Webhook', enabled: true, min_severity: 'warning',
    notify_resolved: true, bot_token: '', chat_id: '', url: '', secret: '', clear_secret: false })
  fieldErrors.value = {}
  editing.value = 'new'
}
function openEdit(c: NotifyChannel) {
  Object.assign(form, { type: c.type, name: c.name, enabled: c.enabled, min_severity: c.min_severity, notify_resolved: c.notify_resolved,
    bot_token: '', chat_id: c.config.chat_id ?? '', url: '', secret: '', clear_secret: false })
  fieldErrors.value = {}
  editing.value = c.id
}

async function save() {
  saving.value = true
  fieldErrors.value = {}
  const body: NotifyChannelInput = {
    name: form.name, enabled: form.enabled, min_severity: form.min_severity, notify_resolved: form.notify_resolved,
    config: form.type === 'telegram'
      ? { bot_token: form.bot_token.trim() || undefined, chat_id: form.chat_id.trim() }
      : { url: form.url.trim() || undefined, secret: form.secret || undefined, clear_secret: form.clear_secret || undefined },
  }
  try {
    if (editing.value === 'new') await createChannel({ ...body, type: form.type })
    else await updateChannel(editing.value as number, body)
    editing.value = null
    await load()
  } catch (e) {
    if (e instanceof ApiError && e.details.length) {
      fieldErrors.value = Object.fromEntries(e.details.map((d) => [d.field.replace('config.', ''), d.message]))
    } else fail(e)
  } finally {
    saving.value = false
  }
}

async function remove(c: NotifyChannel) {
  if (!confirm(`删除通知渠道“${c.name}”？`)) return
  try {
    await deleteChannel(c.id)
    await load()
  } catch (e) {
    fail(e)
  }
}

async function toggle(c: NotifyChannel) {
  try {
    await updateChannel(c.id, { name: c.name, enabled: !c.enabled, config: {} })
    await load()
  } catch (e) {
    fail(e)
  }
}

// ---- 测试通知 ----
const testing = ref<number | null>(null)
const testResult = ref<Record<number, { ok: boolean; error?: string }>>({})
async function test(c: NotifyChannel) {
  testing.value = c.id
  try {
    testResult.value = { ...testResult.value, [c.id]: await testChannel(c.id) }
    deliveries.value = (await listDeliveries(30)).items
  } catch (e) {
    fail(e)
  } finally {
    testing.value = null
  }
}

const kindNames: Record<Delivery['kind'], string> = {
  firing: '告警', resolved: '恢复', repeat: '重复提醒', test: '测试',
  flapping: '频繁变化', still_firing: '仍在告警', panel_down: '面板异常', panel_up: '面板恢复', quiet_summary: '免打扰汇总', reminder: '提醒',
}
const statusNames: Record<Delivery['status'], string> = { sent: '已发送', failed: '失败', retrying: '重试中' }
const typeNames: Record<ChannelType, string> = { telegram: 'Telegram', webhook: 'Webhook' }
const target = (c: NotifyChannel) => (c.type === 'telegram' ? `Chat ${c.config.chat_id} · ${c.config.bot_token}` : `${c.config.url}${c.config.has_secret ? ' · 已设置签名' : ''}`)
</script>

<template>
  <div>
    <p class="muted small intro">
      告警触发、恢复以及严重告警未恢复时的重复提醒，会发送到下列渠道；已静音的告警不发送。
      节点到期（剩 30 / 14 / 7 / 3 / 1 天、当天、已过期）与云账户提醒（超预算、流量包用到 90% / 95%、同步失败）也从这里发送，各发一次。
      同时有 5 台以上节点离线会合并为一条；状态频繁变化的告警只提醒一次；全部节点同时停止上报时发面板告警。渠道由本面板直接发送，凭证只保存在本面板。
    </p>
    <p v-if="error" class="banner">{{ error }}</p>

    <QuietHoursSettings />

    <div class="actions">
      <button type="button" @click="openNew('telegram')">添加 Telegram</button>
      <button type="button" class="secondary" @click="openNew('webhook')">添加 Webhook</button>
    </div>

    <!-- 新增 / 编辑 -->
    <form v-if="editing !== null" class="panel editor" novalidate @submit.prevent="save">
      <h3>{{ editing === 'new' ? `添加 ${typeNames[form.type]}` : `编辑 ${form.name}` }}</h3>
      <div class="fields">
        <label>名称
          <input v-model="form.name" maxlength="40" />
          <small v-if="fieldErrors.name" class="err">{{ fieldErrors.name }}</small>
        </label>
        <template v-if="form.type === 'telegram'">
          <label>Bot Token
            <input v-model="form.bot_token" autocomplete="off" spellcheck="false" :placeholder="editing === 'new' ? '123456789:AA…（从 @BotFather 获取）' : '留空保持不变'" />
            <small v-if="fieldErrors.bot_token" class="err">{{ fieldErrors.bot_token }}</small>
          </label>
          <label>Chat ID
            <input v-model="form.chat_id" spellcheck="false" placeholder="个人 / 群组的数字 ID，或 @频道名" />
            <small v-if="fieldErrors.chat_id" class="err">{{ fieldErrors.chat_id }}</small>
            <small v-else class="muted">先向机器人发一条消息（群组需把机器人拉进群），再通过 @userinfobot 等获取 ID</small>
          </label>
        </template>
        <template v-else>
          <label class="wide">Webhook 地址
            <input v-model="form.url" spellcheck="false" :placeholder="editing === 'new' ? 'https://…' : '留空保持不变'" />
            <small v-if="fieldErrors.url" class="err">{{ fieldErrors.url }}</small>
            <small v-else class="muted">POST JSON（version、kind、title、text、server、severity、message 等）；只支持 HTTPS，本机回环地址除外</small>
          </label>
          <label>签名密钥（可选）
            <input v-model="form.secret" type="password" autocomplete="new-password" :placeholder="editing === 'new' ? '' : '留空保持不变'" />
            <small v-if="fieldErrors.secret" class="err">{{ fieldErrors.secret }}</small>
            <small v-else class="muted">设置后请求头带 X-Vpsmon-Signature: sha256=HMAC(密钥, 请求体)</small>
          </label>
          <label v-if="editing !== 'new'" class="check"><input v-model="form.clear_secret" type="checkbox" />清除签名密钥</label>
        </template>
        <label>最低级别
          <select v-model="form.min_severity">
            <option value="critical">只发严重</option>
            <option value="warning">警告及以上</option>
            <option value="info">全部（含提示）</option>
          </select>
        </label>
        <label class="check"><input v-model="form.notify_resolved" type="checkbox" />恢复时也通知</label>
        <label class="check"><input v-model="form.enabled" type="checkbox" />启用</label>
      </div>
      <div class="actions">
        <button type="submit" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
        <button type="button" class="secondary" @click="editing = null">取消</button>
      </div>
    </form>

    <!-- 渠道列表 -->
    <p v-if="loaded && !channels.length && editing === null" class="muted empty">还没有通知渠道。添加后告警会推送到 Telegram 或你的 Webhook。</p>
    <ul v-if="channels.length" class="panel list">
      <li v-for="c in channels" :key="c.id" :class="{ off: !c.enabled }">
        <div class="ch">
          <div class="ch-main">
            <div class="ch-name">
              <span class="type">{{ typeNames[c.type] }}</span>{{ c.name }}
              <span v-if="!c.enabled" class="muted small">已停用</span>
            </div>
            <div class="muted small ellipsis">{{ target(c) }}</div>
            <div class="muted small">{{ c.min_severity === 'info' ? '全部级别' : `${severityNames[c.min_severity]}及以上` }}{{ c.notify_resolved ? ' · 含恢复通知' : '' }}</div>
            <div v-if="testResult[c.id]" class="small" :class="testResult[c.id].ok ? 'ok' : 'err'">
              {{ testResult[c.id].ok ? '✓ 测试通知已发送，请在接收端确认' : `✗ ${testResult[c.id].error}` }}
            </div>
          </div>
          <div class="ch-actions">
            <button type="button" class="secondary" :disabled="testing === c.id" @click="test(c)">{{ testing === c.id ? '发送中…' : '发送测试' }}</button>
            <button type="button" class="text" @click="toggle(c)">{{ c.enabled ? '停用' : '启用' }}</button>
            <button type="button" class="text" @click="openEdit(c)">编辑</button>
            <button type="button" class="text danger-text" @click="remove(c)">删除</button>
          </div>
        </div>
      </li>
    </ul>

    <!-- 投递记录 -->
    <section v-if="deliveries.length" class="section">
      <h3>最近投递</h3>
      <ul class="panel list deliveries">
        <li v-for="d in deliveries" :key="d.id" :class="d.status">
          <span class="time num small">{{ fmtDateTime(d.created_at) }}</span>
          <span class="small kind">{{ kindNames[d.kind] }}</span>
          <span class="title ellipsis" :title="d.title">{{ d.title }}</span>
          <span class="small muted ellipsis">{{ d.channel_name }}</span>
          <span class="small status" :title="d.last_error">{{ statusNames[d.status] }}<template v-if="d.attempts > 1"> · {{ d.attempts }} 次</template></span>
          <span v-if="d.status === 'failed' && d.last_error" class="small err reason">{{ d.last_error }}</span>
        </li>
      </ul>
      <p class="muted small">投递记录保留 30 天；失败后自动重试 3 次（2 秒 / 8 秒 / 30 秒）。</p>
    </section>
  </div>
</template>

<style scoped>
.intro { margin: 0 0 var(--space-3); }
.actions { display: flex; gap: var(--space-2); flex-wrap: wrap; margin-bottom: var(--space-3); }
.editor { padding: var(--space-4); margin-bottom: var(--space-3); }
.editor h3 { margin: 0 0 var(--space-3); }
.fields { display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: var(--space-3); margin-bottom: var(--space-3); }
.fields label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.fields .wide { grid-column: 1 / -1; }
.fields .check { flex-direction: row; align-items: center; gap: var(--space-2); }
.check input { width: auto; }
.err { color: var(--bad); }
.ok { color: var(--ok); }
.empty { margin: var(--space-3) 0; }
.list { list-style: none; margin: 0; padding: 0; }
.list li { padding: var(--space-3) var(--space-4); }
.list li + li { border-top: 1px solid var(--border); }
.ch { display: flex; gap: var(--space-3); align-items: center; flex-wrap: wrap; }
.ch-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.ch-name { font-weight: var(--weight-strong); display: flex; gap: var(--space-2); align-items: center; }
.type { font-size: var(--font-xs); font-weight: var(--weight-regular); padding: 0 var(--space-2); border-radius: var(--radius-full);
  background: color-mix(in srgb, var(--accent) 12%, transparent); color: var(--accent); }
.off .ch-name, .off .ch-main .muted { opacity: .6; }
.ch-actions { display: flex; gap: var(--space-2); align-items: center; }
.ellipsis { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
.danger-text { color: var(--bad); }
.deliveries li { display: grid; grid-template-columns: 150px 64px minmax(0, 1fr) 120px 110px; gap: var(--space-3); align-items: baseline; }
.deliveries .reason { grid-column: 3 / -1; }
.status { white-space: nowrap; }
.sent .status { color: var(--ok); }
.failed .status { color: var(--bad); }
.retrying .status { color: var(--warn); }
.kind { color: var(--text-muted); }
@media (max-width: 700px) {
  .deliveries li { grid-template-columns: auto minmax(0, 1fr) auto; }
  .deliveries .title { grid-column: 1 / -1; grid-row: 2; }
  .deliveries .kind, .deliveries li > .muted { display: none; }
  .deliveries .reason { grid-column: 1 / -1; }
}
</style>
