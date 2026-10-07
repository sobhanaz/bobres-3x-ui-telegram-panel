<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Dialog from 'primevue/dialog'
import Textarea from 'primevue/textarea'
import Button from 'primevue/button'
import Message from 'primevue/message'

// A sensitive action: what it does, the staff member's reason (kept in the
// audit log), and an explicit confirm. Extra fields go in the default slot.
const props = withDefaults(
  defineProps<{
    title: string
    message?: string
    confirmLabel: string
    danger?: boolean
    busy?: boolean
    error?: string
    detail?: string
    reasonOptional?: boolean
    canConfirm?: boolean
  }>(),
  { message: '', danger: false, busy: false, error: '', detail: '', reasonOptional: false, canConfirm: true },
)
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ confirm: [reason: string] }>()
const { t } = useI18n()
const reason = ref('')
watch(visible, (v) => {
  if (v) reason.value = ''
})
function confirm() {
  emit('confirm', reason.value.trim())
}
</script>

<template>
  <Dialog v-model:visible="visible" :header="title" modal :style="{ inlineSize: 'min(32rem, 94vw)' }" :draggable="false">
    <p v-if="message" class="message">{{ message }}</p>
    <slot />
    <label class="field">
      <span>{{ reasonOptional ? t('common.reason_optional') : t('common.reason') }}</span>
      <Textarea v-model="reason" rows="2" auto-resize maxlength="500" fluid />
      <small class="app-muted">{{ t('common.reason_hint') }}</small>
    </label>
    <Message v-if="error" severity="error" :closable="false" class="msg">
      {{ error }}
      <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
    </Message>
    <template #footer>
      <Button :label="t('app.cancel')" severity="secondary" outlined @click="visible = false" />
      <Button
        :label="confirmLabel"
        :severity="danger ? 'danger' : undefined"
        :loading="busy"
        :disabled="(!reasonOptional && !reason.trim()) || !canConfirm"
        @click="confirm"
      />
    </template>
  </Dialog>
</template>

<style scoped>
.message {
  margin-block-start: 0;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  margin-block-start: 0.75rem;
}
.msg {
  margin-block-start: 0.75rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
}
</style>
