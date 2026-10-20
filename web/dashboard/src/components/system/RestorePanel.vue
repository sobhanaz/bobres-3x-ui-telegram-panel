<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Message from 'primevue/message'
import ProgressBar from 'primevue/progressbar'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { bytes, dateTime, isolate, num, type Lang } from '../../format'
import { isRestoreFinal, limitText, restoreRefusalOf, restoreView, uploadBackup } from '../../restore'
import { page, pollDelay } from '../../updates'
import { useAuth } from '../../stores/auth'
import type { RestoreState } from '../../types'
import RestoreDialog from './RestoreDialog.vue'
import RestoreProgress from './RestoreProgress.vue'

// Restoring a backup from the dashboard (the owner's): one of the server's backups (the
// Restore button on each, which opens the confirmation through openFor) or a file the owner
// uploads here. The server's updater does it: it checks the backup with this server's key,
// takes a backup of the current data, restores, checks that the store is healthy and goes back
// on its own if it is not. The restore replaces core's own database and restarts the store, so
// this page follows it across the restart (it says it is reconnecting and keeps trying), and
// finds the end of the request again afterwards.
const props = defineProps<{ owner: boolean }>()
const emit = defineEmits<{ ended: [] }>()
const { t, locale } = useI18n()
const auth = useAuth()
const lang = computed(() => locale.value as Lang)

const state = ref<RestoreState | null>(null)
const loadError = ref('')
let gone = false
let seq = 0
async function load() {
  const n = ++seq
  try {
    const r = await api<RestoreState>('GET', '/system/restore')
    if (n !== seq || gone) return
    loadError.value = ''
    state.value = r
    if (r.run && !tracked.value) tracked.value = r.run.id
    schedule()
  } catch (e) {
    if (n !== seq || gone) return
    // a core that answers 503 has no update service: there is nothing to restore with
    if (!(e instanceof ApiError && e.status === 503)) loadError.value = errorText(e, t).text
  }
}
onMounted(load)

// ---- following the restore ----
const tracked = ref('')
const reconnecting = ref(false)
const view = computed(() => restoreView(state.value, tracked.value))
const going = computed(() => !!view.value && !isRestoreFinal(view.value))
let timer: ReturnType<typeof setTimeout> | undefined
let failing = false
let started = 0
let handled = ''
function schedule() {
  if (gone || timer || !going.value) return
  if (!started) started = Date.now()
  const elapsed = Date.now() - started
  // a restore can take long: the page keeps looking for as long as the updater may run
  if (elapsed > 3 * 60 * 60_000) return
  timer = setTimeout(() => void pollOnce(), pollDelay(elapsed, failing))
}
async function pollOnce() {
  timer = undefined
  try {
    const r = await api<RestoreState>('GET', '/system/restore')
    if (gone) return
    failing = false
    reconnecting.value = false
    state.value = r
    finished()
  } catch (e) {
    // the session ended (the restored data has its own sessions): the app sends the page to the
    // login page, and the restore goes on without it (its end is on Telegram, and here after logging in)
    if (gone || (e instanceof ApiError && e.status === 401)) return
    failing = true
    reconnecting.value = true
  }
  schedule()
}
/** finished reacts once to the end of the restore this page follows. */
function finished() {
  const v = view.value
  if (!v || !isRestoreFinal(v) || handled === v.id) return
  handled = v.id
  started = 0
  emit('ended')
}
function dismiss() {
  tracked.value = ''
  started = 0
}
onBeforeUnmount(() => {
  gone = true
  clearTimeout(timer)
})

// ---- what can be done now ----
const can = computed(() => state.value?.can ?? null)
/** ready: this role may, and a restore would be accepted now */
const ready = computed(() => props.owner && !!can.value?.ready && !going.value)
const domain = computed(() => state.value?.domain ?? '')
const whyNot = computed(() => {
  const c = can.value
  if (!state.value || !props.owner || !c || c.ready) return ''
  if (state.value.other === 'update') return 'update'
  return c.reason
})

