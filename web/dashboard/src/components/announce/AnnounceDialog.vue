<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import Checkbox from 'primevue/checkbox'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import Tabs from 'primevue/tabs'
import TabList from 'primevue/tablist'
import Tab from 'primevue/tab'
import BroadcastText from '../broadcasts/BroadcastText.vue'
import TextPreview from '../branding/TextPreview.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { contentLang, dateTime, num, type ContentLang, type Lang } from '../../format'
import { splitProblems, testOutcome, timing, type TestOutcome } from '../../broadcasts'
import {
  ANNOUNCE_LANGS,
  ANNOUNCE_PLACEHOLDERS,
  announceProblems,
  channelProblemKey,
  draftFrom,
  refusalOf,
  sendBody,
  statusSeverity,
  type AnnounceDraft,
  type AnnounceDraftReply,
  type AnnounceKind,
  type AnnouncePlaceholder,
  type AnnouncementItem,
} from '../../announce'
import type { TextProblem } from '../../types'

// The Announce dialog: a card for one product (prefilled by core from the product, with placeholders, in every language), where it goes
// (the store's channel and/or private messages to the customers who said yes to product news), a preview of both, "send to me", and the
// send itself, asked first with how many customers it reaches. Core holds the limits (a few a day, quiet hours, not the same product twice in
// 24 hours) and says when one stops it.
const props = defineProps<{ kind: AnnounceKind; productId: string; title: string }>()
const visible = defineModel<boolean>('visible', { required: true })
const { t, locale } = useI18n()
const viewer = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()
const uid = useId()

const reply = ref<AnnounceDraftReply | null>(null)
const loadError = ref('')
const loading = ref(false)
const draft = ref<AnnounceDraft>({ texts: { fa: '', en: '' }, channel: false, private: true })
const tab = ref<ContentLang>(contentLang(viewer.value))
const textProblems = reactive<Record<ContentLang, TextProblem[]>>({ fa: [], en: [] })
const fieldErrors = ref<Record<string, string>>({})
const formError = ref('')
const showAll = ref(false)
const channelCheck = ref<{ ok: boolean; problem: string; detail: string; chat: string } | null>(null)
const checkingChannel = ref(false)

async function load() {
  loading.value = true
  loadError.value = ''
  reply.value = null
  channelCheck.value = null
  formError.value = ''
  fieldErrors.value = {}
  textProblems.fa = []
  textProblems.en = []
  showAll.value = false
  test.value = null
  try {
    const r = await api<AnnounceDraftReply>('GET', `/announcements/draft?kind=${encodeURIComponent(props.kind)}&id=${encodeURIComponent(props.productId)}`)
    reply.value = r
    draft.value = draftFrom(r)
    tab.value = contentLang(viewer.value)
    repreview(0)
    if (r.settings.enabled && r.settings.channel) void checkChannel()
  } catch (e) {
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}

async function checkChannel() {
  checkingChannel.value = true
  try {
    channelCheck.value = await api('POST', '/announcements/channel-check', {})
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      channelCheck.value = { ok: false, problem: String(e.data.problem ?? ''), detail: '', chat: '' }
    } else channelCheck.value = { ok: false, problem: 'error', detail: errorText(e, t).text, chat: '' }
  } finally {
    checkingChannel.value = false
  }
}

function add(p: AnnouncePlaceholder) {
  const text = draft.value.texts[tab.value]
  const gap = text && !/\s$/.test(text) ? ' ' : ''
  draft.value.texts[tab.value] = text + gap + '{' + p + '}'
}

// The preview: the private message in the open tab's language and the channel post, written by core with the product's values now.
const preview = ref<{ text: string; channel: string; channel_lang: string; buy_label: string } | null>(null)
const previewing = ref(false)
let previewTimer: ReturnType<typeof setTimeout> | undefined
let previewSeq = 0
function repreview(delay: number) {
  clearTimeout(previewTimer)
  const mine = ++previewSeq
  previewTimer = setTimeout(async () => {
    if (!draft.value.texts[tab.value].trim()) {
      preview.value = null
      return
    }
    previewing.value = true
    try {
      const res = await api<{ text: string; channel: string; channel_lang: string; buy_label: string }>('POST', '/announcements/preview', {
        kind: props.kind,
        id: props.productId,
        lang: tab.value,
        texts: draft.value.texts,
      })
      if (mine === previewSeq) preview.value = res
    } catch {
      if (mine === previewSeq) preview.value = null
    } finally {
      if (mine === previewSeq) previewing.value = false
    }
  }, delay)
}
watch(() => [draft.value.texts.fa, draft.value.texts.en, tab.value], () => repreview(timing.check))
onBeforeUnmount(() => {
  clearTimeout(previewTimer)
  previewSeq++
})

