<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ChartFrame from '../charts/ChartFrame.vue'
import ChartTooltip from '../charts/ChartTooltip.vue'
import { niceTicks, stackBounds, textWidth } from '../charts/scale'
import { seriesColor } from '../charts/palette'
import { byMouse, keyStep, stepIndex, uid, useChartWidth, useReducedMotion, useRtl } from '../charts/useChart'
import type { ChartSeries, ChartTableData, LegendItem, TooltipData, ValueFormat } from '../charts/types'
import { boxFaces, clampCamera, fit, orderedByDepth, place, project, visibleFaces, type Box, type Camera, type Pt } from './projection'

// Stacked columns in 3D: each period is a column, split into the parts of a
// whole (plans, order types). The scene can be turned with the pointer or the
// two sliders, and a column is picked with the pointer or the arrow keys, as in
// the flat charts. It is drawn as SVG polygons from an orthographic camera
// (projection.ts): no WebGL, so it prints, scales and works wherever the page does.
// The caller shows the flat chart instead on a small screen or when there are
// many columns; the table button gives the numbers in either case.
const props = withDefaults(
  defineProps<{
    title: string
    subtitle?: string
    categories: string[]
    ticks?: string[]
    series: ChartSeries[]
    format: ValueFormat
    axisFormat?: ValueFormat
    partial?: boolean[]
    height?: number
    rtl?: boolean
    loading?: boolean
    emptyText?: string
    corner?: string
    summary?: string
    /** no entrance motion (the person's reduced-motion setting does the same) */
    still?: boolean
  }>(),
  { rtl: undefined },
)
const { t } = useI18n()
const rtl = useRtl(() => props.rtl)
const reduced = useReducedMotion()
const box = ref<HTMLElement | null>(null)
const width = useChartWidth(box)
// Left alone, the scene is as tall as its width asks (a long row of columns needs a long, not a tall, picture).
const H = computed(() => props.height ?? Math.round(Math.min(560, Math.max(340, width.value * 0.5))))
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
const colorOf = (s: ChartSeries) => seriesColor(s.slot, props.series.indexOf(s))
const axisFmt = computed(() => props.axisFormat ?? props.format)

// ---- the camera: turned by the pointer, the sliders and the keys ----
const home = computed<Camera>(() => ({ yaw: rtl.value ? -34 : 34, pitch: 30 }))
const cam = ref<Camera>({ yaw: 0, pitch: 34 })
const settled = ref(false)
let raf = 0
onMounted(() => {
  cam.value = { ...home.value }
  settled.value = true
  if (props.still || reduced.value || typeof requestAnimationFrame !== 'function') return
  // The scene turns into place once (the person's own reduced-motion setting skips it).
  const from = 0
  const t0 = performance.now()
  const step = (now: number) => {
    const k = Math.min(1, (now - t0) / 650)
    const ease = 1 - (1 - k) ** 3
    cam.value = { ...cam.value, yaw: from + (home.value.yaw - from) * ease }
    if (k < 1) raf = requestAnimationFrame(step)
  }
  cam.value = { ...cam.value, yaw: from }
  raf = requestAnimationFrame(step)
})
onBeforeUnmount(() => cancelAnimationFrame(raf))
const setCam = (c: Partial<Camera>) => (cam.value = clampCamera({ ...cam.value, ...c }))
const reset = () => (cam.value = { ...home.value })
let drag: { x: number; y: number; cam: Camera } | null = null
function down(e: PointerEvent) {
  drag = { x: e.clientX, y: e.clientY, cam: { ...cam.value } }
  ;(e.currentTarget as Element).setPointerCapture?.(e.pointerId)
}
function move(e: PointerEvent) {
  if (drag && e.buttons) setCam({ yaw: drag.cam.yaw + (e.clientX - drag.x) * 0.35, pitch: drag.cam.pitch - (e.clientY - drag.y) * 0.25 })
}
const up = () => (drag = null)

