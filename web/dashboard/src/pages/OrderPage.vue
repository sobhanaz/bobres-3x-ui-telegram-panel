<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import UserLink from '../components/UserLink.vue'
import StatusTag from '../components/StatusTag.vue'
import PaymentsTable from '../components/PaymentsTable.vue'
import SellerLink from '../components/orders/SellerLink.vue'
import MoveDialog from '../components/orders/MoveDialog.vue'
import RefundDialog from '../components/orders/RefundDialog.vue'
import { api } from '../api'
import { errorText } from '../errors'
import { useAuth } from '../stores/auth'
import { bytes, dateTime, money, num, percentBP, planName, shortId, type Lang } from '../format'
import { moveServers, serverName } from '../undelivered'
import type { OrderDetail, UndeliveredServer } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()
const toast = useToast()
const auth = useAuth()
const id = computed(() => route.params.id as string)

const d = ref<OrderDetail | null>(null)
const loadError = ref('')
async function load() {
  loadError.value = ''
  try {
    d.value = await api<OrderDetail>('GET', `/orders/${id.value}`)
  } catch (e) {
    loadError.value = errorText(e, t).text
  }
  if (d.value && (d.value.can.move || d.value.order.server_id)) void loadServers()
}
onMounted(load)
watch(id, load)
const o = computed(() => d.value?.order)

// The servers a new service may be moved to (and the name of the one it was moved to): read from the undelivered
// list, which payments.read may read (the servers list itself is servers.write).
const servers = ref<UndeliveredServer[]>([])
const serversError = ref('')
const serversLoading = ref(false)
async function loadServers() {
  serversLoading.value = true
  try {
    const res = await moveServers()
    servers.value = res.servers
    serversError.value = res.servers_error
  } catch (e) {
    serversError.value = errorText(e, t).text
  } finally {
    serversLoading.value = false
  }
}
const chosenServer = computed(() => serverName(o.value?.server_id, servers.value))
const moveOpen = ref(false)
function openMove() {
  moveOpen.value = true
  void loadServers() // their health now, not when the page opened
}

const retrying = ref(false)
async function retry() {
  retrying.value = true
  try {
    await api('POST', `/orders/${id.value}/retry`)
    toast.add({ severity: 'success', summary: t('order.retry_started'), life: 4000 })
    await load()
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 6000 })
  } finally {
    retrying.value = false
  }
}

