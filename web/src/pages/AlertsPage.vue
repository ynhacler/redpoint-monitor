<script setup lang="ts">
// 告警页（设计 16）：“告警”列出全部节点的活动与历史告警；“规则”编辑默认规则与分组 / 节点覆盖（设计 16.2）。
// 标签页与筛选写在地址中（?tab=rules、?state=all），刷新与分享后保持。
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ApiError, deleteAlertRule, listAlertRules, listAlerts, UnauthorizedError, updateAlertRule,
  type AlertEvent, type AlertRule,
} from '../api'
import { ruleNames, ruleSummary, severityNames } from '../alertRules'
import AlertRuleEditor from '../components/AlertRuleEditor.vue'
import EmptyState from '../components/EmptyState.vue'
import { fmtDateTime, fmtDuration } from '../format'
import { installed, logout } from '../store'

const route = useRoute()
const router = useRouter()
const tab = computed(() => (route.query.tab === 'rules' ? 'rules' : 'alerts'))
const stateFilter = computed(() => (route.query.state === 'all' ? 'all' : 'active'))
const setQuery = (q: Record<string, string | undefined>) => router.replace({ query: { ...route.query, ...q } })
function fail(e: unknown, set: (m: string) => void) {
  if (e instanceof UnauthorizedError) logout()
  else set(e instanceof ApiError ? e.message : '加载失败，请稍后重试')
}

// ---- 告警 ----
const events = ref<AlertEvent[]>([])
const cursor = ref('')
const loadingEvents = ref(false)
const eventsError = ref('')
async function loadEvents(more = false) {
  loadingEvents.value = true
  try {
    const p = await listAlerts({ state: stateFilter.value, cursor: more ? cursor.value : undefined, limit: 50 })
    events.value = more ? [...events.value, ...p.items] : p.items
    cursor.value = p.next_cursor
    eventsError.value = ''
  } catch (e) {
    fail(e, (m) => (eventsError.value = m))
  } finally {
    loadingEvents.value = false
  }
}
const duration = (e: AlertEvent) => fmtDuration((e.resolved_at || Date.now() / 1000) - e.fired_at)

// ---- 规则 ----
const rules = ref<AlertRule[]>([])
const rulesError = ref('')
const editing = ref<number | null>(null) // 正在编辑的规则 ID
async function loadRules() {
  try {
    rules.value = await listAlertRules()
    rulesError.value = ''
  } catch (e) {
    fail(e, (m) => (rulesError.value = m))
  }
}
const globals = computed(() => rules.value.filter((r) => r.scope_type === 'global'))
const overrides = computed(() => rules.value.filter((r) => r.scope_type !== 'global'))
const serverName = (id: string) => installed.value.find((s) => String(s.id) === id)?.name ?? `#${id}（已删除）`
const scopeLabel = (r: AlertRule) => (r.scope_type === 'group' ? `分组 ${r.scope_id}` : `节点 ${serverName(r.scope_id)}`)

async function toggle(r: AlertRule) {
  try {
    Object.assign(r, await updateAlertRule(r.id, { enabled: !r.enabled }))
  } catch (e) {
    fail(e, (m) => (rulesError.value = m))
  }
}
async function remove(r: AlertRule) {
  if (!confirm(`删除“${scopeLabel(r)} · ${ruleNames[r.rule_key] ?? r.rule_key}”的覆盖？删除后回到上一层的设置。`)) return
  try {
    await deleteAlertRule(r.id)
    await loadRules()
  } catch (e) {
    fail(e, (m) => (rulesError.value = m))
  }
}
function saved() {
  editing.value = null
  adding.value = false
  loadRules()
}

// 新增覆盖：选择规则与对象，以全局规则为基础编辑
const adding = ref(false)
const add = ref({ ruleKey: 'cpu', scopeType: 'group' as 'group' | 'server', scopeId: '' })
const groups = computed(() => [...new Set(installed.value.map((s) => s.group).filter(Boolean))].sort())
const addBase = computed(() => globals.value.find((r) => r.rule_key === add.value.ruleKey))
const addTaken = computed(() =>
  overrides.value.some((r) => r.rule_key === add.value.ruleKey && r.scope_type === add.value.scopeType && r.scope_id === add.value.scopeId),
)

