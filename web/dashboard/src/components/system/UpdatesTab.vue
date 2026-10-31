<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import Tag from 'primevue/tag'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { dateTime, isolate, rtl, type Lang } from '../../format'
import { channelOf, newerThanLicense, updateDetail, updateReason, updateState, type UpdateState } from '../../system'
import {
  copyText,
  helperProblem,
  isFinal,
  isPrerelease,
  issuedAt,
  page,
  pollDelay,
  progressView,
  refusalOf,
  updateCommand,
  updatePoll,
} from '../../updates'
import { useAuth } from '../../stores/auth'
import type { UpdatesProgress, UpdatesState } from '../../types'
import UpdateChannelCard from './UpdateChannelCard.vue'
import UpdateConfirmDialog from './UpdateConfirmDialog.vue'
import UpdateProgress from './UpdateProgress.vue'

// Which release is the newest on the channel this install follows (stable or
// beta), whether the install runs it, and, for the owner, "Update now": the
// server's updater runs the same verified `bobres update` and this page follows
// it step by step, across the restart the update itself causes. "Check again"
// makes the bot look at GitHub now (the bot has internet access, core has none)
// and shows what it found, when, and why a look failed. The owner also chooses
// the channel here. Installing stays possible on the server's command line.
const { t, locale } = useI18n()
const toast = useToast()
const auth = useAuth()
const lang = computed(() => locale.value as Lang)
// the arrow from the old version to the new one points the way the line reads
const arrowIcon = computed(() => (rtl(locale.value as Lang) ? 'pi-arrow-left' : 'pi-arrow-right'))
const isOwner = computed(() => auth.can('system.write'))

const state = ref<UpdatesState | null>(null)
const loading = ref(true)
const loadError = ref('')
const loadDetail = ref('')
// the version core ran when this page loaded: another one later means a new
// dashboard is waiting to be loaded
const firstVersion = ref('')

let gone = false
let seq = 0
async function load() {
  const n = ++seq
  loading.value = true
  loadError.value = ''
  loadDetail.value = ''
  try {
    const r = await api<UpdatesState>('GET', '/system/updates')
    if (n !== seq || gone) return
    adopt(r)
  } catch (e) {
    if (n !== seq || gone) return
    const err = errorText(e, t)
    loadError.value = t('system.load_error') + ' ' + err.text
    loadDetail.value = err.detail
  } finally {
    if (n === seq) loading.value = false
  }
}

/** adopt takes a whole state; an update that is open (asked for from another
 * place, or before this page was opened) is followed from here on. */
function adopt(r: UpdatesState) {
  state.value = r
  if (!firstVersion.value) firstVersion.value = r.version
  if (r.run && !tracked.value) tracked.value = r.run.id
  schedule()
}
onMounted(load)

// ---- what the page says of the release ----
const check = computed(() => state.value?.check ?? null)
const vState = computed<UpdateState>(() => updateState(check.value))
const reason = computed(() => (state.value ? updateReason(check.value) : null))
const detail = computed(() => updateDetail(check.value, reason.value))
const current = computed(() => check.value?.current || state.value?.version || '')
const latest = computed(() => check.value?.latest || '')
const channel = computed(() => channelOf(check.value))
const prerelease = computed(() => (latest.value ? (check.value?.prerelease ?? isPrerelease(latest.value)) : false))
const released = computed(() => (latest.value ? issuedAt(check.value) : null))
// only GitHub's own page for a release: never a link taken from an answer
const notesUrl = computed(() => {
  const u = check.value?.notes_url ?? ''
  return latest.value && /^https:\/\/github\.com\/[\w.-]+\/[\w.-]+\/releases\/tag\/v[\w.-]+$/.test(u) ? u : ''
})
const checkedAt = computed(() => (check.value && check.value.checked_at > 0 ? check.value.checked_at : 0))
const severity: Record<UpdateState, 'warn' | 'success' | 'secondary'> = { newer: 'warn', current: 'success', unknown: 'secondary' }
const newerThanLicenseNote = computed(() => newerThanLicense(check.value))
// a release that came after a failed check still shows, so does the failure
const unverified = computed(() => !!latest.value && !!check.value && !check.value.verified && reason.value !== 'bad_signature')
const verdict = computed(() => {
  switch (vState.value) {
    case 'current':
      return t('system.updates.verdict.current', { channel: t('system.updates.channel_name.' + (channel.value ?? 'stable')) })
    case 'newer':
      return t(prerelease.value ? 'system.updates.verdict.newer_pre' : 'system.updates.verdict.newer', { version: isolate(latest.value) })
    default:
      return t('system.updates.verdict.unknown')
  }
})

