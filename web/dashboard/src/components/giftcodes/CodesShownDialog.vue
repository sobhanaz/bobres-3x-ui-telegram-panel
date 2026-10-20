<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import Textarea from 'primevue/textarea'
import Message from 'primevue/message'
import { copyText, download } from '../../csv'
import { num, type Lang } from '../../format'
import { codesFile, type Terms } from '../../giftcodes'

// The codes just made, shown once: core keeps a keyed hash of each, never the
// code, so this is the only time anybody can read them. Closing without having
// copied or downloaded them asks first.
const props = defineProps<{ codes: string[]; terms: Terms | null }>()
const visible = defineModel<boolean>('visible', { required: true })
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()

const saved = ref(false)
watch(visible, (v) => {
  if (v) saved.value = false
})

const text = computed(() => props.codes.join('\n'))
const rows = computed(() => Math.min(Math.max(props.codes.length, 1), 12))

async function copy() {
  if (await copyText(text.value)) {
    saved.value = true
    toast.add({ severity: 'success', summary: t('giftcodes.copied'), life: 2500 })
  } else {
    toast.add({ severity: 'warn', summary: t('giftcodes.copy_failed'), life: 6000 })
  }
}

function save() {
  if (!props.terms) return
  const stamp = new Date().toISOString().slice(0, 16).replace(/[-:T]/g, '')
  download(`gift-codes-${stamp}.csv`, codesFile(props.codes, props.terms))
  saved.value = true
}

function done() {
  if (saved.value) {
    visible.value = false
    return
  }
  confirm.require({
    header: t('giftcodes.unsaved_title'),
    message: t('giftcodes.unsaved_message'),
    icon: 'pi pi-exclamation-triangle',
    acceptProps: { label: t('giftcodes.close_anyway'), severity: 'danger' },
    rejectProps: { label: t('giftcodes.keep_open'), severity: 'secondary', outlined: true },
    accept: () => (visible.value = false),
  })
}
</script>

<template>
  <Dialog
    v-model:visible="visible"
    :header="t('giftcodes.shown_title', { n: num(codes.length, lang) }, codes.length)"
    modal
    :closable="false"
    :close-on-escape="false"
    :draggable="false"
    :style="{ inlineSize: 'min(34rem, 94vw)' }"
    class="shown-dialog"
  >
    <Message severity="warn" :closable="false" class="once">{{ t('giftcodes.shown_warning') }}</Message>
    <Textarea
      :model-value="text"
      readonly
      :rows="rows"
      fluid
      dir="ltr"
      class="app-mono codes-text"
      :aria-label="t('giftcodes.shown_codes')"
      @focus="($event.target as HTMLTextAreaElement).select()"
    />
    <p class="app-muted hint">{{ t('giftcodes.shown_hint') }}</p>
    <template #footer>
      <Button type="button" class="copy" icon="pi pi-copy" :label="t('giftcodes.copy_all')" severity="secondary" outlined @click="copy" />
      <Button type="button" class="download" icon="pi pi-download" :label="t('giftcodes.download')" severity="secondary" outlined :disabled="!terms" @click="save" />
      <Button type="button" class="done" :label="t('giftcodes.done')" @click="done" />
    </template>
  </Dialog>
</template>

<style scoped>
.once {
  margin-block-start: 0;
}
.codes-text {
  margin-block-start: 0.75rem;
  font-size: 1.05rem;
  letter-spacing: 0.04em;
  line-height: 1.7;
}
.hint {
  margin-block: 0.5rem 0;
  font-size: 0.85rem;
}
</style>
