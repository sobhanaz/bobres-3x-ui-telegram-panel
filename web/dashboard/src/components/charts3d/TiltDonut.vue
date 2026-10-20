<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ChartFrame from '../charts/ChartFrame.vue'
import ChartTooltip from '../charts/ChartTooltip.vue'
import { sliceAngles, truncate } from '../charts/scale'
import { seriesColor } from '../charts/palette'
import { byMouse, keyStep, stepIndex, uid, useReducedMotion, useRtl } from '../charts/useChart'
import type { ChartTableData, DonutSlice, TooltipData, ValueFormat } from '../charts/types'
import { backWall, donutPoint, frontWall, ringTop } from './projection'

// A thick donut that leans back: the parts of a whole as slices of a ring with
// a visible edge. Drag it (or use the two sliders) to turn it and tilt it; at
// the steepest tilt it is the flat donut. The list beside it writes every part
// with its value and share, so the 3D view adds depth, never information. SVG
// from projection.ts, no WebGL. See README.md for the flat DonutChart's props,
// which this one shares.
const props = withDefaults(
  defineProps<{
    title: string
    subtitle?: string
    slices: DonutSlice[]
    format: ValueFormat
    percent: (pct: number) => string
    centerFormat?: ValueFormat
    size?: number
    centerLabel?: string
    rtl?: boolean
    loading?: boolean
    emptyText?: string
    corner?: string
    summary?: string
    /** no entrance motion (the person's reduced-motion setting does the same) */
    still?: boolean
  }>(),
  { size: 280, rtl: undefined },
)
const { t } = useI18n()
const rtl = useRtl(() => props.rtl)
const reduced = useReducedMotion()
const hintId = uid('chart-hint')

const HOME = { rotation: 0, pitch: 56 }
const view = ref({ rotation: 0, pitch: 90 })
let raf = 0
onMounted(() => {
  if (props.still || reduced.value || typeof requestAnimationFrame !== 'function') {
    view.value = { ...HOME }
    return
  }
  // The donut leans back into place once.
  const t0 = performance.now()
  const step = (now: number) => {
    const k = Math.min(1, (now - t0) / 700)
    view.value = { ...view.value, pitch: 90 + (HOME.pitch - 90) * (1 - (1 - k) ** 3) }
    if (k < 1) raf = requestAnimationFrame(step)
  }
  raf = requestAnimationFrame(step)
})
onBeforeUnmount(() => cancelAnimationFrame(raf))
const setView = (v: Partial<typeof HOME>) => {
  cancelAnimationFrame(raf)
  view.value = { rotation: v.rotation ?? view.value.rotation, pitch: Math.min(86, Math.max(28, v.pitch ?? view.value.pitch)) }
}
const reset = () => setView(HOME)
let drag: { x: number; y: number; v: typeof HOME } | null = null
function down(e: PointerEvent) {
  drag = { x: e.clientX, y: e.clientY, v: { ...view.value } }
  ;(e.currentTarget as Element).setPointerCapture?.(e.pointerId)
}
function move(e: PointerEvent) {
  if (drag && e.buttons) {
    const dir = rtl.value ? -1 : 1
    setView({ rotation: drag.v.rotation + dir * (e.clientX - drag.x) * 0.6, pitch: drag.v.pitch - (e.clientY - drag.y) * 0.3 })
  }
}
const up = () => (drag = null)

const parts = computed(() => props.slices.filter((s) => s.value > 0))
const total = computed(() => parts.value.reduce((n, s) => n + s.value, 0))
const angles = computed(() => sliceAngles(parts.value.map((s) => s.value)))
const colorOf = (s: DonutSlice, i: number) => seriesColor(s.slot, i)
const pct = (v: number) => (total.value > 0 ? (v * 100) / total.value : 0)

const R1 = computed(() => props.size / 2 - 8)
const R0 = computed(() => R1.value * 0.56)
const thick = computed(() => R1.value * 0.2)
const wallPx = computed(() => thick.value * Math.cos((view.value.pitch * Math.PI) / 180))
const H = computed(() => Math.round(props.size * 0.8))
const cx = computed(() => props.size / 2)
const cy = computed(() => H.value / 2 - wallPx.value / 2)
const active = ref<number | null>(null)

