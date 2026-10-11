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
import ReasonDialog from '../components/ReasonDialog.vue'
import PaymentsTable from '../components/PaymentsTable.vue'
import { api } from '../api'
import { errorText } from '../errors'
import { useAuth } from '../stores/auth'
import { bytes, dateTime, money, num, planName, shortId, type Lang } from '../format'
import type { OrderItem, PaymentItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()
const toast = useToast()
const auth = useAuth()
const id = computed(() => route.params.id as string)

interface Detail {
  order: OrderItem
  payments: PaymentItem[] | null
  payments_error: string
  can: { retry: boolean; refund: boolean }
}
const d = ref<Detail | null>(null)
const loadError = ref('')
async function load() {
  loadError.value = ''
  try {
    d.value = await api<Detail>('GET', `/orders/${id.value}`)
  } catch (e) {
    loadError.value = errorText(e, t).text
  }
}
onMounted(load)
watch(id, load)
const o = computed(() => d.value?.order)

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
const busy = ref(false)
const refundError = ref('')
const refundDetail = ref('')
const delivered = computed(() => o.value?.status === 'active')
async function refund(reason: string) {
  busy.value = true
  refundError.value = ''
  try {
    const res = await api<{ wallet: { amount: number; currency: string }; warning?: string }>('POST', `/orders/${id.value}/refund`, { reason })
    refundOpen.value = false
    toast.add({
      severity: res.warning ? 'warn' : 'success',
      summary: t('order.refund_done', { balance: money(res.wallet, lang.value) }),
      detail: res.warning,
      life: 7000,
    })
    await load()
  } catch (e) {
    const err = errorText(e, t)
    refundError.value = err.text
    refundDetail.value = err.detail
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">
      <Button icon="pi pi-arrow-left" class="app-flip" text rounded :aria-label="t('common.back')" @click="router.push({ name: 'payments', query: { tab: 'orders' } })" />
      {{ t('order.title') }}
      <span v-if="o" class="app-ltr app-mono app-muted">{{ shortId(o.id) }}</span>
    </h1>
    <div v-if="d" class="app-actions">
      <RouterLink v-if="auth.can('audit.read')" :to="{ name: 'audit', query: { entity_id: d.order.id } }" class="p-button p-component p-button-text history">
        <i class="pi pi-history" aria-hidden="true" />
        <span>{{ t('audit.history') }}</span>
      </RouterLink>
      <Button v-if="d.can.retry && auth.can('payments.review')" :label="t('order.retry')" icon="pi pi-replay" :loading="retrying" @click="retry" />
      <Button
        v-if="d.can.refund && auth.can('payments.review')"
        :label="t('order.refund')"
        icon="pi pi-undo"
        severity="danger"
        outlined
        @click="(refundError = ''), (refundOpen = true)"
      />
    </div>
  </div>
  <Message v-if="loadError" severity="error" :closable="false">{{ loadError }}</Message>

  <Card>
    <template #content>
      <dl v-if="o" class="app-facts">
        <dt>{{ t('common.customer') }}</dt>
        <dd><UserLink :user="o.user" /></dd>
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
        </dd>
        <template v-if="o.staff">
          <dt>{{ t('order.made_by') }}</dt>
          <dd>
            <bdi>{{ o.staff }}</bdi>
            <span v-if="o.extend" class="app-muted">
              ·
              <template v-if="o.extend.days">{{ t('plan.days', { n: num(o.extend.days, lang) }) }}</template>
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
            {{ t('order.attempts', { n: num(o.attempts, lang) }) }}
            <template v-if="o.status === 'provision_failed' && o.next_attempt_at">
              · {{ t('order.next_attempt', { time: dateTime(o.next_attempt_at, lang) }) }}
            </template>
            <div v-if="o.last_error && o.status === 'provision_failed'" class="app-ltr error">{{ o.last_error }}</div>
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

  <ReasonDialog
    v-model:visible="refundOpen"
    :title="t('order.refund')"
    :message="o ? t(delivered ? 'order.refund_intro_delivered' : 'order.refund_intro', { amount: money(o.amount, lang) }) : ''"
    :confirm-label="t('order.refund')"
    danger
    :busy="busy"
    :error="refundError"
    :detail="refundDetail"
    @confirm="refund"
  />
</template>

<style scoped>
.history {
  text-decoration: none;
  gap: 0.5rem;
}
.payments {
  margin-block-start: var(--app-gap);
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
