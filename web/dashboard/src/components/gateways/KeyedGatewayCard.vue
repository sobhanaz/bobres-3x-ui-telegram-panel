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
import GatewayHealthRow from './GatewayHealthRow.vue'
import GatewayReport from './GatewayReport.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { num, type Lang } from '../../format'
import {
  keyedBlockedByOpen,
  keyedBody,
  keyedDirty,
  keyedFormOf,
  keyedHasSandbox,
  keyedProblems,
  keyedTestBody,
  keyedUsesPin,
  openPayments,
  pinFieldMax,
} from '../../gateways'
import type { GatewayConfig, GatewayTest } from '../../types'

// IDPay, NextPay and Aqayepardakht: offered or not, the sandbox (IDPay only), a
// write-only API key (Aqayepardakht: its PIN) and proxy, a test that changes
// nothing, and the payments of the last 7 and 30 days. Turning it off only stops
// offering it: open payments are still checked. Aqayepardakht has no sandbox
// switch: its test system is the PIN "sandbox", so the PIN field says that, and
// refuses that PIN where production does.
const props = defineProps<{ config: GatewayConfig }>()
const emit = defineEmits<{ saved: [] }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()

const form = ref(keyedFormOf(props.config))
// A new key for the secret fields after a save: they show the stored state again.
const generation = ref(0)
let justSaved = false
// Fresh settings (after any save on the page) keep what staff are typing;
// switches they did not touch follow the new values.
watch(
  () => props.config,
  (next, prev) => {
    if (justSaved) {
      form.value = keyedFormOf(next)
      generation.value++
      justSaved = false
      return
    }
    if (form.value.enabled === prev.enabled) form.value.enabled = next.enabled
    // Aqayepardakht may report the sandbox (its PIN is "sandbox") without having a switch for it
    if (keyedHasSandbox(next.provider) && form.value.sandbox === prev.sandbox) form.value.sandbox = next.sandbox
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
const problems = computed(() => keyedProblems(form.value, props.config))
const dirty = computed(() => keyedDirty(form.value, props.config))
const canSave = computed(() => !readOnly.value && dirty.value && problems.value.length === 0 && !busy.value)
// Aqayepardakht has a PIN where the others have an API key.
const pin = computed(() => keyedUsesPin(props.config.provider))
// With no sandbox field to show them, what is said about the sandbox (by core, or
// because production refuses the PIN "sandbox") goes under the PIN.
const apiKeyError = computed(() => {
  const told = fieldErrors.value.api_key || (pin.value ? fieldErrors.value.sandbox : '')
  if (told) return told
  if (problems.value.includes('api_key')) return pin.value ? t('gateway.keyed.pin.invalid.pin') : t('gateway.keyed.invalid.api_key')
  return pin.value && problems.value.includes('sandbox') ? t('gateway.keyed.pin.invalid.sandbox') : ''
})
const proxyError = computed(() => fieldErrors.value.proxy || (problems.value.includes('proxy') ? t('gateway.invalid.proxy') : ''))
const open = computed(() => props.config.health?.open ?? 0)
// A new API key (or PIN) or sandbox is refused while payments are open.
const keyBlocked = computed(() => keyedBlockedByOpen(form.value, props.config))
const name = computed(() => t('payment.provider.' + props.config.provider))
const hasSandbox = computed(() => keyedHasSandbox(props.config.provider))
// The test system of a PIN gateway is the PIN "sandbox": said under the PIN, and
// production refuses it.
const secretHelp = computed(() => {
  if (!pin.value) return t('gateway.keyed.api_key_help', { name: name.value })
  const test = props.config.sandbox_allowed === false ? t('gateway.keyed.pin.sandbox_prod') : t('gateway.keyed.pin.sandbox_help', { name: name.value })
  return t('gateway.keyed.pin.help', { name: name.value }) + ' ' + test
})
// "N payments are still open", said before saving and again if payments refuses.
const openText = (n: number) =>
  pin.value
    ? t('gateway.keyed.pin.open_payments', { n: num(n, lang.value), name: name.value }, n)
    : t('gateway.keyed.open_payments', { n: num(n, lang.value), name: name.value }, n)
// Offered, turned on but not usable (the problem says why), off, or not set up.
const state = computed(() => {
  const c = props.config
  if (!c.configured) return { severity: 'secondary' as const, text: t('gateway.state.not_set') }
  if (!c.enabled) return { severity: 'secondary' as const, text: t('gateway.state.off') }
  if (c.offered === false) return { severity: 'warn' as const, text: t('gateway.state.not_offered') }
  return { severity: 'success' as const, text: t('gateway.state.on') }
})
// Why the stored settings cannot be used. A PIN gateway has its own words where
// the API key's would say "API key"; any problem core names that is not known
// here reads as invalid.
const problem = computed(() => {
  const p = props.config.problem ?? ''
  if (!p) return ''
  const textKey = (k: string) => (pin.value && te('gateway.keyed.pin.problem.' + k) ? 'gateway.keyed.pin.problem.' : 'gateway.keyed.problem.') + k
  return t(te(textKey(p)) ? textKey(p) : textKey('invalid'), { name: name.value })
})

function reset() {
  form.value = keyedFormOf(props.config)
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
    await api('PUT', `/gateways/${props.config.provider}`, keyedBody(form.value, props.config.provider))
    // The typed secrets are gone at once; the page reads the stored state again.
    form.value = { ...form.value, apiKey: '', proxy: '', clearProxy: false }
    generation.value++
    justSaved = true
    toast.add({ severity: 'success', summary: t('gateway.saved'), life: 3000 })
    emit('saved')
  } catch (e) {
    const n = openPayments(e)
    if (e instanceof ApiError && e.status === 400 && typeof e.data.field === 'string') fieldErrors.value = { [e.data.field]: e.message }
    const err = errorText(e, t)
    error.value = n !== null ? openText(n) : err.text
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
    const r = await api<GatewayTest>('POST', `/gateways/${props.config.provider}/test`, readOnly.value ? {} : keyedTestBody(form.value, props.config.provider))
    if (r.ok) {
      const waiting = r.unverified ? ' ' + t('gateway.test_unverified', { n: num(r.unverified, lang.value) }, r.unverified) : ''
      const ok = pin.value ? t('gateway.keyed.pin.test_ok', { name: name.value }) : t('gateway.keyed.test_ok', { name: name.value })
      result.value = { severity: 'success', text: ok + waiting, detail: '' }
    } else {
      result.value = { severity: 'error', text: t('gateway.keyed.test_failed', { name: name.value }), detail: r.detail }
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
  <Card :class="['keyed-card', 'keyed-' + config.provider]">
    <template #title>
      <div class="title-row">
        <h2 :id="'gateway-' + config.provider + '-title'" class="card-title">{{ name }}</h2>
        <Tag :severity="state.severity" :value="state.text" class="state" />
        <Tag v-if="config.sandbox" :value="t('gateway.sandbox_on')" severity="warn" />
      </div>
    </template>
    <template #subtitle>{{ t('gateway.' + config.provider + '_desc') }}</template>
    <template #content>
      <Message v-if="readOnly" severity="warn" icon="pi pi-key" :closable="false" class="note key-missing">{{ t('gateway.key_missing') }}</Message>
      <Message v-if="problem" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="note problem">{{ problem }}</Message>
      <form class="keyed-form" :aria-labelledby="'gateway-' + config.provider + '-title'" novalidate @submit.prevent="save">
        <div class="fields">
          <div class="field">
            <div class="bool-row">
              <label :for="config.provider + '-enabled'" class="label">{{ t('gateway.keyed.enabled', { name }) }}</label>
              <ToggleSwitch
                v-model="form.enabled"
                :input-id="config.provider + '-enabled'"
                :disabled="readOnly"
                :invalid="problems.includes('enabled')"
                :pt="{ input: { 'aria-describedby': config.provider + '-enabled-help' } }"
                class="enabled"
              />
            </div>
            <small :id="config.provider + '-enabled-help'" class="app-muted help">{{ t('gateway.enabled_help') }}</small>
            <small v-if="problems.includes('enabled')" role="alert" class="field-error">
              {{ pin ? t('gateway.keyed.pin.invalid.needs_pin', { name }) : t('gateway.keyed.invalid.needs_key', { name }) }}
            </small>
          </div>
          <div v-if="hasSandbox" class="field">
            <div class="bool-row">
              <label :for="config.provider + '-sandbox'" class="label">{{ t('gateway.sandbox') }}</label>
              <ToggleSwitch
                v-model="form.sandbox"
                :input-id="config.provider + '-sandbox'"
                :disabled="readOnly || (config.sandbox_allowed === false && !config.sandbox)"
                :pt="{ input: { 'aria-describedby': config.provider + '-sandbox-help' } }"
                class="sandbox"
              />
            </div>
            <small :id="config.provider + '-sandbox-help'" class="app-muted help">
              {{ config.sandbox_allowed === false ? t('gateway.sandbox_prod') : t('gateway.keyed.sandbox_help', { name }) }}
            </small>
            <small v-if="fieldErrors.sandbox" class="app-ltr field-error">{{ fieldErrors.sandbox }}</small>
          </div>
          <SecretField
            :key="'key-' + generation"
            :id="config.provider + '-api-key'"
            v-model="form.apiKey"
            :label="pin ? t('gateway.keyed.pin.label') : t('gateway.keyed.api_key')"
            :set="config.configured"
            :help="secretHelp"
            :placeholder="pin ? 'xxxxxxxxxxxxxxxxxxxx' : 'xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx'"
            :maxlength="pin ? pinFieldMax : 64"
            :disabled="readOnly"
            :error="apiKeyError"
          />
          <Message v-if="keyBlocked" severity="warn" :closable="false" class="key-blocked">
            {{ openText(open) }}
          </Message>
          <SecretField
            :key="'proxy-' + generation"
:id="config.provider + '-proxy'"
            v-model="form.proxy"
            v-model:clear="form.clearProxy"
            :label="t('gateway.proxy')"
            :set="config.proxy_set"
            :help="t('gateway.keyed.proxy_help', { name })"
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
            :disabled="
              (!config.configured && !form.apiKey.trim()) || problems.includes('api_key') || problems.includes('proxy') || (pin && problems.includes('sandbox'))
            "
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
.key-blocked {
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
