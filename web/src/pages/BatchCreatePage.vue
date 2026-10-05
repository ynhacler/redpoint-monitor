<script setup lang="ts">
// 批量新建节点（设计 27.9）：粘贴或导入 CSV → 预览 → 全部校验通过才创建 → 每台主机独立的注册码与安装命令，
// 并导出命令文本、CSV、Ansible inventory 与 playbook。
// 【安全】导出含一次性注册码明文，只在本页显示这一次；文件在浏览器本地生成下载，不经过任何第三方。
import { computed, ref } from 'vue'
import { ApiError, createServersBatch, errorText, type CreateServerInput, type EnrollCodeView } from '../api'
import CommandBlock from '../components/CommandBlock.vue'
import Icon from '../components/Icon.vue'
import { parseCSV } from '../csv'
import { fmtDateTime } from '../format'
import { refresh } from '../store'

// 列顺序与设计 27.9 一致
const columns = ['名称', '主机名', 'IPv4', 'IPv6', '分组', '月流量 GB', '重置日', '到期日'] as const
const fieldColumn: Record<string, number> = {
  name: 0, expected_hostname: 1, expected_ipv4: 2, expected_ipv6: 3, group: 4, traffic_limit_gb: 5, traffic_reset_day: 6, expire_date: 7,
}

const text = ref('')
const ttl = ref<'1h' | '24h' | '7d'>('24h')

interface Row { cells: string[]; input: CreateServerInput; errors: string[] }

const rows = computed<Row[]>(() => {
  let data = parseCSV(text.value)
  if (data.length && /^(名称|name)$/i.test(data[0][0])) data = data.slice(1) // 表头
  return data.map((c) => {
    const cells = columns.map((_, i) => c[i] ?? '')
    const errors: string[] = []
    const num = (v: string, label: string, int = false) => {
      if (v === '') return undefined
      const n = Number(v)
      if (!Number.isFinite(n) || (int && !Number.isInteger(n))) {
        errors.push(`${label}应为${int ? '整数' : '数字'}`)
        return undefined
      }
      return n
    }
    const input: CreateServerInput = {
      name: cells[0],
      expected_hostname: cells[1] || undefined,
      expected_ipv4: cells[2] || undefined,
      expected_ipv6: cells[3] || undefined,
      group: cells[4] || undefined,
      traffic_limit_gb: num(cells[5], '月流量'),
      traffic_reset_day: num(cells[6], '重置日', true),
      expire_date: cells[7] || undefined,
    }
    if (!cells[0]) errors.push('请填写名称')
    return { cells, input, errors }
  })
})

const serverErrors = ref<Record<number, string[]>>({})
const formError = ref('')
const saving = ref(false)
const result = ref<{ items: EnrollCodeView[]; exports: { commands: string; csv: string; ansible: string | null } } | null>(null)

const localErrors = computed(() => rows.value.filter((r) => r.errors.length).length)

async function submit() {
  serverErrors.value = {}
  formError.value = ''
  if (!rows.value.length) {
    formError.value = '请先粘贴或导入节点列表'
    return
  }
  if (localErrors.value) return
  saving.value = true
  try {
    result.value = await createServersBatch(rows.value.map((r) => r.input), ttl.value)
    await refresh()
  } catch (e) {
    if (e instanceof ApiError && e.details.length) {
      const m: Record<number, string[]> = {}
      for (const d of e.details) {
        const hit = /^items\[(\d+)\]\.(.+)$/.exec(d.field)
        if (!hit) {
          formError.value = d.message
          continue
        }
        const col = fieldColumn[hit[2]]
        ;(m[Number(hit[1])] ??= []).push(col != null ? `${columns[col]}：${d.message}` : d.message)
      }
      serverErrors.value = m
      formError.value ||= '有些行需要修改，没有创建任何节点'
    } else {
      formError.value = errorText(e)
    }
  } finally {
    saving.value = false
  }
}

async function importFile(ev: Event) {
  const f = (ev.target as HTMLInputElement).files?.[0]
  if (f) text.value = await f.text()
}

