<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import ProgressBar from 'primevue/progressbar'
import Tabs from 'primevue/tabs'
import TabList from 'primevue/tablist'
import Tab from 'primevue/tab'
import TabPanels from 'primevue/tabpanels'
import TabPanel from 'primevue/tabpanel'
import UserLink from '../components/UserLink.vue'
import TextPreview from '../components/branding/TextPreview.vue'
import BroadcastStatus from '../components/broadcasts/BroadcastStatus.vue'
import SendDialog from '../components/broadcasts/SendDialog.vue'
import RecipientsTab from '../components/broadcasts/RecipientsTab.vue'
import { api, ApiError } from '../api'
import { errorText } from '../errors'
import { useAuth } from '../stores/auth'
import { dateTime, num, type Lang } from '../format'
import {
  actionsFor,
  duration,
  editable,
  live,
  neededLangs,
  percent,
  poller,
  progress,
  rate,
  timing,
  type BroadcastAction,
} from '../broadcasts'
import type { BroadcastItem } from '../types'

// One broadcast: what it says and to whom, how far it got (refreshed every
// couple of seconds while it may still change), what can be done with it
// now, and the customers it could not reach.
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()
const toast = useToast()
const confirm = useConfirm()
const auth = useAuth()
const id = computed(() => String(route.params.id ?? ''))

const item = ref<BroadcastItem | null>(null)
const loadError = ref('')
async function load() {
  try {
    item.value = (await api<{ item: BroadcastItem }>('GET', `/broadcasts/${id.value}`)).item
    loadError.value = ''
  } catch (e) {
    // A failed refresh keeps what is shown; only the first load shows the error.
    if (!item.value || (e instanceof ApiError && e.status === 404)) loadError.value = errorText(e, t).text
    throw e
  }
}
const poll = poller(load, () => timing.poll)
watch(
  () => item.value?.status,
  (s) => (s && live(s) ? poll.start() : poll.stop()),
)
onMounted(() => void load().catch(() => {}))
onBeforeUnmount(poll.stop)
watch(id, () => {
  item.value = null
  loadError.value = ''
  sendFor.value = null
  notSent.value = false
  void load().catch(() => {})
})

const actions = computed(() => (item.value ? actionsFor(item.value.status) : []))
const has = (a: BroadcastAction) => actions.value.includes(a)
const counts = computed(() => item.value?.counts ?? { total: 0, pending: 0, sent: 0, blocked: 0, failed: 0, unknown: 0 })
const p = computed(() => progress(counts.value))
const langs = computed(() => (item.value ? neededLangs(item.value.language).filter((l) => item.value!.texts[l]?.trim()) : []))
const audience = computed(() =>
  item.value ? t('broadcasts.segment.' + item.value.segment) + ' · ' + t('broadcasts.language.' + (item.value.language || 'all')) : '',
)
const countRows = computed(() => [
  { key: 'total', n: counts.value.total },
  { key: 'sent', n: counts.value.sent },
  { key: 'blocked', n: counts.value.blocked },
  { key: 'failed', n: counts.value.failed },
  { key: 'unknown', n: counts.value.unknown },
  { key: 'pending', n: counts.value.pending },
])

// Pause, resume, cancel and delete; the answer is the broadcast as it is now.
const busy = ref<BroadcastAction | ''>('')
const actionError = ref('')
const actionDetail = ref('')
async function act(a: 'pause' | 'resume' | 'cancel' | 'delete') {
  busy.value = a
  actionError.value = ''
  actionDetail.value = ''
  try {
    if (a === 'delete') {
      await api('DELETE', `/broadcasts/${id.value}`)
      toast.add({ severity: 'success', summary: t('broadcasts.action.deleted'), life: 4000 })
      await router.push({ name: 'broadcasts' })
      return
    }
    const res = await api<{ item: BroadcastItem }>('POST', `/broadcasts/${id.value}/${a}`)
    item.value = res.item
    toast.add({ severity: 'success', summary: t('broadcasts.action.' + a + '_done'), life: 4000 })
  } catch (e) {
    const err = errorText(e, t)
    actionError.value = err.text
    actionDetail.value = err.detail
    // The status may have moved on (finished, cancelled elsewhere): show it.
    void load().catch(() => {})
  } finally {
    busy.value = ''
  }
}
function ask(a: 'cancel' | 'delete') {
  confirm.require({
    header: t('broadcasts.action.' + a + '_title'),
    message: t('broadcasts.action.' + a + '_message'),
    acceptProps: { label: t('broadcasts.action.' + a), severity: 'danger' },
    rejectProps: { label: t('broadcasts.action.keep'), severity: 'secondary', outlined: true },
    accept: () => void act(a),
  })
}

