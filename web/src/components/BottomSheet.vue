<script setup lang="ts">
// 底部抽屉（手机 UI 规范 3.3）：手机上从底部弹出，宽屏上为居中的对话框。
// 关闭方式：点遮罩、Esc、标题栏右侧的“完成”、在顶部把手处向下拖动，以及系统返回（打开时在历史中加一项 #sheet，
// 返回即关闭，不会离开页面）。内容区可滚动，最高约 85% 可视高度（dvh：Safari 地址栏伸缩时不被遮挡），并避开底部安全区。
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { t } from '../i18n'

const props = defineProps<{
  /** 是否打开 */
  open: boolean
  /** 标题 */
  title: string
}>()
const emit = defineEmits<{ close: [] }>()

const route = useRoute()
const router = useRouter()
const panel = ref<HTMLElement | null>(null)
const dragY = ref(0)
let startY: number | null = null
let pushed = false

function close() {
  emit('close')
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') close()
}

watch(() => props.open, async (open) => {
  if (open) {
    dragY.value = 0
    document.addEventListener('keydown', onKey)
    document.documentElement.classList.add('sheet-open')
    if (route.hash !== '#sheet') {
      pushed = true
      await router.push({ query: route.query, hash: '#sheet' })
    }
    await nextTick()
    panel.value?.focus()
  } else {
    document.removeEventListener('keydown', onKey)
    document.documentElement.classList.remove('sheet-open')
    if (pushed && route.hash === '#sheet') router.back()
    pushed = false
  }
}, { immediate: true })
// 系统返回：#sheet 离开历史时关闭
watch(() => route.hash, (h) => {
  if (props.open && pushed && h !== '#sheet') {
    pushed = false
    close()
  }
})
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKey)
  document.documentElement.classList.remove('sheet-open')
})

// 在把手 / 标题栏向下拖动关闭
function onDown(e: PointerEvent) {
  startY = e.clientY
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}
function onMove(e: PointerEvent) {
  if (startY != null) dragY.value = Math.max(0, e.clientY - startY)
}
function onUp() {
  if (startY == null) return
  startY = null
  if (dragY.value > 80) close()
  dragY.value = 0
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="sheet-backdrop" @click.self="close">
      <div ref="panel" class="sheet" role="dialog" aria-modal="true" :aria-label="title" tabindex="-1"
        :style="dragY ? { transform: `translateY(${dragY}px)`, transition: 'none' } : undefined">
        <div class="sheet-head" @pointerdown="onDown" @pointermove="onMove" @pointerup="onUp" @pointercancel="onUp">
          <span class="handle" aria-hidden="true" />
          <strong>{{ title }}</strong>
          <button type="button" class="btn text done" @pointerdown.stop @click="close">{{ t('完成') }}</button>
        </div>
        <div class="sheet-body">
          <slot />
        </div>
        <div v-if="$slots.footer" class="sheet-foot">
          <slot name="footer" />
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.sheet-backdrop { position: fixed; inset: 0; z-index: 100; background: color-mix(in srgb, var(--scrim) 50%, transparent);
  display: flex; align-items: flex-end; justify-content: center; }
.sheet { width: 100%; max-height: min(85dvh, 720px); display: flex; flex-direction: column; background: var(--surface);
  border-radius: var(--radius-md) var(--radius-md) 0 0; box-shadow: 0 -4px 24px color-mix(in srgb, var(--scrim) 20%, transparent);
  padding-bottom: env(safe-area-inset-bottom); outline: none; transition: transform .15s; }
.sheet-head { position: relative; display: flex; align-items: center; justify-content: center; min-height: 52px;
  padding: var(--space-3) var(--space-4) var(--space-2); border-bottom: 1px solid var(--border); touch-action: none; cursor: grab; }
.handle { position: absolute; top: 6px; left: 50%; width: 36px; height: 4px; margin-left: -18px; border-radius: var(--radius-full); background: var(--border); }
.done { position: absolute; right: var(--space-2); min-height: 44px; }
.sheet-body { overflow-y: auto; overscroll-behavior: contain; padding: var(--space-2) var(--space-4) var(--space-4); }
.sheet-foot { display: flex; gap: var(--space-2); padding: var(--space-3) var(--space-4); border-top: 1px solid var(--border); }
/* 宽屏：居中对话框 */
@media (min-width: 601px) {
  .sheet-backdrop { align-items: center; }
  .sheet { width: min(440px, calc(100vw - 32px)); border-radius: var(--radius-md); padding-bottom: 0; }
  .handle { display: none; }
  .sheet-head { cursor: default; }
}
</style>
