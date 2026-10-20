<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { LegendItem } from './types'

// The legend of a chart: a button per series that shows or hides it (so the
// colour is never the only way to tell series apart: the name is always here).
// Swatches mirror the mark: a square for bars and areas, a short stroke for lines.
defineProps<{ items: LegendItem[]; toggle?: boolean }>()
defineEmits<{ toggle: [key: string] }>()
const { t } = useI18n()
</script>

<template>
  <ul class="legend" :aria-label="t('charts.legend')">
    <li v-for="it in items" :key="it.key">
      <component
        :is="toggle ? 'button' : 'span'"
        class="item"
        :class="{ off: it.hidden }"
        :type="toggle ? 'button' : undefined"
        :aria-pressed="toggle ? !it.hidden : undefined"
        @click="toggle && $emit('toggle', it.key)"
      >
        <svg class="key" width="16" height="10" viewBox="0 0 16 10" aria-hidden="true">
          <line v-if="it.line" x1="0" y1="5" x2="16" y2="5" :stroke="it.color" stroke-width="2.5" stroke-linecap="round" :stroke-dasharray="it.dashed ? '4 3' : undefined" />
          <rect v-else x="1" y="0" width="10" height="10" rx="2" :fill="it.color" />
        </svg>
        <span class="label">{{ it.label }}</span>
        <span v-if="it.detail" class="detail">{{ it.detail }}</span>
      </component>
    </li>
  </ul>
</template>

<style scoped>
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 0.25rem 0.9rem;
  margin: 0 0 0.5rem;
  padding: 0;
  list-style: none;
  font-size: 0.85rem;
}
.item {
  display: inline-flex;
  gap: 0.4rem;
  align-items: center;
  padding: 0.15rem 0.25rem;
  font: inherit;
  color: var(--p-text-color);
  background: none;
  border: 0;
  border-radius: 0.35rem;
}
button.item {
  cursor: pointer;
}
button.item:hover {
  background: var(--p-content-hover-background, rgb(128 128 128 / 0.12));
}
button.item:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 1px;
}
.key {
  flex: none;
}
.off {
  color: var(--p-text-muted-color);
  text-decoration: line-through;
}
.off .key {
  opacity: 0.35;
}
.detail {
  color: var(--p-text-muted-color);
  font-variant-numeric: tabular-nums;
}
</style>