// Send or schedule a draft: the version shown when the dialog opened, whose
// audience the dialog counts; core refuses it when it changed since.
const sendOpen = ref(false)
const sendMode = ref<'now' | 'later'>('now')
const sendBusy = ref(false)
const sendError = ref('')
const sendDetail = ref('')
const atError = ref('')
const sendFor = ref<BroadcastItem | null>(null)
const notSent = ref(false)
function openSend(mode: 'now' | 'later') {
  sendFor.value = item.value
  notSent.value = false
  actionError.value = ''
  actionDetail.value = ''
  sendMode.value = mode
  sendError.value = ''
  sendDetail.value = ''
  atError.value = ''
  sendOpen.value = true
}
async function send(at: number | null) {
  const target = sendFor.value
  if (!target) return
  sendBusy.value = true
  sendError.value = ''
  sendDetail.value = ''
  atError.value = ''
  try {
    item.value = (await api<{ item: BroadcastItem }>('POST', `/broadcasts/${target.id}/send`, { at, version: target.version })).item
    sendOpen.value = false
    toast.add({ severity: 'success', summary: at === null ? t('broadcasts.send.done_now') : t('broadcasts.send.done_later'), life: 4000 })
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      // Changed elsewhere: show the latest version; sending again is the admin's
      // call. Sent or cancelled meanwhile: core's reason says so.
      sendOpen.value = false
      const fresh = await load().then(() => true, () => false)
      if (fresh && item.value && editable(item.value.status)) notSent.value = true
      else {
        const err = errorText(e, t)
        actionError.value = err.text
        actionDetail.value = err.detail
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

// Customers it could not reach: their own tab, loaded when first opened.
const tab = ref('details')
function showTab(v: string | number) {
  tab.value = String(v)
}
const problemCount = computed(() => counts.value.failed + counts.value.blocked + counts.value.unknown)
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">
      <Button icon="pi pi-arrow-left" class="app-flip back" text rounded :aria-label="t('common.back')" @click="router.push({ name: 'broadcasts' })" />
      {{ t('broadcasts.detail.title') }}
      <BroadcastStatus v-if="item" :status="item.status" class="status" />
    </h1>
    <div v-if="item" class="app-actions">
      <RouterLink v-if="auth.can('audit.read')" :to="{ name: 'audit', query: { entity_id: id } }" class="p-button p-component p-button-text history">
        <i class="pi pi-history" aria-hidden="true" />
        <span>{{ t('audit.history') }}</span>
      </RouterLink>
      <Button v-if="has('edit')" :label="t('common.edit')" icon="pi pi-pencil" outlined @click="router.push({ name: 'broadcast-edit', params: { id } })" />
      <template v-if="has('send')">
        <Button :label="t('broadcasts.send.schedule')" icon="pi pi-clock" outlined class="schedule" @click="openSend('later')" />
        <Button :label="t('broadcasts.send.now')" icon="pi pi-send" class="send-now" @click="openSend('now')" />
      </template>
      <Button v-if="has('pause')" :label="t('broadcasts.action.pause')" icon="pi pi-pause" severity="warn" outlined class="pause" :loading="busy === 'pause'" :disabled="!!busy" @click="act('pause')" />
      <Button v-if="has('resume')" :label="t('broadcasts.action.resume')" icon="pi pi-play" class="resume" :loading="busy === 'resume'" :disabled="!!busy" @click="act('resume')" />
      <Button v-if="has('cancel')" :label="t('broadcasts.action.cancel')" icon="pi pi-times" severity="danger" outlined class="cancel" :loading="busy === 'cancel'" :disabled="!!busy" @click="ask('cancel')" />
      <Button v-if="has('delete')" :label="t('broadcasts.action.delete')" icon="pi pi-trash" severity="danger" text class="delete" :loading="busy === 'delete'" :disabled="!!busy" @click="ask('delete')" />
    </div>
  </div>

  <Message v-if="loadError" severity="error" :closable="false">{{ loadError }}</Message>
  <Message v-if="actionError" severity="error" :closable="false">
    {{ actionError }}
    <div v-if="actionDetail" class="app-ltr detail">{{ actionDetail }}</div>
  </Message>
  <Message v-if="notSent" severity="warn" :closable="false" class="not-sent">{{ t('broadcasts.send.stale') }}</Message>
  <Skeleton v-if="!item && !loadError" height="16rem" />

  <template v-if="item">
    <Message v-if="item.status === 'paused' && item.last_error" severity="warn" :closable="false" class="halted">
      {{ t('broadcasts.detail.halted') }}
      <div class="app-ltr detail">{{ item.last_error }}</div>
    </Message>

    <Tabs :value="tab" @update:value="showTab">
      <TabList>
        <Tab value="details">{{ t('broadcasts.detail.tab_details') }}</Tab>
        <Tab value="problems">
          {{ t('broadcasts.detail.tab_problems') }}
          <span v-if="problemCount" class="app-muted count-badge">{{ num(problemCount, lang) }}</span>
        </Tab>
      </TabList>
      <TabPanels>
        <TabPanel value="details">
          <div class="cols">
            <Card class="progress-card">
              <template #title>{{ t('broadcasts.detail.progress') }}</template>
              <template #content>
                <template v-if="item.status === 'draft' || item.status === 'scheduled'">
                  <p class="app-muted not-started">
                    {{ item.status === 'scheduled' && item.scheduled_at
                      ? t('broadcasts.detail.starts_at', { time: dateTime(item.scheduled_at, lang) })
                      : t('broadcasts.detail.not_started') }}
                  </p>
                </template>
                <template v-else>
                  <div class="bar-line">
                    <span class="big">{{ percent(p.percent, lang) }}</span>
                    <span class="app-muted">{{ t('broadcasts.detail.done_of', { done: num(p.done, lang), total: num(p.total, lang) }) }}</span>
                  </div>
                  <ProgressBar :value="p.percent" :show-value="false" class="bar" :class="{ paused: item.status === 'paused' }" :aria-label="t('broadcasts.progress.label')" />
                  <dl class="counts">
                    <div v-for="c in countRows" :key="c.key" class="count" :class="c.key">
                      <dt>{{ t('broadcasts.counts.' + c.key) }}</dt>
                      <dd>{{ num(c.n, lang) }}</dd>
                    </div>
                  </dl>
                  <dl v-if="item.status === 'running'" class="app-facts speed">
                    <dt>{{ t('broadcasts.detail.rate') }}</dt>
                    <dd>{{ t('broadcasts.detail.rate_value', { n: rate(item.rate_per_sec, lang) }) }}</dd>
                    <template v-if="item.eta_seconds !== null">
                      <dt>{{ t('broadcasts.detail.eta') }}</dt>
                      <dd>{{ t('broadcasts.detail.eta_value', { time: duration(item.eta_seconds, lang) }) }}</dd>
                    </template>
                  </dl>
                  <p v-if="counts.unknown" class="app-muted small">{{ t('broadcasts.detail.unknown_hint') }}</p>
                </template>
              </template>
            </Card>

            <Card>
              <template #title>{{ t('broadcasts.detail.about') }}</template>
              <template #content>
                <dl class="app-facts">
                  <dt>{{ t('broadcasts.list.audience') }}</dt>
                  <dd>{{ audience }}</dd>
                  <dt>{{ t('broadcasts.detail.created_by') }}</dt>
                  <dd><UserLink :user="item.created_by" /></dd>
                  <dt>{{ t('broadcasts.list.created') }}</dt>
                  <dd>{{ dateTime(item.created_at, lang) }}</dd>
                  <template v-if="item.scheduled_at">
                    <dt>{{ t('broadcasts.list.scheduled') }}</dt>
                    <dd>{{ dateTime(item.scheduled_at, lang) }}</dd>
                  </template>
                  <template v-if="item.started_at">
                    <dt>{{ t('broadcasts.detail.started_at') }}</dt>
                    <dd>{{ dateTime(item.started_at, lang) }}</dd>
                  </template>
                  <template v-if="item.finished_at">
                    <dt>{{ t('broadcasts.detail.finished_at') }}</dt>
                    <dd>{{ dateTime(item.finished_at, lang) }}</dd>
                  </template>
                </dl>
              </template>
            </Card>
          </div>

          <Card class="texts-card">
            <template #title>{{ t('broadcasts.text.title') }}</template>
            <template #content>
              <div class="langs">
                <section v-for="l in langs" :key="l" class="lang">
                  <h3 class="lang-name">{{ t('texts.' + l) }}</h3>
                  <div class="block">
                    <div class="app-muted small">{{ t('broadcasts.detail.raw') }}</div>
                    <pre class="raw" :dir="l === 'fa' ? 'rtl' : 'ltr'" :lang="l">{{ item.texts[l] }}</pre>
                  </div>
                  <div class="block">
                    <div class="app-muted small">{{ t('broadcasts.text.preview') }}</div>
                    <div class="bubble">
                      <TextPreview :value="item.texts[l]" :html="true" :placeholders="[]" :lang="l" kind="message" />
                      <TextPreview v-if="item.button && item.button.labels[l]" :value="item.button.labels[l]" :html="false" :placeholders="[]" :lang="l" kind="button" />
                    </div>
                  </div>
                </section>
              </div>
              <dl class="app-facts button-facts">
                <dt>{{ t('broadcasts.button.title') }}</dt>
                <dd v-if="item.button">
                  <span class="app-ltr app-mono url">{{ item.button.url }}</span>
                  <span v-for="l in langs" :key="l" class="label-chip" :dir="l === 'fa' ? 'rtl' : 'ltr'" :lang="l">{{ item.button.labels[l] }}</span>
                </dd>
                <dd v-else class="app-muted">{{ t('broadcasts.detail.no_button') }}</dd>
              </dl>
            </template>
          </Card>
        </TabPanel>

        <TabPanel value="problems">
          <RecipientsTab :key="id" :broadcast-id="id" :active="tab === 'problems'" />
        </TabPanel>
      </TabPanels>
    </Tabs>
  </template>

  <SendDialog
    v-if="item"
    v-model:visible="sendOpen"
    :segment="(sendFor ?? item).segment"
    :language="(sendFor ?? item).language"
    :mode="sendMode"
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
.history {
  text-decoration: none;
  gap: 0.5rem;
}
.halted {
  margin-block-end: var(--app-gap);
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
.count-badge {
  margin-inline-start: 0.35rem;
  font-size: 0.85em;
}
.cols {
  display: grid;
  gap: var(--app-gap);
  grid-template-columns: repeat(auto-fit, minmax(min(22rem, 100%), 1fr));
  margin-block-end: var(--app-gap);
}
.not-started {
  margin: 0;
}
.bar-line {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 0.25rem 0.75rem;
}
.big {
  font-size: 1.6rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.bar {
  block-size: 0.6rem;
  margin-block: 0.5rem 1rem;
}
.bar.paused :deep(.p-progressbar-value) {
  background: var(--p-orange-500);
}
.counts {
  margin: 0;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(6.5rem, 1fr));
  gap: 0.5rem;
}
.count {
  padding: 0.5rem 0.65rem;
  border-radius: 0.5rem;
  background: var(--p-content-hover-background);
}
.count dt {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
}
.count dd {
  margin: 0;
  font-weight: 700;
  font-size: 1.1rem;
  font-variant-numeric: tabular-nums;
}
.count.sent dd {
  color: var(--p-message-success-simple-color, var(--p-green-600));
}
.count.failed dd,
.count.blocked dd {
  color: var(--p-message-error-simple-color, var(--p-red-600));
}
.speed {
  margin-block-start: 1rem;
}
.small {
  font-size: 0.8rem;
}
.texts-card :deep(.p-card-body) {
  gap: 1rem;
}
.langs {
  display: grid;
  gap: 1.5rem;
  grid-template-columns: repeat(auto-fit, minmax(min(22rem, 100%), 1fr));
}
.lang {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  min-inline-size: 0;
}
.lang-name {
  margin: 0;
  font-size: 1rem;
}
.block {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}
.raw {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 0.85rem;
  padding: 0.5rem 0.7rem;
  border: 1px dashed var(--p-content-border-color);
  border-radius: 0.5rem;
  max-block-size: 16rem;
  overflow: auto;
}
.raw[lang='fa'] {
  font-family: var(--app-font-fa);
}
.raw[lang='en'] {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
.bubble {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  max-inline-size: 30rem;
}
.button-facts {
  margin-block-start: 1.25rem;
}
.button-facts dd {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem;
  align-items: baseline;
}
.url {
  overflow-wrap: anywhere;
}
.label-chip {
  padding: 0.1rem 0.5rem;
  border-radius: 0.4rem;
  border: 1px solid var(--p-content-border-color);
  font-size: 0.85rem;
}
</style>
