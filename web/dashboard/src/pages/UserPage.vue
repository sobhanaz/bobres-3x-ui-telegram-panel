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
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import SelectButton from 'primevue/selectbutton'
import UserLink from '../components/UserLink.vue'
import StatusTag from '../components/StatusTag.vue'
import ReasonDialog from '../components/ReasonDialog.vue'
import ServicesTable from '../components/ServicesTable.vue'
import OrdersTable from '../components/OrdersTable.vue'
import PaymentsTable from '../components/PaymentsTable.vue'
import LedgerTable from '../components/LedgerTable.vue'
import { api, newKey } from '../api'
import { errorText } from '../errors'
import { useAuth } from '../stores/auth'
import { useList } from '../composables/useList'
import { asciiNumber, currencies, date, money, num, validAmount, type Lang } from '../format'
import type { LedgerItem, OrderItem, PaymentItem, ServiceItem, UserDetail } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()
const toast = useToast()
const auth = useAuth()
const id = computed(() => route.params.id as string)

const detail = ref<UserDetail | null>(null)
const loadError = ref('')
async function load() {
  loadError.value = ''
  try {
    detail.value = await api<UserDetail>('GET', `/users/${id.value}`)
  } catch (e) {
    loadError.value = errorText(e, t).text
  }
}

const services = useList<ServiceItem, { user: string }>('/services', { user: id.value }, 10)
const orders = useList<OrderItem, { user: string }>('/orders', { user: id.value }, 10)
const payments = useList<PaymentItem, { user: string }>('/payments', { user: id.value }, 10)
const ledger = useList<LedgerItem, { user: string }>('/ledger', { user: id.value }, 10)
const tab = ref('services')
const loaded = new Set<string>()
function showTab(v: string | number) {
  tab.value = String(v)
  if (loaded.has(tab.value)) return
  loaded.add(tab.value)
  ;({ services, orders, payments, ledger })[tab.value as 'services']?.load()
}
onMounted(() => {
  void load()
  showTab('services')
})
watch(id, () => {
  detail.value = null
  loaded.clear()
  for (const l of [services, orders, payments, ledger]) l.filters.user = id.value
  void load()
  showTab(tab.value)
})

// Balance: add or take money, with a reason; the request key makes a
// retried submit count once.
const balanceOpen = ref(false)
const direction = ref<'add' | 'take'>('add')
const amount = ref('')
const currency = ref<string>('IRT')
const balanceKey = ref('')
const busy = ref(false)
const actionError = ref('')
const actionDetail = ref('')
const amountOK = computed(() => validAmount(asciiNumber(amount.value), currency.value) && Number(asciiNumber(amount.value)) > 0)
function openBalance() {
  direction.value = 'add'
  amount.value = ''
  balanceKey.value = newKey()
  actionError.value = ''
  balanceOpen.value = true
}
async function adjust(reason: string) {
  busy.value = true
  actionError.value = ''
  try {
    const sign = direction.value === 'take' ? '-' : ''
    const res = await api<{ wallet: { amount: number; currency: string } }>('POST', `/users/${id.value}/balance`, {
      amount: sign + asciiNumber(amount.value), currency: currency.value, reason, key: balanceKey.value,
    })
    balanceOpen.value = false
    toast.add({ severity: 'success', summary: t('user.balance_done', { balance: money(res.wallet, lang.value) }), life: 5000 })
    await load()
    loaded.delete('ledger')
    if (tab.value === 'ledger') showTab('ledger')
  } catch (e) {
    const err = errorText(e, t)
    actionError.value = err.text
    actionDetail.value = err.detail
  } finally {
    busy.value = false
  }
}

