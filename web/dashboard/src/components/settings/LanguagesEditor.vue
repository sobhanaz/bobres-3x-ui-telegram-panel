<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Checkbox from 'primevue/checkbox'
import RadioButton from 'primevue/radiobutton'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import { BETA_LANGS, LANGS, languageName, rtl, type Lang } from '../../format'
import { formatLangs, parseLangs } from '../../settings'

// The languages customers may pick (setting languages.enabled, "fa,en,ru" with
// the default first): a box per language and the default among the ticked
// ones. The machine-translated languages carry a beta tag and a note.
const props = defineProps<{ modelValue: string; inputId: string; invalid?: boolean; locked?: boolean; describedBy?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; touch: [] }>()
const { t } = useI18n()

const parsed = computed<Lang[]>(() => {
  const p = parseLangs(props.modelValue)
  return 'langs' in p ? p.langs : ['fa', 'en']
})
const enabled = computed(() => parsed.value)
const def = computed(() => parsed.value[0])
const betaOn = computed(() => enabled.value.some((l) => BETA_LANGS.includes(l)))

function toggle(l: Lang, on: boolean) {
  const next = on ? [...enabled.value, l] : enabled.value.filter((x) => x !== l)
  if (!next.length) return // one language at least: the last box stays ticked
  emit('update:modelValue', formatLangs(next, next.includes(def.value) ? def.value : next[0]))
  emit('touch')
}

function makeDefault(l: Lang) {
  emit('update:modelValue', formatLangs(enabled.value, l))
  emit('touch')
}
</script>

<template>
  <div :id="inputId" class="langs" role="group" :aria-describedby="describedBy" :class="{ invalid }">
    <div v-for="l in LANGS" :key="l" class="row" :data-lang="l">
      <Checkbox
        :input-id="inputId + '-' + l"
        :model-value="enabled.includes(l)"
        binary
        :disabled="locked || (enabled.length === 1 && enabled.includes(l))"
        :aria-label="t('settings.langs.enable_aria', { lang: languageName(l) })"
        @update:model-value="(v: boolean) => toggle(l, v)"
      />
      <label :for="inputId + '-' + l" class="name" :lang="l" :dir="rtl(l) ? 'rtl' : 'ltr'">{{ languageName(l) }}</label>
      <Tag v-if="BETA_LANGS.includes(l)" :value="t('lang.beta')" severity="warn" :title="t('lang.beta_note')" />
      <span class="spacer" />
      <template v-if="enabled.includes(l)">
        <RadioButton
          :input-id="inputId + '-def-' + l"
          :name="inputId + '-default'"
          :value="l"
          :model-value="def"
          :disabled="locked"
          :aria-label="t('settings.langs.default_aria', { lang: languageName(l) })"
          @update:model-value="() => makeDefault(l)"
        />
        <label :for="inputId + '-def-' + l" class="app-muted small">{{ t('settings.langs.default') }}</label>
      </template>
    </div>
    <Message v-if="betaOn" severity="warn" size="small" variant="simple" class="note">{{ t('settings.langs.beta_note') }}</Message>
  </div>
</template>

<style scoped>
.langs {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-height: 2rem;
}
.spacer {
  flex: 1;
}
.small {
  font-size: 0.85rem;
}
.note {
  margin-top: 0.25rem;
}
</style>
