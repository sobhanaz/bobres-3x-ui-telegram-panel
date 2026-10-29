<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Select from 'primevue/select'
import Message from 'primevue/message'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import BroadcastStatus from '../components/broadcasts/BroadcastStatus.vue'
import BroadcastProgress from '../components/broadcasts/BroadcastProgress.vue'
import { api, query } from '../api'
import { useList } from '../composables/useList'
import { dateTime, type Lang } from '../format'
import { STATUSES, audienceKeys, changingSoon, poller, previewOf, timing } from '../broadcasts'
import type { BroadcastItem, Page } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()

// The status filter is in the address, so a link (or reload) keeps it.
const initial = String(route.query.status ?? '')
const list = useList<BroadcastItem, { status: string }>('/broadcasts', { status: (STATUSES as readonly string[]).includes(initial) ? initial : '' })
watch(
  () => list.filters.status,
  (s) => void router.replace({ query: { ...route.query, status: s || undefined } }),
)
onMounted(list.load)

// While a broadcast on this page is sending (or due within minutes), the
// list refreshes quietly: no loading veil, the same page and filter.
async function refresh() {
  if (list.loading.value) return
  const status = list.filters.status
  const page = list.page.value
  const rows = list.rows.value
  const res = await api<Page<BroadcastItem>>('GET', '/broadcasts' + query({ status, page, size: rows }))
  if (list.loading.value || status !== list.filters.status || page !== list.page.value || rows !== list.rows.value) return
  list.items.value = res.items
  list.total.value = res.total
}
const poll = poller(refresh, () => timing.listPoll)
// The time due rows are checked against, moved on by a timer: a broadcast
// that falls due while the page is open starts the refresh too.
const now = ref(Date.now() / 1000)
const clock = setInterval(() => (now.value = Date.now() / 1000), timing.clock)
watch(
  () => list.items.value.some((b) => changingSoon(b, now.value)),
  (any) => (any ? poll.start() : poll.stop()),
)
onBeforeUnmount(() => {
  clearInterval(clock)
  poll.stop()
})

const statuses = computed(() => [{ label: t('broadcasts.list.all_statuses'), value: '' }, ...STATUSES.map((s) => ({ label: t('broadcasts.status.' + s), value: s }))])
const audience = (b: BroadcastItem) => audienceKeys(b).map((k) => t(k)).join(' · ')

function open(b: BroadcastItem) {
  if (b.status === 'draft') void router.push({ name: 'broadcast-edit', params: { id: b.id } })
  else void router.push({ name: 'broadcast', params: { id: b.id } })
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.broadcasts.title') }}</h1>
    <Button :label="t('broadcasts.new')" icon="pi pi-plus" @click="router.push({ name: 'broadcast-new' })" />
  </div>
  <div class="app-toolbar">
    <Select
      v-model="list.filters.status"
      :placeholder="t('broadcasts.list.all_statuses')"
      :options="statuses"
      option-label="label"
      option-value="value"
      :aria-label="t('common.status')"
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
    class="app-clickable broadcasts"
    @page="list.onPage"
    @row-click="open($event.data)"
  >
    <template #empty>
      <div class="empty">
        <template v-if="list.filters.status">{{ t('broadcasts.list.empty_filtered') }}</template>
        <template v-else>
          <p>{{ t('broadcasts.list.empty') }}</p>
          <Button :label="t('broadcasts.new')" icon="pi pi-plus" outlined @click="router.push({ name: 'broadcast-new' })" />
        </template>
      </div>
    </template>
    <Column :header="t('broadcasts.list.created')" body-class="app-nowrap">
      <template #body="{ data: b }">{{ dateTime(b.created_at, lang) }}</template>
    </Column>
    <Column :header="t('common.status')">
      <template #body="{ data: b }"><BroadcastStatus :status="b.status" /></template>
    </Column>
    <Column :header="t('broadcasts.list.audience')">
      <template #body="{ data: b }"><span class="audience">{{ audience(b) }}</span></template>
    </Column>
    <Column :header="t('broadcasts.list.text')">
      <template #body="{ data: b }">
        <span
          class="preview"
          :dir="previewOf(b.texts, lang).lang === 'fa' ? 'rtl' : 'ltr'"
          :lang="previewOf(b.texts, lang).lang"
        >{{ previewOf(b.texts, lang).text }}</span>
      </template>
    </Column>
    <Column :header="t('broadcasts.list.progress')">
      <template #body="{ data: b }"><BroadcastProgress :item="b" /></template>
    </Column>
    <Column :header="t('broadcasts.list.scheduled')" body-class="app-nowrap">
      <template #body="{ data: b }">
        <template v-if="b.scheduled_at">{{ dateTime(b.scheduled_at, lang) }}</template>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
  </DataTable>
</template>

<style scoped>
.preview {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
  max-inline-size: 22rem;
  min-inline-size: 10rem;
  unicode-bidi: isolate;
}
.preview[lang='fa'] {
  font-family: var(--app-font-fa);
}
.preview[lang='en'] {
  font-family: var(--app-font-en);
}
.audience {
  font-size: 0.875rem;
}
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.6rem;
  padding-block: 1.5rem;
  text-align: center;
}
.empty p {
  margin: 0;
}
</style>
