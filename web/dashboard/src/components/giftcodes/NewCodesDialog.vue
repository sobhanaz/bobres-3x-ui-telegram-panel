<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import SelectButton from 'primevue/selectbutton'
import Message from 'primevue/message'
import { api } from '../../api'
import { errorText, fieldError } from '../../errors'
import { asciiNumber, currencies, money, num, type Lang } from '../../format'
import { blankForm, formProblems, howMany, minorUnits, mostPaid, requestBody, type GiftForm, type Terms } from '../../giftcodes'
import type { GiftCodeItem } from '../../types'

// Make gift codes: one of staff's own words, or a batch of generated ones with
// the same terms. The answer holds the codes, which core keeps no copy of: they
// go to the dialog that shows them once.
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ created: [res: { codes: string[]; items: GiftCodeItem[]; terms: Terms }] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const form = ref<GiftForm>(blankForm())
const busy = ref(false)
const error = ref('')
const detail = ref('')
const serverField = ref<Record<string, string>>({})
watch(visible, (v) => {
  if (!v) return
  form.value = blankForm()
  busy.value = false
  error.value = ''
  detail.value = ''
  serverField.value = {}
})

const modes = computed(() => [
  { label: t('giftcodes.mode_batch'), value: 'batch' },
  { label: t('giftcodes.mode_own'), value: 'own' },
])
const currencyOptions = computed(() => currencies.map((c) => ({ value: c, label: t('currency.' + c) })))

// A problem shows for a field with something typed in it; an empty required
// field only keeps the button off.
const problems = computed(() => formProblems(form.value))
const filled = (field: keyof GiftForm) => String(form.value[field]).trim() !== ''
function problemText(field: keyof GiftForm): string {
  const sv = serverField.value[field]
  if (sv) return sv
  const p = problems.value[field]
  return p && p !== 'required' && filled(field) ? t('giftcodes.invalid.' + p) : ''
}
const ready = computed(() => Object.keys(problems.value).length === 0)

const each = computed(() => {
  const n = minorUnits(form.value)
  return n === null ? null : { amount: n, currency: form.value.currency }
})
const total = computed(() => mostPaid(form.value))
const endsIn = computed(() => {
  const d = Number(asciiNumber(form.value.days))
  return form.value.days.trim() !== '' && Number.isInteger(d) && d > 0 ? t('giftcodes.ends_in', { n: num(d, lang.value) }, d) : t('giftcodes.never')
})

const serverFields: Record<string, keyof GiftForm> = { max_redemptions: 'maxRedemptions', per_customer_limit: 'perCustomer', expires_at: 'days' }

function edited(field: keyof GiftForm) {
  if (serverField.value[field]) {
    const rest = { ...serverField.value }
    delete rest[field]
    serverField.value = rest
  }
  error.value = ''
  detail.value = ''
}

