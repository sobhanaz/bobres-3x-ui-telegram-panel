<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import { isolate } from '../format'

// A write-only secret (a panel token, a merchant id, a proxy address): core
// never sends it back, so this shows only whether one is stored (and a hint
// such as its last characters), and offers to replace it or, when allowed,
// to remove it. What staff type is the new value; empty keeps the stored one.
// Parents give it a new key to start over (after a save, or a dialog reopening).
const props = withDefaults(
  defineProps<{
    id: string
    label: string
    /** a value is stored */
    set: boolean
    /** a safe part of it, such as its last 4 characters */
    hint?: string
    /** what it is for, under the field */
    help?: string
    placeholder?: string
    /** removing the stored value is allowed */
    clearable?: boolean
    /** the stored value cannot be kept (a panel moved): a new one is asked for */
    force?: boolean
    disabled?: boolean
    /** translated problem with the typed value */
    error?: string
    maxlength?: number
  }>(),
  { hint: '', help: '', placeholder: '', clearable: false, force: false, disabled: false, error: '', maxlength: 1000 },
)
const value = defineModel<string>({ required: true })
const clear = defineModel<boolean>('clear', { default: false })
const { t } = useI18n()

// A stored value shows as "set" until staff choose to replace it; a typed
// value always stays in sight.
const replacing = ref(false)
const open = computed(() => !clear.value && (!props.set || props.force || replacing.value || value.value !== ''))
const shown = ref(false)

const status = computed(() => {
  if (clear.value) return { severity: 'warn', text: t('secret.will_clear') }
  if (props.set) return { severity: 'success', text: props.hint ? t('secret.set_hint', { hint: isolate(props.hint) }) : t('secret.set') }
  return { severity: 'secondary', text: t('secret.not_set') }
})
const helpText = computed(() => props.help || (props.set && !clear.value && !props.force ? t('secret.keep_hint') : ''))
const describedBy = computed(() => [helpText.value && props.id + '-help', props.error && props.id + '-error'].filter(Boolean).join(' ') || undefined)

async function replace() {
  replacing.value = true
  await nextTick()
  document.getElementById(props.id)?.focus()
}
function keep() {
  replacing.value = false
  shown.value = false
  value.value = ''
}
function remove(on: boolean) {
  clear.value = on
  if (on) keep()
}
</script>

<template>
  <div class="secret-field">
    <label v-if="open" :for="id" class="label">{{ label }}</label>
    <span v-else :id="id + '-label'" class="label">{{ label }}</span>
    <div class="status" :aria-labelledby="open ? undefined : id + '-label'" :role="open ? undefined : 'group'">
      <Tag :severity="status.severity" :value="status.text" :icon="set && !clear ? 'pi pi-lock' : undefined" class="state" />
      <template v-if="!disabled">
        <Button v-if="set && !open && !clear" type="button" :label="t('secret.replace')" icon="pi pi-pencil" text size="small" class="replace" @click="replace" />
        <Button v-if="set && open && !force" type="button" :label="t('secret.keep')" icon="pi pi-undo" text size="small" class="keep" @click="keep" />
        <Button
          v-if="clearable && set && !open && !clear"
          type="button"
          :label="t('secret.clear')"
          icon="pi pi-trash"
          text
          severity="danger"
          size="small"
          class="clear"
          @click="remove(true)"
        />
        <Button v-if="clear" type="button" :label="t('secret.undo_clear')" icon="pi pi-undo" text size="small" class="undo" @click="remove(false)" />
      </template>
    </div>
    <div v-if="open" class="control">
      <InputText
        :id="id"
        v-model="value"
        :type="shown ? 'text' : 'password'"
        dir="ltr"
        autocomplete="new-password"
        :spellcheck="false"
        :placeholder="placeholder || (set ? t('secret.new_placeholder') : '')"
        :maxlength="maxlength"
        :disabled="disabled"
        :invalid="!!error"
        :aria-describedby="describedBy"
        data-1p-ignore
        data-lpignore="true"
        class="input"
      />
      <Button
        type="button"
        :icon="shown ? 'pi pi-eye-slash' : 'pi pi-eye'"
        severity="secondary"
        text
        :disabled="disabled || !value"
        :aria-label="shown ? t('secret.hide') : t('secret.show')"
        :aria-pressed="shown"
        class="reveal"
        @click="shown = !shown"
      />
    </div>
    <small v-if="helpText" :id="id + '-help'" class="app-muted help">{{ helpText }}</small>
    <small v-if="error" :id="id + '-error'" role="alert" class="error">{{ error }}</small>
  </div>
</template>

<style scoped>
.secret-field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  min-inline-size: 0;
}
.label {
  font-weight: 600;
}
.status {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.25rem 0.5rem;
}
.control {
  display: flex;
  gap: 0.35rem;
}
.input {
  flex: 1 1 12rem;
  min-inline-size: 0;
  font-family: var(--app-font-en);
}
.help {
  font-size: 0.82rem;
  line-height: 1.6;
}
.error {
  color: var(--p-red-500);
  font-size: 0.85rem;
}
</style>
