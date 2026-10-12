<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import { api } from '../../api'
import { errorText } from '../../errors'
import { dateTime, isolate, num, type Lang } from '../../format'
import { overviewGatewaySeverity, overviewGatewayState } from '../../gateways'
import { healthSeverity } from '../../servers'
import { absent, checkList, mismatched, serviceSeverity, serviceState, sortedServices } from '../../system'
import { useAuth } from '../../stores/auth'
import type { OverviewServer, ServiceHealth, SystemReply } from '../../types'

// Every service's health and version as core sees them (it asks each one in
// parallel, 3 s at most), with the 3x-ui servers and payment methods the
// overview also shows.
const { t, te, locale } = useI18n()
const auth = useAuth()
const lang = computed(() => locale.value as Lang)

const reply = ref<SystemReply | null>(null)
const loading = ref(true)
const loadError = ref('')
const loadDetail = ref('')
async function load() {
  loading.value = true
  loadError.value = ''
  loadDetail.value = ''
  try {
    reply.value = await api<SystemReply>('GET', '/system')
  } catch (e) {
    const err = errorText(e, t)
    loadError.value = t('system.load_error') + ' ' + err.text
    loadDetail.value = err.detail
  } finally {
    loading.value = false
  }
}
onMounted(load)

const services = computed(() => sortedServices(reply.value?.services ?? []))
const others = computed(() => (reply.value ? mismatched(reply.value) : []))
const otherNames = computed(() => new Set(others.value.map((s) => s.name)))
const label = (prefix: string, key: string) => (te(prefix + key) ? t(prefix + key) : key)
const serviceName = (s: ServiceHealth) => label('system.service.', s.name)
const sep = computed(() => (lang.value === 'fa' ? '، ' : ', '))
const mismatchText = computed(() =>
  t('system.version_mismatch', {
    version: isolate(reply.value?.version || t('app.unknown')),
    list: others.value.map((s) => `${serviceName(s)} ${isolate(s.version || t('app.unknown'))}`).join(sep.value),
  }),
)

// A server switched off is not checked, so its old result does not count.
const servers = computed(() => reply.value?.servers ?? null)
const serverHealth = (s: OverviewServer) => (s.enabled === false ? 'off' : s.healthy ? 'ok' : 'down')
const gateways = computed(() => reply.value?.gateways ?? null)
</script>

