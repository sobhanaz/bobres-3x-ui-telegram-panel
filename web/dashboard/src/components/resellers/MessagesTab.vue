<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Message from 'primevue/message'
import RadioButton from 'primevue/radiobutton'
import Select from 'primevue/select'
import ToggleSwitch from 'primevue/toggleswitch'
import Tabs from 'primevue/tabs'
import TabList from 'primevue/tablist'
import Tab from 'primevue/tab'
import BroadcastText from '../broadcasts/BroadcastText.vue'
import ButtonField from '../broadcasts/ButtonField.vue'
import TextPreview from '../branding/TextPreview.vue'
import ReminderCard from './ReminderCard.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { contentLang, num, type ContentLang, type Lang } from '../../format'
import { AUDIENCE_LANGS, splitProblems, testOutcome, timing, type AudienceLang, type TestOutcome } from '../../broadcasts'
import {
  PLACEHOLDERS,
  audienceBody,
  blankResellerDraft,
  draftLangs,
  insertPlaceholder,
  messageBody,
  resellerDraftProblems,
  type Placeholder,
  type ResellerDraft,
  type Target,
} from '../../resellerMessages'
import type { AudienceCount, BroadcastItem, Page, ResellerItem, TextProblem, TierItem, UserBrief } from '../../types'

// The Resellers page's Messages tab: a message to resellers only (all of them, those in debt, or one tier), written once per language with
// placeholders that core fills in for each reseller, previewed as one chosen reseller gets it, tried on your own Telegram, then sent now
// through the broadcasts (its page shows the progress). Below it, the weekly settlement reminder.
const props = defineProps<{ tiers: TierItem[] }>()
const { t, locale } = useI18n()
const viewer = computed(() => locale.value as Lang)
const router = useRouter()
const toast = useToast()
const confirm = useConfirm()
const uid = useId()

const TARGETS: readonly Target[] = ['debt', 'all', 'tier']
const draft = ref<ResellerDraft>(blankResellerDraft())
const needed = computed(() => draftLangs(draft.value))
const tab = ref<ContentLang>(contentLang(viewer.value))
watch(needed, (n) => {
  if (!n.includes(tab.value)) tab.value = n.includes(contentLang(viewer.value)) ? contentLang(viewer.value) : n[0]
})
const textProblems = reactive<Record<ContentLang, TextProblem[]>>({ fa: [], en: [] })
const fieldErrors = ref<Record<string, string>>({})
const showAll = ref(false)
const formError = ref('')
watch(
  () => [draft.value.texts.fa, draft.value.texts.en],
  () => {
    textProblems.fa = []
    textProblems.en = []
  },
)

const tierOptions = computed(() => props.tiers.map((tr) => ({ label: tr.name, value: tr.id })))
const langOptions = computed(() => AUDIENCE_LANGS.map((l) => ({ label: t('broadcasts.language.' + (l || 'all')), value: l || 'all' })))
const langChoice = computed({
  get: () => draft.value.language || 'all',
  set: (v: string) => (draft.value.language = (v === 'all' ? '' : v) as AudienceLang),
})

// The live count of who it reaches.
const count = ref<AudienceCount | null>(null)
const counting = ref(false)
let countSeq = 0
let countTimer: ReturnType<typeof setTimeout> | undefined
function recount(delay: number) {
  clearTimeout(countTimer)
  const mine = ++countSeq
  const body = audienceBody(draft.value)
  if (draft.value.target === 'tier' && !body.tier_id) {
    count.value = null
    counting.value = false
    return
  }
  counting.value = true
  countTimer = setTimeout(async () => {
    try {
      const res = await api<AudienceCount>('POST', '/reseller-messages/audience', body)
      if (mine === countSeq) count.value = res
    } catch {
      if (mine === countSeq) count.value = null
    } finally {
      if (mine === countSeq) counting.value = false
    }
  }, delay)
}
watch(() => JSON.stringify(audienceBody(draft.value)), () => recount(timing.audience))

