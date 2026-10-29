<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import InputText from 'primevue/inputtext'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import Select from 'primevue/select'
import ToggleSwitch from 'primevue/toggleswitch'
import Message from 'primevue/message'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import ProgressBar from 'primevue/progressbar'
import ServicesTable from '../components/ServicesTable.vue'
import ReasonDialog from '../components/ReasonDialog.vue'
import { useList } from '../composables/useList'
import { api } from '../api'
import { errorText } from '../errors'
import { useAuth } from '../stores/auth'
import { num, type Lang } from '../format'
import type { ServiceItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()
const toast = useToast()
const auth = useAuth()

// "Not on the panel" is in the address (?panel=missing, the Overview links to it), so a link or a reload keeps it:
// read it, and follow it while the page is open.
const panelOf = (v: unknown) => (v === 'missing' ? 'missing' : '')
const list = useList<ServiceItem, { q: string; status: string; panel: string }>('/services', { q: '', status: '', panel: panelOf(route.query.panel) })
onMounted(list.load)
const missingOnly = computed({
  get: () => list.filters.panel === 'missing',
  set: (v: boolean) => (list.filters.panel = v ? 'missing' : ''),
})
watch(
  () => panelOf(route.query.panel),
  (p) => {
    if (route.name === 'services') list.filters.panel = p
  },
)
watch(
  () => list.filters.panel,
  (p) => {
    if (panelOf(route.query.panel) !== p) void router.replace({ query: { ...route.query, panel: p || undefined } })
  },
)

const statuses = computed(() => [
  { label: t('service.all_live'), value: '' },
  ...['active', 'expiring_soon', 'expired', 'depleted', 'disabled', 'pending', 'deleted'].map((s) => ({
    label: t('service.status.' + s),
    value: s,
  })),
])

// The bulk actions of "Not on the panel" (services.write): pick services on the page, then make their clients
// again or delete them, with one reason for all. Each is sent on its own with only_missing, so a service that is
// back on the panel or deleted since the list was loaded is skipped (core says so) and nothing is done to it.
const bulkOn = computed(() => missingOnly.value && auth.can('services.write'))
const selected = ref<string[]>([])
watch(
  () => list.items.value,
  (items) => (selected.value = selected.value.filter((id) => items.some((s) => s.id === id))),
)
watch(bulkOn, (on) => {
  if (!on) selected.value = []
})
const picked = computed(() => list.items.value.filter((s) => selected.value.includes(s.id)))

type BulkAction = 'recreate' | 'delete'
interface Outcome {
  service: ServiceItem
  result: 'done' | 'skipped' | 'failed'
  note: string
}
const bulk = ref<BulkAction | null>(null)
const targets = ref<ServiceItem[]>([])
const running = ref(false)
const progress = ref({ done: 0, total: 0 })
const outcomes = ref<Outcome[] | null>(null)
const asking = computed({
  get: () => bulk.value !== null && outcomes.value === null,
  set: (v: boolean) => {
    if (!v && !running.value) bulk.value = null
  },
})
const summaryOpen = computed({
  get: () => outcomes.value !== null,
  set: (v: boolean) => {
    if (!v) {
      outcomes.value = null
      bulk.value = null
    }
  },
})
const count = (n: number) => ({ n: num(n, lang.value) })
const dialogText = computed(() => {
  const n = targets.value.length
  return bulk.value === 'delete'
    ? { title: t('service.bulk.delete_title', count(n), n), message: t('service.bulk.delete_intro'), confirm: t('service.bulk.delete'), danger: true }
    : { title: t('service.bulk.recreate_title', count(n), n), message: t('service.bulk.recreate_intro'), confirm: t('service.bulk.recreate'), danger: false }
})
const tally = computed(() => {
  const o = outcomes.value ?? []
  const n = (r: Outcome['result']) => num(o.filter((x) => x.result === r).length, lang.value)
  return t('service.bulk.summary', { done: n('done'), skipped: n('skipped'), failed: n('failed') })
})

function startBulk(a: BulkAction) {
  if (!picked.value.length) return
  targets.value = picked.value.slice()
  outcomes.value = null
  progress.value = { done: 0, total: targets.value.length }
  bulk.value = a
}

async function runBulk(reason: string) {
  const action = bulk.value
  if (!action || running.value) return
  running.value = true
  progress.value = { done: 0, total: targets.value.length }
  const out: Outcome[] = []
  for (const s of targets.value) {
    try {
      const path = action === 'delete' ? `/services/${s.id}/delete` : `/services/${s.id}/recreate-client`
      const res = await api<{ service: ServiceItem; skipped?: string }>('POST', path, { reason, only_missing: true })
      if (res.skipped) {
        out.push({ service: s, result: 'skipped', note: t(res.skipped === 'deleted' ? 'service.bulk.skipped_deleted' : 'service.bulk.skipped_back') })
      } else {
        out.push({ service: s, result: 'done', note: '' })
      }
    } catch (e) {
      const err = errorText(e, t)
      out.push({ service: s, result: 'failed', note: err.detail ? `${err.text} ${err.detail}` : err.text })
    }
    progress.value = { ...progress.value, done: progress.value.done + 1 }
  }
  running.value = false
  outcomes.value = out
  selected.value = []
  const failed = out.some((o) => o.result === 'failed')
  toast.add({ severity: failed ? 'warn' : 'success', summary: t('service.bulk.done_' + action), detail: tally.value, life: 6000 })
  await list.load()
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.services.title') }}</h1>
  </div>
  <div class="app-toolbar">
    <IconField class="app-grow">
      <InputIcon class="pi pi-search" />
      <InputText v-model="list.filters.q" :placeholder="t('service.search')" fluid :aria-label="t('service.search')" />
    </IconField>
    <Select v-model="list.filters.status" :placeholder="t('service.all_live')" :options="statuses" option-label="label" option-value="value" :aria-label="t('common.status')" />
    <label class="panel-filter">
      <ToggleSwitch v-model="missingOnly" input-id="only-missing" />
      <span>{{ t('service.only_missing') }}</span>
    </label>
  </div>
  <div v-if="bulkOn && list.items.value.length" class="app-toolbar bulk-bar" role="toolbar" :aria-label="t('service.bulk.toolbar')">
    <span class="app-muted bulk-count">{{ selected.length ? t('service.bulk.selected', count(selected.length), selected.length) : t('service.bulk.hint') }}</span>
    <Button
      :label="t('service.bulk.recreate')"
      icon="pi pi-replay"
      size="small"
      severity="warn"
      outlined
      class="bulk-recreate"
      :disabled="!selected.length"
      @click="startBulk('recreate')"
    />
    <Button
      :label="t('service.bulk.delete')"
      icon="pi pi-trash"
      size="small"
      severity="danger"
      outlined
      class="bulk-delete"
      :disabled="!selected.length"
      @click="startBulk('delete')"
    />
  </div>
  <Message v-if="list.error.value" severity="error" :closable="false">{{ list.error.value }}</Message>
  <ServicesTable
    v-model:selected="selected"
    :items="list.items.value"
    :total="list.total.value"
    :rows="list.rows.value"
    :first="(list.page.value - 1) * list.rows.value"
    :loading="list.loading.value"
    :selectable="bulkOn"
    @page="list.onPage"
    @open="(s) => router.push({ name: 'service', params: { id: s.id } })"
  />

  <ReasonDialog
    v-model:visible="asking"
    :title="dialogText.title"
    :message="dialogText.message"
    :confirm-label="dialogText.confirm"
    :danger="dialogText.danger"
    :busy="running"
    @confirm="runBulk"
  >
    <ul class="bulk-targets">
      <li v-for="s in targets" :key="s.id" class="app-ltr app-mono">{{ s.client_email }}</li>
    </ul>
    <div v-if="running" class="bulk-progress" role="status">
      <ProgressBar :value="progress.total ? Math.round((progress.done * 100) / progress.total) : 0" :show-value="false" />
      <small>{{ t('service.bulk.progress', { done: num(progress.done, lang), total: num(progress.total, lang) }) }}</small>
    </div>
  </ReasonDialog>

  <Dialog v-model:visible="summaryOpen" :header="t('service.bulk.summary_title')" modal :style="{ inlineSize: 'min(34rem, 94vw)' }" :draggable="false">
    <p class="bulk-tally">{{ tally }}</p>
    <ul class="bulk-results">
      <li v-for="o in outcomes ?? []" :key="o.service.id" :class="['bulk-result', 'is-' + o.result]">
        <i :class="o.result === 'done' ? 'pi pi-check' : o.result === 'skipped' ? 'pi pi-minus-circle' : 'pi pi-times-circle'" aria-hidden="true" />
        <bdi class="app-ltr app-mono">{{ o.service.client_email }}</bdi>
        <span>{{ t('service.bulk.result_' + o.result) }}</span>
        <small v-if="o.note" class="app-muted note">{{ o.note }}</small>
      </li>
    </ul>
    <template #footer>
      <Button :label="t('service.bulk.close')" @click="summaryOpen = false" />
    </template>
  </Dialog>
</template>

<style scoped>
.panel-filter {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  cursor: pointer;
}
.bulk-bar {
  align-items: center;
  flex-wrap: wrap;
}
.bulk-count {
  flex: 1 1 12rem;
}
.bulk-targets {
  max-block-size: 9rem;
  overflow-y: auto;
  margin: 0.5rem 0 0;
  padding-inline-start: 1.25rem;
  font-size: 0.85rem;
}
.bulk-progress {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  margin-block-start: 0.75rem;
}
.bulk-tally {
  margin-block-start: 0;
  font-weight: 600;
}
.bulk-results {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  max-block-size: 50vh;
  overflow-y: auto;
}
.bulk-result {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.4rem;
}
.bulk-result .note {
  flex-basis: 100%;
  padding-inline-start: 1.4rem;
}
.is-done i {
  color: var(--p-green-500);
}
.is-skipped i {
  color: var(--p-surface-500);
}
.is-failed i {
  color: var(--p-red-500);
}
</style>
