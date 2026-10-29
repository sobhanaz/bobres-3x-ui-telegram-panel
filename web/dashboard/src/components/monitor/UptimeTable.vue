<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import Tag from 'primevue/tag'
import { availabilityLevel, cellState, observedShare, type Availability, type CellState, type UptimeComponent, type UptimeReply, type UptimeWindow } from '../../monitor'
import { useFmt } from './feed'

// The rows of the Uptime section: a service or a panel server each, with its availability over the chosen window, its
// outages and a bar of cells (one for each stretch of the period). A cell with nothing observed is hatched: no data.
const props = defineProps<{ components: UptimeComponent[]; service: boolean; uptime: UptimeReply; period: UptimeWindow }>()
const { t, te } = useI18n()
const { at, n, dur, avail } = useFmt()

interface Cell {
  state: CellState
  title: string
}
interface Row {
  id: string
  name: string
  off: boolean
  a: Availability
  cells: Cell[]
}
const nameOf = (c: UptimeComponent) => (props.service && te('monitor.component.' + c.id) ? t('monitor.component.' + c.id) : c.name || c.id)
const bucketS = computed(() => props.uptime.windows.find((w) => w.name === props.period)?.bucket_s ?? 0)
const rows = computed<Row[]>(() =>
  props.components.map((c) => {
    const a = c.windows[props.period]
    return { id: c.id, name: nameOf(c), off: c.enabled === false, a, cells: a.bar.map((cell, i) => ({ state: cellState(cell), title: cellTitle(a, i) })) }
  }),
)

const percent = (a: Availability) => (a.availability_pct === null ? '' : avail(a.availability_pct))
const outagesText = (a: Availability) => t('monitor.uptime.outages', { n: n(a.outages), time: dur(a.down_s) }, a.outages)
/** the row in a sentence, for the bar: a screen reader gets this instead of sixty cells */
function summary(r: Row): string {
  const head = `${r.name}, ${t('monitor.range.' + props.period)}`
  if (r.a.availability_pct === null) return `${head}: ${t('monitor.uptime.no_data')}`
  return `${head}: ${percent(r.a)}, ${r.a.outages > 0 ? outagesText(r.a) : t('monitor.uptime.no_outages')}`
}
/** a cell's tooltip: when it starts and how it went */
function cellTitle(a: Availability, i: number): string {
  const [observed, down] = a.bar[i]
  const start = props.uptime.generated_at - a.window_s + i * bucketS.value
  const state = cellState([observed, down])
  const what =
    state === 'nodata'
      ? t('monitor.uptime.no_data')
      : state === 'up'
        ? t('monitor.uptime.legend.up')
        : t('monitor.uptime.cell_down', { time: dur(down), share: avail(((observed - down) / observed) * 100) })
  return `${at(start)}: ${what}`
}
</script>

<template>
  <DataTable :value="rows" data-key="id" size="small" scrollable class="uptime-table">
    <Column :header="t('monitor.uptime.col_name')">
      <template #body="{ data }">
        <bdi class="name">{{ data.name }}</bdi>
        <Tag v-if="data.off" severity="secondary" :value="t('monitor.uptime.off')" class="off-tag" />
      </template>
    </Column>
    <Column :header="t('monitor.uptime.col_availability')">
      <template #body="{ data }">
        <span v-if="data.a.availability_pct === null" class="no-data app-muted">{{ t('monitor.uptime.no_data') }}</span>
        <template v-else>
          <strong class="pct" :class="'level-' + availabilityLevel(data.a.availability_pct)">{{ percent(data.a) }}</strong>
          <div v-if="observedShare(data.a) < 99.5" class="app-muted small observed">{{ t('monitor.uptime.observed', { time: dur(data.a.observed_s), window: dur(data.a.window_s) }) }}</div>
        </template>
      </template>
    </Column>
    <Column :header="t('monitor.uptime.col_outages')">
      <template #body="{ data }">
        <span v-if="data.a.availability_pct === null" class="app-muted">—</span>
        <span v-else-if="data.a.outages === 0" class="no-outages">{{ t('monitor.uptime.no_outages') }}</span>
        <template v-else>
          <span class="outages">{{ outagesText(data.a) }}</span>
          <div class="app-muted small longest">{{ t('monitor.uptime.longest', { time: dur(data.a.longest_s) }) }}</div>
        </template>
      </template>
    </Column>
    <Column :header="t('monitor.uptime.col_bar')" class="bar-col">
      <template #body="{ data }">
        <div class="bar" role="img" :aria-label="summary(data)" :data-id="data.id">
          <span v-if="!data.cells.length" class="app-muted small">{{ t('monitor.uptime.no_data') }}</span>
          <span v-for="(c, i) in data.cells" :key="i" class="cell" :class="c.state" :title="c.title" aria-hidden="true" />
        </div>
      </template>
    </Column>
  </DataTable>
</template>

<style scoped>
.small {
  font-size: 0.8rem;
}
.pct {
  font-size: 1.05rem;
}
.level-ok {
  color: var(--p-green-600);
}
.level-warn {
  color: var(--p-orange-600);
}
.level-down {
  color: var(--p-red-600);
}
.off-tag {
  margin-inline-start: 0.5rem;
}
.bar-col {
  min-inline-size: 14rem;
}
.bar {
  display: flex;
  align-items: stretch;
  gap: 1px;
  block-size: 1.5rem;
  min-inline-size: 12rem;
}
.cell {
  flex: 1 1 0;
  min-inline-size: 2px;
  border-radius: 1px;
}
.cell.up {
  background: var(--p-green-500);
}
.cell.partial {
  background: var(--p-orange-500);
}
.cell.down {
  background: var(--p-red-500);
}
/* no data is hatched grey: neither the green of up nor the red of down, and not a colour alone */
.cell.nodata {
  background: repeating-linear-gradient(135deg, var(--p-surface-400) 0 2px, transparent 2px 4px);
  outline: 1px solid var(--p-content-border-color);
  outline-offset: -1px;
}
</style>
