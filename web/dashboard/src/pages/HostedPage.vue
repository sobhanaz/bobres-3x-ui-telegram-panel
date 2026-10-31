<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Password from 'primevue/password'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import Dialog from 'primevue/dialog'
import Message from 'primevue/message'
import Textarea from 'primevue/textarea'
import Checkbox from 'primevue/checkbox'
import { api } from '../api'
import { errorText } from '../errors'
import { ago, date, num, type Lang } from '../format'
import { monthChoices, requestId } from '../vendor'
import { domainOk, looksLikeRegistration, newest, older, stateOf, stateSeverity, tokenOk, type HostedStore } from '../hosted'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()
const fail = (e: unknown) => toast.add({ severity: 'error', summary: errorText(e, t).text, life: 6000 })

const stores = ref<HostedStore[]>([])
const loading = ref(false)
async function load() {
  loading.value = true
  try {
    stores.value = (await api<{ items: HostedStore[] }>('GET', '/hosted')).items
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(() => void load())
const latest = computed(() => newest(stores.value))
const nowMs = ref(Date.now())

// Register a store: paste the code tools/hosted printed, or type the fields.
const adding = ref(false)
const add = ref({ registration: '', customer: '', domain: '', channel: 'stable', license_id: '', metrics_token: '', notes: '' })
const usesCode = computed(() => looksLikeRegistration(add.value.registration))
const addOk = computed(() => usesCode.value || (add.value.customer.trim() !== '' && domainOk(add.value.domain) && (add.value.metrics_token === '' || tokenOk(add.value.metrics_token))))
const working = ref(false)
function openAdd() {
  add.value = { registration: '', customer: '', domain: '', channel: 'stable', license_id: '', metrics_token: '', notes: '' }
  adding.value = true
}
async function saveAdd() {
  working.value = true
  try {
    const body = usesCode.value ? { registration: add.value.registration, notes: add.value.notes } : { ...add.value, registration: '' }
    const h = await api<HostedStore>('POST', '/hosted', body)
    adding.value = false
    toast.add({ severity: 'success', summary: t('hosted.added'), life: 3000 })
    await load()
    if (h.has_token) await check(h)
  } catch (e) {
    fail(e)
  } finally {
    working.value = false
  }
}

// Read a store now.
const checking = ref('')
async function check(h: HostedStore) {
  checking.value = h.id
  try {
    const got = await api<HostedStore>('POST', `/hosted/${h.id}/check`)
    stores.value = stores.value.map((s) => (s.id === got.id ? got : s))
    nowMs.value = Date.now()
  } catch (e) {
    fail(e)
  } finally {
    checking.value = ''
  }
}

// Edit a store: the register's fields, a new metrics token (never shown back), or none.
const editing = ref<HostedStore | null>(null)
const edit = ref({ customer: '', version: '', license_id: '', channel: 'stable', notes: '', metrics_token: '', clear_token: false })
function openEdit(h: HostedStore) {
  editing.value = h
  edit.value = { customer: h.customer, version: h.version, license_id: h.license_id, channel: h.channel, notes: h.notes, metrics_token: '', clear_token: false }
}
const editOk = computed(() => edit.value.customer.trim() !== '' && (edit.value.metrics_token === '' || tokenOk(edit.value.metrics_token)))
async function saveEdit() {
  if (!editing.value) return
  working.value = true
  try {
    await api('PUT', `/hosted/${editing.value.id}`, edit.value)
    editing.value = null
    toast.add({ severity: 'success', summary: t('hosted.saved'), life: 3000 })
    await load()
  } catch (e) {
    fail(e)
  } finally {
    working.value = false
  }
}

function remove(h: HostedStore) {
  confirm.require({
    header: t('hosted.delete'),
    message: t('hosted.delete_confirm', { domain: h.domain }),
    acceptProps: { label: t('hosted.delete'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), text: true },
    accept: async () => {
      try {
        await api('DELETE', `/hosted/${h.id}`)
        await load()
      } catch (e) {
        fail(e)
      }
    },
  })
}

// The store's "Hosted" license: made once, its key shown once.
const licensing = ref<HostedStore | null>(null)
const months = ref<number>(1)
const licId = ref('')
const madeKey = ref('')
function openLicense(h: HostedStore) {
  licensing.value = h
  months.value = 1
  licId.value = requestId()
  madeKey.value = ''
}
async function makeLicense() {
  if (!licensing.value) return
  working.value = true
  try {
    const res = await api<{ key: string; license_id: string }>('POST', `/hosted/${licensing.value.id}/license`, { months: months.value, request_id: licId.value })
    madeKey.value = res.key
    await load()
  } catch (e) {
    fail(e)
  } finally {
    working.value = false
  }
}
const channelOptions = computed(() => (['stable', 'beta'] as const).map((c) => ({ label: t('hosted.channel.' + c), value: c })))
const hours = (s: number) => Math.round(s / 3600)
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.hosted.title') }}</h1>
    <Button icon="pi pi-plus" :label="t('hosted.add')" @click="openAdd" />
  </div>
  <p class="app-muted intro">{{ t('hosted.intro') }}</p>
  <Message severity="info" :closable="false" class="custody">{{ t('hosted.custody') }}</Message>

  <DataTable :value="stores" :loading="loading" data-key="id" size="small" scrollable>
    <template #empty>{{ t('hosted.none') }}</template>
    <Column :header="t('hosted.store')">
      <template #body="{ data: h }">
        <bdi>{{ h.customer }}</bdi>
        <div class="small app-ltr"><a :href="'https://' + h.domain + '/admin/'" target="_blank" rel="noopener noreferrer">{{ h.domain }}</a></div>
      </template>
    </Column>
    <Column :header="t('hosted.state')">
      <template #body="{ data: h }">
        <Tag :severity="stateSeverity(stateOf(h))" :value="t('hosted.states.' + stateOf(h))" />
        <div v-if="h.checked_at" class="app-muted small">{{ ago(h.checked_at, nowMs, lang) }}</div>
        <div v-if="h.last_error" class="err small">{{ h.last_error }}</div>
        <div v-else-if="!h.has_token" class="app-muted small">{{ t('hosted.no_token') }}</div>
      </template>
    </Column>
    <Column :header="t('hosted.version')" body-class="app-nowrap">
      <template #body="{ data: h }">
        <span class="app-ltr">{{ h.last_version || h.version || '—' }}</span>
        <Tag v-if="h.last_version && latest && older(h.last_version, latest)" severity="warn" :value="t('hosted.behind')" class="tag" />
        <div class="app-muted small">{{ t('hosted.channel.' + h.channel) }}</div>
      </template>
    </Column>
    <Column :header="t('hosted.figures')">
      <template #body="{ data: h }">
        <div v-if="h.up" class="small figures">
          <span v-if="h.figures.services_total">{{ t('hosted.services', { up: num(h.figures.services_up ?? 0, lang), total: num(h.figures.services_total, lang) }) }}</span>
          <span v-if="h.figures.orders_paid_24h !== undefined">{{ t('hosted.orders', { n: num(h.figures.orders_paid_24h, lang) }) }}</span>
          <span v-if="(h.figures.alerts_critical ?? 0) + (h.figures.alerts_warning ?? 0) > 0">{{ t('hosted.alerts', { critical: num(h.figures.alerts_critical ?? 0, lang), warning: num(h.figures.alerts_warning ?? 0, lang) }) }}</span>
          <span v-if="h.figures.license_days_left !== undefined">{{ t('hosted.license_days', { n: num(h.figures.license_days_left, lang) }, h.figures.license_days_left) }}</span>
          <span v-if="h.figures.backup_age_seconds !== undefined">{{ t('hosted.backup', { n: num(hours(h.figures.backup_age_seconds), lang) }, hours(h.figures.backup_age_seconds)) }}</span>
        </div>
      </template>
    </Column>
    <Column :header="t('hosted.license')" body-class="app-nowrap">
      <template #body="{ data: h }">
        <span v-if="h.license_id" class="app-mono app-ltr small">{{ h.license_id }}</span>
        <Button v-else text size="small" :label="t('hosted.make_license')" @click="openLicense(h)" />
        <div class="app-muted small">{{ date(h.created_at, lang) }}</div>
      </template>
    </Column>
    <Column :header="t('hosted.actions')">
      <template #body="{ data: h }">
        <div class="row-actions">
          <Button icon="pi pi-refresh" text rounded :loading="checking === h.id" :disabled="!h.has_token" :aria-label="t('hosted.check')" @click="check(h)" />
          <Button icon="pi pi-pencil" text rounded :aria-label="t('hosted.edit')" @click="openEdit(h)" />
          <Button icon="pi pi-trash" text rounded severity="danger" :aria-label="t('hosted.delete')" @click="remove(h)" />
        </div>
      </template>
    </Column>
  </DataTable>

  <Dialog v-model:visible="adding" modal :header="t('hosted.add')" :style="{ width: '34rem' }">
    <div class="form">
      <label class="field">
        <span>{{ t('hosted.registration') }}</span>
        <Textarea v-model="add.registration" rows="3" class="app-ltr app-mono" :placeholder="'bobres-hosted:v1:…'" autocomplete="off" />
        <small class="app-muted">{{ t('hosted.registration_help') }}</small>
      </label>
      <template v-if="!usesCode">
        <label class="field">
          <span>{{ t('hosted.customer') }}</span>
          <InputText v-model="add.customer" maxlength="200" />
        </label>
        <label class="field">
          <span>{{ t('hosted.domain') }}</span>
          <InputText v-model="add.domain" class="app-ltr" :invalid="add.domain !== '' && !domainOk(add.domain)" placeholder="shop.example.com" />
        </label>
        <label class="field">
          <span>{{ t('hosted.channel_label') }}</span>
          <Select v-model="add.channel" :options="channelOptions" option-label="label" option-value="value" />
        </label>
        <label class="field">
          <span>{{ t('hosted.license_id') }}</span>
          <InputText v-model="add.license_id" class="app-ltr app-mono" placeholder="lic_…" />
        </label>
        <label class="field">
          <span>{{ t('hosted.token') }}</span>
          <Password v-model="add.metrics_token" :feedback="false" toggle-mask class="app-ltr" autocomplete="off" :invalid="add.metrics_token !== '' && !tokenOk(add.metrics_token)" />
          <small class="app-muted">{{ t('hosted.token_help') }}</small>
        </label>
      </template>
      <label class="field">
        <span>{{ t('hosted.notes') }}</span>
        <Textarea v-model="add.notes" rows="2" maxlength="2000" />
      </label>
    </div>
    <template #footer>
      <Button text :label="t('app.cancel')" @click="adding = false" />
      <Button :label="t('hosted.add')" :loading="working" :disabled="!addOk" @click="saveAdd" />
    </template>
  </Dialog>

  <Dialog :visible="editing !== null" modal :header="editing?.domain ?? ''" :style="{ width: '32rem' }" @update:visible="(v: boolean) => !v && (editing = null)">
    <div class="form">
      <label class="field">
        <span>{{ t('hosted.customer') }}</span>
        <InputText v-model="edit.customer" maxlength="200" />
      </label>
      <label class="field">
        <span>{{ t('hosted.version_installed') }}</span>
        <InputText v-model="edit.version" class="app-ltr" />
      </label>
      <label class="field">
        <span>{{ t('hosted.license_id') }}</span>
        <InputText v-model="edit.license_id" class="app-ltr app-mono" placeholder="lic_…" />
      </label>
      <label class="field">
        <span>{{ t('hosted.channel_label') }}</span>
        <Select v-model="edit.channel" :options="channelOptions" option-label="label" option-value="value" />
      </label>
      <label class="field">
        <span>{{ t('hosted.new_token') }}</span>
        <Password v-model="edit.metrics_token" :feedback="false" toggle-mask class="app-ltr" autocomplete="off" :disabled="edit.clear_token" :invalid="edit.metrics_token !== '' && !tokenOk(edit.metrics_token)" />
        <small class="app-muted">{{ editing?.has_token ? t('hosted.token_kept') : t('hosted.token_help') }}</small>
      </label>
      <label v-if="editing?.has_token" class="check">
        <Checkbox v-model="edit.clear_token" binary input-id="clear-token" />
        <span>{{ t('hosted.clear_token') }}</span>
      </label>
      <label class="field">
        <span>{{ t('hosted.notes') }}</span>
        <Textarea v-model="edit.notes" rows="3" maxlength="2000" />
      </label>
    </div>
    <template #footer>
      <Button text :label="t('app.cancel')" @click="editing = null" />
      <Button :label="t('hosted.save')" :loading="working" :disabled="!editOk" @click="saveEdit" />
    </template>
  </Dialog>

  <Dialog :visible="licensing !== null" modal :header="t('hosted.make_license')" :style="{ width: '30rem' }" @update:visible="(v: boolean) => !v && (licensing = null)">
    <template v-if="madeKey">
      <p>{{ t('hosted.key_once') }}</p>
      <p class="key app-mono app-ltr">{{ madeKey }}</p>
      <p class="app-muted small">{{ t('hosted.key_next') }}</p>
    </template>
    <template v-else>
      <p class="app-muted">{{ t('hosted.license_help') }}</p>
      <Select v-model="months" :options="[...monthChoices]" :option-label="(m: number) => t('licenses.months', { n: m }, m)" class="months" :aria-label="t('hosted.make_license')" />
    </template>
    <template #footer>
      <Button v-if="madeKey" :label="t('hosted.done')" @click="licensing = null" />
      <template v-else>
        <Button text :label="t('app.cancel')" @click="licensing = null" />
        <Button :label="t('hosted.make_license')" :loading="working" @click="makeLicense" />
      </template>
    </template>
  </Dialog>
</template>

<style scoped>
.intro {
  margin-block: 0 0.75rem;
  font-size: 0.9rem;
}
.custody {
  margin-block-end: 1rem;
}
.form {
  display: grid;
  gap: 0.75rem;
}
.field {
  display: grid;
  gap: 0.25rem;
}
.check {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.row-actions {
  display: flex;
  gap: 0.25rem;
}
.small {
  font-size: 0.8rem;
}
.figures {
  display: grid;
  gap: 0.1rem;
}
.tag {
  margin-inline-start: 0.25rem;
}
.err {
  color: var(--p-red-500);
}
.key {
  font-size: 1rem;
  padding: 0.5rem;
  border: 1px dashed var(--p-content-border-color);
  border-radius: 0.5rem;
  word-break: break-all;
}
.months {
  inline-size: 100%;
}
</style>
