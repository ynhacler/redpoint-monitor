<script setup lang="ts">
// 国旗（设计 1.2.3）：按节点的国家 / 地区代码显示，悬停显示中文名。没有国旗时显示代码。
import { computed } from 'vue'
import { countryName, flagUrl } from '../countries'

const props = defineProps<{
  /** ISO 3166-1 两位代码；为空时不显示 */
  code: string
}>()
const url = computed(() => (props.code ? flagUrl(props.code) : undefined))
const name = computed(() => countryName(props.code))
</script>

<template>
  <img v-if="url" class="flag" :src="url" :alt="name" :title="name" loading="lazy" />
  <span v-else-if="code" class="flag code" :title="name">{{ code }}</span>
</template>

<style scoped>
/* 4:3 比例，高度随文字；细边框让白底国旗在浅色背景上也有轮廓 */
.flag { height: 0.9em; width: 1.2em; border-radius: 2px; box-shadow: 0 0 0 1px var(--border); vertical-align: -0.05em; flex: none; object-fit: cover; }
.code { display: inline-flex; align-items: center; justify-content: center; font-size: var(--font-xs); color: var(--text-muted); box-shadow: none; width: auto; }
</style>