// ---- the scene ----
const SP = 1
const CW = 0.62
const DEPTH = 0.86
const stack = computed(() => stackBounds(shown.value.map((s) => s.values)))
const scale = computed(() => {
  let hi = 0
  for (const u of stack.value.upper) for (const v of u) hi = Math.max(hi, v)
  return niceTicks(0, hi, 4, true)
})
const ZH = computed(() => Math.min(9, Math.max(3, n.value * 0.3)))
const z = (v: number) => (v / (scale.value.max || 1)) * ZH.value
const xi = (i: number) => (rtl.value ? n.value - 1 - i : i)
const floor = computed(() => ({ x0: -0.55, x1: (n.value - 1) * SP + CW + 0.55, y0: -0.5, y1: DEPTH + 0.5 }))
const lblW = computed(() => Math.max(...scale.value.ticks.map((v) => textWidth(axisFmt.value(v)))) + 12)

interface Column {
  i: number
  segs: { s: ChartSeries; box: Box; value: number }[]
  box: Box
}
const columns = computed<Column[]>(() =>
  props.categories.map((_, i) => {
    const x0 = xi(i) * SP + (SP - CW) / 2
    const segs = shown.value.flatMap((s, k) => {
      const lo = stack.value.lower[k][i]
      const hi = stack.value.upper[k][i]
      if (hi <= lo) return []
      return [{ s, value: s.values[i] ?? 0, box: { x0, x1: x0 + CW, y0: 0, y1: DEPTH, z0: z(lo), z1: z(hi) } }]
    })
    const top = stack.value.upper.length ? Math.max(...stack.value.upper.map((u) => u[i])) : 0
    return { i, segs, box: { x0, x1: x0 + CW, y0: 0, y1: DEPTH, z0: 0, z1: z(top) } }
  }),
)

const view = computed(() => {
  const c = cam.value
  const f = floor.value
  const corners: Pt[] = []
  for (const x of [f.x0, f.x1]) for (const y of [f.y0, f.y1]) for (const zz of [0, ZH.value]) corners.push(project(x, y, zz, c))
  const pad = { top: 16, right: rtl.value ? lblW.value : 14, bottom: 40, left: rtl.value ? 14 : lblW.value }
  const fitted = fit(corners, width.value, H.value, pad)
  const P = (x: number, y: number, zz: number): Pt => place(project(x, y, zz, c), fitted)
  return { c, f, P, fitted }
})

const ptsToStr = (pts: Pt[]) => pts.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ')
const sceneFloor = computed(() => {
  const { f, P } = view.value
  return ptsToStr([P(f.x0, f.y0, 0), P(f.x1, f.y0, 0), P(f.x1, f.y1, 0), P(f.x0, f.y1, 0)])
})
// The far walls: the one behind the columns and the one on the far side.
const walls = computed(() => {
  const { c, f, P } = view.value
  const rearY = Math.cos((c.yaw * Math.PI) / 180) >= 0 ? f.y0 : f.y1
  const sideX = Math.sin((c.yaw * Math.PI) / 180) >= 0 ? f.x0 : f.x1
  const rear = ptsToStr([P(f.x0, rearY, 0), P(f.x1, rearY, 0), P(f.x1, rearY, ZH.value), P(f.x0, rearY, ZH.value)])
  const side = ptsToStr([P(sideX, f.y0, 0), P(sideX, f.y1, 0), P(sideX, f.y1, ZH.value), P(sideX, f.y0, ZH.value)])
  // The values are written along the near edge of the far side wall: the outermost edge of the scene, so
  // no column is ever in front of a label. It is the left edge in English and the right one in Persian.
  const nearY = Math.cos((c.yaw * Math.PI) / 180) >= 0 ? f.y1 : f.y0
  const centre = P((f.x0 + f.x1) / 2, (f.y0 + f.y1) / 2, 0).x
  const lines = scale.value.ticks.map((tv) => {
    const zz = z(tv)
    const a = P(f.x0, rearY, zz)
    const b = P(f.x1, rearY, zz)
    const s0 = P(sideX, f.y0, zz)
    const s1 = P(sideX, f.y1, zz)
    const at = P(sideX, nearY, zz)
    const outward = at.x <= centre ? -1 : 1
    return { v: tv, rear: [a, b], side: [s0, s1], label: { x: at.x + outward * 8, y: at.y, anchor: outward < 0 ? 'end' : 'start', text: axisFmt.value(tv) } }
  })
  return { rear, side, lines }
})
const draw = computed(() => {
  const { c, fitted } = view.value
  const boxes = columns.value.map((col) => col.box)
  return orderedByDepth(boxes, c).map((idx) => {
    const col = columns.value[idx]
    return {
      i: col.i,
      segs: col.segs.map((sg) => ({
        key: sg.s.key,
        color: colorOf(sg.s),
        faces: boxFaces(sg.box, c).map((f) => ({ face: f.face, shade: f.shade, points: ptsToStr(f.points.map((p) => place(p, fitted))) })),
      })),
    }
  })
})
function shadeFill(color: string, shade: 'top' | 'light' | 'dark'): string {
  if (shade === 'top') return `color-mix(in srgb, ${color}, white 24%)`
  if (shade === 'dark') return `color-mix(in srgb, ${color}, black 26%)`
  return color
}

