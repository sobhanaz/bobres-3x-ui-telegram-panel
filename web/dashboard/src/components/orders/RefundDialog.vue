<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Textarea from 'primevue/textarea'
import Message from 'primevue/message'
import ReasonDialog from '../ReasonDialog.vue'
import { api } from '../../api'
import { errorText } from '../../errors'
import { isolate, money, num, type Lang } from '../../format'
import { NOTE_MAX } from '../../undelivered'
import type { OrderItem, RefundResult } from '../../types'

// Refund to wallet (POST /orders/{id}/refund {reason, note}): what the buyer paid goes to their wallet. An order
// not delivered yet is cancelled too, and the commission a reseller earned on it is taken back. The note, when
// written, goes to the buyer with the message about the refund; the reason stays in the audit log.
const props = defineProps<{ order: OrderItem | null }>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ refunded: [res: RefundResult] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const noteId = useId()

const note = ref('')
const busy = ref(false)
const error = ref('')
const detail = ref('')
watch(visible, (v) => {
  if (!v) return
  note.value = ''
  error.value = ''
  detail.value = ''
})
// core counts characters (runes), not UTF-16 units
const noteLength = computed(() => [...note.value.trim()].length)
const delivered = computed(() => props.order?.status === 'active')
const referrer = computed(() => (props.order?.seller?.kind === 'referrer' ? props.order.seller.user : null))
const who = (u: { username: string; telegram_id: number }) => isolate(u.username ? '@' + u.username : String(u.telegram_id))

async function confirm(reason: string) {
  if (!props.order) return
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    const res = await api<RefundResult>('POST', `/orders/${props.order.id}/refund`, { reason, note: note.value.trim() })
    visible.value = false
    const taken = res.commission_taken && res.commission_taken.amount > 0 ? t('order.refund_commission_taken', { amount: money(res.commission_taken, lang.value) }) : ''
    const lines = [taken, res.warning ?? ''].filter(Boolean).join(' ')
    toast.add({
      severity: res.warning ? 'warn' : 'success',
      summary: t('order.refund_done', { balance: money(res.wallet, lang.value) }),
      detail: lines || undefined,
      // a warning (a half-made client to remove by hand) stays until it is closed
      life: res.warning ? undefined : 7000,
    })
    emit('refunded', res)
  } catch (e) {
    const err = errorText(e, t)
    error.value = err.text
    detail.value = err.detail
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <ReasonDialog
    v-model:visible="visible"
    :title="t('order.refund')"
    :message="order ? t(delivered ? 'order.refund_intro_delivered' : 'order.refund_intro', { amount: money(order.amount, lang) }) : ''"
    :confirm-label="t('order.refund')"
    danger
    :busy="busy"
    :error="error"
    :detail="detail"
    :can-confirm="noteLength <= NOTE_MAX"
    @confirm="confirm"
  >
    <Message v-if="order && !delivered" severity="info" :closable="false" class="commission">
      {{ referrer ? t('order.refund_commission_named', { who: who(referrer) }) : t('order.refund_commission') }}
    </Message>
    <div class="field">
      <div class="head">
        <label :for="noteId">{{ t('order.refund_note') }}</label>
        <span class="counter" :class="{ over: noteLength > NOTE_MAX }" :aria-label="t('texts.length')">{{ num(noteLength, lang) }} / {{ num(NOTE_MAX, lang) }}</span>
      </div>
      <Textarea :id="noteId" v-model="note" rows="2" auto-resize :maxlength="NOTE_MAX" dir="auto" fluid class="note" />
      <small class="app-muted">{{ t('order.refund_note_hint') }}</small>
    </div>
  </ReasonDialog>
</template>

<style scoped>
.commission {
  margin-block-end: 0.75rem;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.head {
  display: flex;
  align-items: baseline;
  gap: 0.5rem;
}
.counter {
  margin-inline-start: auto;
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
  font-variant-numeric: tabular-nums;
}
.counter.over {
  color: var(--p-message-error-simple-color, var(--p-red-600));
  font-weight: 700;
}
</style>