// The reseller the preview (and the test) is written for: one picked, or the first of the audience.
const resellers = ref<UserBrief[]>([])
const previewFor = ref('')
const resellerOptions = computed(() => [
  { label: t('reseller.messages.preview_first'), value: '' },
  ...resellers.value.map((u) => ({ label: u.username ? '@' + u.username : String(u.telegram_id), value: u.id })),
])
async function loadResellers() {
  try {
    resellers.value = (await api<Page<ResellerItem>>('GET', '/resellers?size=100')).items.map((r) => r.user)
  } catch {
    resellers.value = []
  }
}

const preview = ref('')
const previewAs = ref<UserBrief | null>(null)
const previewing = ref(false)
let previewSeq = 0
let previewTimer: ReturnType<typeof setTimeout> | undefined
function repreview(delay: number) {
  clearTimeout(previewTimer)
  const mine = ++previewSeq
  const text = draft.value.texts[tab.value]
  if (!text.trim()) {
    preview.value = ''
    previewing.value = false
    return
  }
  previewing.value = true
  previewTimer = setTimeout(async () => {
    try {
      const res = await api<{ text: string; reseller: UserBrief | null }>('POST', '/reseller-messages/preview', {
        ...audienceBody(draft.value),
        reseller_id: previewFor.value,
        lang: tab.value,
        text,
      })
      if (mine === previewSeq) {
        preview.value = res.text
        previewAs.value = res.reseller
      }
    } catch {
      if (mine === previewSeq) preview.value = ''
    } finally {
      if (mine === previewSeq) previewing.value = false
    }
  }, delay)
}
watch(
  () => [draft.value.texts[tab.value], tab.value, previewFor.value, JSON.stringify(audienceBody(draft.value))],
  () => repreview(timing.check),
)

onMounted(() => {
  recount(0)
  void loadResellers()
})
onBeforeUnmount(() => {
  clearTimeout(countTimer)
  clearTimeout(previewTimer)
  countSeq++
  previewSeq++
})

function add(p: Placeholder) {
  draft.value.texts[tab.value] = insertPlaceholder(draft.value.texts[tab.value], p).text
}

const problems = computed(() => resellerDraftProblems(draft.value))

/** applyError puts a refused request's reasons under the fields they name. */
function applyError(e: unknown) {
  formError.value = ''
  if (e instanceof ApiError && e.status === 400) {
    const field = typeof e.data.field === 'string' ? e.data.field : ''
    const split = splitProblems(e.data.problems, field)
    textProblems.fa = split.fa
    textProblems.en = split.en
    if (field) fieldErrors.value = { ...fieldErrors.value, [field]: e.message }
    showAll.value = true
    formError.value = t('broadcasts.save.fix_first')
    const bad = needed.value.find((l) => split[l].length || field === 'texts.' + l)
    if (bad) tab.value = bad
    return
  }
  formError.value = errorText(e, t).text
}

