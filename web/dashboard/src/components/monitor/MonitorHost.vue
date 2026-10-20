<script setup lang="ts">
import { computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import Card from 'primevue/card'
import ProgressBar from 'primevue/progressbar'
import Tag from 'primevue/tag'
import { subjects, type Overview } from '../../monitor'
import CollectorNotice from './CollectorNotice.vue'
import MetricChart from './MetricChart.vue'
import { feedKey, useFmt, type MetricDef } from './feed'

// The server the store runs on: CPU, memory, swap, load, traffic and every disk, now and over the range. The
// figures come from the host collector; without it the section says so (and shows the history it has).
const props = defineProps<{ overview: Overview | null }>()
const { t } = useI18n()
const { n, pct, size, rate, dur } = useFmt()
const feed = inject(feedKey)!

const host = computed(() => props.overview?.host)
const mem = computed(() => (host.value && host.value.mem.total > 0 ? (host.value.mem.used / host.value.mem.total) * 100 : null))
const swap = computed(() => (host.value && host.value.swap.total > 0 ? (host.value.swap.used / host.value.swap.total) * 100 : null))
const level = (v: number) => (v >= 95 ? 'full' : v >= 85 ? 'high' : 'ok')
const yesNo = (v: boolean | null | undefined) => (v === true ? t('monitor.yes') : v === false ? t('monitor.no') : t('monitor.unknown_value'))

const has = (...metrics: string[]) => metrics.some((m) => feed.available.value.has(m))
const disks = computed(() => subjects(feed.available.value, 'host.disk_pct').slice(0, 6))
const diskDefs = computed<MetricDef[]>(() => disks.value.map((m, i) => ({ metric: 'host.disk_pct:' + m, label: m, slot: i })))
const anyHistory = computed(() => has('host.cpu_pct', 'host.mem_pct', 'host.swap_pct', 'host.load1', 'host.net_rx_bps', 'host.net_tx_bps') || disks.value.length > 0)
</script>

<template>
  <section id="monitor-host" class="mon-section" tabindex="-1" aria-labelledby="monitor-host-h">
    <h2 id="monitor-host-h" class="mon-h">{{ t('monitor.section.host') }}</h2>

    <CollectorNotice v-if="overview && !host?.available" :overview="overview" />

    <template v-if="host?.available">
      <dl class="tiles">
        <div class="tile cpu">
          <dt>{{ t('monitor.host.cpu') }}</dt>
          <dd>{{ host.cpu_pct === null ? '—' : pct(host.cpu_pct) }}</dd>
          <dd v-if="host.cores" class="app-muted sub">{{ t('monitor.host.cores', { n: n(host.cores) }, host.cores) }}</dd>
        </div>
        <div class="tile load">
          <dt>{{ t('monitor.host.load') }}</dt>
          <dd>{{ host.load.map((v) => n(v, 2)).join(' / ') }}</dd>
          <dd class="app-muted sub">{{ t('monitor.host.load_note') }}</dd>
        </div>
        <div class="tile mem">
          <dt>{{ t('monitor.host.memory') }}</dt>
          <dd>{{ mem === null ? '—' : pct(mem) }}</dd>
          <dd v-if="mem !== null" class="sub"><ProgressBar :value="Math.round(mem)" :show-value="false" :class="['bar', level(mem)]" :aria-label="t('monitor.host.memory')" /></dd>
          <dd class="app-muted sub">{{ size(host.mem.used) }} / {{ size(host.mem.total) }}</dd>
        </div>
        <div class="tile swap">
          <dt>{{ t('monitor.host.swap') }}</dt>
          <dd>{{ swap === null ? t('monitor.host.no_swap') : pct(swap) }}</dd>
          <dd v-if="swap !== null" class="app-muted sub">{{ size(host.swap.used) }} / {{ size(host.swap.total) }}</dd>
        </div>
        <div class="tile net">
          <dt>{{ t('monitor.host.network') }}</dt>
          <dd class="net-line">
            <span><i class="pi pi-arrow-down" aria-hidden="true" /><span class="sr-only">{{ t('monitor.host.rx') }}</span> {{ host.net.rx_bps === null ? '—' : rate(host.net.rx_bps) }}</span>
            <span><i class="pi pi-arrow-up" aria-hidden="true" /><span class="sr-only">{{ t('monitor.host.tx') }}</span> {{ host.net.tx_bps === null ? '—' : rate(host.net.tx_bps) }}</span>
          </dd>
        </div>
        <div class="tile uptime">
          <dt>{{ t('monitor.host.uptime') }}</dt>
          <dd>{{ dur(host.uptime_s) }}</dd>
        </div>
      </dl>

      <Card class="disks-card">
        <template #title>{{ t('monitor.host.disks') }}</template>
        <template #content>
          <p v-if="!host.disks.length" class="app-muted">{{ t('monitor.host.no_disks') }}</p>
          <ul v-else class="disks">
            <li v-for="d in host.disks" :key="d.mount" class="disk" :data-mount="d.mount">
              <div class="disk-head">
                <bdi class="app-ltr app-mono mount">{{ d.mount }}</bdi>
                <Tag v-for="r in d.roles" :key="r" severity="secondary" :value="t('monitor.host.role.' + r)" class="role" />
                <span class="disk-pct">{{ pct(d.pct) }}</span>
              </div>
              <ProgressBar :value="Math.round(d.pct)" :show-value="false" :class="['bar', level(d.pct)]" :aria-label="t('monitor.host.disk_of', { mount: d.mount })" />
              <p class="app-muted sub">{{ t('monitor.host.disk_use', { used: size(d.used), total: size(d.total), free: size(d.avail) }) }}</p>
            </li>
          </ul>
        </template>
      </Card>

      <Card class="about-card">
        <template #title>{{ t('monitor.host.about') }}</template>
        <template #content>
          <dl class="facts">
            <div v-if="host.hostname"><dt>{{ t('monitor.host.hostname') }}</dt><dd class="app-ltr">{{ host.hostname }}</dd></div>
            <div v-if="host.os"><dt>{{ t('monitor.host.os') }}</dt><dd class="app-ltr">{{ host.os }}</dd></div>
            <div v-if="host.kernel"><dt>{{ t('monitor.host.kernel') }}</dt><dd class="app-ltr">{{ host.kernel }}</dd></div>
            <div>
              <dt>{{ t('monitor.host.docker') }}</dt>
              <dd v-if="host.docker.ok" class="app-ltr">{{ host.docker.version }} <span class="app-muted">(API {{ host.docker.api }})</span></dd>
              <dd v-else class="docker-down">{{ t('monitor.host.docker_down') }} <span v-if="host.docker.error" class="app-muted app-ltr">{{ host.docker.error }}</span></dd>
            </div>
            <div v-if="host.clock">
              <dt>{{ t('monitor.host.clock') }}</dt>
              <dd>{{ yesNo(host.clock.synchronized) }}</dd>
            </div>
          </dl>
        </template>
      </Card>
    </template>

    <template v-if="anyHistory">
      <div class="charts">
        <MetricChart :title="t('monitor.host.cpu')" :defs="[{ metric: 'host.cpu_pct', label: t('monitor.host.cpu'), slot: 0, area: true }]" unit="percent" :y-max="100" />
        <MetricChart
          :title="t('monitor.host.memory')"
          :defs="[{ metric: 'host.mem_pct', label: t('monitor.host.memory'), slot: 1, area: true }, { metric: 'host.swap_pct', label: t('monitor.host.swap'), slot: 2, dashed: true }]"
          unit="percent"
          :y-max="100"
        />
        <MetricChart :title="t('monitor.host.load')" :subtitle="t('monitor.host.load_chart')" :defs="[{ metric: 'host.load1', label: t('monitor.host.load_1'), slot: 3 }]" unit="load" />
        <MetricChart
          :title="t('monitor.host.network')"
          :defs="[{ metric: 'host.net_rx_bps', label: t('monitor.host.rx'), slot: 4, area: true }, { metric: 'host.net_tx_bps', label: t('monitor.host.tx'), slot: 5 }]"
          unit="bytes_per_s"
        />
        <MetricChart v-if="diskDefs.length" :title="t('monitor.host.disks')" :defs="diskDefs" unit="percent" :y-max="100" />
      </div>
    </template>
    <p v-else-if="overview" class="app-muted no-history">{{ t('monitor.chart.no_history') }}</p>
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
.tiles {
  margin: 0;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 11rem), 1fr));
  gap: 0.6rem;
}
.tile {
  padding: 0.6rem 0.8rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.6rem;
  background: var(--p-content-background);
  min-inline-size: 0;
}
.tile dt {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
}
.tile dd {
  margin: 0.15rem 0 0;
  font-size: 1.3rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.net-line {
  display: flex;
  flex-direction: column;
  font-size: 1rem !important;
  gap: 0.1rem;
}
.tile dd.sub {
  margin: 0.2rem 0 0;
  font-size: 0.8rem;
  font-weight: 400;
}
.bar {
  block-size: 0.4rem;
  margin-block-start: 0.35rem;
}
.bar.high :deep(.p-progressbar-value) {
  background: var(--p-orange-500);
}
.bar.full :deep(.p-progressbar-value) {
  background: var(--p-red-500);
}
.disks {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
}
.disk-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
.mount {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.disk-pct {
  margin-inline-start: auto;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.facts {
  margin: 0;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 14rem), 1fr));
  gap: 0.6rem 1rem;
}
.facts dt {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
}
.facts dd {
  margin: 0.1rem 0 0;
  overflow-wrap: anywhere;
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
