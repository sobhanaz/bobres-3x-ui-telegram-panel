<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Tab from 'primevue/tab'
import TabList from 'primevue/tablist'
import TabPanel from 'primevue/tabpanel'
import TabPanels from 'primevue/tabpanels'
import Tabs from 'primevue/tabs'
import Tag from 'primevue/tag'
import BackupsTab from '../components/system/BackupsTab.vue'
import HealthTab from '../components/system/HealthTab.vue'
import HttpsTab from '../components/system/HttpsTab.vue'
import LicenseTab from '../components/system/LicenseTab.vue'
import LogsTab from '../components/system/LogsTab.vue'
import UpdatesTab from '../components/system/UpdatesTab.vue'
import { chipOf, chipSeverityOf } from '../license'
import { num, type Lang } from '../format'
import { useAuth } from '../stores/auth'

// The system (owner and admin): every service's health and version, whether
// a newer release is out, the backups, the HTTPS certificates (the owner
// changes them), the license, and for the owner the services' recent warnings
// and errors. The tab is in the address (?tab=), so the menu's license chip can
// open it; each tab loads the first time it is shown.
const { t, locale } = useI18n()
const auth = useAuth()
const route = useRoute()
const router = useRouter()

// the logs carry Telegram ids: the owner's only (system.write)
const canLogs = computed(() => auth.can('system.write'))
const tabs = computed(() => (canLogs.value ? ['health', 'updates', 'backups', 'https', 'logs', 'license'] : ['health', 'updates', 'backups', 'https', 'license']))
const tabOf = (v: unknown) => (typeof v === 'string' && tabs.value.includes(v) ? v : 'health')
const tab = ref(tabOf(route.query.tab))
const seen = reactive(new Set<string>([tab.value]))
function showTab(v: string | number) {
  tab.value = tabOf(String(v))
  seen.add(tab.value)
  if (route.query.tab !== tab.value && (route.query.tab || tab.value !== 'health')) {
    void router.replace({ query: { ...route.query, tab: tab.value } })
  }
}
watch(() => route.query.tab, (v) => {
  if (tabOf(v) !== tab.value) showTab(tabOf(v))
})

// The license tab says what applies now when it needs a look (from /me): the
// free tier, the trial, or a license that is not fine.
const licenseChip = computed(() => chipOf(auth.me?.license))
const chipText = (c: { kind: string; days: number }) => t('license.chip.' + c.kind, { n: num(c.days, locale.value as Lang) }, c.days)
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.system.title') }}</h1>
  </div>
  <p class="app-muted intro">{{ t('system.intro') }}</p>

  <Tabs :value="tab" class="system-tabs" @update:value="showTab">
    <TabList>
      <Tab value="health" class="tab-health">{{ t('system.tab.health') }}</Tab>
      <Tab value="updates" class="tab-updates">{{ t('system.tab.updates') }}</Tab>
      <Tab value="backups" class="tab-backups">{{ t('system.tab.backups') }}</Tab>
      <Tab value="https" class="tab-https">{{ t('system.tab.https') }}</Tab>
      <Tab v-if="canLogs" value="logs" class="tab-logs">{{ t('system.tab.logs') }}</Tab>
      <Tab value="license" class="tab-license">
        {{ t('system.tab.license') }}
        <Tag v-if="licenseChip" :severity="chipSeverityOf(licenseChip)" :value="chipText(licenseChip)" class="tab-state" />
      </Tab>
    </TabList>
    <TabPanels>
      <TabPanel value="health">
        <HealthTab v-if="seen.has('health')" />
      </TabPanel>
      <TabPanel value="updates">
        <UpdatesTab v-if="seen.has('updates')" />
      </TabPanel>
      <TabPanel value="backups">
        <BackupsTab v-if="seen.has('backups')" />
      </TabPanel>
      <TabPanel value="https">
        <HttpsTab v-if="seen.has('https')" />
      </TabPanel>
      <TabPanel v-if="canLogs" value="logs">
        <LogsTab v-if="seen.has('logs')" />
      </TabPanel>
      <TabPanel value="license">
        <LicenseTab v-if="seen.has('license')" />
      </TabPanel>
    </TabPanels>
  </Tabs>
</template>

<style scoped>
.intro {
  margin-block: calc(var(--app-gap) * -0.5) var(--app-gap);
}
.tab-state {
  margin-inline-start: 0.4rem;
}
.system-tabs :deep(.p-tabpanels) {
  padding-inline: 0;
  background: transparent;
}
</style>
