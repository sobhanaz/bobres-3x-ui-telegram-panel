<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import InputText from 'primevue/inputtext'
import UserLink from '../components/UserLink.vue'
import StatusTag from '../components/StatusTag.vue'
import TrafficBar from '../components/TrafficBar.vue'
import ReasonDialog from '../components/ReasonDialog.vue'
import OrdersTable from '../components/OrdersTable.vue'
import { api, newKey } from '../api'
import { errorText } from '../errors'
import { useAuth } from '../stores/auth'
import { asciiNumber, date, dateTime, daysLeft, num, planName, shortId, type Lang } from '../format'
import type { OrderItem, ServiceItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()
const toast = useToast()
const auth = useAuth()
const id = computed(() => route.params.id as string)

const service = ref<ServiceItem | null>(null)
const orders = ref<OrderItem[]>([])
const loadError = ref('')
async function load() {
  loadError.value = ''
  try {
    const res = await api<{ service: ServiceItem; orders: OrderItem[] }>('GET', `/services/${id.value}`)
    service.value = res.service
    orders.value = res.orders
  } catch (e) {
    loadError.value = errorText(e, t).text
  }
}
onMounted(load)
watch(id, load)

const s = computed(() => service.value)
const live = computed(() => !!s.value && s.value.status !== 'deleted' && s.value.status !== 'pending')
const left = computed(() => (s.value?.expires_at ? daysLeft(s.value.expires_at) : null))

// One dialog at a time; each action knows its request.
type Action = 'extend' | 'reset' | 'disable' | 'enable' | 'delete'
const action = ref<Action | null>(null)
const open = computed({ get: () => action.value !== null, set: (v) => !v && (action.value = null) })
const busy = ref(false)
const actionError = ref('')
const actionDetail = ref('')
const days = ref('')
const gb = ref('')
const extendKey = ref('')
function start(a: Action) {
  action.value = a
  actionError.value = ''
  actionDetail.value = ''
  days.value = ''
  gb.value = ''
  extendKey.value = newKey()
}
const extendDays = computed(() => Number(asciiNumber(days.value)) || 0)
const extendGB = computed(() => asciiNumber(gb.value))
const extendOK = computed(
  () =>
    (/^\d{0,4}$/.test(asciiNumber(days.value)) && /^(\d{1,6}(\.\d{1,3})?)?$/.test(extendGB.value)) &&
    (extendDays.value > 0 || Number(extendGB.value) > 0),
)
const newExpiry = computed(() => {
  if (!s.value?.expires_at || extendDays.value <= 0) return null
  const base = Math.max(s.value.expires_at, Date.now() / 1000)
  return base + extendDays.value * 86400
})
const dialog = computed(() => {
  switch (action.value) {
    case 'extend':
      return { title: t('service.extend'), message: t('service.extend_intro'), confirm: t('service.extend'), danger: false }
    case 'reset':
      return { title: t('service.reset'), message: t('service.reset_intro'), confirm: t('service.reset'), danger: false }
    case 'disable':
      return { title: t('service.disable'), message: t('service.disable_intro'), confirm: t('service.disable'), danger: true }
    case 'enable':
      return { title: t('service.enable'), message: t('service.enable_intro'), confirm: t('service.enable'), danger: false }
    case 'delete':
      return { title: t('service.delete'), message: t('service.delete_intro'), confirm: t('service.delete'), danger: true }
  }
  return { title: '', message: '', confirm: '', danger: false }
})

async function confirm(reason: string) {
  busy.value = true
  actionError.value = ''
  try {
    let res: { service: ServiceItem; warning?: string }
    switch (action.value) {
      case 'extend':
        res = await api('POST', `/services/${id.value}/extend`, { days: extendDays.value, traffic_gb: extendGB.value, reason, key: extendKey.value })
        break
      case 'reset':
        res = await api('POST', `/services/${id.value}/reset-traffic`, { reason })
        break
      case 'disable':
      case 'enable':
        res = await api('POST', `/services/${id.value}/enabled`, { enabled: action.value === 'enable', reason })
        break
      case 'delete':
        res = await api('POST', `/services/${id.value}/delete`, { reason })
        break
      default:
        return
    }
    const done = action.value
    action.value = null
    service.value = res.service
    toast.add({ severity: res.warning ? 'warn' : 'success', summary: t('service.done_' + done), detail: res.warning, life: 6000 })
    await load()
  } catch (e) {
    const err = errorText(e, t)
    actionError.value = err.text
    actionDetail.value = err.detail
  } finally {
    busy.value = false
  }
}

const syncing = ref(false)
async function sync() {
  syncing.value = true
  try {
    const res = await api<{ service: ServiceItem }>('POST', `/services/${id.value}/sync`)
    service.value = res.service
    toast.add({ severity: 'success', summary: t('service.synced'), life: 3000 })
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 6000 })
  } finally {
    syncing.value = false
  }
}