// Ban or unban.
const banOpen = ref(false)
const banned = computed(() => detail.value?.user.status === 'banned')
async function setStatus(reason: string) {
  busy.value = true
  actionError.value = ''
  try {
    await api('POST', `/users/${id.value}/status`, { status: banned.value ? 'active' : 'banned', reason })
    banOpen.value = false
    toast.add({ severity: 'success', summary: banned.value ? t('user.unbanned') : t('user.banned'), life: 4000 })
    await load()
  } catch (e) {
    const err = errorText(e, t)
    actionError.value = err.text
    actionDetail.value = err.detail
  } finally {
    busy.value = false
  }
}
function openBan() {
  actionError.value = ''
  banOpen.value = true
}
const directions = computed(() => [
  { label: t('user.add_money'), value: 'add' },
  { label: t('user.take_money'), value: 'take' },
])
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">
      <Button icon="pi pi-arrow-left" class="app-flip back" text rounded :aria-label="t('common.back')" @click="router.push({ name: 'users' })" />
      <template v-if="detail"><UserLink :user="detail.user" :link="false" /></template>
      <template v-else>{{ t('user.title') }}</template>
    </h1>
    <div v-if="detail" class="app-actions">
      <Button v-if="auth.can('wallet.adjust')" :label="t('user.adjust_balance')" icon="pi pi-wallet" outlined @click="openBalance" />
      <Button
        v-if="detail.can_ban"
        :label="banned ? t('user.unban') : t('user.ban')"
        :icon="banned ? 'pi pi-check-circle' : 'pi pi-ban'"
        :severity="banned ? 'success' : 'danger'"
        outlined
        @click="openBan"
      />
    </div>
  </div>
  <Message v-if="loadError" severity="error" :closable="false">{{ loadError }}</Message>

  <div class="cols">
    <Card>
      <template #content>
        <dl v-if="detail" class="app-facts">
          <dt>{{ t('common.status') }}</dt>
          <dd><StatusTag kind="user" :status="detail.user.status" /></dd>
          <dt>{{ t('user.role') }}</dt>
          <dd>{{ detail.user.role === 'user' ? t('user.role_customer') : t('role.' + detail.user.role) }}</dd>
          <dt>{{ t('user.language') }}</dt>
          <dd>{{ detail.user.language === 'fa' ? 'فارسی' : 'English' }}</dd>
          <dt>{{ t('user.joined') }}</dt>
          <dd>{{ date(detail.user.created_at, lang) }}</dd>
          <dt>{{ t('user.invite_code') }}</dt>
          <dd><span v-if="detail.user.ref_code" class="app-ltr app-mono">{{ detail.user.ref_code }}</span><span v-else class="app-muted">—</span></dd>
        </dl>
        <Skeleton v-else height="8rem" />
      </template>
    </Card>
    <Card>
      <template #title>{{ t('user.wallet') }}</template>
      <template #content>
        <template v-if="detail">
          <div v-for="w in detail.user.wallets" :key="w.currency" class="balance">{{ money(w, lang) }}</div>
          <p v-if="!detail.user.wallets.length" class="app-muted">{{ t('user.wallet_empty') }}</p>
        </template>
        <Skeleton v-else height="3rem" />
      </template>
    </Card>
    <Card>
      <template #title>{{ t('user.invitations') }}</template>
      <template #content>
        <dl v-if="detail" class="app-facts">
          <dt>{{ t('user.invited_by') }}</dt>
          <dd><UserLink :user="detail.referral.referred_by" /></dd>
          <dt>{{ t('user.invited') }}</dt>
          <dd>{{ num(detail.referral.invited, lang) }}</dd>
          <dt>{{ t('user.rewards') }}</dt>
          <dd>
            <div v-for="m in detail.referral.earned" :key="m.currency">{{ money(m, lang) }}</div>
            <span v-if="!detail.referral.earned.length" class="app-muted">—</span>
          </dd>
        </dl>
        <Skeleton v-else height="5rem" />
      </template>
    </Card>
  </div>

  <Card class="tabs">
    <template #content>
      <Tabs :value="tab" @update:value="showTab">
        <TabList>
          <Tab value="services">{{ t('user.services') }} <Tag v-if="detail" :value="num(detail.counts.subscriptions, lang)" severity="secondary" /></Tab>
          <Tab value="orders">{{ t('user.orders') }} <Tag v-if="detail" :value="num(detail.counts.orders, lang)" severity="secondary" /></Tab>
          <Tab value="payments">{{ t('user.payments') }}</Tab>
          <Tab value="ledger">{{ t('user.ledger') }}</Tab>
        </TabList>
        <TabPanels>
          <TabPanel value="services">
            <ServicesTable
              :items="services.items.value" :total="services.total.value" :rows="services.rows.value"
              :first="(services.page.value - 1) * services.rows.value" :loading="services.loading.value" hide-user
              @page="services.onPage" @open="(s) => router.push({ name: 'service', params: { id: s.id } })"
            />
          </TabPanel>
          <TabPanel value="orders">
            <OrdersTable
              :items="orders.items.value" :total="orders.total.value" :rows="orders.rows.value"
              :first="(orders.page.value - 1) * orders.rows.value" :loading="orders.loading.value" hide-user
              @page="orders.onPage" @open="(o) => router.push({ name: 'order', params: { id: o.id } })"
            />
          </TabPanel>
          <TabPanel value="payments">
            <PaymentsTable
              :items="payments.items.value" :total="payments.total.value" :rows="payments.rows.value"
              :first="(payments.page.value - 1) * payments.rows.value" :loading="payments.loading.value" hide-user
              @page="payments.onPage" @open="(p) => p.order_id && router.push({ name: 'order', params: { id: p.order_id } })"
            />
          </TabPanel>
          <TabPanel value="ledger">
            <LedgerTable
              :items="ledger.items.value" :total="ledger.total.value" :rows="ledger.rows.value"
              :first="(ledger.page.value - 1) * ledger.rows.value" :loading="ledger.loading.value" hide-user
              @page="ledger.onPage"
            />
          </TabPanel>
        </TabPanels>
      </Tabs>
    </template>
  </Card>

  <ReasonDialog
    v-model:visible="balanceOpen"
    :title="t('user.adjust_balance')"
    :message="t('user.adjust_intro')"
    :confirm-label="direction === 'add' ? t('user.add_money') : t('user.take_money')"
    :danger="direction === 'take'"
    :busy="busy"
    :error="actionError"
    :detail="actionDetail"
    :can-confirm="amountOK"
    @confirm="adjust"
  >
    <div class="app-form">
      <SelectButton v-model="direction" :options="directions" option-label="label" option-value="value" :allow-empty="false" />
      <div class="app-row">
        <label>
          <span>{{ t('common.amount') }}</span>
          <InputText v-model="amount" inputmode="decimal" dir="ltr" autocomplete="off" />
        </label>
        <label>
          <span>{{ t('common.currency') }}</span>
          <Select v-model="currency" :options="currencies.map((c) => ({ value: c, label: t('currency.' + c) }))" option-label="label" option-value="value" />
        </label>
      </div>
    </div>
  </ReasonDialog>

  <ReasonDialog
    v-model:visible="banOpen"
    :title="banned ? t('user.unban') : t('user.ban')"
    :message="banned ? t('user.unban_intro') : t('user.ban_intro')"
    :confirm-label="banned ? t('user.unban') : t('user.ban')"
    :danger="!banned"
    :busy="busy"
    :error="actionError"
    :detail="actionDetail"
    @confirm="setStatus"
  />
</template>

<style scoped>
.cols {
  display: grid;
  gap: var(--app-gap);
  grid-template-columns: repeat(auto-fit, minmax(17rem, 1fr));
  align-items: start;
}
.tabs {
  margin-block-start: var(--app-gap);
}
.balance {
  font-size: 1.3rem;
  font-weight: 700;
}
.back {
  margin-inline-end: 0.25rem;
}
.app-page-title {
  display: flex;
  align-items: center;
  gap: 0.25rem;
}
</style>
