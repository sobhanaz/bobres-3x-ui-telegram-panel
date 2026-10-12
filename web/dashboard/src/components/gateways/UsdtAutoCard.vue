<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Card from 'primevue/card'
import ToggleSwitch from 'primevue/toggleswitch'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import SecretField from '../SecretField.vue'
import GatewayHealthRow from './GatewayHealthRow.vue'
import GatewayReport from './GatewayReport.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { num, type Lang } from '../../format'
import { apiKeyMax, openPayments, usdtAddressError, usdtBlockedByOpen, usdtBody, usdtDirty, usdtFormOf, usdtProblems, usdtTestBody } from '../../gateways'
import type { GatewayConfig, GatewayTest } from '../../types'

// Automatic USDT (TRC20): offered or not, the TRON address customers pay to
// (it cannot change while automatic payments are open), a write-only
// TronGrid API key and proxy (where TronGrid cannot be reached directly), a
// test that changes nothing, and the payments of the last 7 and 30 days with
// the deposits that matched no payment.
const props = defineProps<{ config: GatewayConfig }>()
const emit = defineEmits<{ saved: [] }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()

const form = ref(usdtFormOf(props.config))
// A new key for the secret fields after a save: they show the stored state again.
const generation = ref(0)
let justSaved = false
// Fresh settings (after any save on the page) keep what staff are typing;
// what they did not touch follows the new values.
watch(
  () => props.config,
  (next, prev) => {
    if (justSaved) {
      form.value = usdtFormOf(next)
      generation.value++
      justSaved = false
      return
    }
    if (form.value.enabled === prev.enabled) form.value.enabled = next.enabled
    if (form.value.address === (prev.address ?? '')) form.value.address = next.address ?? ''
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
const problems = computed(() => usdtProblems(form.value, props.config))
const dirty = computed(() => usdtDirty(form.value, props.config))
const canSave = computed(() => !readOnly.value && dirty.value && problems.value.length === 0 && !busy.value)
const addressError = computed(() => {
  if (fieldErrors.value.address) return fieldErrors.value.address
  const e = usdtAddressError(form.value, props.config)
  return e ? t('gateway.usdt.invalid.address_' + e) : ''
})
const apiKeyError = computed(() => fieldErrors.value.api_key || (problems.value.includes('api_key') ? t('gateway.usdt.invalid.api_key') : ''))
const proxyError = computed(() => fieldErrors.value.proxy || (problems.value.includes('proxy') ? t('gateway.invalid.proxy') : ''))
const open = computed(() => props.config.health?.open ?? 0)
// A new address is refused while automatic payments are open (said once
// the address itself is right).
const addressBlocked = computed(() => !addressError.value && usdtBlockedByOpen(form.value, props.config))
const addressDescribedBy = computed(() => ['usdt-address-help', addressError.value && 'usdt-address-error'].filter(Boolean).join(' '))
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
  return te('gateway.usdt.problem.' + p) ? t('gateway.usdt.problem.' + p) : t('gateway.usdt.problem.invalid')
})