// ---- uploading a file ----
const picker = ref<HTMLInputElement | null>(null)
const uploading = ref(false)
const sent = ref(0)
const total = ref(0)
const uploadError = ref('')
let abortUpload: (() => void) | undefined
const upload = computed(() => state.value?.upload ?? null)
const limit = computed(() => limitText(state.value?.limits.max_upload ?? 2 * 1024 * 1024 * 1024))
const percent = computed(() => (total.value > 0 ? Math.min(100, Math.floor((sent.value / total.value) * 100)) : 0))
function choose() {
  picker.value?.click()
}
async function picked(ev: Event) {
  const input = ev.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !state.value) return
  uploadError.value = ''
  if (file.size > (state.value.limits.max_upload ?? 0)) {
    uploadError.value = t('system.backups.rs.refusal.upload_too_large', { n: limit.value.n, unit: limit.value.unit })
    return
  }
  uploading.value = true
  sent.value = 0
  total.value = file.size
  const job = uploadBackup(file, (s, tot) => {
    sent.value = s
    total.value = tot
  })
  abortUpload = job.abort
  try {
    const r = await job.done
    if (gone) return
    if (state.value) state.value = { ...state.value, upload: r.upload }
  } catch (e) {
    if (gone || (e instanceof ApiError && e.code === 'aborted')) return
    const why = restoreRefusalOf(e)
    uploadError.value = why
      ? t('system.backups.rs.refusal.' + why, { n: limit.value.n, unit: limit.value.unit })
      : t('system.backups.rs.upload_error') + ' ' + errorText(e, t).text
  } finally {
    uploading.value = false
    abortUpload = undefined
  }
}
function cancelUpload() {
  abortUpload?.()
}
onBeforeUnmount(() => abortUpload?.())

// ---- the confirmation ----
type Target = { backup: string; uploadId: string; subject: string; source: 'backup' | 'upload'; size: number | null; madeAt: number | null }
const target = ref<Target | null>(null)
const confirmOpen = ref(false)
const applying = ref(false)
const applyError = ref('')
const applyDetail = ref('')
const askCode = ref(false)
const needsCode = computed(() => askCode.value || auth.me?.password?.reauth === true)

/** openFor opens the confirmation for one of the server's backups (the list's Restore button). */
function openFor(b: { file: string; size: number; madeAt: number | null }) {
  target.value = { backup: b.file, uploadId: '', subject: b.file, source: 'backup', size: b.size, madeAt: b.madeAt }
  openConfirm()
}
function restoreUpload() {
  const u = upload.value
  if (!u) return
  target.value = { backup: '', uploadId: u.id, subject: u.name, source: 'upload', size: u.size, madeAt: null }
  openConfirm()
}
function openConfirm() {
  applyError.value = ''
  applyDetail.value = ''
  confirmOpen.value = true
}
async function apply(p: { confirm: string; acknowledge: boolean; code: string }) {
  const tg = target.value
  if (!tg) return
  applying.value = true
  applyError.value = ''
  applyDetail.value = ''
  try {
    const body: Record<string, unknown> = { confirm: p.confirm, acknowledge: p.acknowledge }
    if (tg.backup) body.backup = tg.backup
    else body.upload_id = tg.uploadId
    if (p.code) body.code = p.code
    const r = await api<RestoreState>('POST', '/system/restore', body)
    if (gone) return
    confirmOpen.value = false
    askCode.value = false
    tracked.value = r.run?.id ?? ''
    handled = ''
    state.value = r
    schedule()
  } catch (e) {
    const why = restoreRefusalOf(e)
    if (why) {
      applyError.value = t('system.backups.rs.refusal.' + why, { n: limit.value.n, unit: limit.value.unit })
      if (why === 'code_required') askCode.value = true
      // the page was out of date: read it all again behind the message
      if (['upload_missing', 'backup_unknown', 'already_running', 'helper_missing', 'restore_unsupported'].includes(why)) void load()
    } else {
      const err = errorText(e, t)
      applyError.value = err.text
      applyDetail.value = err.detail
    }
  } finally {
    applying.value = false
  }
}
function reloadPage() {
  page.reload()
}

defineExpose({ openFor, ready })
</script>

