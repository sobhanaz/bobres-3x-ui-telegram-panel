<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import UserLink from './UserLink.vue'
import { dateTime, money, shortId, type Lang } from '../format'
import type { LedgerItem } from '../types'

const props = withDefaults(
  defineProps<{ items: LedgerItem[]; total?: number; rows?: number; first?: number; loading?: boolean; hideUser?: boolean }>(),
  { total: 0, rows: 25, first: 0, loading: false, hideUser: false },
)
const emit = defineEmits<{ page: [e: { page: number; rows: number }] }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const kind = (k: string) => (te('ledger.kind.' + k) ? t('ledger.kind.' + k) : k)
</script>

<template>
  <DataTable
    :value="props.items"
    lazy
    :paginator="props.total > props.rows"
    :rows="props.rows"
    :first="props.first"
    :total-records="props.total"
    :rows-per-page-options="[25, 50, 100]"
    :loading="props.loading"
    data-key="id"
    size="small"
    scrollable
    @page="emit('page', $event)"
  >
    <template #empty>{{ t('ledger.none') }}</template>
    <Column :header="t('common.date')" body-class="app-nowrap">
      <template #body="{ data: e }">{{ dateTime(e.created_at, lang) }}</template>
    </Column>
    <Column v-if="!props.hideUser" :header="t('common.customer')">
      <template #body="{ data: e }"><UserLink :user="e.user" /></template>
    </Column>
    <Column :header="t('ledger.kind_col')">
      <template #body="{ data: e }">{{ kind(e.kind) }}</template>
    </Column>
    <Column :header="t('common.amount')" body-class="app-nowrap">
      <template #body="{ data: e }">
        <span :class="e.amount.amount < 0 ? 'out' : e.amount.amount > 0 ? 'in' : ''">{{ money(e.amount, lang, true) }}</span>
      </template>
    </Column>
    <Column :header="t('ledger.balance_after')" body-class="app-nowrap">
      <template #body="{ data: e }">{{ money(e.balance_after, lang) }}</template>
    </Column>
    <Column :header="t('ledger.reference')">
      <template #body="{ data: e }">
        <RouterLink v-if="e.ref_type === 'order' && e.ref_id" :to="{ name: 'order', params: { id: e.ref_id } }">
          {{ t('ledger.ref.order') }} <span class="app-ltr app-mono">{{ shortId(e.ref_id) }}</span>
        </RouterLink>
        <span v-else-if="e.ref_type" class="app-muted">{{ te('ledger.ref.' + e.ref_type) ? t('ledger.ref.' + e.ref_type) : e.ref_type }}</span>
      </template>
    </Column>
  </DataTable>
</template>

<style scoped>
.in {
  color: var(--p-green-600);
}
.out {
  color: var(--p-red-500);
}
</style>
