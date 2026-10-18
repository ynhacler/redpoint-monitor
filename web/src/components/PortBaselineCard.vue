<script setup lang="ts">
// 端口基线（修订第 170 条）：只给管理员。指定允许对公网开放的监听端口；不在基线中的为“未知监听端口”，
// 基线中没有在监听的为“基线端口未监听”，各有一条告警。只比较对公网开放的端口（与“监听端口”的暴露范围一致），
// 这是本机看到的监听，是否真能从公网访问还取决于防火墙与安全组。
import { computed, ref, watch } from 'vue'
import { ApiError, errorText, putPortBaseline, type ServerView } from '../api'
import { fmtTime } from '../format'
import { exposure, serviceName } from '../ports'

const props = defineProps<{ server: ServerView }>()
const baseline = ref(props.server.port_baseline ?? null)

const keyOf = (p: { port: number; proto: string }) => `${p.port}/${p.proto}`
// 当前对公网开放的监听端口（按端口排序）
const exposed = computed(() => (props.server.latest?.ports ?? []).filter((p) => exposure(p) === 'public')
  .map((p) => ({ key: keyOf(p), service: serviceName(p) }))
  .sort((a, b) => parseInt(a.key) - parseInt(b.key) || a.key.localeCompare(b.key)))
const allowed = computed(() => new Set(baseline.value?.ports ?? []))
const unknown = computed(() => exposed.value.filter((p) => !allowed.value.has(p.key)))
const gone = computed(() => (baseline.value?.ports ?? []).filter((k) => !exposed.value.some((p) => p.key === k)))

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
    error.value = e instanceof ApiError && e.details.length ? e.details[0].message : errorText(e, '保存失败')
  } finally {
    saving.value = false
  }
}

const saveDraft = () => save(draft.value.split(/[\s,，、]+/).filter(Boolean))
const useCurrent = () => save(exposed.value.map((p) => p.key))
</script>

<template>
  <section class="panel baseline">
    <div class="head">
      <h3>端口基线</h3>
      <span class="muted small">仅管理员可见</span>
      <button v-if="!editing" type="button" class="secondary small edit" @click="edit">{{ baseline ? '编辑' : '设置' }}</button>
    </div>
    <p class="muted small note">
      允许对公网开放的监听端口。出现不在基线中的端口（如有人启动了 8080、3306）、或基线端口不再监听时告警。
      只看本机的监听，是否真能从公网访问还取决于防火墙与安全组。
    </p>

    <form v-if="editing" class="form" novalidate @submit.prevent="saveDraft">
      <input v-model="draft" placeholder="22/tcp, 443/tcp, 51820/udp" aria-label="允许的端口" spellcheck="false" />
      <small class="muted">端口/协议，逗号或空格分隔；只写端口时为 tcp</small>
      <p v-if="error" class="small bad">{{ error }}</p>
      <div class="actions">
        <button type="submit" :disabled="saving">保存</button>
        <button type="button" class="secondary" :disabled="saving || !exposed.length" @click="useCurrent">以当前为准</button>
        <button v-if="baseline" type="button" class="text danger-text" :disabled="saving" @click="save([])">取消基线</button>
        <button type="button" class="text" @click="editing = false">取消</button>
      </div>
    </form>

    <template v-else>
      <p v-if="!baseline" class="small">
        还没有设置，不检查新增端口。
        <button type="button" class="text small" :disabled="saving || !exposed.length" @click="useCurrent">以当前对公网开放的 {{ exposed.length }} 个端口为基线</button>
      </p>
      <p v-else class="muted small">{{ baseline.ports.length }} 个允许的端口 · {{ fmtTime(baseline.updated_at) }} 设置</p>
      <p v-if="error" class="small bad">{{ error }}</p>
      <ul class="ports">
        <li v-for="p in exposed" :key="p.key" :class="baseline ? (allowed.has(p.key) ? 'ok' : 'unknown') : ''">
          <span class="num">{{ p.key }}</span><span v-if="p.service" class="muted small">{{ p.service }}</span>
          <span v-if="baseline && !allowed.has(p.key)" class="tag warn">不在基线中</span>
        </li>
        <li v-for="k in gone" :key="'g' + k" class="gone">
          <span class="num">{{ k }}</span><span class="tag bad">未监听</span>
        </li>
      </ul>
      <p v-if="!exposed.length && !gone.length" class="muted small">没有对公网开放的监听端口。</p>
    </template>
  </section>
</template>

<style scoped>
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
.bad, .danger-text { color: var(--bad); }
</style>
