<script setup lang="ts">
// A new config name for one of the user's services (My services, and the reseller panel's services): core checks it as
// at the checkout and renames the config on its panel. The subscription link stays the same, which the form says.
import { ref } from 'vue'
import type { Service } from '../../api'
import { tr } from '../../i18n'
import { fail } from '../../store'
import { haptic } from '../../telegram'
import { nameError, namesApi, reasonText } from './api'

const props = defineProps<{ serviceId: string; current?: string }>()
const emit = defineEmits<{ done: [name: string, service: Service | undefined]; cancel: [] }>()

const draft = ref('')
const busy = ref(false)
const error = ref('')

async function save() {
  const name = draft.value.trim()
  error.value = ''
  if (!name) {
    error.value = tr('names.reason.length')
    return
  }
  busy.value = true
  try {
    const res = await namesApi.rename(props.serviceId, name)
    if (res.ok) {
      haptic('success')
      emit('done', res.name, res.service)
    } else {
      haptic('error')
      error.value = reasonText(res.reason)
    }
  } catch (e) {
    haptic('error')
    if (!fail(e)) error.value = nameError(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="rename" @submit.prevent="save">
    <label class="field">
      <span>{{ tr('names.new') }}</span>
      <input v-model="draft" type="text" dir="ltr" autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="40"
        enterkeyhint="done" :placeholder="current" />
      <span class="muted small">{{ tr('names.rules') }}</span>
      <span class="muted small">{{ tr('names.link_stays') }}</span>
    </label>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <div class="row wrap">
      <button class="btn" type="submit" :disabled="busy">{{ busy ? tr('names.checking') : tr('names.save') }}</button>
      <button class="btn secondary" type="button" @click="emit('cancel')">{{ tr('names.cancel') }}</button>
    </div>
  </form>
</template>

<style scoped>
.rename { display: grid; gap: 0.5rem; margin-top: 0.5rem; }
</style>