function reset() {
  form.value = usdtFormOf(props.config)
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
    await api('PUT', `/gateways/${props.config.provider}`, usdtBody(form.value))
    // The typed secrets are gone at once; the page reads the stored state again.
    form.value = { ...form.value, apiKey: '', clearApiKey: false, proxy: '', clearProxy: false }
    generation.value++
    justSaved = true
    toast.add({ severity: 'success', summary: t('gateway.usdt.saved'), life: 3000 })
    emit('saved')
  } catch (e) {
    const n = openPayments(e)
    if (e instanceof ApiError && e.status === 400 && typeof e.data.field === 'string') fieldErrors.value = { [e.data.field]: e.message }
    const err = errorText(e, t)
    error.value = n !== null ? t('gateway.usdt.open_payments', { n: num(n, lang.value) }) : err.text
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
    const r = await api<GatewayTest>('POST', `/gateways/${props.config.provider}/test`, readOnly.value ? {} : usdtTestBody(form.value))
    result.value = r.ok
      ? { severity: 'success', text: t('gateway.usdt.test_ok'), detail: '' }
      : { severity: 'error', text: t('gateway.usdt.test_failed'), detail: r.detail }
  } catch (e) {
    const err = errorText(e, t)
    result.value = { severity: 'error', text: err.text, detail: err.detail }
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <Card class="usdt-card">
    <template #title>
      <div class="title-row">
        <h2 id="gateway-usdt-title" class="card-title">{{ t('payment.provider.usdt_trc20_auto') }}</h2>
        <Tag :severity="state.severity" :value="state.text" class="state" />
      </div>
    </template>
    <template #subtitle>{{ t('gateway.usdt.desc') }}</template>
    <template #content>
      <Message v-if="readOnly" severity="warn" icon="pi pi-key" :closable="false" class="note key-missing">{{ t('gateway.key_missing') }}</Message>
      <Message v-if="problem" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="note problem">{{ problem }}</Message>
      <form class="usdt-form" aria-labelledby="gateway-usdt-title" novalidate @submit.prevent="save">
        <div class="fields">
          <div class="field">
            <div class="bool-row">
              <label for="usdt-enabled" class="label">{{ t('gateway.usdt.enabled') }}</label>
              <ToggleSwitch
                v-model="form.enabled"
                input-id="usdt-enabled"
                :disabled="readOnly"
                :invalid="problems.includes('enabled')"
                :pt="{ input: { 'aria-describedby': 'usdt-enabled-help' } }"
                class="enabled"
              />
            </div>
            <small id="usdt-enabled-help" class="app-muted help">{{ t('gateway.usdt.enabled_help') }}</small>
            <small v-if="problems.includes('enabled')" role="alert" class="field-error">{{ t('gateway.usdt.invalid.needs_address') }}</small>
          </div>
          <div class="field">
            <label for="usdt-address" class="label">{{ t('gateway.usdt.address') }}</label>
            <InputText
              id="usdt-address"
              v-model="form.address"
              dir="ltr"
              autocomplete="off"
              :spellcheck="false"
              placeholder="T…"
              :maxlength="64"
              :disabled="readOnly"
              :invalid="!!addressError"
              :aria-describedby="addressDescribedBy"
              class="address"
            />
            <small id="usdt-address-help" class="app-muted help">{{ t('gateway.usdt.address_help') }}</small>
            <small v-if="addressError" id="usdt-address-error" role="alert" class="field-error">{{ addressError }}</small>
          </div>
          <Message v-if="addressBlocked" severity="warn" :closable="false" class="address-blocked">
            {{ t('gateway.usdt.open_payments', { n: num(open, lang) }) }}
          </Message>
          <SecretField
            :key="'apikey-' + generation"
            id="usdt-api-key"
            v-model="form.apiKey"
            v-model:clear="form.clearApiKey"
            :label="t('gateway.usdt.api_key')"
            :set="!!config.key_set"
            :help="t('gateway.usdt.api_key_help')"
            placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
            clearable
            :maxlength="apiKeyMax"
            :disabled="readOnly"
            :error="apiKeyError"
          />
          <SecretField
            :key="'proxy-' + generation"
            id="usdt-proxy"
            v-model="form.proxy"
            v-model:clear="form.clearProxy"
            :label="t('gateway.proxy')"
            :set="config.proxy_set"
            :help="t('gateway.usdt.proxy_help')"
            placeholder="http://user:pass@proxy.example.com:3128"
            clearable
            :maxlength="500"
            :disabled="readOnly"
            :error="proxyError"
          />
        </div>

        <GatewayHealthRow :health="config.health" />

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
            :disabled="!form.address.trim() || problems.includes('address') || problems.includes('api_key') || problems.includes('proxy')"
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
      <GatewayReport :provider="config.provider" unmatched class="report" />
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
  min-inline-size: 0;
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
.address {
  inline-size: 100%;
  font-family: var(--app-font-en);
}
.help {
  font-size: 0.82rem;
  line-height: 1.6;
}
.field-error {
  color: var(--p-red-500);
  font-size: 0.85rem;
}
.address-blocked {
  grid-column: 1 / -1;
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
