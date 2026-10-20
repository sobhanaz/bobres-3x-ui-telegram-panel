<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import { dateTime, isolate, type Lang } from '../../format'
import { isFinal, isPrerelease, stepList, type RunPhase, type StepState, type UpdateRunView } from '../../updates'

// Where an update is: asked for, being made step by step, or over, with what the
// server's updater says (one sentence and, for the owner, the newest lines of
// its log). Everything the updater says is shown as text, never as markup. The
// update restarts core itself, so while the page cannot reach it, it says so and
// keeps trying.
const props = defineProps<{
  view: UpdateRunView
  /** core does not answer (it restarts in the middle of the update) */
  reconnecting: boolean
  /** a new dashboard is ready: core runs another version than this page was loaded from */
  newer: boolean
  /** the version core runs now */
  version: string
}>()
const emit = defineEmits<{ reload: []; dismiss: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
// the arrow from the old version to the new one points the way the line reads
const arrowIcon = computed(() => (locale.value === 'fa' ? 'pi-arrow-left' : 'pi-arrow-right'))

const open = ref(false)
const phase = computed<RunPhase>(() => props.view.phase)
const final = computed(() => isFinal(phase.value))
const steps = computed(() => (phase.value === 'not_started' ? [] : stepList(props.view)))
// the title names the release it goes to; an update started on the server may not say
const title = computed(() => {
  if (!props.view.mine) return t('system.updates.progress.title.elsewhere')
  return t('system.updates.progress.title.' + phase.value, { to: isolate(props.view.to || '?') })
})
const hint = computed(() => {
  switch (phase.value) {
    case 'queued':
      return t('system.updates.progress.queued')
    case 'running':
      return props.view.mine ? t('system.updates.progress.running') : t('system.updates.progress.elsewhere')
    case 'done':
      return t('system.updates.progress.done', { version: isolate(props.view.to || props.version) })
    case 'rolled_back':
      return t('system.updates.progress.rolled_back', { from: isolate(props.view.from || '?') })
    case 'failed':
      return t('system.updates.progress.failed')
    default:
      return t('system.updates.progress.not_started')
  }
})
const severity = computed<'info' | 'success' | 'warn' | 'error'>(() => {
  switch (phase.value) {
    case 'done':
      return 'success'
    case 'failed':
      return 'error'
    case 'rolled_back':
    case 'not_started':
      return 'warn'
    default:
      return 'info'
  }
})
const icon = computed(() => {
  switch (phase.value) {
    case 'done':
      return 'pi pi-check-circle'
    case 'failed':
      return 'pi pi-times-circle'
    case 'rolled_back':
    case 'not_started':
      return 'pi pi-exclamation-triangle'
    case 'queued':
      return 'pi pi-clock'
    default:
      return 'pi pi-spin pi-spinner'
  }
})
const stepIcon: Record<StepState, string> = {
  done: 'pi pi-check',
  active: 'pi pi-spin pi-spinner',
  pending: 'pi pi-circle',
  failed: 'pi pi-times',
}
const asked = computed(() =>
  props.view.by && props.view.requestedAt
    ? t('system.updates.progress.asked_by', { by: isolate(props.view.by), time: dateTime(props.view.requestedAt, lang.value) })
    : '',
)
// the reload is offered when the update is over, or when core already runs another version than this page
const canReload = computed(() => props.newer || phase.value === 'done')
</script>

<template>
  <Card class="update-progress" :class="'phase-' + phase" role="status" aria-live="polite">
    <template #title>
      <div class="card-title">
        <i :class="icon" aria-hidden="true" />
        <span class="progress-title">{{ title }}</span>
      </div>
    </template>
    <template #content>
      <div v-if="view.from || view.to" class="versions">
        <span class="app-ltr from">{{ view.from || '?' }}</span>
        <i :class="['pi', arrowIcon, 'arrow']" aria-hidden="true" />
        <span class="app-ltr to">{{ view.to || '?' }}</span>
        <Tag v-if="view.channel" :severity="view.channel === 'beta' ? 'warn' : 'secondary'" :value="t('system.updates.channel_name.' + view.channel)" class="channel-tag" />
        <Tag v-if="isPrerelease(view.to)" severity="warn" :value="t('system.updates.prerelease')" class="prerelease-tag" />
      </div>
      <p v-if="asked" class="app-muted asked">{{ asked }}</p>

      <Message v-if="reconnecting" severity="info" icon="pi pi-spin pi-spinner" :closable="false" class="reconnecting">
        {{ t('system.updates.progress.reconnecting') }}
      </Message>

      <Message :severity="severity" :icon="icon" :closable="false" class="hint-box">
        <span class="hint">{{ hint }}</span>
      </Message>

      <ol v-if="steps.length" class="steps" :aria-label="t('system.updates.progress.steps')">
        <li v-for="s in steps" :key="s.key" class="step" :class="s.state">
          <i :class="stepIcon[s.state]" aria-hidden="true" />
          <span>{{ t('system.updates.progress.step.' + s.key) }}</span>
        </li>
      </ol>

      <!-- the updater's own sentence, English, whatever the page's language -->
      <p v-if="view.message" class="updater-message app-ltr">{{ view.message }}</p>

      <div v-if="view.log.length" class="log-box">
        <Button
          type="button"
          text
          size="small"
          :icon="open ? 'pi pi-chevron-up' : 'pi pi-chevron-down'"
          :label="t('system.updates.progress.details')"
          :aria-expanded="open"
          class="log-toggle"
          @click="open = !open"
        />
        <template v-if="open">
          <div class="app-muted log-title">{{ t('system.updates.progress.log') }}</div>
          <pre class="app-ltr app-mono log">{{ view.log.join('\n') }}</pre>
        </template>
      </div>

      <div v-if="canReload || final" class="actions">
        <Button v-if="canReload" :label="t('system.updates.progress.reload')" icon="pi pi-refresh" class="reload" @click="emit('reload')" />
        <Button v-if="final" :label="t('system.updates.progress.dismiss')" severity="secondary" outlined class="dismiss" @click="emit('dismiss')" />
      </div>
    </template>
  </Card>
</template>

<style scoped>
.card-title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
}
.versions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  font-weight: 600;
  font-size: 1.05rem;
}
.arrow {
  font-size: 0.85rem;
  color: var(--p-text-muted-color);
}
.asked {
  margin-block: 0.4rem 0;
  font-size: 0.9rem;
}
.reconnecting,
.hint-box {
  margin-block-start: 0.75rem;
}
.steps {
  list-style: none;
  margin: 0.9rem 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
}
.step {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  color: var(--p-text-muted-color);
}
.step.active {
  color: var(--p-text-color);
  font-weight: 600;
}
.step.done .pi {
  color: var(--p-green-500);
}
.step.failed {
  color: var(--p-red-500);
  font-weight: 600;
}
.step .pi {
  inline-size: 1rem;
  text-align: center;
}
.updater-message {
  margin-block: 0.75rem 0;
  font-size: 0.9rem;
  overflow-wrap: anywhere;
}
.log-box {
  margin-block-start: 0.5rem;
}
.log-title {
  font-size: 0.85rem;
  margin-block: 0.4rem 0.25rem;
}
.log {
  margin: 0;
  padding: 0.6rem 0.75rem;
  max-block-size: 16rem;
  overflow: auto;
  font-size: 0.8rem;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  border-radius: 0.375rem;
  background: var(--p-content-background);
  border: 1px solid var(--p-content-border-color);
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.6rem;
  margin-block-start: 1rem;
}
</style>
