<script setup lang="ts">
// 公开展示页的订阅标签（设计 45.3）：只读，没有任何修改入口。数据来自 GET /api/v1/public/subscriptions，
// 只有名称、分类、金额、周期与下次扣费日；上传的图标不公开，这里只显示内置标志或首字母。
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { errorText, getPublicSubscriptions, type PublicSubscription } from '../api'
import { daysUntil, fmtMoney } from '../format'
import { t } from '../i18n'
import { categoryNames, cycleNames } from '../subscriptions'
import SubAvatar from './SubAvatar.vue'

const items = ref<PublicSubscription[]>([])
const loaded = ref(false)
const error = ref('')

async function load() {
  try {
    items.value = (await getPublicSubscriptions()).items
    error.value = ''
  } catch (e) {
    error.value = errorText(e, t('订阅加载失败'))
  } finally {
    loaded.value = true
  }
}

const soon = computed(() => items.value.filter((s) => (daysUntil(s.next_charge) ?? 99) <= 7).length)
function due(s: PublicSubscription): string {
  const d = daysUntil(s.next_charge)
  if (d == null) return ''
  return d <= 0 ? t('今天') : t('{n} 天后', { n: d })
}

let timer: ReturnType<typeof setInterval>
onMounted(() => {
  load()
  timer = setInterval(load, 60000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <section :aria-label="t('订阅')">
    <p v-if="error" class="bad">{{ error }}</p>
    <p v-if="loaded && !items.length && !error" class="muted empty">{{ t('还没有订阅') }}</p>
    <template v-if="items.length">
      <p class="sum num">
        <b>{{ t('{n} 个订阅', { n: items.length }) }}</b>
        <span :class="{ warn: soon }">{{ t('7 天内扣费') }} {{ soon }}</span>
      </p>
      <ul class="subs">
        <li v-for="(s, i) in items" :key="i">
          <SubAvatar :brand="s.brand" :name="s.name" :size="36" />
          <div class="main">
            <strong :title="s.name">{{ s.name }}</strong>
            <span class="muted small">{{ t(categoryNames[s.category]) }} · {{ t(cycleNames[s.cycle]) }}</span>
          </div>
          <div class="right num">
            <strong>{{ fmtMoney(s.price_cents, s.currency) }}</strong>
            <span v-if="s.next_charge" class="small" :class="(daysUntil(s.next_charge) ?? 99) <= 7 ? 'warn' : 'muted'">{{ s.next_charge }} · {{ due(s) }}</span>
          </div>
        </li>
      </ul>
    </template>
  </section>
</template>

<style scoped>
.sum { display: flex; gap: var(--space-4); align-items: baseline; margin: 0 0 var(--space-3); }
.sum b { font-size: var(--font-xl); }
.subs { list-style: none; margin: 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: var(--space-3); }
.subs li { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-3) var(--space-4); background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md); min-width: 0; }
.main { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.main strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.right { flex: none; display: flex; flex-direction: column; align-items: flex-end; white-space: nowrap; }
.empty { text-align: center; }
.muted { color: var(--text-muted); }
.warn { color: var(--warn); }
.bad { color: var(--bad); }
@media (max-width: 600px) { .subs { grid-template-columns: minmax(0, 1fr); } }
</style>
