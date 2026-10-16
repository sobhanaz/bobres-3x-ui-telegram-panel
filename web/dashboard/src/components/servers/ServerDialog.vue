<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import ToggleSwitch from 'primevue/toggleswitch'
import Checkbox from 'primevue/checkbox'
import Select from 'primevue/select'
import Button from 'primevue/button'
import Message from 'primevue/message'
import SecretField from '../SecretField.vue'
import { api } from '../../api'
import { errorText, fieldError } from '../../errors'
import { isolate, num, type Lang } from '../../format'
import { useAuth } from '../../stores/auth'
import {
  addressChanged, blankServer, PANEL_KINDS, canTest, needsToken, panelURLProblem, privateNeedsOwner, SERVER_NAME_MAX, serverBody, serverFormOf, serverProblems,
  subURLProblem, testBody, WEIGHT_MAX, WEIGHT_MIN,
} from '../../servers'
import type { ServerItem, ServerTest } from '../../types'

// Add a 3x-ui panel or edit one: its address, a write-only API token, where
// subscription links point, its weight and switches. "Test" tries the typed
// connection without saving; a save tests it too, unless staff skip that.
const props = defineProps<{ server: ServerItem | null }>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ saved: [item: ServerItem | undefined] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const auth = useAuth()
const kindOptions = computed(() => PANEL_KINDS.map((k) => ({ value: k, label: t('server.kind_' + k) })))
const marzban = computed(() => form.value.kind === 'marzban')
const panelName = computed(() => t('server.kind_' + form.value.kind))

const form = ref(blankServer())
// A new key for the token field each time the dialog opens: it starts as "set".
const generation = ref(0)
const busy = ref(false)
const error = ref('')
const detail = ref('')
// What core said about a field (its words), by the field's name.
const fieldErrors = ref<Record<string, string>>({})
// The last Test: it tries the typed address and token (or the stored token)
// and saves nothing.
const testing = ref(false)
const result = ref<{ severity: 'success' | 'warn' | 'error'; text: string; detail: string } | null>(null)

watch(visible, (v) => {
  // A typed token does not outlive the dialog.
  if (!v) {
    form.value.token = ''
    return
  }
  form.value = props.server ? serverFormOf(props.server) : blankServer()
  generation.value++
  error.value = ''
  detail.value = ''
  fieldErrors.value = {}
  result.value = null
})
// A field edited after core refused it may be right now.
watch(
  () => JSON.stringify(form.value),
  () => (fieldErrors.value = {}),
)

const problems = computed(() => serverProblems(form.value, props.server))
const issue = (f: string) => !!fieldErrors.value[f] || (problems.value.includes(f) && touched(f))
// An empty required field is not marked before staff typed in it.
function touched(f: string): boolean {
  const v = { name: form.value.name, base_url: form.value.baseURL, sub_base_url: form.value.subURL, token: form.value.token, weight: form.value.weight }[f]
  return v === undefined || v.trim() !== ''
}
// Only the store owner lets BOBRES reach a private network (core checks it
// again): others cannot mark a panel private, nor give a private one another
// address or token. They may still rename, reweight or switch one off.
const isOwner = computed(() => auth.me?.user.role === 'owner')
const privateLocked = computed(() => !isOwner.value && !form.value.allowPrivate && !props.server?.allow_private)
const ownerNeeded = computed(() => !isOwner.value && privateNeedsOwner(form.value, props.server))
const urlProblem = computed(() => {
  const p = form.value.baseURL.trim() ? panelURLProblem(form.value.baseURL, form.value.allowPrivate) : ''
  if (p === 'https' && privateLocked.value) return t('server.invalid.https_owner')
  return p ? t('server.invalid.' + p) : ''
})
const subProblem = computed(() => (subURLProblem(form.value.subURL) ? t('server.invalid.sub_url') : ''))
const tokenProblem = computed(() =>
  problems.value.includes('token') && form.value.token.trim() ? t(marzban.value ? 'server.invalid.login' : 'server.invalid.token') : '',
)
const weightProblem = computed(() => (problems.value.includes('weight') && form.value.weight.trim() ? t('server.invalid.weight', { min: num(WEIGHT_MIN, lang.value), max: num(WEIGHT_MAX, lang.value) }) : ''))
const moved = computed(() => addressChanged(form.value, props.server) && (props.server?.clients ?? 0) > 0)
// A stored token goes only to the host it was entered for: a new host or
// port needs it typed again.
const tokenAgain = computed(() => !!props.server?.has_token && needsToken(form.value, props.server))
const canSave = computed(() => problems.value.length === 0 && !ownerNeeded.value && !busy.value)
const testable = computed(() => canTest(form.value, props.server) && !ownerNeeded.value)

