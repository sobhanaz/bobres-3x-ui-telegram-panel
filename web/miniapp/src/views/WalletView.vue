<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api, type Intent, type Me, type PendingPayment } from '../api'
import { errorKey, gateFor } from '../errors'
import { tr } from '../i18n'
import { fmtDateTime, fmtMoney, fmtNum, ledgerKey } from '../fmt'
import { newKey, wholeNumber, wholeUsdt } from '../format'
import { isManual, notYetKey, tomanForUsdt } from '../pay'
import { fail, irtBalance, loadWallet, state } from '../store'
import Methods from '../components/Methods.vue'
import PayPanel from '../components/PayPanel.vue'
import PendingPayments from '../components/PendingPayments.vue'

const presets = [100_000, 200_000, 500_000, 1_000_000]
// whole USDT, as they are offered
const usdtPresets = [5, 10, 25, 50, 100]
const error = ref('')
// the amount chosen, in the currency it was chosen in: Toman, or micro-USDT (6 decimals)
const amount = ref(0)
const custom = ref('')
const stage = ref<'amount' | 'method' | 'wait'>('amount')
// The wallet is in Toman either way; a top-up in USDT is credited to it as Toman, at the rate of the moment it is seen.
// `currency` is what the switch says, `chosen` what the amount in `amount` is in.
const currency = ref<'IRT' | 'USDT'>('IRT')
const chosen = ref<'IRT' | 'USDT'>('IRT')
const usdtMethods = ref(false)
const intent = ref<Intent | null>(null)
const busy = ref(false)
const checking = ref(false)
const note = ref('')
// the manual payments still waiting for their receipt; the payment on screen was taken up again from that list
const pending = ref<PendingPayment[]>([])
const pendingFailed = ref(false)
const reopened = ref(false)
// A USDT top-up that was seen: how far its credit to the wallet has got (see watchCredit).
interface Credit { amount: number; scale: number; before: number; phase: 'adding' | 'done' | 'late'; added: number }
const credit = ref<Credit | null>(null)
let key = newKey()
let timer: ReturnType<typeof setInterval> | undefined
let creditTimer: ReturnType<typeof setTimeout> | undefined
let creditRun = 0
// the Toman balance when the payment on screen began: a USDT top-up has landed when the balance is above it
let startBalance = 0
let alive = true

const top = computed<Me['top_up']>(() => state.me?.top_up ?? { currency: 'IRT', min: 10_000, max: 50_000_000 })
const usdt = computed(() => top.value.usdt)
const usdtScale = computed(() => usdt.value?.scale ?? 6)
// "From 1 to 10,000 USDT": the bounds are whole USDT
const usdtRange = computed(() => {
  const u = usdt.value
  return u ? tr('wallet.range', { min: fmtNum(u.min / 10 ** u.scale), max: fmtMoney(u.max, 'USDT', u.scale) }) : ''
})
// Tether is offered when core takes it and the store has a way to be paid in it
const usdtOffered = computed(() => !!usdt.value && usdtMethods.value)
const inUsdt = computed(() => usdtOffered.value && currency.value === 'USDT')
const irt = computed(() => state.wallets.find((w) => w.currency === 'IRT'))
// the live price of one USDT in Toman, without the store's margin: what a top-up in USDT is credited at (0: none is known)
const rate = computed(() => irt.value?.usdt_rate ?? 0)

// An amount of whole USDT the top-up takes, in micro-USDT; 0 when it is not one (not a whole number, or out of bounds).
function usdtMicro(whole: number): number {
  const u = usdt.value
  const micro = u ? whole * 10 ** u.scale : NaN
  return u && Number.isInteger(whole) && micro >= u.min && micro <= u.max && micro % u.step === 0 ? micro : 0
}
// the presets that are inside the bounds core sent (all of them, as the bounds are today)
const usdtChoices = computed(() => usdtPresets.filter((p) => usdtMicro(p) > 0))

// What a top-up of an amount of USDT adds to the wallet, as the customer is told before paying: the amount in Toman at today's
// rate, rounded down (as the wallet is credited), or that the rate is not known yet.
function estimate(micro: number): string {
  return rate.value > 0
    ? tr('wallet.est_usdt', { toman: fmtMoney(tomanForUsdt(micro, rate.value), 'IRT', 0), rate: fmtMoney(rate.value, 'IRT', 0) })
    : tr('wallet.est_none')
}
// under the custom field: the estimate for what is typed there, once it is an amount; with no rate there is nothing to wait for
const typedHint = computed(() => {
  const micro = usdtMicro(wholeUsdt(custom.value))
  return micro > 0 || rate.value <= 0 ? estimate(micro) : ''
})

// A switch of currency starts the amount again.
watch(currency, () => {
  custom.value = ''
  error.value = ''
})

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