const shapes = computed(() =>
  parts.value.map((s, i) => {
    const [a0, a1] = angles.value[i]
    const lift = i === active.value ? -7 : 0
    return {
      s,
      i,
      color: colorOf(s, i),
      lift,
      top: ringTop(cx.value, cy.value, R0.value, R1.value, a0, a1, view.value),
      outer: frontWall(cx.value, cy.value, R1.value, a0, a1, view.value, wallPx.value),
      inner: backWall(cx.value, cy.value, R0.value, a0, a1, view.value, wallPx.value),
    }
  }),
)
// The shadow on the table is the ring's own shape, lower and softer: a plate under the hole would be wrong.
const shadow = computed(() => ringTop(cx.value, cy.value, R0.value, R1.value, 0, Math.PI * 2, view.value, wallPx.value + 7))

function onKey(e: KeyboardEvent) {
  const step = keyStep(e, rtl.value)
  if (step === null) return
  e.preventDefault()
  active.value = stepIndex(active.value, step, parts.value.length)
}
const onFocus = () => {
  if (active.value === null && parts.value.length) active.value = 0
}
const tip = computed<TooltipData | null>(() => {
  const i = active.value
  const s = i === null ? undefined : parts.value[i]
  if (i === null || !s) return null
  return { title: s.label, rows: [{ key: s.key, label: props.percent(pct(s.value)), value: props.format(s.value), color: colorOf(s, i) }] }
})
const tipPos = computed(() => {
  const i = active.value
  if (i === null) return { x: 0, y: 0 }
  const mid = (angles.value[i][0] + angles.value[i][1]) / 2
  const p = donutPoint(cx.value, cy.value, (R0.value + R1.value) / 2, mid, view.value)
  return { x: rtl.value ? props.size - p.x : p.x, y: p.y - 10 }
})
const live = computed(() => (tip.value ? `${tip.value.title}: ${tip.value.rows[0].value}, ${tip.value.rows[0].label}` : ''))
const centre = computed(() => {
  const i = active.value
  const s = i === null ? undefined : parts.value[i]
  const fmt = props.centerFormat ?? props.format
  return s ? { top: fmt(s.value), bottom: truncate(s.label, 16) } : { top: fmt(total.value), bottom: truncate(props.centerLabel ?? t('charts.total'), 16) }
})
const centreSize = computed(() => Math.max(9, Math.min(15, (R0.value * 1.7) / (Math.max(4, centre.value.top.length) * 0.6))))
const table = computed<ChartTableData>(() => ({
  corner: props.corner ?? '',
  columns: [t('charts.value'), t('charts.share')],
  rows: parts.value.map((s) => ({ label: s.label, cells: [props.format(s.value), props.percent(pct(s.value))] })),
}))
const rotId = uid('rot')
const tiltId = uid('tilt')
</script>