const xLabels = computed(() => {
  const { c, f, P } = view.value
  const nearY = Math.cos((c.yaw * Math.PI) / 180) >= 0 ? f.y1 : f.y0
  const labels = props.ticks ?? props.categories
  const items = columns.value
    .map((col) => {
      const p = P((col.box.x0 + col.box.x1) / 2, nearY + (nearY === f.y1 ? 0.15 : -0.15), 0)
      return { i: col.i, x: p.x, y: p.y + 16, text: labels[col.i] ?? '' }
    })
    .sort((a, b) => a.x - b.x)
  // as many as fit without touching, left to right
  const out: typeof items = []
  let edge = -Infinity
  for (const it of items) {
    const w = textWidth(it.text) + 10
    if (it.x - w / 2 >= edge && it.x - w / 2 >= 0 && it.x + w / 2 <= width.value) {
      out.push(it)
      edge = it.x + w / 2
    }
  }
  return out
})

// ---- the active column ----
const active = ref<number | null>(null)
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
  const rows = shown.value.map((s) => ({ key: s.key, label: s.label, value: props.format(s.values[i] ?? 0), color: colorOf(s) }))
  if (shown.value.length > 1) rows.push({ key: '__total', label: t('charts.total'), value: props.format(shown.value.reduce((sum, s) => sum + Math.max(0, s.values[i] ?? 0), 0)), color: 'var(--p-text-muted-color)' })
  return { title: props.categories[i], rows, note: props.partial?.[i] ? t('charts.partial') : undefined }
})
const live = computed(() => (tip.value ? `${tip.value.title}: ${tip.value.rows.map((r) => `${r.label} ${r.value}`).join(', ')}` : ''))
const tipPos = computed(() => {
  const i = active.value
  if (i === null) return { x: 0, y: 0 }
  const col = columns.value[i]
  const p = view.value.P((col.box.x0 + col.box.x1) / 2, (col.box.y0 + col.box.y1) / 2, col.box.z1)
  return { x: p.x, y: Math.max(0, p.y - 8) }
})

const legend = computed<LegendItem[]>(() => props.series.map((s) => ({ key: s.key, label: s.label, color: colorOf(s), hidden: hidden.value.has(s.key) })))
const table = computed<ChartTableData>(() => ({
  corner: props.corner ?? '',
  columns: props.series.map((s) => s.label),
  rows: props.categories.map((c, i) => ({ label: c, cells: props.series.map((s) => props.format(s.values[i] ?? 0)) })),
}))
const isEmpty = computed(() => n.value === 0 || props.series.length === 0)
const facesNow = computed(() => visibleFaces(cam.value).join(' '))
const yawId = uid('yaw')
const pitchId = uid('pitch')
</script>

