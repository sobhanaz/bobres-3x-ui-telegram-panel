<script setup lang="ts">
// Paying with Telegram Stars: core made the payment and the bot made an invoice link; Telegram's own payment sheet
// opens on it (Telegram.WebApp.openInvoice). The bot settles the payment when Telegram reports it, so after "paid" the
// app only asks until it sees the payment succeed. The sheet opens by itself, and again with the button.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { Intent } from '../api'
import { tr } from '../i18n'
import { fmtMoney, fmtNum } from '../fmt'
import { haptic, openInvoice, openTelegram, type InvoiceStatus } from '../telegram'

const props = defineProps<{ intent: Intent; checking: boolean; message: string }>()
const emit = defineEmits<{ check: [quiet?: boolean] }>()

type Phase = 'ready' | 'opened' | 'confirming' | 'cancelled' | 'failed'
const phase = ref<Phase>('ready')
// Only a Telegram link is opened (core checks it too).
const url = computed(() => (/^https:\/\/t\.me\//.test(props.intent.invoice_url ?? '') ? (props.intent.invoice_url ?? '') : ''))
const stars = computed(() => props.intent.stars ?? props.intent.gateway_amount)
const price = computed(() => fmtMoney(props.intent.amount, props.intent.currency, props.intent.scale))
// Nothing to open: the payment is paid, over, or has no invoice.
const closed = computed(() => props.intent.status !== 'pending' || !url.value)

let timer: ReturnType<typeof setTimeout> | undefined
function stopAsking() {
  if (timer) clearTimeout(timer)
  timer = undefined
}
// Telegram says "paid" a moment before the bot has recorded the payment: ask once now and once more shortly after, quietly;
// the screen that holds this one keeps asking at its own pace (and the customer may press "I have paid").
function askAfterPaid() {
  stopAsking()
  emit('check', true)
  timer = setTimeout(() => emit('check', true), 3000)
}

let gone = false

function ended(status: InvoiceStatus) {
  if (gone) return // the customer left while the sheet was open
  switch (status) {
    case 'paid':
    case 'pending':
      phase.value = 'confirming'
      haptic('success')
      askAfterPaid()
      break
    case 'cancelled':
      phase.value = 'cancelled'
      break
    default:
      phase.value = 'failed'
      haptic('error')
  }
}

function open() {
  if (!url.value) return
  phase.value = 'opened'
  // An older Telegram has no payment sheet: the link opens the invoice in the chat, and "I have paid" asks afterwards.
  if (!openInvoice(url.value, ended)) openTelegram(url.value)
}

onMounted(() => {
  if (!closed.value) open()
})
onBeforeUnmount(() => {
  gone = true
  stopAsking()
})
</script>

<template>
  <section class="card pay">
    <h2>{{ tr('stars.title') }}</h2>
    <p v-if="intent.status === 'succeeded'" class="ok" role="status">{{ tr('stars.paid') }}</p>
    <p v-else-if="closed" class="error" role="alert">{{ tr('pay.failed') }}</p>
    <template v-else>
      <p>{{ tr('stars.price', { stars: fmtNum(stars), amount: price }) }}</p>
      <p v-if="phase === 'confirming'" class="muted" role="status">{{ tr('stars.confirming') }}</p>
      <p v-else-if="phase === 'cancelled'" class="muted" role="status">{{ tr('stars.cancelled') }}</p>
      <p v-else-if="phase === 'failed'" class="error" role="alert">{{ tr('stars.failed') }}</p>
      <p v-else-if="phase === 'opened'" class="muted" role="status">{{ tr('stars.opened') }}</p>
      <button class="btn" type="button" :disabled="phase === 'confirming'" @click="open">{{ tr('stars.pay', { stars: fmtNum(stars) }) }}</button>
    </template>
    <button class="btn secondary" type="button" :disabled="checking" @click="emit('check')">
      {{ checking ? tr('pay.checking') : tr('pay.paid_check') }}
    </button>
    <p v-if="message" class="muted" role="status">{{ message }}</p>
  </section>
</template>