/** 在浏览器本地生成文件并下载 */
function download(name: string, content: string, type: string) {
  const url = URL.createObjectURL(new Blob([content], { type }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

const sample = '名称,主机名,IPv4,IPv6,分组,月流量 GB,重置日,到期日\nhk-1,,103.1.2.3,,香港,1000,1,2027-01-01\njp-1,jp1.example.com,,,日本,,,'
</script>

<template>
  <main class="page">
    <div class="nav-row">
      <RouterLink to="/" class="back muted small"><Icon name="arrow-left" :size="14" />仪表板</RouterLink>
    </div>
    <div class="page-head">
      <h1>批量新建节点</h1>
    </div>

    <template v-if="!result">
      <p class="muted small intro">
        每行一个节点，列依次为：名称、主机名、IPv4、IPv6、分组、月流量 GB、重置日、到期日（YYYY-MM-DD），除名称外都可留空；
        第一行可以是表头。全部校验通过才会创建，每个节点生成自己的一次性注册码。
      </p>
      <section class="panel">
        <textarea v-model="text" rows="8" spellcheck="false" class="mono csv" :placeholder="sample" />
        <div class="tools small">
          <label class="file">
            <input type="file" accept=".csv,text/csv,text/plain" @change="importFile" />导入 CSV 文件
          </label>
          <button type="button" class="text" @click="text = sample">填入示例</button>
          <label>注册码有效期
            <select v-model="ttl">
              <option value="1h">1 小时</option>
              <option value="24h">24 小时</option>
              <option value="7d">7 天</option>
            </select>
          </label>
        </div>
      </section>

      <section v-if="rows.length" class="panel preview">
        <div class="table-wrap">
          <table class="small">
            <thead>
              <tr><th>#</th><th v-for="c in columns" :key="c">{{ c }}</th></tr>
            </thead>
            <tbody>
              <template v-for="(r, i) in rows" :key="i">
                <tr :class="{ bad: r.errors.length || serverErrors[i] }">
                  <td class="muted">{{ i + 1 }}</td>
                  <td v-for="(c, j) in r.cells" :key="j">{{ c || '—' }}</td>
                </tr>
                <tr v-if="r.errors.length || serverErrors[i]" class="err-row">
                  <td />
                  <td :colspan="columns.length" class="err">{{ [...r.errors, ...(serverErrors[i] ?? [])].join('；') }}</td>
                </tr>
              </template>
            </tbody>
          </table>
        </div>
        <p v-if="formError" class="err small">{{ formError }}</p>
        <div class="actions">
          <button type="button" :disabled="saving || localErrors > 0" @click="submit">
            {{ saving ? '创建中…' : `创建 ${rows.length} 个节点` }}
          </button>
          <RouterLink to="/" class="btn secondary">取消</RouterLink>
        </div>
      </section>
    </template>

    <template v-else>
      <div class="banner warn small">
        已创建 {{ result.items.length }} 个节点。注册码只显示这一次，请立即下载或复制；
        有效期至 {{ fmtDateTime(result.items[0]?.enroll_expires_at) }}，每个注册码只能使用一次。
      </div>
      <section class="panel">
        <h2>导出</h2>
        <div class="exports">
          <button type="button" class="secondary" @click="download('vpsmon-install-commands.txt', result.exports.commands, 'text/plain')">
            安装命令（.txt）
          </button>
          <button type="button" class="secondary" @click="download('vpsmon-nodes.csv', result.exports.csv, 'text/csv')">CSV</button>
          <button v-if="result.exports.ansible" type="button" class="secondary"
            @click="download('vpsmon-ansible.yml', result.exports.ansible, 'text/yaml')">Ansible（inventory + playbook）</button>
        </div>
        <p v-if="!result.exports.ansible" class="muted small">
          面板还没有已验签的官方版本，安装命令为手动方式，暂不提供 Ansible 导出。
        </p>
        <p v-else class="muted small">
          Ansible 文件中先是 inventory，后是 playbook（先下载、按已验签的哈希校验、再执行），请拆成两个文件使用。
        </p>
      </section>
      <section class="panel">
        <h2>每台主机的安装命令</h2>
        <div v-for="it in result.items" :key="it.server_id" class="node">
          <div class="node-head">
            <RouterLink :to="`/servers/${it.server_id}/install`"><strong>{{ it.server_name }}</strong></RouterLink>
            <span class="muted small mono">{{ it.enroll_code }}</span>
          </div>
          <CommandBlock :command="it.install.command" />
        </div>
      </section>
      <div class="actions">
        <RouterLink to="/" class="btn">返回仪表板</RouterLink>
      </div>
    </template>
  </main>
</template>

<style scoped>
.intro { margin: 0 0 var(--space-4); max-width: 80ch; }
.panel + .panel, .banner + .panel { margin-top: var(--space-4); }
.panel h2 { font-size: var(--font-lg); margin-bottom: var(--space-3); }
.csv { width: 100%; font-size: var(--font-sm); resize: vertical; }
.mono { font-family: var(--font-mono); }
.tools { display: flex; flex-wrap: wrap; gap: var(--space-3) var(--space-4); align-items: center; margin-top: var(--space-3); }
.tools label { display: inline-flex; align-items: center; gap: var(--space-2); }
.tools select { width: auto; }
.file { cursor: pointer; color: var(--accent); }
.file input { display: none; }
.table-wrap { overflow-x: auto; }
table { width: 100%; border-collapse: collapse; white-space: nowrap; }
th, td { text-align: left; padding: var(--space-1) var(--space-2); border-bottom: 1px solid var(--border); }
th { color: var(--text-muted); font-weight: var(--weight-regular); }
tr.bad td { color: var(--bad); }
.err-row td { border-bottom: 1px solid var(--border); white-space: normal; }
.err { color: var(--bad); }
.actions { display: flex; gap: var(--space-2); margin-top: var(--space-4); }
.exports { display: flex; flex-wrap: wrap; gap: var(--space-2); margin-bottom: var(--space-2); }
.node + .node { margin-top: var(--space-4); }
.node-head { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-3); margin-bottom: var(--space-2); }
</style>