watch(
  () => [form.value.baseURL, form.value.token, form.value.allowPrivate],
  () => (result.value = null),
)
async function test() {
  if (!testable.value || testing.value) return
  testing.value = true
  result.value = null
  fieldErrors.value = {}
  try {
    const r = await api<ServerTest>('POST', '/servers/test', testBody(form.value, props.server?.id))
    if (r.ok) {
      const text = t('server.test_ok', { version: isolate(panelName.value + ' ' + (r.panel_version || t('app.unknown'))), n: num(r.inbounds, lang.value) }, r.inbounds)
      result.value = r.version_untested
        ? { severity: 'warn', text: text + ' ' + t('server.version_untested'), detail: '' }
        : { severity: 'success', text, detail: '' }
    } else {
      result.value = { severity: 'error', text: t('server.test_failed'), detail: r.detail }
    }
  } catch (e) {
    fieldErrors.value = fieldError(e)
    const err = errorText(e, t)
    result.value = { severity: 'error', text: err.text, detail: err.detail }
  } finally {
    testing.value = false
  }
}

async function save() {
  if (!canSave.value) return
  busy.value = true
  error.value = ''
  detail.value = ''
  fieldErrors.value = {}
  try {
    const body = serverBody(form.value)
    const res = props.server
      ? await api<{ item?: ServerItem }>('PUT', `/servers/${props.server.id}`, body)
      : await api<{ item?: ServerItem }>('POST', '/servers', body)
    visible.value = false
    emit('saved', res?.item)
  } catch (e) {
    fieldErrors.value = fieldError(e)
    const err = errorText(e, t)
    error.value = err.text
    detail.value = err.detail
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    v-model:visible="visible"
    :header="props.server ? t('server.edit') : t('server.add')"
    modal
    :style="{ inlineSize: 'min(40rem, 96vw)' }"
    :draggable="false"
    class="server-dialog"
  >
    <form class="app-form" novalidate @submit.prevent="save">
      <label>
        <span>{{ t('server.kind') }}</span>
        <Select v-model="form.kind" :options="kindOptions" option-label="label" option-value="value" :disabled="!!props.server" aria-describedby="server-kind-help" class="kind" />
        <small id="server-kind-help" class="app-muted">{{ props.server ? t('server.kind_fixed') : t('server.kind_help') }}</small>
      </label>
      <label>
        <span>{{ t('server.name') }}</span>
        <InputText v-model="form.name" :maxlength="SERVER_NAME_MAX" dir="auto" autocomplete="off" :invalid="issue('name')" class="name" />
        <small v-if="fieldErrors.name" class="app-ltr field-error">{{ fieldErrors.name }}</small>
      </label>
      <label>
        <span>{{ t('server.base_url') }}</span>
        <InputText
          v-model="form.baseURL"
          dir="ltr"
          inputmode="url"
          autocomplete="off"
          :spellcheck="false"
          maxlength="500"
          placeholder="https://panel.example.com:2053/path"
          :invalid="issue('base_url')"
          aria-describedby="server-base-url-help"
          class="base-url"
        />
        <small id="server-base-url-help" class="app-muted">{{ t('server.base_url_help') }}</small>
        <small v-if="urlProblem" class="field-error url-problem">{{ urlProblem }}</small>
        <small v-if="fieldErrors.base_url" class="app-ltr field-error">{{ fieldErrors.base_url }}</small>
      </label>
      <Message v-if="moved" severity="warn" :closable="false" class="moved">
        {{ t('server.address_changed', { n: num(props.server?.clients ?? 0, lang) }, props.server?.clients ?? 0) }}
      </Message>
      <div class="app-field">
        <SecretField
          :key="generation"
          id="server-token"
          v-model="form.token"
          :label="marzban ? t('server.login') : t('server.token')"
          :set="!!props.server?.has_token"
          :force="tokenAgain"
          :help="tokenAgain ? t('server.token_again') : marzban ? t('server.login_help') : t('server.token_help')"
          :error="tokenProblem || fieldErrors.token || ''"
        />
      </div>
      <label>
        <span>{{ t('server.sub_url') }}</span>
        <InputText
          v-model="form.subURL"
          dir="ltr"
          inputmode="url"
          autocomplete="off"
          :spellcheck="false"
          maxlength="500"
          placeholder="https://sub.example.com/sub/"
          :invalid="issue('sub_base_url')"
          aria-describedby="server-sub-url-help"
          class="sub-url"
        />
        <small id="server-sub-url-help" class="app-muted">{{ t('server.sub_url_help') }}</small>
        <small v-if="subProblem" class="field-error">{{ subProblem }}</small>
        <small v-if="fieldErrors.sub_base_url" class="app-ltr field-error">{{ fieldErrors.sub_base_url }}</small>
      </label>
      <label>
        <span>{{ t('server.weight') }}</span>
        <InputText v-model="form.weight" inputmode="numeric" dir="ltr" maxlength="4" :invalid="issue('weight')" aria-describedby="server-weight-help" class="weight" />
        <small id="server-weight-help" class="app-muted">{{ t('server.weight_help') }}</small>
        <small v-if="weightProblem" class="field-error">{{ weightProblem }}</small>
        <small v-if="fieldErrors.weight" class="app-ltr field-error">{{ fieldErrors.weight }}</small>
      </label>
      <div class="app-field">
        <label class="app-check">
          <ToggleSwitch v-model="form.enabled" input-id="server-enabled" aria-describedby="server-enabled-help" class="enabled" />
          <span>{{ t('server.enabled') }}</span>
        </label>
        <small id="server-enabled-help" class="app-muted">{{ t('server.enabled_help') }}</small>
      </div>
      <div class="app-field">
        <label class="app-check">
          <ToggleSwitch
            v-model="form.allowPrivate"
            input-id="server-private"
            :disabled="privateLocked"
            :invalid="ownerNeeded || !!fieldErrors.allow_private"
            class="allow-private"
          />
          <span>{{ t('server.allow_private') }}</span>
        </label>
        <Message v-if="form.allowPrivate" severity="warn" :closable="false" class="private-warning">{{ t('server.allow_private_warning') }}</Message>
        <small v-else class="app-muted">{{ t('server.allow_private_help') }}</small>
        <small v-if="privateLocked" class="app-muted owner-only">{{ t('server.private_owner_only') }}</small>
        <small v-if="ownerNeeded" role="alert" class="field-error owner-needed">{{ t('server.private_owner_change') }}</small>
        <small v-if="fieldErrors.allow_private" class="app-ltr field-error private-error">{{ fieldErrors.allow_private }}</small>
      </div>

      <div class="test-row">
        <Button
          type="button"
          :label="t('server.test')"
          icon="pi pi-bolt"
          severity="secondary"
          outlined
          :loading="testing"
          :disabled="!testable"
          class="test"
          @click="test"
        />
        <span class="app-muted small">{{ t('server.test_hint') }}</span>
      </div>
      <Message v-if="result" :severity="result.severity" :closable="false" class="test-result" aria-live="polite">
        {{ result.text }}
        <div v-if="result.detail" class="app-ltr detail">{{ result.detail }}</div>
      </Message>

      <div class="app-field">
        <label class="app-check">
          <Checkbox v-model="form.skipCheck" binary input-id="server-skip-check" class="skip-check" />
          <span>{{ t('server.skip_check') }}</span>
        </label>
        <small class="app-muted">{{ t('server.skip_check_help') }}</small>
      </div>

      <Message v-if="error" severity="error" :closable="false" class="error">
        {{ error }}
        <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
      </Message>
      <div class="buttons">
        <Button type="button" :label="t('app.cancel')" severity="secondary" outlined @click="visible = false" />
        <Button type="submit" :label="t('common.save')" :loading="busy" :disabled="!canSave" class="save" />
      </div>
    </form>
  </Dialog>
</template>

<style scoped>
.test-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem 0.75rem;
}
.small {
  font-size: 0.82rem;
}
.buttons {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
.field-error {
  color: var(--p-red-500);
}
</style>
