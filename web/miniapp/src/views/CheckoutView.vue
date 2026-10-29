<script setup lang="ts">
// Buying, renewing or adding traffic: review (with a discount code), choose
// how to pay (wallet or an online method), wait for the payment, and see the
// result. The order's request key stays the same for the same purchase, so a
// double tap or a retry never makes a second order. A plan priced in USDT is paid
// from the wallet (which is in Toman) in one more step: the customer is quoted the
// price in Toman at today's rate, for two minutes, and confirms it.
import { computed, onBeforeUnmount, ref } from 'vue'
import { api, ApiError, type Intent, type Order, type Quote, type WalletQuote } from '../api'
import { errorKey, gateFor, staleQuote } from '../errors'
import { tr } from '../i18n'
import { fmtClock, fmtMoney, nameOf, planFacts } from '../fmt'
import { newKey } from '../format'
import { notYetKey } from '../pay'
import { monotonic, useCountdown } from '../countdown'
import { fail, irtSpendable, loadServices, loadWallet, state } from '../store'
import { haptic } from '../telegram'
import type { Plan } from '../api'
import Methods from '../components/Methods.vue'
import PayPanel from '../components/PayPanel.vue'

// label: the name a reseller gave the new service they buy for a customer
const props = defineProps<{ plan: Plan; type: 'new' | 'renew' | 'traffic_topup'; subscriptionId?: string; label?: string }>()
const emit = defineEmits<{ close: []; services: [] }>()

type Stage = 'review' | 'pay' | 'quote' | 'wait' | 'done'
const stage = ref<Stage>('review')
const code = ref('')
const quote = ref<Quote | null>(null)
const discountError = ref('')
const order = ref<Order | null>(null)
const intent = ref<Intent | null>(null)
const busy = ref(false)
const error = ref('')
const note = ref('')
const checking = ref(false)
// The price of a plan in USDT in Toman (walletQuote), the stage 'quote' shows it. It holds for expires_in seconds, which the
// app counts with a timer of its own from the moment the quote arrived (the phone's date may be wrong); renewed says the one on
// screen replaced one that ran out, asking says a new one is on its way, and failed that asking for it did not work.
const walletQuote = ref<WalletQuote | null>(null)
const renewed = ref(false)
const asking = ref(false)
const failed = ref(false)
const noRate = ref(false)
let key = newKey()
let timer: ReturnType<typeof setInterval> | undefined
let alive = true

const total = computed(() => quote.value?.total ?? props.plan.price)
const summaryKey = computed(() =>
  props.type === 'renew' ? 'checkout.summary_renew' : props.type === 'traffic_topup' ? 'checkout.summary_topup' : 'checkout.summary_new',
)
// a reseller pays with the balance and the credit; discount codes are for customers (resellers pay tier prices)
const reseller = computed(() => state.me?.features.reseller === true)
const canWallet = computed(() => props.plan.currency === 'IRT' && irtSpendable() >= (order.value?.amount ?? total.value))
// an order priced in USDT: the wallet button asks for a quote instead of paying (whether the wallet covers it is the quote's to say)
const usdtOrder = computed(() => order.value?.currency === 'USDT')
// what the wallet lacks for the quote on screen
const missing = computed(() => {
  const q = walletQuote.value
  return q ? Math.max(0, q.amount - q.balance - q.credit_limit) : 0
})
const request = () => ({
  plan_id: props.plan.id,
  type: props.type,
  subscription_id: props.subscriptionId,
  discount_code: reseller.value ? undefined : code.value.trim() || undefined,
  label: props.type === 'new' ? props.label || undefined : undefined,
})

function stop() {
  if (timer) clearInterval(timer)
  timer = undefined
}
onBeforeUnmount(() => {
  alive = false
  stop()
})

function oops(e: unknown) {
  if (fail(e)) return
  error.value = tr(errorKey(e))
  haptic('error')
}

