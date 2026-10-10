<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Select from 'primevue/select'
import Chip from 'primevue/chip'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import UserLink from '../components/UserLink.vue'
import { useList } from '../composables/useList'
import { api, query } from '../api'
import { useAuth } from '../stores/auth'
import { dateTime, type Lang } from '../format'
import { auditLink, entityIdLabel, type AuditLink } from '../staff'
import type { AuditItem } from '../types'

const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()
const auth = useAuth()

// The address carries the staff member and item filters (links from the
// Staff and item pages, and from this page): read it, and follow it while open.
const fromQuery = (v: unknown) => (typeof v === 'string' ? v : '')
const list = useList<AuditItem, { action: string; entity: string; actor: string; entity_id: string; from: string }>('/audit', {
  action: '',
  entity: '',
  actor: fromQuery(route.query.actor),
  entity_id: fromQuery(route.query.entity_id),
  from: '',
})
onMounted(list.load)
watch(
  () => fromQuery(route.query.actor),
  (v) => (list.filters.actor = v),
)
watch(
  () => fromQuery(route.query.entity_id),
  (v) => (list.filters.entity_id = v),
)
function dropFilter(name: 'actor' | 'entity_id') {
  list.filters[name] = ''
  const q = { ...route.query }
  delete q[name]
  void router.replace({ query: q })
}

// The actions recorded so far, grouped by their first word for the filter.
const actions = ref<{ action: string; count: number }[]>([])
onMounted(async () => {
  try {
    actions.value = (await api<{ items: { action: string; count: number }[] }>('GET', '/audit/actions')).items
  } catch {
    /* the filter just lists fewer choices */
  }
})
const actionLabel = (a: string) => (te('audit.action.' + a) ? t('audit.action.' + a) : a)
const actionOptions = computed(() => {
  const groups = [...new Set(actions.value.map((a) => a.action.split('.')[0]))]
  return [
    { label: t('audit.all_actions'), value: '' },
    ...groups.flatMap((g) => [
      ...(actions.value.filter((a) => a.action.startsWith(g + '.')).length > 1
        ? [{ label: t('audit.group_all', { group: te('audit.group.' + g) ? t('audit.group.' + g) : g }), value: g }]
        : []),
      ...actions.value.filter((a) => a.action === g || a.action.startsWith(g + '.')).map((a) => ({ label: actionLabel(a.action), value: a.action })),
    ]),
  ]
})
const entityNames = ['user', 'subscription', 'order', 'plan', 'payment_intent', 'discount', 'settings', 'text', 'branding', 'staff', 'dashboard']
const entityLabel = (e: string) => (te('audit.entity.' + e) ? t('audit.entity.' + e) : e)
const sourceLabel = (s: string) => (te('audit.source.' + s) ? t('audit.source.' + s) : s)
const entities = computed(() => [
  { label: t('audit.all_items'), value: '' },
  ...entityNames.map((e) => ({ label: t('audit.entity.' + e), value: e })),
])
const period = ref('')
watch(period, (p) => {
  const start = new Date()
  start.setHours(0, 0, 0, 0)
  const today = Math.floor(start.getTime() / 1000)
  const day = 86_400
  list.filters.from = { today: String(today), week: String(today - 6 * day), month: String(today - 29 * day) }[p] ?? ''
})
const periods = computed(() => [
  { label: t('ledger.period.all'), value: '' },
  { label: t('ledger.period.today'), value: 'today' },
  { label: t('ledger.period.week'), value: 'week' },
  { label: t('ledger.period.month'), value: 'month' },
])
const csvHref = computed(() => '/api/v1/audit.csv' + query({ ...list.filters }))

// Where an entry's item lives in the dashboard, when the viewer may open it.
function itemLink(e: AuditItem): AuditLink | null {
  const to = auditLink(e)
  if (!to) return null
  try {
    const perm = router.resolve(to).meta.perm
    return typeof perm === 'string' && !auth.can(perm) ? null : to
  } catch {
    return null // no such page in this build
  }
}

