<script setup lang="ts">
// Choosing the receipt image: a photo or a screenshot, shown back to the customer before it is sent. It is checked by
// what it is (a JPEG, PNG or WebP) and made smaller when it is big, in receipt.ts; the file's own name is never sent.
import { computed, ref } from 'vue'
import { tr } from '../i18n'
import { fmtBytes, fmtNum } from '../fmt'
import { prepareReceipt, ReceiptError, type PreparedReceipt } from '../receipt'

const receipt = defineModel<PreparedReceipt | null>({ default: null })
const input = ref<HTMLInputElement | null>(null)
const busy = ref(false)
const problem = ref('')

const PROBLEMS = { type: 'err.bad_type', empty: 'err.empty', size: 'err.too_large' } as const

// a screenshot is a few hundred kilobytes: shown in KB below a megabyte
const size = computed(() => {
  const n = receipt.value?.size ?? 0
  return n < 1024 * 1024 ? tr('manual.kb', { n: fmtNum(Math.max(1, Math.round(n / 1024))) }) : fmtBytes(n)
})

async function onPick(e: Event) {
  const el = e.target as HTMLInputElement
  const file = el.files?.[0]
  el.value = '' // choosing the same file again must be a change too
  if (!file) return
  problem.value = ''
  busy.value = true
  try {
    receipt.value = await prepareReceipt(file)
  } catch (err) {
    receipt.value = null
    problem.value = err instanceof ReceiptError ? PROBLEMS[err.problem] : 'err.unknown'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="picker">
    <input ref="input" class="pick-input" type="file" accept="image/jpeg,image/png,image/webp" tabindex="-1" aria-hidden="true" @change="onPick" />
    <button class="btn secondary" type="button" :disabled="busy" @click="input?.click()">
      {{ receipt ? tr('manual.change') : tr('manual.pick') }}
    </button>
    <p v-if="busy" class="muted small" role="status">{{ tr('manual.preparing') }}</p>
    <p v-if="problem" class="error" role="alert">{{ tr(problem) }}</p>
    <figure v-if="receipt" class="preview">
      <img v-if="receipt.preview" :src="receipt.preview" :alt="tr('manual.preview_alt')" />
      <figcaption class="muted small">{{ receipt.name }} · {{ size }}</figcaption>
    </figure>
  </div>
</template>

<style scoped>
.picker { display: grid; gap: 0.5rem; }
.pick-input { position: absolute; width: 1px; height: 1px; opacity: 0; pointer-events: none; }
.preview { margin: 0; display: grid; gap: 0.25rem; justify-items: center; }
.preview img { max-width: 100%; max-height: 14rem; border-radius: 0.5rem; object-fit: contain; }
</style>
