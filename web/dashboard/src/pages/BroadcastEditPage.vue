<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import Tabs from 'primevue/tabs'
import TabList from 'primevue/tablist'
import Tab from 'primevue/tab'
import TabPanels from 'primevue/tabpanels'
import TabPanel from 'primevue/tabpanel'
import AudienceForm from '../components/broadcasts/AudienceForm.vue'
import BroadcastText from '../components/broadcasts/BroadcastText.vue'
import ButtonField from '../components/broadcasts/ButtonField.vue'
import BroadcastStatus from '../components/broadcasts/BroadcastStatus.vue'
import SendDialog from '../components/broadcasts/SendDialog.vue'
import { api, ApiError } from '../api'
import { errorText } from '../errors'
import { dateTime, type Lang } from '../format'
import {
  blankDraft,
  bodyOf,
  draftOf,
  draftProblems,
  editable,
  neededLangs,
  sameDraft,
  splitProblems,
  testOutcome,
  urlProblem,
  type Draft,
  type TestOutcome,
  type TextState,
} from '../broadcasts'
import type { BroadcastItem, TextProblem } from '../types'

// Write a broadcast: who gets it, the text in each language they read, an
// optional link button; try it on your own Telegram, save it as a draft, then
// send it now or at a chosen time. Drafts and scheduled broadcasts open here.
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()
const toast = useToast()
const confirm = useConfirm()

const routeId = () => (route.name === 'broadcast-edit' ? String(route.params.id ?? '') : '')
const id = ref(routeId())
const item = ref<BroadcastItem | null>(null)
const loading = ref(false)
const loadError = ref('')
const draft = ref<Draft>(blankDraft())
const saved = ref<Draft>(blankDraft())
const checks = reactive<Partial<Record<Lang, TextState>>>({})
const textProblems = reactive<Record<Lang, TextProblem[]>>({ fa: [], en: [] })
const fieldErrors = ref<Record<string, string>>({})
const showAll = ref(false)
const formError = ref('')
const formDetail = ref('')
const stale = ref(false)
/** notSent: core refused to send because the broadcast changed elsewhere; the latest is shown. */
const notSent = ref(false)

const needed = computed(() => neededLangs(draft.value.language))
const tab = ref<Lang>(lang.value)
watch(
  needed,
  (n) => {
    if (!n.includes(tab.value)) tab.value = n.includes(lang.value) ? lang.value : n[0]
  },
  { immediate: true },
)
/** edited: something typed that is not saved (an untouched new page has nothing). */
const edited = computed(() => (id.value ? !sameDraft(draft.value, saved.value) : !sameDraft(draft.value, blankDraft())))
const isScheduled = computed(() => item.value?.status === 'scheduled')

function resetForm(d: Draft) {
  draft.value = d
  saved.value = JSON.parse(JSON.stringify(d)) as Draft
  textProblems.fa = []
  textProblems.en = []
  fieldErrors.value = {}
  showAll.value = false
  formError.value = ''
  formDetail.value = ''
  stale.value = false
  notSent.value = false
}

/** load reads the broadcast into the form; false when this page does not show it. */
async function load(): Promise<boolean> {
  if (!id.value) {
    item.value = null
    resetForm(blankDraft())
    return true
  }
  loading.value = true
  loadError.value = ''
  try {
    const res = await api<{ item: BroadcastItem }>('GET', `/broadcasts/${id.value}`)
    // Once it is sending (or over), its page shows it instead.
    if (!editable(res.item.status)) {
      void router.replace({ name: 'broadcast', params: { id: res.item.id } })
      return false
    }
    item.value = res.item
    resetForm(draftOf(res.item))
    return true
  } catch (e) {
    loadError.value = errorText(e, t).text
    return false
  } finally {
    loading.value = false
  }
}
onMounted(load)
// The page is reused when the address changes under it (a saved new draft
// gets its own address; that one is already loaded).
watch(
  () => [route.name, route.params.id],
  () => {
    if (route.name !== 'broadcast-new' && route.name !== 'broadcast-edit') return
    const next = routeId()
    if (next === id.value) return
    id.value = next
    void load()
  },
)

