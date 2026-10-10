<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave } from 'vue-router'
import { useConfirm } from 'primevue/useconfirm'
import Textarea from 'primevue/textarea'
import Button from 'primevue/button'
import Select from 'primevue/select'
import Message from 'primevue/message'
import { api } from '../../api'
import { errorText } from '../../errors'
import { insertAt } from '../../branding'
import { num, type Lang } from '../../format'
import { REPLY_MAX, cannedBody, plainLength, replyOk, requestKeys } from '../../support'
import type { CannedReply, TicketItem, TicketMessageItem } from '../../types'

// The reply box: a reply to the customer (sent by the bot as the store, never
// with a staff name) or an internal note. Every send carries a request key,
// reused when the same send is retried, so the customer gets it once.
const props = defineProps<{ ticketId: string; customerLang: string }>()
const emit = defineEmits<{ sent: [res: { ticket: TicketItem; message: TicketMessageItem }] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const confirm = useConfirm()
const uid = useId()
const areaId = `${uid}-reply`

const draft = ref('')
const length = computed(() => plainLength(draft.value))
const over = computed(() => length.value > REPLY_MAX)
const ok = computed(() => replyOk(draft.value))

// Saved replies to insert (a failure just hides the picker).
const canned = ref<CannedReply[]>([])
onMounted(async () => {
  try {
    canned.value = (await api<{ items: CannedReply[] }>('GET', '/canned-replies')).items
  } catch {
    canned.value = []
  }
})
// The picker starts empty again after each insert (a new one replaces it:
// PrimeVue keeps its own copy of the last choice).
const pickerKey = ref(0)
async function insertCanned(c: CannedReply | null) {
  if (!c) return
  pickerKey.value++
  const text = cannedBody(c, props.customerLang)
  if (!text) return
  const el = document.getElementById(areaId) as HTMLTextAreaElement | null
  const start = el?.selectionStart ?? draft.value.length
  const r = insertAt(draft.value, start, el?.selectionEnd ?? start, text)
  draft.value = r.value
  await nextTick()
  el?.focus()
  el?.setSelectionRange(r.cursor, r.cursor)
}

type Kind = 'reply' | 'note'
const busy = ref<'' | 'reply' | 'close' | 'note'>('')
const error = ref('')
const detail = ref('')
const keys = requestKeys()

async function send(kind: Kind, close: boolean) {
  if (busy.value || !ok.value) return
  const body = draft.value.replace(/\r\n?/g, '\n').trim()
  const key = keys.key(JSON.stringify([kind, close, body]))
  busy.value = kind === 'note' ? 'note' : close ? 'close' : 'reply'
  error.value = ''
  detail.value = ''
  try {
    const res = await api<{ ticket: TicketItem; message: TicketMessageItem }>('POST', `/tickets/${props.ticketId}/messages`, {
      body,
      kind,
      close,
      key,
    })
    keys.done()
    draft.value = ''
    emit('sent', res)
  } catch (e) {
    const err = errorText(e, t)
    error.value = err.text
    detail.value = err.detail
  } finally {
    busy.value = ''
  }
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    void send('reply', false)
  }
}

// Leaving with an unsent reply asks first.
onBeforeRouteLeave(() => {
  if (!draft.value.trim()) return true
  return new Promise<boolean>((resolve) => {
    confirm.require({
      header: t('support.discard_title'),
      message: t('support.discard_message'),
      acceptProps: { label: t('support.discard'), severity: 'danger' },
      rejectProps: { label: t('support.keep_writing'), severity: 'secondary', outlined: true },
      accept: () => resolve(true),
      reject: () => resolve(false),
      onHide: () => resolve(false),
    })
  })
})
</script>

<template>
  <section class="composer" :aria-label="t('support.composer')">
    <div class="head">
      <label :for="areaId" class="label">{{ t('support.reply_label') }}</label>
      <span class="counter" :class="{ over }" :aria-label="t('support.length')">{{ num(length, lang) }} / {{ num(REPLY_MAX, lang) }}</span>
    </div>
    <Textarea
      :id="areaId"
      v-model="draft"
      dir="auto"
      :lang="customerLang || undefined"
      rows="3"
      auto-resize
      fluid
      :invalid="over"
      :placeholder="t('support.reply_placeholder')"
      class="area"
      @keydown="onKey"
    />
    <p class="app-muted hint">{{ t('support.reply_hint') }}</p>
    <Message v-if="error" severity="error" :closable="false" class="msg">
      {{ error }}
      <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
    </Message>
    <div class="bar">
      <Select
        v-if="canned.length"
        :key="pickerKey"
        :model-value="null"
        :options="canned"
        option-label="title"
        filter
        :placeholder="t('support.insert_canned')"
        :aria-label="t('support.insert_canned')"
        class="canned"
        @update:model-value="insertCanned"
      >
        <template #option="{ option }"><bdi>{{ option.title }}</bdi></template>
      </Select>
      <span class="spacer" />
      <Button
        :label="t('support.add_note')"
        icon="pi pi-lock"
        severity="secondary"
        outlined
        :disabled="!ok || (!!busy && busy !== 'note')"
        :loading="busy === 'note'"
        class="note-btn"
        @click="send('note', false)"
      />
      <Button
        :label="t('support.send_close')"
        icon="pi pi-check-circle"
        outlined
        :disabled="!ok || (!!busy && busy !== 'close')"
        :loading="busy === 'close'"
        class="close-btn"
        @click="send('reply', true)"
      />
      <Button
        :label="t('support.send')"
        icon="pi pi-send"
        :disabled="!ok || (!!busy && busy !== 'reply')"
        :loading="busy === 'reply'"
        class="send-btn"
        @click="send('reply', false)"
      />
    </div>
  </section>
</template>

<style scoped>
.composer {
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
}
.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}
.label {
  font-weight: 700;
}
.counter {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
  font-variant-numeric: tabular-nums;
}
.counter.over {
  color: var(--p-red-500);
  font-weight: 700;
}
.area {
  max-block-size: 40vh;
  overflow-y: auto;
}
.hint {
  margin: 0;
  font-size: 0.8rem;
}
.msg {
  margin: 0;
}
.detail {
  font-size: 0.85rem;
}
.bar {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  align-items: center;
}
.spacer {
  flex: 1 1 0;
}
.canned {
  min-inline-size: 0;
  inline-size: min(16rem, 100%);
}
[dir='rtl'] .send-btn :deep(.pi-send) {
  scale: -1 1;
}
@media (max-width: 575px) {
  .bar > :deep(.p-button) {
    flex: 1 1 auto;
  }
  .spacer {
    display: none;
  }
  .canned {
    inline-size: 100%;
  }
}
</style>
