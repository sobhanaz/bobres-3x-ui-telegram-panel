<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch, watchEffect } from 'vue'
import { I18nT, useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Card from 'primevue/card'
import InputText from 'primevue/inputtext'
import Password from 'primevue/password'
import InputOtp from 'primevue/inputotp'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Divider from 'primevue/divider'
import ProgressBar from 'primevue/progressbar'
import { useAuth } from '../stores/auth'
import { ApiError } from '../api'
import { latinDigits } from '../format'
import LangMenu from '../components/LangMenu.vue'
import { APPROVAL_COMMAND, POLL_MS, clock, pendingState, reasonKey, secondsLeft } from '../security'
import { applyBrandColor, applyDocumentBrand } from '../theme'
import mascotWave from '../assets/brand/bobres-wave.webp'
import mascotStill from '../assets/brand/bobres-mascot.webp'

const { t } = useI18n()
const auth = useAuth()
const route = useRoute()
const router = useRouter()

// The store's public name, colour and logo (no session needed).
const brandName = computed(() => auth.publicBrand?.name || auth.brand)
const icon = import.meta.env.BASE_URL + 'favicon.svg'
const logo = computed(() => auth.publicBrand?.logo || icon)
// BOBRES the beaver greets on a store with the default look (waving, or
// standing still for those who turn motion off). A store with its own logo
// (white label) never shows him, and he waits for /brand to answer so a
// store's logo is never preceded by a flash of the mascot.
const mascot = computed(() => auth.publicBrand !== null && !auth.publicBrand.logo)
// "Powered by BOBRES" is the default: shown once /brand has answered unless it
// says no, and shown too when /brand fails (an error never hides it). It waits
// for the answer so a white-label store never sees a flash of it.
const showPoweredBy = computed(() => auth.publicBrandSettled && auth.publicBrand?.powered_by !== false)
onMounted(() => {
  if (auth.publicBrand && !auth.me) applyBrandColor(auth.publicBrand.color)
  void auth.loadPublicBrand()
  // A page reloaded while a sign-in waits for its approval finds the wait again (the pending cookie is the browser's).
  if (!/^#t=/.test(route.hash)) void resume()
})
watchEffect(() => applyDocumentBrand(t('branding.doc_title', { brand: brandName.value }), auth.publicBrand?.logo))

const linking = ref(false)
const error = ref('')
const username = ref('')
const password = ref('')
const code = ref('')
const busy = ref(false)
// Why no approval can be asked for in Telegram (core's reason): shown with the command that signs in without it.
const unavailable = ref('')

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
      case 'approval_rate_limited':
        return t('login.approval_rate_limited')
      case 'not_configured':
        return t('login.not_configured')
      case 'network':
        return t('error.network')
    }
  }
  return t('error.generic')
}

// A login that needs an approval cannot be asked for (the bot is down, nobody can be reached): the reason, and the way in that needs none.
function failed(e: unknown) {
  if (e instanceof ApiError && e.code === 'approval_unavailable') {
    unavailable.value = reasonKey(e.data.reason)
    return
  }
  error.value = messageFor(e)
}

// What a login came to: a session (go in), or a wait for an approval in Telegram (the card below shows it).
async function entered(how: 'session' | 'pending') {
  if (how === 'session') await router.replace({ name: auth.me?.must_change ? 'account' : 'overview' })
}

// A link from the bot carries its one-time token in the fragment (#t=...),
// which browsers never send to servers or in a Referer header. Watching the
// route also catches a link opened in a tab that already shows this page.
// Either way the next replace takes the spent token out of the history entry.
async function consumeLink(hash: string) {
  const m = /^#t=([A-Za-z0-9_-]{20,})$/.exec(hash)
  if (!m || linking.value) return
  error.value = ''
  unavailable.value = ''
  linking.value = true
  try {
    const how = await auth.loginWithLink(m[1])
    if (how === 'session') await entered(how)
    else await router.replace({ name: 'login' }) // the spent token leaves the address bar while the request waits
  } catch (e) {
    failed(e)
    await router.replace({ name: 'login' })
  } finally {
    linking.value = false
  }
}
watch(() => route.hash, consumeLink, { immediate: true })

async function submit() {
  error.value = ''
  unavailable.value = ''
  busy.value = true
  try {
    // The initial password only opens the page where a new one is chosen.
    await entered(await auth.loginWithPassword(username.value.trim(), password.value, latinDigits(code.value)))
  } catch (e) {
    failed(e)
    code.value = ''
  } finally {
    busy.value = false
  }
}

