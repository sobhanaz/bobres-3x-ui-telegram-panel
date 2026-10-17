<script setup lang="ts">
// What the customer does to pay once an online payment is started: open the
// gateway's page, or send USDT to an address; then "I have paid".
import { computed, ref } from 'vue'
import type { Intent } from '../api'
import { tr } from '../i18n'
import { fmtMoney, fmtDateTime } from '../fmt'
import { copyText, openLink } from '../telegram'
import { isolate } from '../format'

const props = defineProps<{ intent: Intent; checking: boolean; message: string }>()
defineEmits<{ check: [] }>()

const copied = ref(false)
const address = computed(() => props.intent.details.pay_address ?? '')
const expires = computed(() => Number(props.intent.details.expires_at ?? 0))
const charged = computed(() =>
  props.intent.gateway_currency === 'USDT' ? fmtMoney(props.intent.gateway_amount, 'USDT', props.intent.scale_gateway) : '',
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
  <section class="card pay">
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
