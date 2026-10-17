<script setup lang="ts">
// The online payment methods the store offers for an amount.
import { onMounted, ref } from 'vue'
import { api } from '../api'
import { errorKey } from '../errors'
import { tr, type Key } from '../i18n'

const props = defineProps<{ currency: string; amount: number; busy: boolean }>()
const emit = defineEmits<{ pick: [method: string] }>()

const methods = ref<string[] | null>(null)
const error = ref('')

async function load() {
  error.value = ''
  try {
    methods.value = await api.methods(props.currency, props.amount)
  } catch (e) {
    error.value = tr(errorKey(e))
  }
}
onMounted(load)
</script>

<template>
  <div class="methods">
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-else-if="methods === null" class="muted">{{ tr('common.loading') }}</p>
    <template v-else>
      <p v-if="!methods.length" class="muted">{{ tr('checkout.no_methods') }}</p>
      <button v-for="m in methods" :key="m" class="btn secondary" type="button" :disabled="busy" @click="emit('pick', m)">
        {{ tr(('method.' + m) as Key) }}
      </button>
      <p class="muted small">{{ tr('checkout.other_methods') }}</p>
    </template>
  </div>
</template>