// Whether the store can take USDT at all is asked once, when the tab opens, at the smallest amount: the switch is there only
// if it can. Not knowing (an answer that failed) is a no: the Toman top-up is what the screen is for.
async function probeUsdt() {
  const u = usdt.value
  if (!u) return
  try {
    const methods = await api.methods('USDT', u.min)
    if (alive) usdtMethods.value = methods.length > 0
  } catch {
    // no Tether
  }
}
onMounted(() => {
  void load()
  void loadPending()
  void probeUsdt()
})

function stop() {
  if (timer) clearInterval(timer)
  timer = undefined
}
onBeforeUnmount(() => {
  alive = false
  stop()
  clearTimeout(creditTimer)
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
  chosen.value = 'IRT'
  key = newKey()
  stage.value = 'method'
}

// whole USDT; the amount is kept in micro-USDT, which is what core takes
function chooseUsdt(whole: number) {
  error.value = ''
  const micro = usdtMicro(whole)
  if (!micro) {
    error.value = tr('wallet.bad_amount')
    return
  }
  amount.value = micro
  chosen.value = 'USDT'
  key = newKey()
  stage.value = 'method'
}

async function pick(method: string) {
  busy.value = true
  error.value = ''
  try {
    const made = await api.topup(amount.value, method, key, chosen.value === 'USDT' ? 'USDT' : undefined)
    // A payment by this method that already failed or ran out cannot be paid again: another way is chosen.
    if (made.status === 'failed' || made.status === 'expired') {
      error.value = tr('pay.failed')
      return
    }
    if (!alive) return
    intent.value = made
    note.value = '' // what was said about an earlier payment is old now
    credit.value = null
    creditRun++
    startBalance = irtBalance()
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
  startBalance = irtBalance()
  stage.value = 'wait'
  startPolling(p.provider)
}

// A USDT payment is credited to the wallet, in Toman, a moment after it is seen. The wallet is asked for at once and then every
// 2 seconds, 8 more times at most, until the balance is above what it was when the top-up began; `credit` says where it stands.
const CREDIT_EVERY = 2000
const CREDIT_RETRIES = 8
async function watchCredit(c: Credit) {
  const run = ++creditRun
  credit.value = c
  for (let i = 0; i <= CREDIT_RETRIES; i++) {
    if (i > 0) await new Promise<void>((resolve) => (creditTimer = setTimeout(resolve, CREDIT_EVERY)))
    if (!alive || run !== creditRun) return // the customer left, or another payment took the screen over
    try {
      await loadWallet()
    } catch (e) {
      if (fail(e)) return
    }
    if (!alive || run !== creditRun) return
    if (irtBalance() > c.before) {
      credit.value = { ...c, phase: 'done', added: irtBalance() - c.before }
      return
    }
  }
  credit.value = { ...c, phase: 'late' }
}
function askCreditAgain() {
  if (credit.value) void watchCredit({ ...credit.value, phase: 'adding' })
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
      custom.value = '' // the next top-up starts from an empty field, not from the amount just paid
      if (current.currency === 'USDT' && !current.order_id) {
        // a top-up in USDT: the wallet is credited a moment later, in Toman
        note.value = ''
        void watchCredit({ amount: current.amount, scale: current.scale, before: startBalance, phase: 'adding', added: 0 })
      } else {
        // a payment taken up again may be an order's: the wallet is not what went up then
        note.value = tr(current.order_id ? 'manual.approved' : 'done.topup')
        await loadWallet()
      }
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

// The time of an automatic USDT payment ran out: nothing more to ask about (the panel says so, and Back starts again).
function expired() {
  stop()
  note.value = ''
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
        <span v-if="w.usdt_rate" class="muted small usdt-line">
          {{ tr('wallet.usdt', { usdt: fmtMoney(w.usdt_equivalent ?? 0, 'USDT', 6), rate: fmtMoney(w.usdt_rate, 'IRT', 0) }) }}
        </span>
      </div>
      <p v-if="note" class="ok" role="status">{{ note }}</p>

      <section v-if="state.cashback" class="card cashback" aria-label="cashback">
        <template v-if="state.cashback.on">
          <p>🎁 {{ tr('cashback.on', { percent: fmtNum(Number(state.cashback.percent)) }) }}</p>
          <p v-if="state.cashback.min_order > 0" class="muted small">
            {{ tr('cashback.min', { amount: fmtMoney(state.cashback.min_order, state.cashback.currency, state.cashback.scale) }) }}
          </p>
          <p v-if="state.cashback.max_per_order > 0" class="muted small">
            {{ tr('cashback.max', { amount: fmtMoney(state.cashback.max_per_order, state.cashback.currency, state.cashback.scale) }) }}
          </p>
          <p v-if="!state.cashback.renewals" class="muted small">{{ tr('cashback.new_only') }}</p>
        </template>
        <p v-if="state.cashback.earned > 0" class="ok">
          {{ tr('cashback.earned', { amount: fmtMoney(state.cashback.earned, state.cashback.currency, state.cashback.scale) }) }}
        </p>
      </section>

      <section v-if="credit" class="card credit" role="status">
        <h2>{{ tr('done.title') }}</h2>
        <p class="ok">{{ tr('wallet.usdt_paid', { amount: fmtMoney(credit.amount, 'USDT', credit.scale) }) }}</p>
        <p v-if="credit.phase === 'adding'" class="muted">{{ tr('wallet.usdt_adding') }}</p>
        <template v-else-if="credit.phase === 'done'">
          <p class="row between">
            <span>{{ tr('wallet.usdt_added') }}</span>
            <strong class="pos" dir="ltr">+{{ fmtMoney(credit.added, 'IRT', 0) }}</strong>
          </p>
          <p v-if="irt" class="row between">
            <span>{{ tr('wallet.usdt_now') }}</span>
            <strong>{{ fmtMoney(irt.balance, 'IRT', irt.scale) }}</strong>
          </p>
          <p v-if="irt?.usdt_rate" class="muted small">
            {{ tr('wallet.usdt', { usdt: fmtMoney(irt.usdt_equivalent ?? 0, 'USDT', 6), rate: fmtMoney(irt.usdt_rate, 'IRT', 0) }) }}
          </p>
        </template>
        <template v-else>
          <p class="muted">{{ tr('wallet.usdt_late') }}</p>
          <button class="btn secondary" type="button" @click="askCreditAgain">{{ tr('common.retry') }}</button>
        </template>
      </section>

      <PendingPayments :payments="pending" @open="reopen" />
      <p v-if="pendingFailed" class="muted small">
        {{ tr('wallet.pending_failed') }} <button class="link" type="button" @click="loadPending">{{ tr('common.retry') }}</button>
      </p>

      <section class="card">
        <h2>{{ tr('wallet.topup') }}</h2>
        <template v-if="stage === 'amount'">
          <div v-if="usdtOffered" class="seg" role="group" :aria-label="tr('wallet.currency')">
            <button class="btn" :class="{ secondary: currency !== 'IRT' }" type="button" :aria-pressed="currency === 'IRT'" @click="currency = 'IRT'">
              {{ tr('wallet.cur_irt') }}
            </button>
            <button class="btn" :class="{ secondary: currency !== 'USDT' }" type="button" :aria-pressed="currency === 'USDT'" @click="currency = 'USDT'">
              {{ tr('wallet.cur_usdt') }}
            </button>
          </div>
          <template v-if="inUsdt">
            <div class="row wrap">
              <button v-for="p in usdtChoices" :key="p" class="btn secondary" type="button" @click="chooseUsdt(p)">
                {{ fmtMoney(p * 10 ** usdtScale, 'USDT', usdtScale) }}
              </button>
            </div>
            <label class="field">
              <span>{{ tr('wallet.custom_usdt') }}</span>
              <span class="row">
                <input v-model="custom" type="text" inputmode="numeric" autocomplete="off" @keydown.enter.prevent="chooseUsdt(wholeUsdt(custom))" />
                <button class="btn" type="button" @click="chooseUsdt(wholeUsdt(custom))">{{ tr('checkout.continue') }}</button>
              </span>
              <span class="muted small">{{ usdtRange }}</span>
            </label>
            <p v-if="typedHint" class="muted small">{{ typedHint }}</p>
          </template>
          <template v-else>
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
        </template>
        <template v-else-if="stage === 'method'">
          <template v-if="chosen === 'USDT'">
            <p>{{ fmtMoney(amount, 'USDT', usdtScale) }}</p>
            <p class="muted small">{{ estimate(amount) }}</p>
          </template>
          <p v-else>{{ fmtMoney(amount, top.currency, 0) }}</p>
          <Methods :currency="chosen === 'USDT' ? 'USDT' : top.currency" :amount="amount" :busy="busy" @pick="pick" />
          <button class="btn secondary" type="button" @click="back">{{ tr('common.back') }}</button>
        </template>
        <template v-else-if="intent">
          <PayPanel :intent="intent" :checking="checking" :message="note" :show-leave="false" @check="(quiet) => check(quiet)" @expired="expired" />
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
/* Toman | USDT: one control, two halves, the chosen one filled */
.seg { display: grid; grid-template-columns: 1fr 1fr; border-radius: 0.6rem; box-shadow: inset 0 0 0 1px var(--accent); }
.seg .btn { box-shadow: none; }
</style>
