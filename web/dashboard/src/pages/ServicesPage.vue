<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import InputText from 'primevue/inputtext'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import Select from 'primevue/select'
import Message from 'primevue/message'
import ServicesTable from '../components/ServicesTable.vue'
import { useList } from '../composables/useList'
import type { ServiceItem } from '../types'

const { t } = useI18n()
const router = useRouter()
const list = useList<ServiceItem, { q: string; status: string }>('/services', { q: '', status: '' })
onMounted(list.load)
const statuses = computed(() => [
  { label: t('service.all_live'), value: '' },
  ...['active', 'expiring_soon', 'expired', 'depleted', 'disabled', 'pending', 'deleted'].map((s) => ({
    label: t('service.status.' + s),
    value: s,
  })),
])
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.services.title') }}</h1>
  </div>
  <div class="app-toolbar">
    <IconField class="app-grow">
      <InputIcon class="pi pi-search" />
      <InputText v-model="list.filters.q" :placeholder="t('service.search')" fluid :aria-label="t('service.search')" />
    </IconField>
    <Select v-model="list.filters.status" :placeholder="t('service.all_live')" :options="statuses" option-label="label" option-value="value" :aria-label="t('common.status')" />
  </div>
  <Message v-if="list.error.value" severity="error" :closable="false">{{ list.error.value }}</Message>
  <ServicesTable
    :items="list.items.value"
    :total="list.total.value"
    :rows="list.rows.value"
    :first="(list.page.value - 1) * list.rows.value"
    :loading="list.loading.value"
    @page="list.onPage"
    @open="(s) => router.push({ name: 'service', params: { id: s.id } })"
  />
</template>
