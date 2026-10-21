<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import Dialog from 'primevue/dialog'
import Textarea from 'primevue/textarea'
import Message from 'primevue/message'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import { api } from '../../api'
import { errorText } from '../../errors'
import { date, dateTime, num, type Lang } from '../../format'
import {
  allTiers,
  blankLicense,
  daysLeft,
  editBody,
  monthChoices,
  newLicenseBody,
  newLicenseProblem,
  requestId,
  statusSeverity,
  type EditLicenseForm,
  type ServerLicense,
} from '../../vendor'

// Every license of the license server: the store's own, the ones made by hand and the ones the bot sold.
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()

const items = ref<ServerLicense[]>([])
const loading = ref(false)
const error = ref('')
const statusFilter = ref('')
const search = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    const q = statusFilter.value ? `?status=${statusFilter.value}` : ''
    items.value = (await api<{ items: ServerLicense[] }>('GET', '/vendor/server-licenses' + q)).items
  } catch (e) {
    error.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

const shown = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return items.value
  return items.value.filter((l) => [l.customer, l.id, l.active_domain ?? ''].some((s) => s.toLowerCase().includes(q)))
})
const statusOptions = computed(() => [
  { label: t('licenses.all'), value: '' },
  { label: t('licenses.status.active'), value: 'active' },
  { label: t('licenses.status.revoked'), value: 'revoked' },
])

function fail(e: unknown) {
  toast.add({ severity: 'error', summary: errorText(e, t).text, life: 6000 })
}

// ---- Make one by hand
const making = ref(false)
const form = ref(blankLicense())
const makeId = ref('')
const working = ref(false)
const problem = computed(() => newLicenseProblem(form.value))
const monthOptions = computed(() => monthChoices.map((m) => ({ label: t('licenses.months', { n: num(m, lang.value) }, m), value: m })))
const modeOptions = computed(() => [
  { label: t('licenses.mode.months'), value: 'months' },
  { label: t('licenses.mode.date'), value: 'date' },
  { label: t('licenses.mode.none'), value: 'none' },
  ...(form.value.tier === 'trial' ? [{ label: t('licenses.mode.default'), value: 'default' }] : []),
])
const tierOptions = computed(() => allTiers.map((x) => ({ label: t('licenses.tier.' + x), value: x })))
function openNew() {
  form.value = blankLicense()
  makeId.value = requestId()
  making.value = true
}
const keyShown = ref<ServerLicense | null>(null) // the license just made (or re-keyed): its key is shown once
async function create() {
  if (problem.value) return
  working.value = true
  try {
    keyShown.value = await api<ServerLicense>('POST', '/vendor/server-licenses', newLicenseBody(form.value, makeId.value))
    making.value = false
    await load()
  } catch (e) {
    fail(e)
  } finally {
    working.value = false
  }
}

// ---- Details
const detail = ref<ServerLicense | null>(null)
const opening = ref(false)
async function openDetail(l: ServerLicense) {
  opening.value = true
  try {
    detail.value = await api<ServerLicense>('GET', `/vendor/server-licenses/${l.id}`)
  } catch (e) {
    fail(e)
  } finally {
    opening.value = false
  }
}
const canRekey = computed(() => detail.value?.status === 'active' && (detail.value.activations ?? []).length === 0)
async function rekey() {
  if (!detail.value) return
  working.value = true
  try {
    keyShown.value = await api<ServerLicense>('POST', `/vendor/server-licenses/${detail.value.id}/rekey`, { request_id: requestId() })
    detail.value = null
  } catch (e) {
    fail(e)
  } finally {
    working.value = false
  }
}

// ---- Change one
const editing = ref<ServerLicense | null>(null)
const edit = ref<EditLicenseForm>({ customer: '', tier: 'standard', mode: 'keep', date: '' })
const editId = ref('')
const editModes = computed(() => [
  { label: t('licenses.edit.keep'), value: 'keep' },
  { label: t('licenses.mode.date'), value: 'date' },
  { label: t('licenses.mode.none'), value: 'none' },
])
function openEdit(l: ServerLicense) {
  editing.value = l
  edit.value = { customer: l.customer, tier: l.tier, mode: 'keep', date: '' }
  editId.value = requestId()
}
const editProblem = computed(() => {
  if (!editing.value) return ''
  const c = edit.value.customer.trim()
  if (c === '' || c.length > 200) return 'customer'
  if (edit.value.mode === 'date' && newLicenseProblem({ ...blankLicense(), customer: 'x', mode: 'date', date: edit.value.date }) === 'date') return 'date'
  return editBody(editing.value, edit.value, 'placeholder') === null ? 'nothing' : ''
})
async function saveEdit() {
  if (!editing.value || editProblem.value) return
  const body = editBody(editing.value, edit.value, editId.value)
  if (!body) return
  working.value = true
  try {
    await api('PUT', `/vendor/server-licenses/${editing.value.id}`, body)
    toast.add({ severity: 'success', summary: t('licenses.changed'), life: 3000 })
    editing.value = null
    await load()
  } catch (e) {
    fail(e)
  } finally {
    working.value = false
  }
}
const extendMonths = ref<number>(1)
async function extend() {
  if (!editing.value) return
  working.value = true
  try {
    await api('PUT', `/vendor/server-licenses/${editing.value.id}`, { extend_months: extendMonths.value, request_id: requestId() })
    toast.add({ severity: 'success', summary: t('licenses.changed'), life: 3000 })
    editing.value = null
    await load()
  } catch (e) {
    fail(e)
  } finally {
    working.value = false
  }
}

