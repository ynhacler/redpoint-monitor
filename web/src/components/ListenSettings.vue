<script setup lang="ts">
// 监听端口（设计 51，修订第 200 条）：修改 Web 端口与 Agent 专用端口，保存后立即生效，不重启面板。
// Web 端口修改后旧端口继续监听，确认新地址可以访问后再关闭；Agent 端口只放行 Agent 接口，安装命令改用 Agent 地址。
// 反向代理模式下两个地址都只能是本机地址，对外的 HTTPS 由反向代理（如 Caddy）负责，所以要同时修改反向代理的配置。
import { computed, onMounted, reactive, ref } from 'vue'
import { ApiError, closeOldListen, errorText, getListenSetting, putListenSetting, type ListenSetting } from '../api'
import { t } from '../i18n'

const cur = ref<ListenSetting | null>(null)
const form = reactive({ web: '', agent: '', agent_url: '' })
const fieldErrors = ref<Record<string, string>>({})
const error = ref('')
const saving = ref(false)
const saved = ref(false)

function fill(v: ListenSetting) {
  cur.value = v
  Object.assign(form, { web: v.web, agent: v.agent, agent_url: v.agent_url })
}
onMounted(async () => {
  try {
    fill(await getListenSetting())
  } catch (e) {
    error.value = errorText(e, t('加载失败'))
  }
})

const proxy = computed(() => cur.value?.mode === 'proxy')
const port = (addr: string) => addr.slice(addr.lastIndexOf(':') + 1)
const dirty = computed(() => !!cur.value && (form.web !== cur.value.web || form.agent !== cur.value.agent || form.agent_url !== cur.value.agent_url))

async function save() {
  saving.value = true
  saved.value = false
  error.value = ''
  fieldErrors.value = {}
  try {
    fill(await putListenSetting({ ...form }))
    saved.value = true
  } catch (e) {
    if (e instanceof ApiError && e.details.length) fieldErrors.value = Object.fromEntries(e.details.map((d) => [d.field, d.message]))
    error.value = errorText(e, t('保存失败'))
  } finally {
    saving.value = false
  }
}
async function closeOld() {
  saving.value = true
  try {
    fill(await closeOldListen())
  } catch (e) {
    error.value = errorText(e, t('操作失败'))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="panel listen">
    <template v-if="cur">
      <p class="small muted intro">
        <template v-if="proxy">{{ t('面板在反向代理后面（只监听本机地址），对外的 HTTPS 由反向代理负责：修改端口后，要同时修改反向代理转发的端口。') }}</template>
        <template v-else>{{ t('面板使用内置 HTTPS，端口直接对外：修改后记得在防火墙中放行新端口。') }}</template>
        {{ t('保存后立即生效，不需要重启面板。') }}
      </p>

      <!-- 修改 Web 端口后：旧端口仍在监听，确认新地址可以访问后关闭 -->
      <div v-if="cur.old_web" class="old banner small">
        {{ t('旧地址 {addr} 仍在监听。', { addr: cur.old_web }) }}<template v-if="proxy">{{ t('把反向代理改为转发到 {addr}，', { addr: cur.web }) }}</template>
        {{ t('确认新地址可以打开面板后，再关闭旧端口（面板重启后也不再监听旧端口）。') }}
        <button type="button" class="secondary small" :disabled="saving" @click="closeOld">{{ t('关闭旧端口 {port}', { port: port(cur.old_web) }) }}</button>
      </div>

      <form class="grid" novalidate @submit.prevent="save">
        <label>{{ t('Web 端口') }}
          <input v-model="form.web" class="mono" autocomplete="off" spellcheck="false" :placeholder="cur.web_default" />
          <small v-if="fieldErrors.web" class="bad">{{ fieldErrors.web }}</small>
          <small v-else class="muted">{{ t('启动参数：{addr}；可只写端口', { addr: cur.web_default }) }}</small>
        </label>
        <label>{{ t('Agent 专用端口') }}
          <input v-model="form.agent" class="mono" autocomplete="off" spellcheck="false" :placeholder="t('留空：Agent 走 Web 端口')" />
          <small v-if="fieldErrors.agent" class="bad">{{ fieldErrors.agent }}</small>
          <small v-else class="muted">{{ t('只接收 Agent 上报；Web 端口仍接收已安装的 Agent') }}</small>
        </label>
        <label v-if="form.agent.trim()" class="wide">{{ t('Agent 连接地址') }}
          <input v-model="form.agent_url" class="mono" autocomplete="off" spellcheck="false"
            :placeholder="proxy ? 'https://agent.example.com' : t('留空：https://面板域名:Agent 端口')" />
          <small v-if="fieldErrors.agent_url" class="bad">{{ fieldErrors.agent_url }}</small>
          <small v-else class="muted">{{ t('写进新节点的安装命令。') }}<template v-if="proxy">{{ t('填反向代理转发到 Agent 端口的 https 地址。') }}</template></small>
        </label>
        <div class="wide actions">
          <button type="submit" :disabled="saving || !dirty">{{ saving ? t('保存中…') : t('保存并生效') }}</button>
          <span v-if="saved && !dirty" class="small ok">{{ t('已生效') }}</span>
          <span v-if="error" class="small bad">{{ error }}</span>
        </div>
      </form>

      <!-- 反向代理示例：Agent 端口单独用一个域名时 -->
      <details v-if="proxy && form.agent.trim()" class="small caddy">
        <summary>{{ t('Caddy 配置示例') }}</summary>
        <pre class="mono">{{ (form.agent_url ? form.agent_url.replace(/^https:\/\//, '').replace(/\/$/, '') : 'agent.example.com') }} {
  reverse_proxy {{ form.agent.includes(':') ? form.agent : `127.0.0.1:${form.agent}` }}
}</pre>
      </details>

      <p class="small muted note">
        {{ t('已安装的节点保存的是安装时的地址，会继续通过 Web 端口上报；要改用 Agent 端口，在节点上重新安装或运行') }}
        <span class="mono">sudo vpsmon-agent re-enroll --server {{ t('新地址') }} --enroll {{ t('注册码') }}</span>。
        {{ t('改错连不上时，在面板主机上运行') }} <span class="mono">vpsmon-server admin reset-listen --data {{ t('数据目录') }}</span> {{ t('并重启面板。') }}
      </p>
    </template>
    <p v-else-if="error" class="small bad">{{ error }}</p>
    <p v-else class="small muted">{{ t('加载中…') }}</p>
  </section>
</template>

<style scoped>
.listen { padding: var(--space-4) var(--space-5); display: flex; flex-direction: column; gap: var(--space-3); }
.intro, .note { margin: 0; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: var(--space-3); }
.grid label { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--font-sm); }
.grid .wide { grid-column: 1 / -1; }
.actions { display: flex; align-items: center; gap: var(--space-3); }
.mono { font-family: var(--font-mono); }
.old { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); margin: 0; }
.old button { margin-left: auto; }
.ok { color: var(--ok); }
.bad { color: var(--bad); }
.caddy pre { margin: var(--space-2) 0 0; padding: var(--space-3); background: var(--surface-2); border-radius: var(--radius-sm); overflow-x: auto; }
</style>
