<script setup lang="ts">
// The customer's manual payments that still wait for their receipt (core: GET /payments/pending): paid at the bank, or
// meant to be, and never proved, for instance because Telegram reloaded the app between the bank and the receipt. Each
// row says what it is and has a button that opens the payment's own screen again (the card or the addresses, the
// reference, and the form for the receipt), which ends where every receipt does.
import type { PendingPayment } from '../api'
import { tr } from '../i18n'
import { fmtAgo, fmtExact, fmtMoney } from '../fmt'
import { isolate } from '../format'
import { shortMethodKey } from '../pay'

defineProps<{ payments: PendingPayment[] }>()
defineEmits<{ open: [payment: PendingPayment] }>()

// An amount in USDT is shown whole, as it is on the payment's screen.
const amountOf = (p: PendingPayment) =>
  p.currency === 'USDT' ? fmtExact(p.amount, 'USDT', p.scale) : fmtMoney(p.amount, p.currency, p.scale)
// What it is for: a top-up has no order.
const purposeOf = (p: PendingPayment) => tr(p.order_id ? 'wallet.pending_order' : 'wallet.pending_topup')
</script>

<template>
  <section v-if="payments.length" class="card" aria-labelledby="pending-title">
    <h2 id="pending-title">{{ tr('wallet.pending_title') }}</h2>
    <p class="muted small">{{ tr('wallet.pending_hint') }}</p>
    <ul class="list">
      <li v-for="p in payments" :key="p.intent_id" class="pending">
        <div :id="'pending-' + p.intent_id" class="row between">
          <strong>{{ tr(shortMethodKey(p.provider)) }}</strong>
          <strong>{{ amountOf(p) }}</strong>
        </div>
        <p class="muted small">{{ purposeOf(p) }} · {{ fmtAgo(p.created_at) }}</p>
        <p v-if="p.details.reference" class="muted small">
          {{ tr('manual.reference') }}: <span class="mono" dir="ltr">{{ isolate(p.details.reference) }}</span>
        </p>
        <button class="btn secondary" type="button" :aria-describedby="'pending-' + p.intent_id" @click="$emit('open', p)">
          {{ tr('wallet.pending_send') }}
        </button>
      </li>
    </ul>
  </section>
</template>

<style scoped>
/* something to finish: set apart from the balance and the top-up around it */
.card { margin-block: 0.75rem; box-shadow: inset 0 0 0 1px var(--accent); }
/* one payment to a block: a line between two payments, so that each button is plainly the one of its own payment */
.pending { display: grid; gap: 0.25rem; padding-block: 0.5rem; }
.pending + .pending { border-top: 1px solid var(--bg); }
</style>
