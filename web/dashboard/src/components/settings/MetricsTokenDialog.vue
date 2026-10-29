<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Textarea from 'primevue/textarea'
import { copyText } from '../../csv'
import { metricsAddress, scrapeConfig } from '../../metrics'

// The metrics token just made, shown once: core keeps only its hash, so this is the only time anybody can read it.
// Closing without having copied it asks first. Under it, the address Prometheus reads and the job to add to its
// configuration (the token goes in a file on the Prometheus server, never in prometheus.yml).
const props = defineProps<{ token: string }>()
const visible = defineModel<boolean>('visible', { required: true })
const { t } = useI18n()
const toast = useToast()
const confirm = useConfirm()

const copied = ref(false)
watch(visible, (v) => {
  if (v) copied.value = false
})

const host = computed(() => window.location.host)
const address = computed(() => metricsAddress(window.location.origin))
const config = computed(() => scrapeConfig(host.value))

async function copy(text: string, ok: string, mark: boolean) {
  if (await copyText(text)) {
    if (mark) copied.value = true
    toast.add({ severity: 'success', summary: ok, life: 2500 })
  } else {
    toast.add({ severity: 'warn', summary: t('prometheus.copy_failed'), life: 6000 })
  }
}

function done() {
  if (copied.value) {
    visible.value = false
    return
  }
  confirm.require({
    header: t('prometheus.unsaved_title'),
    message: t('prometheus.unsaved_message'),
    icon: 'pi pi-exclamation-triangle',
    acceptProps: { label: t('prometheus.close_anyway'), severity: 'danger' },
    rejectProps: { label: t('prometheus.keep_open'), severity: 'secondary', outlined: true },
    accept: () => (visible.value = false),
  })
}
</script>

<template>
  <Dialog
    v-model:visible="visible"
    :header="t('prometheus.shown_title')"
    modal
    :closable="false"
    :close-on-escape="false"
    :draggable="false"
    :style="{ inlineSize: 'min(38rem, 94vw)' }"
    class="metrics-token-dialog"
  >
    <Message severity="warn" :closable="false" class="once">{{ t('prometheus.shown_warning') }}</Message>
    <label class="field">
      <span>{{ t('prometheus.token') }}</span>
      <InputText :model-value="props.token" readonly dir="ltr" class="app-mono token" :aria-label="t('prometheus.token')" @focus="($event.target as HTMLInputElement).select()" />
    </label>
    <label class="field">
      <span>{{ t('prometheus.address') }}</span>
      <InputText :model-value="address" readonly dir="ltr" class="app-mono address" :aria-label="t('prometheus.address')" @focus="($event.target as HTMLInputElement).select()" />
    </label>
    <label class="field">
      <span>{{ t('prometheus.scrape') }}</span>
      <Textarea :model-value="config" readonly :rows="11" fluid dir="ltr" class="app-mono config" :aria-label="t('prometheus.scrape')" @focus="($event.target as HTMLTextAreaElement).select()" />
    </label>
    <p class="app-muted hint">{{ t('prometheus.scrape_hint') }}</p>
    <template #footer>
      <Button type="button" class="copy-config" icon="pi pi-copy" :label="t('prometheus.copy_config')" severity="secondary" outlined @click="copy(config, t('prometheus.config_copied'), false)" />
      <Button type="button" class="copy" icon="pi pi-copy" :label="t('prometheus.copy_token')" severity="secondary" outlined @click="copy(props.token, t('prometheus.token_copied'), true)" />
      <Button type="button" class="done" :label="t('prometheus.done')" @click="done" />
    </template>
  </Dialog>
</template>

<style scoped>
.once {
  margin-block-start: 0;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  margin-block-start: 0.75rem;
  font-weight: 600;
}
.token {
  font-size: 1rem;
  letter-spacing: 0.02em;
}
.config {
  font-weight: 400;
  font-size: 0.85rem;
}
.hint {
  margin-block: 0.5rem 0;
  font-size: 0.85rem;
}
</style>
