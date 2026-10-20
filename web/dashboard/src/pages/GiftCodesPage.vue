<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import ToggleSwitch from 'primevue/toggleswitch'
import Message from 'primevue/message'
import NewCodesDialog from '../components/giftcodes/NewCodesDialog.vue'
import CodesShownDialog from '../components/giftcodes/CodesShownDialog.vue'
import RedemptionsDialog from '../components/giftcodes/RedemptionsDialog.vue'
import { api, query } from '../api'
import { errorText } from '../errors'
import { useList } from '../composables/useList'
import { date, dateTime, latinDigits, money, num, type Lang } from '../format'
import { severity, statuses, type Terms } from '../giftcodes'
import { useAuth } from '../stores/auth'
import type { GiftCodeItem, Page } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const auth = useAuth()

// The list: a status and a search (a note, or a code's last four characters).
const list = useList<GiftCodeItem, { status: string; q: string }>('/gift-codes', { status: '', q: '' })
onMounted(list.load)
const search = ref('')
watch(search, (s) => (list.filters.q = latinDigits(s.trim())))
const statusOptions = computed(() => [
  { label: t('giftcodes.all_statuses'), value: '' },
  ...statuses.map((s) => ({ label: t('giftcodes.status.' + s), value: s })),
])

// Whether customers can use gift codes now (the owner's switch), and whether
// core can make them (it needs its master key): what the list answers besides the page.
type Meta = Page<GiftCodeItem> & { enabled: boolean; available: boolean }
const meta = computed(() => list.raw.value as Meta | null)
const canSwitch = computed(() => auth.can('settings.write') && auth.can('discounts.write'))
const switching = ref(false)
async function turnOn() {
  switching.value = true
  try {
    await api('PUT', '/settings', { values: { 'gift_codes.enabled': 'true' } })
    toast.add({ severity: 'success', summary: t('giftcodes.turned_on'), life: 3000 })
    await list.load()
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 5000 })
  } finally {
    switching.value = false
  }
}

// Switching one code off or on again (what it paid stays paid).
async function toggle(g: GiftCodeItem, enabled: boolean) {
  try {
    const res = await api<{ item: GiftCodeItem }>('PUT', `/gift-codes/${g.id}/enabled`, { enabled })
    const i = list.items.value.findIndex((x) => x.id === g.id)
    if (i >= 0) list.items.value[i] = res.item
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 5000 })
  }
}

// Making codes: the form, then the codes shown once.
const making = ref(false)
const shown = ref(false)
const shownCodes = ref<string[]>([])
const shownTerms = ref<Terms | null>(null)
function created(res: { codes: string[]; terms: Terms }) {
  shownCodes.value = res.codes
  shownTerms.value = res.terms
  shown.value = true
  void list.load()
}

const opened = ref<GiftCodeItem | null>(null)
const redemptions = ref(false)
function openRedemptions(g: GiftCodeItem) {
  opened.value = g
  redemptions.value = true
}

