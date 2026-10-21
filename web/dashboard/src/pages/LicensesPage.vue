<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Password from 'primevue/password'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import Dialog from 'primevue/dialog'
import Textarea from 'primevue/textarea'
import Message from 'primevue/message'
import { api } from '../api'
import { errorText } from '../errors'
import { useList } from '../composables/useList'
import { date, money, type Lang } from '../format'
import { addressOk, grantMonths, keyOk, licenseStatuses, requestId, statusSeverity, type SoldLicense, type VendorStatus } from '../vendor'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()

// Where the license server is and whether the bot sells now.
const status = ref<VendorStatus>({ available: true, on: false, configured: false, admin_url: '', tiers: [] })
const loaded = ref(false)
async function loadStatus() {
  try {
    status.value = await api<VendorStatus>('GET', '/vendor')
    loaded.value = true
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 5000 })
  }
}

const url = ref('')
const key = ref('')
const saving = ref(false)
const formOk = computed(() => addressOk(url.value) && keyOk(key.value))
async function saveConfig() {
  saving.value = true
  try {
    status.value = await api<VendorStatus>('PUT', '/vendor/config', { admin_url: url.value.trim(), admin_key: key.value.trim() })
    key.value = '' // write-only: it is never shown again
    toast.add({ severity: 'success', summary: t('licenses.saved'), life: 3000 })
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 5000 })
  } finally {
    saving.value = false
  }
}

const switching = ref(false)
async function setSales(on: boolean) {
  switching.value = true
  try {
    await api('PUT', '/settings', { values: { 'vendor.enabled': on ? 'true' : 'false' } })
    toast.add({ severity: 'success', summary: t(on ? 'licenses.turned_on' : 'licenses.turned_off'), life: 3000 })
    await loadStatus()
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 5000 })
  } finally {
    switching.value = false
  }
}

// The licenses sold.
const list = useList<SoldLicense, { status: string }>('/vendor/licenses', { status: '' })
onMounted(() => {
  void loadStatus()
  void list.load()
})
const statusOptions = computed(() => [
  { label: t('licenses.all'), value: '' },
  ...licenseStatuses.map((s) => ({ label: t('licenses.status.' + s), value: s })),
])

// Months for nothing, and revoking.
const granting = ref<SoldLicense | null>(null)
const months = ref<number>(1)
const grantId = ref('')
const working = ref(false)
function openGrant(l: SoldLicense) {
  granting.value = l
  months.value = 1
  grantId.value = requestId()
}
async function grant() {
  if (!granting.value) return
  working.value = true
  try {
    await api('POST', `/vendor/licenses/${granting.value.id}/grant`, { months: months.value, request_id: grantId.value })
    toast.add({ severity: 'success', summary: t('licenses.granted'), life: 3000 })
    granting.value = null
    await list.load()
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 5000 })
  } finally {
    working.value = false
  }
}

