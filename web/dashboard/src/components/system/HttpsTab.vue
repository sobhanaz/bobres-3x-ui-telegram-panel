<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Checkbox from 'primevue/checkbox'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import RadioButton from 'primevue/radiobutton'
import Skeleton from 'primevue/skeleton'
import Tag from 'primevue/tag'
import Textarea from 'primevue/textarea'
import { api } from '../../api'
import { date, dateTime, isolate, num, type Lang } from '../../format'
import {
  addressSeverity,
  addressState,
  agentState,
  CADDY_LOGS_COMMAND,
  canonicalIP,
  failureText,
  formProblems,
  gatewayDown,
  httpsPoll,
  isIPLiteral,
  linkSource,
  linksOnIp,
  modeOf,
  needsConfirmation,
  PEM_MAX,
  problemText,
  putBody,
  RECOVERY_COMMAND,
  STATUS_COMMAND,
  TLS_MODES,
  waitForCertificate,
  warningText,
  type HttpsForm,
  type TlsMode,
} from '../../https'
import { UPDATE_COMMAND } from '../../system'
import { useAuth } from '../../stores/auth'
import type { TlsAddress, TlsView } from '../../types'

// How the store's addresses get their HTTPS certificates. Everyone who sees
// the System page sees the status: the mode, what each address serves now
// (issuer, expiry, whether browsers trust it) and the tls-agent's last
// result. The owner also changes the mode. A change is saved only once Caddy
// accepts it; a certificate from Let's Encrypt takes a while, so the tab
// reads the state again every few seconds for up to 90 until it is in use.
const { t, te, locale } = useI18n()
const toast = useToast()
const auth = useAuth()
const lang = computed(() => locale.value as Lang)
const canWrite = computed(() => auth.can('system.write'))
const uid = useId()

const view = ref<TlsView | null>(null)
const loading = ref(true)
const loadError = ref('')
const loadDetail = ref('')
// a slower answer never replaces a newer one
let seq = 0
let gone = false

const form = reactive<HttpsForm>({ mode: 'auto', ip: '', cert: '', key: '', confirmIp: false })
// the form starts from the saved state once; later reads leave what is typed
let seeded = false
// the address the bot detected (the Detect button, or the last detection core remembers)
const detected = ref('')
// core asked for a confirmation of the typed address
const asked = ref(false)

function seed(v: TlsView) {
  if (v.detected_ip) detected.value = v.detected_ip
  if (seeded) return
  seeded = true
  form.mode = modeOf(v.state.mode)
  form.ip = v.state.ip || (isIPLiteral(v.state.domain) ? v.state.domain : '')
}

async function load() {
  const n = ++seq
  loading.value = true
  loadError.value = ''
  loadDetail.value = ''
  try {
    const v = await api<TlsView>('GET', '/system/tls')
    if (n !== seq) return
    view.value = v
    seed(v)
  } catch (e) {
    if (n !== seq) return
    const err = failureText(e, t, te)
    loadError.value = t('system.https.load_error') + ' ' + err.text
    loadDetail.value = err.detail
  } finally {
    if (n === seq) loading.value = false
  }
}
onMounted(load)
onBeforeUnmount(() => {
  gone = true
})

