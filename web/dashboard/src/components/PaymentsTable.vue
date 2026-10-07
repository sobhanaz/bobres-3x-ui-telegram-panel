<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import UserLink from './UserLink.vue'
import StatusTag from './StatusTag.vue'
import { dateTime, money, type Lang } from '../format'
import type { PaymentItem } from '../types'

const props = withDefaults(
  defineProps<{ items: PaymentItem[]; total?: number; rows?: number; first?: number; loading?: boolean; lazy?: boolean; hideUser?: boolean }>(),
  { total: 0, rows: 25, first: 0, loading: false, lazy: true, hideUser: false },
)
const emit = defineEmits<{ page: [e: { page: number; rows: number }]; open: [p: PaymentItem] }>()
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
    @page="emit('page', $event)"
    @row-click="emit('open', $event.data)"
  >
    <template #empty>{{ t('payment.none') }}</template>
    <Column :header="t('common.date')" body-class="app-nowrap">
      <template #body="{ data: p }">{{ dateTime(p.created_at, lang) }}</template>
    </Column>
    <Column v-if="!props.hideUser" :header="t('common.customer')">
      <template #body="{ data: p }"><UserLink :user="p.user" /></template>
    </Column>
    <Column :header="t('payment.method')">
      <template #body="{ data: p }">
        {{ t('payment.provider.' + p.provider) }}
        <div v-if="!p.order_id" class="app-muted small">{{ t('payment.topup') }}</div>
      </template>
    </Column>
    <Column :header="t('common.amount')" body-class="app-nowrap">
      <template #body="{ data: p }">
        {{ money(p.amount, lang) }}
        <div v-if="p.gateway_amount && p.gateway_amount.currency !== p.amount.currency" class="app-muted small">
          {{ money(p.gateway_amount, lang) }}
        </div>
      </template>
    </Column>
    <Column :header="t('common.status')">
      <template #body="{ data: p }">
        <StatusTag kind="payment" :status="p.status" />
        <div v-if="p.proof?.decision === 'rejected' && p.proof.reason" class="app-muted small"><bdi>{{ p.proof.reason }}</bdi></div>
        <div v-else-if="p.failure_reason" class="app-muted small app-ltr">{{ p.failure_reason }}</div>
      </template>
    </Column>
    <Column :header="t('payment.proof')">
      <template #body="{ data: p }">
        <template v-if="p.proof">
          <a v-if="p.proof.has_file" :href="`/api/v1/payments/${p.id}/receipt`" target="_blank" rel="noopener" @click.stop>{{
            t('payment.receipt')
          }}</a>
          <div v-if="p.proof.reference" class="app-ltr app-mono small">{{ p.proof.reference }}</div>
          <div v-if="p.proof.txid" class="app-ltr app-mono small txid">{{ p.proof.txid }}</div>
        </template>
        <span v-else-if="p.provider_ref" class="app-ltr app-mono small">{{ p.provider_ref }}</span>
      </template>
    </Column>
  </DataTable>
</template>

<style scoped>
.small {
  font-size: 0.8rem;
}
.txid {
  max-inline-size: 12rem;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
