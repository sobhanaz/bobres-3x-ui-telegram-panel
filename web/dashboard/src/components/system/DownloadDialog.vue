<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import { bytes, isolate, type Lang } from '../../format'

// Downloading a backup: the file is the store's data, encrypted. Said plainly here: the key
// that opens it is never sent with it (it stays on the server), the download is recorded and
// every owner is told on Telegram, and an owner with a password login and an authenticator
// app gives a current code.
const props = defineProps<{
  file: string
  size: number
  needsCode: boolean
  busy: boolean
  error: string
  detail: string
}>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ confirm: [payload: { code: string }] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const code = ref('')
watch(visible, (v) => {
  if (v) code.value = ''
})
const ready = computed(() => !props.needsCode || /^\d{6}$/.test(code.value.trim()))
function submit() {
  if (!ready.value || props.busy) return
  emit('confirm', { code: code.value.trim() })
}
</script>

<template>
  <Dialog
    v-model:visible="visible"
    :header="t('system.backups.dl.title')"
    modal
    :draggable="false"
    :closable="!busy"
    :style="{ inlineSize: 'min(34rem, 94vw)' }"
    class="download-confirm"
  >
    <form class="body" @submit.prevent="submit">
      <div class="what">
        <span class="app-ltr app-mono name">{{ isolate(file) }}</span>
        <span class="app-muted size">{{ bytes(size, lang) }}</span>
      </div>
      <ul class="points">
        <li><i class="pi pi-lock" aria-hidden="true" /><span class="encrypted">{{ t('system.backups.dl.encrypted') }}</span></li>
        <li><i class="pi pi-key" aria-hidden="true" /><span class="key">{{ t('system.backups.dl.key') }}</span></li>
        <li><i class="pi pi-exclamation-triangle" aria-hidden="true" /><span class="keep">{{ t('system.backups.dl.keep') }}</span></li>
        <li><i class="pi pi-send" aria-hidden="true" /><span class="audited">{{ t('system.backups.dl.audited') }}</span></li>
      </ul>
      <label v-if="needsCode" class="field">
        <span>{{ t('account.code_label') }}</span>
        <InputText v-model="code" inputmode="numeric" autocomplete="one-time-code" dir="ltr" maxlength="6" class="code" />
        <small class="app-muted">{{ t('account.code_hint') }}</small>
      </label>
      <Message v-if="error" severity="error" :closable="false" class="error">
        {{ error }}
        <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
      </Message>
    </form>
    <template #footer>
      <Button :label="t('app.cancel')" severity="secondary" outlined :disabled="busy" class="cancel" @click="visible = false" />
      <Button :label="t('system.backups.dl.submit')" icon="pi pi-download" :loading="busy" :disabled="!ready" class="submit" @click="submit" />
    </template>
  </Dialog>
</template>

<style scoped>
.body {
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
}
.what {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.25rem 0.75rem;
}
.name {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.points {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.7rem;
}
.points li {
  display: flex;
  align-items: flex-start;
  gap: 0.7rem;
}
.points .pi {
  margin-block-start: 0.2rem;
  color: var(--p-text-muted-color);
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.code {
  max-inline-size: 9rem;
  letter-spacing: 0.25em;
  text-align: center;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
</style>