// The status.
const state = computed(() => view.value?.state ?? null)
const savedMode = computed(() => modeOf(state.value?.mode))
const modeLabel = (m: string) => t('system.https.mode.' + modeOf(m))
const serving = computed(() => (view.value?.effective && view.value.effective.mode !== savedMode.value ? modeLabel(view.value.effective.mode) : ''))
const problem = computed(() => (view.value?.problem ? problemText(view.value.problem, t, te) : null))
const checkedAt = computed(() => view.value?.status.checked_at ?? null)
const addresses = computed(() => view.value?.status.addresses ?? [])
// Caddy answered on none of the addresses: said once, above all, instead of one error per address
const caddyDown = computed(() => gatewayDown(addresses.value))
const caddyDownWhy = computed(() => addresses.value.find((a) => a.error)?.error ?? '')
// where dashboard links point: the notes about them moving to the IP address hold only while they follow the domain (BOBRES_PUBLIC_URL, when set, wins)
const source = computed(() => (view.value ? linkSource(view.value) : 'none'))
const domainLinks = computed(() => !!view.value && savedMode.value === 'ip' && source.value === 'domain' && !linksOnIp(view.value))
const overrideLinks = computed(() => savedMode.value === 'ip' && source.value === 'override')
const saved = computed(() => {
  const c = state.value?.cert
  return c && savedMode.value === 'manual' ? c : null
})
const left = (unix: number, days?: number) => {
  const n = days ?? Math.ceil((unix * 1000 - Date.now()) / 86_400_000)
  return n <= 0 ? t('system.https.addresses.expired') : t('system.https.addresses.days_left', { n: num(n, lang.value) }, n)
}

const agentTag = { none: 'warn', idle: 'secondary', waiting: 'warn', ok: 'success', failed: 'danger' } as const
const agentKind = computed(() => agentState(view.value?.status.agent))
const agentView = computed(() => {
  const a = view.value?.status.agent
  const at = a?.applied_at ? dateTime(a.applied_at, lang.value) : '—'
  const reason = a && te('system.https.agent_code.' + a.code) ? t('system.https.agent_code.' + a.code) : (a?.code ?? '')
  switch (agentKind.value) {
    case 'none':
      return { text: t('system.https.agent.none', { command: isolate(UPDATE_COMMAND) }), detail: '' }
    case 'failed':
      return { text: t('system.https.agent.failed', { time: at, reason }), detail: a?.error ?? '' }
    case 'ok':
      return { text: t('system.https.agent.ok', { time: at }), detail: '' }
    default:
      return { text: t('system.https.agent.' + agentKind.value), detail: '' }
  }
})
const roleLabel = (r: string) => (te('system.https.role.' + r) ? t('system.https.role.' + r) : r)
const trustedKey = (a: TlsAddress) => (a.trusted === null ? 'unknown' : a.trusted ? 'yes' : 'no')

// The form.
const manualAllowed = computed(() => state.value?.manual_allowed === true)
const problems = computed(() => formProblems(form, { domain: state.value?.domain ?? '', manualAllowed: manualAllowed.value }))
// a field's problem shows once it has text, or after Apply was tried
const tried = ref(false)
const hasText = (f: string) => (f === 'ip' ? form.ip.trim() !== '' : f === 'cert' ? form.cert.trim() !== '' : f === 'key' ? form.key.trim() !== '' : true)
const fieldProblem = (field: string) => {
  const p = problems.value.find((x) => x.field === field && (tried.value || hasText(field)))
  return p ? t('system.https.code.' + p.code) : ''
}
const confirmNeeded = computed(() => form.mode === 'ip' && needsConfirmation(form.ip, detected.value, asked.value))
const cantApply = computed(() => applying.value || (confirmNeeded.value && !form.confirmIp) || (form.mode === 'manual' && !manualAllowed.value))
const ipHost = computed(() => isolate(canonicalIP(form.ip) || '…'))
const differs = computed(() => detected.value !== '' && canonicalIP(detected.value) !== canonicalIP(form.ip))

// What core answered to the last Apply, shown until the form changes.
const serverError = ref<{ text: string; detail: string } | null>(null)
const serverField = ref<{ field: string; text: string } | null>(null)
const accepted = ref<{ notes: string[]; pending: boolean } | null>(null)
const waiting = ref<'idle' | 'waiting' | 'ready' | 'timeout' | 'failed'>('idle')
function edited() {
  serverError.value = null
  serverField.value = null
}
// The tick confirms the address that was on screen when it was made: another
// address needs its own.
function ipEdited() {
  form.confirmIp = false
  edited()
}
const fieldError = (field: string) => (serverField.value?.field === field ? serverField.value.text : '')
// core's answer about a field first (the key is cleared after a send, which would otherwise read "paste the key")
const shown = (field: string) => fieldError(field) || fieldProblem(field)

