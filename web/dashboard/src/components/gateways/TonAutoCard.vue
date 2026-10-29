<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
import { ago, dateTime, isolate, num, type Lang } from '../../format'
import { apiKeyMax, openPayments, tonAddressError, tonBlockedByOpen, tonBody, tonDirty, tonFormOf, tonPriceState, tonProblems, tonTestBody } from '../../gateways'
import { canonicalTonAddress, cleanTonAddress } from '../../ton'
import type { GatewayConfig, GatewayTest } from '../../types'

// Automatic TON: offered or not, the TON address customers pay to (any form a
// wallet shows; it is saved in the UQ… form, and cannot change while automatic
// payments are open), a write-only TON Center API key and proxy, the live TON
// price (without a fresh one TON is not offered), a test that changes nothing,
// and the payments of the last 7 and 30 days with the deposits that matched no
// payment.
const props = defineProps<{ config: GatewayConfig }>()
// saved: settings changed; refresh: read the settings again (the price moves on its own)
const emit = defineEmits<{ saved: []; refresh: [] }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()

const form = ref(tonFormOf(props.config))
// What "how old" is measured against.
const now = ref(Date.now())
// A new key for the secret fields after a save: they show the stored state again.
const generation = ref(0)
let justSaved = false
// Fresh settings (after any save on the page) keep what staff are typing;
// what they did not touch follows the new values.
watch(
  () => props.config,
  (next, prev) => {
    now.value = Date.now()
    if (justSaved) {
      form.value = tonFormOf(next)
      generation.value++
      justSaved = false
      return
    }
    if (form.value.enabled === prev.enabled) form.value.enabled = next.enabled
    if (form.value.address === (prev.address ?? '')) form.value.address = next.address ?? ''
  },
)

// The price's age moves on its own; the settings are read again every minute
// so the price, and whether TON is offered, stay current.
let tick: ReturnType<typeof setInterval> | undefined
let poll: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  tick = setInterval(() => (now.value = Date.now()), 15_000)
  poll = setInterval(() => emit('refresh'), 60_000)
})
onBeforeUnmount(() => {
  clearInterval(tick)
  clearInterval(poll)
})

const busy = ref(false)
const error = ref('')
const detail = ref('')
// What core said about a field (its words), by the field's name.
const fieldErrors = ref<Record<string, string>>({})
const testing = ref(false)
const result = ref<{ severity: 'success' | 'warn' | 'error'; text: string; detail: string; notes: string[] } | null>(null)
watch(
  () => JSON.stringify(form.value),
  () => {
    fieldErrors.value = {}
    result.value = null
  },
)

const readOnly = computed(() => props.config.key_missing)
const problems = computed(() => tonProblems(form.value, props.config))
const dirty = computed(() => tonDirty(form.value, props.config))
const canSave = computed(() => !readOnly.value && dirty.value && problems.value.length === 0 && !busy.value)
const addressError = computed(() => {
  if (fieldErrors.value.address) return fieldErrors.value.address
  const e = tonAddressError(form.value, props.config)
  return e ? t('gateway.ton.invalid.address_' + e) : ''
})
const apiKeyError = computed(() => fieldErrors.value.api_key || (problems.value.includes('api_key') ? t('gateway.ton.invalid.api_key') : ''))
const proxyError = computed(() => fieldErrors.value.proxy || (problems.value.includes('proxy') ? t('gateway.invalid.proxy') : ''))
const open = computed(() => props.config.health?.open ?? 0)
// A new address is refused while automatic payments are open (said once
// the address itself is right).
const addressBlocked = computed(() => !addressError.value && tonBlockedByOpen(form.value, props.config))
// A right address that is written another way than the one saved: shown back in that one.
const savedAs = computed(() => {
  const typed = cleanTonAddress(form.value.address)
  const canonical = canonicalTonAddress(typed)
  return canonical && canonical !== typed ? canonical : ''
})
const addressDescribedBy = computed(() => ['ton-address-help', savedAs.value && 'ton-address-saved-as', addressError.value && 'ton-address-error'].filter(Boolean).join(' '))
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
  return te('gateway.ton.problem.' + p) ? t('gateway.ton.problem.' + p) : t('gateway.ton.problem.invalid')
})

