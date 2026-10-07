<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import Image from 'primevue/image'
import Tabs from 'primevue/tabs'
import TabList from 'primevue/tablist'
import Tab from 'primevue/tab'
import TabPanels from 'primevue/tabpanels'
import TabPanel from 'primevue/tabpanel'
import InputText from 'primevue/inputtext'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import Select from 'primevue/select'
import UserLink from '../components/UserLink.vue'
import ReasonDialog from '../components/ReasonDialog.vue'
import OrdersTable from '../components/OrdersTable.vue'
import PaymentsTable from '../components/PaymentsTable.vue'
import { api } from '../api'
import { errorText } from '../errors'
import { useAuth } from '../stores/auth'
import { useList } from '../composables/useList'
import { dateTime, money, num, type Lang } from '../format'
import type { OrderItem, PaymentItem, PendingItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()
const toast = useToast()
const auth = useAuth()

// The tab is in the address (?tab=), so a link can open the queue.
const tab = ref(String(route.query.tab ?? 'queue'))
watch(tab, (v) => void router.replace({ query: { ...route.query, tab: v } }))

// Review queue.
const pending = ref<PendingItem[]>([])
const pendingLoading = ref(true)
const pendingError = ref('')
async function loadPending() {
  pendingLoading.value = true
  pendingError.value = ''
  try {
    pending.value = (await api<{ items: PendingItem[] }>('GET', '/payments/pending')).items
  } catch (e) {
    pendingError.value = errorText(e, t).text
  } finally {
    pendingLoading.value = false
  }
}

const orders = useList<OrderItem, { q: string; status: string; type: string }>('/orders', { q: '', status: '', type: '' })
const history = useList<PaymentItem, { status: string; provider: string }>('/payments', { status: '', provider: '' })
const loaded = new Set<string>()
function showTab(v: string | number) {
  tab.value = String(v)
  if (loaded.has(tab.value)) return
  loaded.add(tab.value)
  if (tab.value === 'queue') void loadPending()
  if (tab.value === 'orders') void orders.load()
  if (tab.value === 'history') void history.load()
}
onMounted(() => showTab(tab.value))

const orderStatuses = computed(() => [
  { label: t('common.all'), value: '' },
  ...['awaiting_payment', 'paid', 'provisioning', 'active', 'provision_failed', 'cancelled', 'expired', 'created'].map((s) => ({
    label: t('order.status.' + s),
    value: s,
  })),
])
const orderTypes = computed(() => [
  { label: t('order.all_types'), value: '' },
  ...['new', 'renew', 'traffic_topup'].map((s) => ({ label: t('order.type.' + s), value: s })),
])
const paymentStatuses = computed(() => [
  { label: t('common.all'), value: '' },
  ...['pending', 'confirming', 'succeeded', 'failed', 'expired'].map((s) => ({ label: t('payment.status.' + s), value: s })),
])
const providers = computed(() => [
  { label: t('payment.all_methods'), value: '' },
  ...['manual_card', 'manual_crypto', 'manual_zarinpal', 'zarinpal', 'stars', 'wallet'].map((s) => ({
    label: t('payment.provider.' + s),
    value: s,
  })),
])

// Approve or reject. A rejection's reason goes to the customer.
const reviewing = ref<{ item: PendingItem; decision: 'approved' | 'rejected' } | null>(null)
const reviewOpen = computed({ get: () => reviewing.value !== null, set: (v) => !v && (reviewing.value = null) })
const busy = ref(false)
const reviewError = ref('')
const reviewDetail = ref('')
function review(item: PendingItem, decision: 'approved' | 'rejected') {
  reviewError.value = ''
  reviewDetail.value = ''
  reviewing.value = { item, decision }
}
async function confirmReview(reason: string) {
  if (!reviewing.value) return
  const { item, decision } = reviewing.value
  busy.value = true
  reviewError.value = ''
  try {
    await api('POST', `/payments/${item.id}/review`, { decision, reason })
    reviewing.value = null
    pending.value = pending.value.filter((p) => p.id !== item.id)
    toast.add({ severity: 'success', summary: decision === 'approved' ? t('payment.approved') : t('payment.rejected'), life: 4000 })
    loaded.delete('orders')
    loaded.delete('history')
  } catch (e) {
    const err = errorText(e, t)
    reviewError.value = err.text
    reviewDetail.value = err.detail
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.payments.title') }}</h1>
  </div>
  <Tabs :value="tab" @update:value="showTab">
    <TabList>
      <Tab value="queue">
        {{ t('payment.queue') }}
        <Tag v-if="pending.length" :value="num(pending.length, lang)" severity="warn" />
      </Tab>
      <Tab value="orders">{{ t('payment.orders') }}</Tab>
      <Tab value="history">{{ t('payment.history') }}</Tab>
    </TabList>
    <TabPanels>
      <TabPanel value="queue">
        <Message v-if="pendingError" severity="error" :closable="false">{{ pendingError }}</Message>
        <p v-else-if="!pendingLoading && !pending.length" class="app-muted empty">{{ t('payment.queue_empty') }}</p>
        <div class="queue">
          <Card v-for="p in pending" :key="p.id" class="receipt">
            <template #content>
              <div class="receipt-body">
                <div class="photo">
                  <Image v-if="p.has_file" :src="`/api/v1/payments/${p.id}/receipt`" :alt="t('payment.receipt')" preview image-class="thumb" />
                  <div v-else class="no-photo app-muted"><i class="pi pi-image" aria-hidden="true" />{{ t('payment.no_photo') }}</div>
                </div>
                <dl class="app-facts">
                  <dt>{{ t('common.customer') }}</dt>
                  <dd><UserLink :user="p.user" /></dd>
                  <dt>{{ t('common.amount') }}</dt>
                  <dd class="amount">{{ money(p.amount, lang) }}</dd>
                  <dt>{{ t('payment.method') }}</dt>
                  <dd>{{ t('payment.provider.' + p.provider) }}</dd>
                  <template v-if="p.reference">
                    <dt>{{ t('payment.reference') }}</dt>
                    <dd>
                      <span class="app-ltr app-mono">{{ p.reference }}</span>
                      <Tag v-if="p.possible_duplicate" :value="t('payment.duplicate')" severity="warn" class="dup" />
                    </dd>
                  </template>
                  <template v-if="p.txid">
                    <dt>{{ t('payment.txid') }}</dt>
                    <dd><span class="app-ltr app-mono txid">{{ p.network }} · {{ p.txid }}</span></dd>
                  </template>
                  <dt>{{ t('payment.sent') }}</dt>
                  <dd>{{ dateTime(p.submitted_at, lang) }}</dd>
                  <template v-if="p.order_id">
                    <dt>{{ t('payment.for') }}</dt>
                    <dd>
                      <RouterLink :to="{ name: 'order', params: { id: p.order_id } }">{{ t('payment.order_link') }}</RouterLink>
                    </dd>
                  </template>
                  <template v-else>
                    <dt>{{ t('payment.for') }}</dt>
                    <dd>{{ t('payment.topup') }}</dd>
                  </template>
                </dl>
              </div>
              <Message v-if="p.possible_duplicate" severity="warn" :closable="false" class="dup-msg">{{ t('payment.duplicate_hint') }}</Message>
              <div v-if="auth.can('payments.review')" class="review">
                <Button :label="t('payment.approve')" icon="pi pi-check" severity="success" @click="review(p, 'approved')" />
                <Button :label="t('payment.reject')" icon="pi pi-times" severity="danger" outlined @click="review(p, 'rejected')" />
              </div>
            </template>
          </Card>
        </div>
      </TabPanel>

      <TabPanel value="orders">
        <div class="app-toolbar">
          <IconField class="app-grow">
            <InputIcon class="pi pi-search" />
            <InputText v-model="orders.filters.q" :placeholder="t('order.search')" fluid :aria-label="t('order.search')" />
          </IconField>
          <Select v-model="orders.filters.status" :placeholder="t('common.all')" :options="orderStatuses" option-label="label" option-value="value" :aria-label="t('common.status')" />
          <Select v-model="orders.filters.type" :placeholder="t('order.all_types')" :options="orderTypes" option-label="label" option-value="value" :aria-label="t('order.type_label')" />
        </div>
        <Message v-if="orders.error.value" severity="error" :closable="false">{{ orders.error.value }}</Message>
        <OrdersTable
          :items="orders.items.value" :total="orders.total.value" :rows="orders.rows.value"
          :first="(orders.page.value - 1) * orders.rows.value" :loading="orders.loading.value"
          @page="orders.onPage" @open="(o) => router.push({ name: 'order', params: { id: o.id } })"
        />
      </TabPanel>

      <TabPanel value="history">
        <div class="app-toolbar">
          <Select v-model="history.filters.status" :placeholder="t('common.all')" :options="paymentStatuses" option-label="label" option-value="value" :aria-label="t('common.status')" />
          <Select v-model="history.filters.provider" :placeholder="t('payment.all_methods')" :options="providers" option-label="label" option-value="value" :aria-label="t('payment.method')" />
        </div>
        <Message v-if="history.error.value" severity="error" :closable="false">{{ history.error.value }}</Message>
        <PaymentsTable
          :items="history.items.value" :total="history.total.value" :rows="history.rows.value"
          :first="(history.page.value - 1) * history.rows.value" :loading="history.loading.value"
          @page="history.onPage" @open="(p) => p.order_id && router.push({ name: 'order', params: { id: p.order_id } })"
        />
      </TabPanel>
    </TabPanels>
  </Tabs>

  <ReasonDialog
    v-model:visible="reviewOpen"
    :title="reviewing?.decision === 'approved' ? t('payment.approve') : t('payment.reject')"
    :message="reviewing?.decision === 'approved'
      ? t('payment.approve_intro', { amount: reviewing ? money(reviewing.item.amount, lang) : '' })
      : t('payment.reject_intro')"
    :confirm-label="reviewing?.decision === 'approved' ? t('payment.approve') : t('payment.reject')"
    :danger="reviewing?.decision === 'rejected'"
    :reason-optional="reviewing?.decision === 'approved'"
    :busy="busy"
    :error="reviewError"
    :detail="reviewDetail"
    @confirm="confirmReview"
  />
</template>

<style scoped>
.queue {
  display: grid;
  gap: var(--app-gap);
  grid-template-columns: repeat(auto-fill, minmax(min(26rem, 100%), 1fr));
}
.receipt-body {
  display: flex;
  gap: 1rem;
  flex-wrap: wrap;
}
.photo :deep(.thumb) {
  inline-size: 8rem;
  block-size: 10rem;
  object-fit: cover;
  border-radius: 0.5rem;
  background: var(--p-content-hover-background);
}
.no-photo {
  inline-size: 8rem;
  block-size: 10rem;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  border: 1px dashed var(--p-content-border-color);
  border-radius: 0.5rem;
  font-size: 0.85rem;
  text-align: center;
}
.receipt-body .app-facts {
  flex: 1 1 14rem;
}
.amount {
  font-weight: 700;
  font-size: 1.1rem;
}
.txid {
  word-break: break-all;
}
.dup {
  margin-inline-start: 0.4rem;
}
.dup-msg {
  margin-block-start: 0.75rem;
}
.review {
  display: flex;
  gap: 0.5rem;
  margin-block-start: 1rem;
}
.empty {
  padding-block: 1rem;
}
</style>
