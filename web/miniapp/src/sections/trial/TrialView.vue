<script setup lang="ts">
// The free trial: what it gives, one tap to take it, then the order is
// followed until the service is delivered. It is one per account, in the bot or
// here (core checks), so a second tap or a second device gets "used".
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, ApiError, type Order } from '../../api'
import { errorKey, gateFor } from '../../errors'
import { fmtBytes, fmtNum } from '../../fmt'
import { lang, planName, tr } from '../../i18n'
import { fail, loadServices, state } from '../../store'
import { haptic } from '../../telegram'
import { trialApi, type TrialOffer } from './api'

const emit = defineEmits<{ services: [] }>()

const offer = ref<TrialOffer | null>(null)
const order = ref<Order | null>(null)
const busy = ref(false)
const error = ref('')
let timer: ReturnType<typeof setInterval> | undefined

function stop() {
  if (timer) clearInterval(timer)
  timer = undefined
}
onBeforeUnmount(stop)

function oops(e: unknown) {
  if (fail(e)) return
  error.value = tr(errorKey(e))
  haptic('error')
}

async function load() {
  error.value = ''
  try {
    offer.value = await trialApi.get()
  } catch (e) {
    oops(e)
  }
}
onMounted(load)

const facts = computed(() => {
  const p = offer.value?.plan
  if (!p) return ''
  const parts: string[] = []
  if (p.days) parts.push(tr('plans.days', { n: fmtNum(p.days) }))
  if (p.traffic_bytes) parts.push(fmtBytes(p.traffic_bytes))
  else if (p.kind !== 'time') parts.push(tr('plans.unlimited'))
  return parts.join(' · ')
})

// The customer sees it gone from the More tab straight away.
function taken() {
  if (state.me) state.me.features.trial = false
}

async function start() {
  error.value = ''
  busy.value = true
  try {
    order.value = await trialApi.start()
    taken()
    haptic('success')
    poll()
  } catch (e) {
    if (e instanceof ApiError && e.code === 'trial_used') {
      // taken meanwhile, in the bot or on another device
      taken()
      offer.value = { available: false, used: true }
    } else oops(e)
  } finally {
    busy.value = false
  }
}

// Follow the order every few seconds until the service is delivered or it ends.
function poll() {
  stop()
  let n = 0
  timer = setInterval(async () => {
    if (!order.value || ++n > 80) return stop()
    try {
      const o = await api.order(order.value.id)
      order.value = o
      if (['active', 'provision_failed', 'cancelled', 'expired'].includes(o.status)) {
        stop()
        if (o.status === 'active') void loadServices().catch(() => {})
      }
    } catch (e) {
      if (gateFor(e)) {
        stop()
        fail(e)
      }
    }
  }, 3000)
}

const progress = computed(() => {
  switch (order.value?.status) {
    case 'active':
      return 'trial.ready'
    case 'provision_failed':
      return 'trial.stuck'
    case 'cancelled':
    case 'expired':
      return 'trial.cancelled'
    default:
      return 'trial.setting_up'
  }
})
</script>

<template>
  <section>
    <h1>{{ tr('trial.title') }}</h1>
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>

    <section v-if="order" class="card" role="status">
      <p>{{ tr(progress) }}</p>
      <button v-if="order.status === 'active' || order.status === 'provision_failed'" class="btn" type="button" @click="emit('services')">{{ tr('trial.view') }}</button>
    </section>

    <p v-else-if="offer === null && !error" class="muted">{{ tr('common.loading') }}</p>
    <p v-else-if="offer && offer.used" class="muted">{{ tr('trial.used') }}</p>
    <p v-else-if="offer && !offer.available" class="muted">{{ tr('trial.unavailable') }}</p>
    <section v-else-if="offer && offer.plan" class="card">
      <strong>{{ planName(offer.plan.names, lang) }}</strong>
      <p class="muted">{{ tr('trial.intro') }}</p>
      <p>{{ tr('trial.gives', { facts }) }}</p>
      <button class="btn" type="button" :disabled="busy" @click="start">{{ tr('trial.start') }}</button>
    </section>
  </section>
</template>
