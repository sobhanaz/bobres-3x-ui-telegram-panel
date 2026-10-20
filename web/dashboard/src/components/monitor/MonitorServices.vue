<script setup lang="ts">
import { computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import Card from 'primevue/card'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import Tag from 'primevue/tag'
import { isService, orderServices, subjects, type MonitorComponent, type Overview, type SystemdUnit } from '../../monitor'
import MetricChart from './MetricChart.vue'
import StateTag from './StateTag.vue'
import { feedKey, useFmt, type MetricDef } from './feed'

// The BOBRES services and data stores as core and the containers show them (state, version, how long they have
// run, what core measured), how fast they answer over the range, and the systemd units the host collector
// watches (its own timer, the updater and Docker).
const props = defineProps<{ overview: Overview | null }>()
const { t, te } = useI18n()
const { n, dur, at } = useFmt()
const feed = inject(feedKey)!

const rows = computed(() => (props.overview?.components ?? []).filter(isService))
const units = computed(() => props.overview?.units ?? [])
const hostUp = computed(() => props.overview?.host.available === true)
const nameOf = (c: MonitorComponent) => (te('monitor.component.' + c.id) ? t('monitor.component.' + c.id) : c.name || c.id)

const latency = computed<MetricDef[]>(() =>
  orderServices(subjects(feed.available.value, 'service.latency_ms')).map((s, i) => ({ metric: 'service.latency_ms:' + s, label: te('system.service.' + s) ? t('system.service.' + s) : s, slot: i })),
)

// what each watched unit is for, in the page's words; a unit it does not know is shown by name alone
const UNIT_KEYS: Record<string, string> = {
  'bobres-monitor.timer': 'monitor_timer',
  'bobres-monitor.service': 'monitor_service',
  'bobres-updater.path': 'updater_path',
  'bobres-updater.service': 'updater_service',
  'docker.service': 'docker',
}
const unitNote = (u: SystemdUnit) => (UNIT_KEYS[u.name] ? t('monitor.services.unit.' + UNIT_KEYS[u.name]) : '')
// systemd's own words for its state, with a colour by the state: failed is red, active green, the rest grey
const unitSeverity = (u: SystemdUnit) => (u.active === 'failed' || u.load === 'error' ? 'danger' : u.active === 'active' ? 'success' : u.active === 'activating' || u.active === 'reloading' ? 'info' : 'secondary')
const unitState = (u: SystemdUnit) => (u.sub && u.sub !== u.active ? `${u.active} (${u.sub})` : u.active)
</script>

<template>
  <section id="monitor-services" class="mon-section" tabindex="-1" aria-labelledby="monitor-services-h">
    <h2 id="monitor-services-h" class="mon-h">{{ t('monitor.section.services') }}</h2>

    <Card class="services-card">
      <template #title>{{ t('monitor.services.bobres') }}</template>
      <template #content>
        <DataTable :value="rows" data-key="id" size="small" scrollable class="services-table" :loading="!overview">
          <Column :header="t('monitor.services.col_service')">
            <template #body="{ data }"><bdi class="svc-name">{{ nameOf(data) }}</bdi></template>
          </Column>
          <Column :header="t('monitor.services.col_state')">
            <template #body="{ data }"><StateTag :state="data.status" /></template>
          </Column>
          <Column :header="t('monitor.services.col_version')">
            <template #body="{ data }"><span class="app-ltr" dir="ltr">{{ data.version || '—' }}</span></template>
          </Column>
          <Column :header="t('monitor.services.col_up')">
            <template #body="{ data }">{{ data.uptime_s === null ? '—' : dur(data.uptime_s) }}</template>
          </Column>
          <Column :header="t('monitor.services.col_detail')">
            <template #body="{ data }"><span class="app-muted app-ltr detail" dir="ltr">{{ data.detail }}</span></template>
          </Column>
        </DataTable>
      </template>
    </Card>

    <div v-if="latency.length" class="charts">
      <MetricChart :title="t('monitor.services.latency')" :subtitle="t('monitor.services.latency_note')" :defs="latency" unit="ms" />
    </div>

    <Card class="units-card">
      <template #title>{{ t('monitor.services.units') }}</template>
      <template #content>
        <p v-if="overview && !hostUp" class="app-muted no-units">{{ t('monitor.services.units_need_collector') }}</p>
        <p v-else-if="overview && !units.length" class="app-muted no-units">{{ t('monitor.services.units_none') }}</p>
        <DataTable v-else :value="units" data-key="name" size="small" scrollable class="units-table" :loading="!overview">
          <Column :header="t('monitor.services.col_unit')">
            <template #body="{ data }">
              <bdi class="app-ltr app-mono unit-name">{{ data.name }}</bdi>
              <div v-if="unitNote(data)" class="app-muted small">{{ unitNote(data) }}</div>
            </template>
          </Column>
          <Column :header="t('monitor.services.col_state')">
            <template #body="{ data }">
              <Tag :severity="unitSeverity(data)" :value="unitState(data)" class="app-ltr unit-state" />
              <div v-if="data.result && data.result !== 'success'" class="small attention app-ltr" dir="ltr">{{ data.result }}</div>
            </template>
          </Column>
          <Column :header="t('monitor.services.col_enabled')">
            <template #body="{ data }"><span class="app-ltr" dir="ltr">{{ data.enabled || '—' }}</span></template>
          </Column>
          <Column :header="t('monitor.services.col_restarts')">
            <template #body="{ data }">{{ n(data.restarts) }}</template>
          </Column>
          <Column :header="t('monitor.services.col_since')">
            <template #body="{ data }">{{ data.since > 0 ? at(data.since) : '—' }}</template>
          </Column>
        </DataTable>
      </template>
    </Card>
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
.svc-name {
  font-weight: 600;
}
.detail {
  font-size: 0.85rem;
  overflow-wrap: anywhere;
}
.small {
  font-size: 0.8rem;
}
.attention {
  color: var(--p-orange-600);
}
.charts {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
}
</style>
