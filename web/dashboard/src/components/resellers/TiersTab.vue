<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Message from 'primevue/message'
import TierDialog from './TierDialog.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { isolate, num, type Lang } from '../../format'
import { percent } from '../../resellers'
import type { PlanItem, TierItem } from '../../types'

// The Resellers page's Tiers tab: the page owns the list (its filter uses
// it too); this tab edits it and says when it changed.
const props = defineProps<{ tiers: TierItem[]; loading: boolean; error: string }>()
const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()

// The editor prices plans: they load with the tab.
const plans = ref<PlanItem[]>([])
const plansReady = ref(false)
const plansError = ref('')
async function loadPlans() {
  plansError.value = ''
  try {
    plans.value = (await api<{ items: PlanItem[] }>('GET', '/plans')).items
    plansReady.value = true
  } catch (e) {
    plansError.value = errorText(e, t).text
  }
}
onMounted(loadPlans)

const editing = ref(false)
const current = ref<TierItem | null>(null)
function edit(tier: TierItem | null) {
  current.value = tier
  if (plansError.value) void loadPlans()
  editing.value = true
}
function saved() {
  toast.add({ severity: 'success', summary: t('tier.saved'), life: 3000 })
  emit('changed')
}
// Someone else changed the tier meanwhile: read the list again, so the
// editor opens with the new values (and version) next time.
function stale() {
  emit('changed')
}

function remove(tier: TierItem) {
  confirm.require({
    header: t('tier.delete'),
    message: t('tier.delete_confirm', { name: isolate(tier.name) }),
    acceptProps: { label: t('tier.delete'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      try {
        await api('DELETE', `/reseller-tiers/${tier.id}`)
        toast.add({ severity: 'success', summary: t('tier.deleted'), life: 3000 })
      } catch (e) {
        const summary = e instanceof ApiError && e.status === 409 ? t('tier.in_use') : errorText(e, t).text
        toast.add({ severity: 'error', summary, life: 6000 })
      }
      emit('changed')
    },
  })
}
</script>

<template>
  <div class="app-page-head head">
    <p class="app-muted intro">{{ t('tier.intro') }}</p>
    <Button :label="t('tier.new')" icon="pi pi-plus" class="new-tier" @click="edit(null)" />
  </div>
  <Message v-if="props.error" severity="error" :closable="false">{{ props.error }}</Message>
  <DataTable :value="props.tiers" :loading="props.loading" data-key="id" size="small" scrollable row-hover class="tiers">
    <template #empty>{{ t('tier.none') }}</template>
    <Column :header="t('tier.name')">
      <template #body="{ data: tr }"><bdi>{{ tr.name }}</bdi></template>
    </Column>
    <Column :header="t('tier.discount')">
      <template #body="{ data: tr }">{{ percent(tr.discount_percent, lang) }}</template>
    </Column>
    <Column :header="t('tier.commission')">
      <template #body="{ data: tr }">{{ percent(tr.commission_percent, lang) }}</template>
    </Column>
    <Column :header="t('tier.fixed_prices')">
      <template #body="{ data: tr }">
        <span v-if="tr.prices?.length">{{ num(tr.prices.length, lang) }}</span>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
    <Column :header="t('section.resellers.title')">
      <template #body="{ data: tr }">{{ num(tr.resellers ?? 0, lang) }}</template>
    </Column>
    <Column body-class="app-nowrap">
      <template #body="{ data: tr }">
        <Button icon="pi pi-pencil" text rounded :aria-label="t('common.edit')" class="edit" @click="edit(tr)" />
        <span v-tooltip.top="tr.resellers ? t('tier.in_use') : undefined" class="delete-wrap">
          <Button
            icon="pi pi-trash"
            text
            rounded
            severity="danger"
            :disabled="tr.resellers > 0"
            :aria-label="t('tier.delete')"
            class="delete"
            @click="remove(tr)"
          />
        </span>
      </template>
    </Column>
  </DataTable>

  <TierDialog v-model:visible="editing" :tier="current" :plans="plans" :plans-ready="plansReady" :plans-error="plansError" @saved="saved" @stale="stale" />
</template>

<style scoped>
.head {
  align-items: flex-start;
}
.intro {
  margin: 0;
  flex: 1 1 20rem;
  font-size: 0.9rem;
}
.delete-wrap {
  display: inline-block;
}
</style>
