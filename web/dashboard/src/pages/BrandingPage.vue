<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Tabs from 'primevue/tabs'
import TabList from 'primevue/tablist'
import Tab from 'primevue/tab'
import TabPanels from 'primevue/tabpanels'
import TabPanel from 'primevue/tabpanel'
import StoreTab from '../components/branding/StoreTab.vue'
import TextsTab from '../components/branding/TextsTab.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

// The tab is in the address (?tab=store|texts); each tab loads when first shown.
const tabs = ['store', 'texts']
const initial = String(route.query.tab ?? 'store')
const tab = ref(tabs.includes(initial) ? initial : 'store')
watch(tab, (v) => void router.replace({ query: { ...route.query, tab: v } }))
function showTab(v: string | number) {
  tab.value = String(v)
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.branding.title') }}</h1>
  </div>
  <Tabs :value="tab" @update:value="showTab">
    <TabList>
      <Tab value="store">{{ t('branding.tab_store') }}</Tab>
      <Tab value="texts">{{ t('texts.tab') }}</Tab>
    </TabList>
    <TabPanels>
      <TabPanel value="store">
        <StoreTab :active="tab === 'store'" />
      </TabPanel>
      <TabPanel value="texts">
        <TextsTab :active="tab === 'texts'" />
      </TabPanel>
    </TabPanels>
  </Tabs>
</template>
