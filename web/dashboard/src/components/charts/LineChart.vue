<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ChartFrame from './ChartFrame.vue'
import ChartTooltip from './ChartTooltip.vue'
import { areaPath, linear, nearestPoint, niceTicks, pointX, stackBounds, textWidth, tickIndexes, type XY } from './scale'
import { seriesColor } from './palette'
import { byMouse, keyStep, stepIndex, uid, useChartWidth, useRtl } from './useChart'
import type { ChartSeries, ChartTableData, LegendItem, TooltipData, ValueFormat } from './types'

// Lines and areas over an ordered axis (time, usually): one or several series,
// optionally stacked. The pointer or the arrow keys pick a point and one
// tooltip lists every series at it. A point flagged `partial` (a period the
// range only partly covers) is joined to its neighbour by a dashed segment.
// See README.md for the props.
const props = withDefaults(
  defineProps<{
    title: string
    subtitle?: string
    /** the full name of each point: tooltip, table and screen reader */
    categories: string[]
    /** the short name of each point for the axis (default: the categories) */
    ticks?: string[]
    series: ChartSeries[]
    format: ValueFormat
    /** the short form for the vertical axis ("1.2M"); default: format */
    axisFormat?: ValueFormat
    /** stack the series on each other (areas of parts of a whole) */
    stacked?: boolean
    /** points that hold only part of their period */
    partial?: boolean[]
    height?: number
    /** force the direction; default follows the language */
    rtl?: boolean
    loading?: boolean
    emptyText?: string
    /** a fixed vertical range, e.g. 0 to 100 for a percent */
    yMin?: number
    yMax?: number
    /** whether the vertical axis starts at zero (default) or fits the data */
    baseline?: 'zero' | 'auto'
    /** the header of the table's first column */
    corner?: string
    /** the chart's name for screen readers; default: the title */
    summary?: string
    /** draw a dot on every point (default: when there are 24 or fewer) */
    markers?: boolean
  }>(),
  { height: 240, baseline: 'zero', rtl: undefined, markers: undefined },
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
  else if (props.series.length - next.size > 1) next.add(key) // the last visible series stays
  hidden.value = next
}
const shown = computed(() => props.series.filter((s) => !hidden.value.has(s.key)))
const n = computed(() => props.categories.length)
const colorOf = (s: ChartSeries) => seriesColor(s.slot, props.series.indexOf(s))

const stack = computed(() => (props.stacked ? stackBounds(shown.value.map((s) => s.values)) : null))
const axisFmt = computed(() => props.axisFormat ?? props.format)
const scale = computed(() => {
  let lo = Infinity
  let hi = -Infinity
  if (stack.value) {
    lo = 0
    for (const u of stack.value.upper) for (const v of u) hi = Math.max(hi, v)
  } else {
    for (const s of shown.value) for (const v of s.values) {
      lo = Math.min(lo, v)
      hi = Math.max(hi, v)
    }
  }
  if (!Number.isFinite(lo)) lo = 0
  if (!Number.isFinite(hi)) hi = 1
  if (props.yMin !== undefined) lo = props.yMin
  if (props.yMax !== undefined) hi = props.yMax
  return niceTicks(lo, hi, 4, props.baseline === 'zero' && props.yMin === undefined)
})

const labelW = computed(() => Math.max(...scale.value.ticks.map((v) => textWidth(axisFmt.value(v)))) + 10)
const M = computed(() => ({ top: 10, bottom: 26, left: rtl.value ? 12 : labelW.value, right: rtl.value ? labelW.value : 12 }))
const x0 = computed(() => M.value.left)
const x1 = computed(() => Math.max(x0.value + 1, width.value - M.value.right))
const yBase = computed(() => props.height - M.value.bottom)
const y = computed(() => linear(scale.value.min, scale.value.max, yBase.value, M.value.top))
// Time runs from the start side: the oldest point is at the left in English and at the right in Persian.
const X = (i: number) => {
  const x = pointX(i, n.value, x0.value, x1.value)
  return rtl.value ? x0.value + x1.value - x : x
}

