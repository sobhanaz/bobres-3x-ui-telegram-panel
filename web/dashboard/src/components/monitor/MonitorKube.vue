<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Card from 'primevue/card'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import Tag from 'primevue/tag'
import type { Overview } from '../../monitor'
import { useFmt } from './feed'

// Kubernetes pods, only where the host collector found a cluster (kubectl or k3s on the server). BOBRES
// itself does not run on Kubernetes: a store without a cluster is the usual case, so "not detected" is a
// plain note, never an error.
const props = defineProps<{ overview: Overview | null }>()
const { t } = useI18n()
const { n } = useFmt()

const kube = computed(() => props.overview?.kubernetes)
const phaseSeverity = (phase: string) => (phase === 'Running' || phase === 'Succeeded' ? 'success' : phase === 'Pending' ? 'warn' : phase === 'Failed' ? 'danger' : 'secondary')
</script>

<template>
  <section id="monitor-kubernetes" class="mon-section" tabindex="-1" aria-labelledby="monitor-kubernetes-h">
    <h2 id="monitor-kubernetes-h" class="mon-h">{{ t('monitor.section.kubernetes') }}</h2>

    <p v-if="overview && !kube?.detected" class="app-muted not-detected">
      <i class="pi pi-info-circle" aria-hidden="true" />
      {{ t('monitor.kube.none') }}
    </p>

    <template v-else-if="kube">
      <Card class="nodes-card">
        <template #title>{{ t('monitor.kube.nodes') }}</template>
        <template #content>
          <p v-if="!kube.nodes.length" class="app-muted">{{ t('monitor.kube.no_nodes') }}</p>
          <DataTable v-else :value="kube.nodes" data-key="name" size="small" scrollable class="nodes">
            <Column :header="t('monitor.kube.col_name')"><template #body="{ data }"><bdi class="app-ltr">{{ data.name }}</bdi></template></Column>
            <Column :header="t('monitor.kube.col_ready')">
              <template #body="{ data }">
                <Tag :severity="data.ready ? 'success' : 'danger'" :icon="data.ready ? 'pi pi-check-circle' : 'pi pi-times-circle'" :value="t(data.ready ? 'monitor.kube.ready' : 'monitor.kube.not_ready')" />
              </template>
            </Column>
            <Column :header="t('monitor.kube.col_roles')"><template #body="{ data }"><span class="app-ltr">{{ data.roles || '—' }}</span></template></Column>
            <Column :header="t('monitor.kube.col_version')"><template #body="{ data }"><span class="app-ltr">{{ data.version || '—' }}</span></template></Column>
          </DataTable>
        </template>
      </Card>

      <Card class="pods-card">
        <template #title>{{ t('monitor.kube.pods', { n: n(kube.pods.length) }, kube.pods.length) }}</template>
        <template #content>
          <p v-if="!kube.pods.length" class="app-muted">{{ t('monitor.kube.no_pods') }}</p>
          <DataTable v-else :value="kube.pods" data-key="name" size="small" scrollable paginator :rows="10" class="pods">
            <Column :header="t('monitor.kube.col_namespace')"><template #body="{ data }"><span class="app-ltr">{{ data.namespace }}</span></template></Column>
            <Column :header="t('monitor.kube.col_name')"><template #body="{ data }"><bdi class="app-ltr">{{ data.name }}</bdi></template></Column>
            <Column :header="t('monitor.kube.col_phase')">
              <template #body="{ data }">
                <Tag :severity="phaseSeverity(data.phase)" :value="data.phase || t('monitor.unknown_value')" class="app-ltr" />
                <Tag v-if="!data.ready && data.phase === 'Running'" severity="warn" :value="t('monitor.kube.not_ready')" class="not-ready" />
              </template>
            </Column>
            <Column :header="t('monitor.kube.col_restarts')"><template #body="{ data }">{{ n(data.restarts) }}</template></Column>
            <Column :header="t('monitor.kube.col_node')"><template #body="{ data }"><span class="app-ltr">{{ data.node || '—' }}</span></template></Column>
          </DataTable>
          <p v-if="kube.truncated" class="app-muted truncated">{{ t('monitor.kube.truncated') }}</p>
        </template>
      </Card>
    </template>
  </section>
</template>

<style scoped>
.mon-section {
  display: flex;
  flex-direction: column;
  gap: var(--app-gap);
  min-inline-size: 0;
}
.mon-h {
  margin: 0;
  font-size: 1.1rem;
}
.not-ready {
  margin-inline-start: 0.3rem;
}
</style>