// ---- check again ----
const checking = ref(false)
const checkError = ref('')
const checkDetail = ref('')
/** checkNow makes the bot look at GitHub now and shows what it found (the
 * owner's; an admin only reads the state again). */
async function checkNow() {
  checking.value = true
  checkError.value = ''
  checkDetail.value = ''
  try {
    if (!isOwner.value) {
      await load()
      return
    }
    const r = await api<UpdatesState>('POST', '/system/updates/check', {})
    if (!gone) adopt(r)
  } catch (e) {
    const err = errorText(e, t)
    checkError.value = t('system.updates.check_error') + ' ' + err.text
    checkDetail.value = err.detail
  } finally {
    checking.value = false
  }
}

// ---- the channel ----
const savingChannel = ref(false)
const channelError = ref('')
async function saveChannel(value: 'stable' | 'beta' | '') {
  savingChannel.value = true
  channelError.value = ''
  try {
    const r = await api<UpdatesState>('PUT', '/system/updates/channel', { channel: value })
    if (gone) return
    adopt(r)
    toast.add({ severity: 'success', summary: t('system.updates.channel_card.saved'), life: 2500 })
  } catch (e) {
    const err = errorText(e, t)
    channelError.value = t('system.updates.channel_card.save_error') + ' ' + err.text
  } finally {
    savingChannel.value = false
  }
}

// ---- Update now ----
const apply = computed(() => state.value?.apply ?? null)
const helper = computed(() => state.value?.helper ?? null)
// no update of its own while one is being made (here, or on the server)
const canApply = computed(() => !!apply.value?.ready && isOwner.value && !going.value)
const askCode = ref(false) // core asked for the authenticator code, or /me says it will
const needsCode = computed(() => askCode.value || auth.me?.password?.reauth === true)
const confirmOpen = ref(false)
const applying = ref(false)
const applyError = ref('')
const applyDetail = ref('')
function openConfirm() {
  applyError.value = ''
  applyDetail.value = ''
  confirmOpen.value = true
}
async function applyNow(p: { acceptPrerelease: boolean; code: string }) {
  if (!state.value || !apply.value) return
  applying.value = true
  applyError.value = ''
  applyDetail.value = ''
  try {
    const body: Record<string, unknown> = {
      version: apply.value.target,
      channel: channel.value ?? '',
      confirm: true,
      accept_prerelease: p.acceptPrerelease,
    }
    if (p.code) body.code = p.code
    const r = await api<UpdatesProgress>('POST', '/system/updates/apply', body)
    if (gone) return
    confirmOpen.value = false
    askCode.value = false
    tracked.value = r.run?.id ?? ''
    state.value = { ...state.value, ...r }
    schedule()
  } catch (e) {
    const why = refusalOf(e)
    if (why) {
      applyError.value = t('system.updates.refusal.' + why.code, { latest: isolate(why.latest || '?') })
      if (why.code === 'code_required') askCode.value = true
      // the page was out of date: read it all again behind the message
      if (['version_changed', 'channel_changed', 'not_newer', 'already_running', 'helper_missing', 'no_release'].includes(why.code)) void load()
    } else {
      const err = errorText(e, t)
      applyError.value = err.text
      applyDetail.value = err.detail
    }
  } finally {
    applying.value = false
  }
}

// ---- following the update ----
// The update this page started or found open (its request id). The page reads
// the light progress every few seconds while one is going on: core restarts
// in the middle of it, so when it does not answer the page says it is
// reconnecting and keeps trying; when it answers again it reads everything.
const tracked = ref('')
const reconnecting = ref(false)
const view = computed(() => progressView(state.value, tracked.value))
const newerDashboard = computed(() => !!firstVersion.value && !!state.value && state.value.version !== firstVersion.value)
let timer: ReturnType<typeof setTimeout> | undefined
let failing = false
let started = 0
let handled = '' // the end this page has already reacted to

