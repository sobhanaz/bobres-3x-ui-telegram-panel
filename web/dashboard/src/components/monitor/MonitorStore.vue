<script setup lang="ts">
import { computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { STORE_METRICS } from '../../monitor'
import MetricChart from './MetricChart.vue'
import { feedKey } from './feed'

// The bot and the store over the range: how many updates the bot handles, the errors the services logged, the
// queues (orders and payments waiting, paid orders not yet delivered, core events not yet read) and core's own load.
const { t } = useI18n()
const feed = inject(feedKey)!
const any = computed(() => STORE_METRICS.some((m) => feed.available.value.has(m)))
</script>

<template>
  <section id="monitor-store" class="mon-section" tabindex="-1" aria-labelledby="monitor-store-h">
    <h2 id="monitor-store-h" class="mon-h">{{ t('monitor.section.store') }}</h2>
    <div v-if="any" class="charts">
      <MetricChart :title="t('monitor.store.updates')" :defs="[{ metric: 'bot.updates_per_min', label: t('monitor.store.updates_series'), slot: 0, area: true }]" unit="per_min" />
      <MetricChart
        :title="t('monitor.store.errors')"
        :defs="[{ metric: 'core.errors_per_min', label: t('system.service.core'), slot: 0 }, { metric: 'bot.errors_per_min', label: t('system.service.bot'), slot: 1 }]"
        unit="per_min"
      />
      <MetricChart
        :title="t('monitor.store.queues')"
        :defs="[
          { metric: 'queue.pending_orders', label: t('monitor.counter.orders_pending'), slot: 0 },
          { metric: 'queue.pending_payments', label: t('monitor.counter.payments_pending'), slot: 1 },
          { metric: 'queue.provisioning', label: t('monitor.counter.provision_queue'), slot: 2 },
          { metric: 'queue.outbox', label: t('monitor.counter.outbox_backlog'), slot: 3 },
        ]"
        unit="count"
      />
      <MetricChart
        :title="t('monitor.store.core')"
        :defs="[{ metric: 'core.goroutines', label: t('monitor.store.goroutines'), slot: 0 }, { metric: 'core.db_conns', label: t('monitor.store.db_conns'), slot: 1 }]"
        unit="count"
      />
    </div>
    <p v-else class="app-muted no-history">{{ t('monitor.chart.no_history') }}</p>
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
.charts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 22rem), 1fr));
  gap: var(--app-gap);
  min-inline-size: 0;
}
.charts > * {
  min-inline-size: 0;
}
</style>
