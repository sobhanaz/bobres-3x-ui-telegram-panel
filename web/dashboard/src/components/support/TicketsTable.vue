<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import UserLink from '../UserLink.vue'
import TicketStatusTag from './TicketStatusTag.vue'
import { dateTime, type Lang } from '../../format'
import { duration, isLate, staffName, waitingSeconds } from '../../support'
import type { TicketItem } from '../../types'

// Tickets in a lazy table (the inbox, a customer's Tickets tab). Subjects and
// previews are the customer's own words: shown as text, in their direction.
const props = withDefaults(
  defineProps<{ items: TicketItem[]; total?: number; rows?: number; first?: number; loading?: boolean; hideUser?: boolean; now?: number; empty?: string }>(),
  { total: 0, rows: 25, first: 0, loading: false, hideUser: false, now: 0, empty: '' },
)
const emit = defineEmits<{ page: [e: { page: number; rows: number }]; open: [t: TicketItem] }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const clock = computed(() => props.now || Math.floor(Date.now() / 1000))
const category = (c: string) => (te('support.category.' + c) ? t('support.category.' + c) : c)
const previewIcon: Record<string, string> = { staff: 'pi pi-reply', note: 'pi pi-lock' }
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
    class="app-clickable tickets"
    @page="emit('page', $event)"
    @row-click="emit('open', $event.data)"
  >
    <template #empty>{{ props.empty || t('support.none') }}</template>
    <Column header="#" body-class="app-nowrap">
      <template #body="{ data: tk }">
        <RouterLink :to="{ name: 'ticket', params: { id: tk.id } }" class="number app-ltr app-mono" @click.stop>#{{ tk.number }}</RouterLink>
      </template>
    </Column>
    <Column v-if="!props.hideUser" :header="t('common.customer')">
      <template #body="{ data: tk }"><UserLink :user="tk.user" /></template>
    </Column>
    <Column :header="t('support.subject')" class="subject-col">
      <template #body="{ data: tk }">
        <bdi class="subject" dir="auto">{{ tk.subject || '—' }}</bdi>
        <div v-if="tk.last_message" class="preview app-muted">
          <i v-if="previewIcon[tk.last_message.kind]" :class="previewIcon[tk.last_message.kind]" aria-hidden="true" />
          <span dir="auto">{{ tk.last_message.preview }}</span>
        </div>
      </template>
    </Column>
    <Column :header="t('support.category_label')" body-class="app-nowrap">
      <template #body="{ data: tk }">{{ category(tk.category) }}</template>
    </Column>
    <Column :header="t('common.status')">
      <template #body="{ data: tk }">
        <TicketStatusTag :status="tk.status" />
        <div v-if="waitingSeconds(tk, clock) !== null" class="wait small" :class="{ late: isLate(waitingSeconds(tk, clock)) }">
          <i class="pi pi-clock" aria-hidden="true" />
          {{ t('support.waiting', { time: duration(waitingSeconds(tk, clock) ?? 0, lang) }) }}
        </div>
      </template>
    </Column>
    <Column :header="t('support.assignee')" body-class="app-nowrap">
      <template #body="{ data: tk }">
        <bdi v-if="tk.assignee">{{ staffName(tk.assignee) }}</bdi>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
    <Column :header="t('support.updated')" body-class="app-nowrap">
      <template #body="{ data: tk }">
        {{ dateTime(tk.updated_at, lang) }}
        <div v-if="tk.status === 'closed' && tk.closed_at" class="app-muted small">{{ t('support.closed_short') }}</div>
        <div v-else-if="props.hideUser" class="app-muted small">{{ t('support.opened_on', { date: dateTime(tk.created_at, lang) }) }}</div>
      </template>
    </Column>
  </DataTable>
</template>

<style scoped>
.number {
  color: var(--p-primary-color);
  text-decoration: none;
  font-weight: 500;
}
.number:hover {
  text-decoration: underline;
}
.tickets :deep(.subject-col) {
  min-inline-size: 14rem;
  max-inline-size: 28rem;
}
.subject {
  display: block;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.preview {
  display: flex;
  gap: 0.35rem;
  align-items: baseline;
  font-size: 0.85rem;
  margin-block-start: 0.15rem;
}
.preview span {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
  white-space: pre-line;
}
.preview i {
  font-size: 0.75rem;
  flex: none;
}
.small {
  font-size: 0.8rem;
}
.wait {
  display: flex;
  gap: 0.3rem;
  align-items: center;
  margin-block-start: 0.25rem;
  white-space: nowrap;
  color: var(--p-text-muted-color);
}
.wait.late {
  color: var(--p-orange-500);
  font-weight: 500;
}
</style>
