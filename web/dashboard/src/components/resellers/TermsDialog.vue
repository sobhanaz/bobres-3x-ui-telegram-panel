<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Textarea from 'primevue/textarea'
import ReasonDialog from '../ReasonDialog.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { currencies, isolate, money, type Lang } from '../../format'
import { blankTerms, NOTE_MAX, percent, termsBody, termsFormOf, termsProblems, type Reseller } from '../../resellers'
import type { ResellerDetail, TierItem } from '../../types'

// Change a reseller's terms: tier, credit limit per currency (never below
// what they owe), their own commission, and the staff note. The change names
// the version it was read at: core refuses it when someone changed them since.
const props = defineProps<{ reseller: Reseller }>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ saved: [item: ResellerDetail | undefined]; stale: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const tiers = ref<TierItem[]>([])
const tiersLoading = ref(false)
const form = ref(blankTerms())
const busy = ref(false)
const error = ref('')
const detail = ref('')
// What core said about a field (its words), by the field's name.
const fieldErrors = ref<Record<string, string>>({})
// The version the form was read at. The page may read the reseller again
// while this is open (after a 409): a save still names the form's version.
const version = ref(0)
// Core refused this form as out of date: no save until it is reopened.
const stale = ref(false)

watch(visible, (v) => {
  if (!v) return
  form.value = termsFormOf(props.reseller)
  version.value = props.reseller.version
  stale.value = false
  error.value = ''
  detail.value = ''
  fieldErrors.value = {}
  void loadTiers()
})
async function loadTiers() {
  tiersLoading.value = true
  try {
    tiers.value = (await api<{ items: TierItem[] }>('GET', '/reseller-tiers')).items
  } catch (e) {
    error.value = errorText(e, t).text
  } finally {
    tiersLoading.value = false
  }
}
// A field edited after core refused it may be right now.
watch(
  () => JSON.stringify(form.value),
  () => (fieldErrors.value = {}),
)

const tierOptions = computed(() => {
  const list = tiers.value.map((tr) => ({
    value: tr.id,
    label: t('reseller.tier_option', { name: isolate(tr.name), discount: percent(tr.discount_percent, lang.value), commission: percent(tr.commission_percent, lang.value) }),
  }))
  // Until the list arrives, the current tier still shows by name.
  if (!list.some((o) => o.value === props.reseller.tier.id)) list.unshift({ value: props.reseller.tier.id, label: props.reseller.tier.name })
  return list
})
const tierCommission = computed(() => tiers.value.find((tr) => tr.id === form.value.tier)?.commission_percent ?? null)
const problems = computed(() => termsProblems(form.value, props.reseller.wallets))
const owes = (c: string) => {
  const w = props.reseller.wallets.find((x) => x.currency === c)
  return w && w.balance < 0 ? { amount: -w.balance, currency: c } : null
}
const fieldIssue = (f: string) => problems.value.includes(f) || !!fieldErrors.value[f]

async function save(reason: string) {
  if (stale.value) return
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    const res = await api<{ item?: ResellerDetail }>('PUT', `/resellers/${props.reseller.id}`, {
      version: version.value,
      ...termsBody(form.value, reason, props.reseller.wallets),
    })
    visible.value = false
    emit('saved', res?.item)
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && e.code === 'state') {
      // Changed since this dialog opened: the page reads them again; this
      // form keeps its version, so saving again waits until it is reopened.
      stale.value = true
      error.value = t('reseller.terms_stale')
      detail.value = e.message
      emit('stale')
      return
    }
    if (e instanceof ApiError && e.status === 400 && typeof e.data.field === 'string') {
      fieldErrors.value = { [e.data.field]: e.message }
    }
    const err = errorText(e, t)
    error.value = err.text
    detail.value = err.detail
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <ReasonDialog
    v-model:visible="visible"
    :title="t('reseller.change_terms')"
    :message="t('reseller.terms_intro')"
    :confirm-label="t('common.save')"
    :busy="busy"
    :error="error"
    :detail="detail"
    :can-confirm="!stale && problems.length === 0"
    @confirm="save"
  >
    <div class="app-form">
      <label>
        <span>{{ t('reseller.tier') }}</span>
        <Select v-model="form.tier" :options="tierOptions" option-label="label" option-value="value" :loading="tiersLoading" :invalid="fieldIssue('tier_id')" class="tier" />
        <small v-if="fieldErrors.tier_id" class="app-ltr field-error">{{ fieldErrors.tier_id }}</small>
      </label>
      <div class="app-row">
        <label v-for="c in currencies" :key="c">
          <span>{{ t('reseller.credit_limit_in', { currency: t('currency.' + c) }) }}</span>
          <InputText
            v-model="form.limits[c]"
            inputmode="decimal"
            dir="ltr"
            autocomplete="off"
            placeholder="0"
            :invalid="fieldIssue('credit_limit.' + c)"
            :class="'limit limit-' + c"
          />
          <small v-if="owes(c)" class="app-muted" :class="{ 'field-error': problems.includes('credit_limit.' + c) }">
            {{ t('reseller.limit_at_least', { amount: money(owes(c)!, lang) }) }}
          </small>
          <small v-if="fieldErrors['credit_limit.' + c]" class="app-ltr field-error">{{ fieldErrors['credit_limit.' + c] }}</small>
        </label>
      </div>
      <small class="app-muted hint">{{ t('reseller.limit_hint') }}</small>
      <label>
        <span>{{ t('reseller.commission_override') }}</span>
        <InputText
          v-model="form.commission"
          inputmode="numeric"
          dir="ltr"
          autocomplete="off"
          :placeholder="tierCommission === null ? '' : String(tierCommission)"
          :invalid="fieldIssue('commission_percent')"
          class="commission"
        />
        <small class="app-muted">{{
          tierCommission === null ? t('reseller.commission_override_hint_plain') : t('reseller.commission_override_hint', { n: percent(tierCommission, lang) })
        }}</small>
      </label>
      <label>
        <span>{{ t('reseller.note') }}</span>
        <Textarea v-model="form.note" rows="2" auto-resize :maxlength="NOTE_MAX" fluid :invalid="fieldIssue('note')" class="note" />
        <small class="app-muted">{{ t('reseller.note_hint') }}</small>
      </label>
    </div>
  </ReasonDialog>
</template>

<style scoped>
.field-error {
  color: var(--p-red-500);
}
.hint {
  margin-block-start: -0.5rem;
}
</style>