<template>
  <ChartFrame class="tilt-donut" :title="title" :subtitle="subtitle" :loading="loading" :empty="parts.length === 0" :empty-text="emptyText" :height="H" :table="table" :live="live">
    <div class="donut">
      <div class="stage">
        <div
          class="ring"
          tabindex="0"
          role="group"
          aria-roledescription="chart"
          :aria-label="summary ?? title"
          :aria-describedby="hintId"
          :style="{ inlineSize: size + 'px', blockSize: H + 'px' }"
          @keydown="onKey"
          @focus="onFocus"
          @blur="active = null"
          @pointerleave="(e: PointerEvent) => byMouse(e) && (active = null)"
        >
          <svg :width="size" :height="H" :viewBox="`0 0 ${size} ${H}`" aria-hidden="true" @pointerdown="down" @pointermove="move" @pointerup="up" @pointercancel="up">
            <path class="shadow" :d="shadow" fill-rule="evenodd" />
            <g :transform="rtl ? `translate(${size},0) scale(-1,1)` : undefined">
              <g v-for="sh in shapes" :key="'i' + sh.s.key" :transform="`translate(0,${sh.lift})`">
                <path v-if="sh.inner" class="wall inner" :d="sh.inner" :fill="`color-mix(in srgb, ${sh.color}, black 40%)`" />
              </g>
              <g v-for="sh in shapes" :key="'o' + sh.s.key" :transform="`translate(0,${sh.lift})`">
                <path v-if="sh.outer" class="wall" :d="sh.outer" :fill="`color-mix(in srgb, ${sh.color}, black 24%)`" />
              </g>
              <g v-for="sh in shapes" :key="'t' + sh.s.key" :transform="`translate(0,${sh.lift})`">
                <path class="top" :d="sh.top" fill-rule="evenodd" :fill="`color-mix(in srgb, ${sh.color}, white 12%)`" @pointerenter="active = sh.i" @pointerdown="active = sh.i" />
              </g>
            </g>
          </svg>
          <div class="centre" :style="{ insetBlockStart: cy + 'px' }" aria-hidden="true">
            <div class="centre-value" :style="{ fontSize: centreSize + 'px' }">{{ centre.top }}</div>
            <div class="centre-label">{{ centre.bottom }}</div>
          </div>
          <ChartTooltip v-if="tip" :data="tip" :x="tipPos.x" :y="tipPos.y" :box-width="size" />
          <span :id="hintId" class="sr-only">{{ t('charts.keys_hint') }}</span>
        </div>
        <div class="controls">
          <label :for="rotId">{{ t('charts.rotate') }}</label>
          <input :id="rotId" type="range" min="-180" max="180" step="1" :value="Math.round(view.rotation)" @input="setView({ rotation: Number(($event.target as HTMLInputElement).value) })" />
          <label :for="tiltId">{{ t('charts.tilt') }}</label>
          <input :id="tiltId" type="range" min="28" max="86" step="1" :value="Math.round(view.pitch)" @input="setView({ pitch: Number(($event.target as HTMLInputElement).value) })" />
          <button type="button" class="reset" @click="reset">{{ t('charts.reset_view') }}</button>
        </div>
      </div>
      <ul class="parts">
        <li v-for="(s, i) in parts" :key="s.key" :class="{ on: active === i }" @pointerenter="active = i" @pointerleave="active = null">
          <svg class="key" width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><rect width="12" height="12" rx="3" :fill="colorOf(s, i)" /></svg>
          <span class="name">{{ s.label }}</span>
          <span class="nums">
            <span class="value">{{ format(s.value) }}</span>
            <span class="share">{{ percent(pct(s.value)) }}</span>
          </span>
        </li>
      </ul>
    </div>
  </ChartFrame>
</template>

<style scoped>
.donut {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem 1.5rem;
  align-items: center;
}
.stage {
  flex: none;
  max-inline-size: 100%;
}
.ring {
  position: relative;
  max-inline-size: 100%;
  cursor: grab;
  border-radius: 0.5rem;
}
.ring:active {
  cursor: grabbing;
}
.ring:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 3px;
}
svg {
  display: block;
  overflow: visible;
  touch-action: pan-y;
}
.shadow {
  fill: #000;
  fill-opacity: 0.1;
}
.top,
.wall {
  stroke: var(--chart-surface);
  stroke-width: 1.5;
  stroke-linejoin: round;
}
.top {
  cursor: pointer;
}
.centre {
  position: absolute;
  inset-inline: 0;
  text-align: center;
  pointer-events: none;
  transform: translateY(-50%);
}
.centre-value {
  color: var(--p-text-color);
  font-size: 0.95rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.centre-label {
  max-inline-size: 7rem;
  margin-inline: auto;
  overflow: hidden;
  color: var(--p-text-muted-color);
  font-size: 0.75rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.controls {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem 0.8rem;
  align-items: center;
  margin-block-start: 0.4rem;
  color: var(--p-text-muted-color);
  font-size: 0.82rem;
}
.controls input[type='range'] {
  inline-size: 6rem;
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
.parts {
  display: grid;
  flex: 1 1 14rem;
  gap: 0.2rem;
  min-inline-size: 0;
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: 0.88rem;
}
.parts li {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  gap: 0.2rem 0.6rem;
  align-items: center;
  padding: 0.25rem 0.4rem;
  border-radius: 0.4rem;
}
.parts li.on {
  background: var(--chart-hover);
}
.name {
  overflow-wrap: anywhere;
}
.nums {
  display: grid;
  justify-items: end;
  line-height: 1.35;
}
.value {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.share {
  color: var(--p-text-muted-color);
  font-size: 0.82rem;
  font-variant-numeric: tabular-nums;
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