async function applyCode() {
  discountError.value = ''
  if (!code.value.trim()) {
    quote.value = null
    key = newKey()
    return
  }
  busy.value = true
  try {
    quote.value = await api.quote(request())
    key = newKey() // a different price is a different purchase
  } catch (e) {
    quote.value = null
    if (e instanceof ApiError && e.code === 'discount') discountError.value = tr('checkout.discount_bad')
    else oops(e)
  } finally {
    busy.value = false
  }
}

async function create() {
  error.value = ''
  busy.value = true
  try {
    order.value = await api.createOrder({ ...request(), key })
    if (isPaid(order.value.status)) await finish()
    else stage.value = 'pay'
  } catch (e) {
    oops(e)
  } finally {
    busy.value = false
  }
}

const isPaid = (s: string) => ['paid', 'provisioning', 'active', 'provision_failed'].includes(s)

async function payWallet() {
  if (!order.value) return
  error.value = ''
  busy.value = true
  try {
    order.value = await api.payWallet(order.value.id)
    haptic('success')
    await finish()
  } catch (e) {
    oops(e)
  } finally {
    busy.value = false
  }
}

// When the price on screen stops holding, a new one is asked for by itself (once: if that fails, the customer is told and asks
// again). A payment that is on its way is answered first, and the answer decides: it asks for the new price itself.
const countdown = useCountdown(() => {
  if (stage.value === 'quote' && !busy.value) {
    error.value = ''
    void askQuote(true)
  }
})

// askQuote asks for the price of the wallet payment and shows it; `again` says it replaces one that no longer holds, which the
// customer is told. It pays nothing: the customer looks at the price and confirms.
async function askQuote(again: boolean) {
  const o = order.value
  if (!o) return
  noRate.value = false
  failed.value = false
  countdown.stop()
  asking.value = true
  busy.value = true
  try {
    const q = await api.walletQuote(o.id)
    if (!alive) return
    walletQuote.value = q
    renewed.value = again
    stage.value = 'quote'
    countdown.start(monotonic() + q.expires_in)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'no_rate') {
      // The store knows no USDT rate now, so nothing can be priced in Toman: back to the ways to pay, which do not need one.
      walletQuote.value = null
      stage.value = 'pay'
      noRate.value = true
    } else {
      failed.value = stage.value === 'quote' // the price on screen is old now: it cannot be confirmed, only asked for again
      oops(e)
    }
  } finally {
    asking.value = false
    busy.value = false
  }
}

function openQuote() {
  if (busy.value) return
  error.value = ''
  void askQuote(false)
}

function retryQuote() {
  if (busy.value) return
  error.value = ''
  void askQuote(true)
}

function leaveQuote() {
  countdown.stop()
  walletQuote.value = null
  renewed.value = false
  failed.value = false
  error.value = ''
  stage.value = 'pay'
}

// payQuoted pays the price the customer was shown, by its token: core charges exactly that while it holds, and nothing else.
async function payQuoted() {
  const q = walletQuote.value
  // One request at a time (a double tap sends one), and never a price that has run out.
  if (!order.value || !q || busy.value || !q.enough || countdown.left.value <= 0) return
  error.value = ''
  renewed.value = false
  busy.value = true
  try {
    order.value = await api.payWallet(order.value.id, q.quote)
    haptic('success')
    countdown.stop()
    await finish()
  } catch (e) {
    if (staleQuote(e)) await askQuote(true)
    else {
      oops(e)
      // The wallet is not what it was when the price was shown, core did not take the quote, or the price ran out while this was
      // on its way: look at the price again.
      if (e instanceof ApiError && (e.code === 'insufficient_funds' || e.code === 'invalid')) await askQuote(false)
      else if (countdown.left.value <= 0) await askQuote(true)
    }
  } finally {
    busy.value = false
  }
}

async function payOnline(method: string) {
  if (!order.value) return
  error.value = ''
  busy.value = true
  try {
    const made = await api.pay(order.value.id, method)
    // A payment by this method that already failed or ran out (a Stars invoice is open for an hour) cannot be paid again:
    // the customer picks another way.
    if (made.status === 'failed' || made.status === 'expired') {
      error.value = tr('pay.failed')
      return
    }
    intent.value = made
    stage.value = 'wait'
    startPolling()
  } catch (e) {
    oops(e)
  } finally {
    busy.value = false
  }
}

