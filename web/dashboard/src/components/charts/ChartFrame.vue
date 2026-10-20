<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ChartLegend from './ChartLegend.vue'
import ChartTable from './ChartTable.vue'
import type { ChartTableData, LegendItem } from './types'

// The frame every chart of the kit sits in: a figure with a title, the legend,
// the loading and empty states, a button that swaps the chart for its table, and
// a live region that says what the focused point is. A chart puts its drawing in
// the default slot.
const props = defineProps<{
  title: string
  subtitle?: string
  /** data is on its way and there is none to show yet: a placeholder of the chart's height */
  loading?: boolean
  /** there is nothing to draw */
  empty?: boolean
  emptyText?: string
  /** the drawing's height in px (the placeholder and the empty state keep it) */
  height?: number
  /** a legend is shown for two series or more (one series is named by the title) */
  legend?: LegendItem[]
  legendToggle?: boolean
  /** the numbers as a table; left out, there is no table button */
  table?: ChartTableData | null
  /** what the focused or hovered point says, for screen readers */
  live?: string
}>()
defineEmits<{ toggle: [key: string] }>()
const { t } = useI18n()

const showTable = ref(false)
const state = computed(() => (props.empty ? (props.loading ? 'loading' : 'empty') : 'ready'))
const showLegend = computed(() => (props.legend?.length ?? 0) > 1)
const h = computed(() => (props.height ?? 240) + 'px')
</script>

<template>
  <figure class="chart-frame" :class="{ refreshing: loading && state === 'ready' }" :aria-busy="loading ? 'true' : undefined">
    <figcaption class="head">
      <span class="titles">
        <strong class="title">{{ title }}</strong>
        <span v-if="subtitle" class="subtitle">{{ subtitle }}</span>
      </span>
      <span class="actions">
        <slot name="actions" />
        <button v-if="table && state === 'ready'" type="button" class="table-btn" :aria-pressed="showTable" @click="showTable = !showTable">
          <i class="pi" :class="showTable ? 'pi-chart-bar' : 'pi-table'" aria-hidden="true" />
          {{ showTable ? t('charts.table_hide') : t('charts.table_show') }}
        </button>
      </span>
    </figcaption>

    <div v-if="state === 'loading'" class="skeleton" :style="{ blockSize: h }" role="status" :aria-label="t('charts.loading')" />
    <p v-else-if="state === 'empty'" class="empty" :style="{ minBlockSize: h }" role="status">{{ emptyText ?? t('charts.empty') }}</p>
    <template v-else>
      <ChartLegend v-if="showLegend" :items="legend!" :toggle="legendToggle" @toggle="(k: string) => $emit('toggle', k)" />
      <div v-show="!showTable" class="body"><slot /></div>
      <ChartTable v-if="showTable && table" :data="table" :caption="title" />
      <slot name="footer" />
    </template>
    <p class="sr-only" aria-live="polite" role="status">{{ live }}</p>
  </figure>
</template>

<style scoped>
.chart-frame {
  min-inline-size: 0;
  margin: 0;
}
.head {
  display: flex;
  flex-wrap: wrap;
  gap: 0.25rem 0.75rem;
  align-items: baseline;
  justify-content: space-between;
  margin-block-end: 0.5rem;
}
.titles {
  display: flex;
  flex-wrap: wrap;
  gap: 0 0.6rem;
  align-items: baseline;
  min-inline-size: 0;
}
.subtitle {
  color: var(--p-text-muted-color);
  font-size: 0.85rem;
}
.actions {
  display: inline-flex;
  gap: 0.5rem;
  align-items: center;
}
.table-btn {
  display: inline-flex;
  gap: 0.35rem;
  align-items: center;
  padding: 0.15rem 0.5rem;
  font: inherit;
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
  cursor: pointer;
  background: none;
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.4rem;
}
.table-btn:hover,
.table-btn[aria-pressed='true'] {
  color: var(--p-text-color);
  background: var(--p-content-hover-background, rgb(128 128 128 / 0.12));
}
.table-btn:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 1px;
}
.body {
  min-inline-size: 0;
  transition: opacity 0.2s;
}
/* The data is being refreshed: the previous drawing stays, dimmed, with no jump. */
.refreshing .body {
  opacity: 0.55;
}
.skeleton {
  background: linear-gradient(90deg, var(--p-content-border-color), transparent, var(--p-content-border-color));
  background-size: 200% 100%;
  border-radius: 0.5rem;
  opacity: 0.5;
  animation: chart-shimmer 1.4s linear infinite;
}
@keyframes chart-shimmer {
  to {
    background-position: -200% 0;
  }
}
.empty {
  display: grid;
  place-items: center;
  margin: 0;
  color: var(--p-text-muted-color);
  text-align: center;
  border: 1px dashed var(--p-content-border-color);
  border-radius: 0.5rem;
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
  .table-btn {
    display: none;
  }
}
</style>
