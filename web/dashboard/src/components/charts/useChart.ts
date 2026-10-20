import { computed, onBeforeUnmount, onMounted, ref, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'

/** useChartWidth tracks the width of the element a chart sits in. Until the
 * browser has measured it (and where nothing can be measured: a test) the
 * width is the fallback, so a chart always has a size to draw at. */
export function useChartWidth(el: Ref<HTMLElement | null>, fallback = 640): Ref<number> {
  const width = ref(fallback)
  let ro: ResizeObserver | undefined
  const read = () => {
    const w = el.value?.clientWidth ?? 0
    if (w > 0) width.value = Math.round(w)
  }
  onMounted(() => {
    read()
    if (typeof ResizeObserver !== 'undefined' && el.value) {
      ro = new ResizeObserver(read)
      ro.observe(el.value)
    }
  })
  onBeforeUnmount(() => ro?.disconnect())
  return width
}

/** useReducedMotion follows the person's prefers-reduced-motion setting. */
export function useReducedMotion(): Ref<boolean> {
  const reduced = ref(false)
  let mq: MediaQueryList | undefined
  const set = () => (reduced.value = !!mq?.matches)
  onMounted(() => {
    if (typeof matchMedia !== 'function') return
    mq = matchMedia('(prefers-reduced-motion: reduce)')
    set()
    mq.addEventListener?.('change', set)
  })
  onBeforeUnmount(() => mq?.removeEventListener?.('change', set))
  return reduced
}

/** useRtl says whether the dashboard reads right to left (Persian), unless a
 * chart is told otherwise. Time runs against the reading direction's end: the
 * oldest point is at the start side, which is the right in Persian. */
export function useRtl(forced: () => boolean | undefined): Ref<boolean> {
  const { locale } = useI18n()
  return computed(() => forced() ?? locale.value === 'fa')
}

let counter = 0
/** uid makes an id that is unique on the page, for aria-describedby. */
export function uid(prefix: string): string {
  counter += 1
  return `${prefix}-${counter}`
}

/** byMouse says whether a pointer event came from a mouse or a pen: a finger that lifts has "left" the chart too, but the
 * point it touched should stay picked until the next tap, or the tooltip would flash and be gone before it can be read. */
export const byMouse = (e: Event): boolean => (e as PointerEvent).pointerType !== 'touch'

export type KeyStep = 'prev' | 'next' | 'first' | 'last' | 'clear' | null

/** keyStep reads a key press on a chart: the arrows move along the axis the way
 * they look on screen (the right arrow goes right, which is earlier in
 * right-to-left), Home and End jump, Escape leaves. */
export function keyStep(e: KeyboardEvent, rtl: boolean): KeyStep {
  switch (e.key) {
    case 'ArrowRight':
    case 'ArrowDown':
      return rtl && e.key === 'ArrowRight' ? 'prev' : 'next'
    case 'ArrowLeft':
    case 'ArrowUp':
      return rtl && e.key === 'ArrowLeft' ? 'next' : 'prev'
    case 'Home':
      return 'first'
    case 'End':
      return 'last'
    case 'Escape':
      return 'clear'
    default:
      return null
  }
}

/** stepIndex moves an active index (null: none yet) by a key step within n items. */
export function stepIndex(current: number | null, step: KeyStep, n: number): number | null {
  if (n <= 0 || step === null) return current
  switch (step) {
    case 'clear':
      return null
    case 'first':
      return 0
    case 'last':
      return n - 1
    case 'next':
      return current === null ? 0 : Math.min(n - 1, current + 1)
    default:
      return current === null ? n - 1 : Math.max(0, current - 1)
  }
}