const going = computed(() => {
  const v = progressView(state.value, tracked.value)
  return !!v && !isFinal(v.phase)
})
function schedule() {
  if (gone || timer || !going.value) return
  if (!started) started = Date.now()
  const elapsed = Date.now() - started
  if (elapsed > updatePoll.maxMs) return
  timer = setTimeout(() => void pollOnce(), pollDelay(elapsed, failing))
}
async function pollOnce() {
  timer = undefined
  try {
    const p = await api<UpdatesProgress>('GET', '/system/updates/progress')
    if (gone) return
    const back = failing
    failing = false
    reconnecting.value = false
    if (state.value) state.value = { ...state.value, ...p }
    if (back) void load() // core is back: everything it says may have changed
    finished()
  } catch (e) {
    // the session ended: the app sends the page to the login page, and the
    // update goes on without it (its end is on Telegram and here after logging in)
    if (gone || (e instanceof ApiError && e.status === 401)) return
    failing = true
    reconnecting.value = true
  }
  schedule()
}
/** finished reacts once to the end of the update this page follows: it reads
 * everything again and, after a success, makes the bot look again (it just
 * restarted, and knows nothing of releases yet). */
function finished() {
  const v = progressView(state.value, tracked.value)
  if (!v || !isFinal(v.phase) || handled === v.id) return
  handled = v.id
  started = 0
  void load().then(() => {
    if (v.phase === 'done' && isOwner.value) void checkNow()
  })
}
function reloadPage() {
  page.reload()
}
function dismiss() {
  tracked.value = ''
  started = 0
}
onBeforeUnmount(() => {
  gone = true
  clearTimeout(timer)
})

// ---- the command for the server ----
const command = computed(() => updateCommand(channel.value ?? state.value?.channel.effective))
async function copyCommand() {
  if (await copyText(command.value)) toast.add({ severity: 'info', summary: t('common.copied'), life: 2000 })
  else toast.add({ severity: 'warn', summary: t('system.updates.copy_failed'), life: 6000 })
}
const lastRun = computed(() => state.value?.last ?? null)
const notAvailable = computed(() => apply.value?.reason === 'helper_missing')
</script>

