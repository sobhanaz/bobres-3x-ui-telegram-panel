<script setup lang="ts">
import { computed } from 'vue'
import { areaPath, barPath, linePath, linear, pointX } from './scale'
import { seriesColor } from './palette'
import { useRtl } from './useChart'

// A word-sized trend line or bar strip for a card or a table row. It is one
// image with a text label (the label should say the trend in words: the picture
// carries no numbers of its own). See README.md.
const props = withDefaults(
  defineProps<{
    values: number[]
    /** what it shows, in words, for screen readers */
    label: string
    kind?: 'line' | 'bar'
    colorSlot?: number | 'other'
    width?: number
    height?: number
    rtl?: boolean
  }>(),
  { kind: 'line', width: 96, height: 28, rtl: undefined },
)
const rtl = useRtl(() => props.rtl)
const color = computed(() => seriesColor(props.colorSlot, 0))
const pad = 4
const n = computed(() => props.values.length)
const range = computed(() => {
  const lo = Math.min(0, ...props.values)
  const hi = Math.max(0, ...props.values)
  return { lo, hi: hi === lo ? lo + 1 : hi }
})
const y = computed(() => linear(range.value.lo, range.value.hi, props.height - pad, pad))
const X = (i: number) => {
  const x = pointX(i, n.value, pad, props.width - pad)
  return rtl.value ? props.width - x : x
}
const pts = computed(() => props.values.map((v, i) => ({ x: X(i), y: y.value(v) })))
const line = computed(() => linePath(pts.value))
const area = computed(() => {
  const zero = y.value(0)
  return areaPath(pts.value, pts.value.map((p) => ({ x: p.x, y: zero })))
})
const bars = computed(() => {
  const size = (props.width - 2 * pad) / Math.max(1, n.value)
  const w = Math.max(1.5, Math.min(8, size - 1))
  return props.values.map((v, i) => {
    const cx = X(i)
    const top = y.value(Math.max(0, v))
    return { d: barPath(cx - w / 2, top, w, Math.max(1, y.value(0) - top), 2, 'top'), last: i === n.value - 1 }
  })
})
const end = computed(() => pts.value[pts.value.length - 1])
</script>

<template>
  <svg class="spark" :width="width" :height="height" :viewBox="`0 0 ${width} ${height}`" role="img" :aria-label="label">
    <line class="base" :x1="pad" :x2="width - pad" :y1="y(0)" :y2="y(0)" />
    <template v-if="n > 0">
      <template v-if="kind === 'bar'">
        <path v-for="(b, i) in bars" :key="i" :d="b.d" :fill="color" :opacity="b.last ? 1 : 0.55" />
      </template>
      <template v-else>
        <path v-if="n > 1" :d="area" :fill="color" fill-opacity="0.12" />
        <path v-if="n > 1" :d="line" fill="none" :stroke="color" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
        <circle :cx="end.x" :cy="end.y" r="3.5" :fill="color" stroke="var(--chart-surface)" stroke-width="2" />
      </template>
    </template>
  </svg>
</template>

<style scoped>
.spark {
  display: block;
  flex: none;
  overflow: visible;
}
.base {
  stroke: var(--chart-grid);
  stroke-width: 1;
}
</style>