const problems = computed(() => announceProblems(draft.value))
const channelBlocked = computed(() => draft.value.channel && channelCheck.value !== null && !channelCheck.value.ok)

function applyError(e: unknown) {
  formError.value = ''
  if (e instanceof ApiError && e.status === 400) {
    const field = typeof e.data.field === 'string' ? e.data.field : ''
    const split = splitProblems(e.data.problems, field)
    textProblems.fa = split.fa
    textProblems.en = split.en
    if (field) fieldErrors.value = { ...fieldErrors.value, [field]: e.message }
    showAll.value = true
    formError.value = field && !field.startsWith('texts') ? e.message : t('broadcasts.save.fix_first')
    const bad = ANNOUNCE_LANGS.find((l) => split[l].length || field === 'texts.' + l)
    if (bad) tab.value = bad
    return
  }
  formError.value = errorText(e, t).text
}

// Send to me: the open tab's card as a customer gets it, with its Buy button.
const testing = ref(false)
const test = ref<TestOutcome | null>(null)
async function sendTest() {
  test.value = null
  if (!draft.value.texts[tab.value].trim()) {
    showAll.value = true
    test.value = 'invalid'
    return
  }
  testing.value = true
  try {
    const res = await api<{ ok: boolean; problem?: string }>('POST', '/announcements/test', {
      kind: props.kind,
      id: props.productId,
      lang: tab.value,
      texts: draft.value.texts,
    })
    const outcome = testOutcome(res)
    if (outcome === 'ok') toast.add({ severity: 'success', summary: t('broadcasts.test.ok'), life: 4000 })
    else test.value = outcome
  } catch (e) {
    if (e instanceof ApiError) {
      const outcome = testOutcome(null, e)
      if (outcome === 'invalid') applyError(e)
      else test.value = outcome
    } else test.value = 'failed'
  } finally {
    testing.value = false
  }
}