const refundOpen = ref(false)
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">
      <Button icon="pi pi-arrow-left" class="app-flip" text rounded :aria-label="t('common.back')" @click="router.push({ name: 'payments', query: { tab: o?.status === 'provision_failed' ? 'undelivered' : 'orders' } })" />
      {{ t('order.title') }}
      <span v-if="o" class="app-ltr app-mono app-muted">{{ shortId(o.id) }}</span>
    </h1>
    <div v-if="d" class="app-actions">
      <RouterLink v-if="auth.can('audit.read')" :to="{ name: 'audit', query: { entity_id: d.order.id } }" class="p-button p-component p-button-text history">
        <i class="pi pi-history" aria-hidden="true" />
        <span>{{ t('audit.history') }}</span>
      </RouterLink>
      <Button v-if="d.can.retry && auth.can('payments.review')" :label="t('order.retry')" icon="pi pi-replay" :loading="retrying" @click="retry" />
      <Button v-if="d.can.move && auth.can('payments.review')" :label="t('order.move')" icon="pi pi-server" outlined class="move" @click="openMove" />
      <Button v-if="d.can.refund && auth.can('payments.review')" :label="t('order.refund')" icon="pi pi-undo" severity="danger" outlined @click="refundOpen = true" />
    </div>
  </div>
  <Message v-if="loadError" severity="error" :closable="false">{{ loadError }}</Message>

  <Card>
    <template #content>
      <dl v-if="o" class="app-facts">
        <dt>{{ t('common.customer') }}</dt>
        <dd><UserLink :user="o.user" /></dd>
        <template v-if="o.seller">
          <dt>{{ t('order.seller') }}</dt>
          <dd class="seller"><SellerLink :seller="o.seller" inline /></dd>
        </template>
        <dt>{{ t('common.plan') }}</dt>
        <dd><bdi>{{ planName(o.plan.name, lang) }}</bdi> · {{ t('order.type.' + o.type) }}</dd>
        <dt>{{ t('common.status') }}</dt>
        <dd><StatusTag kind="order" :status="o.status" /></dd>
        <dt>{{ t('common.amount') }}</dt>
        <dd>
          {{ o.amount.amount ? money(o.amount, lang) : t('plan.free') }}
          <span v-if="o.discount" class="app-muted">
            ({{ t('order.discount', { amount: money(o.discount.amount, lang) }) }} <span class="app-ltr app-mono">{{ o.discount.code }}</span>)
          </span>
          <div v-if="o.list" class="app-muted list-price">
            {{ t('order.list', { price: money(o.list.price, lang), rate: money({ amount: o.list.rate, currency: o.amount.currency }, lang) }) }}
          </div>
        </dd>
        <template v-if="o.cashback">
          <dt>{{ t('order.cashback') }}</dt>
          <dd class="cashback">
            {{ t('order.cashback_line', { amount: money(o.cashback.amount, lang), percent: percentBP(o.cashback.percent_bp, lang), base: money(o.amount, lang) }) }}
            <span v-if="o.cashback.capped" class="app-muted">({{ t('order.cashback_capped') }})</span>
            <span v-if="o.cashback.reversed" class="app-muted">· {{ t('order.cashback_reversed', { amount: money(o.cashback.reversed, lang) }) }}</span>
          </dd>
        </template>
        <template v-if="o.staff">
          <dt>{{ t('order.made_by') }}</dt>
          <dd>
            <bdi>{{ o.staff }}</bdi>
            <span v-if="o.extend" class="app-muted">
              ·
              <template v-if="o.extend.days">{{ t('plan.days', { n: num(o.extend.days, lang) }, o.extend.days) }}</template>
              <template v-if="o.extend.days && o.extend.bytes"> + </template>
              <template v-if="o.extend.bytes">{{ bytes(o.extend.bytes, lang) }}</template>
            </span>
          </dd>
        </template>
        <template v-if="o.subscription_id || o.status === 'active'">
          <dt>{{ t('order.service') }}</dt>
          <dd>
            <RouterLink v-if="o.subscription_id" :to="{ name: 'service', params: { id: o.subscription_id } }" class="app-ltr app-mono">{{ shortId(o.subscription_id) }}</RouterLink>
            <span v-else class="app-muted">{{ t('order.service_new') }}</span>
          </dd>
        </template>
        <dt>{{ t('order.created') }}</dt>
        <dd>{{ dateTime(o.created_at, lang) }}</dd>
        <template v-if="o.attempts">
          <dt>{{ t('order.deliveries') }}</dt>
          <dd>
            {{ t('order.attempts', { n: num(o.attempts, lang) }, o.attempts) }}
            <template v-if="o.status === 'provision_failed' && o.next_attempt_at">
              · {{ t('order.next_attempt', { time: dateTime(o.next_attempt_at, lang) }) }}
            </template>
            <div v-if="o.last_error && o.status === 'provision_failed'" class="app-ltr error">{{ o.last_error }}</div>
          </dd>
        </template>
        <template v-if="o.server_id">
          <dt>{{ t('order.server') }}</dt>
          <dd class="chosen-server">
            <bdi v-if="chosenServer">{{ chosenServer }}</bdi>
            <span v-else class="app-ltr app-mono">{{ shortId(o.server_id) }}</span>
            <span class="app-muted"> · {{ t('order.server_chosen') }}</span>
          </dd>
        </template>
        <template v-if="o.refund">
          <dt>{{ t('order.refunded') }}</dt>
          <dd>{{ money(o.refund.amount, lang) }} · {{ dateTime(o.refund.at, lang) }}</dd>
        </template>
      </dl>
      <Skeleton v-else height="12rem" />
    </template>
  </Card>

  <Card class="payments">
    <template #title>{{ t('order.payments') }}</template>
    <template #content>
      <Message v-if="d?.payments_error" severity="warn" :closable="false">{{ t('order.payments_unavailable') }}</Message>
      <PaymentsTable v-else :items="d?.payments ?? []" :lazy="false" hide-user />
    </template>
  </Card>

  <MoveDialog v-model:visible="moveOpen" :order="o ?? null" :servers="servers" :servers-error="serversError" :servers-loading="serversLoading" @moved="load" />
  <RefundDialog v-model:visible="refundOpen" :order="o ?? null" @refunded="load" />
</template>

<style scoped>
.history {
  text-decoration: none;
  gap: 0.5rem;
}
.payments {
  margin-block-start: var(--app-gap);
}
.list-price {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
}
.error {
  color: var(--p-red-500);
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
}
.app-page-title {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}
</style>