const applying = ref(false)
let run = 0
async function apply() {
  tried.value = true
  edited()
  if (problems.value.length || cantApply.value) return
  const mode = form.mode
  const ip = form.ip
  applying.value = true
  accepted.value = null
  waiting.value = 'idle'
  run++
  try {
    const out = await api<TlsView>('PUT', '/system/tls', putBody(form, confirmNeeded.value))
    view.value = out
    seed(out)
    accepted.value = { notes: (out.warnings ?? []).map((w) => warningText(w, t, te)), pending: out.pending === true }
    toast.add({ severity: 'success', summary: t('system.https.form.applied'), life: 3000 })
    form.cert = ''
    files.cert = ''
    form.confirmIp = false
    asked.value = false
    // Caddy not running: nothing will be issued until it starts, and the answer
    // has no live status (it would blank the banner and the rows): read it once
    if (out.pending) void load()
    else void wait(mode)
  } catch (e) {
    const f = failureText(e, t, te, ip)
    serverError.value = { text: f.text, detail: f.detail }
    if (f.field) serverField.value = { field: f.field, text: f.text }
    if (f.code === 'ip_unconfirmed') {
      asked.value = true
      // what core says now, even nothing: it trusts a detection for a few minutes only, and the one seeded here may be older
      detected.value = f.detectedIp
    }
  } finally {
    applying.value = false
    // the key was sent: it is never kept, and its empty field is not an error yet
    form.key = ''
    keyFile.value = ''
    tried.value = false
  }
}

async function wait(mode: TlsMode) {
  const n = ++run
  waiting.value = 'waiting'
  const r = await waitForCertificate(() => api<TlsView>('GET', '/system/tls'), mode, {
    cancelled: () => gone || n !== run,
    onView: (v) => {
      if (n === run) view.value = v
    },
  })
  if (gone || n !== run) return
  waiting.value = r.outcome === 'cancelled' ? 'idle' : r.outcome
}

// Detecting the server's address through the bot.
const detecting = ref(false)
const detectError = ref('')
async function detect() {
  detecting.value = true
  detectError.value = ''
  try {
    const r = await api<{ ip: string }>('POST', '/system/tls/detect-ip')
    detected.value = r.ip
    form.ip = r.ip
    form.confirmIp = false
    asked.value = false
    edited()
  } catch (e) {
    detectError.value = failureText(e, t, te).text
  } finally {
    detecting.value = false
  }
}

// The certificate and key, pasted or read from a file. files says why a
// file was not taken; typing or pasting in its field is the owner's way
// around it, so the reason goes then.
const files = reactive({ cert: '', key: '' })
const keyFile = ref('')
function typed(which: 'cert' | 'key') {
  files[which] = ''
  edited()
}
async function pick(which: 'cert' | 'key', ev: Event) {
  const el = ev.target as HTMLInputElement
  const file = el.files?.[0]
  el.value = ''
  if (!file) return
  files[which] = ''
  if (file.size > PEM_MAX) {
    files[which] = t('system.https.file_too_big')
    return
  }
  try {
    form[which] = (await file.text()).trim() + '\n'
    if (which === 'key') keyFile.value = file.name
    edited()
  } catch {
    files[which] = t('system.https.file_unreadable')
  }
}
// What each field says about itself, and the ids that tie those words to the
// field: a screen reader reads them with it. They sit in a live region that
// is always there (one per field), so one that appears is announced.
const id = (name: string) => `${uid}-${name}`
const describedBy = (...ids: (string | false)[]) => ids.filter(Boolean).join(' ')
const ipMsg = computed(() => shown('ip'))
const certMsg = computed(() => files.cert || shown('cert'))
const keyMsg = computed(() => files.key || shown('key'))
const detectedNote = computed(() => (detected.value && !differs.value ? t('system.https.ip.detected', { ip: isolate(detected.value) }) : ''))
const ipDescribedBy = computed(() => describedBy(id('ip-help'), !!ipMsg.value && id('ip-problem'), !!detectError.value && id('ip-detect-error'), !!detectedNote.value && id('ip-detected')))
const certDescribedBy = computed(() => describedBy(id('cert-help'), !!certMsg.value && id('cert-problem')))
const keyDescribedBy = computed(() => describedBy(id('key-help'), !!keyMsg.value && id('key-problem')))
const certInput = ref<HTMLInputElement | null>(null)
const keyInput = ref<HTMLInputElement | null>(null)
function dropKey() {
  form.key = ''
  keyFile.value = ''
}
</script>

