<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Skeleton from 'primevue/skeleton'
import { byKind, collectorState, healthOf, overallIcon, overallOf, overallSeverity, problems, type MonitorComponent, type Overview } from '../../monitor'
import StateTag from './StateTag.vue'
import { useFmt } from './feed'

// ONE overview of the whole store: is it fine, working but in need of a look, or down, and why, in plain
// words; then every part it checks (the services, the data stores, the server, the panels, the payment
// methods, the jobs) and the store's own figures.
const props = defineProps<{ overview: Overview | null }>()
const emit = defineEmits<{ jump: [id: string] }>()
const { t, te } = useI18n()
const { n } = useFmt()

const overall = computed(() => overallOf(props.overview?.status ?? ''))
const reasons = computed(() => (props.overview ? problems(props.overview) : []))
const groups = computed(() => (props.overview ? byKind(props.overview.components) : []))
const open = computed(() => props.overview?.counters.open_alerts ?? 0)
const noHost = computed(() => !!props.overview && collectorState(props.overview) !== 'ready')

function nameOf(c: MonitorComponent): string {
  if (te('monitor.component.' + c.id)) return t('monitor.component.' + c.id)
  if (c.kind === 'gateway') {
    const provider = c.id.replace(/^gateway\./, '')
    if (te('payment.provider.' + provider)) return t('payment.provider.' + provider)
  }
  return c.name || c.id
}

const TILES = [
  ['open_alerts', 'pi pi-bell'],
  ['errors_1h', 'pi pi-times-circle'],
  ['orders_pending', 'pi pi-shopping-cart'],
  ['payments_pending', 'pi pi-credit-card'],
  ['provision_queue', 'pi pi-cog'],
  ['outbox_backlog', 'pi pi-inbox'],
  ['tickets_open', 'pi pi-comments'],
  ['services_active', 'pi pi-server'],
  ['customers_total', 'pi pi-users'],
] as const
const tiles = computed(() => TILES.map(([key, icon]) => ({ key, icon, value: props.overview?.counters[key] ?? null })))
// the figures that mean a look is needed when above zero
const WATCH = new Set(['open_alerts', 'errors_1h'])
</script>

<template>
  <section id="monitor-status" class="mon-section" tabindex="-1" aria-labelledby="monitor-status-h">
    <h2 id="monitor-status-h" class="mon-h">{{ t('monitor.section.status') }}</h2>

    <Skeleton v-if="!overview" height="8rem" class="verdict-loading" />
    <template v-else>
      <Card :class="['verdict', 'overall-' + overall]">
        <template #content>
          <div class="verdict-head" role="status">
            <i :class="['pi', overallIcon[overall], 'verdict-icon', overallSeverity[overall]]" aria-hidden="true" />
            <div class="verdict-text">
              <div class="verdict-title">{{ t('monitor.overall.' + overall) }}</div>
              <p v-if="overall === 'ok'" class="app-muted verdict-note">{{ t('monitor.overall_note.ok', { n: n(overview.components.length) }, overview.components.length) }}</p>
              <p v-else-if="overall === 'unknown'" class="app-muted verdict-note">{{ t('monitor.overall_note.unknown') }}</p>
              <p v-else-if="!reasons.length" class="app-muted verdict-note">{{ t('monitor.overall_note.alert') }}</p>
            </div>
          </div>
          <ul v-if="reasons.length" class="reasons" :aria-label="t('monitor.reasons')">
            <li v-for="c in reasons" :key="c.id" class="reason" :data-id="c.id">
              <StateTag :state="c.status" />
              <bdi class="reason-name">{{ nameOf(c) }}</bdi>
              <span v-if="c.detail" class="app-muted app-ltr reason-detail" dir="ltr">{{ c.detail }}</span>
            </li>
          </ul>
          <p v-if="open > 0 && overall !== 'ok'" class="open-alerts">
            <i class="pi pi-bell" aria-hidden="true" />
            {{ t('monitor.open_alerts', { n: n(open) }, open) }}
            <Button :label="t('monitor.see_alerts')" text size="small" class="see-alerts" @click="emit('jump', 'events')" />
          </p>
          <p v-if="noHost" class="app-muted no-host">
            <i class="pi pi-info-circle" aria-hidden="true" />
            {{ t('monitor.no_host_note') }}
            <Button :label="t('monitor.see_host')" text size="small" class="see-host" @click="emit('jump', 'host')" />
          </p>
        </template>
      </Card>

      <div class="groups">
        <div v-for="g in groups" :key="g.kind" class="group" :data-kind="g.kind">
          <h3 class="group-title">{{ te('monitor.kind.' + g.kind) ? t('monitor.kind.' + g.kind) : g.kind }}</h3>
          <ul class="chips">
            <li v-for="c in g.items" :key="c.id" class="chip" :class="'chip-' + healthOf(c.status)" :data-id="c.id">
              <bdi class="chip-name">{{ nameOf(c) }}</bdi>
              <StateTag :state="c.status" />
            </li>
          </ul>
        </div>
      </div>

      <h3 class="group-title tiles-title">{{ t('monitor.store_heading') }}</h3>
      <dl class="tiles">
        <div v-for="tile in tiles" :key="tile.key" class="tile" :class="{ attention: WATCH.has(tile.key) && (tile.value ?? 0) > 0 }" :data-key="tile.key">
          <dt><i :class="tile.icon" aria-hidden="true" /> {{ t('monitor.counter.' + tile.key) }}</dt>
          <dd>
            <template v-if="tile.value === null"><span aria-hidden="true">—</span><span class="sr-only">{{ t('monitor.unknown_value') }}</span></template>
            <template v-else>{{ n(tile.value) }}</template>
          </dd>
        </div>
      </dl>
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
.verdict-head {
  display: flex;
  align-items: center;
  gap: 0.9rem;
}
.verdict-icon {
  font-size: 2rem;
}
.verdict-icon.success {
  color: var(--p-green-500);
}
.verdict-icon.warn {
  color: var(--p-orange-500);
}
.verdict-icon.danger {
  color: var(--p-red-500);
}
.verdict-icon.secondary {
  color: var(--p-text-muted-color);
}
.verdict-title {
  font-size: 1.15rem;
  font-weight: 700;
}
.verdict-note {
  margin: 0.15rem 0 0;
}
.reasons {
  list-style: none;
  margin: 0.9rem 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.reason {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
.reason-name {
  font-weight: 600;
}
.reason-detail {
  font-size: 0.85rem;
  overflow-wrap: anywhere;
}
.open-alerts,
.no-host {
  margin: 0.7rem 0 0;
}
.groups {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 17rem), 1fr));
  gap: var(--app-gap);
}
.group-title {
  margin: 0 0 0.4rem;
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--p-text-muted-color);
}
.chips {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.chip {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.6rem;
  padding: 0.45rem 0.7rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.6rem;
  background: var(--p-content-background);
  min-inline-size: 0;
}
.chip-name {
  overflow-wrap: anywhere;
}
.chip-warn {
  border-color: var(--p-orange-400);
}
.chip-down {
  border-color: var(--p-red-400);
}
.tiles-title {
  margin-block-end: 0;
}
.tiles {
  margin: 0;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 9.5rem), 1fr));
  gap: 0.6rem;
}
.tile {
  padding: 0.6rem 0.8rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.6rem;
  background: var(--p-content-background);
  min-inline-size: 0;
}
.tile dt {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
}
.tile dd {
  margin: 0.15rem 0 0;
  font-size: 1.4rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.tile.attention dd {
  color: var(--p-orange-600);
}
.sr-only {
  position: absolute;
  inline-size: 1px;
  block-size: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
</style>
