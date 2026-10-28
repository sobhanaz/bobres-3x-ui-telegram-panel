<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import StatusTag from './StatusTag.vue'
import { date, dateTime, isolate, money, percent, planName, shortId, type Lang } from '../format'
import type { CodeItem } from '../types'

// A customer's own codes, read only: the reward codes issued to them and the gift codes they applied (a gift code is never kept, so
// its row has the last characters of the code only). Core lists nothing that cannot be attributed to the customer.
const props = withDefaults(defineProps<{ items: CodeItem[]; total?: number; rows?: number; first?: number; loading?: boolean }>(), {
  total: 0,
  rows: 25,
  first: 0,
  loading: false,
})
const emit = defineEmits<{ page: [e: { page: number; rows: number }] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

/** what a reward code takes off, or what a gift code added to the wallet */
const worth = (c: CodeItem) => (c.percent ? percent(c.percent, lang.value) : money({ amount: c.amount ?? 0, currency: c.currency ?? 'IRT' }, lang.value))
</script>

<template>
  <DataTable
    :value="props.items"
    lazy
    :paginator="props.total > props.rows"
    :rows="props.rows"
    :first="props.first"
    :total-records="props.total"
    :rows-per-page-options="[10, 25, 50]"
    :loading="props.loading"
    data-key="id"
    size="small"
    scrollable
    class="codes"
    @page="emit('page', $event)"
  >
    <template #empty>{{ t('user.codes_none') }}</template>
    <Column :header="t('code.received')" body-class="app-nowrap">
      <template #body="{ data: c }">{{ dateTime(c.received_at, lang) }}</template>
    </Column>
    <Column :header="t('code.code')">
      <template #body="{ data: c }">
        <span v-if="c.kind === 'reward'" class="app-ltr app-mono">{{ c.code }}</span>
        <template v-else>
          <span class="app-muted">{{ t('code.gift_ending') }}</span> <span class="app-ltr app-mono">{{ c.tail }}</span>
        </template>
      </template>
    </Column>
    <Column :header="t('code.kind')">
      <template #body="{ data: c }">{{ t('code.kind_' + c.kind) }}</template>
    </Column>
    <Column :header="t('code.worth')" body-class="app-nowrap">
      <template #body="{ data: c }">
        {{ worth(c) }}
        <div class="app-muted small">
          <template v-if="c.kind === 'gift'">{{ t('code.gift_added') }}</template>
          <template v-else-if="c.only_plan">{{ t('code.off_plan', { plan: isolate(planName(c.only_plan, lang)) }) }}</template>
          <template v-else>{{ t('code.off_any') }}</template>
        </div>
      </template>
    </Column>
    <Column :header="t('common.status')">
      <template #body="{ data: c }"><StatusTag kind="code" :status="c.state" /></template>
    </Column>
    <Column :header="t('code.valid_until')" body-class="app-nowrap">
      <template #body="{ data: c }">
        <span v-if="c.kind === 'gift'" class="app-muted">—</span>
        <template v-else>{{ c.expires_at ? date(c.expires_at, lang) : t('code.never') }}</template>
      </template>
    </Column>
    <Column :header="t('code.used_on')" body-class="app-nowrap">
      <template #body="{ data: c }">
        <template v-if="c.used_at">{{ dateTime(c.used_at, lang) }}</template>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
    <Column :header="t('code.from')">
      <template #body="{ data: c }">
        <template v-if="c.kind === 'reward' && c.plan">
          <bdi>{{ planName(c.plan, lang) }}</bdi>
          <div v-if="c.order_id" class="small">
            <RouterLink :to="{ name: 'order', params: { id: c.order_id } }">
              {{ t('code.from_order') }} <span class="app-ltr app-mono">{{ shortId(c.order_id) }}</span>
            </RouterLink>
          </div>
        </template>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
  </DataTable>
</template>

<style scoped>
.small {
  font-size: 0.8rem;
}
</style>
