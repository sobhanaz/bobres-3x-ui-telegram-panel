<script setup lang="ts">
// What the customer does to pay once a payment is started: open the gateway's page, or send USDT to an address (then
// "I have paid"); open a Telegram Stars invoice; or pay by hand and send the proof (card to card, USDT, a Zarinpal link).
import { computed, ref, watch } from 'vue'
import type { Intent } from '../api'
import { tr } from '../i18n'
import { fmtClock, fmtDateTime, fmtExact } from '../fmt'
import { copyText, openLink } from '../telegram'
import { isolate } from '../format'
import { isManual } from '../pay'
import { useCountdown, wall } from '../countdown'
import ManualPay from './ManualPay.vue'
import StarsPay from './StarsPay.vue'

const props = withDefaults(defineProps<{ intent: Intent; checking: boolean; message: string; showLeave?: boolean; reopened?: boolean }>(), {
  showLeave: true,
  reopened: false,
})
// check: the customer says they paid (quiet: nobody asked, so say nothing if it is not seen yet); leave: they are done here
// (a screen that has its own way out says showLeave is false); expired: the time of an automatic USDT payment ran out (the
// screen around stops asking whether it was paid).
const emit = defineEmits<{ check: [quiet?: boolean]; leave: []; expired: [] }>()

const copied = ref(false)
const address = computed(() => props.intent.details.pay_address ?? '')
const expires = computed(() => Number(props.intent.details.expires_at ?? 0))
// Every payment to the address has an amount of its own, down to the last micro-USDT (that is how it is told from the
// others), so it is shown whole: "2.500731", never "2.5".
const charged = computed(() =>
  props.intent.gateway_currency === 'USDT' ? fmtExact(props.intent.gateway_amount, 'USDT', props.intent.scale_gateway) : '',
)
// The address as a QR code, which core makes: only a PNG it sent as a data address is drawn (the page's policy allows no more).
const qr = computed(() => (/^data:image\/png;base64,[A-Za-z0-9+/]+=*$/.test(props.intent.qr ?? '') ? (props.intent.qr ?? '') : ''))

// The time left is counted to the moment core named, by the phone's clock (the one place a date is trusted: a deadline is a
// moment, not a length of time). A moment already past when the screen opens is taken as a wrong clock, not as a payment that
// ran out: the address stays, with its "valid until", as before the countdown. Once it runs out nothing is shown to pay to.
const over = ref(false)
const countdown = useCountdown(() => {
  over.value = true
  emit('expired')
}, wall)
watch(
  [address, expires],
  ([a, until]) => {
    over.value = false
    countdown.stop()
    if (a && until > wall()) countdown.start(until)
  },
  { immediate: true },
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
    :reopened="reopened"
    @check="(quiet) => $emit('check', quiet)"
    @leave="$emit('leave')"
  />
  <section v-else class="card pay">
    <p v-if="!over" class="muted">{{ tr('pay.waiting') }}</p>
    <template v-if="address">
      <p v-if="over" class="error" role="alert">{{ tr('pay.usdt_over') }}</p>
      <template v-else>
        <p>{{ tr('pay.usdt_send', { amount: charged }) }}</p>
        <img v-if="qr" class="qr" :src="qr" :alt="tr('pay.usdt_qr')" width="176" height="176" />
        <p class="mono" dir="ltr">{{ isolate(address) }}</p>
        <p class="muted">
          {{ tr('pay.usdt_network', { network: intent.details.network ?? 'TRC20' }) }}
          <template v-if="countdown.running.value">
            · {{ tr('pay.usdt_left') }} <span class="clock" dir="ltr" role="timer">{{ fmtClock(countdown.left.value) }}</span>
          </template>
          <template v-else-if="expires"> · {{ tr('pay.usdt_until', { time: fmtDateTime(expires) }) }}</template>
        </p>
        <button class="btn secondary" type="button" @click="copy">{{ copied ? tr('common.copied') : tr('common.copy') }}</button>
      </template>
    </template>
    <button v-else-if="intent.pay_url" class="btn" type="button" @click="openLink(intent.pay_url)">{{ tr('pay.open') }}</button>
    <button class="btn secondary" type="button" :disabled="checking" @click="$emit('check')">
      {{ checking ? tr('pay.checking') : tr('pay.paid_check') }}
    </button>
    <p v-if="message" class="muted" role="status">{{ message }}</p>
  </section>
</template>

<style scoped>
/* the QR code sits on white with a margin of its own, which scanners need, whatever the theme */
.qr { display: block; inline-size: 176px; block-size: 176px; margin-inline: auto; padding: 8px; box-sizing: content-box; background: #fff; border-radius: 0.5rem; }
.clock { font-variant-numeric: tabular-nums; }
</style>
