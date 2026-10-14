<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Textarea from 'primevue/textarea'
import TextPreview from '../branding/TextPreview.vue'
import ProblemList from '../branding/ProblemList.vue'
import { api } from '../../api'
import { textLength } from '../../branding'
import { isolate, num, type Lang } from '../../format'
import { MAX_TEXT, problemsFor, timing, type TextState } from '../../broadcasts'
import type { TextCheck, TextProblem } from '../../types'

// The broadcast's text in one language: raw text (data, never a dashboard
// message), checked live by core once typing pauses, counted against
// Telegram's limit, and previewed without running any markup.
const value = defineModel<string>({ required: true })
const props = withDefaults(
  defineProps<{ lang: Lang; problems?: TextProblem[]; error?: string; buttonLabel?: string; disabled?: boolean; flagEmpty?: boolean }>(),
  { problems: () => [], error: '', buttonLabel: '', disabled: false, flagEmpty: false },
)
const emit = defineEmits<{ state: [s: TextState] }>()
const { t, locale } = useI18n()
const viewer = computed(() => locale.value as Lang)
const uid = useId()
const areaId = `${uid}-text`

const check = ref<TextCheck | null>(null)
const checking = ref(false)
let seq = 0
let timer: ReturnType<typeof setTimeout> | undefined

function report() {
  emit('state', { checking: checking.value, ok: check.value ? check.value.ok : null })
}

/** schedule asks core about the text once typing pauses; only the answer
 * for the latest text counts. */
function schedule(delay: number) {
  clearTimeout(timer)
  const mine = ++seq
  const text = value.value
  check.value = null
  if (!text.trim()) {
    checking.value = false
    report()
    return
  }
  checking.value = true
  report()
  timer = setTimeout(async () => {
    try {
      const res = await api<TextCheck>('POST', '/broadcasts/check', { lang: props.lang, text })
      if (mine === seq) check.value = res
    } catch {
      if (mine === seq) check.value = null
    } finally {
      if (mine === seq) {
        checking.value = false
        report()
      }
    }
  }, delay)
}
let first = true
watch(
  value,
  () => {
    schedule(first ? 0 : timing.check)
    first = false
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  clearTimeout(timer)
  seq++
})

const len = computed(() => textLength(value.value))
// A refused save's problems show until the text changes; then the live check's.
const shown = computed(() => (props.problems.length ? props.problems : !checking.value && check.value ? problemsFor(check.value.problems, props.lang) : []))
const markup = {
  tags: isolate('<b> <i> <u> <s> <tg-spoiler> <code> <pre> <blockquote> <a href="https://…">'),
  chars: isolate('& < >'),
  entities: isolate('&amp; &lt; &gt;'),
}
</script>

<template>
  <section class="text" :aria-labelledby="areaId + '-label'">
    <div class="head">
      <label :id="areaId + '-label'" :for="areaId" class="name">{{ t('broadcasts.text.label', { lang: t('texts.' + lang) }) }}</label>
      <span class="counter" :class="{ over: len > MAX_TEXT }" :aria-label="t('texts.length')">
        {{ num(len, viewer) }} / {{ num(MAX_TEXT, viewer) }}
      </span>
    </div>
    <Textarea
      :id="areaId"
      v-model="value"
      :dir="lang === 'fa' ? 'rtl' : 'ltr'"
      :lang="lang"
      :class="'ta-' + lang"
      rows="7"
      auto-resize
      fluid
      :disabled="props.disabled"
      :invalid="shown.length > 0 || !!props.error || len > MAX_TEXT || (props.flagEmpty && !value.trim())"
      :aria-describedby="areaId + '-status'"
    />
    <p class="app-muted hint">{{ t('texts.html_hint', markup) }}</p>
    <div :id="areaId + '-status'" class="status" aria-live="polite">
      <span v-if="checking" class="app-muted small"><i class="pi pi-spin pi-spinner" aria-hidden="true" /> {{ t('texts.checking') }}</span>
      <ProblemList v-else-if="shown.length" :problems="shown" />
      <span v-else-if="!value.trim()" class="small" :class="props.error || props.flagEmpty ? 'err' : 'app-muted'">{{ t('broadcasts.text.empty') }}</span>
      <span v-else-if="len > MAX_TEXT" class="err small">{{ t('texts.problem.too_long', { arg: num(MAX_TEXT, viewer) }, MAX_TEXT) }}</span>
      <span v-else-if="check?.ok" class="ok small"><i class="pi pi-check" aria-hidden="true" /> {{ t('texts.ok') }}</span>
      <div v-if="props.error && value.trim()" class="err small app-ltr">{{ props.error }}</div>
    </div>
    <div class="block">
      <div class="app-muted small">{{ t('broadcasts.text.preview') }}</div>
      <div class="bubble">
        <TextPreview :value="value" :html="true" :placeholders="[]" :lang="lang" kind="message" />
        <TextPreview v-if="buttonLabel.trim()" :value="buttonLabel" :html="false" :placeholders="[]" :lang="lang" kind="button" class="btn" />
      </div>
    </div>
  </section>
</template>

<style scoped>
.text {
  display: flex;
  flex-direction: column;
  gap: 0.55rem;
  min-inline-size: 0;
}
.head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.name {
  font-weight: 600;
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
.ta-fa {
  font-family: var(--app-font-fa);
  line-height: 1.8;
}
.ta-en {
  font-family: var(--app-font-en);
}
.hint {
  margin: 0;
  font-size: 0.8rem;
}
.status {
  min-block-size: 1.25rem;
}
.small {
  font-size: 0.8rem;
}
.ok {
  color: var(--p-message-success-simple-color, var(--p-green-600));
}
.err {
  color: var(--p-message-error-simple-color, var(--p-red-600));
}
.block {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}
.bubble {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  max-inline-size: 30rem;
}
</style>