async function finish() {
  stage.value = 'done'
  void loadWallet().catch(() => {})
  startPolling()
}

// Poll the order every few seconds until it is delivered or ends.
function startPolling() {
  stop()
  if (!alive) return // the customer left while the call that led here was on its way
  let n = 0
  timer = setInterval(async () => {
    if (!order.value || ++n > 80) return stop()
    try {
      const o = await api.order(order.value.id)
      order.value = o
      if (isPaid(o.status) && stage.value !== 'done') await finish()
      if (['active', 'provision_failed', 'cancelled', 'expired'].includes(o.status)) {
        stop()
        if (o.status === 'active') {
          haptic('success')
          void loadServices().catch(() => {})
        }
      }
    } catch (e) {
      if (gateFor(e)) {
        stop()
        fail(e)
      }
    }
  }, 3000)
}

// check asks whether the payment landed. quiet: the panel asks by itself (after Telegram says a Stars payment was made),
// so nothing is said until it has.
async function check(quiet = false) {
  if (!intent.value || !order.value) return
  if (!quiet) {
    checking.value = true
    note.value = ''
  }
  try {
    const r = await api.checkPayment(intent.value.intent_id)
    if (r.status === 'failed' || r.status === 'expired' || r.status === 'rejected') note.value = tr('pay.failed')
    else {
      const o = await api.order(order.value.id)
      order.value = o
      if (isPaid(o.status)) await finish()
      else if (!quiet) note.value = tr(notYetKey(intent.value.provider))
    }
  } catch (e) {
    if (!quiet) oops(e)
  } finally {
    checking.value = false
  }
}

const doneKey = computed(() => {
  switch (order.value?.status) {
    case 'active':
      return 'done.ready'
    case 'provision_failed':
      return 'done.stuck'
    case 'cancelled':
    case 'expired':
      return 'done.cancelled'
    default:
      return 'done.paid'
  }
})
</script>

