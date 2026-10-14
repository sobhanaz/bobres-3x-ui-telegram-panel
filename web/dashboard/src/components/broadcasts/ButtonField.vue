<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import ToggleSwitch from 'primevue/toggleswitch'
import InputText from 'primevue/inputtext'
import { isolate, num, type Lang } from '../../format'
import { MAX_LABEL, labelProblem, urlProblem, utf16Length, type Draft } from '../../broadcasts'

// An optional link button under the message: an https link and its text in
// each language the audience needs (plain text, no markup).
const button = defineModel<Draft['button']>('button', { required: true })
const props = withDefaults(defineProps<{ langs: Lang[]; errors?: Record<string, string>; showAll?: boolean; disabled?: boolean }>(), {
  errors: () => ({}),
  showAll: false,
  disabled: false,
})
const { t, locale } = useI18n()
const viewer = computed(() => locale.value as Lang)
const uid = useId()

// The page's own sentence when it sees the problem; otherwise core's reason
// (in English, shown left to right).
const urlError = computed(() => {
  const p = urlProblem(button.value.url)
  if (p && (p !== 'empty' || props.showAll)) {
    return p === 'empty' ? t('broadcasts.button.url_empty') : p === 'too_long' ? t('broadcasts.button.url_long') : t('broadcasts.button.url_bad')
  }
  return ''
})
const urlServer = computed(() => (urlError.value ? '' : (props.errors['button.url'] ?? '')))

function labelError(l: Lang): string {
  const p = labelProblem(button.value.labels[l])
  if (!p || (p === 'empty' && !props.showAll)) return ''
  if (p === 'empty') return t('broadcasts.button.label_empty')
  if (p === 'chars') return t('broadcasts.button.label_chars', { chars: isolate('< > &') })
  return t('broadcasts.button.label_long', { n: num(MAX_LABEL, viewer.value) }, MAX_LABEL)
}
const labelServer = (l: Lang) => (labelError(l) ? '' : (props.errors['button.labels.' + l] ?? ''))
</script>

<template>
  <div class="button-field">
    <div class="toggle">
      <ToggleSwitch v-model="button.on" :input-id="`${uid}-on`" :disabled="props.disabled" />
      <label :for="`${uid}-on`">{{ t('broadcasts.button.add') }}</label>
    </div>
    <div v-if="button.on" class="fields">
      <div class="field">
        <label :for="`${uid}-url`">{{ t('broadcasts.button.url') }}</label>
        <InputText
          :id="`${uid}-url`"
          v-model="button.url"
          dir="ltr"
          inputmode="url"
          autocomplete="off"
          spellcheck="false"
          placeholder="https://"
          class="app-ltr"
          fluid
          :disabled="props.disabled"
          :invalid="!!urlError || !!urlServer"
          :aria-describedby="urlError || urlServer ? `${uid}-url-err` : undefined"
        />
        <small v-if="urlError" :id="`${uid}-url-err`" class="err">{{ urlError }}</small>
        <small v-else-if="urlServer" :id="`${uid}-url-err`" class="err app-ltr">{{ urlServer }}</small>
      </div>
      <div class="labels">
        <div v-for="l in props.langs" :key="l" class="field">
          <label :for="`${uid}-label-${l}`" class="label-head">
            <span>{{ t('broadcasts.button.label', { lang: t('texts.' + l) }) }}</span>
            <span class="counter" :class="{ over: utf16Length(button.labels[l]) > MAX_LABEL }">
              {{ num(utf16Length(button.labels[l]), viewer) }} / {{ num(MAX_LABEL, viewer) }}
            </span>
          </label>
          <InputText
            :id="`${uid}-label-${l}`"
            v-model="button.labels[l]"
            :dir="l === 'fa' ? 'rtl' : 'ltr'"
            :lang="l"
            :class="'in-' + l"
            fluid
            :disabled="props.disabled"
            :invalid="!!labelError(l) || !!labelServer(l)"
            :aria-describedby="labelError(l) || labelServer(l) ? `${uid}-label-${l}-err` : undefined"
          />
          <small v-if="labelError(l)" :id="`${uid}-label-${l}-err`" class="err">{{ labelError(l) }}</small>
          <small v-else-if="labelServer(l)" :id="`${uid}-label-${l}-err`" class="err app-ltr">{{ labelServer(l) }}</small>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.button-field {
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
}
.toggle {
  display: flex;
  align-items: center;
  gap: 0.6rem;
}
.fields {
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
}
.labels {
  display: grid;
  gap: 0.85rem;
  grid-template-columns: repeat(auto-fit, minmax(min(16rem, 100%), 1fr));
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  min-inline-size: 0;
}
.label-head {
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
.in-fa {
  font-family: var(--app-font-fa);
}
.in-en {
  font-family: var(--app-font-en);
}
.err {
  color: var(--p-message-error-simple-color, var(--p-red-600));
}
</style>
