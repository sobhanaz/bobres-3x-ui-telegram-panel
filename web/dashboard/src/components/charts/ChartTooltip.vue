<script setup lang="ts">
import { computed } from 'vue'
import type { TooltipData } from './types'

// The readout beside the pointer or the focused point. It is a sighted-user
// aid only (aria-hidden): the same words go to the chart's live region, and the
// same numbers are in the table view, so nothing is reachable by hover alone.
// Every text is shown with textContent (Vue's interpolation), never as HTML.
const props = defineProps<{
  data: TooltipData
  /** the anchor in px inside the plot box (physical, left to right) */
  x: number
  y: number
  /** the width of the box it may use */
  boxWidth: number
}>()

const WIDTH = 220 // about the widest a tooltip gets; it only decides which side it opens on
const flip = computed(() => props.x + 14 + WIDTH > props.boxWidth && props.x - 14 - WIDTH >= 0)
const style = computed(() => ({
  insetBlockStart: Math.max(0, props.y) + 'px',
  left: (flip.value ? props.x - 14 : props.x + 14) + 'px',
  transform: flip.value ? 'translateX(-100%)' : undefined,
}))
</script>

<template>
  <div class="tip" :style="style" aria-hidden="true">
    <div class="title">{{ data.title }}</div>
    <div v-for="r in data.rows" :key="r.key" class="row">
      <svg class="key" width="14" height="8" viewBox="0 0 14 8" aria-hidden="true">
        <line x1="0" y1="4" x2="14" y2="4" :stroke="r.color" stroke-width="3" stroke-linecap="round" :stroke-dasharray="r.dashed ? '3 2.5' : undefined" />
      </svg>
      <span class="value">{{ r.value }}</span>
      <span class="label">{{ r.label }}</span>
    </div>
    <div v-if="data.note" class="note">{{ data.note }}</div>
  </div>
</template>

<style scoped>
.tip {
  position: absolute;
  z-index: 3;
  max-inline-size: 16rem;
  padding: 0.45rem 0.65rem;
  font-size: 0.82rem;
  line-height: 1.5;
  pointer-events: none;
  color: var(--p-text-color);
  background: var(--chart-surface);
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.5rem;
  box-shadow: 0 4px 14px rgb(0 0 0 / 0.16);
}
.title {
  margin-block-end: 0.15rem;
  font-weight: 600;
}
.row {
  display: flex;
  gap: 0.45rem;
  align-items: center;
}
.value {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.label {
  color: var(--p-text-muted-color);
}
.note {
  margin-block-start: 0.2rem;
  color: var(--p-text-muted-color);
}
</style>
