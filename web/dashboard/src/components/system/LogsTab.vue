<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Message from 'primevue/message'
import SelectButton from 'primevue/selectbutton'
import Skeleton from 'primevue/skeleton'
import Tag from 'primevue/tag'
import { api } from '../../api'
import { errorText } from '../../errors'
import { dateTime, type Lang } from '../../format'
import { LOG_SERVICES, logAttrs, logLevel, logSeverity, logsReason, newestFirst } from '../../system'
import type { SystemLogs } from '../../types'

// The owner's look at what went wrong: the warnings and errors a service kept
// since it last started (they carry Telegram ids, so admins do not see them).
// Lines stay in English, left to right, newest first.
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const service = ref(LOG_SERVICES[0])
const services = computed(() => LOG_SERVICES.map((s) => ({ value: s, label: t('system.service.' + s) })))

const reply = ref<SystemLogs | null>(null)
const loading = ref(true)
const loadError = ref('')
const loadDetail = ref('')
// a slower answer for a service no longer picked must not replace the shown one
let seq = 0
async function load() {
  const n = ++seq
  const name = service.value
  loading.value = true
  loadError.value = ''
  loadDetail.value = ''
  try {
    const r = await api<SystemLogs>('GET', '/system/logs?service=' + encodeURIComponent(name))
    if (n === seq) reply.value = r
  } catch (e) {
    if (n !== seq) return
    reply.value = null
    const err = errorText(e, t)
    loadError.value = t('system.logs.load_error') + ' ' + err.text
    loadDetail.value = err.detail
  } finally {
    if (n === seq) loading.value = false
  }
}
onMounted(load)
function pick(v: unknown) {
  if (typeof v !== 'string' || v === service.value) return
  service.value = v
  reply.value = null
  void load()
}

const lines = computed(() => newestFirst(reply.value?.lines).map((l) => ({ ...l, lvl: logLevel(l), extra: logAttrs(l) })))
const reason = computed(() => logsReason(reply.value?.error))
const showCard = computed(() => (loading.value && !reply.value) || (reply.value && !reason.value) || lines.value.length > 0)
</script>

<template>
  <div class="logs">
    <div class="toolbar">
      <SelectButton
        :model-value="service"
        :options="services"
        option-label="label"
        option-value="value"
        :allow-empty="false"
        :aria-label="t('system.logs.service')"
        class="log-service"
        @update:model-value="pick"
      />
      <span class="spacer" />
      <Button :label="t('system.refresh')" icon="pi pi-refresh" size="small" severity="secondary" outlined :loading="loading" class="refresh" @click="load" />
    </div>
    <p class="app-muted note">
      <i class="pi pi-info-circle" aria-hidden="true" />
      {{ t('system.logs.note') }}
    </p>

    <Message v-if="loadError" severity="error" :closable="false" class="banner load-error">
      {{ loadError }}
      <div v-if="loadDetail" class="app-ltr detail">{{ loadDetail }}</div>
    </Message>

    <Message v-if="reason" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="logs-unread">
      <div class="reason">{{ t('system.logs.reason.' + reason, { service: t('system.service.' + (reply?.service || service)) }) }}</div>
      <div v-if="reason === 'other'" class="app-ltr detail error">{{ reply?.error }}</div>
    </Message>

    <Card v-if="showCard" class="lines-card">
      <template #content>
        <div v-if="loading && !reply" class="skeletons">
          <Skeleton v-for="i in 4" :key="i" height="1.1rem" :width="90 - i * 12 + '%'" />
        </div>
        <p v-else-if="reply && !reason && !lines.length" class="app-muted empty">
          <i class="pi pi-check-circle" aria-hidden="true" />
          {{ t('system.logs.empty') }}
        </p>
        <ol v-else-if="lines.length" dir="ltr" class="app-ltr log-lines" :aria-label="t('system.logs.lines', { service: t('system.service.' + service) })">
          <li v-for="(l, i) in lines" :key="i" class="log-line" :class="l.lvl">
            <div class="line-head">
              <Tag :severity="logSeverity[l.lvl]" :value="l.lvl === 'error' ? 'ERROR' : 'WARN'" class="level" />
              <time class="time" :datetime="new Date(l.time * 1000).toISOString()">{{ dateTime(l.time, lang) }}</time>
            </div>
            <div class="app-mono msg">{{ l.msg }}</div>
            <div v-if="l.extra" class="app-mono attrs">{{ l.extra }}</div>
          </li>
        </ol>
      </template>
    </Card>
  </div>
</template>

<style scoped>
.logs {
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
.log-service {
  flex-wrap: wrap;
}
.note {
  margin: calc(var(--app-gap) * -0.5) 0 0;
  font-size: 0.85rem;
}
.note .pi,
.empty .pi {
  margin-inline-end: 0.3rem;
  vertical-align: middle;
}
.empty {
  margin: 0;
}
.skeletons {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}
.log-lines {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 70vh;
  overflow: auto;
  text-align: left;
}
.log-line {
  padding-block: 0.5rem;
  border-block-end: 1px solid var(--p-content-border-color);
}
.log-line:last-child {
  border-block-end: 0;
}
.line-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  margin-block-end: 0.25rem;
}
.time {
  unicode-bidi: isolate;
  color: var(--p-text-muted-color);
  font-size: 0.8rem;
}
.msg,
.attrs {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.attrs {
  margin-block-start: 0.2rem;
  color: var(--p-text-muted-color);
  font-size: 0.8rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
</style>
