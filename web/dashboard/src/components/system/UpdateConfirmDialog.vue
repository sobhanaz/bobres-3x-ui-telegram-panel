<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Checkbox from 'primevue/checkbox'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import { isolate } from '../../format'

// The last step before "Update now": what will happen (from which version to
// which, on which channel), that the dashboard and the bot are away for a few
// minutes while customers' VPN links keep working, that a backup comes first and
// the update goes back on its own when the new version does not come up healthy,
// and that every owner is told. A pre-release needs its own yes; an owner with a
// password login and an authenticator app gives a current code (as for
// replacing the password).
const props = defineProps<{
  from: string
  to: string
  channel: string
  prerelease: boolean
  /** an authenticator code is needed (the server said so, or /me says it will) */
  needsCode: boolean
  busy: boolean
  error: string
  detail: string
}>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ confirm: [payload: { acceptPrerelease: boolean; code: string }] }>()
const { t, locale } = useI18n()
// the arrow from the old version to the new one points the way the line reads
const arrowIcon = computed(() => (locale.value === 'fa' ? 'pi-arrow-left' : 'pi-arrow-right'))

const accepted = ref(false)
const code = ref('')
watch(visible, (v) => {
  if (v) {
    accepted.value = false
    code.value = ''
  }
})
const codeOk = computed(() => !props.needsCode || /^\d{6}$/.test(code.value.trim()))
const ready = computed(() => (!props.prerelease || accepted.value) && codeOk.value)
function submit() {
  if (!ready.value || props.busy) return
  emit('confirm', { acceptPrerelease: props.prerelease && accepted.value, code: code.value.trim() })
}
</script>

<template>
  <Dialog
    v-model:visible="visible"
    :header="t('system.updates.confirm.title', { version: isolate(to) })"
    modal
    :draggable="false"
    :closable="!busy"
    :style="{ inlineSize: 'min(34rem, 94vw)' }"
    class="update-confirm"
  >
    <form class="body" @submit.prevent="submit">
      <div class="versions">
        <span class="app-ltr from">{{ from }}</span>
        <i :class="['pi', arrowIcon, 'arrow']" aria-hidden="true" />
        <span class="app-ltr to">{{ to }}</span>
        <Tag :severity="channel === 'beta' ? 'warn' : 'secondary'" :value="t('system.updates.channel_name.' + (channel === 'beta' ? 'beta' : 'stable'))" class="channel-tag" />
      </div>

      <Message v-if="prerelease" severity="warn" icon="pi pi-flag" :closable="false" class="prerelease">
        <div class="title">{{ t('system.updates.confirm.prerelease_title') }}</div>
        <p class="line">{{ t('system.updates.confirm.prerelease') }}</p>
        <label class="check">
          <Checkbox v-model="accepted" binary input-id="accept-prerelease" class="accept" />
          <span>{{ t('system.updates.confirm.prerelease_check') }}</span>
        </label>
      </Message>

      <ul class="points">
        <li><i class="pi pi-clock" aria-hidden="true" /><span class="downtime">{{ t('system.updates.confirm.downtime') }}</span></li>
        <li><i class="pi pi-shield" aria-hidden="true" /><span class="safe">{{ t('system.updates.confirm.safe') }}</span></li>
        <li><i class="pi pi-send" aria-hidden="true" /><span class="owners">{{ t('system.updates.confirm.owners') }}</span></li>
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
      <Button :label="t('system.updates.confirm.submit')" icon="pi pi-cloud-download" :loading="busy" :disabled="!ready" class="submit" @click="submit" />
    </template>
  </Dialog>
</template>

<style scoped>
.body {
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
}
.versions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  font-weight: 600;
  font-size: 1.15rem;
}
.arrow {
  font-size: 0.9rem;
  color: var(--p-text-muted-color);
}
.title {
  font-weight: 600;
}
.line {
  margin-block: 0.35rem 0.6rem;
}
.check {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  cursor: pointer;
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
