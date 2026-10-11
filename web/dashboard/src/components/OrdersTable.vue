<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import UserLink from './UserLink.vue'
import StatusTag from './StatusTag.vue'
import { dateTime, isolate, money, planName, type Lang } from '../format'
import type { OrderItem } from '../types'

const props = withDefaults(
  defineProps<{ items: OrderItem[]; total?: number; rows?: number; first?: number; loading?: boolean; lazy?: boolean; hideUser?: boolean }>(),
  { total: 0, rows: 25, first: 0, loading: false, lazy: true, hideUser: false },
)
const emit = defineEmits<{ page: [e: { page: number; rows: number }]; open: [o: OrderItem] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
</script>

<template>
  <DataTable
    :value="props.items"
    :lazy="props.lazy"
    :paginator="props.lazy && props.total > props.rows"
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
    <template #empty>{{ t('order.none') }}</template>
    <Column :header="t('common.date')" body-class="app-nowrap">
      <template #body="{ data: o }">{{ dateTime(o.created_at, lang) }}</template>
    </Column>
    <Column v-if="!props.hideUser" :header="t('common.customer')">
      <template #body="{ data: o }"><UserLink :user="o.user" /></template>
    </Column>
    <Column :header="t('common.plan')">
      <template #body="{ data: o }">
        <bdi>{{ planName(o.plan.name, lang) }}</bdi>
        <div class="app-muted small">
          {{ t('order.type.' + o.type) }}<template v-if="o.staff"> · {{ t('order.by_staff', { who: isolate(o.staff) }) }}</template>
        </div>
      </template>
    </Column>
    <Column :header="t('common.amount')" body-class="app-nowrap">
      <template #body="{ data: o }">
        {{ money(o.amount, lang) }}
        <div v-if="o.discount" class="app-muted small app-ltr">{{ o.discount.code }}</div>
      </template>
    </Column>
    <Column :header="t('common.status')">
      <template #body="{ data: o }">
        <StatusTag kind="order" :status="o.status" />
        <div v-if="o.refund" class="small refund">{{ t('order.refunded') }}</div>
      </template>
    </Column>
  </DataTable>
</template>

<style scoped>
.small {
  font-size: 0.8rem;
}
.refund {
  color: var(--p-orange-500);
  margin-block-start: 0.2rem;
}
</style>
