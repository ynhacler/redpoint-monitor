<script setup lang="ts">
// 防火墙（修订第 168 条）：只给管理员。Agent 不以 root 运行，读不到内核中的实时规则：规则来自可读的配置文件，
// 不一定等于实际生效的规则；配置文件只有 root 可读时只显示状态，并提示在主机上查看的命令。
// 修订第 264 条：fail2ban 的 jail 显示为“已配置”与进程状态，不再写“封禁 sshd”；封禁 IP 数未知（需要 root）；内核模块已加载不代表规则生效。
import { computed } from 'vue'
import type { ServerView } from '../api'
import { t } from '../i18n'

const props = defineProps<{ server: ServerView }>()
const fw = computed(() => props.server.latest?.firewall)

const toolNames: Record<string, string> = { ufw: 'ufw', firewalld: 'firewalld', nftables: 'nftables', iptables: 'iptables', fail2ban: 'fail2ban', csf: 'CSF' }
const actionNames: Record<string, string> = { allow: '放行', deny: '拒绝', reject: '拒绝', limit: '限速', ban: '封禁' }
// 规则不可读时，在主机上查看实时规则的命令
const showCmd: Record<string, string> = {
  ufw: 'sudo ufw status verbose', firewalld: 'sudo firewall-cmd --list-all', nftables: 'sudo nft list ruleset',
  iptables: 'sudo iptables -S INPUT', fail2ban: 'sudo fail2ban-client status', csf: 'sudo csf -l',
}
// 正在运行的防火墙中，入站默认允许的（风险提示）
const allowAll = computed(() => fw.value?.tools.some((x) => x.active && x.policy === 'allow' && x.name !== 'fail2ban') ?? false)
const anyActive = computed(() => fw.value?.tools.some((x) => x.active && x.name !== 'fail2ban') ?? false)

// 状态文字：ufw 看配置是否启用，nftables / iptables 看内核模块是否加载，其余看进程
function activeText(name: string, active: boolean) {
  if (name === 'ufw') return active ? t('已启用') : t('未启用')
  if (name === 'nftables' || name === 'iptables') return active ? t('内核模块已加载') : t('模块未加载')
  return active ? t('运行中') : t('未运行')
}

function target(r: { proto?: string; port?: string; service?: string }) {
  if (r.service) return r.service
  if (!r.port) return r.proto ? t('全部 {proto}', { proto: r.proto.toUpperCase() }) : t('全部端口')
  return `${r.port}${r.proto ? '/' + r.proto : ''}`
}
</script>

<template>
  <section v-if="fw" class="panel firewall">
    <div class="head">
      <h3>{{ t('防火墙') }}</h3>
      <span class="muted small">{{ t('仅管理员可见') }}</span>
    </div>
    <p v-if="!fw.tools.length" class="banner warn">{{ t('未检测到防火墙（ufw、firewalld、nftables、iptables、fail2ban、CSF）。云厂商的安全组不在这里显示。') }}</p>
    <template v-else>
      <p v-if="!anyActive" class="banner warn">{{ t('检测到防火墙但都没有启用。') }}</p>
      <p v-else-if="allowAll" class="banner warn">{{ t('有防火墙的入站默认策略为“允许”，未列出的端口都可以访问。') }}</p>
      <div v-for="tool in fw.tools" :key="tool.name" class="tool">
        <div class="tool-head">
          <strong>{{ toolNames[tool.name] ?? tool.name }}</strong>
          <span class="tag" :class="tool.active ? 'on' : 'off'">{{ activeText(tool.name, tool.active) }}</span>
          <span v-if="tool.policy" class="tag" :class="tool.policy === 'deny' ? 'on' : 'warn'">{{ tool.policy === 'deny' ? t('入站默认拒绝') : t('入站默认允许') }}</span>
          <span v-if="tool.active && (tool.name === 'nftables' || tool.name === 'iptables')" class="muted small">{{ t('仅说明组件存在，不代表有规则生效') }}</span>
          <span v-if="tool.source" class="muted small source">{{ tool.source }}</span>
        </div>
        <p v-if="!tool.rules_readable" class="muted small note">
          {{ t('规则配置只有 root 可读，在主机上查看：') }}<code>{{ showCmd[tool.name] }}</code>
        </p>
        <p v-else-if="!tool.rules?.length" class="muted small note">{{ tool.name === 'fail2ban' ? t('配置中没有启用的 jail。') : t('配置中没有按端口的规则。') }}</p>
        <!-- fail2ban（修订第 264 条，第二轮 UI2-003）：配置中启用的 jail ≠ 正在封禁；Agent 不是 root，读不到运行中的 jail 与封禁列表 -->
        <template v-else-if="tool.name === 'fail2ban'">
          <ul class="rules">
            <li v-for="(r, i) in tool.rules" :key="i">
              <span class="num jail">{{ r.service || target(r) }}</span>
              <span class="muted small">{{ tool.active ? t('已配置 · fail2ban 运行中') : t('已配置 · fail2ban 未运行，不在防护') }}</span>
            </li>
          </ul>
          <p class="muted small note">{{ t('当前封禁 IP：未知（读取运行状态需要 root，在主机上查看：') }}<code>sudo fail2ban-client status {{ tool.rules?.[0]?.service || '' }}</code>）</p>
        </template>
        <ul v-else class="rules">
          <li v-for="(r, i) in tool.rules" :key="i">
            <span class="act" :class="r.action">{{ actionNames[r.action] ? t(actionNames[r.action]) : r.action }}</span>
            <span class="num">{{ target(r) }}</span>
            <span v-if="r.from" class="muted small">{{ t('来自 {from}', { from: r.from }) }}</span>
          </li>
        </ul>
      </div>
      <p class="muted small note">{{ t('规则来自配置文件，不一定等于实际生效的规则；Agent 不以 root 运行，读不到内核中的实时规则。') }}</p>
    </template>
  </section>
</template>

<style scoped>
.jail { font-weight: var(--weight-strong); }
.firewall { padding: var(--space-4) var(--space-5); }
.head { display: flex; align-items: baseline; gap: var(--space-2); }
.head h3 { margin: 0; }
.banner { margin: var(--space-3) 0 0; }
.tool { margin-top: var(--space-3); padding-top: var(--space-3); border-top: 1px solid var(--border); }
.tool-head { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; }
.source { overflow-wrap: anywhere; }
.tag { font-size: var(--font-xs); padding: 1px var(--space-2); border-radius: var(--radius-full); }
.tag.on { background: color-mix(in srgb, var(--ok) 12%, transparent); color: var(--ok); }
.tag.off { background: var(--surface-2); color: var(--text-muted); }
.tag.warn { background: color-mix(in srgb, var(--warn) 14%, transparent); color: var(--warn); }
.note { margin: var(--space-2) 0 0; }
.note code { overflow-wrap: anywhere; }
.rules { list-style: none; margin: var(--space-2) 0 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: var(--space-1) var(--space-4); }
.rules li { display: flex; align-items: baseline; gap: var(--space-2); min-width: 0; flex-wrap: wrap; font-size: var(--font-sm); }
.act { font-size: var(--font-xs); padding: 0 var(--space-2); border-radius: var(--radius-full); border: 1px solid currentColor; color: var(--ok); }
.act.deny, .act.reject, .act.ban { color: var(--bad); }
.act.limit { color: var(--warn); }
</style>
