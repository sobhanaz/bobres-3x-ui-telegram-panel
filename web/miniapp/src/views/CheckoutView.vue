<script setup lang="ts">
// Buying, renewing or adding traffic: review (with a discount code), choose
// how to pay (wallet or an online method), wait for the payment, and see the
// result. The order's request key stays the same for the same purchase, so a
// double tap or a retry never makes a second order.
import { computed, onBeforeUnmount, ref } from 'vue'
import { api, ApiError, type Intent, type Order, type Quote } from '../api'
import { errorKey, gateFor } from '../errors'
import { tr } from '../i18n'
import { fmtMoney, nameOf, planFacts } from '../fmt'
import { newKey } from '../format'
import { fail, irtBalance, loadServices, loadWallet } from '../store'
import { haptic } from '../telegram'
import type { Plan } from '../api'
import Methods from '../components/Methods.vue'
import PayPanel from '../components/PayPanel.vue'

const props = defineProps<{ plan: Plan; type: 'new' | 'renew' | 'traffic_topup'; subscriptionId?: string }>()
const emit = defineEmits<{ close: []; services: [] }>()

type Stage = 'review' | 'pay' | 'wait' | 'done'
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
let key = newKey()
let timer: ReturnType<typeof setInterval> | undefined

const total = computed(() => quote.value?.total ?? props.plan.price)
const summaryKey = computed(() =>
  props.type === 'renew' ? 'checkout.summary_renew' : props.type === 'traffic_topup' ? 'checkout.summary_topup' : 'checkout.summary_new',
)
const canWallet = computed(() => props.plan.currency === 'IRT' && irtBalance() >= (order.value?.amount ?? total.value))
const request = () => ({
  plan_id: props.plan.id,
  type: props.type,
  subscription_id: props.subscriptionId,
  discount_code: code.value.trim() || undefined,
})

function stop() {
  if (timer) clearInterval(timer)
  timer = undefined
}
onBeforeUnmount(stop)

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

async function payOnline(method: string) {
  if (!order.value) return
  error.value = ''
  busy.value = true
  try {
    intent.value = await api.pay(order.value.id, method)
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

async function check() {
  if (!intent.value || !order.value) return
  checking.value = true
  note.value = ''
  try {
    const r = await api.checkPayment(intent.value.intent_id)
    if (r.status === 'failed' || r.status === 'expired' || r.status === 'rejected') note.value = tr('pay.failed')
    else {
      const o = await api.order(order.value.id)
      order.value = o
      if (isPaid(o.status)) await finish()
      else note.value = tr('pay.not_yet')
    }
  } catch (e) {
    oops(e)
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
      <p class="muted">{{ planFacts(plan) }}</p>
      <template v-if="stage === 'review'">
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
      <button class="btn" type="button" :disabled="busy || !canWallet" @click="payWallet">
        {{ tr('checkout.pay_wallet', { balance: fmtMoney(irtBalance(), 'IRT', 0) }) }}
      </button>
      <p v-if="!canWallet" class="muted">{{ tr('checkout.low_wallet') }}</p>
      <h2>{{ tr('checkout.pay_methods') }}</h2>
      <Methods :currency="order.currency" :amount="order.amount" :busy="busy" @pick="payOnline" />
    </template>

    <PayPanel v-else-if="stage === 'wait' && intent" :intent="intent" :checking="checking" :message="note" @check="check" />

    <section v-else-if="stage === 'done'" class="card" role="status">
      <h2>{{ tr('done.title') }}</h2>
      <p>{{ tr(doneKey) }}</p>
      <button class="btn" type="button" @click="emit('services')">{{ tr('done.view') }}</button>
    </section>

    <button v-if="stage !== 'done'" class="btn secondary" type="button" @click="emit('close')">{{ tr('done.back_store') }}</button>
  </main>
</template>
