<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import Select from 'primevue/select'
import ToggleSwitch from 'primevue/toggleswitch'
import Tabs from 'primevue/tabs'
import TabList from 'primevue/tablist'
import Tab from 'primevue/tab'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import UserLink from '../components/UserLink.vue'
import BalanceText from '../components/resellers/BalanceText.vue'
import MakeResellerDialog from '../components/resellers/MakeResellerDialog.vue'
import TiersTab from '../components/resellers/TiersTab.vue'
import MessagesTab from '../components/resellers/MessagesTab.vue'
import { api } from '../api'
import { useAuth } from '../stores/auth'
import { errorText } from '../errors'
import { useList } from '../composables/useList'
import { latinDigits, money, num, type Lang } from '../format'
import { percent, resellerOf } from '../resellers'
import type { ResellerItem, TierItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const auth = useAuth()
const route = useRoute()
const router = useRouter()
const toast = useToast()

// Three tabs, the others in the address (?tab=tiers, ?tab=messages). Messages to resellers are broadcasts: that tab needs broadcasts.write.
type View = 'resellers' | 'tiers' | 'messages'
const canMessage = computed(() => auth.can('broadcasts.write'))
const pick = (v: unknown): View => (v === 'tiers' ? 'tiers' : v === 'messages' && canMessage.value ? 'messages' : 'resellers')
const viewOf = (): View => pick(route.query.tab)
const view = ref<View>(viewOf())
function show(v: string | number) {
  view.value = pick(String(v))
  const q = { ...route.query }
  delete q.tab
  if (view.value !== 'resellers') q.tab = view.value
  void router.replace({ query: q })
}
watch(
  () => [route.query.tab, canMessage.value],
  () => {
    if (route.name === 'resellers') view.value = viewOf()
  },
)

// The list: search, a tier, and "only those who owe" (?in_debt=1 from the overview).
const list = useList<ResellerItem, { q: string; tier: string; in_debt: string }>('/resellers', {
  q: '',
  tier: '',
  in_debt: route.query.in_debt === '1' ? '1' : '',
})
const rows = computed(() => list.items.value.map(resellerOf))
const search = ref('')
watch(search, (s) => (list.filters.q = latinDigits(s.trim())))
const inDebt = computed({
  get: () => list.filters.in_debt === '1',
  set: (v: boolean) => (list.filters.in_debt = v ? '1' : ''),
})

// Tiers: the filter and the Tiers tab share one list.
const tiers = ref<TierItem[]>([])
const tiersLoading = ref(true)
const tiersError = ref('')
async function loadTiers() {
  tiersLoading.value = true
  tiersError.value = ''
  try {
    tiers.value = (await api<{ items: TierItem[] }>('GET', '/reseller-tiers')).items
  } catch (e) {
    tiersError.value = errorText(e, t).text
  } finally {
    tiersLoading.value = false
  }
}
const tierOptions = computed(() => [{ label: t('reseller.all_tiers'), value: '' }, ...tiers.value.map((tr) => ({ label: tr.name, value: tr.id }))])
function tiersChanged() {
  void loadTiers()
  void list.load() // tier names show in the list
}

onMounted(() => {
  void list.load()
  void loadTiers()
})

const making = ref(false)
function made(id: string) {
  toast.add({ severity: 'success', summary: t('reseller.made'), life: 4000 })
  void router.push({ name: 'reseller', params: { id } })
}
function open(id: string) {
  void router.push({ name: 'reseller', params: { id } })
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.resellers.title') }}</h1>
    <div v-if="view === 'resellers'" class="app-actions">
      <Button v-if="canMessage" :label="t('reseller.messages.open')" icon="pi pi-send" outlined class="message" @click="show('messages')" />
      <Button :label="t('reseller.add')" icon="pi pi-plus" class="add" @click="making = true" />
    </div>
  </div>

  <Tabs :value="view" scrollable @update:value="show">
    <TabList>
      <Tab value="resellers">{{ t('reseller.tab_resellers') }}</Tab>
      <Tab value="tiers">{{ t('reseller.tab_tiers') }}</Tab>
      <Tab v-if="canMessage" value="messages">{{ t('reseller.tab_messages') }}</Tab>
    </TabList>
  </Tabs>

  <div class="panel">
    <TiersTab v-if="view === 'tiers'" :tiers="tiers" :loading="tiersLoading" :error="tiersError" @changed="tiersChanged" />
    <MessagesTab v-else-if="view === 'messages'" :tiers="tiers" />
    <template v-else>
      <div class="app-toolbar">
        <IconField class="app-grow">
          <InputIcon class="pi pi-search" />
          <InputText v-model="search" :placeholder="t('reseller.search')" :aria-label="t('reseller.search')" fluid type="search" autocomplete="off" />
        </IconField>
        <Select
          v-model="list.filters.tier"
          :options="tierOptions"
          option-label="label"
          option-value="value"
          :placeholder="t('reseller.all_tiers')"
          :aria-label="t('reseller.tier')"
          class="tier-filter"
        />
        <label class="debt-filter">
          <ToggleSwitch v-model="inDebt" input-id="in-debt" />
          <span>{{ t('reseller.only_in_debt') }}</span>
        </label>
      </div>
      <Message v-if="list.error.value" severity="error" :closable="false">{{ list.error.value }}</Message>
      <DataTable
        :value="rows"
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
        class="app-clickable resellers"
        @page="list.onPage"
        @row-click="open($event.data.id)"
      >
        <template #empty>
          <span v-if="list.filters.q || list.filters.tier || list.filters.in_debt">{{ t('reseller.none_match') }}</span>
          <span v-else>{{ t('reseller.none') }}</span>
        </template>
        <Column :header="t('reseller.col_reseller')">
          <template #body="{ data: r }">
            <UserLink :user="r.user" :link="false" />
            <Tag v-if="r.user.status === 'banned'" :value="t('user.status.banned')" severity="danger" class="tag" />
          </template>
        </Column>
        <Column :header="t('reseller.tier')">
          <template #body="{ data: r }">
            <bdi>{{ r.tier.name }}</bdi>
            <div class="app-muted small">
              {{ r.discount === null ? '' : t('reseller.discount_short', { n: percent(r.discount, lang) }) + ' · ' }}{{ t('reseller.commission_short', { n: percent(r.commission, lang) }) }}
            </div>
          </template>
        </Column>
        <Column :header="t('reseller.balance')" body-class="app-nowrap">
          <template #body="{ data: r }">
            <div v-for="w in r.wallets" :key="w.currency"><BalanceText :value="{ amount: w.balance, currency: w.currency }" /></div>
          </template>
        </Column>
        <Column :header="t('reseller.credit_limit')" body-class="app-nowrap">
          <template #body="{ data: r }">
            <div v-for="w in r.wallets" :key="w.currency">{{ money({ amount: w.limit, currency: w.currency }, lang) }}</div>
          </template>
        </Column>
        <Column :header="t('reseller.customers')">
          <template #body="{ data: r }">{{ num(r.customers, lang) }}</template>
        </Column>
        <Column :header="t('reseller.commission_earned')" body-class="app-nowrap">
          <template #body="{ data: r }">
            <div v-for="m in r.earned" :key="m.currency">{{ money(m, lang) }}</div>
            <span v-if="!r.earned.length" class="app-muted">—</span>
          </template>
        </Column>
      </DataTable>
    </template>
  </div>

  <MakeResellerDialog v-model:visible="making" @made="made" />
</template>

<style scoped>
.panel {
  margin-block-start: var(--app-gap);
}
.debt-filter {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  cursor: pointer;
}
.tag {
  margin-inline-start: 0.5rem;
}
.small {
  font-size: 0.8rem;
}
</style>
