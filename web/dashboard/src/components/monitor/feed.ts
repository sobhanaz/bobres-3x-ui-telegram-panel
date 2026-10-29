import type { ComputedRef, InjectionKey, Ref } from 'vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Lang } from '../../format'
import { initialCalendar } from '../../reports'
import { availabilityText, clock, count, duration, pct, size, stamp, type Range, type SeriesAnswer } from '../../monitor'

// What the Monitoring tab hands its charts: the time grid of the range the series belong to, the series
// core answered (by metric), the metrics it has data for, and whether a fresh look is on its way.

export interface Grid {
  range: Range
  step: number
  /** the start of every bucket, oldest first */
  times: number[]
  /** each point written in full (tooltip, table, screen reader) and short (the axis) */
  categories: string[]
  ticks: string[]
}

export interface ChartFeed {
  grid: ComputedRef<Grid>
  answers: Ref<Record<string, SeriesAnswer>>
  available: Ref<Set<string>>
  loading: Ref<boolean>
}

/** One line of a chart: a metric, the name of the thing it measures and its colour slot (the same thing keeps the same colour on every chart). */
export interface MetricDef {
  metric: string
  label: string
  slot?: number | 'other'
  dashed?: boolean
  area?: boolean
}

export const feedKey: InjectionKey<ChartFeed> = Symbol('monitor-chart-feed')

/** useFmt gives a component the page's writing of numbers, sizes, durations and times: in the language's
 * digits and, for dates, in the calendar chosen on the Reports page (Jalali in Persian until one is chosen). */
export function useFmt() {
  const { locale } = useI18n()
  const lang = computed(() => locale.value as Lang)
  const cal = computed(() => initialCalendar(lang.value))
  return {
    lang,
    cal,
    at: (unix: number) => (unix > 0 ? stamp(unix, lang.value, cal.value) : ''),
    time: (unix: number) => (unix > 0 ? clock(unix, lang.value) : ''),
    n: (v: number | null | undefined, digits = 0) => (typeof v === 'number' ? count(v, lang.value, digits) : ''),
    pct: (v: number) => pct(v, lang.value),
    avail: (v: number) => availabilityText(v, lang.value),
    size: (v: number) => size(v, lang.value),
    rate: (v: number) => size(v, lang.value, true),
    dur: (seconds: number) => duration(seconds, lang.value),
  }
}