// Send: asked first, with where it goes and how many customers get the private message.
const sending = ref(false)
function askSend(force = false) {
  showAll.value = true
  test.value = null
  if (problems.value.length) {
    formError.value = problems.value.includes('targets') ? t('announce.pick_target') : t('broadcasts.save.fix_first')
    const bad = ANNOUNCE_LANGS.find((l) => problems.value.includes('texts.' + l))
    if (bad) tab.value = bad
    return
  }
  formError.value = ''
  const n = reply.value?.audience.total ?? 0
  const parts: string[] = []
  if (draft.value.channel) parts.push(t('announce.confirm_channel', { chat: reply.value?.settings.channel ?? '' }))
  if (draft.value.private) parts.push(n ? t('announce.confirm_private', { n: num(n, viewer.value) }, n) : t('announce.confirm_private_nobody'))
  confirm.require({
    header: t('announce.confirm_title'),
    message: parts.join(' '),
    acceptProps: { label: t('announce.send'), icon: 'pi pi-megaphone' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: () => void send(force),
  })
}
async function send(force: boolean) {
  sending.value = true
  try {
    const res = await api<{ announcement: AnnouncementItem; queued: boolean }>('POST', '/announcements', sendBody(props.kind, props.productId, draft.value, force))
    toast.add({
      severity: 'success',
      summary: res.queued ? t('announce.queued', { at: dateTime(res.announcement.due_at, viewer.value) }) : t('announce.sent'),
      life: 5000,
    })
    visible.value = false
  } catch (e) {
    const refusal = e instanceof ApiError ? refusalOf(e.status, e.data) : null
    if (refusal?.kind === 'repeat') {
      confirm.require({
        header: t('announce.repeat_title'),
        message: t('announce.repeat_message', { at: dateTime(refusal.lastAt, viewer.value) }),
        acceptProps: { label: t('announce.send_anyway'), severity: 'warn' },
        rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
        accept: () => void send(true),
      })
    } else if (refusal?.kind === 'limit') {
      formError.value = t('announce.limit', { n: refusal.max, day: refusal.day })
    } else if (refusal?.kind === 'channel') {
      channelCheck.value = { ok: false, problem: refusal.problem, detail: refusal.detail, chat: refusal.chat }
      formError.value = t(channelProblemKey(refusal.problem), { chat: refusal.chat || reply.value?.settings.channel || '' })
    } else applyError(e)
  } finally {
    sending.value = false
  }
}

const settings = computed(() => reply.value?.settings)
const channelLangLabel = computed(() =>
  settings.value?.channel_langs === 'both' ? t('announce.channel_both') : t('announce.channel_default', { lang: t('texts.' + (settings.value?.default_language ?? 'fa')) }),
)

// Each opening reads the product, the settings and the audience again (last: everything it resets is declared above).
watch(visible, (v) => v && void load(), { immediate: true })
</script>

<template>
  <Dialog v-model:visible="visible" modal :header="t('announce.title', { name: title })" :style="{ inlineSize: 'min(46rem, 96vw)' }" :draggable="false">
    <p v-if="loading" class="app-muted"><i class="pi pi-spin pi-spinner" aria-hidden="true" /> {{ t('app.loading') }}</p>
    <Message v-else-if="loadError" severity="error" :closable="false">{{ loadError }}</Message>
    <div v-else-if="reply" class="announce">
      <Message v-if="!settings?.enabled" severity="warn" :closable="false" class="off">
        {{ t('announce.off') }}
        <RouterLink :to="{ name: 'settings' }">{{ t('announce.open_settings') }}</RouterLink>
      </Message>
      <p class="app-muted small">{{ t('announce.intro', { n: settings?.daily_max ?? 0, start: settings?.quiet_start, end: settings?.quiet_end }) }}</p>

      <fieldset class="targets">
        <legend class="legend">{{ t('announce.where') }}</legend>
        <label :for="`${uid}-ch`" class="target">
          <Checkbox v-model="draft.channel" binary :input-id="`${uid}-ch`" :disabled="!settings?.channel" />
          <span>
            {{ t('announce.to_channel') }}
            <bdi v-if="settings?.channel" class="app-ltr app-mono">{{ settings.channel }}</bdi>
            <span class="app-muted small block">{{ settings?.channel ? channelLangLabel : t('announce.no_channel') }}</span>
          </span>
        </label>
        <label :for="`${uid}-pm`" class="target">
          <Checkbox v-model="draft.private" binary :input-id="`${uid}-pm`" />
          <span>
            {{ t('announce.to_private') }}
            <span class="app-muted small block">{{ t('announce.audience', { n: num(reply.audience.total, viewer) }, reply.audience.total) }}</span>
          </span>
        </label>
      </fieldset>
      <p v-if="checkingChannel" class="app-muted small"><i class="pi pi-spin pi-spinner" aria-hidden="true" /> {{ t('announce.checking_channel') }}</p>
      <Message v-else-if="draft.channel && channelBlocked" severity="warn" :closable="false" class="channel-problem">
        {{ t(channelProblemKey(channelCheck?.problem ?? ''), { chat: settings?.channel ?? '' }) }}
        <span v-if="channelCheck?.detail" class="app-muted small block app-ltr">{{ channelCheck.detail }}</span>
      </Message>

      <p class="app-muted small hint">{{ t('announce.placeholders_hint') }}</p>
      <div class="chips" role="group" :aria-label="t('announce.placeholders')">
        <Button
          v-for="p in ANNOUNCE_PLACEHOLDERS"
          :key="p"
          size="small"
          severity="secondary"
          outlined
          class="chip"
          :aria-label="t('reseller.messages.insert', { what: t('announce.ph.' + p) })"
          @click="add(p)"
        >
          <span class="app-ltr app-mono">{{ '{' + p + '}' }}</span>
          <span class="app-muted">{{ t('announce.ph.' + p) }}</span>
        </Button>
      </div>
      <Tabs :value="tab" @update:value="(v: string | number) => (tab = v as ContentLang)">
        <TabList>
          <Tab v-for="l in ANNOUNCE_LANGS" :key="l" :value="l">{{ t('texts.' + l) }}</Tab>
        </TabList>
      </Tabs>
      <BroadcastText
        :key="tab"
        v-model="draft.texts[tab]"
        :lang="tab"
        :problems="textProblems[tab]"
        :error="fieldErrors['texts.' + tab] ?? ''"
        :flag-empty="showAll"
        check-path="/announcements/check"
        no-preview
      />

      <div class="previews">
        <section v-if="draft.private" class="bubble" aria-live="polite">
          <h3 class="ptitle">{{ t('announce.preview_private', { lang: t('texts.' + tab) }) }}</h3>
          <span v-if="previewing" class="app-muted small"><i class="pi pi-spin pi-spinner" aria-hidden="true" /></span>
          <template v-else-if="preview">
            <TextPreview :value="preview.text" :html="true" :placeholders="[]" :lang="tab" kind="message" />
            <TextPreview :value="preview.buy_label" :html="false" :placeholders="[]" :lang="tab" kind="button" />
          </template>
          <span v-else class="app-muted small">{{ t('announce.preview_empty') }}</span>
        </section>
        <section v-if="draft.channel && settings?.channel" class="bubble" aria-live="polite">
          <h3 class="ptitle">{{ t('announce.preview_channel') }}</h3>
          <template v-if="preview && preview.channel">
            <TextPreview :value="preview.channel" :html="true" :placeholders="[]" :lang="settings.default_language" kind="message" />
            <TextPreview :value="reply.buy_labels[settings.default_language] ?? ''" :html="false" :placeholders="[]" :lang="settings.default_language" kind="button" />
          </template>
          <span v-else class="app-muted small">{{ t('announce.preview_channel_empty') }}</span>
        </section>
      </div>

      <section v-if="reply.recent.length" class="recent">
        <h3 class="ptitle">{{ t('announce.recent') }}</h3>
        <ul class="list">
          <li v-for="a in reply.recent" :key="a.id">
            <Tag :severity="statusSeverity(a)" :value="t('announce.status.' + a.status)" />
            <span>{{ dateTime(a.status === 'queued' ? a.due_at : (a.sent_at ?? a.created_at), viewer) }}</span>
            <span class="app-muted small">
              <template v-if="a.to_channel">{{ t('announce.channel_status.' + a.channel_status) }}</template>
              <template v-if="a.to_channel && a.to_private"> · </template>
              <template v-if="a.to_private">{{ t('announce.to_private') }}</template>
            </span>
          </li>
        </ul>
      </section>

      <Message v-if="formError" severity="error" :closable="false">{{ formError }}</Message>
      <Message v-if="test" :severity="test === 'blocked' || test === 'rate_limited' ? 'warn' : 'error'" :closable="false">
        {{ t('broadcasts.test.' + test) }}
      </Message>
    </div>
    <template #footer>
      <Button :label="t('broadcasts.test.send')" icon="pi pi-user" severity="secondary" outlined :loading="testing" :disabled="!reply || sending" @click="sendTest" />
      <Button
        :label="t('announce.send')"
        icon="pi pi-megaphone"
        :loading="sending"
        :disabled="!reply || !settings?.enabled || testing || channelBlocked"
        @click="askSend(false)"
      />
    </template>
  </Dialog>
</template>

<style scoped>
.announce {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.small {
  font-size: 0.8rem;
}
.block {
  display: block;
}
.targets {
  border: 0;
  padding: 0;
  margin: 0;
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.legend {
  font-weight: 600;
  margin-block-end: 0.5rem;
}
.target {
  display: inline-flex;
  align-items: flex-start;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--app-radius);
  cursor: pointer;
  flex: 1 1 14rem;
}
.hint {
  margin: 0;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
}
.chip {
  gap: 0.35rem;
}
.previews {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(16rem, 1fr));
  gap: 0.75rem;
}
.bubble {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  min-block-size: 2rem;
}
.ptitle {
  font-size: 0.85rem;
  font-weight: 600;
  margin: 0;
}
.list {
  list-style: none;
  padding: 0;
  margin: 0.35rem 0 0;
  display: grid;
  gap: 0.35rem;
}
.list li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
</style>
