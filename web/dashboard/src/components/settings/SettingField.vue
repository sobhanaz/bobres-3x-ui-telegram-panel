<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import ToggleSwitch from 'primevue/toggleswitch'
import Button from 'primevue/button'
import Message from 'primevue/message'
import { api } from '../../api'
import { errorText } from '../../errors'
import { isolate, num, type Lang } from '../../format'
import { describeCheck, fieldId, ltr, maxLength, normalize, unreachable, zoneNow, zoneOptions } from '../../settings'
import type { ChannelCheck, SettingItem } from '../../types'

// One setting, drawn by its kind, with what it does in the bot under it.
const props = withDefaults(
  defineProps<{
    item: SettingItem
    modelValue: string
    /** translated problem with the typed value */
    problem?: string
    /** core's own reason (English) when it refused this key */
    serverError?: string
    placeholder?: string
    /** the channel check may run (a valid channel is typed) */
    checkable?: boolean
  }>(),
  { problem: '', serverError: '', placeholder: '', checkable: false },
)
const emit = defineEmits<{ 'update:modelValue': [value: string]; touch: [] }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const id = computed(() => fieldId(props.item.key))
const labelId = computed(() => id.value + '-label')
const label = computed(() => {
  const k = `settings.field.${props.item.key}.label`
  return te(k) ? t(k) : props.item.key
})
const help = computed(() => {
  const k = `settings.field.${props.item.key}.help`
  return te(k) ? t(k) : ''
})
const locked = computed(() => !props.item.editable)

// Range of a number, with the default when there is one.
const hint = computed(() => {
  const { kind, min, max } = props.item
  const def = props.item.default
  if (kind === 'tz') {
    const now = zoneNow(props.modelValue)
    return now ? t('settings.tz_now', { time: isolate(now) }) : ''
  }
  if (kind !== 'int' || min === null || max === null) return ''
  const range = { min: num(min, lang.value), max: num(max, lang.value) }
  return /^\d+$/.test(def) ? t('settings.range_default', { ...range, def: num(Number(def), lang.value) }) : t('settings.range', range)
})

const describedBy = computed(() =>
  [hint.value && id.value + '-hint', help.value && id.value + '-help', (props.problem || props.serverError) && id.value + '-error']
    .filter(Boolean)
    .join(' ') || undefined,
)

const enumOptions = computed(() =>
  props.item.options.map((o) => {
    const k = `settings.option.${props.item.key}.${o}`
    return { value: o, label: te(k) ? t(k) : o }
  }),
)
const tzOptions = computed(() => [
  { value: '', label: t('settings.tz_default') },
  ...zoneOptions(props.modelValue).map((z) => ({ value: z, label: z })),
])

function set(v: string) {
  emit('update:modelValue', v)
}
function setBool(v: unknown) {
  set(v === true || v === 'true' ? 'true' : 'false')
  emit('touch')
}
function setChoice(v: unknown) {
  set(typeof v === 'string' ? v : '')
  emit('touch')
}

// Channel check: asks the bot whether it sees the channel and is an admin there.
const checking = ref(false)
const check = ref<{ severity: 'success' | 'warn' | 'error'; text: string; detail: string } | null>(null)
watch(
  () => props.modelValue,
  () => (check.value = null),
)
async function runCheck() {
  checking.value = true
  check.value = null
  try {
    const res = await api<ChannelCheck>('POST', '/settings/channel-check', { chat: normalize(props.item, props.modelValue) })
    const view = describeCheck(res)
    check.value = { severity: view.severity, text: t(view.key, { title: isolate(view.title) }), detail: view.detail }
  } catch (e) {
    if (unreachable(e)) {
      check.value = { severity: 'error', text: t('settings.check.unreachable'), detail: '' }
    } else {
      const err = errorText(e, t)
      check.value = { severity: 'error', text: err.text, detail: err.detail }
    }
  } finally {
    checking.value = false
  }
}
</script>