async function copyLink() {
  if (!s.value?.sub_link) return
  try {
    await navigator.clipboard.writeText(s.value.sub_link)
    toast.add({ severity: 'info', summary: t('common.copied'), life: 2000 })
  } catch {
    /* clipboard blocked: the link is selectable */
  }
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">
      <Button icon="pi pi-arrow-left" class="app-flip" text rounded :aria-label="t('common.back')" @click="router.push({ name: 'services' })" />
      {{ t('service.title') }}
      <span v-if="s" class="app-ltr app-mono app-muted">{{ shortId(s.id) }}</span>
    </h1>
    <div v-if="s" class="app-actions">
      <RouterLink v-if="auth.can('audit.read')" :to="{ name: 'audit', query: { entity_id: s.id } }" class="p-button p-component p-button-text history">
        <i class="pi pi-history" aria-hidden="true" />
        <span>{{ t('audit.history') }}</span>
      </RouterLink>
      <template v-if="live">
        <Button v-if="auth.can('services.extend')" :label="t('service.extend')" icon="pi pi-calendar-plus" @click="start('extend')" />
        <Button v-if="auth.can('services.write')" :label="t('service.reset')" icon="pi pi-refresh" outlined @click="start('reset')" />
        <Button
          v-if="auth.can('services.write')"
          :label="s.status === 'disabled' ? t('service.enable') : t('service.disable')"
          :icon="s.status === 'disabled' ? 'pi pi-play' : 'pi pi-pause'"
          :severity="s.status === 'disabled' ? 'success' : 'warn'"
          outlined
          @click="start(s.status === 'disabled' ? 'enable' : 'disable')"
        />
        <Button v-if="auth.can('services.write')" :label="t('service.delete')" icon="pi pi-trash" severity="danger" text @click="start('delete')" />
      </template>
    </div>
  </div>
  <Message v-if="loadError" severity="error" :closable="false">{{ loadError }}</Message>

  <Card>
    <template #content>
      <dl v-if="s" class="app-facts">
        <dt>{{ t('common.customer') }}</dt>
        <dd><UserLink :user="s.user" /></dd>
        <dt>{{ t('common.plan') }}</dt>
        <dd><bdi>{{ planName(s.plan.name, lang) }}</bdi></dd>
        <dt>{{ t('common.status') }}</dt>
        <dd><StatusTag kind="service" :status="s.status" /></dd>
        <dt>{{ t('service.expires') }}</dt>
        <dd>
          <template v-if="s.expires_at">
            {{ dateTime(s.expires_at, lang) }}
            <span class="app-muted">({{ left! >= 0 ? t('service.days_left', { n: num(left!, lang) }) : t('service.days_ago', { n: num(-left!, lang) }) }})</span>
          </template>
          <span v-else class="app-muted">{{ t('service.never') }}</span>
        </dd>
        <dt>{{ t('service.traffic') }}</dt>
        <dd><TrafficBar :used="s.traffic_used" :total="s.traffic_total" /></dd>
        <dt>{{ t('service.link') }}</dt>
        <dd>
          <template v-if="s.sub_link">
            <span class="app-ltr app-mono link">{{ s.sub_link }}</span>
            <Button icon="pi pi-copy" text rounded size="small" :aria-label="t('common.copy')" @click="copyLink" />
          </template>
          <span v-else class="app-muted">—</span>
        </dd>
        <dt>{{ t('service.panel_email') }}</dt>
        <dd><span class="app-ltr app-mono">{{ s.client_email }}</span></dd>
        <dt>{{ t('service.synced_at') }}</dt>
        <dd>
          {{ s.last_synced_at ? dateTime(s.last_synced_at, lang) : t('service.never_synced') }}
          <Button
            v-if="live && auth.can('services.extend')"
            :label="t('service.sync')"
            icon="pi pi-sync"
            text
            size="small"
            :loading="syncing"
            @click="sync"
          />
        </dd>
        <dt>{{ t('service.created') }}</dt>
        <dd>{{ date(s.created_at, lang) }}</dd>
      </dl>
      <Skeleton v-else height="14rem" />
    </template>
  </Card>

  <Card class="orders">
    <template #title>{{ t('service.orders') }}</template>
    <template #content>
      <OrdersTable :items="orders" :lazy="false" hide-user @open="(o) => router.push({ name: 'order', params: { id: o.id } })" />
    </template>
  </Card>

  <ReasonDialog
    v-model:visible="open"
    :title="dialog.title"
    :message="dialog.message"
    :confirm-label="dialog.confirm"
    :danger="dialog.danger"
    :busy="busy"
    :error="actionError"
    :detail="actionDetail"
    :can-confirm="action !== 'extend' || extendOK"
    @confirm="confirm"
  >
    <div v-if="action === 'extend'" class="app-form">
      <div class="app-row">
        <label>
          <span>{{ t('service.add_days') }}</span>
          <InputText v-model="days" inputmode="numeric" dir="ltr" autocomplete="off" :disabled="!s?.expires_at" />
        </label>
        <label>
          <span>{{ t('service.add_gb') }}</span>
          <InputText v-model="gb" inputmode="decimal" dir="ltr" autocomplete="off" :disabled="!s?.traffic_total" />
        </label>
      </div>
      <small v-if="newExpiry" class="app-muted">{{ t('service.new_expiry', { date: date(newExpiry, lang) }) }}</small>
    </div>
  </ReasonDialog>
</template>

<style scoped>
.history {
  text-decoration: none;
  gap: 0.5rem;
}
.orders {
  margin-block-start: var(--app-gap);
}
.link {
  word-break: break-all;
}
.app-page-title {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}
</style>
