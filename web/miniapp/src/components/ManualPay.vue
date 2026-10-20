<script setup lang="ts">
// A manual payment: the customer pays outside the store, to a card, to a USDT address or at the store's Zarinpal link,
// and then sends the proof (a receipt image with the bank's tracking number, or a transaction hash for crypto), which
// the staff review. This shows what the bot shows (the card, the addresses, the link, the exact amount, the payment
// reference), takes the proof, and then says it is waiting for review.
import { computed, ref } from 'vue'
import { api, ApiError, type Intent } from '../api'
import { errorKey } from '../errors'
import { tr, type Key } from '../i18n'
import { fmtMoney } from '../fmt'
import { cardDigits, cleanTxid, networkFor, networksOf, plainAmount, txidPattern, type Network } from '../pay'
import { type PreparedReceipt } from '../receipt'
import { fail, state } from '../store'
import { haptic, openLink, openTelegram } from '../telegram'
import CopyField from './CopyField.vue'
import ReceiptPicker from './ReceiptPicker.vue'

const props = withDefaults(defineProps<{ intent: Intent; checking: boolean; message: string; showLeave?: boolean }>(), { showLeave: true })
const emit = defineEmits<{ check: [quiet?: boolean]; leave: [] }>()

const provider = computed(() => props.intent.provider)
const d = computed(() => props.intent.details)
const amount = computed(() => fmtMoney(props.intent.amount, props.intent.currency, props.intent.scale))
const usdt = computed(() => (props.intent.usdt_amount ? fmtMoney(props.intent.usdt_amount, 'USDT', 6) : ''))
const networks = computed(() => networksOf(d.value))
// Only a web address is opened (core checks it too).
const payLink = computed(() => (/^https?:\/\//i.test(d.value.zarinpal_link ?? '') ? (d.value.zarinpal_link ?? '') : ''))

// The proof: a receipt image (all three methods), or a hash (crypto).
const mode = ref<'receipt' | 'txid'>('receipt')
const receipt = ref<PreparedReceipt | null>(null)
const reference = ref('')
const txid = ref('')
const chosen = ref<Network | ''>('')
const busy = ref(false)
const error = ref('')
const chatClosed = ref(false)
const sent = ref(false)

// The bank's tracking number is how a card or a payment-link transfer is found on the statement.
const needsReference = computed(() => provider.value !== 'manual_crypto')
const hash = computed(() => cleanTxid(txid.value))
const network = computed<Network | ''>(() => chosen.value || networkFor(hash.value, networks.value))
const canSend = computed(() => {
  if (mode.value === 'txid') return txidPattern.test(hash.value) && network.value !== ''
  return receipt.value !== null && (!needsReference.value || reference.value.trim() !== '')
})
const waiting = computed(() => sent.value || props.intent.status === 'confirming')
const closed = computed(() => props.intent.status === 'failed' || props.intent.status === 'expired')
const bot = computed(() => state.me?.brand.bot_url ?? '')

async function send() {
  error.value = ''
  chatClosed.value = false
  busy.value = true
  try {
    if (mode.value === 'txid') await api.sendTxid(props.intent.intent_id, hash.value, network.value)
    else if (receipt.value) await api.sendReceipt(props.intent.intent_id, receipt.value.blob, receipt.value.name, reference.value.trim())
    sent.value = true
    haptic('success')
  } catch (e) {
    if (fail(e)) return
    if (e instanceof ApiError && e.code === 'already_submitted') {
      sent.value = true // an earlier send arrived after all: it waits for review
      return
    }
    chatClosed.value = e instanceof ApiError && e.code === 'chat_closed'
    error.value = tr(errorKey(e))
    haptic('error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section v-if="intent.status === 'succeeded'" class="card" role="status">
    <h2>{{ tr('done.title') }}</h2>
    <p class="ok">{{ tr('manual.approved') }}</p>
  </section>

  <section v-else-if="closed" class="card" role="alert">
    <h2>{{ tr(('method.' + provider) as Key) }}</h2>
    <p class="error">{{ tr('manual.closed') }}</p>
  </section>

  <section v-else-if="waiting" class="card" role="status">
    <h2>{{ tr('manual.sent_title') }}</h2>
    <p>{{ tr('manual.sent_text') }}</p>
    <p v-if="message" class="muted">{{ message }}</p>
    <button class="btn secondary" type="button" :disabled="checking" @click="emit('check')">
      {{ checking ? tr('pay.checking') : tr('manual.check') }}
    </button>
    <button v-if="showLeave" class="btn" type="button" @click="emit('leave')">{{ tr('done.view') }}</button>
  </section>

  <section v-else class="card pay">
    <h2>{{ tr(('method.' + provider) as Key) }}</h2>

    <template v-if="provider === 'manual_card'">
      <p>{{ tr('manual.card_send') }}</p>
      <CopyField :label="tr('manual.amount')" :value="amount" :copy="String(intent.amount)" :code="false" />
      <CopyField v-if="d.card_number" :label="tr('manual.card_number')" :value="d.card_number" :copy="cardDigits(d.card_number)" />
      <p v-if="d.card_holder" class="muted">{{ tr('manual.card_holder', { name: d.card_holder }) }}</p>
    </template>

    <template v-else-if="provider === 'manual_crypto'">
      <p v-if="usdt && intent.currency === 'IRT'">{{ tr('manual.crypto_send_equiv', { amount: usdt, price: amount }) }}</p>
      <p v-else>{{ tr('manual.crypto_send', { amount: usdt || amount }) }}</p>
      <CopyField v-if="usdt" :label="tr('manual.amount')" :value="usdt" :copy="plainAmount(intent.usdt_amount ?? 0, 6)" :code="false" />
      <CopyField v-if="d.usdt_trc20" :label="tr('manual.network', { network: 'TRC20' })" :value="d.usdt_trc20" />
      <CopyField v-if="d.usdt_erc20" :label="tr('manual.network', { network: 'ERC20' })" :value="d.usdt_erc20" />
    </template>

    <template v-else>
      <p>{{ tr('manual.zarinpal_send', { amount }) }}</p>
      <CopyField :label="tr('manual.amount')" :value="amount" :copy="String(intent.amount)" :code="false" />
      <button v-if="payLink" class="btn" type="button" @click="openLink(payLink)">{{ tr('manual.zarinpal_open') }}</button>
    </template>

    <CopyField v-if="d.reference" :label="tr('manual.reference')" :value="d.reference" />
    <p class="muted small">{{ tr('manual.reference_hint') }}</p>

    <h3>{{ tr('manual.step2') }}</h3>
    <div v-if="provider === 'manual_crypto'" class="row" role="group" :aria-label="tr('manual.step2')">
      <button class="btn" :class="{ secondary: mode !== 'receipt' }" type="button" :aria-pressed="mode === 'receipt'" @click="mode = 'receipt'">
        {{ tr('manual.mode_receipt') }}
      </button>
      <button class="btn" :class="{ secondary: mode !== 'txid' }" type="button" :aria-pressed="mode === 'txid'" @click="mode = 'txid'">
        {{ tr('manual.mode_txid') }}
      </button>
    </div>

    <template v-if="mode === 'receipt'">
      <ReceiptPicker v-model="receipt" />
      <label class="field">
        <span>{{ needsReference ? tr('manual.bank_ref') : tr('manual.bank_ref') + ' ' + tr('manual.optional') }}</span>
        <input v-model="reference" type="text" dir="ltr" autocomplete="off" maxlength="128" />
        <span class="muted small">{{ tr('manual.bank_ref_hint') }}</span>
      </label>
    </template>
    <template v-else>
      <label class="field">
        <span>{{ tr('manual.txid') }}</span>
        <input v-model="txid" type="text" dir="ltr" autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="100" />
        <span class="muted small">{{ tr('manual.txid_hint') }}</span>
      </label>
      <label v-if="networks.length > 1" class="field">
        <span>{{ tr('manual.network_pick') }}</span>
        <select :value="network" dir="ltr" @change="chosen = ($event.target as HTMLSelectElement).value as Network">
          <option v-for="n in networks" :key="n" :value="n">{{ n }}</option>
        </select>
      </label>
    </template>

    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <button v-if="chatClosed && bot" class="btn secondary" type="button" @click="openTelegram(bot)">{{ tr('common.open_bot') }}</button>
    <button class="btn" type="button" :disabled="busy || !canSend" @click="send">
      {{ busy ? tr('manual.sending') : mode === 'txid' ? tr('manual.submit_txid') : tr('manual.submit') }}
    </button>
    <p v-if="message" class="muted" role="status">{{ message }}</p>
  </section>
</template>

<style scoped>
select { padding: 0.6rem; border-radius: 0.5rem; border: 1px solid var(--muted); background: var(--bg); color: var(--fg); font: inherit; }
h3 { font-size: 0.95rem; margin: 0.5rem 0 0; }
</style>
