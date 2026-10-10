<script setup lang="ts">
import { computed, nextTick, reactive, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Dialog from 'primevue/dialog'
import Textarea from 'primevue/textarea'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import TextPreview from './TextPreview.vue'
import ProblemList from './ProblemList.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { isolate, num, type Lang } from '../../format'
import { TEXT_LANGS, insertAt, isHtmlContext, textLangs, textLength } from '../../branding'
import type { TextCheck, TextItem, TextProblem } from '../../types'

// One bot text in each language it is used in: edit it as raw text (never
// through vue-i18n), check it live with core, preview it, save or reset.
const props = defineProps<{ item: TextItem | null; brand: string }>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ saved: [item: TextItem] }>()

const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()
const uid = useId()

interface Draft {
  value: string
  check: TextCheck | null
  checking: boolean
  seq: number
  timer: ReturnType<typeof setTimeout> | undefined
  saving: boolean
  error: string
  detail: string
  problems: TextProblem[]
}
const current = ref<TextItem | null>(null)
const drafts = reactive<Partial<Record<Lang, Draft>>>({})
const entries = computed(() => {
  const it = current.value
  if (!it) return []
  return textLangs(it).flatMap((l) => {
    const d = drafts[l]
    return d ? [{ l, d }] : []
  })
})

const stored = (it: TextItem, l: Lang) => it.override[l] || it.default[l] || ''
const areaId = (l: Lang) => `${uid}-${l}`

function stop() {
  for (const l of TEXT_LANGS) clearTimeout(drafts[l]?.timer)
}

// Each opening starts from the text as saved.
function start() {
  stop()
  const it = props.item
  current.value = it
  for (const l of TEXT_LANGS) delete drafts[l]
  if (!it) return
  for (const l of textLangs(it)) {
    drafts[l] = { value: stored(it, l), check: null, checking: false, seq: 0, timer: undefined, saving: false, error: '', detail: '', problems: [] }
    // A saved change may have gone bad since (a newer catalog): check it now.
    if (it.override[l]) schedule(l, 0)
  }
}
watch(visible, (v) => (v ? start() : stop()), { immediate: true })

/** schedule checks the text with core once typing pauses; only the answer
 * for the latest text counts. */
function schedule(l: Lang, delay = 400) {
  const d = drafts[l]
  const it = current.value
  if (!d || !it) return
  clearTimeout(d.timer)
  const seq = ++d.seq
  d.problems = []
  d.error = ''
  d.detail = ''
  const value = d.value
  if (!value.trim()) {
    d.check = null
    d.checking = false
    return
  }
  d.checking = true
  d.timer = setTimeout(async () => {
    try {
      const res = await api<TextCheck>('POST', '/texts/check', { lang: l, key: it.key, value })
      if (seq === d.seq) d.check = res
    } catch {
      if (seq === d.seq) d.check = null
    } finally {
      if (seq === d.seq) d.checking = false
    }
  }, delay)
}

function edit(l: Lang, value: string | undefined) {
  const d = drafts[l]
  if (!d) return
  d.value = value ?? ''
  schedule(l)
}

const len = (d: Draft) => textLength(d.value)
const dirty = (l: Lang) => {
  const d = drafts[l]
  return !!current.value && !!d && d.value.trim() !== stored(current.value, l).trim()
}
const anyDirty = computed(() => entries.value.some((e) => dirty(e.l)))
const blocked = (d: Draft) => !!d.check && !d.check.ok && !d.checking
const canSave = (l: Lang) => {
  const d = drafts[l]
  return !!d && dirty(l) && d.value.trim() !== '' && !d.saving && !blocked(d)
}
const shownProblems = (d: Draft) => (d.problems.length ? d.problems : !d.checking && d.check ? d.check.problems : [])

/** insert puts {name} where the cursor is (or replaces the selection). */
async function insert(l: Lang, name: string) {
  const d = drafts[l]
  if (!d) return
  const el = document.getElementById(areaId(l)) as HTMLTextAreaElement | null
  const startAt = el?.selectionStart ?? d.value.length
  const r = insertAt(d.value, startAt, el?.selectionEnd ?? startAt, '{' + name + '}')
  edit(l, r.value)
  await nextTick()
  el?.focus()
  el?.setSelectionRange(r.cursor, r.cursor)
}

function useDefault(l: Lang) {
  if (current.value) edit(l, current.value.default[l])
}