// An edit clears what core said about that field.
const fields: Record<string, () => unknown> = {
  'texts.fa': () => draft.value.texts.fa,
  'texts.en': () => draft.value.texts.en,
  'button.url': () => draft.value.button.url,
  'button.labels.fa': () => draft.value.button.labels.fa,
  'button.labels.en': () => draft.value.button.labels.en,
  segment: () => draft.value.segment,
  language: () => draft.value.language,
}
for (const [field, get] of Object.entries(fields)) {
  watch(get, () => {
    if (field in fieldErrors.value) {
      const rest = { ...fieldErrors.value }
      delete rest[field]
      fieldErrors.value = rest
    }
    if (field === 'texts.fa') textProblems.fa = []
    if (field === 'texts.en') textProblems.en = []
  })
}

const problems = computed(() => draftProblems(draft.value, checks))
const anyChecking = computed(() => needed.value.some((l) => checks[l]?.checking))
const langIssue = (l: Lang) =>
  textProblems[l].length > 0 || !!fieldErrors.value['texts.' + l] || checks[l]?.ok === false || (showAll.value && problems.value.includes('texts.' + l))
const textError = (l: Lang) => (textProblems[l].length ? '' : (fieldErrors.value['texts.' + l] ?? ''))

/** applyError puts a refused save's reasons under the fields they name. */
function applyError(e: unknown, put: boolean) {
  formError.value = ''
  formDetail.value = ''
  if (e instanceof ApiError && e.status === 409 && put) {
    stale.value = true
    formDetail.value = e.message
    return
  }
  if (e instanceof ApiError && e.status === 400) {
    const field = typeof e.data.field === 'string' ? e.data.field : ''
    const split = splitProblems(e.data.problems, field)
    textProblems.fa = split.fa
    textProblems.en = split.en
    if (field) fieldErrors.value = { ...fieldErrors.value, [field]: e.message }
    if (field || split.fa.length || split.en.length) {
      showAll.value = true
      formError.value = t('broadcasts.save.fix_first')
      // A problem in the other language's text: show that tab.
      const bad = needed.value.find((l) => split[l].length || field === 'texts.' + l)
      if (bad) tab.value = bad
      return
    }
  }
  const err = errorText(e, t)
  formError.value = err.text
  formDetail.value = err.detail
}

const saving = ref(false)
/** save writes the draft (new, or with the version it was read at) and
 * returns what core keeps; null when it refused. */
async function save(): Promise<BroadcastItem | null> {
  saving.value = true
  formError.value = ''
  formDetail.value = ''
  const body = bodyOf(draft.value)
  const put = !!id.value && !!item.value
  try {
    const res = put
      ? await api<{ item: BroadcastItem }>('PUT', `/broadcasts/${id.value}`, { ...body, version: item.value!.version })
      : await api<{ item: BroadcastItem }>('POST', '/broadcasts', body)
    const fresh = !id.value
    item.value = res.item
    id.value = res.item.id
    resetForm(draftOf(res.item))
    if (fresh) await router.replace({ name: 'broadcast-edit', params: { id: res.item.id } })
    return res.item
  } catch (e) {
    applyError(e, put)
    return null
  } finally {
    saving.value = false
  }
}
async function saveDraft() {
  // A button without its link: core would save the draft without the button.
  if (draft.value.button.on && urlProblem(draft.value.button.url)) {
    showAll.value = true
    formError.value = t('broadcasts.save.fix_first')
    formDetail.value = ''
    return
  }
  if (await save()) {
    toast.add({ severity: 'success', summary: isScheduled.value ? t('broadcasts.save.saved_changes') : t('broadcasts.save.saved'), life: 3000 })
  }
}

