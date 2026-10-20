<script setup lang="ts">
import { computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import Card from 'primevue/card'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import Tag from 'primevue/tag'
import {
  boxHealth,
  boxHealthSeverity,
  boxState,
  boxStateSeverity,
  imageName,
  MAX_SERIES,
  orderServices,
  subjects,
  versionOf,
  type Box,
  type Overview,
} from '../../monitor'
import MetricChart from './MetricChart.vue'
import { feedKey, useFmt, type MetricDef } from './feed'

// Docker: the engine, every container (state, health, restarts, image version, CPU, memory, traffic) and
// what Docker keeps on the disk, with the CPU and memory of each service over the range.
const props = defineProps<{ overview: Overview | null }>()
const { t } = useI18n()
const { n, pct, size, dur, at } = useFmt()
const feed = inject(feedKey)!

const boxes = computed(() => props.overview?.containers ?? [])
const host = computed(() => props.overview?.host)
const now = computed(() => props.overview?.generated_at ?? 0)
const df = computed(() => host.value?.docker_disk ?? null)

// A service's colour is its place among the services that have a history, the same on both charts.
const services = computed(() => orderServices(subjects(feed.available.value, 'container.cpu_pct')))
const charted = computed(() => services.value.slice(0, MAX_SERIES))
const cpuDefs = computed<MetricDef[]>(() => charted.value.map((s, i) => ({ metric: 'container.cpu_pct:' + s, label: s, slot: i })))
const memDefs = computed<MetricDef[]>(() => charted.value.map((s, i) => ({ metric: 'container.mem_bytes:' + s, label: s, slot: i })))
const capped = computed(() => services.value.length > MAX_SERIES)

const upFor = (b: Box) => (b.state === 'running' && b.started_at > 0 && now.value > b.started_at ? dur(now.value - b.started_at) : '')
const memText = (b: Box) => (b.state === 'running' ? (b.mem_limit > 0 ? `${size(b.mem_bytes)} / ${size(b.mem_limit)}` : size(b.mem_bytes)) : '')
const memPct = (b: Box) => (b.mem_limit > 0 ? pct((b.mem_bytes / b.mem_limit) * 100) : '')
const dfRows = computed(() =>
  df.value
    ? ([
        ['images', df.value.images],
        ['reclaimable', df.value.images_reclaimable],
        ['containers', df.value.containers],
        ['volumes', df.value.volumes],
        ['build_cache', df.value.build_cache],
      ] as const)
    : [],
)
</script>

<template>
  <section id="monitor-docker" class="mon-section" tabindex="-1" aria-labelledby="monitor-docker-h">
    <h2 id="monitor-docker-h" class="mon-h">{{ t('monitor.section.docker') }}</h2>

    <p v-if="host?.available && !host.docker.ok" class="docker-down" role="alert">
      <i class="pi pi-exclamation-triangle" aria-hidden="true" />
      {{ t('monitor.host.docker_down') }}
      <span v-if="host.docker.error" class="app-muted app-ltr">{{ host.docker.error }}</span>
    </p>

    <Card class="containers-card">
      <template #title>{{ t('monitor.docker.containers') }}</template>
      <template #content>
        <p v-if="overview && !host?.available" class="app-muted no-data">{{ t('monitor.docker.needs_collector') }}</p>
        <p v-else-if="overview && !boxes.length" class="app-muted no-data">{{ t('monitor.docker.none') }}</p>
        <DataTable v-else :value="boxes" data-key="name" size="small" scrollable class="containers" :loading="!overview">
          <Column :header="t('monitor.docker.col_container')">
            <template #body="{ data }">
              <bdi class="app-ltr box-name">{{ data.service || data.name }}</bdi>
              <div v-if="data.service && data.name !== data.service" class="app-muted app-ltr small" dir="ltr">{{ data.name }}</div>
            </template>
          </Column>
          <Column :header="t('monitor.docker.col_version')">
            <template #body="{ data }">
              <span class="app-ltr version" dir="ltr">{{ versionOf(data.image) || '—' }}</span>
              <div class="app-muted app-ltr small" dir="ltr">{{ imageName(data.image) }}</div>
            </template>
          </Column>
          <Column :header="t('monitor.docker.col_state')">
            <template #body="{ data }">
              <Tag :severity="boxStateSeverity[boxState(data)]" :value="t('monitor.docker.state.' + boxState(data))" class="box-state" />
              <div v-if="data.oom_killed" class="small oom">{{ t('monitor.docker.oom') }}</div>
              <div v-if="data.exit_code !== null && data.state !== 'running'" class="small app-muted">{{ t('monitor.docker.exit', { code: n(data.exit_code) }) }}</div>
            </template>
          </Column>
          <Column :header="t('monitor.docker.col_health')">
            <template #body="{ data }">
              <Tag :severity="boxHealthSeverity[boxHealth(data)]" :value="t('monitor.docker.health.' + boxHealth(data))" class="box-health" />
            </template>
          </Column>
          <Column :header="t('monitor.docker.col_restarts')">
            <template #body="{ data }">
              <span :class="{ attention: data.restarts > 0 }">{{ n(data.restarts) }}</span>
            </template>
          </Column>
          <Column :header="t('monitor.docker.col_up')">
            <template #body="{ data }">{{ upFor(data) || '—' }}</template>
          </Column>
          <Column :header="t('monitor.docker.col_cpu')">
            <template #body="{ data }">{{ data.state === 'running' && data.cpu_pct !== null ? pct(data.cpu_pct) : '—' }}</template>
          </Column>
          <Column :header="t('monitor.docker.col_memory')">
            <template #body="{ data }">
              {{ memText(data) || '—' }}
              <div v-if="memPct(data)" class="app-muted small">{{ memPct(data) }}</div>
            </template>
          </Column>
          <Column :header="t('monitor.docker.col_network')">
            <template #body="{ data }">
              <span class="net">
                <span><i class="pi pi-arrow-down" aria-hidden="true" /><span class="sr-only">{{ t('monitor.host.rx') }}</span> {{ size(data.net_rx_bytes) }}</span>
                <span><i class="pi pi-arrow-up" aria-hidden="true" /><span class="sr-only">{{ t('monitor.host.tx') }}</span> {{ size(data.net_tx_bytes) }}</span>
              </span>
            </template>
          </Column>
        </DataTable>
      </template>
    </Card>

    <Card v-if="df" class="docker-disk-card">
      <template #title>{{ t('monitor.docker.disk') }}</template>
      <template #content>
        <dl class="facts">
          <div v-for="[k, v] in dfRows" :key="k">
            <dt>{{ t('monitor.docker.disk_' + k) }}</dt>
            <dd>{{ size(v) }}</dd>
          </div>
        </dl>
        <p class="app-muted small">{{ t('monitor.docker.disk_at', { time: at(df.collected_at) }) }}</p>
      </template>
    </Card>

    <div v-if="charted.length" class="charts">
      <MetricChart :title="t('monitor.docker.cpu_chart')" :subtitle="t('monitor.docker.cpu_note')" :defs="cpuDefs" unit="percent" />
      <MetricChart :title="t('monitor.docker.mem_chart')" :defs="memDefs" unit="bytes" />
    </div>
    <p v-if="capped" class="app-muted small capped">{{ t('monitor.docker.capped', { n: n(MAX_SERIES) }, MAX_SERIES) }}</p>
  </section>
</template>

<style scoped>
.mon-section {
  display: flex;
  flex-direction: column;
  gap: var(--app-gap);
  min-inline-size: 0;
}
.mon-h {
  margin: 0;
  font-size: 1.1rem;
}
.small {
  font-size: 0.8rem;
}
.box-name {
  font-weight: 600;
}
.attention,
.oom {
  color: var(--p-orange-600);
  font-weight: 600;
}
.docker-down {
  margin: 0;
  color: var(--p-red-600);
  font-weight: 600;
}
.net {
  display: flex;
  flex-direction: column;
  white-space: nowrap;
}
.facts {
  margin: 0;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 9rem), 1fr));
  gap: 0.6rem 1rem;
}
.facts dt {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
}
.facts dd {
  margin: 0.1rem 0 0;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.charts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 22rem), 1fr));
  gap: var(--app-gap);
  min-inline-size: 0;
}
.charts > * {
  min-inline-size: 0;
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
