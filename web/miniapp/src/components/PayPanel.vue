<script setup lang="ts">
// What the customer does to pay once a payment is started: open the gateway's page, or send USDT to an address (then
// "I have paid"); open a Telegram Stars invoice; or pay by hand and send the proof (card to card, USDT, a Zarinpal link).
import { computed, ref } from 'vue'
import type { Intent } from '../api'
import { tr } from '../i18n'
import { fmtDateTime, fmtExact } from '../fmt'
import { copyText, openLink } from '../telegram'
import { isolate } from '../format'
import { isManual } from '../pay'
import ManualPay from './ManualPay.vue'
import StarsPay from './StarsPay.vue'

const props = withDefaults(defineProps<{ intent: Intent; checking: boolean; message: string; showLeave?: boolean }>(), { showLeave: true })
// check: the customer says they paid (quiet: nobody asked, so say nothing if it is not seen yet); leave: they are done here
// (a screen that has its own way out says showLeave is false).
defineEmits<{ check: [quiet?: boolean]; leave: [] }>()

const copied = ref(false)
const address = computed(() => props.intent.details.pay_address ?? '')
const expires = computed(() => Number(props.intent.details.expires_at ?? 0))
// Every payment to the address has an amount of its own, down to the last micro-USDT (that is how it is told from the
// others), so it is shown whole: "2.500731", never "2.5".
const charged = computed(() =>
  props.intent.gateway_currency === 'USDT' ? fmtExact(props.intent.gateway_amount, 'USDT', props.intent.scale_gateway) : '',
)

async function copy() {
  try {
    await copyText(address.value)
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    // no clipboard: the address stays selectable
  }
}
</script>

<template>
  <StarsPay v-if="intent.provider === 'stars'" :intent="intent" :checking="checking" :message="message" @check="(quiet) => $emit('check', quiet)" />
  <ManualPay
    v-else-if="isManual(intent.provider)"
    :intent="intent"
    :checking="checking"
    :message="message"
    :show-leave="showLeave"
    @check="(quiet) => $emit('check', quiet)"
    @leave="$emit('leave')"
  />
  <section v-else class="card pay">
    <p class="muted">{{ tr('pay.waiting') }}</p>
    <template v-if="address">
      <p>{{ tr('pay.usdt_send', { amount: charged }) }}</p>
      <p class="mono" dir="ltr">{{ isolate(address) }}</p>
      <p class="muted">
        {{ tr('pay.usdt_network', { network: intent.details.network ?? 'TRC20' }) }}
        <template v-if="expires"> · {{ tr('pay.usdt_until', { time: fmtDateTime(expires) }) }}</template>
      </p>
      <button class="btn secondary" type="button" @click="copy">{{ copied ? tr('common.copied') : tr('common.copy') }}</button>
    </template>
    <button v-else-if="intent.pay_url" class="btn" type="button" @click="openLink(intent.pay_url)">{{ tr('pay.open') }}</button>
    <button class="btn secondary" type="button" :disabled="checking" @click="$emit('check')">
      {{ checking ? tr('pay.checking') : tr('pay.paid_check') }}
    </button>
    <p v-if="message" class="muted" role="status">{{ message }}</p>
  </section>
</template>
