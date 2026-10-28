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
import { RouterLink } from 'vue-router'
import { api } from '../api'
import { useAuth } from '../stores/auth'
import { dateTime, money, num, type Money, type Lang } from '../format'
import { duration } from '../support'
import { serverSummary } from '../servers'
import SupplierBalance from '../components/supplier/SupplierBalance.vue'
import { overviewGatewaySeverity, overviewGatewayState } from '../gateways'
import type { OverviewGateway, OverviewServer } from '../types'

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
  /** panel_missing: services the panel has no client for; absent from older cores */
  services: { active: number; expiring: number; ended: number; panel_missing?: number }
  orders: { paid_24h: number; provision_failed: number }
  /** Revenue: only for staff with reports.read; absent for support. */
  revenue?: { day: Money[]; month: Money[] }
  pending_payments: number
  panel: { healthy: boolean; detail: string }
  recent_orders: RecentOrder[]
  /** Support (M4): tickets waiting for staff; absent from older cores. */
  tickets?: { waiting: number; oldest_waiting_at: number | null }
  /** Resellers (M5), for those who manage them: how many, and what they owe. */
  resellers?: { count: number; debt: Money[] }
  /** 3x-ui servers (M6), each with its health; panel stays their sum. */
  servers?: OverviewServer[]
  /** Payment methods (M6): offered or not, and whether payments go through. */
  gateways?: OverviewGateway[]
}

const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const auth = useAuth()
const data = ref<Overview | null>(null)
const failed = ref(false)
const loadedAt = ref(Math.floor(Date.now() / 1000))

async function load() {
  failed.value = false
  try {
    data.value = await api<Overview>('GET', '/overview')
    loadedAt.value = Math.floor(Date.now() / 1000)
  } catch {
    failed.value = true
  }
}
onMounted(load)