watch([tab, stateFilter], () => (tab.value === 'rules' ? loadRules() : loadEvents()), { immediate: true })
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>告警</h1>
      <div class="segmented" role="tablist">
        <button type="button" role="tab" :class="{ active: tab === 'alerts' }" @click="setQuery({ tab: undefined })">告警</button>
        <button type="button" role="tab" :class="{ active: tab === 'rules' }" @click="setQuery({ tab: 'rules' })">规则</button>
      </div>
    </div>

    <!-- 告警：全部节点 -->
    <template v-if="tab === 'alerts'">
      <div class="bar">
        <div class="segmented">
          <button type="button" :class="{ active: stateFilter === 'active' }" @click="setQuery({ state: undefined })">进行中</button>
          <button type="button" :class="{ active: stateFilter === 'all' }" @click="setQuery({ state: 'all' })">全部</button>
        </div>
        <span class="muted small">告警事件保留 180 天；通知（Telegram / Webhook）随后提供</span>
      </div>
      <p v-if="eventsError" class="banner">{{ eventsError }}</p>
      <EmptyState v-else-if="!loadingEvents && !events.length" :text="stateFilter === 'active' ? '没有进行中的告警，全部节点运行正常。' : '还没有告警。'" />
      <ul v-if="events.length" class="panel list">
        <li v-for="e in events" :key="e.id" :class="[e.severity, e.state]">
          <span class="sev">{{ severityNames[e.severity] }}</span>
          <div class="main">
            <RouterLink :to="`/servers/${e.server_id}`" class="node">{{ e.server_name || `#${e.server_id}` }}</RouterLink>
            <span class="msg">{{ e.message }}</span>
          </div>
          <span class="when muted small num">
            {{ fmtDateTime(e.fired_at) }} ·
            <b v-if="e.state === 'firing'" class="ongoing">进行中 {{ duration(e) }}</b>
            <template v-else>持续 {{ duration(e) }} 后恢复</template>
          </span>
        </li>
      </ul>
      <div v-if="cursor" class="more"><button type="button" class="secondary" :disabled="loadingEvents" @click="loadEvents(true)">加载更多</button></div>
    </template>

    <!-- 规则 -->
    <template v-else>
      <p class="muted small intro">
        规则分三层：默认规则 → 分组覆盖 → 节点覆盖，下层覆盖上层（设计 16.2）。修改后下一轮评估（10 秒内）生效。
      </p>
      <p v-if="rulesError" class="banner">{{ rulesError }}</p>

      <section class="section">
        <h3>默认规则</h3>
        <ul class="panel rules">
          <li v-for="r in globals" :key="r.id" :class="{ off: !r.enabled }">
            <div class="rule">
              <label class="switch" :title="r.enabled ? '点击关闭' : '点击启用'">
                <input type="checkbox" :checked="r.enabled" @change="toggle(r)" />
              </label>
              <div class="rule-main">
                <div class="rule-name">{{ ruleNames[r.rule_key] ?? r.rule_key }} <span class="sev small" :class="r.severity">{{ severityNames[r.severity] }}</span></div>
                <div class="muted small">{{ ruleSummary(r) }}</div>
              </div>
              <button type="button" class="text" @click="editing = editing === r.id ? null : r.id">{{ editing === r.id ? '收起' : '编辑' }}</button>
            </div>
            <AlertRuleEditor v-if="editing === r.id" :rule="r" @saved="saved" @cancel="editing = null" @unauthorized="logout" />
          </li>
        </ul>
      </section>

      <section class="section">
        <div class="section-head">
          <h3>分组与节点覆盖</h3>
          <button v-if="!adding" type="button" class="secondary" @click="adding = true">添加覆盖</button>
        </div>
        <div v-if="adding" class="panel add">
          <div class="add-fields">
            <label>规则
              <select v-model="add.ruleKey">
                <option v-for="g in globals" :key="g.rule_key" :value="g.rule_key">{{ ruleNames[g.rule_key] ?? g.rule_key }}</option>
              </select>
            </label>
            <label>对象
              <select v-model="add.scopeType" @change="add.scopeId = ''">
                <option value="group">分组</option>
                <option value="server">节点</option>
              </select>
            </label>
            <label>{{ add.scopeType === 'group' ? '分组' : '节点' }}
              <select v-model="add.scopeId">
                <option value="" disabled>请选择</option>
                <template v-if="add.scopeType === 'group'">
                  <option v-for="g in groups" :key="g" :value="g">{{ g }}</option>
                </template>
                <template v-else>
                  <option v-for="s in installed" :key="s.id" :value="String(s.id)">{{ s.name }}</option>
                </template>
              </select>
              <small v-if="add.scopeType === 'group' && !groups.length" class="muted">还没有分组：在节点信息中填写分组</small>
            </label>
          </div>
          <p v-if="addTaken" class="small warn-text">该对象已有这条规则的覆盖，请在下方列表中直接修改。</p>
          <AlertRuleEditor v-if="addBase && add.scopeId && !addTaken" :key="`${add.ruleKey}-${add.scopeType}-${add.scopeId}`"
            :rule="addBase" :scope="{ type: add.scopeType, id: add.scopeId }" @saved="saved" @cancel="adding = false" @unauthorized="logout" />
          <div v-else class="add-cancel"><button type="button" class="secondary" @click="adding = false">取消</button></div>
        </div>
        <p v-if="!overrides.length && !adding" class="muted small">还没有覆盖。例如：为“落地”分组把流量阈值改为 70%，或为某台机器关闭 CPU 告警。</p>
        <ul v-if="overrides.length" class="panel rules">
          <li v-for="r in overrides" :key="r.id" :class="{ off: !r.enabled }">
            <div class="rule">
              <label class="switch"><input type="checkbox" :checked="r.enabled" @change="toggle(r)" /></label>
              <div class="rule-main">
                <div class="rule-name">
                  <span class="scope">{{ scopeLabel(r) }}</span> · {{ ruleNames[r.rule_key] ?? r.rule_key }}
                  <span class="sev small" :class="r.severity">{{ severityNames[r.severity] }}</span>
                </div>
                <div class="muted small">{{ r.enabled ? ruleSummary(r) : '已关闭（覆盖上一层）' }}</div>
              </div>
              <button type="button" class="text" @click="editing = editing === r.id ? null : r.id">{{ editing === r.id ? '收起' : '编辑' }}</button>
              <button type="button" class="text danger-text" @click="remove(r)">删除</button>
            </div>
            <AlertRuleEditor v-if="editing === r.id" :rule="r" @saved="saved" @cancel="editing = null" @unauthorized="logout" />
          </li>
        </ul>
      </section>
    </template>
  </main>
