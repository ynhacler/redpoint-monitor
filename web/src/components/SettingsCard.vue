<script setup lang="ts">
// 设置卡片（修订第 207 条）：账号等设置页共用的外框，与“网站配置”同一版式——
// 头部为图标、标题、状态标签与一行说明，右侧放操作按钮；正文由调用方放入，列表行之间用分隔线。
import { Comment, Fragment, useSlots, type VNode } from 'vue'
import Icon, { type IconName } from './Icon.vue'

defineProps<{
  /** 标题 */
  title: string
  /** 标题下的一行说明（可选） */
  desc?: string
  /** 标题前的图标（可选） */
  icon?: IconName
}>()

// 正文只在插槽有实际内容时显示（v-if 都不成立时只渲染出注释节点），避免出现空的分隔区
const slots = useSlots()
const filled = (ns: VNode[]): boolean => ns.some((n) => n.type !== Comment && (n.type !== Fragment || filled(n.children as VNode[])))
const hasBody = () => filled(slots.default?.() ?? [])
</script>

<template>
  <section class="panel settings-card">
    <header class="sc-head">
      <span v-if="icon" class="sc-icon" aria-hidden="true"><Icon :name="icon" :size="18" /></span>
      <div class="sc-title">
        <h2>{{ title }}<slot name="badge" /></h2>
        <p v-if="desc || $slots.desc" class="small muted"><slot name="desc">{{ desc }}</slot></p>
      </div>
      <div v-if="$slots.actions" class="sc-actions"><slot name="actions" /></div>
    </header>
    <div v-if="hasBody()" class="sc-body"><slot /></div>
  </section>
</template>

<style scoped>
.settings-card { padding: 0; overflow: hidden; }
.sc-head { display: flex; align-items: flex-start; gap: var(--space-3); padding: var(--space-4) var(--space-5); }
.sc-icon { flex: none; width: 36px; height: 36px; border-radius: var(--radius-sm); display: grid; place-items: center;
  background: color-mix(in srgb, var(--accent) 10%, transparent); color: var(--accent); }
.sc-title { flex: 1; min-width: 0; }
.sc-title h2 { margin: 0; font-size: var(--font-md); line-height: var(--line-md); display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap; }
.sc-title p { margin: 2px 0 0; }
.sc-actions { flex: none; display: flex; gap: var(--space-2); align-items: center; }
.sc-body { border-top: 1px solid var(--border); padding: var(--space-4) var(--space-5); display: flex; flex-direction: column; gap: var(--space-3); }
.sc-body :deep(p) { margin: 0; }
/* 列表：行之间分隔线，贴满卡片宽度 */
.sc-body :deep(.sc-list) { list-style: none; margin: calc(-1 * var(--space-4)) calc(-1 * var(--space-5)); padding: 0; }
.sc-body :deep(.sc-list > li) { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-3) var(--space-5); }
.sc-body :deep(.sc-list > li + li) { border-top: 1px solid var(--border); }
.sc-body :deep(.sc-empty) { color: var(--text-muted); font-size: var(--font-sm); text-align: center; padding: var(--space-4);
  border: 1px dashed var(--border); border-radius: var(--radius-sm); }
@media (max-width: 600px) {
  .sc-head { padding: var(--space-3) var(--space-4); }
  .sc-body { padding: var(--space-3) var(--space-4); }
  .sc-body :deep(.sc-list) { margin: calc(-1 * var(--space-3)) calc(-1 * var(--space-4)); }
  .sc-body :deep(.sc-list > li) { padding: var(--space-3) var(--space-4); }
  .sc-icon { display: none; }
}
</style>