<template>
  <main class="screen">
    <h1>{{ tr('checkout.title') }}</h1>

    <section class="card">
      <h2>{{ tr(summaryKey, { plan: nameOf(plan) }) }}</h2>
      <p v-if="type === 'renew'" class="muted small" role="note">{{ tr('renew.note') }}</p>
      <p class="muted">{{ planFacts(plan) }}</p>
      <p v-if="label && type === 'new'" class="muted">🏷 {{ label }}</p>
      <template v-if="stage === 'review' && !reseller">
        <label class="field">
          <span>{{ tr('checkout.discount') }}</span>
          <span class="row">
            <input v-model="code" type="text" dir="ltr" autocomplete="off" maxlength="64" @keydown.enter.prevent="applyCode" />
            <button class="btn secondary" type="button" :disabled="busy" @click="applyCode">{{ tr('checkout.apply') }}</button>
          </span>
        </label>
        <p v-if="quote && quote.discount > 0" class="ok">
          {{ tr('checkout.discount_applied', { amount: fmtMoney(quote.discount, quote.currency, quote.scale) }) }}
        </p>
        <p v-if="discountError" class="error" role="alert">{{ discountError }}</p>
      </template>
      <p class="total">
        <span>{{ tr('checkout.total') }}</span>
        <strong>{{ total === 0 ? tr('checkout.free') : fmtMoney(total, plan.currency, plan.scale) }}</strong>
      </p>
    </section>

    <p v-if="error" class="error" role="alert">{{ error }}</p>

    <button v-if="stage === 'review'" class="btn" type="button" :disabled="busy" @click="create">{{ tr('checkout.continue') }}</button>

    <template v-else-if="stage === 'pay' && order">
      <button class="btn" type="button" :disabled="busy || (!usdtOrder && !canWallet)" @click="usdtOrder ? openQuote() : payWallet()">
        {{ tr('checkout.pay_wallet', { balance: fmtMoney(irtSpendable(), 'IRT', 0) }) }}
      </button>
      <p v-if="!usdtOrder && !canWallet" class="muted">{{ tr('checkout.low_wallet') }}</p>
      <p v-if="noRate" class="error" role="alert">{{ tr('err.no_rate') }}</p>
      <h2>{{ tr('checkout.pay_methods') }}</h2>
      <Methods :currency="order.currency" :amount="order.amount" :busy="busy" @pick="payOnline" />
    </template>

    <template v-else-if="stage === 'quote' && order">
      <template v-if="walletQuote && !failed">
        <section class="card quote">
          <h2>{{ tr('quote.title') }}</h2>
          <p v-if="renewed" class="notice" role="status">{{ tr('quote.updated') }}</p>
          <p class="total">
            <span>{{ tr('quote.price') }}</span>
            <strong>{{ fmtMoney(walletQuote.list_amount, walletQuote.list_currency, walletQuote.list_scale) }}</strong>
          </p>
          <p class="total">
            <span>{{ tr('quote.rate') }}</span>
            <strong>{{ tr('quote.rate_line', { rate: fmtMoney(walletQuote.rate, walletQuote.currency, walletQuote.scale) }) }}</strong>
          </p>
          <p class="total charge">
            <span>{{ tr('quote.charge') }}</span>
            <strong class="big">{{ fmtMoney(walletQuote.amount, walletQuote.currency, walletQuote.scale) }}</strong>
          </p>
          <p class="total">
            <span>{{ tr('quote.balance') }}</span>
            <span>{{ fmtMoney(walletQuote.balance, walletQuote.currency, walletQuote.scale) }}</span>
          </p>
          <p v-if="asking" class="muted" role="status">{{ tr('quote.updating') }}</p>
          <p v-else class="muted" role="timer">{{ tr('quote.held') }} <span class="clock" dir="ltr">{{ fmtClock(countdown.left.value) }}</span></p>
          <p v-if="!walletQuote.enough" class="error" role="alert">
            {{ tr('checkout.low_wallet') }} {{ tr('quote.missing', { missing: fmtMoney(missing, walletQuote.currency, walletQuote.scale) }) }}
          </p>
        </section>
        <button class="btn" type="button" :disabled="busy || !walletQuote.enough || countdown.left.value <= 0" @click="payQuoted">
          {{ tr('quote.confirm', { amount: fmtMoney(walletQuote.amount, walletQuote.currency, walletQuote.scale) }) }}
        </button>
      </template>
      <!-- the price on screen is old and a new one could not be asked for: only asking again (the error above says why) -->
      <button v-else class="btn" type="button" :disabled="busy" @click="retryQuote">{{ tr('common.retry') }}</button>
      <button class="btn secondary" type="button" :disabled="busy" @click="leaveQuote">{{ tr('common.back') }}</button>
    </template>

    <PayPanel
      v-else-if="stage === 'wait' && intent"
      :intent="intent"
      :checking="checking"
      :message="note"
      @check="(quiet) => check(quiet)"
      @leave="emit('services')"
      @expired="stop"
    />

    <section v-else-if="stage === 'done'" class="card" role="status">
      <h2>{{ tr('done.title') }}</h2>
      <p>{{ tr(doneKey) }}</p>
      <p v-if="order?.status === 'active' && order.cashback && order.cashback.amount > 0" class="ok">
        🎁 {{ tr('done.cashback', { amount: fmtMoney(order.cashback.amount, order.cashback.currency, order.cashback.scale) }) }}
      </p>
      <button class="btn" type="button" @click="emit('services')">{{ tr('done.view') }}</button>
    </section>

    <button v-if="stage !== 'done'" class="btn secondary" type="button" @click="emit('close')">{{ tr('done.back_store') }}</button>
  </main>
</template>

<style scoped>
/* the sum that is taken has a line of its own, big: it is the one number the customer must read */
.charge { flex-direction: column; align-items: flex-start; gap: 0.1rem; }
/* the new price is called out: a line of its own with the accent beside it, so a changed number is not missed */
.notice { padding: 0.5rem 0.75rem; border-radius: 0.5rem; background: var(--bg); border-inline-start: 0.25rem solid var(--accent); }
.clock { font-variant-numeric: tabular-nums; }
</style>
