<script setup lang="ts">
// 端口基线（修订第 170 条）：只给管理员。指定允许对公网开放的监听端口；不在基线中的为“未知监听端口”，
// 基线中没有在监听的为“基线端口未监听”，各有一条告警。只比较对公网开放的端口（与“监听端口”的暴露范围一致），
// 这是本机看到的监听，是否真能从公网访问还取决于防火墙与安全组：“从面板检测公网可达”逐个 TCP 连接确认（修订第 171 条），
// 手动触发——自动定时检测会在节点日志里留下大量连接记录，fail2ban 等可能封禁面板地址。
import { computed, ref, watch } from 'vue'
import { ApiError, checkExposure, errorText, putPortBaseline, type ServerView } from '../api'
import { fmtTime } from '../format'
import { t } from '../i18n'
import { exposure, reachNames, serviceName } from '../ports'

const props = defineProps<{ server: ServerView }>()
const baseline = ref(props.server.port_baseline ?? null)

const keyOf = (p: { port: number; proto: string }) => `${p.port}/${p.proto}`
// 当前对外监听的端口（按端口排序）
const exposed = computed(() => (props.server.latest?.ports ?? []).filter((p) => exposure(p) === 'public')
  .map((p) => ({ key: keyOf(p), service: serviceName(p) }))
  .sort((a, b) => parseInt(a.key) - parseInt(b.key) || a.key.localeCompare(b.key)))
const allowed = computed(() => new Set(baseline.value?.ports ?? []))
const unknown = computed(() => exposed.value.filter((p) => !allowed.value.has(p.key)))
const gone = computed(() => (baseline.value?.ports ?? []).filter((k) => !exposed.value.some((p) => p.key === k)))

// 最近一次公网可达检测（修订第 171 条）
const exposureRes = ref(props.server.port_exposure ?? null)
const checking = ref(false)
const reach = computed(() => new Map((exposureRes.value?.results ?? []).map((r) => [`${r.port}/${r.proto}`, r])))
const stateNames = reachNames

async function runCheck() {
  checking.value = true
  error.value = ''
  try {
    exposureRes.value = await checkExposure(props.server.id)
  } catch (e) {
    error.value = e instanceof ApiError && e.details.length ? e.details[0].message : errorText(e, t('检测失败'))
  } finally {
    checking.value = false
  }
}

const editing = ref(false)
const draft = ref('')
const saving = ref(false)
const error = ref('')
// 面板返回的基线变化时同步（正在编辑时不覆盖）
watch(() => props.server.port_baseline, (v) => { if (!editing.value) baseline.value = v ?? null })

function edit() {
  draft.value = (baseline.value?.ports ?? exposed.value.map((p) => p.key)).join(', ')
  editing.value = true
  error.value = ''
}

async function save(ports: string[]) {
  saving.value = true
  error.value = ''
  try {
    const v = await putPortBaseline(props.server.id, ports)
    baseline.value = v.ports.length ? v : null
    editing.value = false
  } catch (e) {
    error.value = e instanceof ApiError && e.details.length ? e.details[0].message : errorText(e, t('保存失败'))
  } finally {
    saving.value = false
  }
}

const saveDraft = () => save(draft.value.split(/[\s,，、]+/).filter(Boolean))

// 端口备注（修订第 258 条）：基线中的端口可加一句备注（如“Xray 入口”），点“备注”就地编辑，回车或失焦保存
const labelOf = (k: string) => baseline.value?.labels?.[k] ?? ''
const labelEditing = ref('')
const labelDraft = ref('')
function editLabel(k: string) {
  labelEditing.value = k
  labelDraft.value = labelOf(k)
}
async function saveLabel() {
  const k = labelEditing.value
  if (!k || !baseline.value) return
  labelEditing.value = ''
  if (labelDraft.value.trim() === labelOf(k)) return
  saving.value = true
  error.value = ''
  try {
    baseline.value = await putPortBaseline(props.server.id, baseline.value.ports, { [k]: labelDraft.value.trim() })
  } catch (e) {
    error.value = e instanceof ApiError && e.details.length ? e.details[0].message : errorText(e, t('保存失败'))
  } finally {
    saving.value = false
  }
}
// 设为当前基线须确认（修订第 263 条，第二轮 UI2-002）：先列出将要保存的端口与比较规则，确认后才保存
const confirming = ref<string[] | null>(null)
const confirmClear = ref(false)
const useCurrent = () => { confirming.value = exposed.value.map((p) => p.key) }
async function confirmSave() {
  const ports = confirming.value
  if (!ports) return
  await save(ports)
  if (!error.value) confirming.value = null
}
async function clearBaseline() {
  await save([])
  confirmClear.value = false
}
// 编辑表单中的“以当前为准”只填入输入框，由“保存”提交
const fillCurrent = () => { draft.value = exposed.value.map((p) => p.key).join(', ') }
const changes = computed(() => (baseline.value ? unknown.value.length + gone.value.length : 0))
</script>

