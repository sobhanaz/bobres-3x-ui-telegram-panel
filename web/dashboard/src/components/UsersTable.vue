<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Tag from 'primevue/tag'
import UserLink from './UserLink.vue'
import StatusTag from './StatusTag.vue'
import BalanceText from './resellers/BalanceText.vue'
import { date, num, type Lang } from '../format'
import type { UserItem } from '../types'

// A page of customers (the Users page, a reseller's customers); a row opens
// the customer.
const props = withDefaults(
  defineProps<{ items: UserItem[]; total?: number; rows?: number; first?: number; loading?: boolean; empty?: string }>(),
  { total: 0, rows: 25, first: 0, loading: false, empty: '' },
)
const emit = defineEmits<{ page: [e: { page: number; rows: number }]; open: [u: UserItem] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
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
    row-hover
    class="app-clickable"
    @page="emit('page', $event)"
    @row-click="emit('open', $event.data)"
  >
    <template #empty>{{ props.empty || t('user.none') }}</template>
    <Column :header="t('common.customer')">
      <template #body="{ data: u }">
        <UserLink :user="u" :link="false" />
        <Tag v-if="u.role !== 'user'" :value="t('role.' + u.role)" severity="info" class="role" />
      </template>
    </Column>
    <Column :header="t('common.status')">
      <template #body="{ data: u }"><StatusTag kind="user" :status="u.status" /></template>
    </Column>
    <Column :header="t('user.wallet')" body-class="app-nowrap">
      <template #body="{ data: u }">
        <div v-for="w in u.wallets" :key="w.currency"><BalanceText :value="w" /></div>
        <span v-if="!u.wallets.length" class="app-muted">—</span>
      </template>
    </Column>
    <Column :header="t('user.services')">
      <template #body="{ data: u }">{{ num(u.subscriptions ?? 0, lang) }}</template>
    </Column>
    <Column :header="t('user.orders')">
      <template #body="{ data: u }">{{ num(u.orders ?? 0, lang) }}</template>
    </Column>
    <Column :header="t('user.joined')" body-class="app-nowrap">
      <template #body="{ data: u }">{{ date(u.created_at, lang) }}</template>
    </Column>
  </DataTable>
</template>

<style scoped>
.role {
  margin-inline-start: 0.5rem;
}
</style>