<template>
  <div class="https">
    <div class="toolbar">
      <span v-if="checkedAt" class="app-muted checked">{{ t('system.checked_at', { time: dateTime(checkedAt, lang) }) }}</span>
      <span class="spacer" />
      <Button :label="t('system.refresh')" icon="pi pi-refresh" size="small" severity="secondary" outlined :loading="loading" class="refresh" @click="load" />
    </div>

    <Message v-if="loadError" severity="error" :closable="false" class="load-error">
      {{ loadError }}
      <div v-if="loadDetail" class="app-ltr detail">{{ loadDetail }}</div>
    </Message>

    <Card v-if="loading && !view" class="status-card">
      <template #content>
        <div class="skeletons">
          <Skeleton v-for="i in 3" :key="i" height="1.2rem" :width="80 - i * 15 + '%'" />
        </div>
      </template>
    </Card>

    <template v-else-if="view && state">
      <Message v-if="problem" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="https-problem">
        {{ problem.text }}
        <div v-if="problem.detail" class="app-ltr detail">{{ problem.detail }}</div>
      </Message>

      <Message v-if="caddyDown" severity="error" icon="pi pi-times-circle" :closable="false" class="caddy-down" role="alert">
        <div class="warn-title caddy-down-title">{{ t('system.https.caddy_down.title') }}</div>
        <p class="warn-one caddy-down-detail">{{ t('system.https.caddy_down.detail', { status: isolate(STATUS_COMMAND), logs: isolate(CADDY_LOGS_COMMAND) }) }}</p>
        <div v-if="caddyDownWhy" class="app-ltr detail caddy-down-why">{{ caddyDownWhy }}</div>
      </Message>

      <Card class="status-card">
        <template #title>{{ t('system.https.status.title') }}</template>
        <template #content>
          <dl class="app-facts facts">
            <dt>{{ t('system.https.status.mode') }}</dt>
            <dd class="saved-mode">
              <Tag :value="modeLabel(state.mode)" severity="info" />
              <span v-if="serving" class="app-muted serving">{{ t('system.https.status.serving') }}: {{ serving }}</span>
            </dd>
            <dt>{{ t('system.https.status.domain') }}</dt>
            <dd class="app-ltr app-mono domain">{{ state.domain || '—' }}</dd>
            <template v-if="state.zarinpal_host">
              <dt>{{ t('system.https.status.zarinpal_host') }}</dt>
              <dd class="app-ltr app-mono zarinpal-host">{{ state.zarinpal_host }}</dd>
            </template>
            <template v-if="savedMode === 'ip' && state.ip">
              <dt>{{ t('system.https.status.server_ip') }}</dt>
              <dd class="app-ltr app-mono server-ip">{{ state.ip }}</dd>
            </template>
            <template v-if="saved">
              <dt>{{ t('system.https.status.certificate') }}</dt>
              <dd class="saved-cert">
                <span class="app-ltr">{{ saved.subject }}</span>
                <span class="app-muted">{{ date(saved.not_after, lang) }} · {{ left(saved.not_after) }}</span>
              </dd>
            </template>
            <dt>{{ t('system.https.status.public_url') }}</dt>
            <dd class="public-url">
              <span class="app-ltr app-mono">{{ view.public_url || '—' }}</span>
              <small v-if="domainLinks" class="app-muted links-domain">{{ t('system.https.status.links_domain') }}</small>
              <small v-else-if="overrideLinks" class="app-muted links-override">{{ t('system.https.status.links_override') }}</small>
            </dd>
            <dt>{{ t('system.https.status.applier') }}</dt>
            <dd class="applier">
              <Tag :severity="agentTag[agentKind]" :value="agentView.text" class="agent-state" :class="'agent-' + agentKind" />
              <div v-if="agentView.detail" class="app-ltr detail agent-error">{{ agentView.detail }}</div>
            </dd>
          </dl>
        </template>
      </Card>

      <Card class="addresses-card">
        <template #title>{{ t('system.https.addresses.title') }}</template>
        <template #content>
          <p v-if="!addresses.length" class="app-muted no-addresses">{{ t('system.https.addresses.none') }}</p>
          <ul v-else class="addresses" :aria-label="t('system.https.addresses.list_label')">
            <li v-for="a in addresses" :key="a.address" class="address" :class="'role-' + a.role">
              <div class="address-head">
                <span class="app-ltr app-mono host">{{ a.address }}</span>
                <Tag severity="secondary" :value="roleLabel(a.role)" class="role" />
                <Tag :severity="addressSeverity[addressState(a)]" :value="t('system.https.state.' + addressState(a))" class="address-state" :class="'is-' + addressState(a)" />
              </div>
              <dl v-if="a.cert" class="app-facts details">
                <dt>{{ t('system.https.addresses.issuer') }}</dt>
                <dd class="app-ltr issuer">{{ a.cert.issuer }}</dd>
                <dt>{{ t('system.https.addresses.expires') }}</dt>
                <dd class="expires">{{ date(a.cert.not_after, lang) }} · {{ left(a.cert.not_after, a.days_left) }}</dd>
                <dt>{{ t('system.https.addresses.trusted') }}</dt>
                <dd class="trusted" :class="'trusted-' + trustedKey(a)">{{ t('system.https.addresses.trusted_' + trustedKey(a)) }}</dd>
              </dl>
              <p v-if="a.expect !== 'public' && te('system.https.expect.' + a.expect)" class="app-muted expect">{{ t('system.https.expect.' + a.expect) }}</p>
              <div v-if="a.error && !caddyDown" class="app-ltr detail address-error">{{ a.error }}</div>
            </li>
          </ul>
        </template>
      </Card>

      <Card v-if="canWrite" class="https-form">
        <template #title>{{ t('system.https.form.title') }}</template>
        <template #content>
          <form class="app-form" novalidate @submit.prevent="apply">
            <p class="app-muted intro">{{ t('system.https.form.intro') }}</p>

            <fieldset class="modes" :disabled="applying">
              <legend class="legend">{{ t('system.https.form.modes') }}</legend>
              <div class="cards">
                <label
                  v-for="m in TLS_MODES"
                  :key="m"
                  :for="`${uid}-mode-${m}`"
                  class="card"
                  :class="['mode-' + m, { on: form.mode === m, off: m === 'manual' && !manualAllowed }]"
                >
                  <RadioButton
                    v-model="form.mode"
                    :input-id="`${uid}-mode-${m}`"
                    :name="`${uid}-mode`"
                    :value="m"
                    :disabled="m === 'manual' && !manualAllowed"
                    @update:model-value="edited"
                  />
                  <span class="card-text">
                    <span class="card-title">{{ t('system.https.card.' + m + '.title') }}</span>
                    <small class="app-muted">{{ t('system.https.card.' + m + '.desc') }}</small>
                    <small v-if="m === 'manual' && !manualAllowed" class="problem manual-off">{{ t('system.https.form.manual_off') }}</small>
                  </span>
                </label>
              </div>
            </fieldset>

            <div v-if="form.mode === 'ip'" class="ip-fields">
              <div class="app-field">
                <label :for="id('ip')">{{ t('system.https.ip.label') }}</label>
                <div class="ip-row">
                  <InputText
                    :id="id('ip')"
                    v-model="form.ip"
                    dir="ltr"
                    spellcheck="false"
                    autocomplete="off"
                    :invalid="!!fieldProblem('ip') || !!fieldError('ip')"
                    :aria-invalid="!!ipMsg"
                    :aria-describedby="ipDescribedBy"
                    class="ip-input app-mono"
                    @update:model-value="ipEdited"
                  />
                  <Button :label="t('system.https.ip.detect')" icon="pi pi-search" severity="secondary" outlined :loading="detecting" :disabled="applying" class="detect" @click="detect" />
                </div>
              </div>
              <small :id="id('ip-help')" class="app-muted">{{ t('system.https.ip.hint') }}</small>
              <div class="live" aria-live="polite">
                <small v-if="ipMsg" :id="id('ip-problem')" class="problem ip-problem">{{ ipMsg }}</small>
                <small v-if="detectError" :id="id('ip-detect-error')" class="problem detect-error">{{ detectError }}</small>
                <small v-if="detectedNote" :id="id('ip-detected')" class="app-muted detected">{{ detectedNote }}</small>
              </div>
              <div v-if="confirmNeeded" class="app-check confirm">
                <Checkbox v-model="form.confirmIp" binary :input-id="id('confirm')" class="confirm-box" @update:model-value="edited" />
                <label :for="id('confirm')">
                  <span class="confirm-why">
                    {{ differs ? t('system.https.ip.differs', { detected: isolate(detected), ip: ipHost }) : t('system.https.ip.unknown') }}
                  </span>
                  <span class="confirm-text">{{ t('system.https.ip.confirm', { ip: ipHost }) }}</span>
                </label>
              </div>
            </div>

            <div v-if="form.mode === 'manual'" class="manual-fields">
              <div class="app-field">
                <label :for="id('cert')">{{ t('system.https.cert.label') }}</label>
                <Textarea
                  :id="id('cert')"
                  v-model="form.cert"
                  rows="6"
                  dir="ltr"
                  spellcheck="false"
                  autocomplete="off"
                  :invalid="!!fieldProblem('cert') || !!fieldError('cert')"
                  :aria-invalid="!!certMsg"
                  :aria-describedby="certDescribedBy"
                  class="cert-input app-mono"
                  fluid
                  @update:model-value="typed('cert')"
                />
                <input ref="certInput" type="file" accept=".pem,.crt,.cer,.txt,text/plain" class="file" tabindex="-1" aria-hidden="true" @change="pick('cert', $event)" />
                <div class="file-row">
                  <Button :label="t('system.https.cert.file')" icon="pi pi-upload" severity="secondary" outlined size="small" :disabled="applying" class="cert-file" @click="certInput?.click()" />
                  <small :id="id('cert-help')" class="app-muted">{{ t('system.https.cert.hint') }}</small>
                </div>
                <div class="live" aria-live="polite">
                  <small v-if="certMsg" :id="id('cert-problem')" class="problem cert-problem">{{ certMsg }}</small>
                </div>
              </div>

              <div class="app-field">
                <label :id="id('key-label')" :for="id('key')">{{ t('system.https.key.label') }}</label>
                <div v-if="keyFile" class="key-loaded" role="group" :aria-labelledby="id('key-label')">
                  <i class="pi pi-key" aria-hidden="true" />
                  <span class="key-loaded-text">{{ t('system.https.key.loaded', { file: isolate(keyFile) }) }}</span>
                  <Button :label="t('system.https.key.remove')" icon="pi pi-times" text size="small" severity="secondary" class="key-remove" @click="dropKey" />
                </div>
                <Textarea
                  v-else
                  :id="id('key')"
                  v-model="form.key"
                  rows="4"
                  dir="ltr"
                  spellcheck="false"
                  autocomplete="off"
                  autocapitalize="off"
                  data-1p-ignore
                  :invalid="!!fieldProblem('key') || !!fieldError('key')"
                  :aria-invalid="!!keyMsg"
                  :aria-describedby="keyDescribedBy"
                  class="key-input app-mono"
                  fluid
                  @update:model-value="typed('key')"
                />
                <input ref="keyInput" type="file" accept=".pem,.key,.txt,text/plain" class="file" tabindex="-1" aria-hidden="true" @change="pick('key', $event)" />
                <div class="file-row">
                  <Button :label="t('system.https.key.file')" icon="pi pi-upload" severity="secondary" outlined size="small" :disabled="applying" class="key-file" @click="keyInput?.click()" />
                  <small :id="id('key-help')" class="app-muted">{{ t('system.https.key.hint') }}</small>
                </div>
                <div class="live" aria-live="polite">
                  <small v-if="keyMsg" :id="id('key-problem')" class="problem key-problem">{{ keyMsg }}</small>
                </div>
              </div>
            </div>

            <Message v-if="form.mode === 'ip'" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="warn warn-ip">
              <div class="warn-title">{{ t('system.https.warn.ip_title') }}</div>
              <ul class="warn-list">
                <li v-if="source === 'override'" class="warn-links-override">{{ t('system.https.warn.ip_links_override') }}</li>
                <li v-else class="warn-links">{{ t('system.https.warn.ip_links') }}</li>
                <li>{{ t('system.https.warn.ip_scan') }}</li>
                <li>{{ t('system.https.warn.ip_ct') }}</li>
                <li>{{ t('system.https.warn.ip_ports') }}</li>
                <li>{{ t('system.https.warn.ip_domain') }}</li>
              </ul>
            </Message>
            <Message v-else-if="form.mode === 'manual'" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="warn warn-manual">
              <div class="warn-title">{{ t('system.https.warn.manual_title') }}</div>
              <ul class="warn-list">
                <li>{{ t('system.https.warn.manual_lockout', { command: isolate(RECOVERY_COMMAND) }) }}</li>
                <li>{{ t('system.https.warn.manual_webhooks') }}</li>
                <li>{{ t('system.https.warn.manual_renew') }}</li>
              </ul>
            </Message>
            <Message v-else severity="info" :closable="false" class="warn warn-auto">
              <div class="warn-title">{{ t('system.https.warn.auto_title') }}</div>
              <p class="warn-one">{{ t('system.https.warn.auto_ports') }}</p>
            </Message>

            <Message v-if="serverError" severity="error" :closable="false" class="apply-error" aria-live="assertive">
              {{ serverError.text }}
              <div v-if="serverError.detail" class="app-ltr detail">{{ serverError.detail }}</div>
            </Message>

            <div class="app-actions">
              <Button type="submit" :label="t('system.https.form.apply')" icon="pi pi-check" :loading="applying" :disabled="cantApply" class="apply" />
            </div>
          </form>

          <div class="result" aria-live="polite">
            <template v-if="accepted">
              <Message v-if="accepted.pending" severity="info" :closable="false" class="pending">{{ t('system.https.form.pending') }}</Message>
              <Message v-if="accepted.notes.length" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="accepted-notes">
                <div class="warn-title">{{ t('system.https.form.accepted_with') }}</div>
                <ul class="warn-list">
                  <li v-for="n in accepted.notes" :key="n">{{ n }}</li>
                </ul>
              </Message>
            </template>
            <Message v-if="waiting === 'waiting'" severity="info" icon="pi pi-spin pi-spinner" :closable="false" class="waiting">
              <div class="warn-title">{{ t('system.https.wait.title') }}</div>
              <p class="warn-one">{{ t('system.https.wait.detail', { every: num(httpsPoll.ms / 1000, lang), max: num(httpsPoll.timeoutMs / 1000, lang) }) }}</p>
            </Message>
            <Message v-else-if="waiting === 'ready'" severity="success" :closable="false" class="ready">{{ t('system.https.wait.ready') }}</Message>
            <Message v-else-if="waiting === 'timeout'" severity="warn" :closable="false" class="timeout">{{ t('system.https.wait.timeout') }}</Message>
            <Message v-else-if="waiting === 'failed'" severity="warn" :closable="false" class="poll-failed">{{ t('system.https.wait.failed') }}</Message>
          </div>
        </template>
      </Card>
      <p v-else class="app-muted owner-only">{{ t('system.https.owner_only') }}</p>
    </template>
  </div>
