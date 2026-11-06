// 公开展示页的自动翻页（设计 45.3）：内容放不下时每 12 秒换一页，每页的数量按实际的卡片高度与列数计算。
// 手机宽度不翻页，全部列出，上下滚动。
import { computed, nextTick, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'

const PAGE_MS = 12000

export function useWallPaging<T>(list: Readonly<Ref<T[]>>) {
  const grid = ref<HTMLElement>()
  const perPage = ref(Number.POSITIVE_INFINITY)
  const page = ref(0)
  const pages = computed(() => Math.max(1, Math.ceil(list.value.length / perPage.value)))
  const shown = computed(() => (pages.value === 1 ? list.value : list.value.slice(page.value * perPage.value, (page.value + 1) * perPage.value)))
  const phone = window.matchMedia('(max-width: 600px)')

  function measure() {
    const el = grid.value
    const first = el?.firstElementChild as HTMLElement | null | undefined
    if (phone.matches) perPage.value = Number.POSITIVE_INFINITY
    if (!el || !first || phone.matches) return
    const cs = getComputedStyle(el)
    const gap = parseFloat(cs.rowGap) || 0
    const rows = Math.max(1, Math.floor((el.clientHeight + gap) / (first.offsetHeight + gap)))
    perPage.value = cs.gridTemplateColumns.split(' ').length * rows
  }
  watch(pages, (n) => {
    if (page.value >= n) page.value = 0
  })
  watch(() => list.value.length > 0, () => nextTick(measure))

  let flip: ReturnType<typeof setInterval>
  let observer: ResizeObserver | undefined
  onMounted(() => {
    flip = setInterval(() => (page.value = (page.value + 1) % pages.value), PAGE_MS)
    observer = new ResizeObserver(measure)
    if (grid.value) observer.observe(grid.value)
  })
  watch(grid, (el) => {
    observer?.disconnect()
    if (el) observer?.observe(el)
  })
  onUnmounted(() => {
    clearInterval(flip)
    observer?.disconnect()
  })
  return { grid, page, pages, shown }
}
