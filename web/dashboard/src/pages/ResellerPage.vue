<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import Tabs from 'primevue/tabs'
import TabList from 'primevue/tablist'
import Tab from 'primevue/tab'
import TabPanels from 'primevue/tabpanels'
import TabPanel from 'primevue/tabpanel'
import UserLink from '../components/UserLink.vue'
import StatusTag from '../components/StatusTag.vue'
import ReasonDialog from '../components/ReasonDialog.vue'
import ServicesTable from '../components/ServicesTable.vue'
import OrdersTable from '../components/OrdersTable.vue'
import LedgerTable from '../components/LedgerTable.vue'
import UsersTable from '../components/UsersTable.vue'
import BalanceText from '../components/resellers/BalanceText.vue'
import PaymentDialog from '../components/resellers/PaymentDialog.vue'
import TermsDialog from '../components/resellers/TermsDialog.vue'
import { api, ApiError, query } from '../api'
import { errorText } from '../errors'
import { useAuth } from '../stores/auth'
import { useList } from '../composables/useList'
import { date, money, num, type Lang, type Money } from '../format'
import { owed, percent, resellerOf, type Reseller } from '../resellers'
import type { LedgerItem, OrderItem, ResellerDetail, ServiceItem, UserItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()
const toast = useToast()
const auth = useAuth()
const id = computed(() => route.params.id as string)

const reseller = ref<Reseller | null>(null)
const loadError = ref('')
const gone = ref(false)
async function load() {
  loadError.value = ''
  try {
    reseller.value = resellerOf((await api<{ item: ResellerDetail }>('GET', `/resellers/${id.value}`)).item)
    gone.value = false
  } catch (e) {
    // Not (or no longer) a reseller: their customer page still is.
    gone.value = e instanceof ApiError && e.status === 404
    loadError.value = gone.value ? t('reseller.not_reseller') : errorText(e, t).text
  }
}
/** take uses a change's answer when it carries the item, else reads it again. */
function take(item: ResellerDetail | undefined) {
  if (item?.user) reseller.value = resellerOf(item)
  else void load()
}

// The tabs, in the address (?tab=), each loading when first shown.
const services = useList<ServiceItem, { user: string }>('/services', { user: id.value }, 10)
const customers = useList<UserItem, { referred_by: string }>('/users', { referred_by: id.value }, 10)
const ledger = useList<LedgerItem, { user: string }>('/ledger', { user: id.value }, 10)
const orders = useList<OrderItem, { user: string }>('/orders', { user: id.value }, 10)
const tabs = ['services', 'customers', 'ledger', 'orders']
const tabOf = (v: unknown) => (typeof v === 'string' && tabs.includes(v) ? v : 'services')
const tab = ref(tabOf(route.query.tab))
const loaded = new Set<string>()
function showTab(v: string | number) {
  tab.value = tabOf(String(v))
  if (route.query.tab !== tab.value && (route.query.tab || tab.value !== 'services')) {
    void router.replace({ query: { ...route.query, tab: tab.value } })
  }
  if (loaded.has(tab.value)) return
  loaded.add(tab.value)
  ;({ services, customers, ledger, orders })[tab.value as 'services'].load()
}
onMounted(() => {
  void load()
  showTab(tab.value)
})
watch(id, () => {
  reseller.value = null
  loaded.clear()
  for (const l of [services, ledger, orders]) l.filters.user = id.value
  customers.filters.referred_by = id.value
  void load()
  showTab(tab.value)
})
const csvHref = computed(() => '/api/v1/ledger.csv' + query({ user: id.value }))

// Money moved: the statement shows it the next time it is opened.
function moneyMoved() {
  loaded.delete('ledger')
  if (tab.value === 'ledger') showTab('ledger')
}

const paying = ref(false)
function paid(res: { item?: ResellerDetail; entry?: LedgerItem }) {
  take(res.item)
  const after = res.entry?.balance_after
  toast.add({ severity: 'success', summary: after ? t('reseller.payment_done', { balance: balanceWords(after) }) : t('reseller.payment_recorded'), life: 5000 })
  moneyMoved()
}
// A balance in words: what they owe reads as debt, never as a bare minus.
function balanceWords(m: Money): string {
  const debt = owed(m)
  return debt ? t('reseller.debt_amount', { amount: money(debt, lang.value) }) : money(m, lang.value)
}

const terming = ref(false)
function termsSaved(item: ResellerDetail | undefined) {
  take(item)
  toast.add({ severity: 'success', summary: t('reseller.terms_saved'), life: 4000 })
}

// Remove: back to a regular customer. Core refuses it while they owe money.
const removing = ref(false)
const busy = ref(false)
const actionError = ref('')
const actionDetail = ref('')
function openRemove() {
  actionError.value = ''
  actionDetail.value = ''
  removing.value = true
}
async function remove(reason: string) {
  busy.value = true
  actionError.value = ''
  try {
    await api('POST', `/resellers/${id.value}/remove`, { reason })
    removing.value = false
    toast.add({ severity: 'success', summary: t('reseller.removed'), life: 4000 })
    void router.push({ name: 'user', params: { id: id.value } })
  } catch (e) {
    const err = errorText(e, t)
    actionError.value = err.text
    actionDetail.value = err.detail
    if (e instanceof ApiError && e.status === 409) void load()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">
      <Button icon="pi pi-arrow-left" class="app-flip back" text rounded :aria-label="t('common.back')" @click="router.push({ name: 'resellers' })" />
      <template v-if="reseller">
        <UserLink :user="reseller.user" :link="false" />
        <Tag :value="reseller.tier.name" severity="info" class="tier-tag" />
      </template>
      <template v-else>{{ t('reseller.title') }}</template>
    </h1>
    <div v-if="reseller" class="app-actions">
      <RouterLink v-if="auth.can('audit.read')" :to="{ name: 'audit', query: { entity_id: id } }" class="p-button p-component p-button-text link-button">
        <i class="pi pi-history" aria-hidden="true" />
        <span>{{ t('audit.history') }}</span>
      </RouterLink>
      <RouterLink :to="{ name: 'user', params: { id } }" class="p-button p-component p-button-text link-button customer-page">
        <i class="pi pi-user" aria-hidden="true" />
        <span>{{ t('reseller.customer_page') }}</span>
      </RouterLink>
      <Button :label="t('reseller.record_payment')" icon="pi pi-wallet" class="pay" @click="paying = true" />
      <Button :label="t('reseller.change_terms')" icon="pi pi-sliders-h" outlined class="terms" @click="terming = true" />
      <span v-tooltip.bottom="reseller.canRemove ? undefined : t('reseller.remove_blocked')" class="remove-wrap">
        <Button
          :label="t('reseller.remove')"
          icon="pi pi-user-minus"
          severity="danger"
          outlined
          :disabled="!reseller.canRemove"
          aria-describedby="remove-hint"
          class="remove"
          @click="openRemove"
        />
      </span>
    </div>
  </div>
  <p v-if="reseller && !reseller.canRemove" id="remove-hint" class="app-muted hint">{{ t('reseller.remove_blocked') }}</p>
  <Message v-if="loadError" severity="error" :closable="false">
    {{ loadError }}
    <RouterLink v-if="gone" :to="{ name: 'user', params: { id } }" class="gone-link">{{ t('reseller.customer_page') }}</RouterLink>
  </Message>

  <template v-if="!gone">
    <div class="app-grid stats">
      <Card class="stat">
        <template #subtitle>{{ t('reseller.balance') }}</template>
        <template #content>
          <template v-if="reseller">
            <div v-for="w in reseller.wallets" :key="w.currency" class="big balance"><BalanceText :value="{ amount: w.balance, currency: w.currency }" /></div>
          </template>
          <Skeleton v-else height="2.5rem" />
        </template>
      </Card>
      <Card class="stat">
        <template #subtitle>{{ t('reseller.credit_limit') }}</template>
        <template #content>
          <template v-if="reseller">
            <div v-for="w in reseller.wallets" :key="w.currency" class="big limit">{{ money({ amount: w.limit, currency: w.currency }, lang) }}</div>
          </template>
          <Skeleton v-else height="2.5rem" />
        </template>
      </Card>
      <Card class="stat">
        <template #subtitle>{{ t('reseller.available') }}</template>
        <template #content>
          <template v-if="reseller">
            <div v-for="w in reseller.wallets" :key="w.currency" class="big available">
              <BalanceText :value="{ amount: w.available, currency: w.currency }" />
            </div>
          </template>
          <Skeleton v-else height="2.5rem" />
        </template>
      </Card>
      <Card class="stat">
        <template #subtitle>{{ t('reseller.customers') }}</template>
        <template #content>
          <div v-if="reseller" class="big customers">{{ num(reseller.customers, lang) }}</div>
          <Skeleton v-else height="2.5rem" />
        </template>
      </Card>
      <Card class="stat">
        <template #subtitle>{{ t('reseller.commission_earned') }}</template>
        <template #content>
          <template v-if="reseller">
            <div v-for="m in reseller.earned" :key="m.currency" class="big earned">{{ money(m, lang) }}</div>
            <div v-if="!reseller.earned.length" class="big app-muted">—</div>
          </template>
          <Skeleton v-else height="2.5rem" />
        </template>
      </Card>
    </div>

    <Card class="facts">
      <template #content>
        <dl v-if="reseller" class="app-facts">
          <dt>{{ t('reseller.tier') }}</dt>
          <dd>
            <bdi>{{ reseller.tier.name }}</bdi>
            <span v-if="reseller.discount !== null" class="app-muted"> · {{ t('reseller.discount_off', { n: percent(reseller.discount, lang) }) }}</span>
          </dd>
          <dt>{{ t('reseller.commission') }}</dt>
          <dd>
            {{ percent(reseller.commission, lang) }}
            <span class="app-muted"> · {{ reseller.override === null ? t('reseller.commission_from_tier') : t('reseller.commission_own') }}</span>
          </dd>
          <dt>{{ t('common.status') }}</dt>
          <dd><StatusTag kind="user" :status="reseller.user.status" /></dd>
          <dt>{{ t('reseller.since') }}</dt>
          <dd>{{ date(reseller.createdAt, lang) }}</dd>
          <dt>{{ t('reseller.note') }}</dt>
          <dd>
            <bdi v-if="reseller.note" class="note">{{ reseller.note }}</bdi>
            <span v-else class="app-muted">—</span>
          </dd>
        </dl>
        <Skeleton v-else height="7rem" />
      </template>
    </Card>

    <Card class="tabs">
      <template #content>
        <Tabs :value="tab" @update:value="showTab">
          <TabList>
            <Tab value="services">{{ t('user.services') }}</Tab>
            <Tab value="customers">
              {{ t('reseller.customers') }} <Tag v-if="reseller" :value="num(reseller.customers, lang)" severity="secondary" />
            </Tab>
            <Tab value="ledger">{{ t('reseller.statement') }}</Tab>
            <Tab value="orders">{{ t('user.orders') }}</Tab>
          </TabList>
          <TabPanels>
            <TabPanel value="services">
              <ServicesTable
                :items="services.items.value" :total="services.total.value" :rows="services.rows.value"
                :first="(services.page.value - 1) * services.rows.value" :loading="services.loading.value" hide-user
                @page="services.onPage" @open="(s) => router.push({ name: 'service', params: { id: s.id } })"
              />
            </TabPanel>
            <TabPanel value="customers">
              <p class="app-muted tab-hint">{{ t('reseller.customers_hint') }}</p>
              <Message v-if="customers.error.value" severity="error" :closable="false">{{ customers.error.value }}</Message>
              <UsersTable
                :items="customers.items.value" :total="customers.total.value" :rows="customers.rows.value"
                :first="(customers.page.value - 1) * customers.rows.value" :loading="customers.loading.value"
                :empty="t('reseller.customers_none')"
                @page="customers.onPage" @open="(u) => router.push({ name: 'user', params: { id: u.id } })"
              />
            </TabPanel>
            <TabPanel value="ledger">
              <div class="statement-head">
                <p class="app-muted tab-hint">{{ t('reseller.statement_hint') }}</p>
                <a v-if="auth.can('ledger.read')" :href="csvHref" download class="p-button p-component p-button-text link-button">
                  <i class="pi pi-download" aria-hidden="true" />
                  <span>{{ t('ledger.export') }}</span>
                </a>
              </div>
              <LedgerTable
                :items="ledger.items.value" :total="ledger.total.value" :rows="ledger.rows.value"
                :first="(ledger.page.value - 1) * ledger.rows.value" :loading="ledger.loading.value" hide-user
                @page="ledger.onPage"
              />
            </TabPanel>
            <TabPanel value="orders">
              <OrdersTable
                :items="orders.items.value" :total="orders.total.value" :rows="orders.rows.value"
                :first="(orders.page.value - 1) * orders.rows.value" :loading="orders.loading.value" hide-user
                @page="orders.onPage" @open="(o) => router.push({ name: 'order', params: { id: o.id } })"
              />
            </TabPanel>
          </TabPanels>
        </Tabs>
      </template>
    </Card>
  </template>

  <template v-if="reseller">
    <PaymentDialog v-model:visible="paying" :reseller="reseller" @paid="paid" />
    <TermsDialog v-model:visible="terming" :reseller="reseller" @saved="termsSaved" @stale="load" />
    <ReasonDialog
      v-model:visible="removing"
      :title="t('reseller.remove')"
      :message="t('reseller.remove_intro')"
      :confirm-label="t('reseller.remove')"
      danger
      :busy="busy"
      :error="actionError"
      :detail="actionDetail"
      @confirm="remove"
    />
  </template>
</template>

<style scoped>
.app-page-title {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  flex-wrap: wrap;
}
.back {
  margin-inline-end: 0.25rem;
}
.tier-tag {
  margin-inline-start: 0.5rem;
}
.link-button {
  text-decoration: none;
  gap: 0.5rem;
}
.remove-wrap {
  display: inline-block;
}
.hint {
  margin-block: calc(var(--app-gap) * -0.5) var(--app-gap);
  font-size: 0.85rem;
}
.gone-link {
  margin-inline-start: 0.5rem;
}
.big {
  font-size: 1.3rem;
  font-weight: 700;
  line-height: 1.5;
}
.facts,
.tabs {
  margin-block-start: var(--app-gap);
}
.note {
  white-space: pre-wrap;
}
.tab-hint {
  margin-block: 0 0.75rem;
  font-size: 0.85rem;
}
.statement-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}
</style>