<template>
  <div v-if="state || loadError" class="restore-panel">
    <Message v-if="loadError" severity="error" :closable="false" class="banner load-error">{{ loadError }}</Message>

    <RestoreProgress v-if="view" :view="view" :reconnecting="reconnecting" @reload="reloadPage" @dismiss="dismiss" />

    <Card v-if="owner && state" class="restore-upload-card">
      <template #title>{{ t('system.backups.rs.panel_title') }}</template>
      <template #content>
        <p class="app-muted panel-hint">{{ t('system.backups.rs.panel_hint', { n: limit.n, unit: limit.unit }) }}</p>

        <Message v-if="whyNot" severity="secondary" icon="pi pi-info-circle" :closable="false" class="why-not">
          {{ t('system.backups.rs.not_ready.' + whyNot) }}
        </Message>
        <Message v-if="uploadError" severity="error" :closable="false" class="upload-error">{{ uploadError }}</Message>

        <input ref="picker" type="file" accept=".bkp,application/octet-stream" class="picker" tabindex="-1" aria-hidden="true" @change="picked" />

        <div v-if="uploading" class="progress">
          <ProgressBar :value="percent" :show-value="false" class="bar" />
          <div class="app-muted amount">
            {{ t('system.backups.rs.uploading', { percent: num(percent, lang) + '%', sent: bytes(sent, lang), total: bytes(total, lang) }) }}
          </div>
          <Button :label="t('app.cancel')" severity="secondary" outlined size="small" class="cancel-upload" @click="cancelUpload" />
        </div>

        <div v-else-if="upload" class="staged">
          <div class="staged-what">
            <span class="app-ltr app-mono name">{{ upload.name }}</span>
            <span class="app-muted size">{{ bytes(upload.size, lang) }}</span>
          </div>
          <p class="app-muted staged-until">{{ t('system.backups.rs.uploaded', { time: dateTime(upload.expires_at, lang) }) }}</p>
          <p class="app-muted sum">SHA-256 <span class="app-ltr app-mono">{{ upload.sha256 }}</span></p>
          <div class="row">
            <Button :label="t('system.backups.rs.restore_this')" icon="pi pi-replay" severity="danger" :disabled="!ready" class="restore-upload" @click="restoreUpload" />
            <Button :label="t('system.backups.rs.choose_other')" icon="pi pi-upload" severity="secondary" outlined :disabled="!ready" class="choose" @click="choose" />
          </div>
        </div>

        <Button v-else :label="t('system.backups.rs.choose')" icon="pi pi-upload" severity="secondary" outlined :disabled="!ready" class="choose" @click="choose" />

        <p v-if="state.last && !view" class="app-muted last">
          {{
            t('system.backups.rs.last', {
              name: isolate(state.last.subject || '?'),
              time: dateTime(state.last.outcome_at ?? state.last.requested_at, lang),
              outcome: t('system.backups.rs.outcome.' + (['done', 'failed', 'rolled_back', 'not_started'].includes(state.last.outcome) ? state.last.outcome : 'failed')),
            })
          }}
        </p>
      </template>
    </Card>

    <RestoreDialog
      v-if="target"
      v-model:visible="confirmOpen"
      :subject="target.subject"
      :source="target.source"
      :size="target.size"
      :made-at="target.madeAt"
      :domain="domain"
      :needs-code="needsCode"
      :busy="applying"
      :error="applyError"
      :detail="applyDetail"
      @confirm="apply"
    />
  </div>
</template>

<style scoped>
.restore-panel {
  display: flex;
  flex-direction: column;
  gap: var(--app-gap);
}
.panel-hint {
  margin-block: 0 0.75rem;
}
.why-not,
.upload-error {
  margin-block-end: 0.75rem;
}
.picker {
  display: none;
}
.progress {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  align-items: flex-start;
}
.bar {
  inline-size: 100%;
}
.staged-what {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.25rem 0.75rem;
}
.name {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.staged-until,
.sum {
  margin-block: 0.35rem 0;
  font-size: 0.85rem;
  overflow-wrap: anywhere;
}
.row {
  display: flex;
  flex-wrap: wrap;
  gap: 0.6rem;
  margin-block-start: 0.75rem;
}
.last {
  margin-block: 0.9rem 0;
  font-size: 0.85rem;
}
</style>