// Send to me: the text in the language of the open tab, with the button.
const testing = ref(false)
const test = ref<{ outcome: TestOutcome; detail: string } | null>(null)
const testLang = computed<Lang>(() => (needed.value.includes(tab.value) ? tab.value : needed.value[0]))
async function sendTest() {
  const l = testLang.value
  const text = draft.value.texts[l].trim()
  test.value = null
  if (!text) {
    showAll.value = true
    test.value = { outcome: 'invalid', detail: '' }
    return
  }
  testing.value = true
  try {
    const res = await api<{ ok: boolean; problem?: string; detail?: string }>('POST', '/broadcasts/test', {
      lang: l,
      text,
      button: bodyOf(draft.value).button,
    })
    const outcome = testOutcome(res)
    if (outcome === 'ok') toast.add({ severity: 'success', summary: t('broadcasts.test.ok'), life: 4000 })
    else test.value = { outcome, detail: res.detail ?? '' }
  } catch (e) {
    if (e instanceof ApiError) {
      const outcome = testOutcome(null, e)
      // Core named the field or the problems: they show under the fields.
      if (outcome === 'invalid') applyError(e, false)
      else test.value = { outcome, detail: outcome === 'failed' ? errorText(e, t).detail : '' }
    } else {
      test.value = { outcome: 'failed', detail: '' }
    }
  } finally {
    testing.value = false
  }
}
const testText = computed(() => {
  const o = test.value?.outcome
  if (!o) return ''
  if (o === 'invalid' && !draft.value.texts[testLang.value].trim()) return t('broadcasts.test.need_text')
  return t('broadcasts.test.' + o)
})

// Send now or schedule: save first, then send, from a dialog that says how
// many customers it reaches and when.
const sendOpen = ref(false)
const sendMode = ref<'now' | 'later'>('now')
const sendBusy = ref(false)
const sendError = ref('')
const sendDetail = ref('')
const atError = ref('')
let leaving = false
function openSend(mode: 'now' | 'later') {
  showAll.value = true
  test.value = null
  notSent.value = false
  if (problems.value.length || anyChecking.value) {
    formError.value = anyChecking.value ? t('broadcasts.save.wait_check') : t('broadcasts.save.fix_first')
    formDetail.value = ''
    const bad = needed.value.find((l) => problems.value.includes('texts.' + l))
    if (bad) tab.value = bad
    return
  }
  formError.value = ''
  sendMode.value = mode
  sendError.value = ''
  sendDetail.value = ''
  atError.value = ''
  sendOpen.value = true
}
async function send(at: number | null) {
  sendBusy.value = true
  sendError.value = ''
  sendDetail.value = ''
  atError.value = ''
  try {
    let target = item.value
    if (!id.value || edited.value || !target) {
      target = await save()
      if (!target) {
        sendOpen.value = false
        return
      }
    }
    // With the version this page shows (or just saved): core refuses it when
    // the broadcast changed since, instead of sending what nobody here saw.
    await api<{ item: BroadcastItem }>('POST', `/broadcasts/${target.id}/send`, { at, version: target.version })
    sendOpen.value = false
    toast.add({ severity: 'success', summary: at === null ? t('broadcasts.send.done_now') : t('broadcasts.send.done_later'), life: 4000 })
    leaving = true
    await router.push({ name: 'broadcast', params: { id: target.id } })
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      // Changed elsewhere: show the latest version; sending again is the admin's
      // call. Sent or cancelled meanwhile (its page shows it now): core's reason.
      sendOpen.value = false
      if (await load()) notSent.value = true
      else {
        const err = errorText(e, t)
        toast.add({ severity: 'warn', summary: err.text, detail: err.detail || undefined, life: 8000 })
      }
    } else if (e instanceof ApiError && e.data.field === 'at') atError.value = e.message
    else {
      const err = errorText(e, t)
      sendError.value = err.text
      sendDetail.value = err.detail
    }
  } finally {
    sendBusy.value = false
  }
}