<template>
  <div class="health">
    <div class="toolbar">
      <span v-if="reply?.checked_at" class="app-muted checked">{{ t('system.checked_at', { time: dateTime(reply.checked_at, lang) }) }}</span>
      <span class="spacer" />
      <Button :label="t('system.refresh')" icon="pi pi-refresh" size="small" severity="secondary" outlined :loading="loading" class="refresh" @click="load" />
    </div>

    <Message v-if="loadError" severity="error" :closable="false" class="banner load-error">
      {{ loadError }}
      <div v-if="loadDetail" class="app-ltr detail">{{ loadDetail }}</div>
    </Message>
    <Message v-if="others.length" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="banner mismatch-banner">
      {{ mismatchText }}
    </Message>

    <Card class="services-card">
      <template #title>{{ t('system.services') }}</template>
      <template #content>
        <DataTable :value="services" :loading="loading && !reply" data-key="name" size="small" scrollable class="services-health">
          <Column :header="t('system.col_service')">
            <template #body="{ data }">
              <span class="service-name">{{ serviceName(data) }}</span>
            </template>
          </Column>
          <Column :header="t('system.col_version')">
            <template #body="{ data }">
              <span class="version app-ltr">{{ data.version || '—' }}</span>
              <Tag v-if="otherNames.has(data.name)" severity="warn" :value="t('system.mismatch')" class="mismatch" />
            </template>
          </Column>
          <Column :header="t('system.col_state')">
            <template #body="{ data }">
              <Tag :severity="serviceSeverity[serviceState(data)]" :value="t('system.state.' + serviceState(data))" class="service-state" />
            </template>
          </Column>
          <Column :header="t('system.col_checks')">
            <template #body="{ data }">
              <div v-if="!absent(data)" class="checks">
                <Tag
                  v-for="c in checkList(data)"
                  :key="c.name"
                  :severity="c.ok ? 'success' : 'danger'"
                  :icon="c.ok ? 'pi pi-check' : 'pi pi-times'"
                  :value="label('system.check.', c.name)"
                  :aria-label="t(c.ok ? 'system.check_ok' : 'system.check_fail', { name: label('system.check.', c.name) })"
                  :class="['check', c.ok ? 'pass' : 'fail']"
                />
              </div>
            </template>
          </Column>
          <Column :header="t('system.col_detail')">
            <template #body="{ data }">
              <span v-if="absent(data)" class="app-muted">{{ t('system.' + serviceState(data)) }}</span>
              <span v-else-if="data.detail" class="app-muted app-ltr error">{{ data.detail }}</span>
            </template>
          </Column>
        </DataTable>
      </template>
    </Card>

    <div v-if="servers || gateways" class="cards">
      <Card v-if="servers" class="servers-summary">
        <template #title>{{ t('system.servers') }}</template>
        <template #content>
          <p v-if="!servers.length" class="app-muted">{{ t('overview.servers_none') }}</p>
          <div v-for="s in servers" :key="s.id" class="row server">
            <div class="row-main">
              <bdi>{{ s.name }}</bdi>
              <span class="app-muted clients">{{ t('server.clients') }}: {{ num(s.clients, lang) }}</span>
              <Tag :severity="healthSeverity[serverHealth(s)]" :value="t('server.health_state.' + serverHealth(s))" class="server-health" />
            </div>
            <div v-if="serverHealth(s) === 'down' && s.detail" class="app-muted app-ltr detail">{{ s.detail }}</div>
          </div>
          <RouterLink v-if="auth.can('servers.write')" :to="{ name: 'servers' }" class="manage servers-link">{{ t('system.servers_manage') }}</RouterLink>
        </template>
      </Card>
      <Card v-if="gateways" class="gateways-summary">
        <template #title>{{ t('system.gateways') }}</template>
        <template #content>
          <p v-if="!gateways.length" class="app-muted">{{ t('system.gateways_none') }}</p>
          <div v-for="g in gateways" :key="g.provider" class="row gateway">
            <div class="row-main">
              <span>{{ label('payment.provider.', g.provider) }}</span>
              <Tag :severity="overviewGatewaySeverity[overviewGatewayState(g)]" :value="t('overview.gateway_state.' + overviewGatewayState(g))" class="gateway-state" />
            </div>
          </div>
          <RouterLink v-if="auth.can('gateways.write')" :to="{ name: 'gateways' }" class="manage gateways-link">{{ t('system.gateways_manage') }}</RouterLink>
        </template>
      </Card>
    </div>
  </div>
</template>

<style scoped>
.health {
  display: flex;
  flex-direction: column;
  gap: var(--app-gap);
}
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
}
.spacer {
  flex: 1;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
.service-name {
  font-weight: 500;
}
.version {
  display: inline-block;
  margin-inline-end: 0.4rem;
}
.checks {
  display: flex;
  flex-wrap: wrap;
  gap: 0.3rem;
}
.error {
  font-size: 0.85rem;
  overflow-wrap: anywhere;
}
.cards {
  display: grid;
  gap: var(--app-gap);
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 20rem), 1fr));
}
.row + .row {
  border-block-start: 1px solid var(--p-content-border-color);
}
.row {
  padding-block: 0.5rem;
}
.row-main {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
}
.row-main > :last-child {
  margin-inline-start: auto;
}
.clients {
  font-size: 0.85rem;
}
.manage {
  display: inline-block;
  margin-block-start: 0.75rem;
}
</style>
