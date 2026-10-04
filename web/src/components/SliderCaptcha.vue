<script setup lang="ts">
// 滑动拼图验证码（设计 17.4）：拖动滑块把拼图块移到缺口处。图片由面板生成，正确位置只在服务端，
// 这里只上报拼图块位置与拖动用时，由服务端判断是否通过。支持鼠标与触摸（Pointer Events）。
import { onMounted, ref } from 'vue'
import { ApiError, errorText, getCaptcha, type Captcha, type CaptchaAnswer } from '../api'

const emit = defineEmits<{
  /** 拖动完成，交给父组件随登录一起提交 */
  done: [answer: CaptchaAnswer]
  /** 面板未启用验证码（--no-login-captcha） */
  disabled: []
}>()

const cap = ref<Captcha>()
const x = ref(0) // 拼图块左边缘位置（图片像素）
const dragging = ref(false)
const finished = ref(false)
const error = ref('')
let startX = 0
let startAt = 0

/** 重新获取验证码（初次加载、登录失败后、用户点击刷新） */
async function reload() {
  x.value = 0
  finished.value = false
  error.value = ''
  try {
    cap.value = await getCaptcha()
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) emit('disabled')
    else error.value = errorText(e, '验证码加载失败')
  }
}
defineExpose({ reload })
onMounted(reload)

const maxX = () => (cap.value ? cap.value.width - cap.value.piece_size : 0)

function down(e: PointerEvent) {
  if (!cap.value || finished.value) return
  dragging.value = true
  startX = e.clientX - x.value
  startAt = performance.now()
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
}
function move(e: PointerEvent) {
  if (!dragging.value) return
  x.value = Math.min(maxX(), Math.max(0, e.clientX - startX))
}
function up() {
  if (!dragging.value || !cap.value) return
  dragging.value = false
  if (x.value < 2) return // 只是点击没有拖动
  finished.value = true
  emit('done', { id: cap.value.id, x: x.value, ms: Math.round(performance.now() - startAt) })
}
</script>

<template>
  <div class="captcha">
    <div v-if="cap" class="stage" :style="{ width: `${cap.width}px`, height: `${cap.height}px` }">
      <img :src="cap.background" alt="" draggable="false" />
      <img class="piece" :src="cap.piece" alt="" draggable="false"
        :style="{ left: `${x}px`, top: `${cap.piece_y}px`, width: `${cap.piece_size}px` }" />
      <button type="button" class="text refresh" title="换一张" @click="reload">↻</button>
    </div>
    <div v-else class="stage placeholder small muted">{{ error || '加载中…' }}</div>
    <div class="track" :style="{ width: cap ? `${cap.width}px` : undefined }">
      <span class="hint small">{{ finished ? '已完成，请点击登录' : '向右拖动滑块完成拼图' }}</span>
      <div class="fill" :style="{ width: `${x + 20}px` }" />
      <div class="handle" role="slider" aria-label="拖动完成拼图" :aria-valuenow="Math.round(x)"
        :class="{ done: finished }" :style="{ left: `${x}px` }"
        @pointerdown="down" @pointermove="move" @pointerup="up" @pointercancel="up">→</div>
    </div>
  </div>
</template>

<style scoped>
.captcha { display: flex; flex-direction: column; gap: var(--space-2); }
/* 固定 280px，不随容器缩放：滑块位置以图片像素计算，缩放会导致位置对不上 */
.stage { position: relative; border-radius: var(--radius-sm); overflow: hidden; user-select: none; }
.stage img { display: block; }
.placeholder { display: flex; align-items: center; justify-content: center; background: var(--surface-2); width: 280px; height: 140px; }
.piece { position: absolute; pointer-events: none; filter: drop-shadow(0 1px 2px rgba(0, 0, 0, .5)); }
.refresh { position: absolute; top: var(--space-1); right: var(--space-1); color: var(--on-accent); font-size: var(--font-lg); }
.track { position: relative; height: 40px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--surface-2); touch-action: none; }
.hint { position: absolute; inset: 0 0 0 44px; display: flex; align-items: center; justify-content: center; color: var(--text-muted); pointer-events: none; }
.fill { position: absolute; left: 0; top: 0; bottom: 0; background: color-mix(in srgb, var(--accent) 15%, transparent); border-radius: var(--radius-sm); }
.handle { position: absolute; top: -1px; width: 44px; height: 40px; border-radius: var(--radius-sm); background: var(--accent); color: var(--on-accent);
  display: flex; align-items: center; justify-content: center; cursor: grab; touch-action: none; user-select: none; }
.handle:active { cursor: grabbing; }
.handle.done { background: var(--ok); }
</style>
