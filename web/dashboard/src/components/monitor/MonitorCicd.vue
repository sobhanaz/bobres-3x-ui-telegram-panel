<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Card from 'primevue/card'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import { ciProblem, githubLink, runIcon, runSeverity, runState, shortSha, type CiRun, type Overview } from '../../monitor'
import { useFmt } from './feed'

// CI/CD as the bot reads it from GitHub's public API (core itself has no internet access): the newest runs on
// the main branch, the Release workflow's runs and the newest release, each with a link into the repository. When
// GitHub cannot be reached the page says so and keeps showing what it last knew.
const props = defineProps<{ overview: Overview | null }>()
const { t } = useI18n()
const { at } = useFmt()

const ci = computed(() => props.overview?.ci)
const problem = computed(() => (ci.value ? ciProblem(ci.value) : null))
const release = computed(() => ci.value?.latest_release ?? null)
const known = computed(() => !!ci.value && (ci.value.available || ci.value.runs.length > 0 || ci.value.release_runs.length > 0 || !!ci.value.latest_release))
const resetAt = computed(() => (ci.value && ci.value.rate_reset_at > 0 ? at(ci.value.rate_reset_at) : ''))
const stateWord = (r: CiRun) => t('monitor.ci.run.' + runState(r))
const checkedAt = computed(() => (ci.value && ci.value.checked_at > 0 ? at(ci.value.checked_at) : ''))
const blocks = computed(() => [
  { key: 'runs', title: 'monitor.ci.runs', rows: ci.value?.runs ?? [] },
  { key: 'release_runs', title: 'monitor.ci.release_runs', rows: ci.value?.release_runs ?? [] },
])
</script>

<template>
  <section id="monitor-cicd" class="mon-section" tabindex="-1" aria-labelledby="monitor-cicd-h">
    <h2 id="monitor-cicd-h" class="mon-h">{{ t('monitor.section.cicd') }}</h2>

    <Message v-if="ci && ci.main_failing === true" severity="error" icon="pi pi-times-circle" :closable="false" class="main-failing">
      {{ t('monitor.ci.main_failing') }}
    </Message>
    <Message v-if="problem" :severity="problem === 'not_checked' ? 'info' : 'warn'" :icon="problem === 'not_checked' ? 'pi pi-info-circle' : 'pi pi-exclamation-triangle'" :closable="false" class="ci-problem" :data-problem="problem">
      <div class="problem-text">
        {{ t('monitor.ci.problem.' + problem, { time: resetAt }) }}
        <template v-if="known && problem !== 'not_checked' && checkedAt"> {{ t('monitor.ci.older', { time: checkedAt }) }}</template>
      </div>
      <div v-if="ci?.detail" class="app-muted app-ltr small" dir="ltr">{{ ci.detail }}</div>
    </Message>

    <Card v-if="release" class="release-card">
      <template #title>{{ t('monitor.ci.latest_release') }}</template>
      <template #content>
        <div class="release">
          <a v-if="githubLink(release.url)" :href="githubLink(release.url)" target="_blank" rel="noopener noreferrer" class="app-ltr tag-link">{{ release.tag }}</a>
          <span v-else class="app-ltr tag-link">{{ release.tag }}</span>
          <Tag v-if="release.prerelease" severity="warn" :value="t('monitor.ci.prerelease')" />
          <span class="app-muted">{{ t('monitor.ci.published', { time: at(release.published_at) }) }}</span>
        </div>
      </template>
    </Card>

    <template v-for="block in blocks" :key="block.key">
      <Card :class="'runs-card ' + block.key + '-card'">
        <template #title>{{ t(block.title) }}</template>
        <template #content>
          <p v-if="overview && !block.rows.length" class="app-muted no-runs">{{ t('monitor.ci.no_runs') }}</p>
          <DataTable v-else :value="block.rows" size="small" scrollable :class="'runs-table ' + block.key" :loading="!overview">
            <Column :header="t('monitor.ci.col_workflow')">
              <template #body="{ data }">
                <a v-if="githubLink(data.url)" :href="githubLink(data.url)" target="_blank" rel="noopener noreferrer" class="run-link">{{ data.workflow }}<i class="pi pi-external-link ext" aria-hidden="true" /></a>
                <template v-else>{{ data.workflow }}</template>
              </template>
            </Column>
            <Column :header="t('monitor.ci.col_result')">
              <template #body="{ data }">
                <Tag :severity="runSeverity[runState(data)]" :icon="runIcon[runState(data)]" :value="stateWord(data)" class="run-state" />
              </template>
            </Column>
            <Column :header="t('monitor.ci.col_branch')"><template #body="{ data }"><span class="app-ltr" dir="ltr">{{ data.branch }}</span></template></Column>
            <Column :header="t('monitor.ci.col_event')"><template #body="{ data }"><span class="app-ltr" dir="ltr">{{ data.event }}</span></template></Column>
            <Column :header="t('monitor.ci.col_commit')"><template #body="{ data }"><code class="app-ltr app-mono" dir="ltr">{{ shortSha(data.sha) || '—' }}</code></template></Column>
            <Column :header="t('monitor.ci.col_when')"><template #body="{ data }">{{ at(data.at) }}</template></Column>
          </DataTable>
        </template>
      </Card>
    </template>

    <p v-if="checkedAt" class="app-muted small checked">{{ t('monitor.ci.checked', { time: checkedAt }) }}</p>
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
.release {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
}
.tag-link {
  font-weight: 700;
  font-size: 1.1rem;
}
.ext {
  margin-inline-start: 0.3rem;
  font-size: 0.7rem;
}
.small {
  font-size: 0.8rem;
}
.checked {
  margin: 0;
}
</style>
