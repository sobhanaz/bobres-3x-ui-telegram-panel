<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ChartFrame from './ChartFrame.vue'
import ChartTooltip from './ChartTooltip.vue'
import { bandIndex, bandOf, barPath, linear, niceTicks, stackBounds, textWidth, tickIndexes, truncate } from './scale'
import { seriesColor } from './palette'
import { byMouse, keyStep, stepIndex, uid, useChartWidth, useRtl } from './useChart'
import type { ChartSeries, ChartTableData, LegendItem, TooltipData, ValueFormat } from './types'

// Bars: columns over an ordered axis (grouped or stacked), or horizontal bars
// for a ranking with the value written at the tip. A bar is at most 28 px
// thick, rounded at the end that carries the value and square at its base;
// touching bars are separated by a gap of the surface colour. The pointer or
// the arrow keys pick a category and one tooltip lists every series in it.
// See README.md for the props.
const props = withDefaults(
  defineProps<{
    title: string
    subtitle?: string
    /** the full name of each category: tooltip, table and screen reader */
    categories: string[]
    /** the short name of each category for the axis (default: the categories) */
    ticks?: string[]
    series: ChartSeries[]
    format: ValueFormat
    axisFormat?: ValueFormat
    mode?: 'grouped' | 'stacked'
    orientation?: 'vertical' | 'horizontal'
    /** categories that hold only part of their period are drawn lighter */
    partial?: boolean[]
    /** px; horizontal bars default to the height their rows need */
    height?: number
    rtl?: boolean
    loading?: boolean
    emptyText?: string
    /** write the value at the tip of every bar (few bars only) */
    labels?: boolean
    corner?: string
    summary?: string
  }>(),
  { mode: 'grouped', orientation: 'vertical', rtl: undefined },
)
const { t } = useI18n()
const rtl = useRtl(() => props.rtl)
const box = ref<HTMLElement | null>(null)
const width = useChartWidth(box)
const hintId = uid('chart-hint')

const hidden = ref(new Set<string>())
const toggle = (key: string) => {
  const next = new Set(hidden.value)
  if (next.has(key)) next.delete(key)
  else if (props.series.length - next.size > 1) next.add(key)
  hidden.value = next
}
const shown = computed(() => props.series.filter((s) => !hidden.value.has(s.key)))
const n = computed(() => props.categories.length)
const horizontal = computed(() => props.orientation === 'horizontal')
const stacked = computed(() => props.mode === 'stacked')
const colorOf = (s: ChartSeries) => seriesColor(s.slot, props.series.indexOf(s))
const axisFmt = computed(() => props.axisFormat ?? props.format)

const THICK = 28
const GAP = 2
const ROW = 34 // horizontal bars: the height of a row

const stack = computed(() => (stacked.value ? stackBounds(shown.value.map((s) => s.values)) : null))
const scale = computed(() => {
  let lo = 0
  let hi = 0
  if (stack.value) for (const u of stack.value.upper) for (const v of u) hi = Math.max(hi, v)
  else for (const s of shown.value) for (const v of s.values) {
    lo = Math.min(lo, v)
    hi = Math.max(hi, v)
  }
  return niceTicks(lo, hi, 4, true)
})
const H = computed(() => (horizontal.value ? Math.max(props.height ?? 0, n.value * ROW + 12) : (props.height ?? 240)))

// ---- layout (physical coordinates; the language mirrors the horizontal axis) ----
const catLabels = computed(() => props.ticks ?? props.categories)
const yLabelW = computed(() => Math.max(...scale.value.ticks.map((v) => textWidth(axisFmt.value(v)))) + 10)
const catW = computed(() => Math.min(width.value * 0.4, Math.max(...catLabels.value.map((c) => textWidth(c)), 0) + 12))
const valueW = computed(() => (props.labels || horizontal.value ? Math.max(...shown.value.flatMap((s) => s.values.map((v) => textWidth(props.format(v)))), 0) + 10 : 0))
const M = computed(() => {
  if (horizontal.value) {
    return { top: 4, bottom: 4, left: rtl.value ? valueW.value : catW.value, right: rtl.value ? catW.value : valueW.value }
  }
  return { top: 14, bottom: 26, left: rtl.value ? 12 : yLabelW.value, right: rtl.value ? yLabelW.value : 12 }
})
const x0 = computed(() => M.value.left)
const x1 = computed(() => Math.max(x0.value + 1, width.value - M.value.right))
const yTop = computed(() => M.value.top)
const yBot = computed(() => H.value - M.value.bottom)

