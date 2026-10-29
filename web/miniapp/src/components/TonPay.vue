<script setup lang="ts">
// An automatic TON payment: the customer sends the exact amount of TON, from any wallet, to the store's address and writes the
// payment's comment (its memo) in the transfer. The comment is how the payment is found, so it is shown as large as the
// amount, with a warning; both and the address can be copied. A countdown runs to the deadline; a payment made after it
// is still looked at by the store, which the screen says once the time is over.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { Intent } from '../api'
import { tr } from '../i18n'
import { isolate } from '../format'
import { countdown, fixedAmount } from '../pay'
import CopyField from './CopyField.vue'

const props = defineProps<{ intent: Intent; checking: boolean; message: string }>()
defineEmits<{ check: [quiet?: boolean] }>()

const d = computed(() => props.intent.details)
const address = computed(() => d.value.pay_address ?? '')
const memo = computed(() => d.value.memo ?? '')
// Never rounded: the amount has all nine decimals of a nanoton, as the payment was worked out, in Latin digits.
const plain = computed(() => fixedAmount(props.intent.gateway_amount, props.intent.scale_gateway > 0 ? props.intent.scale_gateway : 9))
const expires = computed(() => Number(d.value.expires_at ?? 0))

// The time left, to the second, by the device's clock; it stops at zero.
const now = ref(Date.now())
const left = computed(() => (expires.value > 0 ? Math.max(0, Math.ceil(expires.value - now.value / 1000)) : 0))
const over = computed(() => expires.value > 0 && left.value === 0)
let timer: ReturnType<typeof setInterval> | undefined
function stop() {
  clearInterval(timer)
  timer = undefined
}
onMounted(() => {
  if (expires.value <= 0 || left.value === 0) return
  timer = setInterval(() => {
    now.value = Date.now()
    if (left.value === 0) stop()
  }, 1000)
})
onBeforeUnmount(stop)
</script>

<template>
  <section class="card pay ton-pay">
    <p class="muted">{{ tr('pay.waiting') }}</p>
    <template v-if="address">
      <p>{{ tr('pay.ton_intro') }}</p>
      <CopyField class="ton-amount" :label="tr('pay.ton_amount')" :value="plain + ' TON'" :copy="plain" />
      <CopyField class="ton-address" :label="tr('pay.ton_address')" :value="address" />
      <CopyField v-if="memo" class="ton-memo" :label="tr('pay.ton_memo')" :value="memo" />
      <p v-if="memo" class="warn" role="note">{{ tr('pay.ton_memo_warning') }}</p>
      <p class="muted">
        {{ tr('pay.usdt_network', { network: d.network ?? 'TON' }) }}
        <template v-if="expires > 0 && !over"> · <span class="ton-left">{{ tr('pay.ton_time_left', { time: isolate(countdown(left)) }) }}</span></template>
      </p>
      <p v-if="over" class="error ton-over" role="status">{{ tr('pay.ton_expired') }}</p>
    </template>
    <button class="btn secondary" type="button" :disabled="checking" @click="$emit('check')">
      {{ checking ? tr('pay.checking') : tr('pay.paid_check') }}
    </button>
    <p v-if="message" class="muted" role="status">{{ message }}</p>
  </section>
</template>

<style scoped>
/* the comment is what finds the payment: the warning is not small print */
.warn { font-weight: 600; border-inline-start: 3px solid var(--danger); padding-inline-start: 0.6rem; }
</style>