// Send to me: the open tab's text as the previewed reseller gets it, with its buttons.
const testing = ref(false)
const test = ref<TestOutcome | null>(null)
async function sendTest() {
  test.value = null
  const text = draft.value.texts[tab.value].trim()
  if (!text) {
    showAll.value = true
    test.value = 'invalid'
    return
  }
  testing.value = true
  try {
    const body = messageBody(draft.value)
    const res = await api<{ ok: boolean; problem?: string }>('POST', '/reseller-messages/test', {
      ...audienceBody(draft.value),
      reseller_id: previewFor.value,
      lang: tab.value,
      text,
      button: body.button,
      settle_button: body.settle_button,
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

// Send now: asked first, with how many resellers it reaches.
const sending = ref(false)
function askSend() {
  showAll.value = true
  test.value = null
  if (problems.value.length) {
    formError.value = t('broadcasts.save.fix_first')
    const bad = needed.value.find((l) => problems.value.includes('texts.' + l))
    if (bad) tab.value = bad
    return
  }
  formError.value = ''
  const n = count.value?.total ?? 0
  confirm.require({
    header: t('reseller.messages.send_title'),
    message: n ? t('reseller.messages.send_message', { n: num(n, viewer.value) }, n) : t('reseller.messages.send_nobody'),
    acceptProps: { label: t('broadcasts.send.now'), icon: 'pi pi-send' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: () => void send(),
  })
}
async function send() {
  sending.value = true
  try {
    const res = await api<{ item: BroadcastItem }>('POST', '/reseller-messages', messageBody(draft.value))
    toast.add({ severity: 'success', summary: t('broadcasts.send.done_now'), life: 4000 })
    await router.push({ name: 'broadcast', params: { id: res.item.id } })
  } catch (e) {
    applyError(e)
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <div class="messages">
    <Card>
      <template #title>{{ t('reseller.messages.title') }}</template>
      <template #subtitle>{{ t('reseller.messages.intro') }}</template>
      <template #content>
        <fieldset class="targets">
          <legend class="legend">{{ t('reseller.messages.who') }}</legend>
          <label v-for="tg in TARGETS" :key="tg" :for="`${uid}-t-${tg}`" class="target" :class="{ on: draft.target === tg }">
            <RadioButton v-model="draft.target" :input-id="`${uid}-t-${tg}`" :name="`${uid}-target`" :value="tg" />
            <span>{{ t('reseller.messages.target.' + tg) }}</span>
          </label>
        </fieldset>
        <div class="row">
          <Select
            v-if="draft.target === 'tier'"
            v-model="draft.tierId"
            :options="tierOptions"
            option-label="label"
            option-value="value"
            :placeholder="t('reseller.pick_tier')"
            :aria-label="t('reseller.tier')"
            :invalid="showAll && !draft.tierId"
            class="tier"
          />
          <Select v-model="langChoice" :options="langOptions" option-label="label" option-value="value" :aria-label="t('broadcasts.audience.language_label')" class="lang" />
          <span class="app-muted count" aria-live="polite">
            <template v-if="counting">{{ t('broadcasts.audience.counting') }}</template>
            <template v-else-if="count && !count.total">{{ t('reseller.messages.send_nobody') }}</template>
            <template v-else-if="count">{{ t('reseller.messages.count', { n: num(count.total, viewer) }) }}</template>
          </span>
        </div>
        <p class="app-muted small">{{ t('reseller.messages.left_out') }}</p>
      </template>
    </Card>

    <Card>
      <template #title>{{ t('broadcasts.text.title') }}</template>
      <template #content>
        <p class="app-muted small intro">{{ t('reseller.messages.placeholders_hint') }}</p>
        <div class="chips" role="group" :aria-label="t('reseller.messages.placeholders')">
          <Button
            v-for="p in PLACEHOLDERS"
            :key="p"
            size="small"
            severity="secondary"
            outlined
            class="chip"
            :aria-label="t('reseller.messages.insert', { what: t('reseller.messages.ph.' + p) })"
            @click="add(p)"
          >
            <span class="app-ltr app-mono">{{ '{' + p + '}' }}</span>
            <span class="app-muted">{{ t('reseller.messages.ph.' + p) }}</span>
          </Button>
        </div>
        <Tabs v-if="needed.length > 1" :value="tab" @update:value="(v: string | number) => (tab = v as ContentLang)">
          <TabList>
            <Tab v-for="l in needed" :key="l" :value="l">{{ t('texts.' + l) }}</Tab>
          </TabList>
        </Tabs>
        <BroadcastText
          :key="tab"
          v-model="draft.texts[tab]"
          :lang="tab"
          :problems="textProblems[tab]"
          :error="fieldErrors['texts.' + tab] ?? ''"
          :flag-empty="showAll"
          check-path="/reseller-messages/check"
          no-preview
        />
        <div class="preview-head">
          <span class="app-muted small">{{ t('reseller.messages.preview_as') }}</span>
          <Select v-model="previewFor" :options="resellerOptions" option-label="label" option-value="value" filter :aria-label="t('reseller.messages.preview_as')" class="who" />
        </div>
        <div class="bubble" aria-live="polite">
          <span v-if="previewing" class="app-muted small"><i class="pi pi-spin pi-spinner" aria-hidden="true" /></span>
          <template v-else-if="preview">
            <TextPreview :value="preview" :html="true" :placeholders="[]" :lang="tab" kind="message" class="rendered" />
            <TextPreview v-if="draft.button.on && draft.button.labels[tab].trim()" :value="draft.button.labels[tab]" :html="false" :placeholders="[]" :lang="tab" kind="button" />
            <TextPreview v-if="draft.settle" :value="t('reseller.messages.settle_label_' + tab)" :html="false" :placeholders="[]" :lang="tab" kind="button" />
          </template>
          <span v-else class="app-muted small">{{ t('reseller.messages.preview_empty') }}</span>
        </div>
        <p v-if="preview && !previewAs" class="app-muted small">{{ t('reseller.messages.preview_sample') }}</p>
      </template>
    </Card>

    <Card>
      <template #title>{{ t('broadcasts.button.title') }}</template>
      <template #content>
        <label class="switch">
          <ToggleSwitch v-model="draft.settle" :input-id="`${uid}-settle`" />
          <span>{{ t('reseller.messages.settle') }}</span>
        </label>
        <p class="app-muted small">{{ t('reseller.messages.settle_hint') }}</p>
        <ButtonField v-model:button="draft.button" :langs="needed" :errors="fieldErrors" :show-all="showAll" />
      </template>
    </Card>

    <div class="footer">
      <Message v-if="formError" severity="error" :closable="false" class="form-error">{{ formError }}</Message>
      <Message v-if="test" :severity="test === 'blocked' || test === 'rate_limited' ? 'warn' : 'error'" :closable="false" class="test-result">
        {{ t('broadcasts.test.' + test) }}
      </Message>
      <div class="actions">
        <Button :label="t('broadcasts.test.send')" icon="pi pi-user" severity="secondary" outlined class="test" :loading="testing" :disabled="sending" @click="sendTest" />
        <span class="grow" />
        <Button :label="t('broadcasts.send.now')" icon="pi pi-send" class="send-now" :loading="sending" :disabled="testing" @click="askSend" />
      </div>
      <p class="app-muted small">{{ t('reseller.messages.test_hint', { lang: t('texts.' + tab) }) }}</p>
    </div>

    <ReminderCard :resellers="resellerOptions" />
  </div>
</template>

<style scoped>
.messages {
  display: flex;
  flex-direction: column;
  gap: var(--app-gap);
  max-inline-size: 60rem;
}
.targets {
  border: 0;
  padding: 0;
  margin: 0 0 0.75rem;
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
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--app-radius);
  cursor: pointer;
}
.target.on {
  border-color: var(--p-primary-color);
}
.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
.tier,
.lang,
.who {
  min-inline-size: 12rem;
}
.count {
  font-size: 0.9rem;
}
.small {
  font-size: 0.8rem;
}
.intro {
  margin-block: 0 0.5rem;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
  margin-block-end: 0.75rem;
}
.chip {
  gap: 0.35rem;
}
.preview-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  margin-block: 0.75rem 0.35rem;
}
.bubble {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  max-inline-size: 30rem;
  min-block-size: 2rem;
}
.switch {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  cursor: pointer;
}
.footer {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  padding: 0.85rem 1rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--app-radius);
  background: var(--p-content-background);
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
@media (max-width: 480px) {
  .actions > :deep(.p-button) {
    flex: 1 1 auto;
  }
  .grow {
    display: none;
  }
  .tier,
  .lang,
  .who {
    min-inline-size: 0;
    inline-size: 100%;
  }
}
</style>
