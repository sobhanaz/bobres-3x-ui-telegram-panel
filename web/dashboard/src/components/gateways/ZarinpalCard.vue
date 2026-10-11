<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Card from 'primevue/card'
import ToggleSwitch from 'primevue/toggleswitch'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import SecretField from '../SecretField.vue'
import GatewayReport from './GatewayReport.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { dateTime, num, type Lang } from '../../format'
import { blockedByOpen, healthState, openPayments, zarinpalBody, zarinpalDirty, zarinpalFormOf, zarinpalProblems, zarinpalTestBody } from '../../gateways'
import type { GatewayConfig, GatewayTest } from '../../types'

// Zarinpal: offered or not, the sandbox, a write-only merchant id and proxy,
// a test that changes nothing, and the payments of the last 7 and 30 days.
// Turning it off only stops offering it: open payments are still checked.
const props = defineProps<{ config: GatewayConfig }>()
const emit = defineEmits<{ saved: [] }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()

const form = ref(zarinpalFormOf(props.config))
// A new key for the secret fields after a save: they show the stored state again.
const generation = ref(0)
let justSaved = false
// Fresh settings (after any save on the page) keep what staff are typing;
// switches they did not touch follow the new values.
watch(
  () => props.config,
  (next, prev) => {
    if (justSaved) {
      form.value = zarinpalFormOf(next)
      generation.value++
      justSaved = false
      return
    }
    if (form.value.enabled === prev.enabled) form.value.enabled = next.enabled
    if (form.value.sandbox === prev.sandbox) form.value.sandbox = next.sandbox
  },
)

const busy = ref(false)
const error = ref('')
const detail = ref('')
// What core said about a field (its words), by the field's name.
const fieldErrors = ref<Record<string, string>>({})
const testing = ref(false)
const result = ref<{ severity: 'success' | 'warn' | 'error'; text: string; detail: string } | null>(null)
watch(
  () => JSON.stringify(form.value),
  () => {
    fieldErrors.value = {}
    result.value = null
  },
)

const readOnly = computed(() => props.config.key_missing)
const problems = computed(() => zarinpalProblems(form.value, props.config))
const dirty = computed(() => zarinpalDirty(form.value, props.config))
const canSave = computed(() => !readOnly.value && dirty.value && problems.value.length === 0 && !busy.value)
const merchantError = computed(() => fieldErrors.value.merchant_id || (problems.value.includes('merchant_id') ? t('gateway.invalid.merchant') : ''))
const proxyError = computed(() => fieldErrors.value.proxy || (problems.value.includes('proxy') ? t('gateway.invalid.proxy') : ''))
const open = computed(() => props.config.health?.open ?? 0)
// A new merchant id or sandbox is refused while payments are open.
const merchantBlocked = computed(() => blockedByOpen(form.value, props.config))
const health = computed(() => healthState(props.config.health))
// Offered, turned on but not usable (the problem says why), off, or not set up.
const state = computed(() => {
  const c = props.config
  if (!c.configured) return { severity: 'secondary' as const, text: t('gateway.state.not_set') }
  if (!c.enabled) return { severity: 'secondary' as const, text: t('gateway.state.off') }
  if (c.offered === false) return { severity: 'warn' as const, text: t('gateway.state.not_offered') }
  return { severity: 'success' as const, text: t('gateway.state.on') }
})
const problem = computed(() => {
  const p = props.config.problem ?? ''
  if (!p) return ''
  return te('gateway.problem.' + p) ? t('gateway.problem.' + p) : t('gateway.problem.invalid')
})

function reset() {
  form.value = zarinpalFormOf(props.config)
  generation.value++
  error.value = ''
  detail.value = ''
}