<template>
  <div class="updates">
    <div class="toolbar">
      <span v-if="checkedAt" class="app-muted checked">{{ t('system.updates.checked_at', { time: dateTime(checkedAt, lang) }) }}</span>
      <span v-else-if="state" class="app-muted checked">{{ t('system.updates.never_checked') }}</span>
      <span class="spacer" />
      <Button
        :label="checking ? t('system.updates.checking') : isOwner ? t('system.refresh') : t('system.updates.reload')"
        icon="pi pi-refresh"
        size="small"
        severity="secondary"
        outlined
        :loading="checking || (loading && !!state)"
        class="refresh"
        @click="checkNow"
      />
    </div>

    <Message v-if="loadError" severity="error" :closable="false" class="banner load-error">
      {{ loadError }}
      <div v-if="loadDetail" class="app-ltr detail">{{ loadDetail }}</div>
    </Message>
    <Message v-if="checkError" severity="error" :closable="false" class="banner check-error">
      {{ checkError }}
      <div v-if="checkDetail" class="app-ltr detail">{{ checkDetail }}</div>
    </Message>

    <Card v-if="loading && !state" class="versions-card">
      <template #content>
        <Skeleton height="1.2rem" width="60%" class="skeleton" />
        <Skeleton height="1.2rem" width="40%" />
      </template>
    </Card>

    <template v-else-if="state">
      <UpdateProgress
        v-if="view"
        :view="view"
        :reconnecting="reconnecting"
        :newer="newerDashboard"
        :version="state.version"
        @reload="reloadPage"
        @dismiss="dismiss"
      />

      <Card class="versions-card">
        <template #title>
          <div class="card-title">
            <span>{{ t('system.updates.title') }}</span>
            <Tag :severity="severity[vState]" :value="t('system.updates.state.' + vState)" class="update-state" />
          </div>
        </template>
        <template #content>
          <p class="verdict">{{ verdict }}</p>
          <dl class="app-facts facts">
            <dt>{{ t('system.updates.current') }}</dt>
            <dd class="app-ltr current-version">{{ current || '—' }}</dd>
            <dt>{{ t('system.updates.latest') }}</dt>
            <dd class="latest-line">
              <span class="app-ltr latest-version">{{ latest || '—' }}</span>
              <Tag v-if="prerelease" severity="warn" :value="t('system.updates.prerelease')" class="prerelease" />
            </dd>
            <template v-if="released">
              <dt>{{ t('system.updates.released') }}</dt>
              <dd class="released">{{ dateTime(released, lang) }}</dd>
            </template>
            <template v-if="channel">
              <dt>{{ t('system.updates.channel') }}</dt>
              <dd class="channel">
                <Tag :severity="channel === 'beta' ? 'warn' : 'secondary'" :value="t('system.updates.channel_name.' + channel)" class="channel-name" />
              </dd>
            </template>
            <template v-if="notesUrl">
              <dt>{{ t('system.updates.notes') }}</dt>
              <dd>
                <a :href="notesUrl" target="_blank" rel="noopener noreferrer" class="notes-link">
                  <span class="app-ltr">{{ latest }}</span>
                  <i class="pi pi-external-link" aria-hidden="true" />
                </a>
              </dd>
            </template>
          </dl>
          <!-- not checked here; a release that failed the check says so below -->
          <p v-if="unverified" class="app-muted unverified">
            <i class="pi pi-info-circle" aria-hidden="true" />
            {{ t('system.updates.unverified') }}
          </p>
        </template>
      </Card>

      <Message v-if="reason" :severity="vState === 'unknown' ? 'secondary' : 'warn'" icon="pi pi-question-circle" :closable="false" class="not-checked">
        <div class="reason-title">{{ t(vState === 'unknown' ? 'system.updates.could_not_check' : 'system.updates.last_check_failed') }}</div>
        <div class="reason">{{ t('system.updates.reason.' + reason) }}</div>
        <div v-if="detail" class="app-ltr detail error">{{ detail }}</div>
      </Message>

      <Card v-if="vState === 'newer'" class="update-card">
        <template #title>
          <div class="card-title">
            <i class="pi pi-arrow-circle-up" aria-hidden="true" />
            <span class="available-title">{{ t('system.updates.available', { version: isolate(latest) }) }}</span>
            <Tag v-if="prerelease" severity="warn" :value="t('system.updates.prerelease')" class="prerelease" />
          </div>
        </template>
        <template #content>
          <!-- advisory: the license's update date never refuses or hides an update -->
          <p v-if="newerThanLicenseNote" class="license-note">
            {{ t('system.updates.newer_than_license') }}
            <RouterLink :to="{ name: 'system', query: { tab: 'license' } }" class="license-link">{{ t('system.updates.see_license') }}</RouterLink>
          </p>

          <template v-if="canApply">
            <p class="update-hint">{{ t('system.updates.update.hint') }}</p>
            <Button :label="t('system.updates.update.now')" icon="pi pi-cloud-download" class="update-now" @click="openConfirm" />
          </template>
          <Message v-else-if="going || apply?.reason === 'already_running'" severity="info" icon="pi pi-spin pi-spinner" :closable="false" class="running-note">
            {{ t('system.updates.update.running') }}
          </Message>
          <Message v-else-if="notAvailable" severity="secondary" icon="pi pi-box" :closable="false" class="not-available">
            <div class="title">{{ t('system.updates.update.not_available_title') }}</div>
            <p v-if="helper && helper.problem" class="problem">{{ t('system.updates.update.helper_problem.' + helperProblem(helper.problem)) }}</p>
            <p class="how">{{ t('system.updates.update.not_available') }}</p>
          </Message>
          <p v-if="apply && !apply.allowed" class="app-muted owner-only">{{ t('system.updates.update.owner_only') }}</p>

          <details class="server-way" :open="notAvailable">
            <summary>{{ t('system.updates.update.server_title') }}</summary>
            <p class="how-by-hand">{{ t('system.updates.how') }}</p>
            <div class="command-row">
              <code class="app-ltr app-mono command">{{ command }}</code>
              <Button
                icon="pi pi-copy"
                text
                rounded
                size="small"
                :aria-label="t('system.updates.copy_command')"
                v-tooltip.top="t('common.copy')"
                class="copy-command"
                @click="copyCommand"
              />
            </div>
            <p class="app-muted backup-first">{{ t('system.updates.backup_first') }}</p>
          </details>
        </template>
      </Card>

      <UpdateChannelCard
        :setting="state.channel.setting"
        :effective="state.channel.effective"
        :source="state.channel.source"
        :can-edit="isOwner"
        :busy="savingChannel"
        :error="channelError"
        @save="saveChannel"
        @reset="saveChannel('')"
      />

      <Message v-if="lastRun && !view" severity="secondary" icon="pi pi-history" :closable="false" class="last-update">
        <div class="title">{{ t('system.updates.last.title') }}</div>
        <div class="last-line">
          <span class="app-ltr">{{ lastRun.from }}</span>
          <i :class="['pi', arrowIcon, 'arrow']" aria-hidden="true" />
          <span class="app-ltr">{{ lastRun.to }}</span>
          <Tag
            :severity="lastRun.outcome === 'done' ? 'success' : lastRun.outcome === 'rolled_back' ? 'warn' : 'danger'"
            :value="t('system.updates.last.outcome.' + (lastRun.outcome || 'failed'))"
            class="last-outcome"
          />
          <span v-if="lastRun.outcome_at" class="app-muted">{{ dateTime(lastRun.outcome_at, lang) }}</span>
        </div>
        <div class="app-muted last-by">{{ t('system.updates.last.by', { by: isolate(lastRun.by) }) }}</div>
      </Message>
    </template>

    <UpdateConfirmDialog
      v-if="state && apply"
      v-model:visible="confirmOpen"
      :from="current"
      :to="apply.target || latest"
      :channel="channel ?? 'stable'"
      :prerelease="isPrerelease(apply.target || latest)"
      :needs-code="needsCode"
      :busy="applying"
      :error="applyError"
      :detail="applyDetail"
      @confirm="applyNow"
    />
  </div>