// Leaving with unsaved edits asks first (also for a closing browser tab).
const removeGuard = router.beforeEach((to) => {
  if (leaving || !edited.value || to.fullPath === route.fullPath) return true
  return new Promise<boolean>((resolve) => {
    let answered = false
    const answer = (v: boolean) => {
      if (answered) return
      answered = true
      resolve(v)
    }
    confirm.require({
      header: t('broadcasts.leave.title'),
      message: t('broadcasts.leave.message'),
      acceptProps: { label: t('texts.discard'), severity: 'danger' },
      rejectProps: { label: t('texts.keep_editing'), severity: 'secondary', outlined: true },
      accept: () => answer(true),
      reject: () => answer(false),
      onHide: () => answer(false),
    })
  })
})
function beforeUnload(e: BeforeUnloadEvent) {
  if (!leaving && edited.value) e.preventDefault()
}
window.addEventListener('beforeunload', beforeUnload)
onBeforeUnmount(() => {
  removeGuard()
  window.removeEventListener('beforeunload', beforeUnload)
})

function back() {
  if (id.value && isScheduled.value) void router.push({ name: 'broadcast', params: { id: id.value } })
  else void router.push({ name: 'broadcasts' })
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">
      <Button icon="pi pi-arrow-left" class="app-flip back" text rounded :aria-label="t('common.back')" @click="back" />
      {{ id ? t('broadcasts.title_edit') : t('broadcasts.title_new') }}
      <BroadcastStatus v-if="item" :status="item.status" class="status" />
    </h1>
  </div>
  <Message v-if="loadError" severity="error" :closable="false">
    {{ loadError }}
    <Button :label="t('app.retry')" text size="small" @click="load" />
  </Message>
  <Skeleton v-if="loading" height="20rem" />

  <div v-else-if="!loadError" class="compose">
    <Message v-if="isScheduled && item?.scheduled_at" severity="info" :closable="false" icon="pi pi-clock">
      {{ t('broadcasts.compose.scheduled_note', { time: dateTime(item.scheduled_at, lang) }) }}
    </Message>
    <Message v-if="stale" severity="warn" :closable="false" class="stale">
      {{ t('broadcasts.save.stale') }}
      <div v-if="formDetail" class="app-ltr detail">{{ formDetail }}</div>
      <Button :label="t('broadcasts.save.reload')" icon="pi pi-refresh" size="small" severity="warn" outlined class="reload" @click="load" />
    </Message>
    <Message v-if="notSent" severity="warn" :closable="false" class="not-sent">{{ t('broadcasts.send.stale') }}</Message>

    <Card>
      <template #title>{{ t('broadcasts.audience.title') }}</template>
      <template #content>
        <AudienceForm v-model:segment="draft.segment" v-model:language="draft.language" />
        <small v-if="fieldErrors.segment || fieldErrors.language" class="err app-ltr">{{ fieldErrors.segment || fieldErrors.language }}</small>
      </template>
    </Card>

    <Card>
      <template #title>{{ t('broadcasts.text.title') }}</template>
      <template #content>
        <p class="app-muted intro">{{ needed.length > 1 ? t('broadcasts.text.both_hint') : t('broadcasts.text.one_hint', { lang: t('texts.' + needed[0]) }) }}</p>
        <Tabs v-if="needed.length > 1" :value="tab" @update:value="(v: string | number) => (tab = v as Lang)">
          <TabList>
            <Tab v-for="l in needed" :key="l" :value="l">
              <span class="tab-name" :class="{ issue: langIssue(l) }">
                {{ t('texts.' + l) }}
                <i v-if="langIssue(l)" class="pi pi-exclamation-circle" :aria-label="t('broadcasts.text.has_problem')" />
              </span>
            </Tab>
          </TabList>
          <TabPanels>
            <TabPanel v-for="l in needed" :key="l" :value="l">
              <BroadcastText
                v-model="draft.texts[l]"
                :lang="l"
                :problems="textProblems[l]"
                :error="textError(l)"
                :flag-empty="showAll"
                :button-label="draft.button.on ? draft.button.labels[l] : ''"
                @state="(s) => (checks[l] = s)"
              />
            </TabPanel>
          </TabPanels>
        </Tabs>
        <BroadcastText
          v-else
          v-model="draft.texts[needed[0]]"
          :lang="needed[0]"
          :problems="textProblems[needed[0]]"
          :error="textError(needed[0])"
          :flag-empty="showAll"
          :button-label="draft.button.on ? draft.button.labels[needed[0]] : ''"
          @state="(s) => (checks[needed[0]] = s)"
        />
      </template>
    </Card>

    <Card>
      <template #title>{{ t('broadcasts.button.title') }}</template>
      <template #content>
        <ButtonField v-model:button="draft.button" :langs="needed" :errors="fieldErrors" :show-all="showAll" />
      </template>
    </Card>

    <div class="footer">
      <Message v-if="formError && !stale" severity="error" :closable="false" class="form-error">
        {{ formError }}
        <div v-if="formDetail" class="app-ltr detail">{{ formDetail }}</div>
      </Message>
      <Message v-if="test && testText" :severity="test.outcome === 'blocked' || test.outcome === 'rate_limited' ? 'warn' : 'error'" :closable="false" class="test-result">
        {{ testText }}
        <div v-if="test.detail" class="app-ltr detail">{{ test.detail }}</div>
      </Message>
      <div class="actions">
        <Button
          :label="t('broadcasts.test.send')"
          icon="pi pi-user"
          severity="secondary"
          outlined
          class="test"
          :loading="testing"
          :disabled="saving || sendBusy"
          @click="sendTest"
        />
        <span class="grow" />
        <Button
          :label="isScheduled ? t('broadcasts.save.changes') : t('broadcasts.save.draft')"
          icon="pi pi-save"
          severity="secondary"
          class="save"
          :loading="saving && !sendOpen"
          :disabled="!edited || saving || sendBusy"
          @click="saveDraft"
        />
        <Button :label="t('broadcasts.send.schedule')" icon="pi pi-clock" outlined class="schedule" :disabled="saving || sendBusy" @click="openSend('later')" />
        <Button :label="t('broadcasts.send.now')" icon="pi pi-send" class="send-now" :disabled="saving || sendBusy" @click="openSend('now')" />
      </div>
      <p class="app-muted small">{{ t('broadcasts.test.hint', { lang: t('texts.' + testLang) }) }}</p>
    </div>
  </div>

  <SendDialog
    v-model:visible="sendOpen"
    :segment="draft.segment"
    :language="draft.language"
    :mode="sendMode"
    :at="item?.scheduled_at ?? null"
    :busy="sendBusy"
    :error="sendError"
    :detail="sendDetail"
    :at-error="atError"
    @confirm="send"
  />
</template>

<style scoped>
.app-page-title {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.35rem;
}
.status {
  font-size: 0.8rem;
}
.compose {
  display: flex;
  flex-direction: column;
  gap: var(--app-gap);
  max-inline-size: 60rem;
}
.intro {
  margin-block: 0 0.75rem;
  font-size: 0.9rem;
}
.tab-name {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
}
.tab-name.issue i {
  color: var(--p-message-error-simple-color, var(--p-red-600));
  font-size: 0.85rem;
}
.footer {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  padding: 0.85rem 1rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--app-radius);
  background: var(--p-content-background);
  position: sticky;
  inset-block-end: 0.5rem;
  z-index: 1;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  align-items: center;
}
.grow {
  flex: 1 1 0;
}
.small {
  font-size: 0.8rem;
  margin: 0;
}
.err {
  display: block;
  margin-block-start: 0.5rem;
  color: var(--p-message-error-simple-color, var(--p-red-600));
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
}
.reload {
  margin-block-start: 0.5rem;
}
@media (max-width: 480px) {
  /* on a phone the bar would cover a third of the screen: it scrolls along */
  .footer {
    position: static;
  }
  .actions > :deep(.p-button) {
    flex: 1 1 auto;
  }
  .grow {
    display: none;
  }
}
</style>
