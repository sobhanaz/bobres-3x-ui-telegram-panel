<script setup lang="ts">
import { computed, reactive, ref, shallowRef, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Textarea from 'primevue/textarea'
import ToggleSwitch from 'primevue/toggleswitch'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import HelpPreview from './HelpPreview.vue'
import HelpProblems from './HelpProblems.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { isolate, num, type Lang } from '../../format'
import { textLength } from '../../branding'
import {
  HELP_LANGS,
  blankDraft,
  draftOf,
  langState,
  localProblems,
  newButton,
  problemsAt,
  requestOf,
  sameDraft,
  tidyLine,
  titleMax,
  type LangState,
} from '../../help'
import type { HelpDraft, HelpItem, HelpKind, HelpLimits, HelpProblem } from '../../types'

// One question or tutorial, in Persian and English: edit it, see the bot's
// screen for each language as it is typed, have core check it as typing
// pauses (the markup of a text is core's to judge), save it.
const props = defineProps<{ kind: HelpKind; item: HelpItem | null; limits: HelpLimits }>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ saved: [item: HelpItem, created: boolean]; stale: [] }>()

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()
const uid = useId()
const fid = (name: string) => `${uid}-${name}`

const draft = reactive<HelpDraft>(blankDraft(props.kind))
const original = shallowRef<HelpDraft>(blankDraft(props.kind))
const saving = ref(false)
const checking = ref(false)
const serverProblems = ref<HelpProblem[]>([])
const savedProblems = ref<HelpProblem[]>([])
const error = ref('')
const detail = ref('')
let seq = 0
let timer: ReturnType<typeof setTimeout> | undefined

function stop() {
  clearTimeout(timer)
  seq++
  checking.value = false
}

// Each opening starts from the entry as saved (or a blank one).
function start() {
  stop()
  const d = props.item ? draftOf(props.item) : blankDraft(props.kind)
  Object.assign(draft, structuredClone(d))
  original.value = structuredClone(d)
  serverProblems.value = []
  savedProblems.value = []
  error.value = ''
  detail.value = ''
}
watch(visible, (v) => (v ? start() : stop()), { immediate: true })

const dirty = computed(() => !sameDraft(draft, original.value))
const blank = computed(() => HELP_LANGS.every((l) => langState(draft, l) === 'empty') && draft.buttons.length === 0)

/** schedule has core check the draft once typing pauses; only the answer for the latest draft counts. */
function schedule(delay = 400) {
  clearTimeout(timer)
  const mine = ++seq
  savedProblems.value = []
  error.value = ''
  detail.value = ''
  if (blank.value) {
    serverProblems.value = []
    checking.value = false
    return
  }
  checking.value = true
  const body = requestOf(draft)
  timer = setTimeout(async () => {
    try {
      const res = await api<{ ok: boolean; problems: HelpProblem[] }>('POST', '/help/check', body)
      if (mine === seq) serverProblems.value = res.problems
    } catch {
      if (mine === seq) serverProblems.value = []
    } finally {
      if (mine === seq) checking.value = false
    }
  }, delay)
}
// Only what was changed is checked: what is stored was checked when it was saved.
watch(
  draft,
  () => {
    if (visible.value && dirty.value) schedule()
  },
  { deep: true },
)

/** Everything wrong, once each: what the page sees at once, what core said of the draft, what core said of the last save. */
const problems = computed(() => {
  const all = [...localProblems(draft, props.limits), ...serverProblems.value, ...savedProblems.value]
  return all.filter((p, i) => all.findIndex((q) => q.field === p.field && q.code === p.code && q.arg === p.arg) === i)
})
// A blank new entry is not told off for being blank.
const shown = computed(() => (dirty.value ? problems.value : []))
const canSave = computed(() => dirty.value && !saving.value && !checking.value && problems.value.length === 0)

const titleLimit = computed(() => titleMax(props.kind, props.limits))
const k = (name: string) => t(`help.${props.kind}.${name}`)
const stateLabel = (l: Lang) => t('help.edit.state_' + langState(draft, l))
const stateSeverity = (s: LangState) => (s === 'complete' ? 'success' : s === 'half' ? 'warn' : 'secondary')
const titleLen = (l: Lang) => tidyLine(draft.title[l]).length
const bodyLen = (l: Lang) => textLength(draft.body[l])
const markup = {
  tags: isolate('<b> <i> <u> <s> <tg-spoiler> <code> <pre> <blockquote> <a href="https://…">'),
  chars: isolate('& < >'),
  entities: isolate('&amp; &lt; &gt;'),
  https: isolate('https://'),
}

