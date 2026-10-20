<script setup lang="ts">
// A value the customer has to copy into their bank or wallet app (a card number, a USDT address, an amount, a payment
// reference): shown whole, left to right, with a button that copies it.
import { ref } from 'vue'
import { tr } from '../i18n'
import { isolate } from '../format'
import { copyText } from '../telegram'

const props = withDefaults(
  defineProps<{
    label: string
    value: string
    /** what is copied when it is not the value as shown (a card number without its dashes) */
    copy?: string
    /** a code (a card, an address, a reference) is shown whole, left to right, in a fixed-width font; an amount reads as text */
    code?: boolean
  }>(),
  { code: true },
)

const copied = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

async function copy() {
  try {
    await copyText(props.copy ?? props.value)
    copied.value = true
    clearTimeout(timer)
    timer = setTimeout(() => (copied.value = false), 1500)
  } catch {
    // no clipboard: the value stays selectable
  }
}
</script>

<template>
  <div class="copy-field">
    <span class="muted small">{{ label }}</span>
    <span class="row between">
      <span v-if="code" class="mono" dir="ltr">{{ isolate(value) }}</span>
      <strong v-else>{{ value }}</strong>
      <button class="btn secondary compact" type="button" @click="copy">{{ copied ? tr('common.copied') : tr('common.copy') }}</button>
    </span>
  </div>
</template>

<style scoped>
.copy-field { display: grid; gap: 0.2rem; }
.compact { min-height: 2.25rem; padding: 0.25rem 0.75rem; flex: none; }
/* a long address wraps instead of pushing the button off the screen */
.mono, strong { min-width: 0; }
</style>
