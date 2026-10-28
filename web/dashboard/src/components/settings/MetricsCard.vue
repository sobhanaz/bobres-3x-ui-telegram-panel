<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Button from 'primevue/button'
import Card from 'primevue/card'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import Tag from 'primevue/tag'
import MetricsTokenDialog from './MetricsTokenDialog.vue'
import { api } from '../../api'
import { errorText, fieldError } from '../../errors'
import { num, type Lang } from '../../format'
import { metricsAddress, TOKEN_MAX, TOKEN_MIN, tokenProblem, type MadeToken, type MetricsState } from '../../metrics'
import { useAuth } from '../../stores/auth'

// Prometheus metrics (Settings > Monitoring): /metrics on the store's own domain answers only while a token is set, and
// only to a request that carries it. The token is write-only: core keeps its hash, the owner makes one here and sees it
// once, or types a token of their own. Admins see whether it is on; the owner changes it.
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()
const auth = useAuth()

const state = ref<MetricsState | null>(null)
const loading = ref(true)
const loadError = ref('')
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    state.value = await api<MetricsState>('GET', '/metrics-token')
  } catch (e) {
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

const canChange = computed(() => auth.can('system.write'))
const on = computed(() => state.value?.enabled === true)
const address = computed(() => metricsAddress(window.location.origin))

const busy = ref(false)
const error = ref('')
const detail = ref('')
function fail(e: unknown) {
  const err = errorText(e, t)
  error.value = err.text
  detail.value = err.detail
}

// ---- make a token (shown once)
const made = ref('')
const showing = ref(false)
// The token does not outlive the dialog: closed, it is gone from the page.
watch(showing, (v) => {
  if (!v) made.value = ''
})
async function generate() {
  busy.value = true
  error.value = ''
  try {
    const r = await api<MadeToken>('POST', '/metrics-token/generate')
    state.value = { enabled: r.enabled }
    made.value = r.token
    showing.value = true
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}
function askGenerate() {
  if (!on.value) {
    void generate()
    return
  }
  confirm.require({
    header: t('prometheus.replace_title'),
    message: t('prometheus.replace_confirm'),
    icon: 'pi pi-exclamation-triangle',
    acceptProps: { label: t('prometheus.replace'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: () => void generate(),
  })
}

// ---- a token of the owner's own
const own = ref('')
const ownOpen = ref(false)
const ownServerError = ref('')
const ownProblem = computed(() => {
  if (!own.value.trim()) return ''
  const p = tokenProblem(own.value)
  return p ? t('prometheus.invalid.' + p, { min: num(TOKEN_MIN, lang.value), max: num(TOKEN_MAX, lang.value) }) : ''
})
const canSaveOwn = computed(() => own.value.trim() !== '' && !ownProblem.value && !busy.value)
async function saveOwn() {
  if (!canSaveOwn.value) return
  busy.value = true
  error.value = ''
  ownServerError.value = ''
  try {
    state.value = await api<MetricsState>('PUT', '/metrics-token', { token: own.value.trim() })
    own.value = ''
    ownOpen.value = false
    toast.add({ severity: 'success', summary: t('prometheus.saved'), life: 3000 })
  } catch (e) {
    ownServerError.value = fieldError(e).token ?? ''
    fail(e)
  } finally {
    busy.value = false
  }
}

// ---- turn it off
function askOff() {
  confirm.require({
    header: t('prometheus.off_title'),
    message: t('prometheus.off_confirm'),
    icon: 'pi pi-exclamation-triangle',
    acceptProps: { label: t('prometheus.turn_off'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      busy.value = true
      error.value = ''
      try {
        state.value = await api<MetricsState>('DELETE', '/metrics-token')
        toast.add({ severity: 'success', summary: t('prometheus.removed'), life: 3000 })
      } catch (e) {
        fail(e)
      } finally {
        busy.value = false
      }
    },
  })
}
</script>

<template>
  <Card class="settings-card metrics-card">
    <template #title>
      <div class="title-row">
        <h2 id="settings-prometheus-title" class="card-title">{{ t('prometheus.title') }}</h2>
        <Tag v-if="state" :severity="on ? 'success' : 'secondary'" :value="on ? t('prometheus.on') : t('prometheus.off')" class="state" />
      </div>
    </template>
    <template #subtitle>{{ t('prometheus.desc') }}</template>
    <template #content>
      <Message v-if="loadError" severity="error" :closable="false" class="banner">
        <div class="load-error">
          <span>{{ loadError }}</span>
          <Button :label="t('app.retry')" icon="pi pi-refresh" size="small" severity="secondary" outlined @click="load" />
        </div>
      </Message>
      <Skeleton v-else-if="loading" height="6rem" border-radius="var(--app-radius)" />
      <div v-else class="body">
        <p class="app-muted">
          <template v-if="on">{{ t('prometheus.on_help') }}</template>
          <template v-else>{{ t('prometheus.off_help') }}</template>
        </p>
        <p class="address-line">
          <span class="app-muted">{{ t('prometheus.address') }}</span>
          <bdi class="app-mono app-ltr address" dir="ltr">{{ address }}</bdi>
        </p>
        <p v-if="!canChange" class="app-muted owner-only">{{ t('prometheus.owner_only') }}</p>
        <template v-else>
          <div class="actions">
            <Button
              type="button"
              class="generate"
              :label="on ? t('prometheus.make_new') : t('prometheus.make')"
              icon="pi pi-key"
              :loading="busy"
              :severity="on ? 'secondary' : undefined"
              :outlined="on"
              @click="askGenerate"
            />
            <Button type="button" class="own-toggle" :label="t('prometheus.own')" icon="pi pi-pencil" text :aria-expanded="ownOpen" @click="ownOpen = !ownOpen" />
            <Button v-if="on" type="button" class="turn-off" :label="t('prometheus.turn_off')" icon="pi pi-power-off" severity="danger" text :disabled="busy" @click="askOff" />
          </div>
          <form v-if="ownOpen" class="own-form" novalidate @submit.prevent="saveOwn">
            <label class="field">
              <span>{{ t('prometheus.own_label') }}</span>
              <InputText
                v-model="own"
                type="password"
                dir="ltr"
                autocomplete="new-password"
                :spellcheck="false"
                :maxlength="TOKEN_MAX + 20"
                :invalid="!!ownProblem || !!ownServerError"
                aria-describedby="prometheus-own-help"
                class="own-input"
              />
              <small id="prometheus-own-help" class="app-muted">{{ t('prometheus.own_help', { min: num(TOKEN_MIN, lang), max: num(TOKEN_MAX, lang) }) }}</small>
              <small v-if="ownProblem" class="field-error own-problem">{{ ownProblem }}</small>
              <small v-if="ownServerError" class="field-error app-ltr">{{ ownServerError }}</small>
            </label>
            <Button type="submit" class="save-own" :label="t('prometheus.save')" :disabled="!canSaveOwn" :loading="busy" />
          </form>
          <Message v-if="error" severity="error" :closable="false" class="error">
            {{ error }}
            <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
          </Message>
        </template>
        <p class="app-muted docs">{{ t('prometheus.docs') }}</p>
      </div>
      <MetricsTokenDialog v-model:visible="showing" :token="made" />
    </template>
  </Card>
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
.body {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.body p {
  margin: 0;
}
.address-line {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.25rem 0.75rem;
}
.address {
  overflow-wrap: anywhere;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
.own-form {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 0.75rem;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  inline-size: min(100%, 32rem);
  font-weight: 600;
}
.field small {
  font-weight: 400;
}
.field-error {
  color: var(--p-red-500);
}
.load-error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
.docs {
  font-size: 0.85rem;
}
</style>
