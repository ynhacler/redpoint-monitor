<script setup lang="ts">
// 代理程序（修订第 169 条）：只给管理员。按进程名识别，协议、端口、传输方式与 TLS 类型来自可读的配置文件（不含任何密钥），
// 并核对端口是否在监听。曾经运行、现在没有运行的列在顶部，主动停用或卸载后可点“不再提醒”。
// 每个程序一个折叠分组，入站为四列紧凑行，正常监听不再用绿色标签（修订第 252 条）。
import { computed, ref } from 'vue'
import { errorText, forgetProxies, type ServerView } from '../api'
import { fmtTime } from '../format'
import { t } from '../i18n'

const props = defineProps<{ server: ServerView }>()
const proxies = computed(() => props.server.latest?.proxies ?? [])
const forgotten = ref(new Set<string>())
const missing = computed(() => (props.server.proxies_missing ?? []).filter((m) => !forgotten.value.has(m.name)))
const error = ref('')

const names: Record<string, string> = {
  xray: 'Xray', v2ray: 'V2Ray', 'sing-box': 'sing-box', hysteria: 'Hysteria', trojan: 'Trojan', 'trojan-go': 'Trojan-Go',
  shadowsocks: 'Shadowsocks', tuic: 'TUIC', gost: 'GOST', brook: 'Brook', frps: 'frps', openvpn: 'OpenVPN', wireguard: 'WireGuard', 'x-ui': 'x-ui 面板',
}
const protoNames: Record<string, string> = {
  vless: 'VLESS', vmess: 'VMess', trojan: 'Trojan', shadowsocks: 'Shadowsocks', ss: 'Shadowsocks', hysteria: 'Hysteria', hysteria2: 'Hysteria 2',
  tuic: 'TUIC', socks: 'SOCKS', http: 'HTTP', 'dokodemo-door': '任意门', wireguard: 'WireGuard', openvpn: 'OpenVPN', frp: 'frp', brook: 'Brook',
}

async function forget(name: string) {
  error.value = ''
  try {
    await forgetProxies(props.server.id, name)
    forgotten.value = new Set([...forgotten.value, name])
  } catch (e) {
    error.value = errorText(e, t('操作失败'))
  }
}

// 安全方式（修订第 266 条，第二轮 UI2-010）：紧凑行只为 TLS / Reality 显示徽章，缺省时留空，但提示中区分三种情况——
// 不适用（协议自带加密或不加密）、未配置（明确为 none）、无法识别（配置中没有这一项）
const noTlsProtocols = new Set(['shadowsocks', 'ss', 'socks', 'http', 'dokodemo-door', 'wireguard', 'openvpn', 'frp', 'brook'])
function secInfo(i: { protocol: string; security?: string }): { badge: string; text: string } {
  if (i.security && i.security !== 'none') {
    const b = i.security === 'reality' ? 'Reality' : i.security.toUpperCase()
    return { badge: b, text: t('安全：{s}', { s: b }) }
  }
  if (noTlsProtocols.has(i.protocol)) return { badge: '', text: t('安全：不适用（协议自带加密或不加密）') }
  if (i.security === 'none') return { badge: '', text: t('安全：未配置 TLS') }
  return { badge: '', text: t('安全：无法识别') }
}
// 复制配置路径（只复制路径，不含任何配置内容）
const copied = ref('')
async function copyPath(p: string) {
  try {
    await navigator.clipboard.writeText(p)
    copied.value = p
    setTimeout(() => copied.value === p && (copied.value = ''), 1500)
  } catch {
    /* 剪贴板不可用（非 HTTPS 等）时忽略 */
  }
}

// 对外的入站（非回环）中有没在监听的
const hasDown = (p: { running: boolean; inbounds?: { local?: boolean; listening: boolean }[] }) =>
  p.running && (p.inbounds ?? []).some((i) => !i.local && !i.listening)
</script>

