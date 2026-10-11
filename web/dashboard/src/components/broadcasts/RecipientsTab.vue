<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Select from 'primevue/select'
import Message from 'primevue/message'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import UserLink from '../UserLink.vue'
import { useList } from '../../composables/useList'
import { dateTime, type Lang } from '../../format'
import { PROBLEM_STATUSES } from '../../broadcasts'
import type { BroadcastRecipientItem } from '../../types'

// The customers a broadcast could not reach, one result at a time (failed,
// blocked the bot, unknown), with Telegram's answer. Loads when first shown.
const props = defineProps<{ broadcastId: string; active: boolean }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const list = useList<BroadcastRecipientItem, { status: string }>(`/broadcasts/${props.broadcastId}/recipients`, { status: 'failed' }, 25)
let loaded = false
watch(
  () => props.active,
  (on) => {
    if (!on || loaded) return
    loaded = true
    void list.load()
  },
  { immediate: true },
)

const options = computed(() => PROBLEM_STATUSES.map((s) => ({ label: t('broadcasts.recipients.status.' + s), value: s })))
const statusLabel = (s: string) => (te('broadcasts.recipients.status.' + s) ? t('broadcasts.recipients.status.' + s) : s)
</script>

<template>
  <div class="app-toolbar">
    <Select
      v-model="list.filters.status"
      :options="options"
      option-label="label"
      option-value="value"
      :aria-label="t('broadcasts.recipients.status_label')"
    />
    <Button v-if="list.loading.value" text loading :aria-label="t('app.loading')" />
  </div>
  <p class="app-muted hint">{{ t('broadcasts.recipients.hint.' + list.filters.status) }}</p>
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
    size="small"
    scrollable
    class="recipients"
    @page="list.onPage"
  >
    <template #empty>{{ t('broadcasts.recipients.empty') }}</template>
    <Column :header="t('common.customer')">
      <template #body="{ data: r }"><UserLink :user="r.user" /></template>
    </Column>
    <Column :header="t('broadcasts.recipients.status_label')">
      <template #body="{ data: r }">{{ statusLabel(r.status) }}</template>
    </Column>
    <Column :header="t('broadcasts.recipients.error')">
      <template #body="{ data: r }">
        <span v-if="r.error" class="app-ltr error-text">{{ r.error }}</span>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
    <Column :header="t('broadcasts.recipients.when')" body-class="app-nowrap">
      <template #body="{ data: r }">
        <template v-if="r.done_at">{{ dateTime(r.done_at, lang) }}</template>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
  </DataTable>
</template>

<style scoped>
.hint {
  margin-block: 0 0.75rem;
  font-size: 0.85rem;
}
.error-text {
  font-size: 0.85rem;
  overflow-wrap: anywhere;
}
</style>
