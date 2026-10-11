<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import ReasonDialog from '../ReasonDialog.vue'
import BalanceText from './BalanceText.vue'
import { api, newKey } from '../../api'
import { errorText } from '../../errors'
import { asciiNumber, currencies } from '../../format'
import { afterPayment, paymentAmount, paymentCurrency, type Reseller } from '../../resellers'
import type { LedgerItem, ResellerDetail } from '../../types'

// Record money a reseller paid outside the bot: it is credited to their
// wallet (lowering what they owe) and they are told in the bot. The request
// key is new each time the dialog opens, so a retried submit counts once.
const props = defineProps<{ reseller: Reseller }>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ paid: [res: { item?: ResellerDetail; entry?: LedgerItem }] }>()
const { t } = useI18n()

const amount = ref('')
const currency = ref('IRT')
const key = ref('')
const busy = ref(false)
const error = ref('')
const detail = ref('')

watch(visible, (v) => {
  if (!v) return
  amount.value = ''
  currency.value = paymentCurrency(props.reseller.wallets)
  key.value = newKey()
  error.value = ''
  detail.value = ''
})

const minor = computed(() => paymentAmount(amount.value, currency.value))
const now = computed(() => afterPayment(props.reseller.wallets, currency.value, 0))
const after = computed(() => (minor.value === null ? null : afterPayment(props.reseller.wallets, currency.value, minor.value)))
const currencyOptions = computed(() => currencies.map((c) => ({ value: c, label: t('currency.' + c) })))

async function pay(reason: string) {
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    const res = await api<{ item?: ResellerDetail; entry?: LedgerItem }>('POST', `/resellers/${props.reseller.id}/payments`, {
      amount: asciiNumber(amount.value), currency: currency.value, reason, key: key.value,
    })
    visible.value = false
    emit('paid', res ?? {})
  } catch (e) {
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
    :title="t('reseller.record_payment')"
    :message="t('reseller.payment_intro')"
    :confirm-label="t('reseller.record_payment')"
    :busy="busy"
    :error="error"
    :detail="detail"
    :can-confirm="minor !== null"
    @confirm="pay"
  >
    <div class="app-form">
      <div class="app-row">
        <label>
          <span>{{ t('common.amount') }}</span>
          <InputText v-model="amount" inputmode="decimal" dir="ltr" autocomplete="off" class="amount" />
        </label>
        <label>
          <span>{{ t('common.currency') }}</span>
          <Select v-model="currency" :options="currencyOptions" option-label="label" option-value="value" />
        </label>
      </div>
      <dl class="app-facts sums">
        <dt>{{ t('reseller.balance_now') }}</dt>
        <dd><BalanceText :value="now" /></dd>
        <dt>{{ t('reseller.balance_after') }}</dt>
        <dd class="after">
          <BalanceText v-if="after" :value="after" />
          <span v-else class="app-muted">—</span>
        </dd>
      </dl>
    </div>
  </ReasonDialog>
</template>

<style scoped>
.sums {
  font-size: 0.95rem;
}
</style>
