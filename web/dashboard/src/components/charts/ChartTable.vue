<script setup lang="ts">
import type { ChartTableData } from './types'

// The numbers of a chart as a table: the alternative for screen readers and for
// anyone who wants the exact values. The scroll area can be focused and moved
// with the keyboard, and the first column stays in view while it scrolls.
defineProps<{ data: ChartTableData; caption: string }>()
</script>

<template>
  <div class="table-scroll" tabindex="0" role="region" :aria-label="caption">
    <table class="chart-table">
      <caption class="sr-only">{{ caption }}</caption>
      <thead>
        <tr>
          <th scope="col">{{ data.corner }}</th>
          <th v-for="c in data.columns" :key="c" scope="col">{{ c }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(r, i) in data.rows" :key="i">
          <th scope="row">{{ r.label }}</th>
          <td v-for="(c, j) in r.cells" :key="j">{{ c }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.table-scroll {
  max-block-size: 18rem;
  overflow: auto;
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.5rem;
}
.table-scroll:focus-visible {
  outline: 2px solid var(--p-primary-color);
}
.chart-table {
  inline-size: 100%;
  font-size: 0.85rem;
  border-collapse: separate;
  border-spacing: 0;
}
th,
td {
  padding: 0.4rem 0.7rem;
  text-align: start;
  white-space: nowrap;
  border-block-end: 1px solid var(--p-content-border-color);
}
thead th {
  position: sticky;
  inset-block-start: 0;
  z-index: 2;
  background: var(--p-content-background);
}
tbody th {
  position: sticky;
  inset-inline-start: 0;
  z-index: 1;
  font-weight: 500;
  background: var(--p-content-background);
}
thead th:first-child {
  inset-inline-start: 0;
  z-index: 3;
}
td {
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