// Vertical: categories along x (oldest at the start side), values up.
const ys = computed(() => linear(scale.value.min, scale.value.max, yBot.value, yTop.value))
const catBand = (i: number) => {
  const b = bandOf(i, n.value, x0.value, x1.value)
  return rtl.value ? { start: x0.value + x1.value - b.start - b.size, size: b.size } : b
}
// Horizontal: categories down y, values along x from the start side.
const xs = computed(() => {
  const f = linear(scale.value.min, scale.value.max, 0, 1)
  return (v: number) => (rtl.value ? x1.value - f(v) * (x1.value - x0.value) : x0.value + f(v) * (x1.value - x0.value))
})
const rowBand = (i: number) => bandOf(i, n.value, yTop.value, yBot.value)

interface Mark {
  key: string
  d: string
  fill: string
  /** the series' share of the category's band, for the hover wash */
  cat: number
  partial: boolean
}
const marks = computed<Mark[]>(() => {
  const out: Mark[] = []
  const k = shown.value.length
  for (let i = 0; i < n.value; i++) {
    const partial = !!props.partial?.[i]
    if (horizontal.value) {
      const band = rowBand(i)
      const th = Math.min(THICK - 6, (band.size - 8 - (stacked.value ? 0 : (k - 1) * GAP)) / (stacked.value ? 1 : k))
      const total = stacked.value ? th : th * k + (k - 1) * GAP
      const top = band.start + (band.size - total) / 2
      shown.value.forEach((s, j) => {
        const v = s.values[i] ?? 0
        const lo = stack.value ? stack.value.lower[j][i] : Math.min(0, v)
        const hi = stack.value ? stack.value.upper[j][i] : Math.max(0, v)
        if (hi === lo) return
        const a = xs.value(lo)
        const b = xs.value(hi)
        const left = Math.min(a, b)
        const tipRound = !stack.value || j === lastNonZero(i) ? (rtl.value ? 'left' : 'right') : 'none'
        out.push({ key: `${s.key}-${i}`, d: barPath(left, top + (stacked.value ? 0 : j * (th + GAP)), Math.abs(b - a), th, 4, tipRound), fill: colorOf(s), cat: i, partial })
      })
    } else {
      const band = catBand(i)
      const inner = Math.max(2, band.size * 0.76)
      const w = Math.min(THICK, stacked.value ? inner : (inner - (k - 1) * GAP) / k)
      const total = stacked.value ? w : w * k + (k - 1) * GAP
      const left = band.start + (band.size - total) / 2
      shown.value.forEach((s, j) => {
        const v = s.values[i] ?? 0
        const lo = stack.value ? stack.value.lower[j][i] : Math.min(0, v)
        const hi = stack.value ? stack.value.upper[j][i] : Math.max(0, v)
        if (hi === lo) return
        const ya = ys.value(hi)
        const yb = ys.value(lo)
        const round = stack.value ? (j === lastNonZero(i) ? 'top' : 'none') : v < 0 ? 'bottom' : 'top'
        out.push({ key: `${s.key}-${i}`, d: barPath(left + (stacked.value ? 0 : j * (w + GAP)), ya, w, Math.max(1.5, yb - ya), 4, round), fill: colorOf(s), cat: i, partial })
      })
    }
  }
  return out
})
// In a stack the segment that carries the rounded end is the top-most with a value.
function lastNonZero(i: number): number {
  for (let j = shown.value.length - 1; j >= 0; j--) if ((shown.value[j].values[i] ?? 0) > 0) return j
  return -1
}

const valueLabels = computed(() => {
  if (!props.labels && !horizontal.value) return []
  const out: { key: string; x: number; y: number; anchor: 'start' | 'end' | 'middle'; text: string }[] = []
  for (let i = 0; i < n.value; i++) {
    const total = stacked.value ? shown.value.reduce((s, x) => s + Math.max(0, x.values[i] ?? 0), 0) : null
    shown.value.forEach((s, j) => {
      if (stacked.value && j !== shown.value.length - 1) return
      const v = total ?? s.values[i] ?? 0
      if (!v) return
      if (horizontal.value) {
        const band = rowBand(i)
        const tip = xs.value(v)
        out.push({ key: `${s.key}-${i}`, x: tip + (rtl.value ? -6 : 6), y: band.start + band.size / 2, anchor: rtl.value ? 'end' : 'start', text: props.format(v) })
      } else {
        const band = catBand(i)
        out.push({ key: `${s.key}-${i}`, x: band.start + band.size / 2, y: ys.value(v) - 5, anchor: 'middle', text: props.format(v) })
      }
    })
  }
  return out
})

