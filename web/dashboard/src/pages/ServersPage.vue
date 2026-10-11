<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import ServerDialog from '../components/servers/ServerDialog.vue'
import InboundsDrawer from '../components/servers/InboundsDrawer.vue'
import { api, ApiError } from '../api'
import { errorText } from '../errors'
import { dateTime, isolate, num, planName, type Lang } from '../format'
import { healthOf, healthSeverity } from '../servers'
import type { ServerItem, ServersReply } from '../types'

// The 3x-ui panels services are created on (owner and admin): their health,
// load and plans. New services go to the least loaded healthy server a plan
// allows; a server keeps its existing services whatever is changed here.
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()

const servers = ref<ServerItem[]>([])
// The panel versions BOBRES is tested with, when core says.
const tested = ref({ min: '', max: '' })
const loading = ref(true)
const loadError = ref('')
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const r = await api<ServersReply>('GET', '/servers')
    servers.value = r.items
    tested.value = { min: r.tested_min_version ?? '', max: r.tested_max_version ?? '' }
  } catch (e) {
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

const editing = ref(false)
const current = ref<ServerItem | null>(null)
function edit(s: ServerItem | null) {
  current.value = s
  editing.value = true
}
function saved() {
  toast.add({ severity: 'success', summary: t('server.saved'), life: 3000 })
  void load()
}

const untestedText = computed(() =>
  tested.value.min && tested.value.max
    ? t('server.version_range', { min: isolate(tested.value.min), max: isolate(tested.value.max) })
    : t('server.version_untested'),
)

const inbounds = ref(false)
const inboundsOf = ref<ServerItem | null>(null)
function showInbounds(s: ServerItem) {
  inboundsOf.value = s
  inbounds.value = true
}

function remove(s: ServerItem) {
  confirm.require({
    header: t('server.delete'),
    message: t('server.delete_confirm', { name: isolate(s.name) }),
    acceptProps: { label: t('server.delete'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      try {
        await api('DELETE', `/servers/${s.id}`)
        toast.add({ severity: 'success', summary: t('server.deleted'), life: 3000 })
      } catch (e) {
        // 409: services on it, or plans still sending new services to it (core says which).
        const summary = e instanceof ApiError && e.status === 409 && s.clients > 0 ? t('server.in_use') : errorText(e, t).text
        toast.add({ severity: 'error', summary, life: 6000 })
      }
      void load()
    },
  })
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.servers.title') }}</h1>
    <Button :label="t('server.add')" icon="pi pi-plus" class="add" @click="edit(null)" />
  </div>
  <p class="app-muted intro">{{ t('server.intro') }}</p>
  <Message v-if="loadError" severity="error" :closable="false" class="banner">
    <div class="load-error">
      <span>{{ loadError }}</span>
      <Button :label="t('app.retry')" icon="pi pi-refresh" size="small" severity="secondary" outlined @click="load" />
    </div>
  </Message>

  <DataTable :value="servers" :loading="loading" data-key="id" size="small" scrollable row-hover class="servers">
    <template #empty>{{ loadError ? '' : t('server.none') }}</template>
    <Column :header="t('server.name')">
      <template #body="{ data: s }">
        <bdi class="server-name">{{ s.name }}</bdi>
        <div class="app-muted small app-ltr url">{{ s.base_url }}</div>
        <div v-if="!s.has_token || s.allow_private" class="tags">
          <Tag v-if="!s.has_token" :value="t('server.no_token')" severity="danger" />
          <Tag v-if="s.allow_private" :value="t('server.private')" severity="warn" />
        </div>
      </template>
    </Column>
    <Column :header="t('server.health')">
      <template #body="{ data: s }">
        <Tag :severity="healthSeverity[healthOf(s)]" :value="t('server.health_state.' + healthOf(s))" class="health" />
        <div v-if="healthOf(s) === 'down' && s.health_error" class="app-muted small app-ltr detail">{{ s.health_error }}</div>
        <div v-if="s.checked_at && healthOf(s) !== 'off'" class="app-muted small">
          {{ t('server.checked_at', { time: dateTime(s.checked_at, lang) }) }}
        </div>
      </template>
    </Column>
    <Column :header="t('server.version')">
      <template #body="{ data: s }">
        <span v-if="s.panel_version" class="app-ltr">{{ s.panel_version }}</span>
        <span v-else class="app-muted">—</span>
        <div v-if="s.version_untested">
          <Tag :value="t('server.untested')" severity="warn" v-tooltip.top="untestedText" :aria-label="untestedText" class="untested" />
        </div>
      </template>
    </Column>
    <Column :header="t('server.clients')">
      <template #body="{ data: s }"><span class="clients">{{ num(s.clients, lang) }}</span></template>
    </Column>
    <Column :header="t('server.weight')">
      <template #body="{ data: s }">{{ num(s.weight, lang) }}</template>
    </Column>
    <Column :header="t('server.plans')">
      <template #body="{ data: s }">
        <div v-if="s.plans?.length" class="plans">
          <Tag v-for="p in s.plans" :key="p.id" :value="planName(p.name, lang)" severity="secondary" />
        </div>
        <span v-else class="app-muted small">{{ t('server.plans_any') }}</span>
      </template>
    </Column>
    <Column body-class="app-nowrap">
      <template #body="{ data: s }">
        <Button icon="pi pi-list" text rounded :aria-label="t('server.inbounds')" v-tooltip.top="t('server.inbounds')" class="inbounds" @click="showInbounds(s)" />
        <Button icon="pi pi-pencil" text rounded :aria-label="t('common.edit')" class="edit" @click="edit(s)" />
        <span v-tooltip.top="s.clients > 0 ? t('server.in_use') : undefined" class="delete-wrap">
          <Button
            icon="pi pi-trash"
            text
            rounded
            severity="danger"
            :disabled="s.clients > 0"
            :aria-label="t('server.delete')"
            class="delete"
            @click="remove(s)"
          />
        </span>
      </template>
    </Column>
  </DataTable>

  <ServerDialog v-model:visible="editing" :server="current" @saved="saved" />
  <InboundsDrawer v-model:visible="inbounds" :server="inboundsOf" />
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
.small {
  font-size: 0.8rem;
}
.url,
.detail {
  overflow-wrap: anywhere;
  max-inline-size: 22rem;
}
.tags,
.plans {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
  margin-block-start: 0.2rem;
}
.untested {
  margin-block-start: 0.2rem;
}
.delete-wrap {
  display: inline-block;
}
</style>
