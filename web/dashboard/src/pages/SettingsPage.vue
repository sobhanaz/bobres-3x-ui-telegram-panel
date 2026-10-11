<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import SettingsCard from '../components/settings/SettingsCard.vue'
import { api } from '../api'
import { errorText } from '../errors'
import { effective, wideGroups } from '../settings'
import type { SettingItem, SettingsReply } from '../types'

// The bot's settings, one card per group in the order core gives. Each card
// saves on its own; core answers every setting, so all cards stay current.
const { t } = useI18n()

const reply = ref<SettingsReply | null>(null)
const loading = ref(true)
const loadError = ref('')
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    reply.value = await api<SettingsReply>('GET', '/settings')
  } catch (e) {
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

const byGroup = computed(() => {
  const out: Record<string, SettingItem[]> = {}
  for (const item of reply.value?.items ?? []) (out[item.group] ??= []).push(item)
  return out
})
// Groups in core's order; a group core did not list still shows, at the end.
const groups = computed(() => {
  const listed = (reply.value?.groups ?? []).filter((g) => byGroup.value[g]?.length)
  return [...listed, ...Object.keys(byGroup.value).filter((g) => !listed.includes(g))]
})

const wide = computed(() => wideGroups(groups.value.map((g) => ({ group: g, count: byGroup.value[g].length }))))

const maintenanceOn = computed(() => {
  const m = reply.value?.items.find((i) => i.key === 'maintenance.enabled')
  return m ? effective(m) === 'true' : false
})

function saved(r: SettingsReply) {
  reply.value = r
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.settings.title') }}</h1>
  </div>
  <p class="app-muted intro">{{ t('settings.intro') }}</p>

  <Message v-if="maintenanceOn" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="banner">
    {{ t('settings.maintenance_on') }}
  </Message>
  <Message v-if="loadError" severity="error" :closable="false" class="banner">
    <div class="load-error">
      <span>{{ loadError }}</span>
      <Button :label="t('app.retry')" icon="pi pi-refresh" size="small" severity="secondary" outlined @click="load" />
    </div>
  </Message>

  <div v-if="loading && !reply" class="cards" aria-busy="true">
    <Skeleton v-for="n in 4" :key="n" height="14rem" border-radius="var(--app-radius)" />
  </div>
  <div v-else-if="reply" class="cards">
    <SettingsCard
      v-for="g in groups"
      :key="g"
      :group="g"
      :items="byGroup[g]"
      :class="{ wide: wide.includes(g) }"
      @saved="saved"
    />
    <p v-if="!groups.length" class="app-muted">{{ t('settings.empty') }}</p>
  </div>
</template>

<style scoped>
.intro {
  margin-block: calc(var(--app-gap) * -0.5) var(--app-gap);
}
.banner {
  margin-block-end: var(--app-gap);
}
.load-error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
}
.cards {
  display: grid;
  gap: var(--app-gap);
  /* two columns where they fit, one on a phone */
  grid-template-columns: repeat(auto-fit, minmax(min(100%, max(24rem, (100% - var(--app-gap)) / 2)), 1fr));
  align-items: stretch;
}
.cards > .wide {
  grid-column: 1 / -1;
}
</style>
