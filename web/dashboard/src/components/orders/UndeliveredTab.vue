<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Message from 'primevue/message'
import UserLink from '../UserLink.vue'
import SellerLink from './SellerLink.vue'
import MoveDialog from './MoveDialog.vue'
import RefundDialog from './RefundDialog.vue'
import { api } from '../../api'
import { errorText } from '../../errors'
import { useAuth } from '../../stores/auth'
import { useList } from '../../composables/useList'
import { ago, dateTime, money, num, planName, shortId, type Lang } from '../../format'
import { canMove, inTime, serverName } from '../../undelivered'
import type { OrderItem, UndeliveredPage } from '../../types'

// The Payments page's Undelivered tab: paid orders whose service could not be made yet, the longest waiting first
// (GET /orders/undelivered). Core tries each again on its own; staff may try one or all now, deliver a new service
// on another server, or refund one to the buyer's wallet. The page shows the count on the tab (total).
const emit = defineEmits<{ total: [n: number]; changed: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const router = useRouter()
const toast = useToast()
const confirm = useConfirm()
const auth = useAuth()

const list = useList<OrderItem, Record<string, string>>('/orders/undelivered', {})
const page = computed(() => list.raw.value as UndeliveredPage | null)
const servers = computed(() => page.value?.servers ?? [])
const serversError = computed(() => page.value?.servers_error ?? '')
watch(list.total, (n) => emit('total', n))

// "5 minutes ago" and "in 12 minutes" count from the last load, and move on while the tab is open.
const now = ref(Date.now())
let tick: ReturnType<typeof setInterval> | undefined
async function load() {
  await list.load()
  now.value = Date.now()
}
onMounted(() => {
  void load()
  tick = setInterval(() => (now.value = Date.now()), 30_000)
})
onBeforeUnmount(() => clearInterval(tick))
defineExpose({ load })

function nextTry(o: OrderItem): string {
  if (!o.next_attempt_at) return ''
  const left = inTime(o.next_attempt_at, now.value, lang.value)
  return left ? t('order.next_attempt', { time: left }) : t('undelivered.next_now')
}
const openErrors = reactive<Record<string, boolean>>({})

async function afterAction() {
  await load()
  emit('changed')
}

const retrying = ref('')
async function retry(o: OrderItem) {
  retrying.value = o.id
  try {
    await api('POST', `/orders/${o.id}/retry`)
    toast.add({ severity: 'success', summary: t('order.retry_started'), life: 4000 })
    await afterAction()
  } catch (e) {
    const err = errorText(e, t)
    toast.add({ severity: 'error', summary: err.text, detail: err.detail || undefined, life: 6000 })
  } finally {
    retrying.value = ''
  }
}

const retryingAll = ref(false)
function retryAll() {
  const n = list.total.value
  confirm.require({
    header: t('undelivered.retry_all'),
    message: t('undelivered.retry_all_confirm', { n: num(n, lang.value) }, n),
    acceptProps: { label: t('undelivered.retry_all') },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      retryingAll.value = true
      try {
        const res = await api<{ count: number }>('POST', '/orders/undelivered/retry')
        toast.add({
          severity: 'success',
          summary: res.count ? t('undelivered.retry_all_done', { n: num(res.count, lang.value) }, res.count) : t('undelivered.retry_all_none'),
          life: 5000,
        })
        await afterAction()
      } catch (e) {
        const err = errorText(e, t)
        toast.add({ severity: 'error', summary: err.text, detail: err.detail || undefined, life: 6000 })
      } finally {
        retryingAll.value = false
      }
    },
  })
}

const moving = ref<OrderItem | null>(null)
const moveOpen = ref(false)
function openMove(o: OrderItem) {
  moving.value = o
  moveOpen.value = true
}
const refunding = ref<OrderItem | null>(null)
const refundOpen = ref(false)
function openRefund(o: OrderItem) {
  refunding.value = o
  refundOpen.value = true
}
</script>

