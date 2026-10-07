<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Card from 'primevue/card'
import InputText from 'primevue/inputtext'
import Password from 'primevue/password'
import InputOtp from 'primevue/inputotp'
import Button from 'primevue/button'
import Message from 'primevue/message'
import { api, ApiError } from '../api'
import { useAuth, type Me } from '../stores/auth'
import { dateTime, latinDigits, type Lang } from '../format'

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

const me = computed(() => auth.me)

function fail(e: unknown) {
  if (e instanceof ApiError) {
    if (e.code === 'username_taken') return (error.value = t('account.username_taken'))
    if (e.code === 'code_wrong') return (error.value = t('account.code_wrong'))
    if (e.code === 'invalid') return (error.value = e.message)
  }
  error.value = t('error.generic')
}

async function start() {
  error.value = ''
  busy.value = true
  try {
    setup.value = await api<{ qr: string; secret: string }>('POST', '/me/password', { username: username.value.trim(), password: password.value })
    password.value = ''
  } catch (e) {
    fail(e)
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
    code.value = ''
    toast.add({ severity: 'success', summary: t('account.enabled'), life: 5000 })
  } catch (e) {
    fail(e)
    code.value = ''
  } finally {
    busy.value = false
  }
}

function remove() {
  confirm.require({
    header: t('account.password_title'),
    message: t('account.remove_confirm'),
    acceptProps: { label: t('account.remove'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      try {
        auth.set(await api<Me>('DELETE', '/me/password'))
        toast.add({ severity: 'info', summary: t('account.removed'), life: 4000 })
      } catch (e) {
        fail(e)
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
        <template v-else-if="me.password.enabled && !setup">
          <p>{{ t('account.password_on', { username: me.password.username }) }}</p>
          <Button :label="t('account.remove')" severity="danger" outlined @click="remove" />
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
        <template v-else>
          <p class="app-muted">{{ t('account.password_off') }}</p>
          <p>{{ t('account.password_intro') }}</p>
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
            <Button type="submit" :label="t('account.start')" :loading="busy" :disabled="!username || password.length < 10" />
          </form>
        </template>
      </template>
    </Card>
  </div>
</template>

<style scoped>
.cols {
  display: grid;
  gap: var(--app-gap);
  grid-template-columns: repeat(auto-fit, minmax(20rem, 1fr));
  align-items: start;
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
</style>
