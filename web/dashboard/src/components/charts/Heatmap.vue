<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ChartFrame from './ChartFrame.vue'
import ChartTooltip from './ChartTooltip.vue'
import { level, textWidth, tickIndexes } from './scale'
import { sequentialColor } from './palette'
import { byMouse, uid, useChartWidth, useRtl } from './useChart'
import type { ChartTableData, TooltipData, ValueFormat } from './types'

// A grid of cells shaded by one value on a one-hue scale (light is little, dark
// is a lot; in the dark theme brighter is more). Cells are at most 28 px high
// with a gap between them; the pointer or the arrow keys pick a cell and a
// tooltip names it. Rows are read down, columns along the reading direction.
// See README.md for the props.
const props = withDefaults(
  defineProps<{
    title: string
    subtitle?: string
    rows: string[]
    /** the full name of each column: tooltip and table */
    cols: string[]
    /** the short name of each column for the axis (default: the columns) */
    colTicks?: string[]
    /** values[row][col] */
    values: number[][]
    format: ValueFormat
    /** what the value is, for the tooltip ("Paid orders") */
    valueLabel: string
    rtl?: boolean
    loading?: boolean
    emptyText?: string
    corner?: string
    summary?: string
  }>(),
  { rtl: undefined },
)
const { t } = useI18n()
const rtl = useRtl(() => props.rtl)
const box = ref<HTMLElement | null>(null)
const width = useChartWidth(box)
const hintId = uid('chart-hint')

const ROW = 28
const TOP = 20
const LEVELS = 7
const max = computed(() => props.values.reduce((m, r) => Math.max(m, ...r), 0))
const labelW = computed(() => Math.max(...props.rows.map((r) => textWidth(r)), 0) + 10)
const left = computed(() => (rtl.value ? 4 : labelW.value))
const right = computed(() => (rtl.value ? labelW.value : 4))
const x0 = computed(() => left.value)
const x1 = computed(() => Math.max(x0.value + props.cols.length, width.value - right.value))
const cw = computed(() => (x1.value - x0.value) / Math.max(1, props.cols.length))
const H = computed(() => TOP + props.rows.length * ROW + 2)
const colX = (c: number) => (rtl.value ? x1.value - (c + 1) * cw.value : x0.value + c * cw.value)

const cells = computed(() =>
  props.values.flatMap((row, r) =>
    row.map((v, c) => ({ r, c, v, x: colX(c) + 1, y: TOP + r * ROW + 1, lvl: level(v, max.value, LEVELS) })),
  ),
)
const ticks = computed(() => {
  const labels = props.colTicks ?? props.cols
  const maxLabels = Math.max(2, Math.floor((x1.value - x0.value) / 38))
  return tickIndexes(props.cols.length, maxLabels).map((c) => ({ c, text: labels[c] ?? '', x: colX(c) + cw.value / 2 }))
})
const rowLabels = computed(() => props.rows.map((text, r) => ({ r, text, x: rtl.value ? x1.value + 8 : x0.value - 8, y: TOP + r * ROW + ROW / 2 })))

const active = ref<{ r: number; c: number } | null>(null)
const svg = ref<SVGSVGElement | null>(null)
function fromPointer(e: PointerEvent) {
  const el = svg.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const px = rect.width > 0 ? ((e.clientX - rect.left) * width.value) / rect.width : e.clientX
  const py = rect.height > 0 ? ((e.clientY - rect.top) * H.value) / rect.height : e.clientY
  const c = Math.floor((rtl.value ? x1.value - px : px - x0.value) / cw.value)
  const r = Math.floor((py - TOP) / ROW)
  active.value = c >= 0 && c < props.cols.length && r >= 0 && r < props.rows.length ? { r, c } : null
}
function onKey(e: KeyboardEvent) {
  const a = active.value ?? { r: 0, c: 0 }
  const nr = props.rows.length
  const nc = props.cols.length
  const clamp = (v: number, hi: number) => Math.min(hi - 1, Math.max(0, v))
  const dx = e.key === 'ArrowRight' ? (rtl.value ? -1 : 1) : e.key === 'ArrowLeft' ? (rtl.value ? 1 : -1) : 0
  if (e.key === 'Escape') active.value = null
  else if (dx !== 0) active.value = active.value ? { r: a.r, c: clamp(a.c + dx, nc) } : a
  else if (e.key === 'ArrowDown') active.value = active.value ? { r: clamp(a.r + 1, nr), c: a.c } : a
  else if (e.key === 'ArrowUp') active.value = active.value ? { r: clamp(a.r - 1, nr), c: a.c } : a
  else if (e.key === 'Home') active.value = { r: a.r, c: 0 }
  else if (e.key === 'End') active.value = { r: a.r, c: nc - 1 }
  else return
  e.preventDefault()
}
const onFocus = () => {
  if (!active.value && props.rows.length && props.cols.length) active.value = { r: 0, c: 0 }
}

