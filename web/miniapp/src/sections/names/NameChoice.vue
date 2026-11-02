<script setup lang="ts">
// The checkout's name step for a new service: the automatic name (the default) or one the buyer chooses, checked with
// core before the order is made, so nobody pays for a name that is taken. The checkout asks it for the name to send
// (ensure) and hands it core's refusal when the order still comes back with one (refused).
import { ref, watch } from 'vue'
import { isolate } from '../../format'
import { tr } from '../../i18n'
import { haptic } from '../../telegram'
import { nameError, namesApi, reasonText } from './api'

defineProps<{ disabled?: boolean }>()

const mode = ref<'auto' | 'choose'>('auto')
const draft = ref('')
const busy = ref(false)
const error = ref('')
// the full name core said the draft gets ("" until a check of this draft said yes)
const free = ref('')

// A changed name is a new one to check.
watch(draft, () => {
  free.value = ''
  error.value = ''
})

function pick(m: 'auto' | 'choose') {
  mode.value = m
  error.value = ''
}

// the check on its way, and the name it is about: a tap on Continue right after the field lost focus waits for it
let inflight: { name: string; done: Promise<boolean> } | null = null

/** check asks core about the name typed; it says whether it can be used. */
function check(): Promise<boolean> {
  const name = draft.value.trim()
  if (inflight?.name === name) return inflight.done
  const done = ask(name).finally(() => {
    if (inflight?.done === done) inflight = null
  })
  inflight = { name, done }
  return done
}

async function ask(name: string): Promise<boolean> {
  error.value = ''
  free.value = ''
  if (!name) {
    error.value = tr('names.reason.length')
    return false
  }
  busy.value = true
  try {
    const res = await namesApi.check(name)
    if (draft.value.trim() !== name) return false // typed on meanwhile
    if (res.ok) {
      free.value = res.name
      return true
    }
    error.value = reasonText(res.reason)
    haptic('error')
    return false
  } catch (e) {
    error.value = nameError(e)
    return false
  } finally {
    busy.value = false
  }
}

/** ensure is the name the order carries: "" for the automatic one, the name typed once it checked free, null while it is not. */
async function ensure(): Promise<string | null> {
  if (mode.value === 'auto') return ''
  if (free.value || (await check())) return draft.value.trim()
  if (!error.value) error.value = tr('names.need_check')
  return null
}

/** refused shows core's refusal of the name at the order (taken meanwhile): the buyer types another or goes automatic. */
function refused(reason: string) {
  free.value = ''
  error.value = reasonText(reason)
}

/** chosen is the full name to show once the order is made ("" for the automatic one). */
const chosen = (): string => (mode.value === 'choose' ? free.value : '')

defineExpose({ ensure, refused, chosen })
</script>

<template>
  <fieldset class="names" :disabled="disabled">
    <legend>{{ tr('names.title') }}</legend>
    <div class="row wrap" role="radiogroup">
      <button class="btn" :class="{ secondary: mode !== 'auto' }" type="button" role="radio" :aria-checked="mode === 'auto'" @click="pick('auto')">
        {{ tr('names.auto') }}
      </button>
      <button class="btn" :class="{ secondary: mode !== 'choose' }" type="button" role="radio" :aria-checked="mode === 'choose'" @click="pick('choose')">
        {{ tr('names.choose') }}
      </button>
    </div>
    <template v-if="mode === 'choose'">
      <label class="field">
        <span>{{ tr('names.label') }}</span>
        <span class="row">
          <input v-model="draft" type="text" dir="ltr" autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="40"
            enterkeyhint="done" @keydown.enter.prevent="check" @blur="draft.trim() && !free && !busy && check()" />
          <button class="btn secondary" type="button" :disabled="busy" @click="check">{{ busy ? tr('names.checking') : tr('names.check') }}</button>
        </span>
        <span class="muted small">{{ tr('names.rules') }}</span>
      </label>
      <p v-if="free" class="ok" role="status">{{ tr('names.free', { name: isolate(free) }) }}</p>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
    </template>
  </fieldset>
</template>

<style scoped>
.names { border: 0; padding: 0; margin: 0.75rem 0 0; display: grid; gap: 0.5rem; }
.names legend { font-weight: 600; margin-bottom: 0.25rem; }
</style>
