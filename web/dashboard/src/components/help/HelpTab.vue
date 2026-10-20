<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Message from 'primevue/message'
import HelpSection from './HelpSection.vue'
import { api } from '../../api'
import { errorText } from '../../errors'
import type { HelpPage } from '../../types'

// The FAQ and the tutorials the bot shows, in Persian and English (Branding,
// "Help content"). The page loads when its tab is first opened and after every
// change that does not bring the lists back itself, so the order, the versions
// and how the bot lays out the FAQ always come from core.
const props = defineProps<{ active: boolean }>()
const { t } = useI18n()

const page = ref<HelpPage | null>(null)
const loadError = ref('')
const loading = ref(false)
let loaded = false

async function load() {
  loaded = true
  loading.value = true
  loadError.value = ''
  try {
    page.value = await api<HelpPage>('GET', '/help')
  } catch (e) {
    loaded = page.value !== null
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
watch(
  () => props.active,
  (a) => {
    if (a && !loaded) void load()
  },
  { immediate: true },
)
</script>

<template>
  <p class="app-muted intro">{{ t('help.intro') }}</p>
  <Message v-if="loadError" severity="error" :closable="false">
    {{ loadError }}
    <Button :label="t('app.retry')" text size="small" @click="load" />
  </Message>
  <p v-if="loading && !page" class="app-muted">{{ t('app.loading') }}</p>
  <template v-if="page">
    <HelpSection kind="faq" :page="page" @page="(p) => (page = p)" @reload="load" />
    <HelpSection kind="tutorial" :page="page" @page="(p) => (page = p)" @reload="load" />
  </template>
</template>

<style scoped>
.intro {
  margin-block: 0 var(--app-gap);
}
</style>
