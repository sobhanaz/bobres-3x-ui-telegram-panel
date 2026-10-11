<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Card from 'primevue/card'
import InputText from 'primevue/inputtext'
import Password from 'primevue/password'
import InputOtp from 'primevue/inputotp'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Divider from 'primevue/divider'
import { useAuth } from '../stores/auth'
import { ApiError } from '../api'
import { latinDigits } from '../format'
import { setLang, currentLang } from '../i18n'

const { t } = useI18n()
const auth = useAuth()
const route = useRoute()
const router = useRouter()
const logo = import.meta.env.BASE_URL + 'favicon.svg'

const linking = ref(false)
const error = ref('')
const username = ref('')
const password = ref('')
const code = ref('')
const busy = ref(false)

function messageFor(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case 'link_invalid':
        return t('login.link_invalid')
      case 'login_failed':
        return t('login.failed')
      case 'locked':
        return t('login.locked')
      case 'rate_limited':
        return t('login.rate_limited')
      case 'not_configured':
        return t('login.not_configured')
      case 'network':
        return t('error.network')
    }
  }
  return t('error.generic')
}

// A link from the bot carries its one-time token in the fragment (#t=...),
// which browsers never send to servers or in a Referer header. Watching the
// route also catches a link opened in a tab that already shows this page.
// Either way the next replace takes the spent token out of the history entry.
async function consumeLink(hash: string) {
  const m = /^#t=([A-Za-z0-9_-]{20,})$/.exec(hash)
  if (!m || linking.value) return
  error.value = ''
  linking.value = true
  try {
    await auth.loginWithLink(m[1])
    await router.replace({ name: 'overview' })
  } catch (e) {
    error.value = messageFor(e)
    await router.replace({ name: 'login' })
  } finally {
    linking.value = false
  }
}
watch(() => route.hash, consumeLink, { immediate: true })

async function submit() {
  error.value = ''
  busy.value = true
  try {
    await auth.loginWithPassword(username.value.trim(), password.value, latinDigits(code.value))
    await router.replace({ name: 'overview' })
  } catch (e) {
    error.value = messageFor(e)
    code.value = ''
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login">
    <div class="top">
      <Button :label="t('lang.switch')" text size="small" @click="setLang(currentLang() === 'fa' ? 'en' : 'fa')" />
    </div>
    <Card class="card">
      <template #title>
        <div class="head">
          <img :src="logo" alt="" width="36" height="36" />
          <div>
            <div>{{ t('login.title') }}</div>
            <div class="app-muted sub">{{ t('login.subtitle', { brand: auth.brand }) }}</div>
          </div>
        </div>
      </template>
      <template #content>
        <Message v-if="linking" severity="info" :closable="false">{{ t('login.link_working') }}</Message>
        <Message v-if="error" severity="error" :closable="false" class="msg">{{ error }}</Message>

        <h3>{{ t('login.link_title') }}</h3>
        <p class="app-muted">{{ t('login.link_hint') }}</p>

        <Divider />

        <h3>{{ t('login.password_title') }}</h3>
        <form class="form" @submit.prevent="submit">
          <label>
            <span>{{ t('login.username') }}</span>
            <InputText v-model="username" autocomplete="username" dir="ltr" required />
          </label>
          <label>
            <span>{{ t('login.password') }}</span>
            <Password v-model="password" :feedback="false" toggle-mask input-class="w-full" autocomplete="current-password" required fluid />
          </label>
          <div class="code">
            <span>{{ t('login.code') }}</span>
            <InputOtp v-model="code" :length="6" integer-only dir="ltr" />
          </div>
          <Button type="submit" :label="t('login.submit')" :loading="busy" :disabled="!username || !password || code.length !== 6" />
        </form>
        <p class="app-muted small">{{ t('login.no_password_yet') }}</p>
      </template>
    </Card>
  </div>
</template>

<style scoped>
.login {
  min-block-size: 100vh;
  display: grid;
  place-items: center;
  padding: 1rem;
  box-sizing: border-box;
}
.top {
  position: fixed;
  inset-block-start: 0.75rem;
  inset-inline-end: 0.75rem;
}
.card {
  inline-size: min(28rem, 100%);
}
.head {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}
.sub {
  font-size: 0.9rem;
  font-weight: 400;
}
h3 {
  margin-block: 0.5rem 0.25rem;
  font-size: 1rem;
}
.msg {
  margin-block-end: 0.75rem;
}
.form {
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
}
.form label,
.code {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.small {
  font-size: 0.85rem;
  margin-block-end: 0;
}
</style>