const revoking = ref<SoldLicense | null>(null)
const reason = ref('')
async function revoke() {
  if (!revoking.value || reason.value.trim() === '') return
  working.value = true
  try {
    await api('POST', `/vendor/licenses/${revoking.value.id}/revoke`, { reason: reason.value.trim() })
    toast.add({ severity: 'success', summary: t('licenses.revoked'), life: 3000 })
    revoking.value = null
    reason.value = ''
    await list.load()
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 5000 })
  } finally {
    working.value = false
  }
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.licenses.title') }}</h1>
  </div>
  <p class="app-muted intro">{{ t('licenses.intro') }}</p>

  <Message v-if="loaded && !status.available" severity="warn" :closable="false" class="no-key">{{ t('licenses.no_key') }}</Message>

  <section v-if="loaded" class="card" aria-labelledby="lic-server">
    <h2 id="lic-server" class="card-title">{{ t('licenses.server') }}</h2>
    <p v-if="status.configured" class="app-muted">
      {{ t('licenses.configured') }} <bdi class="app-ltr app-mono">{{ status.admin_url }}</bdi>
    </p>
    <p v-else class="app-muted">{{ t('licenses.not_configured') }}</p>
    <form class="form" autocomplete="off" @submit.prevent="saveConfig">
      <label class="field">
        <span>{{ t('licenses.url') }}</span>
        <InputText v-model="url" class="app-ltr" type="url" placeholder="https://license.example.com" :invalid="url !== '' && !addressOk(url)" />
      </label>
      <label class="field">
        <span>{{ t('licenses.key') }}</span>
        <Password v-model="key" class="app-ltr" input-class="key-input" :feedback="false" toggle-mask autocomplete="off" :invalid="key !== '' && !keyOk(key)" />
        <small class="app-muted">{{ t('licenses.key_help') }}</small>
      </label>
      <div class="row">
        <Button type="submit" :label="t('licenses.save')" :loading="saving" :disabled="!formOk || !status.available" />
        <Button
          v-if="status.configured && !status.on"
          type="button"
          severity="success"
          :label="t('licenses.turn_on')"
          :loading="switching"
          @click="setSales(true)"
        />
        <Button v-if="status.on" type="button" severity="secondary" outlined :label="t('licenses.turn_off')" :loading="switching" @click="setSales(false)" />
      </div>
    </form>
    <p class="app-muted prices">
      <span v-for="tier in status.tiers" :key="tier.name" class="price">
        {{ t('licenses.tier.' + tier.name) }}: {{ money(tier.monthly, lang) }} / {{ t('licenses.month') }} · {{ money(tier.yearly, lang) }} / {{ t('licenses.year') }}
      </span>
    </p>
    <Tag v-if="status.on" severity="success" :value="t('licenses.on')" />
    <Tag v-else severity="secondary" :value="t('licenses.off')" />
  </section>

  <div class="app-toolbar">
    <Select v-model="list.filters.status" class="status-filter" :options="statusOptions" option-label="label" option-value="value" :aria-label="t('licenses.status_col')" />
    <Button v-if="list.loading.value" text loading :aria-label="t('app.loading')" />
  </div>
  <Message v-if="list.error.value" severity="error" :closable="false">{{ list.error.value }}</Message>

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
    class="sold"
    @page="list.onPage"
  >
    <template #empty>{{ t('licenses.none') }}</template>
    <Column :header="t('licenses.buyer')">
      <template #body="{ data: l }">
        <bdi v-if="l.buyer?.username">@{{ l.buyer.username }}</bdi>
        <span v-else class="app-mono">{{ l.buyer?.telegram_id }}</span>
        <div class="app-muted small app-mono app-ltr">{{ l.license_id }}</div>
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
      <template #body="{ data: l }">
        <span v-if="l.ends_at">{{ date(l.ends_at, lang) }}</span>
        <span v-else class="app-muted">{{ t('licenses.no_end') }}</span>
      </template>
    </Column>
    <Column :header="t('licenses.sold')" body-class="app-nowrap">
      <template #body="{ data: l }">{{ date(l.created_at, lang) }}</template>
    </Column>
    <Column :header="t('licenses.actions')">
      <template #body="{ data: l }">
        <div class="row-actions">
          <Button class="give" icon="pi pi-plus" text rounded :disabled="l.status !== 'active'" :aria-label="t('licenses.give')" @click="openGrant(l)" />
          <Button class="revoke" icon="pi pi-ban" text rounded severity="danger" :disabled="l.status !== 'active'" :aria-label="t('licenses.revoke')" @click="revoking = l" />
        </div>
      </template>
    </Column>
  </DataTable>

  <Dialog :visible="granting !== null" modal :header="t('licenses.give')" :style="{ width: '24rem' }" @update:visible="(v: boolean) => !v && (granting = null)">
    <p class="app-muted">{{ t('licenses.give_help') }}</p>
    <Select v-model="months" :options="[...grantMonths]" :option-label="(m: number) => t('licenses.months', { n: m }, m)" class="months" :aria-label="t('licenses.give')" />
    <template #footer>
      <Button text :label="t('app.cancel')" @click="granting = null" />
      <Button :label="t('licenses.give')" :loading="working" @click="grant" />
    </template>
  </Dialog>

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
.card {
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.75rem;
  padding: 1rem;
  margin-block-end: 1rem;
  display: grid;
  gap: 0.75rem;
}
.card-title {
  margin: 0;
  font-size: 1rem;
}
.form {
  display: grid;
  gap: 0.75rem;
  max-inline-size: 36rem;
}
.field {
  display: grid;
  gap: 0.25rem;
}
.row {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.prices {
  display: grid;
  gap: 0.25rem;
  margin: 0;
}
.small {
  font-size: 0.8rem;
}
.row-actions {
  display: flex;
  gap: 0.25rem;
}
.months,
.reason {
  inline-size: 100%;
}
</style>
