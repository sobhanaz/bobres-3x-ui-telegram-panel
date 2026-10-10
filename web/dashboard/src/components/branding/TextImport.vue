<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import SelectButton from 'primevue/selectbutton'
import Message from 'primevue/message'
import ProblemList from './ProblemList.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { isolate, num, type Lang } from '../../format'
import { parseImport } from '../../branding'
import type { TextProblem } from '../../types'

// Import bot texts from a JSON file: an export of this page ({"lang","texts"})
// or a plain key → text object. Core checks every text; all or nothing.
const emit = defineEmits<{ done: [] }>()
const { t, locale } = useI18n()
const uiLang = computed(() => locale.value as Lang)
const toast = useToast()

const input = ref<HTMLInputElement | null>(null)
const open = ref(false)
const fileName = ref('')
const texts = ref<Record<string, string> | null>(null)
const fixedLang = ref<Lang | null>(null)
const lang = ref<Lang>('fa')
const busy = ref(false)
const error = ref('')
const detail = ref('')
const problems = ref<TextProblem[]>([])

const count = computed(() => (texts.value ? Object.keys(texts.value).length : 0))
const langOptions = computed(() => [
  { label: t('texts.fa'), value: 'fa' },
  { label: t('texts.en'), value: 'en' },
])

async function picked(e: Event) {
  const el = e.target as HTMLInputElement
  const file = el.files?.[0]
  el.value = '' // the same file can be chosen again
  if (!file) return
  error.value = ''
  detail.value = ''
  problems.value = []
  texts.value = null
  fixedLang.value = null
  fileName.value = file.name
  if (file.size > 1024 * 1024) {
    error.value = t('texts.import_too_large')
  } else {
    const r = parseImport(await file.text())
    if (!r.ok) {
      error.value = t('texts.import_' + r.error)
    } else {
      texts.value = r.texts
      fixedLang.value = r.lang
      lang.value = r.lang ?? uiLang.value
    }
  }
  open.value = true
}

async function run() {
  if (!texts.value) return
  busy.value = true
  error.value = ''
  detail.value = ''
  problems.value = []
  try {
    const res = await api<{ changed: number }>('POST', '/texts/import', { lang: lang.value, texts: texts.value })
    open.value = false
    toast.add({ severity: 'success', summary: t('texts.import_done', { n: num(res.changed, uiLang.value) }), life: 4000 })
    emit('done')
  } catch (e) {
    const probs = e instanceof ApiError && Array.isArray(e.data.problems) ? (e.data.problems as TextProblem[]) : []
    problems.value = probs.filter((p) => p && typeof p.code === 'string')
    if (!problems.value.length) {
      const err = errorText(e, t)
      error.value = err.text
      detail.value = err.detail
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <input ref="input" type="file" accept="application/json,.json" class="file" tabindex="-1" aria-hidden="true" @change="picked" />
  <Button :label="t('texts.import')" icon="pi pi-upload" severity="secondary" outlined @click="input?.click()" />

  <Dialog v-model:visible="open" :header="t('texts.import_title')" modal :draggable="false" :style="{ inlineSize: 'min(34rem, 94vw)' }">
    <div class="app-form">
      <p class="file-name">
        <i class="pi pi-file" aria-hidden="true" />
        <span>{{ texts ? t('texts.import_file', { n: num(count, uiLang), file: isolate(fileName) }) : isolate(fileName) }}</span>
      </p>
      <template v-if="texts">
        <div class="app-field">
          <span id="import-lang">{{ t('texts.import_lang') }}</span>
          <SelectButton
            v-model="lang"
            :options="langOptions"
            option-label="label"
            option-value="value"
            :allow-empty="false"
            :disabled="fixedLang !== null"
            aria-labelledby="import-lang"
          />
          <small v-if="fixedLang" class="app-muted">{{ t('texts.import_lang_fixed') }}</small>
        </div>
        <p class="app-muted intro">{{ t('texts.import_intro') }}</p>
      </template>
      <Message v-if="error" severity="error" :closable="false">
        {{ error }}
        <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
      </Message>
      <div v-if="problems.length" class="problems">
        <p>{{ t('texts.import_problems') }}</p>
        <ProblemList :problems="problems" show-key />
      </div>
    </div>
    <template #footer>
      <Button :label="t('app.cancel')" severity="secondary" outlined @click="open = false" />
      <Button v-if="texts" :label="t('texts.import_run')" icon="pi pi-upload" :loading="busy" @click="run" />
    </template>
  </Dialog>
</template>

<style scoped>
.file {
  position: absolute;
  inline-size: 1px;
  block-size: 1px;
  opacity: 0;
  pointer-events: none;
}
.file-name {
  display: flex;
  gap: 0.5rem;
  align-items: baseline;
  margin: 0;
  overflow-wrap: anywhere;
}
.intro {
  margin: 0;
  font-size: 0.875rem;
}
.problems p {
  margin-block: 0 0.4rem;
}
.problems {
  max-block-size: 16rem;
  overflow-y: auto;
}
.detail {
  font-size: 0.85rem;
}
</style>
