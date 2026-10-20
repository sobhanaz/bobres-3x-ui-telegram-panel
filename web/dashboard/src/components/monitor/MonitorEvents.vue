<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Card from 'primevue/card'
import Message from 'primevue/message'
import SelectButton from 'primevue/selectbutton'
import Skeleton from 'primevue/skeleton'
import Tag from 'primevue/tag'
import { alertStateOf, eventTitle, levelOf, levelSeverity, type EventItem, type EventLevel, type EventsReply, type Words } from '../../monitor'
import { useFmt } from './feed'

// The newest warnings and errors in one list: the watchdog's alerts raised and fixed, the error lines of the
// host's journal and, for the owner only (they carry Telegram ids), the lines the services logged.
const props = defineProps<{ events: EventsReply | null; failed: boolean; level: EventLevel }>()
const emit = defineEmits<{ 'update:level': [level: EventLevel] }>()
const { t, te, tm } = useI18n()
const { at, lang } = useFmt()

const levels = computed(() => (['warn', 'error'] as const).map((v) => ({ value: v, label: t('monitor.events.level.' + v) })))
const words: Words = { t: (key, ...args) => (t as (k: string, ...a: unknown[]) => string)(key, ...args), te, tm }
const items = computed(() => (props.events?.items ?? []).map((e) => ({ e, lvl: levelOf(e), text: eventTitle(e, lang.value, words), state: alertStateOf(e) })))
const sourceName = (e: EventItem) => (te('monitor.source.' + e.source.replace('.', '_')) ? t('monitor.source.' + e.source.replace('.', '_')) : e.source)
const pick = (v: unknown) => {
  if (v === 'warn' || v === 'error') emit('update:level', v)
}
</script>

<template>
  <section id="monitor-events" class="mon-section" tabindex="-1" aria-labelledby="monitor-events-h">
    <h2 id="monitor-events-h" class="mon-h">{{ t('monitor.section.events') }}</h2>

    <div class="toolbar">
      <SelectButton
        :model-value="level"
        :options="levels"
        option-label="label"
        option-value="value"
        :allow-empty="false"
        :aria-label="t('monitor.events.level_label')"
        class="level-filter"
        @update:model-value="pick"
      />
    </div>

    <Message v-if="failed" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="events-failed">{{ t('monitor.events.load_error') }}</Message>
    <Message v-if="events && !events.logs_visible" severity="info" icon="pi pi-lock" :closable="false" class="owner-only">{{ t('monitor.events.owner_only') }}</Message>
    <Message v-if="events && events.unreachable.length" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="unreachable">
      {{ t('monitor.events.unreachable', { list: events.unreachable.map((s) => (te('system.service.' + s) ? t('system.service.' + s) : s)).join(lang === 'fa' ? '، ' : ', ') }) }}
    </Message>

    <Card class="events-card">
      <template #content>
        <Skeleton v-if="!events && !failed" height="6rem" />
        <p v-else-if="events && !items.length" class="all-clear">
          <i class="pi pi-check-circle" aria-hidden="true" />
          {{ t('monitor.events.none') }}
        </p>
        <ol v-else-if="events" class="events" :aria-label="t('monitor.events.list_label')">
          <li v-for="(it, i) in items" :key="i" class="event" :class="['event-' + it.lvl, 'kind-' + it.e.kind]">
            <div class="event-head">
              <Tag :severity="levelSeverity[it.lvl]" :icon="it.lvl === 'error' ? 'pi pi-times-circle' : 'pi pi-exclamation-triangle'" :value="t('monitor.events.level_tag.' + it.lvl)" class="event-level" />
              <Tag severity="secondary" :value="sourceName(it.e)" class="event-source" />
              <Tag v-if="it.state" :severity="it.state === 'open' ? 'danger' : it.state === 'resolved' ? 'success' : 'secondary'" :value="t('monitor.events.state.' + it.state)" class="event-state" />
              <time class="app-muted event-time" :datetime="new Date(it.e.at * 1000).toISOString()">{{ at(it.e.at) }}</time>
            </div>
            <div class="event-title" dir="auto">{{ it.text }}</div>
            <div v-if="it.e.detail" class="app-muted app-mono event-detail" dir="auto">{{ it.e.detail }}</div>
          </li>
        </ol>
        <p v-if="events?.truncated" class="app-muted truncated">{{ t('monitor.events.truncated') }}</p>
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
.toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
}
.level-filter {
  flex-wrap: wrap;
}
.all-clear {
  margin: 0;
  color: var(--p-green-600);
}
.events {
  list-style: none;
  margin: 0;
  padding: 0;
  max-block-size: 70vh;
  overflow: auto;
}
.event {
  padding-block: 0.6rem;
  border-block-end: 1px solid var(--p-content-border-color);
}
.event:last-child {
  border-block-end: 0;
}
.event-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.4rem 0.5rem;
  margin-block-end: 0.25rem;
}
.event-time {
  unicode-bidi: isolate;
  font-size: 0.8rem;
}
.event-title,
.event-detail {
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.event-detail {
  margin-block-start: 0.2rem;
  font-size: 0.8rem;
}
.truncated {
  margin: 0.6rem 0 0;
}
</style>