const tip = computed<TooltipData | null>(() => {
  const a = active.value
  if (!a || a.r >= props.rows.length || a.c >= props.cols.length) return null
  const v = props.values[a.r]?.[a.c] ?? 0
  return { title: `${props.rows[a.r]}, ${props.cols[a.c]}`, rows: [{ key: 'v', label: props.valueLabel, value: props.format(v), color: sequentialColor(level(v, max.value, LEVELS) || 1) }] }
})
const live = computed(() => (tip.value ? `${tip.value.title}: ${tip.value.rows[0].label} ${tip.value.rows[0].value}` : ''))
const tipPos = computed(() => {
  const a = active.value
  return a ? { x: colX(a.c) + cw.value / 2, y: TOP + a.r * ROW } : { x: 0, y: 0 }
})
const table = computed<ChartTableData>(() => ({
  corner: props.corner ?? '',
  columns: props.cols,
  rows: props.rows.map((r, i) => ({ label: r, cells: props.cols.map((_, c) => props.format(props.values[i]?.[c] ?? 0)) })),
}))
const isEmpty = computed(() => props.rows.length === 0 || props.cols.length === 0 || max.value <= 0)
</script>

<template>
  <ChartFrame class="heatmap" :title="title" :subtitle="subtitle" :loading="loading" :empty="isEmpty" :empty-text="emptyText" :height="H" :table="table" :live="live">
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
        <g class="axis-text">
          <text v-for="tk in ticks" :key="tk.c" class="col-tick" :x="tk.x" :y="12" text-anchor="middle">{{ tk.text }}</text>
          <text v-for="l in rowLabels" :key="l.r" class="row-label" :x="l.x" :y="l.y" :text-anchor="rtl ? 'start' : 'end'" dominant-baseline="central">{{ l.text }}</text>
        </g>
        <g class="cells">
          <rect
            v-for="ce in cells"
            :key="ce.r + '-' + ce.c"
            :x="ce.x"
            :y="ce.y"
            :width="Math.max(1, cw - 2)"
            :height="ROW - 2"
            rx="3"
            :fill="sequentialColor(ce.lvl)"
            :class="{ on: active && active.r === ce.r && active.c === ce.c }"
            :data-level="ce.lvl"
          />
        </g>
      </svg>
      <ChartTooltip v-if="tip" :data="tip" :x="tipPos.x" :y="tipPos.y" :box-width="width" />
      <span :id="hintId" class="sr-only">{{ t('charts.keys_hint') }}</span>
    </div>
    <template #footer>
      <div class="scale" aria-hidden="true">
        <span>{{ t('charts.less') }}</span>
        <svg width="112" height="10" viewBox="0 0 112 10"><rect v-for="i in LEVELS" :key="i" :x="(i - 1) * 16" y="0" width="14" height="10" rx="2" :fill="sequentialColor(i - 1)" /></svg>
        <span>{{ t('charts.more') }}</span>
      </div>
    </template>
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
}
.axis-text text {
  fill: var(--chart-axis-text);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.cells {
  animation: chart-in 0.45s ease-out;
}
.cells rect {
  stroke: transparent;
  stroke-width: 2;
}
.cells rect.on {
  stroke: var(--p-text-color);
}
@keyframes chart-in {
  from {
    opacity: 0;
  }
}
.scale {
  display: flex;
  gap: 0.5rem;
  align-items: center;
  margin-block-start: 0.5rem;
  color: var(--p-text-muted-color);
  font-size: 0.8rem;
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