const xTicks = computed(() => {
  if (horizontal.value) return []
  const maxLabels = Math.max(2, Math.floor((x1.value - x0.value) / 70))
  return tickIndexes(n.value, maxLabels).map((i) => {
    const b = catBand(i)
    const text = catLabels.value[i] ?? ''
    const cx = b.start + b.size / 2
    const w = textWidth(text)
    let anchor: 'start' | 'middle' | 'end' = 'middle'
    if (cx - w / 2 < 2) anchor = 'start'
    else if (cx + w / 2 > width.value - 2) anchor = 'end'
    return { i, text, x: anchor === 'start' ? Math.max(2, cx) : anchor === 'end' ? Math.min(width.value - 2, cx) : cx, anchor }
  })
})
const rowLabels = computed(() => {
  if (!horizontal.value) return []
  const maxChars = Math.max(4, Math.floor((catW.value - 8) / 6.4))
  return catLabels.value.map((c, i) => {
    const b = rowBand(i)
    return { i, text: truncate(c, maxChars), x: rtl.value ? x1.value + 8 : x0.value - 8, y: b.start + b.size / 2, anchor: rtl.value ? ('start' as const) : ('end' as const) }
  })
})
const yTicks = computed(() => (horizontal.value ? [] : scale.value.ticks.map((v) => ({ v, y: ys.value(v), text: axisFmt.value(v) }))))
const yLabelX = computed(() => (rtl.value ? x1.value + 8 : x0.value - 8))

// ---- the active category ----
const active = ref<number | null>(null)
const svg = ref<SVGSVGElement | null>(null)
function fromPointer(e: PointerEvent) {
  const el = svg.value
  if (!el || n.value === 0) return
  const rect = el.getBoundingClientRect()
  if (horizontal.value) {
    const py = rect.height > 0 ? ((e.clientY - rect.top) * H.value) / rect.height : e.clientY
    active.value = bandIndex(py, n.value, yTop.value, yBot.value)
  } else {
    const px = rect.width > 0 ? ((e.clientX - rect.left) * width.value) / rect.width : e.clientX
    const xl = rtl.value ? x0.value + x1.value - px : px
    active.value = bandIndex(xl, n.value, x0.value, x1.value)
  }
}
function onKey(e: KeyboardEvent) {
  const step = keyStep(e, rtl.value && !horizontal.value)
  if (step === null) return
  e.preventDefault()
  active.value = stepIndex(active.value, step, n.value)
}
const onFocus = () => {
  if (active.value === null && n.value) active.value = horizontal.value ? 0 : n.value - 1
}

const tip = computed<TooltipData | null>(() => {
  const i = active.value
  if (i === null || i < 0 || i >= n.value) return null
  const rows = shown.value.map((s) => ({ key: s.key, label: s.label, value: props.format(s.values[i] ?? 0), color: colorOf(s) }))
  if (stacked.value && shown.value.length > 1) {
    rows.push({ key: '__total', label: t('charts.total'), value: props.format(shown.value.reduce((sum, s) => sum + Math.max(0, s.values[i] ?? 0), 0)), color: 'var(--p-text-muted-color)' })
  }
  return { title: props.categories[i], rows, note: props.partial?.[i] ? t('charts.partial') : undefined }
})
const live = computed(() => (tip.value ? `${tip.value.title}: ${tip.value.rows.map((r) => `${r.label} ${r.value}`).join(', ')}` : ''))
const wash = computed(() => {
  const i = active.value
  if (i === null) return null
  if (horizontal.value) {
    const b = rowBand(i)
    return { x: 0, y: b.start, w: width.value, h: b.size }
  }
  const b = catBand(i)
  return { x: b.start, y: yTop.value - 6, w: b.size, h: yBot.value - yTop.value + 6 }
})
const tipPos = computed(() => {
  const i = active.value
  if (i === null) return { x: 0, y: 0 }
  if (horizontal.value) {
    const b = rowBand(i)
    const top = Math.max(...shown.value.map((s) => s.values[i] ?? 0), 0)
    return { x: xs.value(top), y: b.start }
  }
  const b = catBand(i)
  return { x: b.start + b.size / 2, y: yTop.value }
})

