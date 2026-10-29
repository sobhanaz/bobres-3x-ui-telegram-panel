<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Card from 'primevue/card'
import Message from 'primevue/message'
import SelectButton from 'primevue/selectbutton'
import { UPTIME_WINDOWS, uptimeWindowOf, type UptimeReply, type UptimeWindow } from '../../monitor'
import { useFmt } from './feed'
import UptimeTable from './UptimeTable.vue'

// How available each BOBRES service and each panel server was over 24 hours, 7 days or 30 days, from the samples core
// keeps: the percentage of the time that was observed, the outages and a bar with a cell for each stretch of the period.
// A moment nobody sampled is "no data": it is not in the percentage and its cell is hatched, never green and never red.
const props = defineProps<{ uptime: UptimeReply | null; failed: boolean }>()
const { t } = useI18n()
const { at, n, dur } = useFmt()

const WINDOW_KEY = 'bobres.monitor.uptime'
function savedWindow(): UptimeWindow {
  try {
    return uptimeWindowOf(localStorage.getItem(WINDOW_KEY))
  } catch {
    return '24h'
  }
}
const win = ref<UptimeWindow>(savedWindow())
function pick(w: UptimeWindow | null) {
  if (!w) return
  win.value = w
  try {
    localStorage.setItem(WINDOW_KEY, w)
  } catch {
    /* not remembered: fine */
  }
}
const windowOptions = computed(() => UPTIME_WINDOWS.map((w) => ({ value: w, label: t('monitor.range.' + w) })))
const legend = ['up', 'partial', 'down', 'nodata'] as const
const hasData = computed(() => props.uptime?.since != null)
</script>

<template>
  <section id="monitor-uptime" class="mon-section" tabindex="-1" aria-labelledby="monitor-uptime-h">
    <div class="head">
      <h2 id="monitor-uptime-h" class="mon-h">{{ t('monitor.section.uptime') }}</h2>
      <SelectButton
        :model-value="win"
        :options="windowOptions"
        option-label="label"
        option-value="value"
        :allow-empty="false"
        :aria-label="t('monitor.uptime.window_label')"
        class="window-switch"
        @update:model-value="pick"
      />
    </div>
    <p class="app-muted intro">{{ t('monitor.uptime.intro') }}</p>

    <Message v-if="failed" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="uptime-failed">{{ t('monitor.uptime.load_error') }}</Message>

    <template v-if="uptime">
      <Card class="uptime-card services-card">
        <template #title>{{ t('monitor.uptime.services') }}</template>
        <template #content>
          <UptimeTable :components="uptime.services" service :uptime="uptime" :period="win" class="services-table" />
        </template>
      </Card>

      <Card class="uptime-card panels-card">
        <template #title>{{ t('monitor.uptime.panels') }}</template>
        <template #content>
          <p v-if="uptime.panels_unavailable" class="app-muted panels-unavailable">{{ t('monitor.uptime.panels_unavailable') }}</p>
          <p v-else-if="!uptime.panels.length" class="app-muted no-panels">{{ t('monitor.uptime.no_panels') }}</p>
          <UptimeTable v-else :components="uptime.panels" :service="false" :uptime="uptime" :period="win" class="panels-table" />
        </template>
      </Card>

      <ul class="legend" :aria-label="t('monitor.uptime.legend.label')">
        <li v-for="s in legend" :key="s" :class="'legend-' + s"><span class="swatch" :class="s" aria-hidden="true" />{{ t('monitor.uptime.legend.' + s) }}</li>
      </ul>
      <p class="app-muted small retention">
        {{ t('monitor.uptime.retention', { n: n(uptime.retention_days), step: dur(uptime.sample_every_s) }, uptime.retention_days) }}
        <template v-if="hasData">{{ t('monitor.uptime.since', { date: at(uptime.since ?? 0) }) }}</template>
        <template v-else>{{ t('monitor.uptime.none_yet') }}</template>
      </p>
      <p v-if="uptime.status_page" class="app-muted small status-page status-on">
        {{ t('monitor.uptime.status_on') }} <a href="/status" target="_blank" rel="noopener noreferrer" class="app-ltr" dir="ltr">/status</a>
      </p>
      <p v-else class="app-muted small status-page status-off">{{ t('monitor.uptime.status_off') }}</p>
    </template>
    <p v-else-if="!failed" class="app-muted loading">{{ t('monitor.uptime.loading') }}</p>
  </section>
</template>

<style scoped>
.mon-section {
  display: flex;
  flex-direction: column;
  gap: var(--app-gap);
  min-inline-size: 0;
}
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}
.mon-h {
  margin: 0;
  font-size: 1.1rem;
}
.window-switch {
  flex-wrap: wrap;
}
.intro,
.retention,
.status-page {
  margin: 0;
}
.small {
  font-size: 0.8rem;
}
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem 1.25rem;
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: 0.85rem;
}
.legend li {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}
.swatch {
  inline-size: 0.9rem;
  block-size: 0.9rem;
  border-radius: 1px;
}
.swatch.up {
  background: var(--p-green-500);
}
.swatch.partial {
  background: var(--p-orange-500);
}
.swatch.down {
  background: var(--p-red-500);
}
.swatch.nodata {
  background: repeating-linear-gradient(135deg, var(--p-surface-400) 0 2px, transparent 2px 4px);
  outline: 1px solid var(--p-content-border-color);
  outline-offset: -1px;
}
</style>