const csvHref = computed(() => '/api/v1/gift-codes.csv' + query({ status: list.filters.status, q: list.filters.q }))
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.giftcodes.title') }}</h1>
    <div class="actions">
      <a :href="csvHref" download class="p-button p-component p-button-outlined export">
        <i class="pi pi-download" aria-hidden="true" />
        <span>{{ t('giftcodes.export') }}</span>
      </a>
      <Button class="new" icon="pi pi-plus" :label="t('giftcodes.new')" :disabled="meta !== null && !meta.available" @click="making = true" />
    </div>
  </div>

  <Message v-if="meta && !meta.available" severity="warn" :closable="false" class="no-key">{{ t('giftcodes.no_key') }}</Message>
  <Message v-else-if="meta && !meta.enabled" severity="info" :closable="false" class="is-off">
    <span>{{ t('giftcodes.off') }}</span>
    <Button v-if="canSwitch" class="turn-on" link :label="t('giftcodes.turn_on')" :loading="switching" @click="turnOn" />
  </Message>
  <p class="app-muted intro">{{ t('giftcodes.intro') }}</p>

  <div class="app-toolbar">
    <Select v-model="list.filters.status" class="status-filter" :options="statusOptions" option-label="label" option-value="value" :aria-label="t('giftcodes.status_col')" />
    <IconField class="grow">
      <InputIcon class="pi pi-search" />
      <InputText v-model="search" type="search" class="search" :placeholder="t('giftcodes.search')" :aria-label="t('giftcodes.search')" />
    </IconField>
    <Button v-if="list.loading.value" text loading :aria-label="t('app.loading')" />
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
    class="codes"
    @page="list.onPage"
  >
    <template #empty>{{ t('giftcodes.none') }}</template>
    <Column :header="t('giftcodes.code')">
      <template #body="{ data: g }">
        <span class="app-ltr app-mono tail">…{{ g.tail }}</span>
        <div class="app-muted small">{{ g.generated ? t('giftcodes.kind_generated') : t('giftcodes.kind_own') }}</div>
      </template>
    </Column>
    <Column :header="t('giftcodes.value')" body-class="app-nowrap">
      <template #body="{ data: g }">{{ money(g.amount, lang) }}</template>
    </Column>
    <Column :header="t('giftcodes.used')" body-class="app-nowrap">
      <template #body="{ data: g }">
        {{ t('giftcodes.used_of', { used: num(g.redeemed, lang), max: num(g.max_redemptions, lang) }) }}
        <div v-if="g.per_customer_limit > 1" class="app-muted small">{{ t('giftcodes.per_customer_short', { n: num(g.per_customer_limit, lang) }) }}</div>
      </template>
    </Column>
    <Column :header="t('giftcodes.status_col')">
      <template #body="{ data: g }">
        <Tag :severity="severity(g.status)" :value="t('giftcodes.status.' + g.status)" />
      </template>
    </Column>
    <Column :header="t('giftcodes.ends')" body-class="app-nowrap">
      <template #body="{ data: g }">
        <span v-if="g.expires_at">{{ date(g.expires_at, lang) }}</span>
        <span v-else class="app-muted">{{ t('giftcodes.never') }}</span>
      </template>
    </Column>
    <Column :header="t('giftcodes.last_used')" body-class="app-nowrap">
      <template #body="{ data: g }">
        <span v-if="g.last_redeemed_at">{{ dateTime(g.last_redeemed_at, lang) }}</span>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
    <Column :header="t('giftcodes.note')">
      <template #body="{ data: g }">
        <bdi class="note-text">{{ g.note }}</bdi>
        <div class="app-muted small">{{ t('giftcodes.made', { date: date(g.created_at, lang) }) }}<template v-if="g.created_by?.username"> · <bdi>@{{ g.created_by.username }}</bdi></template></div>
      </template>
    </Column>
    <Column :header="t('giftcodes.active')">
      <template #body="{ data: g }">
        <div class="row-actions">
          <ToggleSwitch :model-value="g.enabled" class="toggle" :aria-label="t('giftcodes.active')" @update:model-value="(v: boolean) => toggle(g, v)" />
          <Button class="who" icon="pi pi-users" text rounded :aria-label="t('giftcodes.redemptions')" @click="openRedemptions(g)" />
        </div>
      </template>
    </Column>
  </DataTable>

  <NewCodesDialog v-model:visible="making" @created="created" />
  <CodesShownDialog v-model:visible="shown" :codes="shownCodes" :terms="shownTerms" />
  <RedemptionsDialog v-model:visible="redemptions" :code="opened" />
</template>

<style scoped>
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.export {
  text-decoration: none;
  gap: 0.5rem;
}
.intro {
  margin-block: 0 0.75rem;
  font-size: 0.9rem;
}
.grow {
  flex: 1 1 14rem;
  max-inline-size: 22rem;
}
.search {
  inline-size: 100%;
}
.tail {
  font-weight: 700;
}
.small {
  font-size: 0.8rem;
}
.note-text {
  overflow-wrap: anywhere;
}
.row-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.is-off :deep(.p-message-text) {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
</style>
