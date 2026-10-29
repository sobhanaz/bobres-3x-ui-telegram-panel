<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import JoinChatsCard from '../components/settings/JoinChatsCard.vue'
import SecurityCard from '../components/settings/SecurityCard.vue'
import MetricsCard from '../components/settings/MetricsCard.vue'
import SettingsCard from '../components/settings/SettingsCard.vue'
import WheelReportCard from '../components/settings/WheelReportCard.vue'
import { api } from '../api'
import { errorText } from '../errors'
import { effective, movedGroups, wideGroups } from '../settings'
import { useAuth } from '../stores/auth'
import type { SettingItem, SettingsReply } from '../types'

// The bot's settings, one card per group in the order core gives. Each card
// saves on its own; core answers every setting, so all cards stay current.
// The payment details live on the Payment gateways page (owner only).
const { t } = useI18n()
const auth = useAuth()
const route = useRoute()

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
  await showEntry()
}
onMounted(load)

// A link may name one entry (the Reports page links to the daily summary's
// switch): once the cards are drawn it is brought into view.
async function showEntry() {
  const id = route.hash.slice(1)
  if (!id) return
  await nextTick()
  document.getElementById(id)?.scrollIntoView?.({ block: 'center' })
}

const byGroup = computed(() => {
  const out: Record<string, SettingItem[]> = {}
  for (const item of reply.value?.items ?? []) if (!movedGroups.includes(item.group)) (out[item.group] ??= []).push(item)
  return out
})
// Groups in core's order; a group core did not list still shows, at the end.
const groups = computed(() => {
  const listed = (reply.value?.groups ?? []).filter((g) => byGroup.value[g]?.length)
  return [...listed, ...Object.keys(byGroup.value).filter((g) => !listed.includes(g))]
})

// The required channels and groups are one list with its own card (the join
// group's settings are the list's first row); it takes the page's full width.
const JOIN = 'join'
const MONITOR = 'monitor'
const wide = computed(() => [
  ...wideGroups(groups.value.filter((g) => g !== JOIN).map((g) => ({ group: g, count: byGroup.value[g].length }))),
  JOIN,
])

const maintenanceOn = computed(() => {
  const m = reply.value?.items.find((i) => i.key === 'maintenance.enabled')
  return m ? effective(m) === 'true' : false
})

// The wheel's report follows what was saved (its budget, whether it is on).
const saves = ref(0)
function saved(r: SettingsReply) {
  reply.value = r
  saves.value++
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.settings.title') }}</h1>
  </div>
  <p class="app-muted intro">
    {{ t('settings.intro') }}
    <template v-if="auth.can('gateways.write')">
      {{ t('settings.payments_moved') }}
      <RouterLink :to="{ name: 'gateways' }" class="payments-moved">{{ t('section.gateways.title') }}</RouterLink>
    </template>
  </p>
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
    <template v-for="g in groups" :key="g">
      <JoinChatsCard v-if="g === JOIN" class="wide" />
      <SettingsCard v-else :group="g" :items="byGroup[g]" :class="{ wide: wide.includes(g) }" @saved="saved" />
      <!-- /metrics for Prometheus: its token is write-only, so it is not a setting of the card above -->
      <MetricsCard v-if="g === MONITOR" class="wide" />
      <WheelReportCard v-if="g === 'wheel' && auth.can('reports.read')" class="wide" :refresh-key="saves" />
    </template>
    <p v-if="!groups.length" class="app-muted">{{ t('settings.empty') }}</p>
    <!-- How staff sign in: its own card and endpoint, because the owner changes it with a confirmation, not with Save. -->
    <SecurityCard class="wide" />
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