interface Line {
  s: ChartSeries
  color: string
  runs: { d: string; dashed: boolean }[]
  area: string
  pts: XY[]
}
const lines = computed<Line[]>(() =>
  shown.value.map((s, k) => {
    const upper = stack.value ? stack.value.upper[k] : s.values
    const pts = upper.map((v, i) => ({ x: X(i), y: y.value(v) }))
    const runs: Line['runs'] = []
    for (let i = 0; i + 1 < pts.length; i++) {
      const dashed = !!(props.partial?.[i] || props.partial?.[i + 1])
      const last = runs[runs.length - 1]
      if (last && last.dashed === dashed) last.d += `L${pts[i + 1].x.toFixed(2)},${pts[i + 1].y.toFixed(2)}`
      else runs.push({ dashed, d: `M${pts[i].x.toFixed(2)},${pts[i].y.toFixed(2)}L${pts[i + 1].x.toFixed(2)},${pts[i + 1].y.toFixed(2)}` })
    }
    let area = ''
    if (stack.value) {
      const lower = stack.value.lower[k].map((v, i) => ({ x: X(i), y: y.value(v) }))
      area = areaPath(pts, lower)
    } else if (s.area) {
      const zero = Math.min(yBase.value, Math.max(M.value.top, y.value(0)))
      area = areaPath(pts, pts.map((p) => ({ x: p.x, y: zero })))
    }
    return { s, color: colorOf(s), runs, area, pts }
  }),
)
// The line of a single point is a dot, not a path.
const lone = computed(() => n.value === 1)

const xTicks = computed(() => {
  const labels = props.ticks ?? props.categories
  const maxLabels = Math.max(2, Math.floor((x1.value - x0.value) / 84))
  return tickIndexes(n.value, maxLabels).map((i) => {
    const text = labels[i] ?? ''
    const w = textWidth(text)
    const x = X(i)
    let anchor: 'start' | 'middle' | 'end' = 'middle'
    if (x - w / 2 < 2) anchor = 'start'
    else if (x + w / 2 > width.value - 2) anchor = 'end'
    return { i, text, x: anchor === 'start' ? Math.max(2, x) : anchor === 'end' ? Math.min(width.value - 2, x) : x, anchor }
  })
})
const yTicks = computed(() => scale.value.ticks.map((v) => ({ v, y: y.value(v), text: axisFmt.value(v) })))
const yLabelX = computed(() => (rtl.value ? x1.value + 8 : x0.value - 8))
const yLabelAnchor = computed(() => (rtl.value ? 'start' : 'end'))
const showMarkers = computed(() => props.markers ?? n.value <= 24)

// ---- the active point (pointer or keyboard) ----
const active = ref<number | null>(null)
const svg = ref<SVGSVGElement | null>(null)
function fromPointer(e: PointerEvent) {
  const el = svg.value
  if (!el || n.value === 0) return
  const rect = el.getBoundingClientRect()
  const px = rect.width > 0 ? ((e.clientX - rect.left) * width.value) / rect.width : e.clientX
  const xl = rtl.value ? x0.value + x1.value - px : px
  active.value = nearestPoint(xl, n.value, x0.value, x1.value)
}
function onKey(e: KeyboardEvent) {
  const step = keyStep(e, rtl.value)
  if (step === null) return
  e.preventDefault()
  active.value = stepIndex(active.value, step, n.value)
}
const onFocus = () => {
  if (active.value === null && n.value) active.value = n.value - 1
}

const tip = computed<TooltipData | null>(() => {
  const i = active.value
  if (i === null || i < 0 || i >= n.value) return null
  const rows = shown.value.map((s) => ({ key: s.key, label: s.label, value: props.format(s.values[i] ?? 0), color: colorOf(s), dashed: s.dashed }))
  if (props.stacked && shown.value.length > 1) {
    const total = shown.value.reduce((sum, s) => sum + Math.max(0, s.values[i] ?? 0), 0)
    rows.push({ key: '__total', label: t('charts.total'), value: props.format(total), color: 'var(--p-text-muted-color)', dashed: false })
  }
  return { title: props.categories[i], rows, note: props.partial?.[i] ? t('charts.partial') : undefined }
})
const live = computed(() => (tip.value ? `${tip.value.title}: ${tip.value.rows.map((r) => `${r.label} ${r.value}`).join(', ')}` : ''))
const tipX = computed(() => (active.value === null ? 0 : X(active.value)))

