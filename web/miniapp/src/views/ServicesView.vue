<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { Service } from '../api'
import { errorKey } from '../errors'
import { tr } from '../i18n'
import { fmtBytes, fmtDate, fmtNum, statusKey } from '../fmt'
import { daysLeft } from '../format'
import { fail, loadServices, state } from '../store'
import { copyText, openLink } from '../telegram'

const emit = defineEmits<{ renew: [s: Service]; topup: [s: Service] }>()
const error = ref('')
const copied = ref('')

async function load() {
  error.value = ''
  try {
    await loadServices()
  } catch (e) {
    if (!fail(e)) error.value = tr(errorKey(e))
  }
}
onMounted(load)

async function copy(s: Service) {
  try {
    await copyText(s.link)
    copied.value = s.id
    setTimeout(() => (copied.value = ''), 1500)
  } catch {
    // no clipboard
  }
}

function expiry(s: Service): string {
  if (!s.expires_at) return ''
  const n = daysLeft(s.expires_at, Date.now())
  return n < 0 ? tr('services.expired') : tr('services.days_left', { n: fmtNum(n) }) + ' · ' + tr('services.expires', { date: fmtDate(s.expires_at) })
}

function usage(s: Service): string {
  return s.traffic_total
    ? tr('services.traffic', { used: fmtBytes(s.traffic_used), total: fmtBytes(s.traffic_total) })
    : tr('services.traffic_free', { used: fmtBytes(s.traffic_used) })
}

const percent = (s: Service) => (s.traffic_total ? Math.min(100, Math.round((s.traffic_used / s.traffic_total) * 100)) : 0)
</script>

<template>
  <section>
    <h1>{{ tr('services.title') }}</h1>
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-else-if="state.services === null" class="muted">{{ tr('common.loading') }}</p>
    <p v-else-if="!state.services.length" class="muted">{{ tr('services.empty') }}</p>
    <ul v-else class="list">
      <li v-for="s in state.services" :key="s.id" class="card">
        <div class="row between">
          <strong>{{ s.label }}</strong>
          <span class="badge" :class="s.status">{{ tr(statusKey(s.status)) }}</span>
        </div>
        <p class="muted">{{ expiry(s) }}</p>
        <p class="muted">{{ usage(s) }}</p>
        <div v-if="s.traffic_total" class="bar" role="progressbar" :aria-valuenow="percent(s)" aria-valuemin="0" aria-valuemax="100">
          <span :style="{ width: percent(s) + '%' }"></span>
        </div>
        <div class="row wrap">
          <button v-if="s.link" class="btn secondary" type="button" @click="copy(s)">
            {{ copied === s.id ? tr('common.copied') : tr('services.link') }}
          </button>
          <button v-if="s.page_url" class="btn secondary" type="button" @click="openLink(s.page_url)">{{ tr('services.page') }}</button>
          <button v-if="s.can_renew" class="btn" type="button" @click="emit('renew', s)">{{ tr('services.renew') }}</button>
          <button v-if="s.can_topup" class="btn" type="button" @click="emit('topup', s)">{{ tr('services.topup') }}</button>
        </div>
      </li>
    </ul>
  </section>
</template>