const expanded = ref<Record<string, boolean>>({})
const pretty = (v: unknown) => JSON.stringify(v, null, 2)
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.audit.title') }}</h1>
    <a :href="csvHref" download class="p-button p-component p-button-outlined export">
      <i class="pi pi-download" aria-hidden="true" />
      <span>{{ t('ledger.export') }}</span>
    </a>
  </div>
  <div class="app-toolbar">
    <Chip v-if="list.filters.actor" :label="t('audit.one_staff')" removable @remove="dropFilter('actor')" />
    <Chip v-if="list.filters.entity_id" :label="t('audit.one_item')" removable @remove="dropFilter('entity_id')" />
    <Select v-model="list.filters.action" :placeholder="t('audit.all_actions')" :options="actionOptions" option-label="label" option-value="value" filter :aria-label="t('audit.action_col')" />
    <Select v-model="list.filters.entity" :placeholder="t('audit.all_items')" :options="entities" option-label="label" option-value="value" :aria-label="t('audit.item_col')" />
    <Select v-model="period" :placeholder="t('ledger.period.all')" :options="periods" option-label="label" option-value="value" :aria-label="t('ledger.period_label')" />
  </div>
  <Message v-if="list.error.value" severity="error" :closable="false">{{ list.error.value }}</Message>
  <p class="app-muted hint">{{ t('audit.hint') }}</p>
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
    @page="list.onPage"
  >
    <template #empty>{{ t('audit.none') }}</template>
    <Column :header="t('common.date')" body-class="app-nowrap">
      <template #body="{ data: e }">{{ dateTime(e.created_at, lang) }}</template>
    </Column>
    <Column :header="t('audit.who')">
      <template #body="{ data: e }">
        <UserLink v-if="e.actor" :user="e.actor" />
        <span v-else class="app-muted">{{ t('audit.system') }}</span>
        <div class="meta">
          <Tag v-if="e.source" :value="sourceLabel(e.source)" severity="secondary" class="source" />
          <span v-if="e.ip" class="app-muted small app-ltr">{{ e.ip }}</span>
        </div>
      </template>
    </Column>
    <Column :header="t('audit.action_col')">
      <template #body="{ data: e }">{{ actionLabel(e.action) }}</template>
    </Column>
    <Column :header="t('audit.item_col')">
      <template #body="{ data: e }">
        <RouterLink v-if="itemLink(e)" :to="itemLink(e)!">
          {{ entityLabel(e.entity) }}
          <span v-if="e.entity_id" class="app-ltr app-mono" :title="e.entity_id">{{ entityIdLabel(e.entity_id) }}</span>
        </RouterLink>
        <span v-else>
          {{ entityLabel(e.entity) }}
          <span v-if="e.entity_id" class="app-ltr app-mono app-muted" :title="e.entity_id">{{ entityIdLabel(e.entity_id) }}</span>
        </span>
      </template>
    </Column>
    <Column :header="t('common.reason')">
      <template #body="{ data: e }">
        <bdi v-if="e.reason">{{ e.reason }}</bdi>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
    <Column>
      <template #body="{ data: e }">
        <button v-if="e.after || e.before" type="button" class="details" :aria-expanded="!!expanded[e.id]" @click="expanded[e.id] = !expanded[e.id]">
          {{ expanded[e.id] ? t('audit.hide') : t('audit.details') }}
        </button>
        <div v-if="expanded[e.id]" class="json app-ltr">
          <div v-if="e.before"><strong>{{ t('audit.before') }}</strong><pre>{{ pretty(e.before) }}</pre></div>
          <div v-if="e.after"><strong>{{ t('audit.after') }}</strong><pre>{{ pretty(e.after) }}</pre></div>
        </div>
      </template>
    </Column>
  </DataTable>
</template>

<style scoped>
.export {
  text-decoration: none;
  gap: 0.5rem;
}
.hint {
  margin-block: 0 0.75rem;
  font-size: 0.9rem;
}
.small {
  font-size: 0.8rem;
}
.meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem;
  margin-block-start: 0.2rem;
}
.source {
  font-size: 0.72rem;
  padding-block: 0.05rem;
}
.details {
  background: none;
  border: 0;
  padding: 0;
  color: var(--p-primary-color);
  cursor: pointer;
  font: inherit;
}
.json {
  margin-block-start: 0.4rem;
  max-inline-size: 28rem;
}
.json pre {
  margin: 0.25rem 0 0.5rem;
  padding: 0.5rem;
  border-radius: 0.4rem;
  background: var(--p-content-hover-background);
  font-size: 0.8rem;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
