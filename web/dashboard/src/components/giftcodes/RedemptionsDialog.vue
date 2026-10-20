<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Dialog from 'primevue/dialog'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Message from 'primevue/message'
import UserLink from '../UserLink.vue'
import { api, query } from '../../api'
import { errorText } from '../../errors'
import { dateTime, money, num, type Lang } from '../../format'
import { csvCalendar } from '../../reports'
import type { GiftCodeItem, GiftRedemptionItem, Page } from '../../types'

// Who redeemed one code, and when, newest first, with the file of them.
const props = defineProps<{ code: GiftCodeItem | null }>()
const visible = defineModel<boolean>('visible', { required: true })
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const items = ref<GiftRedemptionItem[]>([])
const total = ref(0)
const page = ref(1)
const rows = ref(25)
const loading = ref(false)
const error = ref('')
let seq = 0

async function load() {
  const c = props.code
  if (!c) return
  const mine = ++seq
  loading.value = true
  error.value = ''
  try {
    const res = await api<Page<GiftRedemptionItem>>('GET', `/gift-codes/${c.id}/redemptions` + query({ page: page.value, size: rows.value }))
    if (mine !== seq) return
    items.value = res.items
    total.value = res.total
  } catch (e) {
    if (mine === seq) error.value = errorText(e, t).text
  } finally {
    if (mine === seq) loading.value = false
  }
}

watch(
  () => [visible.value, props.code?.id] as const,
  ([open]) => {
    if (!open) return
    items.value = []
    total.value = 0
    page.value = 1
    void load()
  },
)

function onPage(e: { page: number; rows: number }) {
  page.value = e.page + 1
  rows.value = e.rows
  void load()
}

const csvHref = computed(() => (props.code ? `/api/v1/gift-codes/${props.code.id}/redemptions.csv` + query({ calendar: csvCalendar(lang.value) }) : '#'))
</script>

<template>
  <Dialog v-model:visible="visible" modal :draggable="false" :style="{ inlineSize: 'min(48rem, 96vw)' }" class="redemptions-dialog">
    <template #header>
      <span class="p-dialog-title">
        {{ t('giftcodes.redemptions_of') }}
        <span class="app-ltr app-mono tail">…{{ code?.tail }}</span>
      </span>
    </template>
    <p v-if="code" class="app-muted terms">
      {{ money(code.amount, lang) }} ·
      {{ t('giftcodes.used_of', { used: num(code.redeemed, lang), max: num(code.max_redemptions, lang) }) }}
      <template v-if="code.note"> · {{ code.note }}</template>
    </p>
    <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
    <DataTable
      :value="items"
      lazy
      :paginator="total > rows"
      :rows="rows"
      :first="(page - 1) * rows"
      :total-records="total"
      :rows-per-page-options="[25, 50, 100]"
      :loading="loading"
      data-key="id"
      size="small"
      scrollable
      class="redemptions"
      @page="onPage"
    >
      <template #empty>{{ t('giftcodes.redemptions_none') }}</template>
      <Column :header="t('common.date')" body-class="app-nowrap">
        <template #body="{ data: r }">{{ dateTime(r.created_at, lang) }}</template>
      </Column>
      <Column :header="t('common.customer')">
        <template #body="{ data: r }"><UserLink :user="r.user" /></template>
      </Column>
      <Column :header="t('giftcodes.credited')" body-class="app-nowrap">
        <template #body="{ data: r }">{{ money(r.amount, lang) }}</template>
      </Column>
      <Column :header="t('ledger.balance_after')" body-class="app-nowrap">
        <template #body="{ data: r }">{{ money(r.balance_after, lang) }}</template>
      </Column>
      <Column :header="t('giftcodes.number')" body-class="app-nowrap">
        <template #body="{ data: r }">{{ num(r.seq, lang) }}</template>
      </Column>
    </DataTable>
    <template #footer>
      <a :href="csvHref" download class="p-button p-component p-button-outlined export">
        <i class="pi pi-download" aria-hidden="true" />
        <span>{{ t('giftcodes.export_redemptions') }}</span>
      </a>
    </template>
  </Dialog>
</template>

<style scoped>
.tail {
  font-weight: 700;
}
.terms {
  margin-block: 0 0.75rem;
}
.export {
  text-decoration: none;
  gap: 0.5rem;
}
</style>
