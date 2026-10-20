<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ChartFrame from './ChartFrame.vue'
import ChartTooltip from './ChartTooltip.vue'
import { arcPath, polar, sliceAngles, truncate } from './scale'
import { seriesColor } from './palette'
import { byMouse, keyStep, stepIndex, uid, useRtl } from './useChart'
import type { ChartTableData, DonutSlice, TooltipData, ValueFormat } from './types'

// A donut: the parts of a whole. The list beside it writes every part's name,
// value and share (so the colours are never the only key), the centre shows the
// total or, while a part is hovered or focused, that part. Slices run in the
// reading direction (clockwise in English, anticlockwise in Persian). Parts with
// no value are left out; more than seven parts should be folded into "other" by
// the caller. See README.md for the props.
const props = withDefaults(
  defineProps<{
    title: string
    subtitle?: string
    slices: DonutSlice[]
    format: ValueFormat
    /** a share of the whole as text: percent(0..100) */
    percent: (pct: number) => string
    /** the total in the centre, short enough for the hole (default: format) */
    centerFormat?: ValueFormat
    /** the size of the ring in px */
    size?: number
    /** the word under the total in the centre (default: Total) */
    centerLabel?: string
    rtl?: boolean
    loading?: boolean
    emptyText?: string
    corner?: string
    summary?: string
  }>(),
  { size: 200, rtl: undefined },
)
const { t } = useI18n()
const rtl = useRtl(() => props.rtl)
const hintId = uid('chart-hint')

const parts = computed(() => props.slices.filter((s) => s.value > 0))
const total = computed(() => parts.value.reduce((n, s) => n + s.value, 0))
const angles = computed(() => sliceAngles(parts.value.map((s) => s.value)))
const S = computed(() => props.size)
const c = computed(() => S.value / 2)
const R1 = computed(() => S.value / 2 - 6)
const R0 = computed(() => R1.value * 0.62)
const colorOf = (s: DonutSlice, i: number) => seriesColor(s.slot, i)
const pct = (v: number) => (total.value > 0 ? (v * 100) / total.value : 0)

const active = ref<number | null>(null)
const shapes = computed(() =>
  parts.value.map((s, i) => {
    const [a0, a1] = angles.value[i]
    const mid = (a0 + a1) / 2
    const out = i === active.value ? 5 : 0
    const dx = Math.sin(mid) * out
    const dy = -Math.cos(mid) * out
    return { s, i, d: arcPath(c.value, c.value, R0.value, R1.value, a0, a1), color: colorOf(s, i), dx, dy, mid }
  }),
)

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
  const mid = angles.value[i].reduce((a, b) => a + b, 0) / 2
  const p = polar(c.value, c.value, (R0.value + R1.value) / 2, rtl.value ? -mid : mid)
  return { x: p.x, y: p.y }
})
const live = computed(() => (tip.value ? `${tip.value.title}: ${tip.value.rows[0].value}, ${tip.value.rows[0].label}` : ''))
const centre = computed(() => {
  const i = active.value
  const s = i === null ? undefined : parts.value[i]
  const fmt = props.centerFormat ?? props.format
  return s ? { top: fmt(s.value), bottom: truncate(s.label, 16) } : { top: fmt(total.value), bottom: truncate(props.centerLabel ?? t('charts.total'), 16) }
})
// The text must fit the hole: the longer the number, the smaller it is written.
const centreSize = computed(() => Math.max(9, Math.min(15, (R0.value * 1.7) / (Math.max(4, centre.value.top.length) * 0.6))))
const table = computed<ChartTableData>(() => ({
  corner: props.corner ?? '',
  columns: [t('charts.value'), t('charts.share')],
  rows: parts.value.map((s) => ({ label: s.label, cells: [props.format(s.value), props.percent(pct(s.value))] })),
}))
</script>

<template>
  <ChartFrame class="donut-chart" :title="title" :subtitle="subtitle" :loading="loading" :empty="parts.length === 0" :empty-text="emptyText" :height="size" :table="table" :live="live">
    <div class="donut">
      <div
        class="ring"
        tabindex="0"
        role="group"
        aria-roledescription="chart"
        :aria-label="summary ?? title"
        :aria-describedby="hintId"
        :style="{ inlineSize: size + 'px', blockSize: size + 'px' }"
        @keydown="onKey"
        @focus="onFocus"
        @blur="active = null"
        @pointerleave="(e: PointerEvent) => byMouse(e) && (active = null)"
      >
        <svg :width="size" :height="size" :viewBox="`0 0 ${size} ${size}`" aria-hidden="true">
          <g :transform="rtl ? `translate(${size},0) scale(-1,1)` : undefined">
            <path
              v-for="sh in shapes"
              :key="sh.s.key"
              class="slice"
              :d="sh.d"
              :fill="sh.color"
              fill-rule="evenodd"
              stroke="var(--chart-surface)"
              stroke-width="2"
              :transform="`translate(${sh.dx.toFixed(2)},${sh.dy.toFixed(2)})`"
              @pointerenter="active = sh.i"
              @pointerdown="active = sh.i"
            />
          </g>
          <text class="centre-value" :x="c" :y="c - 3" text-anchor="middle" :style="{ fontSize: centreSize + 'px' }">{{ centre.top }}</text>
          <text class="centre-label" :x="c" :y="c + 16" text-anchor="middle">{{ centre.bottom }}</text>
        </svg>
        <ChartTooltip v-if="tip" :data="tip" :x="tipPos.x" :y="tipPos.y" :box-width="size" />
        <span :id="hintId" class="sr-only">{{ t('charts.keys_hint') }}</span>
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
.ring {
  position: relative;
  flex: none;
  max-inline-size: 100%;
  border-radius: 50%;
}
.ring:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 3px;
}
svg {
  display: block;
  overflow: visible;
}
.slice {
  cursor: pointer;
  transition: transform 0.15s;
}
.centre-value {
  fill: var(--p-text-color);
  font-size: 15px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.centre-label {
  fill: var(--chart-axis-text);
  font-size: 12px;
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
</style>
