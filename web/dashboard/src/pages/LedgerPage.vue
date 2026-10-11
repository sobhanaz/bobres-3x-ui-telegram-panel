<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import Button from 'primevue/button'
import Select from 'primevue/select'
import Message from 'primevue/message'
import Chip from 'primevue/chip'
import LedgerTable from '../components/LedgerTable.vue'
import { useList } from '../composables/useList'
import { query } from '../api'
import { currencies } from '../format'
import type { LedgerItem } from '../types'

const { t } = useI18n()
const route = useRoute()
const list = useList<LedgerItem, { user: string; kind: string; currency: string; from: string }>('/ledger', {
  user: String(route.query.user ?? ''),
  kind: '',
  currency: '',
  from: '',
})
onMounted(list.load)

// Periods count from the start of today in the browser's own time zone.
const period = ref('')
watch(period, (p) => {
  const start = new Date()
  start.setHours(0, 0, 0, 0)
  const day = 86_400
  const today = Math.floor(start.getTime() / 1000)
  list.filters.from = { today: String(today), week: String(today - 6 * day), month: String(today - 29 * day) }[p] ?? ''
})

const kinds = computed(() => [
  { label: t('ledger.all_kinds'), value: '' },
  ...['topup', 'purchase', 'refund', 'adjust', 'referral', 'trial_grant'].map((k) => ({ label: t('ledger.kind.' + k), value: k })),
])
const currencyOptions = computed(() => [
  { label: t('ledger.all_currencies'), value: '' },
  ...currencies.map((c) => ({ label: t('currency.' + c), value: c })),
])
const periods = computed(() => [
  { label: t('ledger.period.all'), value: '' },
  { label: t('ledger.period.today'), value: 'today' },
  { label: t('ledger.period.week'), value: 'week' },
  { label: t('ledger.period.month'), value: 'month' },
])
const csvHref = computed(() => '/api/v1/ledger.csv' + query({ ...list.filters }))
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.ledger.title') }}</h1>
    <a :href="csvHref" download class="p-button p-component p-button-outlined export">
      <i class="pi pi-download" aria-hidden="true" />
      <span>{{ t('ledger.export') }}</span>
    </a>
  </div>
  <div class="app-toolbar">
    <Chip v-if="list.filters.user" :label="t('ledger.one_customer')" removable @remove="list.filters.user = ''" />
    <Select v-model="list.filters.kind" :placeholder="t('ledger.all_kinds')" :options="kinds" option-label="label" option-value="value" :aria-label="t('ledger.kind_col')" />
    <Select v-model="list.filters.currency" :placeholder="t('ledger.all_currencies')" :options="currencyOptions" option-label="label" option-value="value" :aria-label="t('common.currency')" />
    <Select v-model="period" :placeholder="t('ledger.period.all')" :options="periods" option-label="label" option-value="value" :aria-label="t('ledger.period_label')" />
    <Button v-if="list.loading.value" text loading :aria-label="t('app.loading')" />
  </div>
  <Message v-if="list.error.value" severity="error" :closable="false">{{ list.error.value }}</Message>
  <p class="app-muted hint">{{ t('ledger.hint') }}</p>
  <LedgerTable
    :items="list.items.value"
    :total="list.total.value"
    :rows="list.rows.value"
    :first="(list.page.value - 1) * list.rows.value"
    :loading="list.loading.value"
    @page="list.onPage"
  />
</template>

<style scoped>
.export {
  text-decoration: none;
  gap: 0.5rem;
}
.hint {
  margin-block: 0 0.75rem;
  font-size: 0.9rem;
}
</style>
