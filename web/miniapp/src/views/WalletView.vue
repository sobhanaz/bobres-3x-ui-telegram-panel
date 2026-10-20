<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, type Intent } from '../api'
import { errorKey } from '../errors'
import { tr } from '../i18n'
import { fmtDateTime, fmtMoney, ledgerKey } from '../fmt'
import { newKey, wholeNumber } from '../format'
import { isManual } from '../pay'
import { fail, loadWallet, state } from '../store'
import Methods from '../components/Methods.vue'
import PayPanel from '../components/PayPanel.vue'

const presets = [100_000, 200_000, 500_000, 1_000_000]
const error = ref('')
const amount = ref(0)
const custom = ref('')
const stage = ref<'amount' | 'method' | 'wait'>('amount')
const intent = ref<Intent | null>(null)
const busy = ref(false)
const checking = ref(false)
const note = ref('')
let key = newKey()
let timer: ReturnType<typeof setInterval> | undefined

const top = computed(() => state.me?.top_up ?? { currency: 'IRT', min: 10_000, max: 50_000_000 })

async function load() {
  error.value = ''
  try {
    await loadWallet()
  } catch (e) {
    if (!fail(e)) error.value = tr(errorKey(e))
  }
}
onMounted(load)
onBeforeUnmount(() => timer && clearInterval(timer))

function choose(n: number) {
  error.value = ''
  if (!Number.isFinite(n) || n < top.value.min || n > top.value.max) {
    error.value = tr('wallet.bad_amount')
    return
  }
  amount.value = n
  key = newKey()
  stage.value = 'method'
}

async function pick(method: string) {
  busy.value = true
  error.value = ''
  try {
    const made = await api.topup(amount.value, method, key)
    // A payment by this method that already failed or ran out cannot be paid again: another way is chosen.
    if (made.status === 'failed' || made.status === 'expired') {
      error.value = tr('pay.failed')
      return
    }
    intent.value = made
    stage.value = 'wait'
    // Asked every 5 seconds for 5 minutes; a payment that waits for staff to review it is told in the bot.
    let polls = 0
    if (timer) clearInterval(timer)
    timer = setInterval(() => (++polls > 60 ? clearInterval(timer) : void check(true)), 5000)
  } catch (e) {
    if (!fail(e)) error.value = tr(errorKey(e))
  } finally {
    busy.value = false
  }
}

async function check(quiet = false) {
  if (!intent.value) return
  if (!quiet) checking.value = true
  try {
    const r = await api.checkPayment(intent.value.intent_id)
    if (r.status === 'succeeded') {
      if (timer) clearInterval(timer)
      stage.value = 'amount'
      intent.value = null
      note.value = tr('done.topup')
      await loadWallet()
    } else if (!quiet) note.value = tr(isManual(intent.value.provider) ? 'manual.still_waiting' : 'pay.not_yet')
  } catch (e) {
    if (fail(e) && timer) clearInterval(timer)
    else if (!quiet) error.value = tr(errorKey(e))
  } finally {
    checking.value = false
  }
}

function back() {
  if (timer) clearInterval(timer)
  stage.value = 'amount'
  intent.value = null
}
</script>

<template>
  <section>
    <h1>{{ tr('wallet.title') }}</h1>
    <p v-if="error" class="error" role="alert">{{ error }}</p>

    <div v-for="w in state.wallets" :key="w.currency" class="card">
      <span class="muted">{{ tr('wallet.balance') }}</span>
      <strong class="big">{{ fmtMoney(w.balance, w.currency, w.scale) }}</strong>
    </div>
    <p v-if="note" class="ok" role="status">{{ note }}</p>

    <section class="card">
      <h2>{{ tr('wallet.topup') }}</h2>
      <template v-if="stage === 'amount'">
        <div class="row wrap">
          <button v-for="p in presets" :key="p" class="btn secondary" type="button" @click="choose(p)">{{ fmtMoney(p, top.currency, 0) }}</button>
        </div>
        <label class="field">
          <span>{{ tr('wallet.custom') }}</span>
          <span class="row">
            <input v-model="custom" type="text" inputmode="numeric" autocomplete="off" @keydown.enter.prevent="choose(wholeNumber(custom))" />
            <button class="btn" type="button" @click="choose(wholeNumber(custom))">{{ tr('checkout.continue') }}</button>
          </span>
          <span class="muted small">{{ tr('wallet.range', { min: fmtMoney(top.min, top.currency, 0), max: fmtMoney(top.max, top.currency, 0) }) }}</span>
        </label>
      </template>
      <template v-else-if="stage === 'method'">
        <p>{{ fmtMoney(amount, top.currency, 0) }}</p>
        <Methods :currency="top.currency" :amount="amount" :busy="busy" @pick="pick" />
        <button class="btn secondary" type="button" @click="back">{{ tr('common.back') }}</button>
      </template>
      <template v-else-if="intent">
        <PayPanel :intent="intent" :checking="checking" :message="note" :show-leave="false" @check="(quiet) => check(quiet)" />
        <button class="btn secondary" type="button" @click="back">{{ tr('common.back') }}</button>
      </template>
    </section>

    <h2>{{ tr('wallet.history') }}</h2>
    <p v-if="!state.ledger.length" class="muted">{{ tr('wallet.empty') }}</p>
    <ul v-else class="list">
      <li v-for="(e, i) in state.ledger" :key="i" class="card row between">
        <span>{{ tr(ledgerKey(e.kind)) }}<br /><span class="muted small">{{ fmtDateTime(e.at) }}</span></span>
        <strong :class="e.amount < 0 ? 'neg' : 'pos'" dir="ltr">{{ (e.amount > 0 ? '+' : '') + fmtMoney(e.amount, e.currency, e.scale) }}</strong>
      </li>
    </ul>
  </section>
</template>
