<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import Dialog from 'primevue/dialog'
import Message from 'primevue/message'
import ToggleSwitch from 'primevue/toggleswitch'
import { api } from '../api'
import { errorText } from '../errors'
import { useList } from '../composables/useList'
import { date, money, num, type Lang } from '../format'
import { requestId } from '../vendor'
import { openAmount, payoutProblem, percentValue, reversedState, wholeToman, type Affiliate, type AffiliateDetail, type AffiliateSettings } from '../affiliates'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const fail = (e: unknown) => toast.add({ severity: 'error', summary: errorText(e, t).text, life: 5000 })

// The affiliates, with the program's settings in the same answer.
const list = useList<Affiliate, Record<string, string>>('/affiliates', {})
const settings = computed<AffiliateSettings | null>(() => (list.raw.value as unknown as { settings?: AffiliateSettings } | null)?.settings ?? null)
onMounted(() => void list.load())

// The program: on or off, the percent and the minimum payout.
const form = ref({ enabled: false, percent: '20', min: '0' })
watch(settings, (s) => {
  if (s) form.value = { enabled: s.enabled, percent: String(s.percent), min: String(s.min_payout) }
})
const formPercent = computed(() => percentValue(form.value.percent))
const formMin = computed(() => wholeToman(form.value.min))
const formOk = computed(() => typeof formPercent.value === 'number' && formPercent.value >= 1 && formMin.value !== null)
const saving = ref(false)
async function saveSettings() {
  if (!formOk.value) return
  saving.value = true
  try {
    await api('PUT', '/affiliates/settings', { enabled: form.value.enabled, percent: formPercent.value, min_payout: formMin.value })
    toast.add({ severity: 'success', summary: t('affiliates.saved'), life: 3000 })
    await list.load()
  } catch (e) {
    fail(e)
  } finally {
    saving.value = false
  }
}

function who(a: Affiliate | { user: Affiliate['user'] }): string {
  const u = a.user
  if (!u) return ''
  return u.username ? '@' + u.username : String(u.telegram_id)
}

// One affiliate: their commissions and payouts.
const detail = ref<AffiliateDetail | null>(null)
async function openDetail(a: Affiliate) {
  try {
    detail.value = await api<AffiliateDetail>('GET', `/affiliates/${a.id}`)
  } catch (e) {
    fail(e)
  }
}

// A percent of their own, and switching them off.
const editing = ref<Affiliate | null>(null)
const editPercent = ref('')
const editStatus = ref<'active' | 'disabled'>('active')
const working = ref(false)
function openEdit(a: Affiliate) {
  editing.value = a
  editPercent.value = a.percent === null ? '' : String(a.percent)
  editStatus.value = a.status
}
const editValue = computed(() => percentValue(editPercent.value))
async function saveEdit() {
  if (!editing.value || editValue.value === 'bad') return
  working.value = true
  try {
    await api('PUT', `/affiliates/${editing.value.id}`, { percent: editValue.value, status: editStatus.value })
    toast.add({ severity: 'success', summary: t('affiliates.saved'), life: 3000 })
    editing.value = null
    await list.load()
  } catch (e) {
    fail(e)
  } finally {
    working.value = false
  }
}
const statusOptions = computed(() => (['active', 'disabled'] as const).map((s) => ({ label: t('affiliates.status.' + s), value: s })))