async function create() {
  if (!ready.value || busy.value) return
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    const res = await api<{ codes: string[]; items: GiftCodeItem[] }>('POST', '/gift-codes', requestBody(form.value))
    const first = res.items[0]
    const terms: Terms = {
      amount: first.amount,
      max_redemptions: first.max_redemptions,
      per_customer_limit: first.per_customer_limit,
      expires_at: first.expires_at,
      note: first.note,
    }
    visible.value = false
    emit('created', { codes: res.codes, items: res.items, terms })
  } catch (e) {
    const err = errorText(e, t)
    error.value = err.text
    detail.value = err.detail
    // Core names its own fields; the form's are spelled another way.
    serverField.value = Object.fromEntries(Object.entries(fieldError(e)).map(([k, v]) => [serverFields[k] ?? k, v]))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog v-model:visible="visible" :header="t('giftcodes.new')" modal :style="{ inlineSize: 'min(36rem, 94vw)' }" :draggable="false" class="new-dialog">
    <form class="app-form" @submit.prevent="create">
      <SelectButton v-model="form.mode" :options="modes" option-label="label" option-value="value" :allow-empty="false" class="mode" :aria-label="t('giftcodes.mode')" />
      <p class="app-muted lead">{{ form.mode === 'batch' ? t('giftcodes.batch_intro') : t('giftcodes.own_intro') }}</p>

      <div class="app-row">
        <label>
          <span>{{ t('giftcodes.amount') }}</span>
          <InputText v-model="form.amount" class="amount" inputmode="decimal" dir="ltr" autocomplete="off" :invalid="!!problemText('amount')" @input="edited('amount')" />
          <small v-if="problemText('amount')" class="problem amount-problem">{{ problemText('amount') }}</small>
        </label>
        <label>
          <span>{{ t('common.currency') }}</span>
          <Select v-model="form.currency" :options="currencyOptions" option-label="label" option-value="value" class="currency" />
        </label>
      </div>
      <div class="app-row">
        <label>
          <span>{{ t('giftcodes.uses') }}</span>
          <InputText v-model="form.maxRedemptions" class="uses" inputmode="numeric" dir="ltr" autocomplete="off" :invalid="!!problemText('maxRedemptions')" @input="edited('maxRedemptions')" />
          <small v-if="problemText('maxRedemptions')" class="problem">{{ problemText('maxRedemptions') }}</small>
        </label>
        <label>
          <span>{{ t('giftcodes.per_customer') }}</span>
          <InputText v-model="form.perCustomer" class="per-customer" inputmode="numeric" dir="ltr" autocomplete="off" :invalid="!!problemText('perCustomer')" @input="edited('perCustomer')" />
          <small v-if="problemText('perCustomer')" class="problem">{{ problemText('perCustomer') }}</small>
        </label>
      </div>

      <div v-if="form.mode === 'batch'" class="app-row">
        <label>
          <span>{{ t('giftcodes.count') }}</span>
          <InputText v-model="form.count" class="count" inputmode="numeric" dir="ltr" autocomplete="off" :invalid="!!problemText('count')" @input="edited('count')" />
          <small v-if="problemText('count')" class="problem">{{ problemText('count') }}</small>
        </label>
        <label>
          <span>{{ t('giftcodes.prefix') }}</span>
          <InputText v-model="form.prefix" class="prefix upper" dir="ltr" maxlength="8" autocomplete="off" :placeholder="t('giftcodes.prefix_placeholder')" :invalid="!!problemText('prefix')" @input="edited('prefix')" />
          <small v-if="problemText('prefix')" class="problem">{{ problemText('prefix') }}</small>
        </label>
      </div>
      <label v-else>
        <span>{{ t('giftcodes.own_code') }}</span>
        <InputText v-model="form.code" class="own-code upper" dir="ltr" maxlength="40" autocomplete="off" spellcheck="false" :invalid="!!problemText('code')" @input="edited('code')" />
        <small v-if="problemText('code')" class="problem own-problem">{{ problemText('code') }}</small>
        <small v-else class="app-muted">{{ t('giftcodes.code_hint') }}</small>
      </label>

      <div class="app-row">
        <label>
          <span>{{ t('giftcodes.days') }}</span>
          <InputText v-model="form.days" class="days" inputmode="numeric" dir="ltr" autocomplete="off" :placeholder="t('giftcodes.never')" :invalid="!!problemText('days')" @input="edited('days')" />
          <small v-if="problemText('days')" class="problem">{{ problemText('days') }}</small>
        </label>
        <label>
          <span>{{ t('giftcodes.note_field') }}</span>
          <InputText v-model="form.note" class="note" maxlength="200" autocomplete="off" :placeholder="t('giftcodes.note_placeholder')" :invalid="!!problemText('note')" @input="edited('note')" />
          <small v-if="problemText('note')" class="problem">{{ problemText('note') }}</small>
        </label>
      </div>

      <dl class="app-facts summary">
        <dt>{{ t('giftcodes.sum_codes') }}</dt>
        <dd class="sum-codes">{{ num(howMany(form), lang) }}</dd>
        <dt>{{ t('giftcodes.sum_value') }}</dt>
        <dd class="sum-value">{{ each ? money(each, lang) : '—' }}</dd>
        <dt>{{ t('giftcodes.sum_ends') }}</dt>
        <dd>{{ endsIn }}</dd>
        <dt>{{ t('giftcodes.sum_most') }}</dt>
        <dd class="sum-most">{{ total ? money(total, lang) : '—' }}</dd>
      </dl>
      <small class="app-muted">{{ t('giftcodes.shown_once') }}</small>

      <Message v-if="error" severity="error" :closable="false" class="error">
        {{ error }}
        <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
      </Message>
      <div class="buttons">
        <Button type="button" :label="t('app.cancel')" severity="secondary" outlined @click="visible = false" />
        <Button type="submit" class="create" :label="t('giftcodes.make')" :loading="busy" :disabled="!ready" />
      </div>
    </form>
  </Dialog>
</template>

<style scoped>
.lead {
  margin: 0;
  font-size: 0.9rem;
}
.problem {
  color: var(--p-red-500);
}
.upper,
.upper :deep(input) {
  text-transform: uppercase;
}
.summary {
  font-size: 0.95rem;
  padding-block-start: 0.25rem;
}
.buttons {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.detail {
  font-size: 0.85rem;
}
</style>