<template>
  <section class="panel proxies">
    <div class="head">
      <h3>{{ t('代理') }}</h3>
      <span class="muted small">{{ t('仅管理员可见') }}</span>
    </div>
    <p class="muted small note top">ⓘ {{ t('按进程名识别，协议与端口来自可读的配置文件；不读取也不上传任何密钥、UUID 或密码。') }}</p>
    <p v-if="error" class="small bad">{{ error }}</p>
    <ul v-if="missing.length" class="missing">
      <li v-for="m in missing" :key="m.name">
        <span class="tag warn">{{ t('没有运行') }}</span>
        <strong>{{ names[m.name] ?? m.name }}</strong>
        <span class="muted small">{{ t('{time} 起', { time: fmtTime(m.since) }) }}</span>
        <button type="button" class="text small" :title="t('主动停用或卸载时点这里')" @click="forget(m.name)">{{ t('不再提醒') }}</button>
      </li>
    </ul>
    <!-- 每个程序一个折叠分组（修订第 252 条，UI-011）：标题只显示一次状态；有问题的默认展开，正常的收起 -->
    <details v-for="p in proxies" :key="p.name" class="proxy" :open="!p.running || hasDown(p) || proxies.length === 1">
      <summary class="proxy-head">
        <strong>{{ names[p.name] ? t(names[p.name]) : p.name }}</strong>
        <span class="tag" :class="p.running ? (hasDown(p) ? 'warn' : 'on') : 'off'">
          {{ p.running ? (hasDown(p) ? t('运行中 · 有端口没在监听') : (p.name === 'wireguard' ? t('{n} 个接口', { n: p.procs }) : t('运行中'))) : t('已安装 · 未运行') }}
        </span>
        <span v-if="p.inbounds?.length" class="muted small count">{{ t('{n} 个入站', { n: p.inbounds.length }) }}</span>
      </summary>
      <p v-if="p.config_no === 'unreadable'" class="muted small note">{{ t('配置只有 root 可读：只能确认是否在运行，看不到协议与端口。') }}</p>
      <p v-else-if="p.config_no === 'invalid'" class="muted small note">{{ t('配置格式无法识别。') }}</p>
      <p v-else-if="!p.inbounds?.length && p.name !== 'x-ui'" class="muted small note">{{ t('没有找到配置或入站。') }}</p>
      <!-- 入站：协议、端口 / 网络、传输与安全、监听状态四列；正常监听为中性文字，只有异常才用醒目标签 -->
      <ul v-if="p.inbounds?.length" class="inbounds">
        <li v-for="(i, k) in p.inbounds" :key="k">
          <span class="proto">{{ protoNames[i.protocol] ? t(protoNames[i.protocol]) : i.protocol }}</span>
          <span class="num port">{{ i.port }}/{{ i.network ?? 'tcp' }}</span>
          <span class="muted small tr" :title="secInfo(i).text">{{ i.transport || '' }}<span v-if="secInfo(i).badge" class="sec">{{ secInfo(i).badge }}</span><span class="sr-only">{{ secInfo(i).text }}</span></span>
          <span v-if="i.listening" class="small st">{{ i.local ? t('仅本机') : t('监听中') }}</span>
          <span v-else class="tag st" :class="i.local || !p.running ? 'off' : 'bad'">{{ t('未监听') }}</span>
        </li>
      </ul>
      <!-- 配置路径（修订第 266 条，UI2-015）：等宽内联代码，过长省略，悬停看全，可复制（只复制路径） -->
      <p v-if="p.config" class="muted small path-line">{{ t('配置：') }}<code class="path" :title="p.config">{{ p.config }}</code>
        <button type="button" class="text small" @click="copyPath(p.config)">{{ copied === p.config ? t('已复制') : t('复制') }}</button></p>
    </details>
  </section>
</template>

<style scoped>
.proxies { padding: var(--space-4) var(--space-5); }
.head { display: flex; align-items: baseline; gap: var(--space-2); }
.head h3 { margin: 0; }
.missing { list-style: none; margin: var(--space-3) 0 0; padding: var(--space-2) var(--space-3); border-radius: var(--radius-sm);
  background: color-mix(in srgb, var(--warn) 10%, transparent); display: flex; flex-direction: column; gap: var(--space-1); }
.missing li { display: flex; align-items: baseline; gap: var(--space-2); flex-wrap: wrap; }
.missing button { margin-left: auto; padding: 0; }
.proxy { margin-top: var(--space-3); padding-top: var(--space-3); border-top: 1px solid var(--border); }
.proxy-head { display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; cursor: pointer; list-style: none; min-height: 36px; }
.proxy-head::-webkit-details-marker { display: none; }
.proxy-head::before { content: '›'; color: var(--text-muted); transition: transform .15s; display: inline-block; width: 10px; }
.proxy[open] > .proxy-head::before { transform: rotate(90deg); }
.count { margin-left: auto; }
.note.top { margin: var(--space-1) 0 0; }
.path-line { display: flex; align-items: center; gap: var(--space-2); margin: var(--space-2) 0 0; min-width: 0; }
.path { font-family: var(--font-mono); background: var(--surface-2); padding: 1px 6px; border-radius: 4px; min-width: 0; overflow: hidden;
  text-overflow: ellipsis; white-space: nowrap; }
.sec { margin-left: var(--space-1); padding: 0 6px; border-radius: var(--radius-full); background: color-mix(in srgb, var(--accent) 10%, transparent);
  color: var(--text); font-size: var(--font-xs); }
.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
/* 入站行（UI2-009）：约 36px 行高、行间 1px 浅分隔线 */
.inbounds li + li { border-top: 1px solid var(--border); padding-top: var(--space-1); }
.inbounds li { min-height: 32px; }
.note { margin: var(--space-2) 0 0; }
.inbounds { list-style: none; margin: var(--space-2) 0 0; padding: 0; display: flex; flex-direction: column; gap: var(--space-1); }
.inbounds li { display: grid; grid-template-columns: minmax(5.5em, auto) auto minmax(0, 1fr) auto; align-items: baseline; gap: var(--space-3);
  font-size: var(--font-sm); }
.proto { font-weight: var(--weight-strong); }
.tr { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.st { justify-self: end; color: var(--text-muted); }
.tag { font-size: var(--font-xs); padding: 1px var(--space-2); border-radius: var(--radius-full); }
.tag.on { background: color-mix(in srgb, var(--ok) 12%, transparent); color: var(--ok); }
.tag.off { background: var(--surface-2); color: var(--text-muted); }
.tag.warn { background: color-mix(in srgb, var(--warn) 14%, transparent); color: var(--warn); }
.tag.bad { background: color-mix(in srgb, var(--bad) 12%, transparent); color: var(--bad); }
.bad { color: var(--bad); }
</style>