function addButton() {
  if (draft.buttons.length < props.limits.buttons) draft.buttons.push(newButton())
}
function removeButton(i: number) {
  draft.buttons.splice(i, 1)
}

async function save() {
  if (!canSave.value) return
  saving.value = true
  error.value = ''
  detail.value = ''
  savedProblems.value = []
  try {
    const body: Record<string, unknown> = requestOf(draft)
    const creating = !props.item
    let res: { item: HelpItem }
    if (props.item) {
      body.version = props.item.version
      res = await api<{ item: HelpItem }>('PUT', `/help/${props.item.id}`, body)
    } else {
      res = await api<{ item: HelpItem }>('POST', '/help', body)
    }
    visible.value = false
    toast.add({ severity: 'success', summary: creating ? t('help.edit.created') : t('help.edit.saved'), life: 3000 })
    emit('saved', res.item, creating)
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && e.code === 'state' && props.item) {
      // Someone saved this entry (or the list changed) since it was opened: the page loads it again.
      visible.value = false
      emit('stale')
      return
    }
    const list = e instanceof ApiError && Array.isArray(e.data.problems) ? (e.data.problems as HelpProblem[]) : []
    savedProblems.value = list.filter((p) => p && typeof p.code === 'string' && typeof p.field === 'string')
    if (!savedProblems.value.length) {
      const err = errorText(e, t)
      error.value = err.text
      detail.value = err.detail
    }
  } finally {
    saving.value = false
  }
}

