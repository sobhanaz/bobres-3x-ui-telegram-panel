<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Message from 'primevue/message'
import { api, query } from '../../api'
import { errorText } from '../../errors'
import { money, num, type Lang } from '../../format'
import { reportRange, reportRows } from '../../gateways'
import type { GatewayReport } from '../../types'

// A gateway's payments of the last 7 and 30 days, by status, side by side.
const props = defineProps<{ provider: string }>()
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
const statusLabel = (s: string) => (te('payment.status.' + s) ? t('payment.status.' + s) : s)
const totalText = (list: { amount: number; currency: string }[]) => list.map((m) => money(m, lang.value)).join(' · ')
</script>

<template>
  <section class="report" :aria-label="t('gateway.report')">
    <div class="head">
      <h3 class="title">{{ t('gateway.report') }}</h3>
      <Button icon="pi pi-refresh" text rounded size="small" :loading="loading" :aria-label="t('gateway.report_refresh')" class="refresh" @click="load" />
    </div>
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
</style>
