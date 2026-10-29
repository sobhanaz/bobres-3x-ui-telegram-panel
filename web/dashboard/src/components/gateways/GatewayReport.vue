<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Message from 'primevue/message'
import UnmatchedDeposits from './UnmatchedDeposits.vue'
import { api, query } from '../../api'
import { errorText } from '../../errors'
import { dateTime, money, num, shortId, type Lang } from '../../format'
import { checkErrors, depositNetwork, reportRange, reportRows } from '../../gateways'
import type { GatewayReport } from '../../types'

// A gateway's payments of the last 7 and 30 days, by status, side by side,
// and the payment checks that failed meanwhile (the gateway could not be
// asked, or refused; when core reports them); with `unmatched`, also the
// deposits that matched no payment and are still to handle, whatever their
// age (when core reports them); with `scan` (a gateway that watches an address),
// also why the last read of the address failed, when it did.
const props = withDefaults(defineProps<{ provider: string; unmatched?: boolean; scan?: boolean }>(), { unmatched: false, scan: false })
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const week = ref<GatewayReport | null>(null)
const month = ref<GatewayReport | null>(null)
const loading = ref(true)
const error = ref('')

async function fetchReport(days: number, now: number): Promise<GatewayReport> {
  return api<GatewayReport>('GET', '/gateways/report' + query({ provider: props.provider, ...reportRange(days, now) }))
}
async function load() {
  loading.value = true
  error.value = ''
  try {
    // Both periods end at the same moment.
    const now = Date.now()
    const [w, m] = await Promise.all([fetchReport(7, now), fetchReport(30, now)])
    week.value = w
    month.value = m
  } catch (e) {
    error.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

const rows = computed(() => (week.value || month.value ? reportRows(week.value, month.value) : []))
const deposits = computed(() => (props.unmatched && !error.value && Array.isArray(month.value?.unmatched) ? month.value.unmatched : null))
const depositCount = computed(() => (typeof month.value?.unmatched_count === 'number' ? month.value.unmatched_count : undefined))
const checks = computed(() => (error.value ? null : checkErrors(week.value, month.value)))
const scanError = computed(() => (props.scan && !error.value ? month.value?.scan_error || week.value?.scan_error || '' : ''))
// A deposit list is of the gateway's network: TON for automatic TON, else TRC20.
const network = computed(() => depositNetwork(props.provider))
const statusLabel = (s: string) => (te('payment.status.' + s) ? t('payment.status.' + s) : s)
const totalText = (list: { amount: number; currency: string }[]) => list.map((m) => money(m, lang.value)).join(' · ')
</script>

<template>
  <section class="report" :aria-label="t('gateway.report')">
    <div class="head">
      <h3 class="title">{{ t('gateway.report') }}</h3>
      <Button icon="pi pi-refresh" text rounded size="small" :loading="loading" :aria-label="t('gateway.report_refresh')" class="refresh" @click="load" />
    </div>
    <Message v-if="scanError" severity="warn" :closable="false" class="scan-error">
      {{ t('gateway.scan_error') }}
      <div class="app-ltr detail">{{ scanError }}</div>
    </Message>
    <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
    <DataTable v-else :value="rows" :loading="loading" data-key="status" size="small" scrollable class="report-table">
      <template #empty>{{ t('gateway.report_empty') }}</template>
      <Column :header="t('common.status')">
        <template #body="{ data: r }">{{ statusLabel(r.status) }}</template>
      </Column>
      <Column :header="t('gateway.days_7')" body-class="app-nowrap">
        <template #body="{ data: r }">
          <span :class="'count week-' + r.status">{{ num(r.week.count, lang) }}</span>
          <div v-if="r.week.total.length" class="app-muted small">{{ totalText(r.week.total) }}</div>
        </template>
      </Column>
      <Column :header="t('gateway.days_30')" body-class="app-nowrap">
        <template #body="{ data: r }">
          <span :class="'count month-' + r.status">{{ num(r.month.count, lang) }}</span>
          <div v-if="r.month.total.length" class="app-muted small">{{ totalText(r.month.total) }}</div>
        </template>
      </Column>
    </DataTable>
    <section v-if="checks" class="check-errors" :aria-label="t('gateway.check_errors.title')">
      <h4 class="checks-title"><i class="pi pi-exclamation-triangle" aria-hidden="true" /> {{ t('gateway.check_errors.title') }}</h4>
      <p class="checks-count">{{ t('gateway.check_errors.counts', { week: num(checks.week, lang), month: num(checks.month, lang) }) }}</p>
      <p class="app-muted small checks-hint">{{ t('gateway.check_errors.hint') }}</p>
      <ol class="check-error-list">
        <li v-for="(c, i) in checks.items" :key="c.at + ':' + c.intent_id + ':' + i" class="check-error">
          <div class="check-head">
            <time class="check-at" :datetime="new Date(c.at * 1000).toISOString()">{{ dateTime(c.at, lang) }}</time>
            <span v-if="c.intent_id" class="app-muted check-payment">
              {{ t('gateway.check_errors.payment') }} <span class="app-ltr app-mono">{{ shortId(c.intent_id) }}</span>
            </span>
          </div>
          <div v-if="c.detail" class="app-ltr detail check-detail">{{ c.detail }}</div>
        </li>
      </ol>
      <p v-if="checks.more" class="app-muted small checks-more">
        {{ t('gateway.check_errors.more', { shown: num(checks.items.length, lang), total: num(checks.month, lang) }) }}
      </p>
    </section>
    <UnmatchedDeposits v-if="deposits" :items="deposits" :count="depositCount" :network="network" class="deposits" @changed="load" />
  </section>
</template>

<style scoped>
.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}
.title {
  margin: 0;
  font-size: 1rem;
  font-weight: 700;
}
.small {
  font-size: 0.8rem;
}
.deposits {
  margin-block-start: 1.25rem;
}
.scan-error {
  margin-block-end: 1rem;
}
.check-errors {
  margin-block-start: 1.25rem;
}
.checks-title {
  margin: 0;
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.95rem;
  font-weight: 700;
}
.checks-title .pi {
  color: var(--p-orange-500);
}
.checks-count {
  margin-block: 0.4rem 0;
}
.checks-hint,
.checks-more {
  margin-block: 0.25rem 0;
}
.check-error-list {
  list-style: none;
  margin: 0.6rem 0 0;
  padding: 0;
}
.check-error {
  padding-block: 0.45rem;
  border-block-end: 1px solid var(--p-content-border-color);
}
.check-error:last-child {
  border-block-end: 0;
}
.check-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.25rem 0.75rem;
}
.check-at {
  font-weight: 600;
  unicode-bidi: isolate;
}
.check-payment {
  font-size: 0.85rem;
}
.detail {
  margin-block-start: 0.2rem;
  font-size: 0.85rem;
  overflow-wrap: anywhere;
}
</style>
