<script setup lang="ts">
// 节点关联的云实例（设计 44.5）：云厂商口径的状态、规格、流量包与到期时间；到期时间可一键写入节点。
// 没有关联时不显示。
import { computed, ref, watch } from 'vue'
import { applyCloudExpire, errorText, listCloudInstances, type CloudInstance, type ServerView } from '../api'
import { fmtBytes, fmtDate, fmtTime } from '../format'
import { refresh } from '../store'
import UsageBar from './UsageBar.vue'

const props = defineProps<{ server: ServerView }>()
const insts = ref<CloudInstance[]>([])
const msg = ref('')

async function load() {
  try {
    insts.value = await listCloudInstances({ server_id: props.server.id })
  } catch {
    insts.value = [] // 云账户是可选功能：读取失败时不显示，不打扰节点详情
  }
}
watch(() => props.server.id, load, { immediate: true })

const providerNames: Record<string, string> = {
  aws: 'AWS', aliyun_cn: '阿里云', aliyun_intl: '阿里云国际', tencent_cn: '腾讯云', tencent_intl: '腾讯云国际', oci: 'Oracle Cloud',
}
const kindNames: Record<string, string> = { ec2: 'EC2', lightsail: 'Lightsail', ecs: 'ECS', swas: '轻量', cvm: 'CVM', lighthouse: '轻量',
  oci: 'OCI', oci_egress: '租户' }

// 按日历日计算剩余天数，与节点详情“到期”一栏一致
function daysLeft(i: CloudInstance): number {
  const day = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
  return Math.round((day(new Date(i.expire_at * 1000)) - day(new Date())) / 86400_000)
}
// 节点到期日与实例不同（或未填）时提示写入
const canApply = (i: CloudInstance) => i.expire_at > 0 && props.server.expire_date !== fmtDate(i.expire_at)
const busy = ref(false)
async function apply(i: CloudInstance) {
  busy.value = true
  msg.value = ''
  try {
    const r = await applyCloudExpire(i.id)
    msg.value = `已把节点到期日设为 ${r.expire_date}`
    await refresh()
  } catch (e) {
    msg.value = errorText(e)
  } finally {
    busy.value = false
  }
}
const pct = (i: CloudInstance) => (i.traffic_limit_bytes ? (i.traffic_used_bytes / i.traffic_limit_bytes) * 100 : 0)
const updated = computed(() => Math.max(0, ...insts.value.map((i) => i.updated_at)))
</script>

<template>
  <section v-if="insts.length" class="panel cloud">
    <div class="head">
      <h3>云厂商</h3>
      <span class="small muted">同步于 {{ fmtTime(updated) }}</span>
    </div>
    <div v-for="i in insts" :key="i.id" class="inst">
      <div class="row">
        <span><b>{{ providerNames[i.provider] ?? i.provider }}</b> · {{ i.account_name }}</span>
        <span class="small" :class="i.state.toLowerCase() === 'running' ? 'ok' : 'muted'">{{ i.state }}</span>
      </div>
      <div class="small muted">{{ kindNames[i.kind] ?? i.kind }} · {{ i.region }} · {{ i.plan || '—' }} · {{ i.instance_id }}</div>
      <div v-if="i.expire_at" class="row small">
        <span :class="daysLeft(i) < 0 ? 'bad' : daysLeft(i) < 30 ? 'warn' : ''">
          到期 {{ fmtDate(i.expire_at) }}（{{ daysLeft(i) >= 0 ? `${daysLeft(i)} 天后` : `已过期 ${-daysLeft(i)} 天` }}）
        </span>
        <button v-if="canApply(i)" type="button" class="secondary small" :disabled="busy" @click="apply(i)">写入节点到期日</button>
      </div>
      <div v-if="i.traffic_limit_bytes" class="traffic">
        <div class="row small"><span>流量包（云厂商口径）</span>
          <span class="num">{{ fmtBytes(i.traffic_used_bytes) }} / {{ fmtBytes(i.traffic_limit_bytes) }}</span></div>
        <UsageBar :pct="pct(i)" />
      </div>
    </div>
    <p v-if="msg" class="small muted">{{ msg }}</p>
  </section>
</template>

<style scoped>
.cloud { display: flex; flex-direction: column; gap: var(--space-3); }
.head { display: flex; align-items: baseline; justify-content: space-between; gap: var(--space-2); }
.head h3 { margin: 0; }
.inst { display: flex; flex-direction: column; gap: var(--space-1); }
.inst + .inst { border-top: 1px solid var(--border); padding-top: var(--space-3); }
.row { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); flex-wrap: wrap; }
.traffic { display: flex; flex-direction: column; gap: var(--space-1); margin-top: var(--space-1); }
p { margin: 0; }
</style>