// The wait for an approval in Telegram: asked about every few seconds until it ends. The request lives 5 minutes in core, whatever this page does.
const now = ref(Date.now())
let pollTimer: ReturnType<typeof setInterval> | undefined
let tickTimer: ReturnType<typeof setInterval> | undefined
let polling = false

const waiting = computed(() => auth.pending)
const left = computed(() => (waiting.value ? secondsLeft(waiting.value.expires_at, now.value) : 0))
const waited = computed(() => (waiting.value ? Math.max(0, waiting.value.ttl_s - left.value) : 0))
const waitState = computed(() => (waiting.value ? pendingState(waiting.value, waited.value) : 'self'))
const percentLeft = computed(() => (waiting.value && waiting.value.ttl_s > 0 ? Math.round((left.value / waiting.value.ttl_s) * 100) : 0))
const showWayOut = computed(() => waitState.value === 'slow' || waitState.value === 'failed')

function stopWaiting() {
  clearInterval(pollTimer)
  clearInterval(tickTimer)
  pollTimer = tickTimer = undefined
}
function startWaiting() {
  stopWaiting()
  now.value = Date.now()
  tickTimer = setInterval(() => (now.value = Date.now()), 1000)
  pollTimer = setInterval(() => void poll(), POLL_MS)
}
watch(
  () => auth.pending !== null,
  (isWaiting) => (isWaiting ? startWaiting() : stopWaiting()),
  { immediate: true },
)
onBeforeUnmount(stopWaiting)

async function poll() {
  if (polling || !auth.pending) return
  polling = true
  try {
    const r = await auth.pollPending()
    if (r === 'session') {
      stopWaiting()
      await entered('session')
    } else if (r !== 'pending') {
      stopWaiting()
      if (r !== 'none') error.value = t('login.ended.' + r)
    }
  } catch (e) {
    // A network blip or a pause the server asked for keeps the wait going; anything else ends it.
    if (e instanceof ApiError && e.code !== 'network' && e.code !== 'rate_limited') {
      stopWaiting()
      auth.pending = null
      failed(e)
    }
  } finally {
    polling = false
  }
}

// One look at load: a wait that was open before the page was reloaded shows again (or, approved meanwhile, lets the person in).
async function resume() {
  try {
    const r = await auth.pollPending()
    if (r === 'session') await entered('session')
    else if (r !== 'pending' && r !== 'none') error.value = t('login.ended.' + r)
  } catch {
    /* nothing to resume */
  }
}

async function cancelWaiting() {
  try {
    await auth.cancelPending()
  } catch {
    /* the request ends on its own in 5 minutes */
  }
}
</script>

<template>
  <div class="login">
    <div class="top">
      <LangMenu />
    </div>
    <div class="middle">
      <picture v-if="mascot" class="mascot">
        <source :srcset="mascotStill" media="(prefers-reduced-motion: reduce)" />
        <img :src="mascotWave" alt="" width="420" height="420" decoding="async" />
      </picture>
      <Card class="card">
        <template #title>
          <div class="head">
            <img :src="logo" alt="" width="36" height="36" class="logo" />
            <div>
              <div>{{ t('login.title') }}</div>
              <div class="app-muted sub">{{ t('login.subtitle', { brand: brandName }) }}</div>
            </div>
          </div>
        </template>
        <template #content>
          <Message v-if="linking" severity="info" :closable="false">{{ t('login.link_working') }}</Message>
          <Message v-if="error" severity="error" :closable="false" class="msg">{{ error }}</Message>
          <Message v-if="unavailable" severity="error" :closable="false" class="msg unavailable">
            <I18nT :keypath="'login.approval.' + unavailable" tag="span" class="hint">
              <template #cmd><code class="cmd" dir="ltr">{{ APPROVAL_COMMAND }}</code></template>
            </I18nT>
          </Message>

          <section v-if="waiting" class="waiting" role="status" aria-live="polite" aria-labelledby="pending-title">
            <i class="pi pi-send icon" aria-hidden="true" />
            <h3 id="pending-title">{{ t('login.pending_title') }}</h3>
            <p class="line">{{ t('login.pending_' + waitState) }}</p>
            <p class="app-muted time">{{ left > 0 ? t('login.pending_left', { time: clock(left) }) : t('login.pending_checking') }}</p>
            <ProgressBar :value="percentLeft" :show-value="false" class="bar" :aria-label="t('login.pending_title')" />
            <I18nT v-if="showWayOut" keypath="login.pending_way_out" tag="p" class="app-muted hint">
              <template #cmd><code class="cmd" dir="ltr">{{ waiting.command || APPROVAL_COMMAND }}</code></template>
            </I18nT>
            <Button :label="t('login.pending_cancel')" severity="secondary" outlined class="cancel" @click="cancelWaiting" />
          </section>

          <template v-else>
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
              <span>{{ t('login.code_optional') }}</span>
              <InputOtp v-model="code" :length="6" integer-only dir="ltr" class="otp" />
              <I18nT keypath="login.code_hint" tag="small" class="app-muted hint">
                <template #cmd><code class="cmd" dir="ltr">bobres admin password</code></template>
              </I18nT>
            </div>
            <Button type="submit" :label="t('login.submit')" :loading="busy" :disabled="!username || !password || (code.length > 0 && code.length !== 6)" />
          </form>
          <I18nT keypath="login.initial_hint" tag="p" class="app-muted small hint">
            <template #cmd><code class="cmd" dir="ltr">bobres admin password</code></template>
          </I18nT>

          <Divider />

          <h3>{{ t('login.link_title') }}</h3>
          <I18nT keypath="login.link_hint" tag="p" class="app-muted hint">
            <template #dashboard><code class="cmd" dir="ltr">/dashboard</code></template>
            <template #admin><code class="cmd" dir="ltr">/admin</code></template>
          </I18nT>
          <p class="app-muted small">{{ t('login.no_password_yet') }}</p>
          </template>
        </template>
      </Card>
    </div>
    <footer v-if="showPoweredBy" class="attribution app-muted">
      <div class="powered-by"><img :src="icon" alt="" width="16" height="16" />{{ t('license.powered_by') }}</div>
      <div class="made-by">{{ t('license.made_by') }}</div>
    </footer>
  </div>