<template>
  <div class="field" :class="{ 'is-bool': item.kind === 'bool' }">
    <div v-if="item.kind === 'bool'" class="bool-row">
      <label :id="labelId" :for="id" class="label">
        {{ label }}
        <i v-if="locked" class="pi pi-lock lock" role="img" :aria-label="t('settings.read_only')" />
      </label>
      <ToggleSwitch
        :input-id="id"
        :model-value="modelValue === 'true'"
        :disabled="locked"
        :invalid="!!problem || !!serverError"
        :pt="{ input: { 'aria-describedby': describedBy } }"
        @update:model-value="setBool"
      />
    </div>
    <template v-else>
      <label :id="labelId" :for="id" class="label">
        {{ label }}
        <i v-if="locked" class="pi pi-lock lock" role="img" :aria-label="t('settings.read_only')" />
      </label>
      <Select
        v-if="item.kind === 'enum'"
        :label-id="id"
        :aria-labelledby="labelId"
        :model-value="modelValue"
        :options="enumOptions"
        option-label="label"
        option-value="value"
        :placeholder="t('settings.select')"
        :disabled="locked"
        :invalid="!!problem || !!serverError"
        :pt="{ label: { 'aria-describedby': describedBy } }"
        fluid
        @update:model-value="setChoice"
      />
      <Select
        v-else-if="item.kind === 'tz'"
        :label-id="id"
        :aria-labelledby="labelId"
        :model-value="modelValue"
        :options="tzOptions"
        option-label="label"
        option-value="value"
        filter
        auto-filter-focus
        reset-filter-on-hide
        :filter-placeholder="t('settings.tz_filter')"
        :placeholder="t('settings.tz_default')"
        :disabled="locked"
        :invalid="!!problem || !!serverError"
        :pt="{ label: { 'aria-describedby': describedBy } }"
        fluid
        @update:model-value="setChoice"
      >
        <template #value="{ value, placeholder: ph }">
          <span v-if="value" class="app-ltr">{{ value }}</span>
          <span v-else>{{ ph }}</span>
        </template>
        <template #option="{ option }">
          <span :class="{ 'app-ltr': option.value }">{{ option.label }}</span>
        </template>
      </Select>
      <div v-else class="control">
        <InputText
          :id="id"
          :model-value="modelValue"
          :dir="ltr(item) ? 'ltr' : 'auto'"
          :inputmode="item.kind === 'int' ? 'numeric' : item.kind === 'url' ? 'url' : undefined"
          :maxlength="maxLength(item)"
          :placeholder="placeholder"
          :readonly="locked"
          :invalid="!!problem || !!serverError"
          :aria-describedby="describedBy"
          autocomplete="off"
          :spellcheck="false"
          class="input"
          @update:model-value="(v) => set(v ?? '')"
          @blur="emit('touch')"
        />
        <Button
          v-if="item.kind === 'channel'"
          type="button"
          :label="t('settings.check.button')"
          icon="pi pi-search"
          severity="secondary"
          outlined
          :loading="checking"
          :disabled="!checkable"
          @click="runCheck"
        />
      </div>
    </template>
    <small v-if="hint" :id="id + '-hint'" class="app-muted hint">{{ hint }}</small>
    <small v-if="help" :id="id + '-help'" class="app-muted help">{{ help }}</small>
    <div v-if="problem || serverError" :id="id + '-error'" role="alert">
      <Message v-if="problem" severity="error" size="small" variant="simple">{{ problem }}</Message>
      <Message v-if="serverError" severity="error" size="small" variant="simple">
        <span class="app-ltr">{{ serverError }}</span>
      </Message>
    </div>
    <Message v-if="check" :severity="check.severity" :closable="false" class="check" aria-live="polite">
      {{ check.text }}
      <div v-if="check.detail" class="app-ltr detail">{{ check.detail }}</div>
    </Message>
  </div>
</template>

<style scoped>
.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  min-inline-size: 0;
}
.label {
  font-weight: 600;
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}
.lock {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
}
.bool-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}
.bool-row .label {
  flex: 1 1 auto;
  min-inline-size: 0;
}
.control {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.input {
  flex: 1 1 12rem;
  min-inline-size: 0;
}
.hint,
.help {
  font-size: 0.82rem;
  line-height: 1.6;
}
.check {
  margin-block-start: 0.25rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
</style>
