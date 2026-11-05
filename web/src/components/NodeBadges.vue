<script setup lang="ts">
// 节点标志（修订第 279 条）：IP 协议栈 v4 / v6（Agent 上报）与用户标注的网络线路（CN2 GIA、CMIN2 等）。
// 仪表板的节点卡片与表格共用；没有任何标志时不占位。
import { computed } from 'vue'
import type { ServerView } from '../api'
import { t } from '../i18n'
import { isPremiumLine, stackBadges } from '../lines'

const props = defineProps<{
  /** 节点 */
  server: ServerView
}>()

const stack = computed(() => stackBadges(props.server))
const lines = computed(() => props.server.line_tags ?? [])
</script>

<template>
  <span v-if="stack.length || lines.length" class="badges">
    <span v-for="b in stack" :key="b.text" class="badge ip" :title="t(b.title)">{{ b.text }}</span>
    <span v-for="l in lines" :key="l" class="badge line" :class="{ premium: isPremiumLine(l) }" :title="t('网络线路')">{{ l }}</span>
  </span>
</template>

<style scoped>
.badges { display: inline-flex; flex-wrap: wrap; align-items: center; gap: var(--space-1); min-width: 0; }
.badge { flex: none; font-size: var(--font-xs); line-height: 1rem; padding: 0 5px; border-radius: var(--radius-sm);
  border: 1px solid var(--border); color: var(--text-muted); white-space: nowrap; font-weight: var(--weight-strong); }
.ip { font-family: var(--font-mono); }
.line.premium { color: var(--accent); border-color: var(--accent); }
</style>
