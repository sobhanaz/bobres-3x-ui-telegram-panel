<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Card from 'primevue/card'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import ReasonDialog from '../ReasonDialog.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { keyOf, keyProblem, keyRefusal } from '../../license'
import type { LicenseInfo } from '../../types'

// The owner types the license key the seller gave; core asks the license
// server for this install's license (through the bot: core has no way out),
// checks it and installs it, with the owner's reason in the audit log. The
// key is never kept or shown again.
const emit = defineEmits<{ saved: [info: LicenseInfo] }>()
const { t } = useI18n()
const toast = useToast()

const raw = ref('')
const key = computed(() => keyOf(raw.value))
const problem = computed(() => keyProblem(raw.value))
// nothing typed yet is not a problem to point out
const shownProblem = computed(() => (problem.value === 'empty' ? '' : problem.value))

// A refusal belongs to the key it refused: typing again hides it.
const refused = ref<{ key: string; reason: string; detail: string } | null>(null)
const refusal = computed(() => (refused.value && refused.value.key === key.value ? refused.value : null))

const confirmOpen = ref(false)
const busy = ref(false)
const error = ref('')
const detail = ref('')
function ask() {
  if (problem.value) return
  error.value = ''
  detail.value = ''
  confirmOpen.value = true
}

/** No license server is set up (BOBRES_LICENSE_SERVER on the bot), or it,
 * or the bot on the way, did not answer or failed: said as such, with core's
 * words on which. '' for any other failure. */
function serverTrouble(e: unknown): string {
  if (!(e instanceof ApiError) || ![502, 503, 504].includes(e.status)) return ''
  return e.data.code === 'not_configured' ? 'license.activate.not_configured' : 'license.activate.unreachable'
}

async function activate(reason: string) {
  const k = key.value
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    const info = await api<LicenseInfo>('POST', '/license/activate', { key: k, reason })
    confirmOpen.value = false
    raw.value = ''
    refused.value = null
    toast.add({ severity: 'success', summary: t('license.activate.done'), life: 3000 })
    emit('saved', info)
  } catch (e) {
    const no = e instanceof ApiError ? keyRefusal(e) : null
    if (no) {
      // shown next to the key, where it can be corrected
      confirmOpen.value = false
      refused.value = { key: k, ...no }
      return
    }
    const trouble = serverTrouble(e)
    if (trouble) {
      const data = (e as ApiError).data
      error.value = t(trouble)
      detail.value = typeof data.message === 'string' ? data.message : ''
      return
    }
    const err = errorText(e, t)
    error.value = err.text
    detail.value = err.detail
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Card class="license-activate">
    <template #title>{{ t('license.activate.title') }}</template>
    <template #content>
      <form class="app-form" @submit.prevent="ask">
        <p class="app-muted hint">{{ t('license.activate.hint') }}</p>
        <label>
          <span>{{ t('license.activate.key') }}</span>
          <InputText
            v-model="raw"
            dir="ltr"
            spellcheck="false"
            autocomplete="off"
            autocapitalize="characters"
            placeholder="BOBRES-XXXX-XXXX-XXXX"
            :invalid="!!refusal || shownProblem !== ''"
            :aria-describedby="shownProblem ? 'license-key-problem' : refusal ? 'license-key-refused' : 'license-key-help'"
            class="key-input app-mono"
            fluid
          />
          <small v-if="shownProblem" id="license-key-problem" class="key-problem problem" aria-live="polite">{{ t('license.activate.problem.' + shownProblem) }}</small>
          <small v-else id="license-key-help" class="app-muted">{{ t('license.activate.key_hint') }}</small>
        </label>
        <Message v-if="refusal" id="license-key-refused" severity="error" :closable="false" class="key-refused">
          <span class="key-reason">{{ t(refusal.reason) }}</span>
          <div v-if="refusal.detail" class="app-ltr detail">{{ refusal.detail }}</div>
        </Message>
        <div class="app-actions">
          <Button type="submit" :label="t('license.activate.activate')" icon="pi pi-key" :disabled="!!problem" class="activate" />
        </div>
      </form>

      <ReasonDialog
        v-model:visible="confirmOpen"
        :title="t('license.activate.confirm_title')"
        :message="t('license.activate.confirm_message')"
        :confirm-label="t('license.activate.activate')"
        :busy="busy"
        :error="error"
        :detail="detail"
        @confirm="activate"
      />
    </template>
  </Card>
</template>

<style scoped>
.hint {
  margin: 0;
}
.problem {
  color: var(--p-red-500);
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
</style>
