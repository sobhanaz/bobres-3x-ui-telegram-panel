<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Drawer from 'primevue/drawer'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import ToggleSwitch from 'primevue/toggleswitch'
import Tag from 'primevue/tag'
import Button from 'primevue/button'
import Message from 'primevue/message'
import { api } from '../../api'
import { errorText } from '../../errors'
import { currentLang } from '../../i18n'
import { isolate, num, type Lang } from '../../format'
import { noInbound } from '../../servers'
import type { InboundItem, ServerItem } from '../../types'

// A panel's inbounds and whether new services join each. The switch never
// touches the panel itself: existing services keep the inbounds they have.
const props = defineProps<{ server: ServerItem | null }>()
const visible = defineModel<boolean>('visible', { required: true })
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const items = ref<InboundItem[]>([])
const loading = ref(false)
const loadError = ref('')
const loadDetail = ref('')
const saveError = ref('')
const saveDetail = ref('')
const saving = ref<number[]>([])

watch(visible, (v) => {
  if (v) void load()
})
async function load() {
  if (!props.server) return
  loading.value = true
  loadError.value = ''
  loadDetail.value = ''
  saveError.value = ''
  items.value = []
  try {
    items.value = (await api<{ items: InboundItem[] }>('GET', `/servers/${props.server.id}/inbounds`)).items
  } catch (e) {
    const err = errorText(e, t)
    loadError.value = err.text
    loadDetail.value = err.detail
  } finally {
    loading.value = false
  }
}

// The switch moves at once and goes back when core refuses.
async function setAttach(row: InboundItem, attach: boolean) {
  if (!props.server || saving.value.includes(row.id)) return
  saving.value = [...saving.value, row.id]
  saveError.value = ''
  saveDetail.value = ''
  row.attach = attach
  try {
    await api('PUT', `/servers/${props.server.id}/inbounds/${row.id}`, { attach })
  } catch (e) {
    row.attach = !attach
    const err = errorText(e, t)
    saveError.value = err.text
    saveDetail.value = err.detail
  } finally {
    saving.value = saving.value.filter((id) => id !== row.id)
  }
}

const none = computed(() => !loading.value && !loadError.value && items.value.length > 0 && noInbound(items.value))
</script>

<template>
  <Drawer
    v-model:visible="visible"
    :position="currentLang() === 'fa' ? 'left' : 'right'"
    :header="t('server.inbounds_of', { name: isolate(props.server?.name ?? '') })"
    class="inbounds-drawer"
    :style="{ inlineSize: 'min(34rem, 100vw)' }"
  >
    <Message severity="info" :closable="false" class="note">{{ t('server.inbounds_note') }}</Message>
    <Message v-if="loadError" severity="error" :closable="false" class="note">
      <div class="load-error">
        <span>{{ loadError }}</span>
        <Button :label="t('app.retry')" icon="pi pi-refresh" size="small" severity="secondary" outlined @click="load" />
      </div>
      <div v-if="loadDetail" class="app-ltr detail">{{ loadDetail }}</div>
    </Message>
    <Message v-if="none" severity="warn" :closable="false" class="note none-attached">{{ t('server.no_inbound') }}</Message>
    <Message v-if="saveError" severity="error" :closable="false" class="note save-error">
      {{ saveError }}
      <div v-if="saveDetail" class="app-ltr detail">{{ saveDetail }}</div>
    </Message>
    <DataTable :value="items" :loading="loading" data-key="id" size="small" scrollable class="inbounds">
      <template #empty>{{ loadError ? '' : t('server.no_inbounds') }}</template>
      <Column :header="t('server.inbound')">
        <template #body="{ data: ib }">
          <span class="app-ltr app-mono">{{ ib.tag || '#' + ib.id }}</span>
          <div class="app-muted small">
            {{ t('server.inbound_id', { id: num(ib.id, lang) }) }}<template v-if="ib.protocol">
              · <span class="app-ltr">{{ ib.protocol }}{{ ib.port ? ':' + ib.port : '' }}</span></template>
          </div>
        </template>
      </Column>
      <Column :header="t('server.in_panel')">
        <template #body="{ data: ib }">
          <Tag :severity="ib.enabled ? 'success' : 'secondary'" :value="ib.enabled ? t('server.inbound_on') : t('server.inbound_off')" />
        </template>
      </Column>
      <Column :header="t('server.attach')">
        <template #body="{ data: ib }">
          <ToggleSwitch
            :model-value="ib.attach"
            :disabled="saving.includes(ib.id)"
            :aria-label="t('server.attach_for', { tag: ib.tag || '#' + ib.id })"
            :class="'attach attach-' + ib.id"
            @update:model-value="(v: boolean) => setAttach(ib, v)"
          />
        </template>
      </Column>
    </DataTable>
  </Drawer>
</template>

<style scoped>
.note {
  margin-block-end: 0.75rem;
}
.load-error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
.small {
  font-size: 0.8rem;
}
</style>