</template>

<style scoped>
.https {
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
.skeletons {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.facts dd {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.25rem 0.6rem;
}
.facts dd.public-url,
.facts dd.applier {
  flex-direction: column;
  align-items: flex-start;
}
.links-domain,
.links-override {
  max-inline-size: 40rem;
}
.addresses {
  list-style: none;
  margin: 0;
  padding: 0;
}
.address {
  padding-block: 0.7rem;
  border-block-end: 1px solid var(--p-content-border-color);
}
.address:first-child {
  padding-block-start: 0;
}
.address:last-child {
  border-block-end: 0;
  padding-block-end: 0;
}
.address-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.4rem 0.6rem;
}
.host {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.details {
  margin-block-start: 0.5rem;
  font-size: 0.9rem;
}
.expect {
  margin-block: 0.4rem 0;
  font-size: 0.85rem;
}
.trusted-no {
  color: var(--p-orange-500);
}
.no-addresses,
.owner-only {
  margin: 0;
}
.modes {
  border: 0;
  margin: 0;
  padding: 0;
  min-inline-size: 0;
}
.legend {
  padding: 0;
  margin-block-end: 0.5rem;
  font-weight: 600;
}
.cards {
  display: grid;
  gap: 0.6rem;
  grid-template-columns: repeat(auto-fit, minmax(min(15rem, 100%), 1fr));
}
.card {
  display: flex;
  /* .app-form label (a field's label over its input) would stack the radio dot above the text */
  flex-direction: row;
  gap: 0.6rem;
  align-items: flex-start;
  padding: 0.7rem 0.8rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--app-radius);
  background: var(--p-content-background);
  cursor: pointer;
  transition: border-color 0.15s, background-color 0.15s;
}
.card:hover {
  border-color: var(--p-primary-color);
}
.card.on {
  border-color: var(--p-primary-color);
  background: var(--p-highlight-background);
}
.card.off {
  cursor: default;
  opacity: 0.75;
}
.card.off:hover {
  border-color: var(--p-content-border-color);
}
.modes:disabled .card {
  cursor: default;
  opacity: 0.7;
}
.card-text {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  min-inline-size: 0;
}
.card-title {
  font-weight: 600;
}
.card small {
  font-size: 0.8rem;
  line-height: 1.5;
}
.problem {
  color: var(--p-red-500);
}
.ip-fields,
.manual-fields {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.manual-fields {
  gap: 1rem;
}
.live {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
/* the region is always there; with nothing in it the gap above it goes */
.ip-fields > .live:empty {
  margin-block-start: -0.5rem;
}
.manual-fields .live {
  gap: 0.25rem;
}
.manual-fields .live:empty {
  margin-block-start: -0.35rem;
}
.ip-row {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.ip-input {
  flex: 1 1 14rem;
  min-inline-size: 0;
}
.confirm {
  /* .app-check sets the direction only: a div needs its display */
  display: flex;
  flex-direction: row;
  align-items: flex-start;
  gap: 0.6rem;
  padding: 0.6rem 0.8rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.5rem;
  background: var(--p-content-hover-background);
}
.confirm-box {
  flex: none;
  margin-block-start: 0.15rem;
}
.confirm label {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}
.confirm-text {
  font-weight: 600;
}
.file {
  position: absolute;
  inline-size: 1px;
  block-size: 1px;
  opacity: 0;
  pointer-events: none;
}
.file-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem 0.75rem;
}
.key-loaded {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.5rem;
}
.warn-title {
  font-weight: 600;
}
.warn-list {
  margin: 0.4rem 0 0;
  padding-inline-start: 1.2rem;
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}
.warn-one {
  margin: 0.3rem 0 0;
}
.result {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}
.result:not(:empty) {
  margin-block-start: var(--app-gap);
}
</style>