</template>

<style scoped>
.updates {
  display: flex;
  flex-direction: column;
  gap: var(--app-gap);
}
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
}
.spacer {
  flex: 1;
}
.card-title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
}
.skeleton {
  margin-block-end: 0.6rem;
}
.verdict {
  margin-block: 0 0.9rem;
  font-weight: 500;
}
.latest-line {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
.notes-link {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}
.notes-link .pi {
  font-size: 0.8rem;
}
.unverified {
  margin-block: 0.75rem 0;
}
.unverified .pi {
  margin-inline-end: 0.3rem;
  vertical-align: middle;
}
.reason-title,
.title {
  font-weight: 600;
}
.update-hint {
  margin-block: 0 0.9rem;
}
.license-note {
  margin-block: 0 0.9rem;
  font-size: 0.85rem;
}
.owner-only {
  margin-block: 0.75rem 0;
}
.problem,
.how {
  margin-block: 0.4rem 0;
}
.server-way {
  margin-block-start: 1rem;
}
.server-way summary {
  cursor: pointer;
  font-weight: 500;
}
.how-by-hand {
  margin-block: 0.6rem 0.4rem;
}
.command-row {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  max-inline-size: 100%;
}
.command {
  padding: 0.2rem 0.6rem;
  border-radius: 0.375rem;
  background: var(--p-content-background);
  border: 1px solid var(--p-content-border-color);
  user-select: all;
  overflow-wrap: anywhere;
}
.backup-first {
  margin-block: 0.6rem 0;
  font-size: 0.85rem;
}
.last-line {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  margin-block-start: 0.3rem;
}
.arrow {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
}
.last-by {
  margin-block-start: 0.2rem;
  font-size: 0.85rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
@media (max-width: 640px) {
  .update-now {
    inline-size: 100%;
  }
}
</style>
