<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Card from 'primevue/card'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Tag from 'primevue/tag'
import Skeleton from 'primevue/skeleton'
import Message from 'primevue/message'
import Button from 'primevue/button'
import { api } from '../api'
import { dateTime, money, num, type Money, type Lang } from '../format'

interface RecentOrder {
  id: string
  type: string
  status: string
  amount: Money
  created_at: number
  telegram_id: number
  username: string
  plan: Record<string, string>
}

interface Overview {
  users: { total: number; new_24h: number; new_7d: number }
  services: { active: number; expiring: number; ended: number }
  orders: { paid_24h: number; provision_failed: number }
  revenue: { day: Money[]; month: Money[] }
  pending_payments: number
  panel: { healthy: boolean; detail: string }
  recent_orders: RecentOrder[]
}

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const data = ref<Overview | null>(null)
const failed = ref(false)

async function load() {
  failed.value = false
  try {
    data.value = await api<Overview>('GET', '/overview')
  } catch {
    failed.value = true
  }
}
onMounted(load)

const moneyLine = (list: Money[]) => (list.length ? list.map((m) => money(m, lang.value)).join(' · ') : t('overview.no_revenue'))
const planName = (p: Record<string, string>) => p[lang.value] || p.en || p.fa || ''
const statusSeverity = (s: string) =>
  ({ active: 'success', provision_failed: 'danger', awaiting_payment: 'warn', created: 'secondary', cancelled: 'secondary', expired: 'secondary' })[s] ?? 'info'
</script>

<template>
  <h1 class="app-page-title">{{ t('section.overview.title') }}</h1>

  <Message v-if="failed" severity="error" :closable="false">
    {{ t('error.generic') }}
    <Button :label="t('app.retry')" text size="small" @click="load" />
  </Message>

  <div class="app-grid">
    <Card class="stat">
      <template #subtitle>{{ t('overview.users') }}</template>
      <template #content>
        <template v-if="data">
          <div class="big">{{ num(data.users.total, lang) }}</div>
          <div class="app-muted">{{ t('overview.users_new', { day: num(data.users.new_24h, lang), week: num(data.users.new_7d, lang) }) }}</div>
        </template>
        <Skeleton v-else height="3rem" />
      </template>
    </Card>
    <Card class="stat">
      <template #subtitle>{{ t('overview.services') }}</template>
      <template #content>
        <template v-if="data">
          <div class="big">{{ num(data.services.active, lang) }}</div>
          <div class="app-muted">{{ t('overview.services_detail', { expiring: num(data.services.expiring, lang), ended: num(data.services.ended, lang) }) }}</div>
        </template>
        <Skeleton v-else height="3rem" />
      </template>
    </Card>
    <Card class="stat">
      <template #subtitle>{{ t('overview.revenue_day') }}</template>
      <template #content>
        <div v-if="data" class="mid">{{ moneyLine(data.revenue.day) }}</div>
        <Skeleton v-else height="3rem" />
      </template>
    </Card>
    <Card class="stat">
      <template #subtitle>{{ t('overview.revenue_month') }}</template>
      <template #content>
        <div v-if="data" class="mid">{{ moneyLine(data.revenue.month) }}</div>
        <Skeleton v-else height="3rem" />
      </template>
    </Card>
    <Card class="stat">
      <template #subtitle>{{ t('overview.pending') }}</template>
      <template #content>
        <div v-if="data" class="big" :class="{ warn: data.pending_payments > 0 }">
          {{ data.pending_payments < 0 ? t('app.unknown') : num(data.pending_payments, lang) }}
        </div>
        <Skeleton v-else height="3rem" />
      </template>
    </Card>
    <Card class="stat">
      <template #subtitle>{{ t('overview.failed') }}</template>
      <template #content>
        <template v-if="data">
          <div class="big" :class="{ bad: data.orders.provision_failed > 0 }">{{ num(data.orders.provision_failed, lang) }}</div>
          <div class="app-muted">{{ t('overview.failed_hint') }}</div>
        </template>
        <Skeleton v-else height="3rem" />
      </template>
    </Card>
    <Card class="stat">
      <template #subtitle>{{ t('overview.panel') }}</template>
      <template #content>
        <template v-if="data">
          <Tag :severity="data.panel.healthy ? 'success' : 'danger'" :value="data.panel.healthy ? t('overview.panel_ok') : t('overview.panel_down')" />
          <div v-if="data.panel.detail" class="app-muted app-ltr detail">{{ data.panel.detail }}</div>
        </template>
        <Skeleton v-else height="3rem" />
      </template>
    </Card>
  </div>

  <Card class="recent">
    <template #title>{{ t('overview.recent') }}</template>
    <template #content>
      <DataTable :value="data?.recent_orders ?? []" :loading="!data && !failed" data-key="id" size="small" scrollable>
        <template #empty>{{ t('overview.no_orders') }}</template>
        <Column :header="t('overview.col_customer')">
          <template #body="{ data: o }">
            <bdi>{{ o.username ? '@' + o.username : o.telegram_id }}</bdi>
          </template>
        </Column>
        <Column :header="t('overview.col_plan')">
          <template #body="{ data: o }"><bdi>{{ planName(o.plan) }}</bdi></template>
        </Column>
        <Column :header="t('overview.col_type')">
          <template #body="{ data: o }">{{ t('order.type.' + o.type) }}</template>
        </Column>
        <Column :header="t('overview.col_amount')">
          <template #body="{ data: o }">{{ money(o.amount, lang) }}</template>
        </Column>
        <Column :header="t('overview.col_status')">
          <template #body="{ data: o }">
            <Tag :severity="statusSeverity(o.status)" :value="t('order.status.' + o.status)" />
          </template>
        </Column>
        <Column :header="t('overview.col_date')">
          <template #body="{ data: o }">{{ dateTime(o.created_at, lang) }}</template>
        </Column>
      </DataTable>
    </template>
  </Card>
</template>

<style scoped>
.big {
  font-size: 1.9rem;
  font-weight: 700;
  line-height: 1.2;
}
.mid {
  font-size: 1.15rem;
  font-weight: 700;
}
.warn {
  color: var(--p-orange-500);
}
.bad {
  color: var(--p-red-500);
}
.detail {
  margin-block-start: 0.4rem;
  font-size: 0.85rem;
}
.recent {
  margin-block-start: var(--app-gap);
}
</style>