// Closing with unsaved edits asks first.
function close(v: boolean) {
  if (v || !dirty.value) {
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
</script>

<template>
  <Dialog
    :visible="visible"
    modal
    :draggable="false"
    :header="item ? k('edit') : k('new')"
    :style="{ inlineSize: 'min(72rem, 96vw)' }"
    @update:visible="close"
  >
    <form class="app-form form" @submit.prevent="save">
      <div class="shown">
        <label :for="fid('enabled')" class="switch">
          <ToggleSwitch v-model="draft.enabled" :input-id="fid('enabled')" />
          <span>{{ t('help.edit.shown') }}</span>
        </label>
        <small class="app-muted">{{ t('help.edit.shown_hint') }}</small>
      </div>
      <p class="app-muted hint">{{ t('help.edit.langs_hint') }}</p>

      <div class="langs">
        <section v-for="l in HELP_LANGS" :key="l" class="lang" :aria-labelledby="fid('head-' + l)">
          <div class="lang-head">
            <h3 :id="fid('head-' + l)" class="lang-name">{{ t('texts.' + l) }}</h3>
            <Tag :value="stateLabel(l)" :severity="stateSeverity(langState(draft, l))" />
          </div>

          <div class="field">
            <div class="label-row">
              <label :for="fid('title-' + l)">{{ k('title_label') }}</label>
              <span class="counter" :class="{ over: titleLen(l) > titleLimit }">{{ num(titleLen(l), lang) }} / {{ num(titleLimit, lang) }}</span>
            </div>
            <InputText
              :id="fid('title-' + l)"
              v-model="draft.title[l]"
              :dir="l === 'fa' ? 'rtl' : 'ltr'"
              :lang="l"
              :class="'in-' + l"
              autocomplete="off"
              fluid
              :invalid="shown.some((p) => p.field === 'title.' + l)"
            />
            <HelpProblems :problems="problemsAt(shown, 'title.' + l)" />
          </div>

          <div class="field">
            <div class="label-row">
              <label :for="fid('body-' + l)">{{ k('body_label') }}</label>
              <span class="counter" :class="{ over: bodyLen(l) > limits.body }">{{ num(bodyLen(l), lang) }} / {{ num(limits.body, lang) }}</span>
            </div>
            <Textarea
              :id="fid('body-' + l)"
              v-model="draft.body[l]"
              :dir="l === 'fa' ? 'rtl' : 'ltr'"
              :lang="l"
              :class="'in-' + l"
              rows="6"
              auto-resize
              fluid
              :invalid="shown.some((p) => p.field === 'body.' + l)"
            />
            <HelpProblems :problems="problemsAt(shown, 'body.' + l)" />
          </div>

          <div class="block">
            <div class="app-muted small">{{ t('help.preview.title') }}</div>
            <HelpPreview :draft="draft" :lang="l" :limits="limits" />
          </div>
        </section>
      </div>
      <small class="app-muted">{{ t('help.edit.html_hint', markup) }}</small>
      <HelpProblems :problems="problemsAt(shown, '')" />

      <section v-if="kind === 'tutorial'" class="buttons" :aria-labelledby="fid('buttons')">
        <h3 :id="fid('buttons')" class="lang-name">{{ t('help.edit.buttons') }}</h3>
        <p class="app-muted hint">{{ t('help.edit.buttons_hint', { https: markup.https, cap: num(limits.buttons, lang) }) }}</p>
        <div v-for="(b, i) in draft.buttons" :key="i" class="button-row" role="group" :aria-label="t('help.edit.button_n', { n: num(i + 1, lang) })">
          <div class="field url">
            <label :for="fid(`url-${i}`)">{{ t('help.edit.button_url') }}</label>
            <InputText
              :id="fid(`url-${i}`)"
              v-model="b.url"
              dir="ltr"
              inputmode="url"
              autocomplete="off"
              fluid
              :invalid="shown.some((p) => p.field === `buttons.${i}.url`)"
            />
            <HelpProblems :problems="problemsAt(shown, `buttons.${i}.url`)" />
          </div>
          <div v-for="l in HELP_LANGS" :key="l" class="field label">
            <label :for="fid(`label-${i}-${l}`)">{{ t('help.edit.button_label', { lang: t('texts.' + l) }) }}</label>
            <InputText
              :id="fid(`label-${i}-${l}`)"
              v-model="b.label[l]"
              :dir="l === 'fa' ? 'rtl' : 'ltr'"
              :lang="l"
              autocomplete="off"
              fluid
              :invalid="shown.some((p) => p.field === `buttons.${i}.label.${l}`)"
            />
            <HelpProblems :problems="problemsAt(shown, `buttons.${i}.label.${l}`)" />
          </div>
          <Button
            type="button"
            icon="pi pi-trash"
            text
            rounded
            severity="danger"
            class="remove"
            :aria-label="t('help.edit.remove_button', { n: num(i + 1, lang) })"
            @click="removeButton(i)"
          />
        </div>
        <HelpProblems :problems="problemsAt(shown, 'buttons')" />
        <div>
          <Button
            type="button"
            :label="t('help.edit.add_button')"
            icon="pi pi-plus"
            severity="secondary"
            outlined
            :disabled="draft.buttons.length >= limits.buttons"
            @click="addButton"
          />
        </div>
      </section>

      <Message v-if="error" severity="error" :closable="false">
        {{ error }}
        <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
      </Message>
    </form>
    <template #footer>
      <span v-if="checking" class="app-muted small status" aria-live="polite"><i class="pi pi-spin pi-spinner" aria-hidden="true" /> {{ t('texts.checking') }}</span>
      <Button type="button" :label="t('app.cancel')" severity="secondary" outlined @click="close(false)" />
      <Button type="button" :label="t('common.save')" icon="pi pi-check" :loading="saving" :disabled="!canSave" @click="save" />
    </template>
  </Dialog>
</template>

<style scoped>
.form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.shown {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
.switch {
  display: inline-flex;
  align-items: center;
  gap: 0.6rem;
  cursor: pointer;
  font-weight: 500;
}
.hint {
  margin: 0;
  font-size: 0.9rem;
}
.small {
  font-size: 0.8rem;
}
.langs {
  display: grid;
  gap: 1.5rem;
  grid-template-columns: repeat(auto-fit, minmax(min(24rem, 100%), 1fr));
}
.lang {
  display: flex;
  flex-direction: column;
  gap: 0.8rem;
  min-inline-size: 0;
}
.lang-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.lang-name {
  margin: 0;
  font-size: 1rem;
  font-weight: 700;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  min-inline-size: 0;
}
.label-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 0.5rem;
}
.counter {
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
  line-height: 1.8;
}
.in-en {
  font-family: var(--app-font-en);
}
.block {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}
.buttons {
  display: flex;
  flex-direction: column;
  gap: 0.8rem;
}
.button-row {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 1fr) minmax(0, 1fr) auto;
  gap: 0.75rem;
  align-items: start;
  padding: 0.75rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.6rem;
}
.remove {
  margin-block-start: 1.6rem;
}
.detail {
  font-size: 0.85rem;
}
.status {
  margin-inline-end: auto;
}
@media (max-width: 40rem) {
  .button-row {
    grid-template-columns: minmax(0, 1fr);
  }
  .remove {
    margin-block-start: 0;
    justify-self: end;
  }
}
</style>