// The live price: without a fresh one customers are not offered TON.
const price = computed(() => props.config.price ?? null)
const priceState = computed(() => tonPriceState(props.config))
const priceTag = computed(() => ({ severity: ({ fresh: 'success', stale: 'warn', none: 'secondary' } as const)[priceState.value], text: t('gateway.ton.price.' + priceState.value) }))
const priceWarning = computed(() => (priceState.value === 'fresh' ? '' : t(priceState.value === 'stale' ? 'gateway.ton.price.warn_stale' : 'gateway.ton.price.warn_none')))
const priceSource = computed(() => (price.value ? (te('gateway.ton.source.' + price.value.source) ? t('gateway.ton.source.' + price.value.source) : price.value.source) : ''))

function reset() {
  form.value = tonFormOf(props.config)
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
    await api('PUT', `/gateways/${props.config.provider}`, tonBody(form.value))
    // The typed secrets are gone at once; the page reads the stored state again.
    form.value = { ...form.value, apiKey: '', clearApiKey: false, proxy: '', clearProxy: false }
    generation.value++
    justSaved = true
    toast.add({ severity: 'success', summary: t('gateway.ton.saved'), life: 3000 })
    emit('saved')
  } catch (e) {
    const n = openPayments(e)
    if (e instanceof ApiError && e.status === 400 && typeof e.data.field === 'string') fieldErrors.value = { [e.data.field]: e.message }
    const err = errorText(e, t)
    error.value = n !== null ? t('gateway.ton.open_payments', { n: num(n, lang.value) }, n) : err.text
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
    const r = await api<GatewayTest>('POST', `/gateways/${props.config.provider}/test`, readOnly.value ? {} : tonTestBody(form.value))
    if (r.ok) {
      // The latest transaction shows the address is really watched; a new wallet has none yet.
      const notes: string[] = []
      if (typeof r.latest_tx_at === 'number' && r.latest_tx_at > 0) notes.push(t('gateway.ton.test_latest', { time: isolate(dateTime(r.latest_tx_at, lang.value)) }))
      else if (r.latest_tx_at === null) notes.push(t('gateway.ton.test_no_tx'))
      if (typeof r.latency_ms === 'number') notes.push(t('gateway.ton.test_latency', { ms: num(r.latency_ms, lang.value) }))
      result.value = { severity: 'success', text: t('gateway.ton.test_ok'), detail: '', notes }
    } else {
      result.value = { severity: 'error', text: t('gateway.ton.test_failed'), detail: r.detail, notes: [] }
    }
  } catch (e) {
    const err = errorText(e, t)
    result.value = { severity: 'error', text: err.text, detail: err.detail, notes: [] }
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <Card class="ton-card">
    <template #title>
      <div class="title-row">
        <h2 id="gateway-ton-title" class="card-title">{{ t('gateway.ton.title') }}</h2>
        <Tag :severity="state.severity" :value="state.text" class="state" />
      </div>
    </template>
    <template #subtitle>{{ t('gateway.ton.desc') }}</template>
    <template #content>
      <Message v-if="readOnly" severity="warn" icon="pi pi-key" :closable="false" class="note key-missing">{{ t('gateway.key_missing') }}</Message>
      <Message v-if="problem" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="note problem">{{ problem }}</Message>

      <section class="price" aria-labelledby="ton-price-title">
        <div class="price-head">
          <h3 id="ton-price-title" class="price-title">{{ t('gateway.ton.price.title') }}</h3>
          <Tag :severity="priceTag.severity" :value="priceTag.text" class="price-state" />
        </div>
        <dl v-if="price" class="app-facts price-facts">
          <dt>{{ t('gateway.ton.price.label') }}</dt>
          <dd><span class="app-ltr app-mono price-value">{{ price.usdt_per_ton }} USDT</span></dd>
          <dt>{{ t('gateway.ton.price.source') }}</dt>
          <dd class="price-source">{{ priceSource }}</dd>
          <dt>{{ t('gateway.ton.price.updated') }}</dt>
          <dd class="price-age" :title="dateTime(price.at, lang)">{{ ago(price.at, now, lang) }}</dd>
        </dl>
        <Message v-if="priceWarning" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="price-warning">{{ priceWarning }}</Message>
      </section>

      <form class="ton-form" aria-labelledby="gateway-ton-title" novalidate @submit.prevent="save">
        <div class="fields">
          <div class="field">
            <div class="bool-row">
              <label for="ton-enabled" class="label">{{ t('gateway.ton.enabled') }}</label>
              <ToggleSwitch
                v-model="form.enabled"
                input-id="ton-enabled"
                :disabled="readOnly"
                :invalid="problems.includes('enabled')"
                :pt="{ input: { 'aria-describedby': 'ton-enabled-help' } }"
                class="enabled"
              />
            </div>
            <small id="ton-enabled-help" class="app-muted help">{{ t('gateway.ton.enabled_help') }}</small>
            <small v-if="problems.includes('enabled')" role="alert" class="field-error">{{ t('gateway.ton.invalid.needs_address') }}</small>
          </div>
          <div class="field">
            <label for="ton-address" class="label">{{ t('gateway.ton.address') }}</label>
            <InputText
              id="ton-address"
              v-model="form.address"
              dir="ltr"
              autocomplete="off"
              :spellcheck="false"
              placeholder="UQ…"
              :maxlength="128"
              :disabled="readOnly"
              :invalid="!!addressError"
              :aria-describedby="addressDescribedBy"
              class="address"
            />
            <small id="ton-address-help" class="app-muted help">{{ t('gateway.ton.address_help') }}</small>
            <small v-if="savedAs" id="ton-address-saved-as" class="help saved-as">{{ t('gateway.ton.address_saved_as', { address: isolate(savedAs) }) }}</small>
            <small v-if="addressError" id="ton-address-error" role="alert" class="field-error">{{ addressError }}</small>
          </div>
          <Message v-if="addressBlocked" severity="warn" :closable="false" class="address-blocked">
            {{ t('gateway.ton.open_payments', { n: num(open, lang) }, open) }}
          </Message>
          <SecretField
            :key="'apikey-' + generation"
            id="ton-api-key"
            v-model="form.apiKey"
            v-model:clear="form.clearApiKey"
            :label="t('gateway.ton.api_key')"
            :set="!!config.key_set"
            :help="t('gateway.ton.api_key_help')"
            placeholder="xxxxxxxxxxxxxxxxxxxxxxxx"
            clearable
            :maxlength="apiKeyMax"
            :disabled="readOnly"
            :error="apiKeyError"
          />
          <SecretField
            :key="'proxy-' + generation"
            id="ton-proxy"
            v-model="form.proxy"
            v-model:clear="form.clearProxy"
            :label="t('gateway.proxy')"
            :set="config.proxy_set"
            :help="t('gateway.ton.proxy_help')"
            placeholder="http://user:pass@proxy.example.com:3128"
            clearable
            :maxlength="500"
            :disabled="readOnly"
            :error="proxyError"
          />
        </div>

        <GatewayHealthRow :health="config.health" scan />

        <Message v-if="result" :severity="result.severity" :closable="false" class="test-result" aria-live="polite">
          {{ result.text }}
          <div v-for="(n, i) in result.notes" :key="i" class="test-note">{{ n }}</div>
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
      <GatewayReport :provider="config.provider" unmatched scan class="report" />
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
.price {
  margin-block-end: 1.25rem;
  padding: 0.85rem 1rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--app-radius);
}
.price-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  margin-block-end: 0.6rem;
}
.price-title {
  margin: 0;
  font-size: 1rem;
  font-weight: 700;
}
.price-value {
  font-weight: 600;
}
.price-warning {
  margin-block-start: 0.75rem;
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
.saved-as {
  overflow-wrap: anywhere;
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
.test-note {
  font-size: 0.9rem;
  margin-block-start: 0.25rem;
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