// ---- Revoke
const revoking = ref<ServerLicense | null>(null)
const reason = ref('')
async function revoke() {
  if (!revoking.value || reason.value.trim() === '') return
  working.value = true
  try {
    await api('POST', `/vendor/server-licenses/${revoking.value.id}/revoke`, { reason: reason.value.trim() })
    toast.add({ severity: 'success', summary: t('licenses.revoked'), life: 3000 })
    revoking.value = null
    reason.value = ''
    await load()
  } catch (e) {
    fail(e)
  } finally {
    working.value = false
  }
}

async function copyKey() {
  if (!keyShown.value?.key) return
  try {
    await navigator.clipboard.writeText(keyShown.value.key)
    toast.add({ severity: 'success', summary: t('licenses.copied'), life: 2000 })
  } catch {
    /* the key is on screen to select by hand */
  }
}
const entitlementsText = computed(() => (detail.value?.entitlements ? JSON.stringify(detail.value.entitlements, null, 2) : ''))
function endText(l: ServerLicense) {
  if (l.ends_at === null) return t('licenses.no_end')
  const d = daysLeft(l.ends_at)
  return d !== null && d < 0 ? t('licenses.ended_on', { date: date(l.ends_at, lang.value) }) : date(l.ends_at, lang.value)
}
</script>

