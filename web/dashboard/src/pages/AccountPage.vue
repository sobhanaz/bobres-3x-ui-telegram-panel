<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Card from 'primevue/card'
import InputText from 'primevue/inputtext'
import Password from 'primevue/password'
import InputOtp from 'primevue/inputotp'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import Message from 'primevue/message'
import SessionList from '../components/staff/SessionList.vue'
import { api, ApiError } from '../api'
import { errorText } from '../errors'
import { useAuth, type Me } from '../stores/auth'
import { dateTime, latinDigits, num, type Lang } from '../format'
import type { SessionItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const auth = useAuth()
const toast = useToast()
const confirm = useConfirm()

// Suggest the Telegram username when it fits the username rules.
const suggested = (auth.me?.user.username ?? '').toLowerCase()
const username = ref(auth.me?.password.username ?? (/^[a-z0-9_.-]{3,32}$/.test(suggested) ? suggested : ''))
const password = ref('')
const setup = ref<{ qr: string; secret: string } | null>(null)
const code = ref('')
const busy = ref(false)
const error = ref('')
const changing = ref(false)

const me = computed(() => auth.me)

// Replacing or removing a confirmed password needs the authenticator's
// current code, unless this session is a fresh login link. The server says
// so in /me, and again (code_required) when that window has passed since.
const needCode = ref(false)
const current = ref('')
const askCode = computed(() => needCode.value || (!!me.value?.password.enabled && !!me.value.password.reauth))
const currentCode = computed(() => latinDigits(current.value.trim()))
const codeOK = computed(() => /^\d{6}$/.test(currentCode.value))

function failText(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.code === 'code_required') {
      needCode.value = true
      return t('account.code_required')
    }
    if (e.code === 'username_taken') return t('account.username_taken')
    if (e.code === 'code_wrong') return t('account.code_wrong')
    if (e.code === 'locked') return t('login.locked')
    if (e.code === 'invalid') return e.message
  }
  return errorText(e, t).text
}
function fail(e: unknown) {
  error.value = failText(e)
}

function startChange() {
  error.value = ''
  password.value = ''
  current.value = ''
  username.value = me.value?.password.username ?? username.value
  changing.value = true
}
function cancelChange() {
  error.value = ''
  password.value = ''
  current.value = ''
  changing.value = false
}

async function start() {
  error.value = ''
  busy.value = true
  try {
    const body: Record<string, string> = { username: username.value.trim(), password: password.value }
    if (askCode.value) body.code = currentCode.value
    setup.value = await api<{ qr: string; secret: string }>('POST', '/me/password', body)
    password.value = ''
    current.value = ''
    // Core replaced the old login with the new one, off until its first code:
    // show that, also if the page is left before confirming.
    if (auth.me) auth.me.password = { enabled: false, reauth: false }
  } catch (e) {
    fail(e)
    current.value = ''
  } finally {
    busy.value = false
  }
}

async function confirmCode() {
  error.value = ''
  busy.value = true
  try {
    auth.set(await api<Me>('POST', '/me/password/confirm', { code: latinDigits(code.value) }))
    setup.value = null
    changing.value = false
    needCode.value = false
    code.value = ''
    toast.add({ severity: 'success', summary: t('account.enabled'), life: 5000 })
    // Turning the password on logs the other devices out.
    void loadSessions()
  } catch (e) {
    fail(e)
    code.value = ''
  } finally {
    busy.value = false
  }
}

// Turning the password off: a confirm, with the current code when needed.
const removeOpen = ref(false)
const removeBusy = ref(false)
const removeError = ref('')
function openRemove() {
  removeError.value = ''
  current.value = ''
  removeOpen.value = true
}
async function confirmRemove() {
  if (askCode.value && !codeOK.value) return
  removeError.value = ''
  removeBusy.value = true
  try {
    auth.set(await api<Me>('DELETE', '/me/password', askCode.value ? { code: currentCode.value } : {}))
    removeOpen.value = false
    needCode.value = false
    current.value = ''
    toast.add({ severity: 'info', summary: t('account.removed'), life: 4000 })
  } catch (e) {
    removeError.value = failText(e)
    current.value = ''
  } finally {
    removeBusy.value = false
  }
}