// The revenue cards: for those who may read the reports. Core leaves revenue
// out for others (support), and an answer without it shows no card, not an
// empty one.
const showRevenue = computed(() => auth.can('reports.read') && (!data.value || !!data.value.revenue))
// The support card: for those who answer tickets, when core reports them.
const tickets = computed(() => (auth.can('tickets.write') ? (data.value?.tickets ?? null) : null))
const oldestWait = computed(() => {
  const at = tickets.value?.oldest_waiting_at
  return tickets.value && tickets.value.waiting > 0 && at ? duration(Math.max(0, loadedAt.value - at), lang.value) : ''
})
// The reseller card: for those who manage resellers, once there are any.
const resellers = computed(() => {
  const r = auth.can('resellers.write') ? data.value?.resellers : undefined
  return r && r.count > 0 ? r : null
})
const owing = computed(() => (resellers.value?.debt ?? []).filter((m) => m.amount > 0))
// The servers card replaces the single panel card once core lists servers.
// Only servers taking new services count: a switched-off one is not checked.
const servers = computed(() => data.value?.servers ?? null)
const summary = computed(() => serverSummary(servers.value ?? []))
const serversTag = computed(() => {
  const { live, down } = summary.value
  if (!servers.value?.length) return { severity: 'warn', text: t('overview.servers_none') }
  if (!live) return { severity: 'warn', text: t('overview.servers_all_off') }
  if (down.length) return { severity: 'danger', text: t('overview.servers_down', { n: num(down.length, lang.value), total: num(live, lang.value) }) }
  return { severity: 'success', text: t('overview.servers_ok') }
})
const serverClients = computed(() => (servers.value ?? []).reduce((n, s) => n + s.clients, 0))
// "5 services on 2 servers": each noun follows its own number ("1 service on 1 server").
const serverClientsText = computed(() => {
  const serverCount = servers.value?.length ?? 0
  return t('overview.servers_clients', {
    services: t('overview.services_n', { n: num(serverClients.value, lang.value) }, serverClients.value),
    servers: t('overview.servers_n', { n: num(serverCount, lang.value) }, serverCount),
  })
})
// The owner's balance at the supplier, when the store resells digital accounts.
const showSupplier = computed(() => auth.can('private.features') && auth.can('gateways.write'))
const gateways = computed(() => (data.value?.gateways?.length ? data.value.gateways : null))
// The services the panel has no account for: a card only while there are any.
const missing = computed(() => data.value?.services.panel_missing ?? 0)
// A card opens its page for those who may use it; for others it is just a card.
function linkTo(perm: string, name: string, query?: Record<string, string>) {
  return auth.can(perm) ? { is: RouterLink, attrs: { to: { name, query }, class: 'card-link' } } : { is: 'div', attrs: {} }
}
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
    <component :is="linkTo('services.read', 'services', { panel: 'missing' }).is" v-if="missing > 0" v-bind="linkTo('services.read', 'services', { panel: 'missing' }).attrs" class="card-wrap missing-card">
      <Card class="stat">
        <template #subtitle>{{ t('overview.panel_missing') }}</template>
        <template #content>
          <div class="big warn">{{ num(missing, lang) }}</div>
          <div class="app-muted">{{ t('overview.panel_missing_hint') }}</div>
        </template>
      </Card>
    </component>
    <SupplierBalance v-if="showSupplier" quiet />
    <Card v-if="showRevenue" class="stat revenue-day">
      <template #subtitle>{{ t('overview.revenue_day') }}</template>
      <template #content>
        <div v-if="data?.revenue" class="mid">{{ moneyLine(data.revenue.day) }}</div>
        <Skeleton v-else height="3rem" />
      </template>
    </Card>
    <Card v-if="showRevenue" class="stat revenue-month">
      <template #subtitle>{{ t('overview.revenue_month') }}</template>
      <template #content>
        <div v-if="data?.revenue" class="mid">{{ moneyLine(data.revenue.month) }}</div>
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
    <RouterLink v-if="tickets" :to="{ name: 'support', query: { status: 'open' } }" class="card-link">
      <Card class="stat">
        <template #subtitle>{{ t('overview.tickets') }}</template>
        <template #content>
          <div class="big" :class="{ warn: tickets.waiting > 0 }">{{ num(tickets.waiting, lang) }}</div>
          <div class="app-muted">{{ oldestWait ? t('overview.tickets_oldest', { time: oldestWait }) : t('overview.tickets_none') }}</div>
        </template>
      </Card>
    </RouterLink>
    <RouterLink v-if="resellers" :to="{ name: 'resellers', query: owing.length ? { in_debt: '1' } : {} }" class="card-link resellers-card">
      <Card class="stat">
        <template #subtitle>{{ t('overview.reseller_debt') }}</template>
        <template #content>
          <div class="mid" :class="{ warn: owing.length > 0 }">{{ owing.length ? moneyLine(owing) : t('overview.reseller_no_debt') }}</div>
          <div class="app-muted">{{ t('overview.resellers_count', { n: num(resellers.count, lang) }, resellers.count) }}</div>
        </template>
      </Card>
    </RouterLink>
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
    <component :is="linkTo('servers.write', 'servers').is" v-if="servers" v-bind="linkTo('servers.write', 'servers').attrs" class="card-wrap servers-card">
      <Card class="stat">
        <template #subtitle>{{ t('overview.servers') }}</template>
        <template #content>
          <Tag :severity="serversTag.severity" :value="serversTag.text" class="servers-state" />
          <div v-for="s in summary.down" :key="s.id" class="down">
            <bdi>{{ s.name }}</bdi>
            <div v-if="s.detail" class="app-muted app-ltr detail">{{ s.detail }}</div>
          </div>
          <div v-if="servers.length" class="app-muted count">
            {{ serverClientsText }}
          </div>
        </template>
      </Card>
    </component>
    <Card v-else class="stat">
      <template #subtitle>{{ t('overview.panel') }}</template>
      <template #content>
        <template v-if="data">
          <Tag :severity="data.panel.healthy ? 'success' : 'danger'" :value="data.panel.healthy ? t('overview.panel_ok') : t('overview.panel_down')" />
          <div v-if="data.panel.detail" class="app-muted app-ltr detail">{{ data.panel.detail }}</div>
        </template>
        <Skeleton v-else height="3rem" />
      </template>
    </Card>
    <component :is="linkTo('gateways.write', 'gateways').is" v-if="gateways" v-bind="linkTo('gateways.write', 'gateways').attrs" class="card-wrap gateways-card">
      <Card class="stat">
        <template #subtitle>{{ t('overview.gateways') }}</template>
        <template #content>
          <div v-for="g in gateways" :key="g.provider" class="gateway">
            <span>{{ te('payment.provider.' + g.provider) ? t('payment.provider.' + g.provider) : g.provider }}</span>
            <Tag :severity="overviewGatewaySeverity[overviewGatewayState(g)]" :value="t('overview.gateway_state.' + overviewGatewayState(g))" class="gateway-state" />
          </div>
        </template>
      </Card>
    </component>
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
.down {
  margin-block-start: 0.4rem;
}
.count {
  margin-block-start: 0.4rem;
  font-size: 0.85rem;
}
.gateway {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}
.gateway + .gateway {
  margin-block-start: 0.4rem;
}
.card-link {
  display: block;
  color: inherit;
  text-decoration: none;
  border-radius: var(--p-card-border-radius, var(--app-radius));
}
.card-link:hover .stat {
  box-shadow: 0 0 0 1px var(--p-primary-color);
}
.card-link .stat,
.card-wrap .stat {
  block-size: 100%;
}
</style>