<template>
  <p class="app-muted intro">{{ t('licenses.all_intro') }}</p>
  <div class="app-toolbar">
    <Select v-model="statusFilter" class="status-filter" :options="statusOptions" option-label="label" option-value="value" :aria-label="t('licenses.status_col')" @change="load" />
    <IconField class="grow">
      <InputIcon class="pi pi-search" />
      <InputText v-model="search" type="search" class="search" :placeholder="t('licenses.search')" :aria-label="t('licenses.search')" />
    </IconField>
    <Button v-if="loading" text loading :aria-label="t('app.loading')" />
    <Button class="new" icon="pi pi-plus" :label="t('licenses.new')" @click="openNew" />
  </div>
  <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>

  <DataTable :value="shown" paginator :rows="25" :rows-per-page-options="[25, 50, 100]" data-key="id" size="small" scrollable class="all" :loading="loading">
    <template #empty>{{ t('licenses.none_all') }}</template>
    <Column :header="t('licenses.customer')">
      <template #body="{ data: l }">
        <bdi class="name">{{ l.customer }}</bdi>
        <div class="app-muted small app-mono app-ltr">{{ l.id }}</div>
      </template>
    </Column>
    <Column :header="t('licenses.tier_col')">
      <template #body="{ data: l }">{{ t('licenses.tier.' + l.tier) }}</template>
    </Column>
    <Column :header="t('licenses.status_col')">
      <template #body="{ data: l }">
        <Tag :severity="statusSeverity(l.status)" :value="t('licenses.status.' + l.status)" />
      </template>
    </Column>
    <Column :header="t('licenses.ends')" body-class="app-nowrap">
      <template #body="{ data: l }">{{ endText(l) }}</template>
    </Column>
    <Column :header="t('licenses.runs_on')">
      <template #body="{ data: l }">
        <span v-if="l.active_domain" class="app-ltr app-mono">{{ l.active_domain }}</span>
        <span v-else class="app-muted">{{ t('licenses.not_activated') }}</span>
        <div v-if="l.last_seen_at" class="app-muted small">{{ t('licenses.last_seen', { date: dateTime(l.last_seen_at, lang) }) }}</div>
      </template>
    </Column>
    <Column :header="t('licenses.moves')" body-class="app-nowrap">
      <template #body="{ data: l }">{{ num(l.moves, lang) }}</template>
    </Column>
    <Column :header="t('licenses.actions')">
      <template #body="{ data: l }">
        <div class="row-actions">
          <Button class="details" icon="pi pi-eye" text rounded :loading="opening" :aria-label="t('licenses.details')" @click="openDetail(l)" />
          <Button class="edit" icon="pi pi-pencil" text rounded :disabled="l.status !== 'active'" :aria-label="t('licenses.edit.title')" @click="openEdit(l)" />
          <Button class="revoke" icon="pi pi-ban" text rounded severity="danger" :disabled="l.status !== 'active'" :aria-label="t('licenses.revoke')" @click="revoking = l" />
        </div>
      </template>
    </Column>
  </DataTable>

  <!-- A license by hand -->
  <Dialog :visible="making" modal :header="t('licenses.new')" :style="{ width: '28rem' }" @update:visible="(v: boolean) => !v && (making = false)">
    <form class="form" @submit.prevent="create">
      <label class="field">
        <span>{{ t('licenses.customer') }}</span>
        <InputText v-model="form.customer" maxlength="200" :invalid="problem === 'customer' && form.customer !== ''" />
        <small class="app-muted">{{ t('licenses.customer_help') }}</small>
      </label>
      <label class="field">
        <span>{{ t('licenses.tier_col') }}</span>
        <Select v-model="form.tier" :options="tierOptions" option-label="label" option-value="value" @change="form.mode = form.tier === 'trial' ? 'default' : form.mode === 'default' ? 'months' : form.mode" />
      </label>
      <label class="field">
        <span>{{ t('licenses.end') }}</span>
        <Select v-model="form.mode" :options="modeOptions" option-label="label" option-value="value" />
      </label>
      <Select v-if="form.mode === 'months'" v-model="form.months" :options="monthOptions" option-label="label" option-value="value" :aria-label="t('licenses.end')" />
      <label v-if="form.mode === 'date'" class="field">
        <span>{{ t('licenses.end_date') }}</span>
        <InputText v-model="form.date" class="app-ltr" placeholder="2027-12-31" :invalid="problem === 'date' && form.date !== ''" />
        <small class="app-muted">{{ t('licenses.end_date_help') }}</small>
      </label>
      <small v-if="form.mode === 'default'" class="app-muted">{{ t('licenses.default_help') }}</small>
    </form>
    <template #footer>
      <Button text :label="t('app.cancel')" @click="making = false" />
      <Button :label="t('licenses.make')" :loading="working" :disabled="problem !== ''" @click="create" />
    </template>
  </Dialog>

  <!-- The key, once -->
  <Dialog :visible="keyShown !== null" modal :header="t('licenses.key_once')" :style="{ width: '30rem' }" @update:visible="(v: boolean) => !v && (keyShown = null)">
    <Message v-if="keyShown?.replayed" severity="info" :closable="false">{{ t('licenses.replayed') }}</Message>
    <template v-else>
      <p class="app-muted">{{ t('licenses.key_once_help') }}</p>
      <Textarea :model-value="keyShown?.key" readonly rows="2" class="keybox app-ltr app-mono" :aria-label="t('licenses.key_once')" />
    </template>
    <template #footer>
      <Button v-if="!keyShown?.replayed" outlined icon="pi pi-copy" :label="t('licenses.copy')" @click="copyKey" />
      <Button :label="t('licenses.done')" @click="keyShown = null" />
    </template>
  </Dialog>

  <!-- Details -->
  <Dialog :visible="detail !== null" modal :header="detail?.customer" :style="{ width: '46rem', maxWidth: '96vw' }" @update:visible="(v: boolean) => !v && (detail = null)">
    <template v-if="detail">
      <p class="app-muted app-mono app-ltr">{{ detail.id }} · {{ t('licenses.tier.' + detail.tier) }} · {{ t('licenses.status.' + detail.status) }} · {{ endText(detail) }} · {{ t('licenses.grace', { n: num(detail.grace_days, lang) }, detail.grace_days) }}</p>
      <p v-if="detail.reason" class="app-muted">{{ t('licenses.reason_was') }} <bdi>{{ detail.reason }}</bdi></p>
      <h3>{{ t('licenses.activations') }}</h3>
      <DataTable :value="detail.activations ?? []" size="small" class="acts">
        <template #empty>{{ t('licenses.not_activated') }}</template>
        <Column :header="t('licenses.domain')"><template #body="{ data: a }"><span class="app-ltr app-mono">{{ a.domain }}</span></template></Column>
        <Column :header="t('licenses.since')"><template #body="{ data: a }">{{ dateTime(a.activated_at, lang) }}</template></Column>
        <Column :header="t('licenses.seen')"><template #body="{ data: a }">{{ dateTime(a.last_seen_at, lang) }}</template></Column>
        <Column :header="t('licenses.version')"><template #body="{ data: a }"><span class="app-ltr">{{ a.version }}</span></template></Column>
        <Column :header="t('licenses.state')">
          <template #body="{ data: a }">{{ a.deactivated_at ? t('licenses.moved_away') : t('licenses.running') }}<span v-if="a.counted" class="app-muted"> · {{ t('licenses.counted') }}</span></template>
        </Column>
      </DataTable>
      <h3>{{ t('licenses.history') }}</h3>
      <DataTable :value="detail.audit ?? []" size="small" class="acts">
        <Column :header="t('licenses.when')"><template #body="{ data: e }">{{ dateTime(e.at, lang) }}</template></Column>
        <Column :header="t('licenses.what')"><template #body="{ data: e }"><span class="app-ltr app-mono">{{ e.action }}</span></template></Column>
        <Column :header="t('licenses.who')"><template #body="{ data: e }"><span class="app-ltr">{{ e.actor }}</span></template></Column>
      </DataTable>
      <h3>{{ t('licenses.grants') }}</h3>
      <pre class="app-ltr app-mono grants">{{ entitlementsText }}</pre>
    </template>
    <template #footer>
      <Button v-if="canRekey" outlined severity="secondary" icon="pi pi-key" :label="t('licenses.rekey')" :loading="working" @click="rekey" />
      <Button :label="t('licenses.done')" @click="detail = null" />
    </template>
  </Dialog>

  <!-- Change one -->
  <Dialog :visible="editing !== null" modal :header="t('licenses.edit.title')" :style="{ width: '28rem' }" @update:visible="(v: boolean) => !v && (editing = null)">
    <form class="form" @submit.prevent="saveEdit">
      <label class="field">
        <span>{{ t('licenses.customer') }}</span>
        <InputText v-model="edit.customer" maxlength="200" />
      </label>
      <label class="field">
        <span>{{ t('licenses.tier_col') }}</span>
        <Select v-model="edit.tier" :options="tierOptions" option-label="label" option-value="value" />
        <small v-if="editing && edit.tier !== editing.tier" class="app-muted">{{ t('licenses.edit.tier_help') }}</small>
      </label>
      <label class="field">
        <span>{{ t('licenses.end') }}</span>
        <Select v-model="edit.mode" :options="editModes" option-label="label" option-value="value" />
      </label>
      <label v-if="edit.mode === 'date'" class="field">
        <span>{{ t('licenses.end_date') }}</span>
        <InputText v-model="edit.date" class="app-ltr" placeholder="2027-12-31" :invalid="editProblem === 'date' && edit.date !== ''" />
        <small class="app-muted">{{ t('licenses.end_date_help') }}</small>
      </label>
      <div class="extend">
        <span class="app-muted">{{ t('licenses.edit.extend') }}</span>
        <Select v-model="extendMonths" :options="monthOptions" option-label="label" option-value="value" />
        <Button outlined type="button" icon="pi pi-plus" :label="t('licenses.edit.extend_go')" :loading="working" @click="extend" />
      </div>
    </form>
    <template #footer>
      <Button text :label="t('app.cancel')" @click="editing = null" />
      <Button :label="t('licenses.save')" :loading="working" :disabled="editProblem !== ''" @click="saveEdit" />
    </template>
  </Dialog>

  <!-- Revoke -->
  <Dialog :visible="revoking !== null" modal :header="t('licenses.revoke')" :style="{ width: '26rem' }" @update:visible="(v: boolean) => !v && (revoking = null)">
    <p class="app-muted">{{ t('licenses.revoke_help') }}</p>
    <Textarea v-model="reason" rows="3" class="reason" :placeholder="t('licenses.reason')" :aria-label="t('licenses.reason')" maxlength="500" />
    <template #footer>
      <Button text :label="t('app.cancel')" @click="revoking = null" />
      <Button severity="danger" :label="t('licenses.revoke')" :loading="working" :disabled="reason.trim() === ''" @click="revoke" />
    </template>
  </Dialog>
</template>

<style scoped>
.intro {
  margin-block: 0 0.75rem;
  font-size: 0.9rem;
}
.grow {
  flex: 1 1 14rem;
  max-inline-size: 22rem;
}
.search,
.reason,
.keybox {
  inline-size: 100%;
}
.small {
  font-size: 0.8rem;
}
.name {
  font-weight: 600;
}
.row-actions {
  display: flex;
  gap: 0.25rem;
}
.form {
  display: grid;
  gap: 0.75rem;
}
.field {
  display: grid;
  gap: 0.25rem;
}
.extend {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  border-block-start: 1px solid var(--p-content-border-color);
  padding-block-start: 0.75rem;
}
h3 {
  margin: 1rem 0 0.4rem;
  font-size: 0.95rem;
}
.grants {
  max-block-size: 12rem;
  overflow: auto;
  margin: 0;
  font-size: 0.8rem;
}
</style>
