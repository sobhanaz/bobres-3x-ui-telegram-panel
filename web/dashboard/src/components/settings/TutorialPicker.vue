<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from 'primevue/select'
import { api } from '../../api'
import { contentLang, type ContentLang, type Lang } from '../../format'
import { tidyLine } from '../../help'
import type { HelpItem, HelpPage } from '../../types'

// The tutorial the bot sends after the video of a delivered service (setting delivery.tutorial): one of the Tutorials section, picked by its title; the
// setting holds its id. Only tutorials that are switched on are listed. A saved id that is not in the list (the tutorial was deleted or switched off)
// shows as not available, so the owner sees why nothing is sent. When the list cannot be loaded, the saved choice is kept and said so.
const props = defineProps<{ modelValue: string; inputId: string; invalid?: boolean; locked?: boolean; describedBy?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; touch: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

// null until the list comes, and when it cannot
const tutorials = ref<HelpItem[] | null>(null)
const failed = ref(false)
onMounted(async () => {
  try {
    const page = await api<HelpPage>('GET', '/help')
    if (Array.isArray(page?.tutorials)) tutorials.value = page.tutorials.filter((x) => x.enabled)
    else failed.value = true
  } catch {
    failed.value = true
  }
})

/** titleOf is a tutorial's title in the language of the page, else in the other one. */
function titleOf(it: HelpItem): string {
  const own: ContentLang = contentLang(lang.value)
  const other: ContentLang = own === 'fa' ? 'en' : 'fa'
  return tidyLine(it.title?.[own] ?? '') || tidyLine(it.title?.[other] ?? '') || it.id
}

interface Option {
  value: string
  label: string
  /** the saved tutorial is not among the ones that can be sent */
  gone?: boolean
}
const options = computed<Option[]>(() => {
  const out: Option[] = [{ value: '', label: t('settings.tutorial.none') }]
  for (const it of tutorials.value ?? []) out.push({ value: it.id, label: titleOf(it) })
  const saved = props.modelValue
  if (saved !== '' && !out.some((o) => o.value === saved)) {
    // With the list known the tutorial is gone; without it nothing is known but that one is saved.
    out.push(tutorials.value ? { value: saved, label: t('settings.tutorial.unavailable'), gone: true } : { value: saved, label: t('settings.tutorial.saved') })
  }
  return out
})
const gone = computed(() => options.value.some((o) => o.gone && o.value === props.modelValue))
const loading = computed(() => tutorials.value === null && !failed.value)

function pick(v: unknown) {
  emit('update:modelValue', typeof v === 'string' ? v : '')
  emit('touch')
}
</script>

<template>
  <div class="picker" :class="{ 'is-gone': gone }" :aria-busy="loading">
    <Select
      :label-id="inputId"
      :aria-labelledby="inputId + '-label'"
      :model-value="modelValue"
      :options="options"
      option-label="label"
      option-value="value"
      :placeholder="t('settings.tutorial.none')"
      :loading="loading"
      :disabled="locked"
      :invalid="invalid"
      :pt="{ label: { 'aria-describedby': describedBy } }"
      fluid
      @update:model-value="pick"
    >
      <template #option="{ option }">
        <span :class="{ gone: option.gone }">{{ option.label }}</span>
      </template>
    </Select>
    <small v-if="failed" class="app-muted note" role="status">{{ t('settings.tutorial.load_failed') }}</small>
  </div>
</template>

<style scoped>
.picker {
  display: grid;
  gap: 0.35rem;
}
.note {
  font-size: 0.82rem;
}
/* a tutorial that is gone looks switched off, in the list and as the value */
.gone,
.is-gone :deep(.p-select-label) {
  color: var(--p-text-muted-color);
  font-style: italic;
}
</style>
