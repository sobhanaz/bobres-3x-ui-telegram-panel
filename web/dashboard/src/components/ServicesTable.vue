<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Tag from 'primevue/tag'
import Checkbox from 'primevue/checkbox'
import UserLink from './UserLink.vue'
import StatusTag from './StatusTag.vue'
import TrafficBar from './TrafficBar.vue'
import { date, dateTime, daysLeft, num, planName, shortId, type Lang } from '../format'
import type { ServiceItem } from '../types'

const props = withDefaults(
  defineProps<{
    items: ServiceItem[]
    total?: number
    rows?: number
    first?: number
    loading?: boolean
    lazy?: boolean
    hideUser?: boolean
    /** rows get checkboxes, and the header one selects the whole page (the bulk actions of "Not on the panel") */
    selectable?: boolean
  }>(),
  { total: 0, rows: 25, first: 0, loading: false, lazy: true, hideUser: false, selectable: false },
)
const emit = defineEmits<{ page: [e: { page: number; rows: number }]; open: [s: ServiceItem] }>()
/** the ids of the selected services */
const selected = defineModel<string[]>('selected', { default: () => [] })
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const left = (s: ServiceItem) => (s.expires_at ? daysLeft(s.expires_at) : null)

const allOnPage = computed(() => props.items.length > 0 && props.items.every((s) => selected.value.includes(s.id)))
function pickPage(on: boolean) {
  const ids = props.items.map((s) => s.id)
  selected.value = on ? [...new Set([...selected.value, ...ids])] : selected.value.filter((id) => !ids.includes(id))
}
function pick(id: string, on: boolean) {
  selected.value = on ? [...new Set([...selected.value, id])] : selected.value.filter((x) => x !== id)
}
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
    <Column v-if="props.selectable" header-class="pick-col" body-class="pick-col">
      <template #header>
        <Checkbox
          :model-value="allOnPage"
          binary
          input-id="pick-page"
          class="pick-page"
          :disabled="!props.items.length"
          :aria-label="t('service.bulk.select_page')"
          :title="t('service.bulk.select_page')"
          @update:model-value="(v: boolean) => pickPage(v)"
        />
      </template>
      <template #body="{ data: s }">
        <!-- a tap on the box selects; it does not open the service -->
        <span class="pick-wrap" @click.stop>
          <Checkbox
            :model-value="selected.includes(s.id)"
            binary
            class="pick"
            :aria-label="t('service.bulk.select_one', { email: s.client_email })"
            @update:model-value="(v: boolean) => pick(s.id, v)"
          />
        </span>
      </template>
    </Column>
    <Column :header="t('service.col_id')">
      <template #body="{ data: s }">
        <span class="app-ltr app-mono">{{ shortId(s.id) }}</span>
        <div v-if="s.label" class="app-muted label"><bdi>{{ s.label }}</bdi></div>
      </template>
    </Column>
    <Column v-if="!props.hideUser" :header="t('common.customer')">
      <template #body="{ data: s }"><UserLink :user="s.user" /></template>
    </Column>
    <Column :header="t('common.plan')">
      <template #body="{ data: s }"><bdi>{{ planName(s.plan.name, lang) }}</bdi></template>
    </Column>
    <Column :header="t('common.status')">
      <template #body="{ data: s }">
        <div class="states">
          <StatusTag kind="service" :status="s.status" />
          <Tag
            v-if="s.panel_missing_since"
            severity="warn"
            icon="pi pi-exclamation-triangle"
            :value="t('service.panel_missing')"
            :title="t('service.panel_missing_since', { time: dateTime(s.panel_missing_since, lang) })"
            class="panel-missing"
          />
        </div>
      </template>
    </Column>
    <Column :header="t('service.expires')" body-class="app-nowrap">
      <template #body="{ data: s }">
        <template v-if="s.expires_at">
          <div>{{ date(s.expires_at, lang) }}</div>
          <small class="app-muted">{{
            left(s)! >= 0 ? t('service.days_left', { n: num(left(s)!, lang) }, left(s)!) : t('service.days_ago', { n: num(-left(s)!, lang) }, -left(s)!)
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

<style scoped>
.label {
  font-size: 0.8rem;
}
.states {
  display: flex;
  flex-wrap: wrap;
  gap: 0.3rem;
}
.panel-missing {
  white-space: nowrap;
}
:deep(.pick-col) {
  inline-size: 2.75rem;
}
.pick-wrap {
  display: inline-flex;
  padding: 0.25rem;
  cursor: default;
}
</style>