<template>
  <ChartFrame
    class="iso-columns"
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
      class="scene"
      tabindex="0"
      role="group"
      aria-roledescription="chart"
      :aria-label="summary ?? title"
      :aria-describedby="hintId"
      :data-faces="facesNow"
      :data-settled="settled"
      @keydown="onKey"
      @focus="onFocus"
      @blur="active = null"
      @pointerleave="(e: PointerEvent) => byMouse(e) && (active = null)"
    >
      <svg :width="width" :height="H" :viewBox="`0 0 ${width} ${H}`" aria-hidden="true" @pointerdown="down" @pointermove="move" @pointerup="up" @pointercancel="up">
        <polygon class="floor" :points="sceneFloor" />
        <polygon class="wall" :points="walls.rear" />
        <polygon class="wall" :points="walls.side" />
        <g class="rules">
          <template v-for="l in walls.lines" :key="l.v">
            <line :x1="l.rear[0].x" :y1="l.rear[0].y" :x2="l.rear[1].x" :y2="l.rear[1].y" :class="{ zero: l.v === 0 }" />
            <line :x1="l.side[0].x" :y1="l.side[0].y" :x2="l.side[1].x" :y2="l.side[1].y" />
          </template>
        </g>
        <g class="axis-text">
          <text v-for="l in walls.lines" :key="'v' + l.v" class="y-tick" :x="l.label.x" :y="l.label.y" :text-anchor="l.label.anchor" dominant-baseline="central">{{ l.label.text }}</text>
          <text v-for="l in xLabels" :key="'c' + l.i" class="x-tick" :x="l.x" :y="l.y" text-anchor="middle">{{ l.text }}</text>
        </g>
        <g
          v-for="col in draw"
          :key="col.i"
          class="column"
          :class="{ dim: active !== null && active !== col.i, on: active === col.i }"
          @pointerenter="active = col.i"
          @pointermove="active = col.i"
        >
          <g v-for="sg in col.segs" :key="sg.key">
            <polygon v-for="f in sg.faces" :key="f.face" :points="f.points" :fill="shadeFill(sg.color, f.shade as 'top' | 'light' | 'dark')" />
          </g>
        </g>
      </svg>
      <ChartTooltip v-if="tip" :data="tip" :x="tipPos.x" :y="tipPos.y" :box-width="width" />
      <span :id="hintId" class="sr-only">{{ t('charts.keys_hint') }}</span>
    </div>
    <div class="controls">
      <label :for="yawId">{{ t('charts.rotate') }}</label>
      <input :id="yawId" type="range" min="-70" max="70" step="1" :value="Math.round(cam.yaw)" @input="setCam({ yaw: Number(($event.target as HTMLInputElement).value) })" />
      <label :for="pitchId">{{ t('charts.tilt') }}</label>
      <input :id="pitchId" type="range" min="18" max="80" step="1" :value="Math.round(cam.pitch)" @input="setCam({ pitch: Number(($event.target as HTMLInputElement).value) })" />
      <button type="button" class="reset" @click="reset">{{ t('charts.reset_view') }}</button>
    </div>
  </ChartFrame>
</template>

<style scoped>
.scene {
  position: relative;
  min-inline-size: 0;
  border-radius: 0.5rem;
  cursor: grab;
}
.scene:active {
  cursor: grabbing;
}
.scene:focus-visible {
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
.floor {
  fill: var(--chart-hover);
  stroke: var(--chart-grid);
}
.wall {
  fill: var(--chart-hover);
  fill-opacity: 0.45;
  stroke: var(--chart-grid);
}
.rules line {
  stroke: var(--chart-grid);
  stroke-width: 1;
}
.rules line.zero {
  stroke: var(--chart-axis);
}
.axis-text text {
  fill: var(--chart-axis-text);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.column polygon {
  stroke: var(--chart-surface);
  stroke-width: 0.8;
  stroke-linejoin: round;
  transition: opacity 0.15s;
}
.column.dim polygon {
  opacity: 0.45;
}
.column.on polygon {
  stroke: var(--p-text-color);
  stroke-width: 1;
}
.controls {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem 0.8rem;
  align-items: center;
  margin-block-start: 0.5rem;
  color: var(--p-text-muted-color);
  font-size: 0.82rem;
}
.controls input[type='range'] {
  inline-size: 7rem;
  accent-color: var(--p-primary-color);
}
.reset {
  padding: 0.1rem 0.5rem;
  font: inherit;
  color: inherit;
  cursor: pointer;
  background: none;
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.4rem;
}
.reset:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 1px;
}
.sr-only {
  position: absolute;
  inline-size: 1px;
  block-size: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
@media print {
  .controls {
    display: none;
  }
}
</style>