// Saving the default text itself (or resetting) removes the change, so later
// catalog improvements reach this text again.
async function save(l: Lang, reset = false) {
  const d = drafts[l]
  const it = current.value
  if (!d || !it) return
  const value = d.value.trim()
  const toDefault = reset || value === it.default[l].trim()
  d.saving = true
  d.error = ''
  d.detail = ''
  d.problems = []
  try {
    const res = await api<{ item: TextItem }>('PUT', '/texts', { lang: l, key: it.key, value: toDefault ? '' : value })
    current.value = res.item
    clearTimeout(d.timer)
    d.seq++
    d.check = null
    d.checking = false
    d.value = stored(res.item, l)
    emit('saved', res.item)
    toast.add({ severity: 'success', summary: toDefault ? t('texts.reset_done') : t('texts.saved'), life: 3000 })
  } catch (e) {
    const probs = e instanceof ApiError && Array.isArray(e.data.problems) ? (e.data.problems as TextProblem[]) : []
    d.problems = probs.filter((p) => p && typeof p.code === 'string' && (!p.lang || p.lang === l))
    if (!d.problems.length) {
      const err = errorText(e, t)
      d.error = err.text
      d.detail = err.detail
    }
  } finally {
    d.saving = false
  }
}

function reset(l: Lang) {
  const it = current.value
  if (!it) return
  confirm.require({
    header: t('texts.reset'),
    message: t('texts.reset_confirm', { key: isolate(it.key), lang: t('texts.' + l) }),
    acceptProps: { label: t('texts.reset'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: () => void save(l, true),
  })
}

// Closing with unsaved edits asks first.
function close(v: boolean) {
  if (v || !anyDirty.value) {
    visible.value = v
    return
  }
  confirm.require({
    header: t('texts.discard_title'),
    message: t('texts.discard_message'),
    acceptProps: { label: t('texts.discard'), severity: 'danger' },
    rejectProps: { label: t('texts.keep_editing'), severity: 'secondary', outlined: true },
    accept: () => (visible.value = false),
  })
}

const context = computed(() => current.value?.context ?? 'html')
const contextLabel = computed(() => (te('texts.context.' + context.value) ? t('texts.context.' + context.value) : context.value))
const groupLabel = computed(() => {
  const g = current.value?.group ?? ''
  return te('texts.group.' + g) ? t('texts.group.' + g) : g
})
const html = computed(() => isHtmlContext(context.value))
// Tag names and entities are Telegram's, the same in every language.
const markup = {
  tags: isolate('<b> <i> <u> <s> <tg-spoiler> <code> <pre> <blockquote> <a href="https://…">'),
  chars: isolate('& < >'),
  entities: isolate('&amp; &lt; &gt;'),
}
const previewKind = computed(() => (context.value.startsWith('button') ? 'button' : context.value.startsWith('toast') ? 'toast' : 'message'))
</script>

<template>
  <Dialog :visible="visible" modal :draggable="false" :style="{ inlineSize: 'min(64rem, 96vw)' }" @update:visible="close">
    <template #header>
      <div class="head">
        <span class="title">{{ t('texts.edit_title') }}</span>
        <span v-if="current" class="app-ltr app-mono key">{{ current.key }}</span>
      </div>
    </template>
    <template v-if="current">
      <dl class="app-facts about">
        <dt>{{ t('texts.where') }}</dt>
        <dd>{{ contextLabel }} · {{ t('texts.limit', { n: num(current.max, lang) }) }}</dd>
        <dt>{{ t('texts.group_label') }}</dt>
        <dd>{{ groupLabel }}</dd>
      </dl>
      <p class="app-muted hint">
        {{ html ? t('texts.html_hint', markup) : t('texts.plain_hint', markup) }}
      </p>

      <div class="langs">
        <section v-for="e in entries" :key="e.l" class="lang" :aria-labelledby="areaId(e.l) + '-label'">
          <div class="lang-head">
            <label :id="areaId(e.l) + '-label'" :for="areaId(e.l)" class="lang-name">{{ t('texts.' + e.l) }}</label>
            <Tag v-if="current.override[e.l]" :value="t('texts.changed')" severity="info" />
            <span class="counter" :class="{ over: len(e.d) > current.max }" :aria-label="t('texts.length')">
              {{ num(len(e.d), lang) }} / {{ num(current.max, lang) }}
            </span>
          </div>

          <Textarea
            :id="areaId(e.l)"
            :model-value="e.d.value"
            :dir="e.l === 'fa' ? 'rtl' : 'ltr'"
            :lang="e.l"
            :class="'ta-' + e.l"
            rows="5"
            auto-resize
            fluid
            :invalid="shownProblems(e.d).length > 0"
            @update:model-value="(v: string | undefined) => edit(e.l, v)"
          />

          <div v-if="current.placeholders.length" class="chips">
            <span class="app-muted small">{{ t('texts.placeholders') }}</span>
            <Button
              v-for="p in current.placeholders"
              :key="p"
              :label="'{' + p + '}'"
              size="small"
              severity="secondary"
              outlined
              class="chip app-ltr app-mono"
              :aria-label="t('texts.insert', { name: '{' + p + '}' })"
              @click="insert(e.l, p)"
            />
          </div>

          <div class="status" aria-live="polite">
            <span v-if="e.d.checking" class="app-muted small"><i class="pi pi-spin pi-spinner" aria-hidden="true" /> {{ t('texts.checking') }}</span>
            <ProblemList v-else-if="shownProblems(e.d).length" :problems="shownProblems(e.d)" />
            <span v-else-if="e.d.check?.ok" class="ok small"><i class="pi pi-check" aria-hidden="true" /> {{ t('texts.ok') }}</span>
            <span v-else-if="!e.d.value.trim()" class="app-muted small">{{ t('texts.empty_hint') }}</span>
          </div>
          <Message v-if="e.d.error" severity="error" :closable="false" size="small">
            {{ e.d.error }}
            <div v-if="e.d.detail" class="app-ltr detail">{{ e.d.detail }}</div>
          </Message>

          <div class="block">
            <div class="app-muted small">{{ t('texts.preview') }}</div>
            <TextPreview :value="e.d.value" :html="html" :placeholders="current.placeholders" :lang="e.l" :brand="brand" :kind="previewKind" />
          </div>

          <div class="block">
            <div class="default-head">
              <span class="app-muted small">{{ t('texts.default_text') }}</span>
              <Button
                :label="t('texts.use_default')"
                text
                size="small"
                :disabled="e.d.value.trim() === current.default[e.l].trim()"
                @click="useDefault(e.l)"
              />
            </div>
            <div class="default app-muted" :dir="e.l === 'fa' ? 'rtl' : 'ltr'" :lang="e.l">{{ current.default[e.l] }}</div>
          </div>

          <div class="actions">
            <Button
              v-if="current.override[e.l]"
              :label="t('texts.reset')"
              icon="pi pi-undo"
              severity="danger"
              text
              :disabled="e.d.saving"
              @click="reset(e.l)"
            />
            <Button :label="t('common.save')" icon="pi pi-check" :loading="e.d.saving" :disabled="!canSave(e.l)" @click="save(e.l)" />
          </div>
        </section>
      </div>
    </template>
  </Dialog>
</template>

<style scoped>
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.25rem 0.75rem;
  min-inline-size: 0;
}
.title {
  font-weight: 700;
  font-size: 1.1rem;
}
.key {
  overflow-wrap: anywhere;
}
.about {
  margin-block-end: 0.5rem;
}
.hint {
  font-size: 0.85rem;
  margin-block: 0 1rem;
}
.langs {
  display: grid;
  gap: 1.5rem;
  grid-template-columns: repeat(auto-fit, minmax(min(24rem, 100%), 1fr));
}
.lang {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  min-inline-size: 0;
}
.lang-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.lang-name {
  font-weight: 700;
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
.chips {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem;
}
.chip {
  padding-block: 0.15rem;
  padding-inline: 0.45rem;
  font-size: 0.8rem;
}
.small {
  font-size: 0.8rem;
}
.status {
  min-block-size: 1.25rem;
}
.ok {
  color: var(--p-message-success-simple-color, var(--p-green-600));
}
.detail {
  font-size: 0.85rem;
}
.block {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}
.default-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}
.default {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 0.85rem;
  padding: 0.5rem 0.7rem;
  border: 1px dashed var(--p-content-border-color);
  border-radius: 0.5rem;
}
.default[lang='fa'] {
  font-family: var(--app-font-fa);
}
.default[lang='en'] {
  font-family: var(--app-font-en);
}
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
</style>
