<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import UserLink from './UserLink.vue'
import StatusTag from './StatusTag.vue'
import TrafficBar from './TrafficBar.vue'
import { date, daysLeft, num, planName, shortId, type Lang } from '../format'
import type { ServiceItem } from '../types'

const props = withDefaults(
  defineProps<{ items: ServiceItem[]; total?: number; rows?: number; first?: number; loading?: boolean; lazy?: boolean; hideUser?: boolean }>(),
  { total: 0, rows: 25, first: 0, loading: false, lazy: true, hideUser: false },
)
const emit = defineEmits<{ page: [e: { page: number; rows: number }]; open: [s: ServiceItem] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const left = (s: ServiceItem) => (s.expires_at ? daysLeft(s.expires_at) : null)
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
    <template #empty>{{ t('service.none') }}</template>
    <Column :header="t('service.col_id')">
      <template #body="{ data: s }"><span class="app-ltr app-mono">{{ shortId(s.id) }}</span></template>
    </Column>
    <Column v-if="!props.hideUser" :header="t('common.customer')">
      <template #body="{ data: s }"><UserLink :user="s.user" /></template>
    </Column>
    <Column :header="t('common.plan')">
      <template #body="{ data: s }"><bdi>{{ planName(s.plan.name, lang) }}</bdi></template>
    </Column>
    <Column :header="t('common.status')">
      <template #body="{ data: s }"><StatusTag kind="service" :status="s.status" /></template>
    </Column>
    <Column :header="t('service.expires')" body-class="app-nowrap">
      <template #body="{ data: s }">
        <template v-if="s.expires_at">
          <div>{{ date(s.expires_at, lang) }}</div>
          <small class="app-muted">{{
            left(s)! >= 0 ? t('service.days_left', { n: num(left(s)!, lang) }) : t('service.days_ago', { n: num(-left(s)!, lang) })
          }}</small>
        </template>
        <span v-else class="app-muted">{{ t('service.never') }}</span>
      </template>
    </Column>
    <Column :header="t('service.traffic')">
      <template #body="{ data: s }"><TrafficBar :used="s.traffic_used" :total="s.traffic_total" /></template>
    </Column>
  </DataTable>
</template>
