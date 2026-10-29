<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { I18nT, useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Checkbox from 'primevue/checkbox'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import Tag from 'primevue/tag'
import ToggleSwitch from 'primevue/toggleswitch'
import { ApiError, api } from '../../api'
import { errorText } from '../../errors'
import { num, type Lang } from '../../format'
import { APPROVAL_COMMAND, reasonKey, type SecurityReply } from '../../security'
import { useAuth } from '../../stores/auth'

// How staff sign in. The owner turns on the approval of dashboard sign-ins in Telegram here: a staff sign-in then opens only after the staff
// member, or an owner, taps Approve in the bot. Turning it on shows what it does and needs the owner's word and, with a password login, the
// current authenticator code; core tells the owner in Telegram first, and keeps the approval off when that message cannot be sent, so it can
// never be switched on with nobody able to answer. Everyone else who reads Settings sees the state.
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const auth = useAuth()
const toast = useToast()

const info = ref<SecurityReply | null>(null)
const loading = ref(true)
const loadError = ref('')

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    info.value = await api<SecurityReply>('GET', '/security')
  } catch (e) {
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

const state = computed(() => info.value?.login_approval ?? null)
const enabled = computed(() => state.value?.enabled === true)
const canChange = computed(() => info.value?.can_change === true)
const ttlMinutes = computed(() => Math.round((state.value?.ttl_s ?? 300) / 60))

// ---- the dialog: turn on, turn off, or change who is asked ----
type Mode = 'on' | 'off' | 'notify'
const mode = ref<Mode | null>(null)
const open = computed({
  get: () => mode.value !== null,
  set: (v: boolean) => {
    if (!v) mode.value = null
  },
})
const acknowledged = ref(false)
const code = ref('')
const askCode = ref(false) // core asked for the authenticator code, or /me says it will
const needsCode = computed(() => askCode.value || auth.me?.password?.reauth === true)
const busy = ref(false)
const error = ref('')
const unavailable = ref('')
// For 'notify': the value the dialog asks to set.
const wantNotify = ref(true)

watch(mode, (m) => {
  if (m) {
    acknowledged.value = false
    code.value = ''
    error.value = ''
    unavailable.value = ''
  }
})

function ask(m: Mode, notify = true) {
  wantNotify.value = notify
  mode.value = m
}

const codeOk = computed(() => !needsCode.value || /^\d{6}$/.test(code.value.trim()))
const ready = computed(() => codeOk.value && (mode.value !== 'on' || acknowledged.value))

async function submit() {
  if (!mode.value || !ready.value || busy.value) return
  busy.value = true
  error.value = ''
  unavailable.value = ''
  const body: Record<string, unknown> = { enabled: mode.value === 'on' ? true : mode.value === 'off' ? false : enabled.value }
  if (mode.value === 'on') body.acknowledge = true
  if (mode.value === 'notify') body.notify_owner = wantNotify.value
  else if (mode.value === 'on') body.notify_owner = state.value?.notify_owner ?? true
  if (code.value.trim()) body.code = code.value.trim()
  const done = mode.value
  try {
    info.value = await api<SecurityReply>('PUT', '/security/login-approval', body)
    mode.value = null
    askCode.value = false
    toast.add({ severity: 'success', summary: t('security.approval.saved_' + done), life: 4000 })
    void auth.refresh() // the code rule of this session may have changed
  } catch (e) {
    if (e instanceof ApiError && e.code === 'approval_unavailable') {
      unavailable.value = reasonKey(e.data.reason)
    } else if (e instanceof ApiError && e.code === 'code_required') {
      askCode.value = true
      error.value = t('account.code_required')
    } else if (e instanceof ApiError && e.code === 'code_wrong') {
      error.value = t('account.code_wrong')
    } else if (e instanceof ApiError && e.code === 'locked') {
      error.value = t('login.locked')
    } else {
      error.value = errorText(e, t).text
    }
  } finally {
    busy.value = false
  }
}

const titleId = 'settings-security-title'
</script>

<template>
  <div class="security">
  <Card class="security-card">
    <template #title>
      <div class="title-row">
        <h2 :id="titleId" class="card-title">{{ t('security.title') }}</h2>
      </div>
    </template>
    <template #subtitle>{{ t('security.desc') }}</template>
    <template #content>
      <div v-if="loading && !info" aria-busy="true"><Skeleton height="6rem" /></div>
      <Message v-else-if="loadError" severity="error" :closable="false">
        <div class="load-error">
          <span>{{ loadError }}</span>
          <Button :label="t('app.retry')" icon="pi pi-refresh" size="small" severity="secondary" outlined @click="load" />
        </div>
      </Message>
      <section v-else-if="state" class="approval" :aria-labelledby="titleId">
        <div class="head">
          <h3 class="name">{{ t('security.approval.title') }}</h3>
          <Tag :severity="enabled ? 'success' : 'secondary'" :value="enabled ? t('security.approval.on') : t('security.approval.off')" class="state" />
        </div>
        <p class="app-muted desc">{{ t('security.approval.desc', { minutes: num(ttlMinutes, lang) }, ttlMinutes) }}</p>

        <p v-if="enabled" class="app-muted waiting">{{ t('security.approval.waiting', { n: num(state.waiting, lang) }) }}</p>
        <label v-if="enabled" class="notify">
          <ToggleSwitch
            :model-value="state.notify_owner"
            :disabled="!canChange || busy"
            input-id="security-notify-owner"
            @update:model-value="(v: boolean) => ask('notify', v)"
          />
          <span class="notify-text">
            <span class="notify-label">{{ t('security.approval.notify_owner') }}</span>
            <small class="app-muted">{{ t('security.approval.notify_owner_hint') }}</small>
          </span>
        </label>

        <Message v-if="!state.bot_ready && !enabled" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="note">
          {{ t('security.approval.no_bot') }}
        </Message>
        <I18nT v-if="enabled" keypath="security.approval.way_out" tag="p" class="app-muted way-out">
          <template #cmd><code class="cmd" dir="ltr">{{ state.command || APPROVAL_COMMAND }}</code></template>
        </I18nT>

        <div class="actions">
          <span v-if="!canChange" class="app-muted"><i class="pi pi-lock" aria-hidden="true" /> {{ t('security.approval.read_only') }}</span>
          <Button
            v-else-if="!enabled"
            :label="t('security.approval.turn_on')"
            icon="pi pi-shield"
            :disabled="!state.bot_ready"
            class="turn-on"
            @click="ask('on')"
          />
          <Button v-else :label="t('security.approval.turn_off')" icon="pi pi-lock-open" severity="secondary" outlined class="turn-off" @click="ask('off')" />
        </div>
      </section>
    </template>
  </Card>

  <Dialog
    v-model:visible="open"
    :header="t('security.approval.dialog.' + (mode ?? 'on') + '_title')"
    modal
    :draggable="false"
    :closable="!busy"
    :style="{ inlineSize: 'min(36rem, 94vw)' }"
    class="security-dialog"
  >
    <form class="body" @submit.prevent="submit">
      <template v-if="mode === 'on'">
        <ul class="points">
          <li><i class="pi pi-lock" aria-hidden="true" /><span>{{ t('security.approval.dialog.point_what') }}</span></li>
          <li><i class="pi pi-send" aria-hidden="true" /><span>{{ t('security.approval.dialog.point_who') }}</span></li>
          <li><i class="pi pi-clock" aria-hidden="true" /><span>{{ t('security.approval.dialog.point_time', { minutes: num(ttlMinutes, lang) }, ttlMinutes) }}</span></li>
          <li>
            <i class="pi pi-exclamation-triangle" aria-hidden="true" />
            <I18nT keypath="security.approval.dialog.point_way_out" tag="span">
              <template #cmd><code class="cmd" dir="ltr">{{ state?.command || APPROVAL_COMMAND }}</code></template>
            </I18nT>
          </li>
          <li><i class="pi pi-check-circle" aria-hidden="true" /><span>{{ t('security.approval.dialog.point_test') }}</span></li>
        </ul>
        <label class="check">
          <Checkbox v-model="acknowledged" binary input-id="security-acknowledge" class="accept" />
          <span>{{ t('security.approval.dialog.acknowledge') }}</span>
        </label>
      </template>
      <p v-else-if="mode === 'off'" class="line">{{ t('security.approval.dialog.off_text') }}</p>
      <p v-else-if="mode === 'notify'" class="line">{{ t('security.approval.dialog.' + (wantNotify ? 'notify_on_text' : 'notify_off_text')) }}</p>

      <label v-if="needsCode" class="field">
        <span>{{ t('account.code_label') }}</span>
        <InputText v-model="code" inputmode="numeric" autocomplete="one-time-code" dir="ltr" maxlength="6" class="code" />
        <small class="app-muted">{{ t('account.code_hint') }}</small>
      </label>

      <Message v-if="unavailable" severity="error" :closable="false" class="error">
        <I18nT :keypath="'security.approval.unavailable.' + unavailable" tag="span">
          <template #cmd><code class="cmd" dir="ltr">{{ state?.command || APPROVAL_COMMAND }}</code></template>
        </I18nT>
      </Message>
      <Message v-if="error" severity="error" :closable="false" class="error">{{ error }}</Message>
    </form>
    <template #footer>
      <Button :label="t('app.cancel')" severity="secondary" outlined :disabled="busy" class="cancel" @click="mode = null" />
      <Button
        :label="t('security.approval.dialog.' + (mode ?? 'on') + '_submit')"
        :icon="mode === 'off' ? 'pi pi-lock-open' : 'pi pi-shield'"
        :loading="busy"
        :disabled="!ready"
        class="submit"
        @click="submit"
      />
    </template>
  </Dialog>
  </div>
</template>

<style scoped>
.title-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
.card-title {
  margin: 0;
  font-size: 1.1rem;
  font-weight: 700;
}
.load-error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
}
.approval {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
}
.name {
  margin: 0;
  font-size: 1rem;
}
.desc,
.waiting,
.way-out {
  margin: 0;
  line-height: 1.8;
}
.notify {
  display: flex;
  align-items: flex-start;
  gap: 0.75rem;
  cursor: pointer;
}
.notify-text {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}
.notify-label {
  font-weight: 600;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
  margin-block-start: 0.25rem;
}
/* A command is a small LTR chip so the right-to-left sentence around it never scrambles it. */
.cmd {
  display: inline-block;
  unicode-bidi: isolate;
  direction: ltr;
  padding: 0 0.4em;
  border-radius: 0.35rem;
  font-size: 0.9em;
  background: color-mix(in srgb, currentColor 12%, transparent);
  white-space: nowrap;
}
.body {
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
}
.points {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.7rem;
}
.points li {
  display: flex;
  align-items: flex-start;
  gap: 0.7rem;
  line-height: 1.8;
}
.points i {
  margin-block-start: 0.35rem;
  color: var(--p-primary-color);
}
.line {
  margin: 0;
  line-height: 1.8;
}
.check {
  display: flex;
  align-items: flex-start;
  gap: 0.6rem;
  cursor: pointer;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.code {
  max-inline-size: 10rem;
}
</style>