const legend = computed<LegendItem[]>(() =>
  props.series.map((s) => ({ key: s.key, label: s.label, color: colorOf(s), hidden: hidden.value.has(s.key) })),
)
const table = computed<ChartTableData>(() => ({
  corner: props.corner ?? '',
  columns: props.series.map((s) => s.label),
  rows: props.categories.map((c, i) => ({ label: c, cells: props.series.map((s) => props.format(s.values[i] ?? 0)) })),
}))
const isEmpty = computed(() => n.value === 0 || props.series.length === 0)
</script>

<template>
  <ChartFrame
    class="bar-chart"
    :title="title"
    :subtitle="subtitle"
    :loading="loading"
    :empty="isEmpty"
    :empty-text="emptyText"
    :height="H"
    :legend="legend"
    legend-toggle
    :table="table"
    :live="live"
    @toggle="toggle"
  >
    <div
      ref="box"
      class="plot"
      tabindex="0"
      role="group"
      aria-roledescription="chart"
      :aria-label="summary ?? title"
      :aria-describedby="hintId"
      @keydown="onKey"
      @focus="onFocus"
      @blur="active = null"
      @pointerleave="(e: PointerEvent) => byMouse(e) && (active = null)"
    >
      <svg ref="svg" :width="width" :height="H" :viewBox="`0 0 ${width} ${H}`" aria-hidden="true" @pointermove="fromPointer" @pointerdown="fromPointer">
        <rect v-if="wash" class="wash" :x="wash.x" :y="wash.y" :width="wash.w" :height="wash.h" rx="4" />
        <g v-if="!horizontal" class="grid">
          <line v-for="tk in yTicks" :key="tk.v" :x1="x0" :x2="x1" :y1="tk.y" :y2="tk.y" :class="{ zero: tk.v === 0 }" />
        </g>
        <g class="axis-text">
          <template v-if="!horizontal">
            <text v-for="tk in yTicks" :key="'y' + tk.v" class="y-tick" :x="yLabelX" :y="tk.y" :text-anchor="rtl ? 'start' : 'end'" dominant-baseline="central">{{ tk.text }}</text>
            <text v-for="tk in xTicks" :key="'x' + tk.i" class="x-tick" :x="tk.x" :y="yBot + 17" :text-anchor="tk.anchor">{{ tk.text }}</text>
          </template>
          <template v-else>
            <text v-for="r in rowLabels" :key="r.i" class="row-label" :x="r.x" :y="r.y" :text-anchor="r.anchor" dominant-baseline="central">{{ r.text }}</text>
          </template>
        </g>
        <g class="marks">
          <path v-for="m in marks" :key="m.key" class="bar" :d="m.d" :fill="m.fill" :opacity="m.partial ? 0.55 : 1" stroke="var(--chart-surface)" :stroke-width="stacked ? 1.5 : 0" />
        </g>
        <g v-if="valueLabels.length" class="value-text">
          <text v-for="l in valueLabels" :key="l.key" :x="l.x" :y="l.y" :text-anchor="l.anchor" dominant-baseline="central">{{ l.text }}</text>
        </g>
        <line v-if="horizontal" class="baseline" :x1="rtl ? x1 : x0" :x2="rtl ? x1 : x0" :y1="yTop" :y2="yBot" />
      </svg>
      <ChartTooltip v-if="tip" :data="tip" :x="tipPos.x" :y="tipPos.y" :box-width="width" />
      <span :id="hintId" class="sr-only">{{ t('charts.keys_hint') }}</span>
    </div>
  </ChartFrame>
</template>

<style scoped>
.plot {
  position: relative;
  min-inline-size: 0;
  border-radius: 0.5rem;
}
.plot:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 2px;
}
svg {
  direction: ltr;
  display: block;
  max-inline-size: 100%;
  overflow: visible;
  touch-action: pan-y;
}
.wash {
  fill: var(--chart-hover);
}
.grid line {
  stroke: var(--chart-grid);
  stroke-width: 1;
}
.grid line.zero,
.baseline {
  stroke: var(--chart-axis);
  stroke-width: 1;
}
.axis-text text {
  fill: var(--chart-axis-text);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.value-text text {
  fill: var(--p-text-color);
  font-size: 12px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.marks {
  animation: chart-in 0.45s ease-out;
}
@keyframes chart-in {
  from {
    opacity: 0;
  }
}
.sr-only {
  position: absolute;
  inline-size: 1px;
  block-size: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
</style>
