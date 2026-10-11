<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import Textarea from 'primevue/textarea'
import ReasonDialog from '../ReasonDialog.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { date, type Lang } from '../../format'
import { canSave, licenseSeverity, reasonKey, stateOf, TOKEN_FILE_MAX, tokenOf, tokenProblem } from '../../license'
import type { LicenseInfo } from '../../types'

// The owner pastes a token (or opens the file it came in), checks it without
// saving, then saves it with a reason for the audit log. The token is never
// shown again: core keeps it and answers with what it says.
const props = defineProps<{ replacing: boolean }>()
const emit = defineEmits<{ saved: [info: LicenseInfo] }>()
const { t, te, locale } = useI18n()
const toast = useToast()
const lang = computed(() => locale.value as Lang)

const raw = ref('')
const token = computed(() => tokenOf(raw.value))
const problem = computed(() => tokenProblem(raw.value))
const fileError = ref('')

// A check belongs to the token it checked: typing again hides it.
const checked = ref<{ token: string; info: LicenseInfo } | null>(null)
const result = computed(() => (checked.value && checked.value.token === token.value ? checked.value.info : null))
const resultState = computed(() => stateOf(result.value?.state))
const checking = ref(false)
const checkError = ref('')
const checkDetail = ref('')

/** A token core will not install comes back as a 400 with its reason code
 * (expired among them); empty or too long ones without a code. */
function refusedInfo(e: unknown): LicenseInfo | null {
  if (!(e instanceof ApiError) || e.status !== 400 || typeof e.data.code !== 'string' || !e.data.code) return null
  const code = e.data.code
  return { state: code === 'expired' ? 'expired' : 'invalid', reason: code, install_id: '', domain: '', license: null }
}

async function check() {
  if (problem.value) return
  const tok = token.value
  checking.value = true
  checkError.value = ''
  checkDetail.value = ''
  try {
    checked.value = { token: tok, info: await api<LicenseInfo>('POST', '/license/check', { token: tok }) }
  } catch (e) {
    const info = refusedInfo(e)
    if (info) {
      checked.value = { token: tok, info }
    } else {
      const err = errorText(e, t)
      checkError.value = err.text
      checkDetail.value = err.detail
    }
  } finally {
    checking.value = false
  }
}

const input = ref<HTMLInputElement | null>(null)
async function picked(ev: Event) {
  const el = ev.target as HTMLInputElement
  const file = el.files?.[0]
  el.value = ''
  if (!file) return
  fileError.value = ''
  if (file.size > TOKEN_FILE_MAX) {
    fileError.value = t('license.import.file_too_big')
    return
  }
  try {
    raw.value = (await file.text()).trim()
  } catch {
    fileError.value = t('license.import.file_unreadable')
  }
}

const confirmOpen = ref(false)
const saving = ref(false)
const saveError = ref('')
const saveDetail = ref('')
function askSave() {
  saveError.value = ''
  saveDetail.value = ''
  confirmOpen.value = true
}
async function save(reason: string) {
  if (!result.value) return
  saving.value = true
  saveError.value = ''
  saveDetail.value = ''
  try {
    const info = await api<LicenseInfo>('PUT', '/license', { token: token.value, reason })
    confirmOpen.value = false
    raw.value = ''
    checked.value = null
    toast.add({ severity: 'success', summary: t('license.import.saved'), life: 3000 })
    emit('saved', info)
  } catch (e) {
    const info = refusedInfo(e)
    if (info) {
      // core refused it after all: show why next to the token
      confirmOpen.value = false
      checked.value = { token: token.value, info }
      return
    }
    const err = errorText(e, t)
    saveError.value = err.text
    saveDetail.value = err.detail
  } finally {
    saving.value = false
  }
}

const tierName = (tier: string) => (te('license.tier.' + tier) ? t('license.tier.' + tier) : tier)
</script>

<template>
  <Card class="license-import">
    <template #title>{{ props.replacing ? t('license.import.replace') : t('license.import.add') }}</template>
    <template #content>
      <div class="app-form">
        <p class="app-muted hint">{{ t('license.import.hint') }}</p>
        <label>
          <span>{{ t('license.import.token') }}</span>
          <Textarea v-model="raw" rows="4" auto-resize dir="ltr" spellcheck="false" autocomplete="off" class="token-input app-mono" fluid />
          <small v-if="raw && problem && problem !== 'empty'" class="token-problem problem">{{ t('license.import.problem.' + problem) }}</small>
          <small v-if="fileError" class="file-error problem">{{ fileError }}</small>
        </label>
        <input ref="input" type="file" accept=".lic,.txt,.key,text/plain" class="file" tabindex="-1" aria-hidden="true" @change="picked" />
        <div class="app-actions">
          <Button :label="t('license.import.file')" icon="pi pi-upload" severity="secondary" outlined class="open-file" @click="input?.click()" />
          <Button :label="t('license.import.check')" icon="pi pi-verified" :loading="checking" :disabled="!!problem" class="check" @click="check" />
        </div>

        <Message v-if="checkError" severity="error" :closable="false" class="check-error">
          {{ checkError }}
          <div v-if="checkDetail" class="app-ltr detail">{{ checkDetail }}</div>
        </Message>
        <div v-if="result" class="check-result" aria-live="polite">
          <div class="result-head">
            <span>{{ t('license.import.result') }}</span>
            <Tag :severity="licenseSeverity[resultState]" :value="t('license.state.' + resultState)" class="result-state" />
            <template v-if="result.license">
              <span class="result-tier">{{ tierName(result.license.tier) }}</span>
              <span class="app-muted result-expires">{{ t('license.expires') }}: {{ date(result.license.expires_at, lang) }}</span>
            </template>
          </div>
          <p v-if="resultState === 'invalid'" class="result-reason">{{ t(reasonKey(result.reason)) }}</p>
          <Message v-if="resultState === 'expired'" severity="warn" :closable="false" class="expired-warn">{{ t('license.import.expired_warn') }}</Message>
          <div v-if="canSave(result.state)" class="app-actions">
            <Button :label="t('license.import.save')" icon="pi pi-save" class="save" @click="askSave" />
          </div>
        </div>
      </div>

      <ReasonDialog
        v-model:visible="confirmOpen"
        :title="t('license.import.confirm_title')"
        :message="t('license.import.confirm_message')"
        :confirm-label="t('license.import.save')"
        :busy="saving"
        :error="saveError"
        :detail="saveDetail"
        @confirm="save"
      />
    </template>
  </Card>
</template>

<style scoped>
.hint {
  margin: 0;
}
.file {
  position: absolute;
  inline-size: 1px;
  block-size: 1px;
  opacity: 0;
  pointer-events: none;
}
.problem {
  color: var(--p-red-500);
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
}
.check-result {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  padding: 0.75rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--app-radius);
}
.result-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
}
.result-reason {
  margin: 0;
}
</style>
