<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import Tabs from 'primevue/tabs'
import TabList from 'primevue/tablist'
import Tab from 'primevue/tab'
import InputText from 'primevue/inputtext'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import Select from 'primevue/select'
import TicketsTable from '../components/support/TicketsTable.vue'
import CannedReplies from '../components/support/CannedReplies.vue'
import { useAuth } from '../stores/auth'
import { useList } from '../composables/useList'
import { num, type Lang } from '../format'
import { ASSIGNED, CATEGORIES, INBOX_TABS, inboxTab, loadCounts, statusParam, ticketCounts, ticketSearch, type InboxTab } from '../support'
import type { TicketItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()
const auth = useAuth()
const canManage = computed(() => auth.can('tickets.manage'))

// One row of tabs: the inbox by status (?status=), then saved replies
// (?tab=canned, for those who manage them).
type View = InboxTab | 'canned'
const viewOf = (): View => (route.query.tab === 'canned' && canManage.value ? 'canned' : inboxTab(route.query.status))
const view = ref<View>(viewOf())
function show(v: string | number) {
  const next = String(v) as View
  view.value = next
  const q = { ...route.query }
  delete q.tab
  delete q.status
  if (next === 'canned') q.tab = 'canned'
  else q.status = next
  void router.replace({ query: q })
}
// The menu and other pages may change the address while this page is open
// (not when leaving it: the next page's address is not for this one).
watch(
  () => [route.query.tab, route.query.status],
  () => {
    if (route.name !== 'support') return
    const v = viewOf()
    if (v !== view.value) view.value = v
  },
)

const list = useList<TicketItem, { status: string; assigned: string; category: string; q: string }>('/tickets', {
  status: view.value === 'canned' ? 'open' : statusParam(view.value),
  assigned: '',
  category: '',
  q: '',
})
// The list loads when the inbox is first shown; a status change reloads it.
let listLoaded = false
function showList() {
  if (view.value === 'canned') return
  const status = statusParam(view.value)
  if (list.filters.status !== status) list.filters.status = status
  else if (!listLoaded) void list.load()
  listLoaded = true
}
watch(view, showList)
const search = ref('')
watch(search, (s) => (list.filters.q = ticketSearch(s)))
const now = ref(Math.floor(Date.now() / 1000))
watch(list.items, () => (now.value = Math.floor(Date.now() / 1000)))

onMounted(() => {
  showList()
  void loadCounts()
})
function refresh() {
  void list.load()
  void loadCounts()
}

const tabs = computed(() =>
  INBOX_TABS.map((s) => ({
    value: s,
    label: t('support.tab.' + s),
    count: s === 'open' ? ticketCounts.value?.waiting : s === 'answered' ? ticketCounts.value?.answered : undefined,
  })),
)
const assignedOptions = computed(() => [
  { label: t('support.assigned_any'), value: '' },
  ...ASSIGNED.map((a) => ({ label: t('support.assigned_' + a), value: a })),
])
const categoryOptions = computed(() => [
  { label: t('support.category_all'), value: '' },
  ...CATEGORIES.map((c) => ({ label: t('support.category.' + c), value: c })),
])
const empty = computed(() => {
  if (list.filters.q || list.filters.assigned || list.filters.category) return t('support.none')
  return view.value === 'open' ? t('support.none_waiting') : t('support.none')
})
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.support.title') }}</h1>
    <Button
      v-if="view !== 'canned'"
      icon="pi pi-refresh"
      text
      rounded
      :loading="list.loading.value"
      :aria-label="t('support.refresh')"
      v-tooltip.bottom="t('support.refresh')"
      @click="refresh"
    />
  </div>

  <Tabs :value="view" scrollable @update:value="show">
    <TabList>
      <Tab v-for="tb in tabs" :key="tb.value" :value="tb.value">
        {{ tb.label }}
        <Tag v-if="tb.count" :value="num(tb.count, lang)" :severity="tb.value === 'open' ? 'warn' : 'secondary'" class="count" />
      </Tab>
      <Tab v-if="canManage" value="canned"><i class="pi pi-bookmark" aria-hidden="true" /> {{ t('support.tab.canned') }}</Tab>
    </TabList>
  </Tabs>

  <div class="panel">
    <CannedReplies v-if="view === 'canned'" />
    <template v-else>
      <div class="app-toolbar">
        <IconField class="app-grow">
          <InputIcon class="pi pi-search" />
          <InputText v-model="search" :placeholder="t('support.search')" :aria-label="t('support.search')" fluid type="search" autocomplete="off" />
        </IconField>
        <Select
          v-model="list.filters.assigned"
          :options="assignedOptions"
          option-label="label"
          option-value="value"
          :placeholder="t('support.assigned_any')"
          :aria-label="t('support.assignee')"
        />
        <Select
          v-model="list.filters.category"
          :options="categoryOptions"
          option-label="label"
          option-value="value"
          :placeholder="t('support.category_all')"
          :aria-label="t('support.category_label')"
        />
      </div>
      <p class="app-muted hint">{{ t('support.search_hint') }}</p>
      <Message v-if="list.error.value" severity="error" :closable="false">{{ list.error.value }}</Message>
      <TicketsTable
        :items="list.items.value"
        :total="list.total.value"
        :rows="list.rows.value"
        :first="(list.page.value - 1) * list.rows.value"
        :loading="list.loading.value"
        :now="now"
        :empty="empty"
        @page="list.onPage"
        @open="(tk) => router.push({ name: 'ticket', params: { id: tk.id } })"
      />
    </template>
  </div>
</template>

<style scoped>
.panel {
  margin-block-start: var(--app-gap);
}
.count {
  margin-inline-start: 0.4rem;
  font-size: 0.75rem;
  padding-block: 0.05rem;
}
.hint {
  margin-block: -0.5rem 0.75rem;
  font-size: 0.85rem;
}
</style>