// Devices logged in with this account.
const sessions = ref<SessionItem[]>([])
const sessionsLoading = ref(false)
const sessionsError = ref('')
const revoking = ref('')
const others = computed(() => sessions.value.filter((s) => !s.current).length)
async function loadSessions() {
  sessionsLoading.value = true
  sessionsError.value = ''
  try {
    sessions.value = (await api<{ items: SessionItem[] }>('GET', '/me/sessions')).items
  } catch (e) {
    sessionsError.value = errorText(e, t).text
  } finally {
    sessionsLoading.value = false
  }
}
onMounted(loadSessions)

async function revoke(s: SessionItem) {
  revoking.value = s.id
  sessionsError.value = ''
  try {
    await api('POST', `/me/sessions/${s.id}/revoke`, {})
    toast.add({ severity: 'success', summary: t('account.revoked'), life: 3000 })
    await loadSessions()
  } catch (e) {
    sessionsError.value = errorText(e, t).text
  } finally {
    revoking.value = ''
  }
}

const othersBusy = ref(false)
function revokeOthers() {
  confirm.require({
    header: t('account.revoke_others'),
    message: t('account.revoke_others_confirm'),
    acceptProps: { label: t('account.revoke_others'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      othersBusy.value = true
      sessionsError.value = ''
      try {
        const res = await api<{ revoked: number }>('POST', '/me/sessions/revoke-others', {})
        toast.add({ severity: 'success', summary: t('account.revoked_others', { n: num(res.revoked, lang.value) }), life: 4000 })
        await loadSessions()
      } catch (e) {
        sessionsError.value = errorText(e, t).text
      } finally {
        othersBusy.value = false
      }
    },
  })
}
</script>

<template>
  <h1 class="app-page-title">{{ t('account.title') }}</h1>
  <div v-if="me" class="cols">
    <Card>
      <template #content>
        <dl>
          <dt>{{ t('account.telegram') }}</dt>
          <dd><bdi>{{ me.user.username ? '@' + me.user.username : '' }}</bdi> <span class="app-ltr app-muted">{{ me.user.telegram_id }}</span></dd>
          <dt>{{ t('account.role') }}</dt>
          <dd>{{ t('role.' + me.user.role) }}</dd>
          <dt>{{ t('account.session') }}</dt>
          <dd>{{ t(me.session.method === 'link' ? 'account.session_link' : 'account.session_password', { time: dateTime(me.session.expires_at, lang) }) }}</dd>
        </dl>
      </template>
    </Card>

    <Card>
      <template #title>{{ t('account.password_title') }}</template>
      <template #content>
        <Message v-if="error" severity="error" :closable="false" class="msg">{{ error }}</Message>
        <template v-if="!me.password_available">
          <p class="app-muted">{{ t('account.password_unavailable') }}</p>
        </template>
        <template v-else-if="setup">
          <p>{{ t('account.scan') }}</p>
          <img :src="setup.qr" alt="" width="200" height="200" class="qr" />
          <!-- the key reads left to right but sits where the page's lines start -->
          <p><span class="app-ltr secret">{{ setup.secret }}</span></p>
          <p>{{ t('account.confirm_code') }}</p>
          <form class="form" @submit.prevent="confirmCode">
            <InputOtp v-model="code" :length="6" integer-only dir="ltr" />
            <Button type="submit" :label="t('account.confirm')" :loading="busy" :disabled="code.length !== 6" />
          </form>
        </template>
        <template v-else-if="me.password.enabled && !changing">
          <p>{{ t('account.password_on', { username: me.password.username }) }}</p>
          <div class="buttons">
            <Button :label="t('account.change')" icon="pi pi-pencil" outlined @click="startChange" />
            <Button :label="t('account.remove')" severity="danger" outlined @click="openRemove" />
          </div>
        </template>
        <template v-else>
          <template v-if="!changing">
            <p class="app-muted">{{ t('account.password_off') }}</p>
            <p>{{ t('account.password_intro') }}</p>
          </template>
          <p v-else>{{ t('account.change_intro') }}</p>
          <form class="form" @submit.prevent="start">
            <label>
              <span>{{ t('login.username') }}</span>
              <InputText v-model="username" dir="ltr" autocomplete="username" required />
              <small class="app-muted">{{ t('account.username_hint') }}</small>
            </label>
            <label>
              <span>{{ t('login.password') }}</span>
              <!-- no strength popup: it covers the button and its labels are not translated -->
              <Password v-model="password" :feedback="false" toggle-mask autocomplete="new-password" required fluid />
              <small class="app-muted">{{ t('account.password_hint') }}</small>
            </label>
            <label v-if="askCode">
              <span>{{ t('account.code_label') }}</span>
              <InputText v-model="current" inputmode="numeric" autocomplete="one-time-code" dir="ltr" maxlength="6" class="code" />
              <small class="app-muted">{{ t('account.code_hint') }}</small>
            </label>
            <div class="buttons">
              <Button v-if="changing" type="button" :label="t('app.cancel')" severity="secondary" outlined @click="cancelChange" />
              <Button type="submit" :label="t('account.start')" :loading="busy" :disabled="!username || password.length < 10 || (askCode && !codeOK)" />
            </div>
          </form>
        </template>
      </template>
    </Card>

    <Card class="wide">
      <template #title>
        <div class="card-head">
          <span>{{ t('account.sessions_title') }}</span>
          <Button
            v-if="others > 0"
            :label="t('account.revoke_others')"
            icon="pi pi-sign-out"
            severity="danger"
            outlined
            size="small"
            :loading="othersBusy"
            @click="revokeOthers"
          />
        </div>
      </template>
      <template #content>
        <p class="app-muted intro">{{ t('account.sessions_intro') }}</p>
        <Message v-if="sessionsError" severity="error" :closable="false" class="msg">{{ sessionsError }}</Message>
        <SessionList :items="sessions" :loading="sessionsLoading" :busy="revoking" :empty="t('account.no_sessions')" @revoke="revoke" />
      </template>
    </Card>
  </div>

  <Dialog v-model:visible="removeOpen" :header="t('account.password_title')" modal :style="{ inlineSize: 'min(30rem, 94vw)' }" :draggable="false">
    <p class="dialog-msg">{{ t('account.remove_confirm') }}</p>
    <form class="form" @submit.prevent="confirmRemove">
      <label v-if="askCode">
        <span>{{ t('account.code_label') }}</span>
        <InputText v-model="current" inputmode="numeric" autocomplete="one-time-code" dir="ltr" maxlength="6" class="code" />
        <small class="app-muted">{{ t('account.code_hint') }}</small>
      </label>
    </form>
    <Message v-if="removeError" severity="error" :closable="false" class="dialog-error">{{ removeError }}</Message>
    <template #footer>
      <Button :label="t('app.cancel')" severity="secondary" outlined @click="removeOpen = false" />
      <Button :label="t('account.remove')" severity="danger" :loading="removeBusy" :disabled="askCode && !codeOK" @click="confirmRemove" />
    </template>
  </Dialog>
</template>

<style scoped>
.cols {
  display: grid;
  gap: var(--app-gap);
  grid-template-columns: repeat(auto-fit, minmax(min(20rem, 100%), 1fr));
  align-items: start;
}
.wide {
  grid-column: 1 / -1;
}
dl {
  margin: 0;
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 0.5rem 1rem;
}
dt {
  color: var(--p-text-muted-color);
}
dd {
  margin: 0;
  min-inline-size: 0;
  overflow-wrap: anywhere;
}
.form {
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
  align-items: flex-start;
}
.form label {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  inline-size: 100%;
}
.buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.code {
  max-inline-size: 10rem;
  letter-spacing: 0.2em;
}
.card-head {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem 1rem;
  align-items: center;
  justify-content: space-between;
}
.intro {
  margin-block: 0 0.75rem;
}
.qr {
  border-radius: 0.5rem;
  background: #fff;
  padding: 0.5rem;
}
.secret {
  font-family: ui-monospace, monospace;
  word-break: break-all;
}
.msg {
  margin-block-end: 0.75rem;
}
.dialog-msg {
  margin-block-start: 0;
}
.dialog-error {
  margin-block-start: 0.75rem;
}
</style>