</template>

<style scoped>
.login {
  min-block-size: 100vh;
  display: grid;
  /* the card in the middle, the attribution (when shown) at the bottom */
  grid-template-rows: 1fr auto;
  place-items: center;
  padding: 1rem;
  box-sizing: border-box;
}
.top {
  position: fixed;
  inset-block-start: 0.75rem;
  inset-inline-end: 0.75rem;
}
.middle {
  inline-size: min(28rem, 100%);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.75rem;
}
.card {
  inline-size: 100%;
}
/* The mascot's navy backdrop is a round badge in light mode and melts into
   the navy page in dark mode. */
.mascot {
  display: block;
  inline-size: clamp(7rem, 32vw, 10rem);
}
.mascot img {
  display: block;
  inline-size: 100%;
  block-size: auto;
  border-radius: 50%;
  box-shadow: 0 0.75rem 1.5rem -0.75rem rgb(15 23 42 / 0.55);
}
/* A slow float, only for those who have not asked for less motion. */
@media (prefers-reduced-motion: no-preference) {
  .mascot {
    animation: mascot-in 0.4s ease-out both;
  }
  .mascot img {
    animation: mascot-float 6s ease-in-out infinite;
  }
}
@keyframes mascot-in {
  from {
    opacity: 0;
    scale: 0.92;
  }
}
@keyframes mascot-float {
  50% {
    translate: 0 -0.4rem;
  }
}
.head {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}
.logo {
  object-fit: contain;
  border-radius: 0.5rem;
  flex: none;
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
/* The wait for an approval in Telegram: one calm column in the middle of the card. */
.waiting {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  gap: 0.5rem;
}
.waiting .icon {
  font-size: 1.75rem;
  color: var(--p-primary-color);
}
.waiting h3 {
  margin-block: 0;
}
.waiting .line,
.waiting .time {
  margin-block: 0;
}
.waiting .bar {
  inline-size: 100%;
  block-size: 0.4rem;
}
.waiting .cancel {
  margin-block-start: 0.5rem;
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
/* Help texts: roomy lines, and a command or a path is a small LTR chip so the right-to-left sentence around it never scrambles it. */
.hint {
  line-height: 1.9;
}
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
/* The six boxes of the code share the row evenly (left to right, as the digits are typed) instead of huddling at one side. */
.otp {
  inline-size: 100%;
}
.otp :deep(.p-inputotp) {
  display: grid;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  gap: 0.5rem;
  direction: ltr;
}
.otp :deep(.p-inputotp-input) {
  inline-size: 100%;
  min-inline-size: 0;
  aspect-ratio: 1 / 1.1;
  text-align: center;
  font-size: 1.25rem;
  padding: 0;
}
.attribution {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.15rem;
  font-size: 0.8rem;
  padding-block-start: 0.75rem;
}
.powered-by {
  display: flex;
  align-items: center;
  gap: 0.35rem;
}
.powered-by img {
  flex: none;
}
</style>