async function save() {
  if (!canSave.value) return
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    await api('PUT', `/gateways/${props.config.provider}`, zarinpalBody(form.value))
    // The typed secrets are gone at once; the page reads the stored state again.
    form.value = { ...form.value, merchant: '', proxy: '', clearProxy: false }
    generation.value++
    justSaved = true
    toast.add({ severity: 'success', summary: t('gateway.saved'), life: 3000 })
    emit('saved')
  } catch (e) {
    const n = openPayments(e)
    if (e instanceof ApiError && e.status === 400 && typeof e.data.field === 'string') fieldErrors.value = { [e.data.field]: e.message }
    const err = errorText(e, t)
    error.value = n !== null ? t('gateway.open_payments', { n: num(n, lang.value) }) : err.text
    detail.value = err.detail
  } finally {
    busy.value = false
  }
}

async function test() {
  if (testing.value) return
  testing.value = true
  result.value = null
  try {
    const r = await api<GatewayTest>('POST', `/gateways/${props.config.provider}/test`, readOnly.value ? {} : zarinpalTestBody(form.value))
    if (r.ok) {
      const waiting = r.unverified ? ' ' + t('gateway.test_unverified', { n: num(r.unverified, lang.value) }) : ''
      result.value = { severity: 'success', text: t('gateway.test_ok') + waiting, detail: '' }
    } else {
      result.value = { severity: 'error', text: t('gateway.test_failed'), detail: r.detail }
    }
  } catch (e) {
    const err = errorText(e, t)
    result.value = { severity: 'error', text: err.text, detail: err.detail }
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <Card class="zarinpal-card">
    <template #title>
      <div class="title-row">
        <h2 id="gateway-zarinpal-title" class="card-title">{{ t('payment.provider.zarinpal') }}</h2>
        <Tag :severity="state.severity" :value="state.text" class="state" />
        <Tag v-if="config.sandbox" :value="t('gateway.sandbox_on')" severity="warn" />
      </div>
    </template>
    <template #subtitle>{{ t('gateway.zarinpal_desc') }}</template>
    <template #content>
      <Message v-if="readOnly" severity="warn" icon="pi pi-key" :closable="false" class="note key-missing">{{ t('gateway.key_missing') }}</Message>
      <Message v-if="problem" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="note problem">{{ problem }}</Message>
      <form class="zarinpal-form" aria-labelledby="gateway-zarinpal-title" novalidate @submit.prevent="save">
        <div class="fields">
          <div class="field">
            <div class="bool-row">
              <label for="zarinpal-enabled" class="label">{{ t('gateway.enabled') }}</label>
              <ToggleSwitch
                v-model="form.enabled"
                input-id="zarinpal-enabled"
                :disabled="readOnly"
                :invalid="problems.includes('enabled')"
                :pt="{ input: { 'aria-describedby': 'zarinpal-enabled-help' } }"
                class="enabled"
              />
            </div>
            <small id="zarinpal-enabled-help" class="app-muted help">{{ t('gateway.enabled_help') }}</small>
            <small v-if="problems.includes('enabled')" role="alert" class="field-error">{{ t('gateway.invalid.needs_merchant') }}</small>
          </div>
          <div class="field">
            <div class="bool-row">
              <label for="zarinpal-sandbox" class="label">{{ t('gateway.sandbox') }}</label>
              <ToggleSwitch
                v-model="form.sandbox"
                input-id="zarinpal-sandbox"
                :disabled="readOnly || (config.sandbox_allowed === false && !config.sandbox)"
                :pt="{ input: { 'aria-describedby': 'zarinpal-sandbox-help' } }"
                class="sandbox"
              />
            </div>
            <small id="zarinpal-sandbox-help" class="app-muted help">
              {{ config.sandbox_allowed === false ? t('gateway.sandbox_prod') : t('gateway.sandbox_help') }}
            </small>
            <small v-if="fieldErrors.sandbox" class="app-ltr field-error">{{ fieldErrors.sandbox }}</small>
          </div>
          <SecretField
            :key="'merchant-' + generation"
            id="zarinpal-merchant"
            v-model="form.merchant"
            :label="t('gateway.merchant')"
            :set="config.configured"
            :hint="config.merchant_hint"
            :help="t('gateway.merchant_help')"
            placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
            :maxlength="64"
            :disabled="readOnly"
            :error="merchantError"
          />
          <Message v-if="merchantBlocked" severity="warn" :closable="false" class="merchant-blocked">
            {{ t('gateway.open_payments', { n: num(open, lang) }) }}
          </Message>
          <SecretField
            :key="'proxy-' + generation"
            id="zarinpal-proxy"
            v-model="form.proxy"
            v-model:clear="form.clearProxy"
            :label="t('gateway.proxy')"
            :set="config.proxy_set"
            :help="t('gateway.proxy_help')"
            placeholder="http://user:pass@proxy.example.com:3128"
            clearable
            :maxlength="500"
            :disabled="readOnly"
            :error="proxyError"
          />
        </div>

        <div class="health" aria-live="polite">
          <Tag
            :severity="health === 'ok' ? 'success' : health === 'error' ? 'danger' : 'secondary'"
            :value="t('gateway.health.' + health)"
            class="health-state"
          />
          <span v-if="config.health?.last_paid_at" class="app-muted small">{{ t('gateway.last_paid', { time: dateTime(config.health.last_paid_at, lang) }) }}</span>
          <span v-if="open" class="app-muted small open-count">
            {{ t('gateway.open_count', { n: num(open, lang) }) }}<template v-if="config.health?.oldest_open_at">
              · {{ t('gateway.oldest_open', { time: dateTime(config.health.oldest_open_at, lang) }) }}</template>
          </span>
          <div v-if="config.health?.last_error" class="last-error small">
            <span class="app-muted">{{ config.health.last_error_at ? t('gateway.last_error_at', { time: dateTime(config.health.last_error_at, lang) }) : t('gateway.last_error') }}</span>
            <span class="app-ltr detail error-text">{{ config.health.last_error }}</span>
          </div>
        </div>

        <Message v-if="result" :severity="result.severity" :closable="false" class="test-result" aria-live="polite">
          {{ result.text }}
          <div v-if="result.detail" class="app-ltr detail">{{ result.detail }}</div>
        </Message>
        <Message v-if="error" severity="error" :closable="false" class="error">
          {{ error }}
          <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
        </Message>
        <div class="actions">
          <Button
            type="button"
            :label="t('gateway.test')"
            icon="pi pi-bolt"
            severity="secondary"
            outlined
            :loading="testing"
            :disabled="(!config.configured && !form.merchant.trim()) || problems.includes('merchant_id') || problems.includes('proxy')"
            class="test"
            @click="test"
          />
          <span class="spacer" />
          <template v-if="!readOnly">
            <span v-if="dirty" class="app-muted unsaved" aria-live="polite">{{ t('settings.unsaved') }}</span>
            <Button type="button" :label="t('settings.reset')" icon="pi pi-undo" severity="secondary" text :disabled="!dirty || busy" @click="reset" />
            <Button type="submit" :label="t('common.save')" icon="pi pi-check" :loading="busy" :disabled="!canSave" class="save" />
          </template>
        </div>
      </form>
      <GatewayReport :provider="config.provider" class="report" />
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
.note {
  margin-block-end: 1rem;
}
.fields {
  display: grid;
  gap: 1.15rem;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 18rem), 1fr));
  column-gap: 1.5rem;
  margin-block-end: 1.25rem;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.label {
  font-weight: 600;
}
.bool-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}
.help {
  font-size: 0.82rem;
  line-height: 1.6;
}
.field-error {
  color: var(--p-red-500);
  font-size: 0.85rem;
}
.merchant-blocked {
  grid-column: 1 / -1;
}
.health {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem 0.75rem;
  margin-block-end: 1rem;
}
.last-error {
  flex-basis: 100%;
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.25rem 0.5rem;
}
.last-error .error-text {
  margin: 0;
}
.small {
  font-size: 0.85rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
.test-result,
.error {
  margin-block-end: 1rem;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  padding-block-start: 1rem;
  border-block-start: 1px solid var(--p-content-border-color);
}
.spacer {
  flex: 1 1 auto;
}
.unsaved {
  font-size: 0.85rem;
}
.report {
  margin-block-start: 1.5rem;
}
</style>
