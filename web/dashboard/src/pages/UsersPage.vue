<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import InputText from 'primevue/inputtext'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import Select from 'primevue/select'
import Message from 'primevue/message'
import UsersTable from '../components/UsersTable.vue'
import { useList } from '../composables/useList'
import type { UserItem } from '../types'

const { t } = useI18n()
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
  { label: t('role.reseller'), value: 'reseller' },
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
  <UsersTable
    :items="list.items.value"
    :total="list.total.value"
    :rows="list.rows.value"
    :first="(list.page.value - 1) * list.rows.value"
    :loading="list.loading.value"
    @page="list.onPage"
    @open="open"
  />
</template>