<template>
  <section class="panel baseline">
    <div class="head">
      <h3>{{ t('端口基线') }}</h3>
      <span class="muted small">{{ t('仅管理员可见') }}</span>
      <button v-if="!editing" type="button" class="secondary small edit" @click="edit">{{ baseline ? t('编辑') : t('设置') }}</button>
    </div>
    <p class="muted small note">
      {{ t('允许对外监听的端口。出现不在基线中的端口（如有人启动了 8080、3306）、或基线端口不再监听时告警。') }}
      {{ t('只看本机的监听，是否真能从公网访问还取决于防火墙与安全组。') }}
    </p>

    <form v-if="editing" class="form" novalidate @submit.prevent="saveDraft">
      <input v-model="draft" placeholder="22/tcp, 443/tcp, 51820/udp" :aria-label="t('允许的端口')" spellcheck="false" />
      <small class="muted">{{ t('端口/协议，逗号或空格分隔；只写端口时为 tcp') }}</small>
      <p v-if="error" class="small bad">{{ error }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving">{{ t('保存') }}</button>
        <button type="button" class="secondary" :disabled="saving || !exposed.length" @click="fillCurrent">{{ t('以当前为准') }}</button>
        <button v-if="baseline && !confirmClear" type="button" class="text danger-text" :disabled="saving" @click="confirmClear = true">{{ t('取消基线') }}</button>
        <span v-if="confirmClear" class="small">{{ t('取消后不再检查新增与停止监听的端口。') }}
          <button type="button" class="text danger-text" :disabled="saving" @click="clearBaseline">{{ t('确认取消') }}</button>
          <button type="button" class="text" @click="confirmClear = false">{{ t('不取消') }}</button></span>
        <button type="button" class="text" @click="editing = false">{{ t('取消') }}</button>
      </div>
    </form>

    <template v-else>
      <!-- 状态（修订第 263 条）：未启用 / 已启用（共 N 项、更新时间、变化数） -->
      <div v-if="!baseline" class="state-line small">
        <span class="state off">{{ t('端口基线：未启用') }}</span>
        <span class="muted">{{ t('当前发现 {n} 个对外监听端口', { n: exposed.length }) }}</span>
        <button v-if="!confirming" type="button" class="small" :disabled="saving || !exposed.length" @click="useCurrent">{{ t('设为当前基线（{n}）', { n: exposed.length }) }}</button>
      </div>
      <div v-else class="state-line small">
        <span class="state on">{{ t('基线已启用') }}</span>
        <span class="muted">{{ t('共 {n} 项 · 更新于 {time}', { n: baseline.ports.length, time: fmtTime(baseline.updated_at) }) }}</span>
        <span v-if="changes" class="warn">{{ t('{n} 项变化', { n: changes }) }}</span>
      </div>
      <div v-if="confirming" class="confirm-box small" role="dialog" :aria-label="t('确认端口基线')">
        <p>{{ t('将以下 {n} 个端口设为基线：', { n: confirming.length }) }}<span class="num">{{ confirming.join(t('、')) }}</span></p>
        <p class="muted">{{ t('只比较监听在全部地址或公网地址的端口，仅本机 / 内网监听的不算；保存后从下一次端口刷新（约 1 分钟内）开始比较，新增或停止监听的端口会告警。') }}</p>
        <div class="actions">
          <button type="button" :disabled="saving" @click="confirmSave">{{ saving ? t('保存中…') : t('确认设为基线') }}</button>
          <button type="button" class="secondary" :disabled="saving" @click="confirming = null">{{ t('取消') }}</button>
        </div>
      </div>
      <p v-if="error" class="small bad">{{ error }}</p>
      <ul class="ports">
        <li v-for="p in exposed" :key="p.key" :class="baseline ? (allowed.has(p.key) ? 'ok' : 'unknown') : ''">
          <span class="num">{{ p.key }}</span><span v-if="p.service" class="muted small">{{ p.service }}</span>
          <span v-if="baseline && !allowed.has(p.key)" class="tag warn">{{ t('不在基线中') }}</span>
          <span v-if="reach.get(p.key)" class="tag" :class="'r-' + reach.get(p.key)!.state">{{ t(stateNames[reach.get(p.key)!.state] ?? '') || reach.get(p.key)!.state }}</span>
          <template v-if="baseline && allowed.has(p.key)">
            <input v-if="labelEditing === p.key" v-model="labelDraft" class="label-input" maxlength="40" :placeholder="t('如 Xray 入口')" :aria-label="t('{port} 的备注', { port: p.key })"
              autofocus @keydown.enter.prevent="saveLabel" @keydown.esc="labelEditing = ''" @blur="saveLabel" />
            <template v-else>
              <span v-if="labelOf(p.key)" class="label-text">{{ labelOf(p.key) }}</span>
              <button type="button" class="text small label-btn" :disabled="saving" @click="editLabel(p.key)">{{ labelOf(p.key) ? t('改备注') : t('备注') }}</button>
            </template>
          </template>
        </li>
        <li v-for="k in gone" :key="'g' + k" class="gone">
          <span class="num">{{ k }}</span><span v-if="labelOf(k)" class="label-text">{{ labelOf(k) }}</span><span class="tag bad">{{ t('未监听') }}</span>
        </li>
      </ul>
      <p v-if="!exposed.length && !gone.length" class="muted small">{{ t('没有对外监听的端口。') }}</p>
      <div v-if="exposed.length" class="check">
        <button type="button" class="secondary small" :disabled="checking" @click="runCheck">{{ checking ? t('检测中…') : t('从面板检测公网可达') }}</button>
        <span v-if="exposureRes" class="muted small">{{ t('{time} 检测 {target}', { time: fmtTime(exposureRes.checked_at), target: exposureRes.target }) }}</span>
      </div>
      <p v-if="exposureRes" class="muted small note">
        {{ t('逐个 TCP 连接节点的公网地址（连上即断开）。“超时”多为防火墙或安全组拦截；结果反映的是从面板所在网络能否访问。') }}
      </p>
    </template>
  </section>
</template>

<style scoped>
.state-line { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; margin: var(--space-2) 0; }
.state { padding: 1px 8px; border-radius: var(--radius-full); }
.state.off { background: var(--surface-2); color: var(--text-muted); }
.state.on { background: color-mix(in srgb, var(--ok) 12%, transparent); color: var(--ok); }
.state-line .warn { color: var(--warn); }
.confirm-box { border: 1px solid var(--border); border-radius: var(--radius-sm); padding: var(--space-3); margin: var(--space-2) 0; background: var(--surface-2); }
.confirm-box p { margin: 0 0 var(--space-2); overflow-wrap: anywhere; }
.label-text { font-size: var(--font-sm); }
.label-btn { padding: 0 var(--space-1); margin-left: auto; }
.label-input { flex: 1; min-width: 8em; max-width: 16em; padding: 2px var(--space-2); font-size: var(--font-sm); }
.baseline { padding: var(--space-4) var(--space-5); }
.head { display: flex; align-items: baseline; gap: var(--space-2); }
.head h3 { margin: 0; }
.edit { margin-left: auto; }
.note { margin: var(--space-1) 0 var(--space-3); }
.form { display: flex; flex-direction: column; gap: var(--space-2); }
.actions { display: flex; gap: var(--space-2); flex-wrap: wrap; align-items: center; }
.ports { list-style: none; margin: var(--space-3) 0 0; padding: 0; display: flex; flex-wrap: wrap; gap: var(--space-2); }
.ports li { display: inline-flex; align-items: baseline; gap: var(--space-1); padding: 2px var(--space-2); border-radius: var(--radius-sm);
  border: 1px solid var(--border); font-size: var(--font-sm); }
.ports li.ok { border-color: color-mix(in srgb, var(--ok) 40%, var(--border)); }
.ports li.unknown { border-color: var(--warn); }
.ports li.gone { border-color: var(--bad); border-style: dashed; }
.tag { font-size: var(--font-xs); padding: 0 var(--space-1); border-radius: var(--radius-full); }
.tag.warn { color: var(--warn); }
.tag.bad { color: var(--bad); }
.tag.r-open { color: var(--ok); }
.tag.r-refused, .tag.r-timeout, .tag.r-skipped, .tag.r-error { color: var(--text-muted); }
.check { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; margin-top: var(--space-3); }
.bad, .danger-text { color: var(--bad); }
</style>