// A payout made by hand.
const paying = ref<Affiliate | null>(null)
const payAmount = ref('')
const payNote = ref('')
const payId = ref('')
function openPay(a: Affiliate) {
  paying.value = a
  payAmount.value = String(openAmount(a))
  payNote.value = ''
  payId.value = requestId()
}
const payProblem = computed(() => (paying.value ? payoutProblem(wholeToman(payAmount.value), paying.value, settings.value?.min_payout ?? 0) : 'amount'))
async function pay() {
  if (!paying.value || payProblem.value !== '') return
  working.value = true
  try {
    await api('POST', `/affiliates/${paying.value.id}/payouts`, { amount: wholeToman(payAmount.value), note: payNote.value.trim(), request_id: payId.value })
    toast.add({ severity: 'success', summary: t('affiliates.paid_ok'), life: 3000 })
    paying.value = null
    await list.load()
  } catch (e) {
    fail(e)
  } finally {
    working.value = false
  }
}
const toman = (n: number) => money({ amount: n, currency: 'IRT' }, lang.value)
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.affiliates.title') }}</h1>
  </div>
  <p class="app-muted intro">{{ t('affiliates.intro') }}</p>

  <section v-if="settings" class="card" aria-labelledby="aff-program">
    <h2 id="aff-program" class="card-title">{{ t('affiliates.program') }}</h2>
    <Message v-if="!settings.vendor_on" severity="warn" :closable="false">{{ t('affiliates.needs_sales') }}</Message>
    <form class="form" @submit.prevent="saveSettings">
      <label class="switch">
        <ToggleSwitch v-model="form.enabled" input-id="aff-enabled" />
        <span>{{ t('affiliates.enabled') }}</span>
      </label>
      <label class="field">
        <span>{{ t('affiliates.percent') }}</span>
        <InputText v-model="form.percent" class="short app-ltr" inputmode="numeric" :invalid="formPercent === 'bad' || formPercent === null" />
        <small class="app-muted">{{ t('affiliates.percent_help') }}</small>
      </label>
      <label class="field">
        <span>{{ t('affiliates.min_payout') }}</span>
        <InputText v-model="form.min" class="short app-ltr" inputmode="numeric" :invalid="formMin === null" />
        <small class="app-muted">{{ t('affiliates.min_payout_help') }}</small>
      </label>
      <div class="row">
        <Button type="submit" :label="t('affiliates.save')" :loading="saving" :disabled="!formOk" />
        <Tag v-if="settings.on" severity="success" :value="t('affiliates.on')" />
        <Tag v-else severity="secondary" :value="t('affiliates.off')" />
      </div>
    </form>
    <p class="app-muted small">{{ t('affiliates.rules') }}</p>
  </section>

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
    @page="list.onPage"
  >
    <template #empty>{{ t('affiliates.none') }}</template>
    <Column :header="t('affiliates.who')">
      <template #body="{ data: a }">
        <bdi>{{ who(a) }}</bdi>
        <div class="app-muted small app-mono app-ltr">aff_{{ a.code }}</div>
      </template>
    </Column>
    <Column :header="t('affiliates.percent_col')" body-class="app-nowrap">
      <template #body="{ data: a }">
        <span v-if="a.percent !== null">{{ num(a.percent, lang) }}% <Tag severity="info" :value="t('affiliates.own')" /></span>
        <span v-else class="app-muted">{{ num(settings?.percent ?? 20, lang) }}%</span>
      </template>
    </Column>
    <Column :header="t('affiliates.status_col')">
      <template #body="{ data: a }">
        <Tag :severity="a.status === 'active' ? 'success' : 'secondary'" :value="t('affiliates.status.' + a.status)" />
      </template>
    </Column>
    <Column :header="t('affiliates.referred')">
      <template #body="{ data: a }">{{ num(a.referred, lang) }}</template>
    </Column>
    <Column :header="t('affiliates.licenses')">
      <template #body="{ data: a }">{{ num(a.licenses, lang) }}</template>
    </Column>
    <Column :header="t('affiliates.earned')" body-class="app-nowrap">
      <template #body="{ data: a }">{{ money(a.earned, lang) }}</template>
    </Column>
    <Column :header="t('affiliates.paid')" body-class="app-nowrap">
      <template #body="{ data: a }">{{ money(a.paid, lang) }}</template>
    </Column>
    <Column :header="t('affiliates.balance')" body-class="app-nowrap">
      <template #body="{ data: a }">{{ money(a.balance, lang) }}</template>
    </Column>
    <Column :header="t('affiliates.actions')">
      <template #body="{ data: a }">
        <div class="row-actions">
          <Button icon="pi pi-list" text rounded :aria-label="t('affiliates.details')" @click="openDetail(a)" />
          <Button icon="pi pi-pencil" text rounded :aria-label="t('affiliates.edit')" @click="openEdit(a)" />
          <Button icon="pi pi-wallet" text rounded :disabled="openAmount(a) <= 0" :aria-label="t('affiliates.pay')" @click="openPay(a)" />
        </div>
      </template>
    </Column>
  </DataTable>

  <Dialog :visible="detail !== null" modal :header="detail ? who(detail.affiliate) : ''" :style="{ width: '52rem' }" @update:visible="(v: boolean) => !v && (detail = null)">
    <template v-if="detail">
      <p class="app-muted">{{ t('affiliates.detail_line', { percent: num(detail.effective_percent, lang), earned: money(detail.affiliate.earned, lang), paid: money(detail.affiliate.paid, lang) }) }}</p>
      <h3 class="sub">{{ t('affiliates.commissions') }}</h3>
      <DataTable :value="detail.commissions" size="small" data-key="order_id" scrollable>
        <template #empty>{{ t('affiliates.no_commissions') }}</template>
        <Column :header="t('affiliates.buyer')">
          <template #body="{ data: c }"><bdi>{{ who({ user: c.buyer }) }}</bdi><div class="app-muted small app-mono app-ltr">{{ c.license_id }}</div></template>
        </Column>
        <Column :header="t('affiliates.tier')">
          <template #body="{ data: c }">{{ t('licenses.tier.' + c.tier) }}</template>
        </Column>
        <Column :header="t('affiliates.base')" body-class="app-nowrap">
          <template #body="{ data: c }">{{ money(c.base, lang) }}</template>
        </Column>
        <Column :header="t('affiliates.amount')" body-class="app-nowrap">
          <template #body="{ data: c }">
            {{ money(c.amount, lang) }} <span class="app-muted small">({{ num(c.percent, lang) }}%)</span>
            <div v-if="reversedState(c)" class="small">
              <Tag severity="danger" :value="t('affiliates.reversed_' + reversedState(c), { amount: money(c.reversed, lang) })" />
            </div>
          </template>
        </Column>
        <Column :header="t('affiliates.date')" body-class="app-nowrap">
          <template #body="{ data: c }">{{ date(c.created_at, lang) }}</template>
        </Column>
      </DataTable>
      <h3 class="sub">{{ t('affiliates.payouts') }}</h3>
      <DataTable :value="detail.payouts" size="small" data-key="id" scrollable>
        <template #empty>{{ t('affiliates.no_payouts') }}</template>
        <Column :header="t('affiliates.amount')" body-class="app-nowrap">
          <template #body="{ data: p }">{{ money(p.amount, lang) }}</template>
        </Column>
        <Column :header="t('affiliates.note')">
          <template #body="{ data: p }"><bdi>{{ p.note }}</bdi></template>
        </Column>
        <Column :header="t('affiliates.date')" body-class="app-nowrap">
          <template #body="{ data: p }">{{ date(p.created_at, lang) }}</template>
        </Column>
      </DataTable>
    </template>
  </Dialog>

  <Dialog :visible="editing !== null" modal :header="t('affiliates.edit')" :style="{ width: '26rem' }" @update:visible="(v: boolean) => !v && (editing = null)">
    <div class="form">
      <label class="field">
        <span>{{ t('affiliates.own_percent') }}</span>
        <InputText v-model="editPercent" class="short app-ltr" inputmode="numeric" :invalid="editValue === 'bad'" :placeholder="String(settings?.percent ?? 20)" />
        <small class="app-muted">{{ t('affiliates.own_percent_help') }}</small>
      </label>
      <label class="field">
        <span>{{ t('affiliates.status_col') }}</span>
        <Select v-model="editStatus" :options="statusOptions" option-label="label" option-value="value" />
      </label>
    </div>
    <template #footer>
      <Button text :label="t('app.cancel')" @click="editing = null" />
      <Button :label="t('affiliates.save')" :loading="working" :disabled="editValue === 'bad'" @click="saveEdit" />
    </template>
  </Dialog>

  <Dialog :visible="paying !== null" modal :header="t('affiliates.pay')" :style="{ width: '28rem' }" @update:visible="(v: boolean) => !v && (paying = null)">
    <template v-if="paying">
      <p class="app-muted">{{ t('affiliates.pay_help', { open: toman(openAmount(paying)), balance: money(paying.balance, lang) }) }}</p>
      <div class="form">
        <label class="field">
          <span>{{ t('affiliates.amount_toman') }}</span>
          <InputText v-model="payAmount" class="app-ltr" inputmode="numeric" :invalid="payProblem !== ''" />
          <small v-if="payProblem" class="err">{{ t('affiliates.problem.' + payProblem, { min: toman(settings?.min_payout ?? 0) }) }}</small>
        </label>
        <label class="field">
          <span>{{ t('affiliates.note') }}</span>
          <InputText v-model="payNote" maxlength="500" :placeholder="t('affiliates.note_placeholder')" />
        </label>
      </div>
    </template>
    <template #footer>
      <Button text :label="t('app.cancel')" @click="paying = null" />
      <Button :label="t('affiliates.pay')" :loading="working" :disabled="payProblem !== ''" @click="pay" />
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
.switch {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.short {
  max-inline-size: 10rem;
}
.row {
  display: flex;
  flex-wrap: wrap;
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
.sub {
  font-size: 0.95rem;
  margin-block: 1rem 0.5rem;
}
.err {
  color: var(--p-red-500);
}
</style>
