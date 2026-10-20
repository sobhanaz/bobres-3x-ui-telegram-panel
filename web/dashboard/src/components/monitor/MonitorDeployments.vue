<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Card from 'primevue/card'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import Tag from 'primevue/tag'
import { outcomeOf, outcomeSeverity, type Overview } from '../../monitor'
import { useFmt } from './feed'

// The updates of the install, newest first: those asked for on the System page (who, the outcome, the updater's
// sentence) and the newest one run by hand on the server with `bobres update`.
const props = defineProps<{ overview: Overview | null }>()
const { t } = useI18n()
const { at, dur } = useFmt()

const rows = computed(() => props.overview?.deployments ?? [])
const took = (d: { at: number; finished_at: number }) => (d.finished_at > d.at ? dur(d.finished_at - d.at) : '')
</script>

<template>
  <section id="monitor-deployments" class="mon-section" tabindex="-1" aria-labelledby="monitor-deployments-h">
    <h2 id="monitor-deployments-h" class="mon-h">{{ t('monitor.section.deployments') }}</h2>
    <Card class="deployments-card">
      <template #content>
        <p v-if="overview && !rows.length" class="app-muted no-deployments">{{ t('monitor.deployments.none') }}</p>
        <DataTable v-else :value="rows" data-key="id" size="small" scrollable class="deployments" :loading="!overview">
          <Column :header="t('monitor.deployments.col_when')">
            <template #body="{ data }">{{ at(data.at) }}<div v-if="took(data)" class="app-muted small">{{ t('monitor.deployments.took', { time: took(data) }) }}</div></template>
          </Column>
          <Column :header="t('monitor.deployments.col_version')">
            <template #body="{ data }">
              <span class="app-ltr versions" dir="ltr">{{ data.from || '?' }} &rarr; {{ data.to || '?' }}</span>
              <div v-if="data.channel" class="app-muted small">{{ t('monitor.deployments.channel', { channel: data.channel }) }}</div>
            </template>
          </Column>
          <Column :header="t('monitor.deployments.col_outcome')">
            <template #body="{ data }">
              <Tag :severity="outcomeSeverity[outcomeOf(data)]" :value="t('monitor.deployments.outcome.' + outcomeOf(data))" class="outcome" />
              <div v-if="data.message" class="app-muted app-ltr small message" dir="ltr">{{ data.message }}</div>
            </template>
          </Column>
          <Column :header="t('monitor.deployments.col_source')">
            <template #body="{ data }">
              {{ t('monitor.deployments.source.' + (data.source === 'dashboard' ? 'dashboard' : 'cli')) }}
              <div v-if="data.by" class="app-muted small"><bdi>{{ data.by }}</bdi></div>
            </template>
          </Column>
        </DataTable>
      </template>
    </Card>
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
.small {
  font-size: 0.8rem;
}
.message {
  overflow-wrap: anywhere;
}
</style>