const legend = computed<LegendItem[]>(() =>
  props.series.map((s) => ({ key: s.key, label: s.label, color: colorOf(s), dashed: s.dashed, line: !props.stacked && !s.area, hidden: hidden.value.has(s.key) })),
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
    class="line-chart"
    :title="title"
    :subtitle="subtitle"
    :loading="loading"
    :empty="isEmpty"
    :empty-text="emptyText"
    :height="height"
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
      <svg ref="svg" :width="width" :height="height" :viewBox="`0 0 ${width} ${height}`" aria-hidden="true" @pointermove="fromPointer" @pointerdown="fromPointer">
        <g class="grid">
          <line v-for="tk in yTicks" :key="tk.v" :x1="x0" :x2="x1" :y1="tk.y" :y2="tk.y" :class="{ zero: tk.v === 0 }" />
        </g>
        <g class="axis-text">
          <text v-for="tk in yTicks" :key="'y' + tk.v" class="y-tick" :x="yLabelX" :y="tk.y" :text-anchor="yLabelAnchor" dominant-baseline="central">{{ tk.text }}</text>
          <text v-for="tk in xTicks" :key="'x' + tk.i" class="x-tick" :x="tk.x" :y="yBase + 17" :text-anchor="tk.anchor">{{ tk.text }}</text>
        </g>
        <g v-for="l in lines" :key="l.s.key" class="series">
          <path v-if="l.area" :d="l.area" :fill="l.color" :fill-opacity="stacked ? 0.55 : 0.14" :stroke="stacked ? 'var(--chart-surface)' : undefined" stroke-width="1" />
          <path v-for="(r, k) in l.runs" :key="k" :d="r.d" fill="none" :stroke="l.color" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" :stroke-dasharray="r.dashed || l.s.dashed ? '5 4' : undefined" :opacity="r.dashed ? 0.7 : 1" />
          <template v-if="showMarkers || lone">
            <circle v-for="(p, i) in l.pts" :key="i" :cx="p.x" :cy="p.y" r="3.5" :fill="l.color" stroke="var(--chart-surface)" stroke-width="2" />
          </template>
          <circle v-else-if="l.pts.length" :cx="l.pts[l.pts.length - 1].x" :cy="l.pts[l.pts.length - 1].y" r="4" :fill="l.color" stroke="var(--chart-surface)" stroke-width="2" />
        </g>
        <g v-if="active !== null" class="cursor">
          <line :x1="tipX" :x2="tipX" :y1="M.top" :y2="yBase" />
          <circle v-for="l in lines" :key="l.s.key" :cx="l.pts[active]?.x ?? tipX" :cy="l.pts[active]?.y ?? 0" r="5" :fill="l.color" stroke="var(--chart-surface)" stroke-width="2" />
        </g>
      </svg>
      <ChartTooltip v-if="tip" :data="tip" :x="tipX" :y="M.top" :box-width="width" />
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
/* The drawing is laid out in physical coordinates (time runs from the start side
   by its own mirroring), so text-anchor start is always the left. */
svg {
  direction: ltr;
  display: block;
  max-inline-size: 100%;
  overflow: visible;
  touch-action: pan-y;
}
.grid line {
  stroke: var(--chart-grid);
  stroke-width: 1;
}
.grid line.zero {
  stroke: var(--chart-axis);
}
.axis-text text {
  fill: var(--chart-axis-text);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.series {
  animation: chart-in 0.45s ease-out;
}
.cursor line {
  stroke: var(--chart-axis);
  stroke-width: 1;
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