</template>

<style scoped>
.page-head .segmented { margin-left: auto; }
.bar { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; margin-bottom: var(--space-3); }
.list, .rules { list-style: none; margin: 0; padding: 0; }
.list li { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; gap: var(--space-3); align-items: baseline; padding: var(--space-3) var(--space-4); }
.list li + li, .rules li + li { border-top: 1px solid var(--border); }
.main { min-width: 0; display: flex; gap: var(--space-2); flex-wrap: wrap; align-items: baseline; }
.node { font-weight: var(--weight-strong); }
.resolved .msg, .resolved .node { color: var(--text-muted); }
.sev { font-size: var(--font-xs); line-height: var(--line-xs); padding: 1px var(--space-2); border-radius: var(--radius-full);
  border: 1px solid currentColor; color: var(--text-muted); white-space: nowrap; font-weight: var(--weight-regular); }
.critical .sev, .sev.critical { color: var(--bad); }
.warning .sev, .sev.warning { color: var(--warn); }
.resolved .sev { opacity: .6; }
.ongoing { color: var(--bad); }
.warning .ongoing { color: var(--warn); }
.info .ongoing { color: var(--text); }
.more { display: flex; justify-content: center; margin-top: var(--space-4); }
.intro { margin: 0 0 var(--space-2); }
.rules li { padding: var(--space-3) var(--space-4); }
.rule { display: flex; align-items: center; gap: var(--space-3); }
.rule-main { flex: 1; min-width: 0; }
.rule-name { font-weight: var(--weight-strong); display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; }
.scope { color: var(--accent); }
.off .rule-name, .off .rule-main .muted { opacity: .55; }
.switch input { width: 18px; height: 18px; margin: 0; }
.section-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: var(--space-3); gap: var(--space-3); }
.add { padding: var(--space-4); margin-bottom: var(--space-3); }
.add-fields { display: grid; grid-template-columns: repeat(auto-fill, minmax(170px, 1fr)); gap: var(--space-3); }
.add-fields label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.add-cancel { margin-top: var(--space-3); }
.warn-text { color: var(--warn); }
.danger-text { color: var(--bad); }
@media (max-width: 600px) {
  .list li { grid-template-columns: auto minmax(0, 1fr); }
  .when { grid-column: 2; }
}
</style>
