<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Message from 'primevue/message'
import { api } from '../../api'
import { errorText } from '../../errors'
import { dateTime, money, type Lang } from '../../format'
import type { PlanItem } from '../../types'

// The plans that were deleted (the owner's list): they are off sale and out of every list but kept, with their orders. Restoring one brings it back
// into the Plans table, off sale, for the owner to turn on.
const emit = defineEmits<{ restored: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()

const open = ref(false)
const items = ref<(PlanItem & { deleted_at?: number })[]>([])
const loading = ref(false)
const error = ref('')
const restoring = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    items.value = (await api<{ items: (PlanItem & { deleted_at?: number })[] }>('GET', '/plans/deleted')).items
  } catch (e) {
    error.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
async function toggle() {
  open.value = !open.value
  if (open.value) await load()
}
async function restore(p: PlanItem) {
  restoring.value = p.id
  try {
    await api('POST', `/plans/${p.id}/restore`)
    toast.add({ severity: 'success', summary: t('plan.restored'), life: 6000 })
    await load()
    emit('restored')
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 6000 })
  } finally {
    restoring.value = ''
  }
}
const label = (p: PlanItem) => p.name[lang.value] || p.name.en || p.name.fa || ''
</script>

<template>
  <section class="deleted">
    <Button :label="open ? t('plan.deleted_hide') : t('plan.deleted_show')" :icon="open ? 'pi pi-eye-slash' : 'pi pi-history'" text size="small" @click="toggle" />
    <template v-if="open">
      <p class="app-muted intro">{{ t('plan.deleted_intro') }}</p>
      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
      <DataTable v-else :value="items" :loading="loading" data-key="id" size="small" scrollable row-hover>
        <template #empty>{{ t('plan.deleted_none') }}</template>
        <Column :header="t('plan.name')">
          <template #body="{ data: p }"><bdi>{{ label(p) }}</bdi></template>
        </Column>
        <Column :header="t('plan.price')">
          <template #body="{ data: p }">{{ p.price.amount ? money(p.price, lang) : t('plan.free') }}</template>
        </Column>
        <Column :header="t('plan.deleted_on')">
          <template #body="{ data: p }">{{ p.deleted_at ? dateTime(p.deleted_at, lang) : '—' }}</template>
        </Column>
        <Column>
          <template #body="{ data: p }">
            <Button :label="t('plan.restore')" icon="pi pi-replay" size="small" outlined :loading="restoring === p.id" @click="restore(p)" />
          </template>
        </Column>
      </DataTable>
    </template>
  </section>
</template>

<style scoped>
.deleted {
  margin-block-start: var(--app-gap);
}
.intro {
  margin-block: 0.25rem 0.75rem;
}
</style>
