<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Checkbox from 'primevue/checkbox'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import { bytes, dateTime, isolate, type Lang } from '../../format'
import { confirmMatches } from '../../restore'

// The last step before a restore, which replaces ALL the store's data with a backup's:
// what is lost (everything after the backup's time) and that the dashboard and the bot
// are away meanwhile, that a backup of the current data comes first and the restore goes
// back on its own when the restored store is not healthy, and that every owner is told.
// The owner types the store's domain, ticks that they understand, and (with a password
// login and an authenticator app) gives a current code, as for replacing the password.
const props = defineProps<{
  /** the backup: its file name, or the name an upload had */
  subject: string
  source: 'backup' | 'upload'
  size: number | null
  /** when the backup was made (unix seconds), when the list says */
  madeAt: number | null
  /** what the owner has to type */
  domain: string
  /** an authenticator code is needed (the server said so, or /me says it will) */
  needsCode: boolean
  busy: boolean
  error: string
  detail: string
}>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ confirm: [payload: { confirm: string; acknowledge: boolean; code: string }] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const typed = ref('')
const acknowledged = ref(false)
const code = ref('')
watch(visible, (v) => {
  if (v) {
    typed.value = ''
    acknowledged.value = false
    code.value = ''
  }
})
const typedOk = computed(() => confirmMatches(typed.value, props.domain))
const codeOk = computed(() => !props.needsCode || /^\d{6}$/.test(code.value.trim()))
const ready = computed(() => typedOk.value && acknowledged.value && codeOk.value)
function submit() {
  if (!ready.value || props.busy) return
  emit('confirm', { confirm: typed.value.trim(), acknowledge: acknowledged.value, code: code.value.trim() })
}
</script>

<template>
  <Dialog
    v-model:visible="visible"
    :header="t('system.backups.rs.title', { name: isolate(subject) })"
    modal
    :draggable="false"
    :closable="!busy"
    :style="{ inlineSize: 'min(36rem, 94vw)' }"
    class="restore-confirm"
  >
    <form class="body" @submit.prevent="submit">
      <div class="what">
        <span class="app-ltr app-mono name">{{ subject }}</span>
        <span v-if="size" class="app-muted size">{{ bytes(size, lang) }}</span>
        <span v-if="madeAt" class="app-muted made">{{ t('system.backups.rs.made', { time: dateTime(madeAt, lang) }) }}</span>
        <span class="app-muted from">{{ t('system.backups.rs.source.' + source) }}</span>
      </div>

      <Message severity="error" icon="pi pi-exclamation-triangle" :closable="false" class="lost">
        <div class="title">{{ t('system.backups.rs.lost_title') }}</div>
        <p class="line">{{ t('system.backups.rs.lost') }}</p>
      </Message>

      <ul class="points">
        <li><i class="pi pi-clock" aria-hidden="true" /><span class="downtime">{{ t('system.backups.rs.downtime') }}</span></li>
        <li><i class="pi pi-shield" aria-hidden="true" /><span class="safe">{{ t('system.backups.rs.safe') }}</span></li>
        <li><i class="pi pi-key" aria-hidden="true" /><span class="key">{{ t('system.backups.rs.key') }}</span></li>
        <li><i class="pi pi-send" aria-hidden="true" /><span class="owners">{{ t('system.backups.rs.owners') }}</span></li>
      </ul>

      <label class="check">
        <Checkbox v-model="acknowledged" binary input-id="restore-ack" class="ack" />
        <span>{{ t('system.backups.rs.ack') }}</span>
      </label>

      <label class="field">
        <span>{{ t('system.backups.rs.type_domain') }} <strong class="app-ltr app-mono domain">{{ domain }}</strong></span>
        <InputText v-model="typed" dir="ltr" autocomplete="off" autocapitalize="off" spellcheck="false" :invalid="typed !== '' && !typedOk" class="typed" />
      </label>

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
      <Button :label="t('system.backups.rs.submit')" icon="pi pi-replay" severity="danger" :loading="busy" :disabled="!ready" class="submit" @click="submit" />
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
.title {
  font-weight: 600;
}
.line {
  margin-block: 0.35rem 0;
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
.check {
  display: flex;
  align-items: flex-start;
  gap: 0.6rem;
  cursor: pointer;
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
