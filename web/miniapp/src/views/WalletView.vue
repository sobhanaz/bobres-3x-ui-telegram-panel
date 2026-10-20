<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, type Intent, type PendingPayment } from '../api'
import { errorKey, gateFor } from '../errors'
import { tr } from '../i18n'
import { fmtDateTime, fmtMoney, ledgerKey } from '../fmt'
import { newKey, wholeNumber } from '../format'
import { isManual, notYetKey } from '../pay'
import { fail, loadWallet, state } from '../store'
import Methods from '../components/Methods.vue'
import PayPanel from '../components/PayPanel.vue'
import PendingPayments from '../components/PendingPayments.vue'

const presets = [100_000, 200_000, 500_000, 1_000_000]
const error = ref('')
const amount = ref(0)
const custom = ref('')
const stage = ref<'amount' | 'method' | 'wait'>('amount')
const intent = ref<Intent | null>(null)
const busy = ref(false)
const checking = ref(false)
const note = ref('')
// the manual payments still waiting for their receipt; the payment on screen was taken up again from that list
const pending = ref<PendingPayment[]>([])
const pendingFailed = ref(false)
const reopened = ref(false)
let key = newKey()
let timer: ReturnType<typeof setInterval> | undefined
let alive = true

const top = computed(() => state.me?.top_up ?? { currency: 'IRT', min: 10_000, max: 50_000_000 })

async function load() {
  error.value = ''
  try {
    await loadWallet()
  } catch (e) {
    if (!fail(e)) error.value = tr(errorKey(e))
  }
}

// The list is a help, not the wallet: when it cannot be read the wallet still works, and being signed out or switched off
// is the only failure that replaces the screen (a busy store or payments does not).
let pendingAsk = 0
async function loadPending() {
  const mine = ++pendingAsk
  try {
    const rows = await api.pendingPayments()
    if (mine !== pendingAsk) return // a newer ask is on its way, and its answer is the one to show
    pending.value = rows
    pendingFailed.value = false
  } catch (e) {
    if (mine !== pendingAsk) return
    const gate = gateFor(e)
    if (gate && gate !== 'maintenance' && gate !== 'unavailable') state.gate = gate
    else pendingFailed.value = true
  }
}
onMounted(() => {
  void load()
  void loadPending()
})

function stop() {
  if (timer) clearInterval(timer)
  timer = undefined
}
onBeforeUnmount(() => {
  alive = false
  stop()
})

// A payment is asked about every 5 seconds, as long as the screen is open. One that waits for the staff (the result comes
// in the bot) is asked about every 30 seconds, for half an hour: it would use most of the customer's changes a minute.
function startPolling(provider: string) {
  stop()
  if (!alive) return // the customer left while the call that led here was on its way
  const manual = isManual(provider)
  let polls = 0
  timer = setInterval(() => (manual && ++polls > 60 ? stop() : void check(true)), manual ? 30_000 : 5000)
}

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
    if (!alive) return
    intent.value = made
    stage.value = 'wait'
    startPolling(made.provider)
  } catch (e) {
    if (!fail(e)) error.value = tr(errorKey(e))
  } finally {
    busy.value = false
  }
}

// Take a payment up again from the list: the screen it had, and then the same way on as a payment made here.
function reopen(p: PendingPayment) {
  error.value = ''
  note.value = ''
  stop()
  intent.value = p
  reopened.value = true
  stage.value = 'wait'
  startPolling(p.provider)
}

async function check(quiet = false) {
  const current = intent.value
  if (!current) return
  if (!quiet) checking.value = true
  try {
    const r = await api.checkPayment(current.intent_id)
    if (intent.value !== current) return // the customer went back while the call was on its way
    if (r.status === 'succeeded') {
      stop()
      stage.value = 'amount'
      intent.value = null
      reopened.value = false
      // a payment taken up again may be an order's: the wallet is not what went up then
      note.value = tr(current.order_id ? 'manual.approved' : 'done.topup')
      await loadWallet()
      void loadPending()
    } else if (r.status === 'failed' || r.status === 'expired' || r.status === 'rejected') {
      // Over: the staff turned a manual payment down, or a gateway's time ran out. Nothing more to wait for.
      stop()
      note.value = tr('pay.failed')
    } else if (!quiet) note.value = tr(notYetKey(current.provider))
  } catch (e) {
    if (fail(e)) stop()
    else if (!quiet) error.value = tr(errorKey(e))
  } finally {
    checking.value = false
  }
}

function back() {
  stop()
  if (intent.value) note.value = '' // what was said about that payment (still waiting, did not go through) is old now
  stage.value = 'amount'
  intent.value = null
  if (reopened.value) {
    reopened.value = false
    void load() // the staff may have decided meanwhile
  }
  void loadPending() // a payment started here and left is waiting for its receipt now, and one whose receipt went is not
}
</script>

<template>
  <section>
    <h1>{{ tr('wallet.title') }}</h1>
    <p v-if="error" class="error" role="alert">{{ error }}</p>

    <div v-if="reopened && intent" class="stack">
      <PayPanel :intent="intent" :checking="checking" :message="note" :show-leave="false" reopened @check="(quiet) => check(quiet)" />
      <button class="btn secondary" type="button" @click="back">{{ tr('common.back') }}</button>
    </div>

    <template v-else>
      <div v-for="w in state.wallets" :key="w.currency" class="card">
        <span class="muted">{{ tr('wallet.balance') }}</span>
        <strong class="big">{{ fmtMoney(w.balance, w.currency, w.scale) }}</strong>
      </div>
      <p v-if="note" class="ok" role="status">{{ note }}</p>

      <PendingPayments :payments="pending" @open="reopen" />
      <p v-if="pendingFailed" class="muted small">
        {{ tr('wallet.pending_failed') }} <button class="link" type="button" @click="loadPending">{{ tr('common.retry') }}</button>
      </p>

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
    </template>
  </section>
</template>

<style scoped>
/* a payment taken up again is a screen of its own: blocks apart, buttons the width of the screen (as in the checkout) */
.stack { display: grid; gap: 0.75rem; }
</style>