<template>
  <div class="app-toolbar">
    <p class="app-muted intro app-grow">{{ t('undelivered.intro') }}</p>
    <Button :label="t('undelivered.refresh')" icon="pi pi-refresh" text :loading="list.loading.value" class="refresh" @click="load" />
    <Button
      v-if="auth.can('payments.review')"
      :label="t('undelivered.retry_all')"
      icon="pi pi-replay"
      :disabled="!list.total.value"
      :loading="retryingAll"
      class="retry-all"
      @click="retryAll"
    />
  </div>
  <Message v-if="list.error.value" severity="error" :closable="false">{{ list.error.value }}</Message>
  <DataTable
    :value="list.items.value"
    lazy
    :paginator="list.total.value > list.rows.value"
    :rows="list.rows.value"
    :first="(list.page.value - 1) * list.rows.value"
    :total-records="list.total.value"
    :rows-per-page-options="[25, 50, 100]"
    :loading="list.loading.value"
    data-key="id"
    size="small"
    scrollable
    row-hover
    class="app-clickable undelivered"
    @page="list.onPage"
    @row-click="router.push({ name: 'order', params: { id: $event.data.id } })"
  >
    <template #empty>
      <div v-if="page && !list.error.value" class="empty">
        <i class="pi pi-check-circle" aria-hidden="true" />
        <span>{{ t('undelivered.empty') }}</span>
      </div>
    </template>
    <Column :header="t('undelivered.buyer')">
      <template #body="{ data: o }"><UserLink :user="o.user" :link="auth.can('users.read')" /></template>
    </Column>
    <Column :header="t('order.seller')">
      <template #body="{ data: o }"><SellerLink :seller="o.seller" /></template>
    </Column>
    <Column :header="t('common.plan')">
      <template #body="{ data: o }">
        <RouterLink :to="{ name: 'order', params: { id: o.id } }" class="plan" @click.stop><bdi>{{ planName(o.plan.name, lang) }}</bdi></RouterLink>
        <div class="app-muted small">{{ t('order.type.' + o.type) }}</div>
      </template>
    </Column>
    <Column :header="t('undelivered.paid')" body-class="app-nowrap">
      <template #body="{ data: o }">{{ o.amount.amount ? money(o.amount, lang) : t('plan.free') }}</template>
    </Column>
    <Column :header="t('undelivered.waiting')" body-class="app-nowrap">
      <template #body="{ data: o }">
        <span class="waiting" :title="dateTime(o.created_at, lang)">{{ ago(o.created_at, now, lang) }}</span>
      </template>
    </Column>
    <Column :header="t('undelivered.tries')" body-class="app-nowrap">
      <template #body="{ data: o }">
        {{ t('order.attempts', { n: num(o.attempts, lang) }, o.attempts) }}
        <div v-if="o.next_attempt_at" class="app-muted small next" :title="dateTime(o.next_attempt_at, lang)">{{ nextTry(o) }}</div>
      </template>
    </Column>
    <Column :header="t('undelivered.last_error')">
      <template #body="{ data: o }">
        <button
          v-if="o.last_error"
          type="button"
          class="last-error app-ltr"
          :class="{ open: openErrors[o.id] }"
          :title="o.last_error"
          :aria-expanded="!!openErrors[o.id]"
          @click.stop="openErrors[o.id] = !openErrors[o.id]"
        >
          {{ o.last_error }}
        </button>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
    <Column :header="t('order.server')">
      <template #body="{ data: o }">
        <bdi v-if="serverName(o.server_id, servers)" class="server">{{ serverName(o.server_id, servers) }}</bdi>
        <span v-else-if="o.server_id" class="app-ltr app-mono server">{{ shortId(o.server_id) }}</span>
        <span v-else class="app-muted">{{ t('order.server_plan') }}</span>
      </template>
    </Column>
    <Column v-if="auth.can('payments.review')" :header="t('undelivered.actions')" body-class="app-nowrap">
      <template #body="{ data: o }">
        <div class="row-actions">
          <Button
            v-tooltip.top="t('order.retry')"
            icon="pi pi-replay"
            text
            rounded
            :aria-label="t('order.retry')"
            :loading="retrying === o.id"
            class="act-retry"
            @click.stop="retry(o)"
          />
          <Button
            v-if="canMove(o)"
            v-tooltip.top="t('order.move')"
            icon="pi pi-server"
            text
            rounded
            :aria-label="t('order.move')"
            class="act-move"
            @click.stop="openMove(o)"
          />
          <Button
            v-tooltip.top="t('order.refund')"
            icon="pi pi-undo"
            text
            rounded
            severity="danger"
            :aria-label="t('order.refund')"
            class="act-refund"
            @click.stop="openRefund(o)"
          />
        </div>
      </template>
    </Column>
  </DataTable>

  <MoveDialog v-model:visible="moveOpen" :order="moving" :servers="servers" :servers-error="serversError" :servers-loading="list.loading.value" @moved="afterAction" />
  <RefundDialog v-model:visible="refundOpen" :order="refunding" @refunded="afterAction" />
</template>

<style scoped>
.intro {
  margin: 0;
  font-size: 0.9rem;
}
.small {
  font-size: 0.8rem;
}
.plan {
  color: inherit;
}
.empty {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding-block: 1rem;
}
.empty .pi {
  color: var(--p-green-500);
}
.last-error {
  display: block;
  max-inline-size: 18rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  padding: 0;
  border: 0;
  background: none;
  font: inherit;
  font-size: 0.85rem;
  color: var(--p-red-500);
  text-align: start;
  cursor: pointer;
}
.last-error.open {
  white-space: normal;
  overflow-wrap: anywhere;
}
.last-error:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 2px;
}
.row-actions {
  display: flex;
  gap: 0.1rem;
}
</style>
