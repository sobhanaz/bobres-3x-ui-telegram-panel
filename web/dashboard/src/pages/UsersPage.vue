<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import InputText from 'primevue/inputtext'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import UserLink from '../components/UserLink.vue'
import StatusTag from '../components/StatusTag.vue'
import { useList } from '../composables/useList'
import { date, money, num, type Lang } from '../format'
import type { UserItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const router = useRouter()
const list = useList<UserItem, { q: string; status: string; role: string }>('/users', { q: '', status: '', role: '' })
onMounted(list.load)

const statuses = computed(() => [
  { label: t('common.all'), value: '' },
  { label: t('user.status.active'), value: 'active' },
  { label: t('user.status.banned'), value: 'banned' },
])
const roles = computed(() => [
  { label: t('user.all_roles'), value: '' },
  { label: t('user.role_customer'), value: 'user' },
  { label: t('role.support'), value: 'support' },
  { label: t('role.admin'), value: 'admin' },
  { label: t('role.owner'), value: 'owner' },
])
function open(u: UserItem) {
  void router.push({ name: 'user', params: { id: u.id } })
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.users.title') }}</h1>
  </div>
  <div class="app-toolbar">
    <IconField class="app-grow">
      <InputIcon class="pi pi-search" />
      <InputText v-model="list.filters.q" :placeholder="t('user.search')" fluid :aria-label="t('user.search')" />
    </IconField>
    <Select v-model="list.filters.status" :placeholder="t('common.all')" :options="statuses" option-label="label" option-value="value" :aria-label="t('common.status')" />
    <Select v-model="list.filters.role" :placeholder="t('user.all_roles')" :options="roles" option-label="label" option-value="value" :aria-label="t('user.role')" />
  </div>
  <Message v-if="list.error.value" severity="error" :closable="false">{{ list.error.value }}</Message>
  <DataTable
    :value="list.items.value"
    lazy
    :paginator="list.total.value > list.rows.value"
    :rows="list.rows.value"
    :first="(list.page.value - 1) * list.rows.value"
    :total-records="list.total.value"
    :rows-per-page-options="[25, 50, 100]"
    :loading="list.loading.value"
    data-key="id"
    size="small"
    scrollable
    row-hover
    class="app-clickable"
    @page="list.onPage"
    @row-click="open($event.data)"
  >
    <template #empty>{{ t('user.none') }}</template>
    <Column :header="t('common.customer')">
      <template #body="{ data: u }">
        <UserLink :user="u" :link="false" />
        <Tag v-if="u.role !== 'user'" :value="t('role.' + u.role)" severity="info" class="role" />
      </template>
    </Column>
    <Column :header="t('common.status')">
      <template #body="{ data: u }"><StatusTag kind="user" :status="u.status" /></template>
    </Column>
    <Column :header="t('user.wallet')" body-class="app-nowrap">
      <template #body="{ data: u }">
        <div v-for="w in u.wallets" :key="w.currency">{{ money(w, lang) }}</div>
        <span v-if="!u.wallets.length" class="app-muted">—</span>
      </template>
    </Column>
    <Column :header="t('user.services')">
      <template #body="{ data: u }">{{ num(u.subscriptions ?? 0, lang) }}</template>
    </Column>
    <Column :header="t('user.orders')">
      <template #body="{ data: u }">{{ num(u.orders ?? 0, lang) }}</template>
    </Column>
    <Column :header="t('user.joined')" body-class="app-nowrap">
      <template #body="{ data: u }">{{ date(u.created_at, lang) }}</template>
    </Column>
  </DataTable>
</template>

<style scoped>
.role {
  margin-inline-start: 0.5rem;
}
</style>
