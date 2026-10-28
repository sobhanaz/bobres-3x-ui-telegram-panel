<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, provide, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Message from 'primevue/message'
import SelectButton from 'primevue/selectbutton'
import ToggleSwitch from 'primevue/toggleswitch'
import { api, ApiError, query } from '../../api'
import { errorText } from '../../errors'
import {
  batches,
  gridTimes,
  pointStamp,
  RANGE_STEP,
  RANGES,
  rangeOf,
  REFRESH_MS,
  SERIES_EVERY_S,
  tickLabel,
  tidy,
  tidyUptime,
  UPTIME_EVERY_S,
  wantedMetrics,
  type EventLevel,
  type EventsReply,
  type MetricInfo,
  type Overview,
  type Range,
  type SeriesAnswer,
  type UptimeReply,
} from '../../monitor'
import { feedKey, useFmt, type Grid } from '../monitor/feed'
import MonitorCicd from '../monitor/MonitorCicd.vue'
import MonitorDeployments from '../monitor/MonitorDeployments.vue'
import MonitorDocker from '../monitor/MonitorDocker.vue'
import MonitorEvents from '../monitor/MonitorEvents.vue'
import MonitorHost from '../monitor/MonitorHost.vue'
import MonitorKube from '../monitor/MonitorKube.vue'
import MonitorServices from '../monitor/MonitorServices.vue'
import MonitorStatus from '../monitor/MonitorStatus.vue'
import MonitorStore from '../monitor/MonitorStore.vue'
import MonitorUptime from '../monitor/MonitorUptime.vue'

// The Monitoring tab (owner and admin, read only): one overview of the whole store, then the host, Docker,
// Kubernetes, the services, how available the services and panel servers have been, CI/CD, the deployments and
// the alerts and logs. It asks core for the overview and the events every 20 seconds while it is shown and the
// browser tab is visible, for the series of the chosen range when a new point can have appeared, and for the
// uptime once a minute. Nothing here changes anything on the server.
const props = defineProps<{ active: boolean }>()
const { t } = useI18n()
const { time, lang, cal } = useFmt()

const RANGE_KEY = 'bobres.monitor.range'
function savedRange(): Range {
  try {
    return rangeOf(localStorage.getItem(RANGE_KEY))
  } catch {
    return '1h'
  }
}
const range = ref<Range>(savedRange())
const auto = ref(true)
const level = ref<EventLevel>('warn')

const overview = ref<Overview | null>(null)
const failure = ref<{ text: string; detail: string; code: string } | null>(null)
const events = ref<EventsReply | null>(null)
const eventsFailed = ref(false)
const answers = shallowRef<Record<string, SeriesAnswer>>({})
const available = shallowRef<Set<string>>(new Set())
const seriesRange = ref<Range | null>(null)
const seriesLoading = ref(false)
const seriesFailed = ref(false)
const refreshing = ref(false)
const uptime = ref<UptimeReply | null>(null)
const uptimeFailed = ref(false)

let lastOkAt = 0
let seriesAt = 0
let uptimeAt = 0
let oseq = 0
let eseq = 0
let sseq = 0
let useq = 0
let inflight: Promise<void> | null = null

async function loadOverview() {
  const n = ++oseq
  try {
    const o = tidy(await api<Overview>('GET', '/monitor/overview'))
    if (n !== oseq) return
    overview.value = o
    failure.value = null
    lastOkAt = Date.now()
  } catch (e) {
    if (n !== oseq) return
    const err = errorText(e, t)
    failure.value = { text: err.text, detail: err.detail, code: e instanceof ApiError ? e.code : '' }
  }
}

async function loadEvents() {
  const n = ++eseq
  try {
    const r = await api<EventsReply>('GET', '/monitor/events' + query({ level: level.value, limit: 100 }))
    if (n !== eseq) return
    events.value = { items: r.items ?? [], logs_visible: r.logs_visible === true, unreachable: r.unreachable ?? [], truncated: r.truncated === true }
    eventsFailed.value = false
  } catch {
    if (n === eseq) eventsFailed.value = true
  }
}

// The uptime has an answer of its own: the section stays when the overview cannot be read.
async function loadUptime() {
  const n = ++useq
  try {
    const u = tidyUptime(await api<UptimeReply>('GET', '/monitor/uptime'))
    if (n !== useq) return
    uptime.value = u
    uptimeFailed.value = false
    uptimeAt = Date.now()
  } catch {
    if (n === useq) uptimeFailed.value = true
  }
}
const uptimeDue = () => !uptime.value || Date.now() - uptimeAt >= UPTIME_EVERY_S * 1000

// The series: first which metrics have data (so a request names only those), then them, a dozen at a time.
async function loadSeries() {
  const n = ++sseq
  const asked = range.value
  seriesLoading.value = true
  try {
    const m = await api<{ metrics: MetricInfo[] }>('GET', '/monitor/metrics')
    const names = new Set((m.metrics ?? []).map((x) => x.metric))
    const parts = await Promise.all(
      batches(wantedMetrics(names)).map((b) => api<{ series: SeriesAnswer[] }>('GET', '/monitor/series' + query({ metrics: b.join(','), range: asked }))),
    )
    if (n !== sseq) return
    const next: Record<string, SeriesAnswer> = {}
    for (const p of parts) for (const s of p.series ?? []) next[s.metric] = { ...s, points: s.points ?? [] }
    available.value = names
    answers.value = next
    seriesRange.value = asked
    seriesFailed.value = false
    seriesAt = Date.now()
  } catch {
    if (n === sseq) seriesFailed.value = true
  } finally {
    if (n === sseq) seriesLoading.value = false
  }
}

const seriesDue = () => seriesRange.value !== range.value || Date.now() - seriesAt >= SERIES_EVERY_S[range.value] * 1000

/** refresh asks for everything that is due; a refresh already on its way is waited for, not repeated */
function refresh(): Promise<void> {
  if (inflight) return inflight
  inflight = (async () => {
    refreshing.value = true
    try {
      await Promise.all([loadOverview(), loadEvents(), uptimeDue() ? loadUptime() : Promise.resolve()])
      if (seriesDue()) await loadSeries()
    } finally {
      refreshing.value = false
    }
  })().finally(() => {
    inflight = null
  })
  return inflight
}
async function refreshNow() {
  seriesAt = 0
  uptimeAt = 0
  await refresh()
  schedule()
}

// ---- when to ask: only while the tab is shown and the browser tab is in front
const pageVisible = ref(typeof document === 'undefined' || document.visibilityState !== 'hidden')
const onVisibility = () => (pageVisible.value = document.visibilityState !== 'hidden')
const running = computed(() => props.active && auto.value && pageVisible.value)
let timer: ReturnType<typeof setTimeout> | undefined
const stale = () => !overview.value || Date.now() - lastOkAt >= REFRESH_MS
function stop() {
  clearTimeout(timer)
  timer = undefined
}
function schedule() {
  stop()
  if (running.value) timer = setTimeout(() => void cycle(), REFRESH_MS)
}
async function cycle() {
  await refresh()
  schedule()
}
watch(running, (on) => {
  if (!on) stop()
  else if (stale()) void cycle()
  else schedule()
})
watch(
  () => props.active,
  (a) => {
    if (a && !running.value && stale()) void refresh()
  },
)
onMounted(() => {
  document.addEventListener('visibilitychange', onVisibility)
  void (running.value ? cycle() : refresh())
})
onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', onVisibility)
  stop()
})

watch(range, (r) => {
  try {
    localStorage.setItem(RANGE_KEY, r)
  } catch {
    /* not remembered: fine */
  }
  void loadSeries()
})
watch(level, () => void loadEvents())

// ---- what the charts read
const grid = computed<Grid>(() => {
  const r = seriesRange.value ?? range.value
  const first = Object.values(answers.value)[0]
  const step = first && first.step_s > 0 ? first.step_s : RANGE_STEP[r]
  let now = overview.value?.generated_at || Math.floor(Date.now() / 1000)
  for (const s of Object.values(answers.value)) {
    const last = s.points[s.points.length - 1]
    if (last && last[0] > now) now = last[0]
  }
  const times = gridTimes(r, now, step)
  return {
    range: r,
    step,
    times,
    categories: times.map((u) => pointStamp(u, lang.value, cal.value)),
    ticks: times.map((u) => tickLabel(u, r, lang.value, cal.value)),
  }
})
provide(feedKey, { grid, answers, available, loading: seriesLoading })

const rangeOptions = computed(() => RANGES.map((r) => ({ value: r, label: t('monitor.range.' + r) })))

const SECTIONS = ['status', 'store', 'host', 'docker', 'kubernetes', 'services', 'uptime', 'cicd', 'deployments', 'events'] as const
// When the overview cannot be read at all, what is drawn from it is left out (an empty "loading" would be a lie);
// the charts, the uptime and the events have answers of their own and stay.
const readable = computed(() => !!overview.value || !failure.value)
const shown = computed(() => (readable.value ? [...SECTIONS] : SECTIONS.filter((s) => s === 'store' || s === 'uptime' || s === 'events')))
function jump(id: string) {
  const el = document.getElementById('monitor-' + id)
  if (!el) return
  const calm = typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  el.scrollIntoView?.({ behavior: calm ? 'auto' : 'smooth', block: 'start' })
  el.focus({ preventScroll: true })
}
const updated = computed(() => (overview.value?.generated_at ? time(overview.value.generated_at) : ''))
const notConfigured = computed(() => failure.value?.code === 'not_configured')
</script>

<template>
  <div class="monitoring">
    <div class="toolbar">
      <SelectButton
        v-model="range"
        :options="rangeOptions"
        option-label="label"
        option-value="value"
        :allow-empty="false"
        :aria-label="t('monitor.range_label')"
        class="range-switch"
      />
      <span class="spacer" />
      <span class="auto">
        <ToggleSwitch v-model="auto" input-id="monitor-auto" class="auto-switch" />
        <label for="monitor-auto">{{ t('monitor.auto') }}</label>
      </span>
      <Button :label="t('system.refresh')" icon="pi pi-refresh" size="small" severity="secondary" outlined :loading="refreshing" class="refresh" @click="refreshNow" />
    </div>
    <p v-if="updated" class="app-muted updated">{{ t('monitor.updated', { time: updated }) }}</p>

    <Message v-if="notConfigured" severity="info" icon="pi pi-info-circle" :closable="false" class="not-configured">{{ t('monitor.not_configured') }}</Message>
    <Message v-else-if="failure" severity="error" :closable="false" class="load-error">
      {{ t('monitor.load_error') }} {{ failure.text }}
      <span v-if="overview"> {{ t('monitor.showing_old', { time: updated }) }}</span>
      <div v-if="failure.detail" class="app-ltr detail">{{ failure.detail }}</div>
    </Message>
    <Message v-if="seriesFailed && !notConfigured" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="series-failed">{{ t('monitor.series_error') }}</Message>

    <nav v-if="!notConfigured" class="jump" :aria-label="t('monitor.jump')">
      <Button v-for="s in shown" :key="s" :label="t('monitor.section.' + s)" text size="small" :class="'jump-' + s" @click="jump(s)" />
    </nav>

    <template v-if="!notConfigured">
      <MonitorStatus v-if="readable" :overview="overview" @jump="jump" />
      <MonitorStore />
      <template v-if="readable">
        <MonitorHost :overview="overview" />
        <MonitorDocker :overview="overview" />
        <MonitorKube :overview="overview" />
        <MonitorServices :overview="overview" />
      </template>
      <MonitorUptime :uptime="uptime" :failed="uptimeFailed" />
      <template v-if="readable">
        <MonitorCicd :overview="overview" />
        <MonitorDeployments :overview="overview" />
      </template>
      <MonitorEvents :events="events" :failed="eventsFailed" :level="level" @update:level="level = $event" />
    </template>
  </div>
</template>

<style scoped>
.monitoring {
  display: flex;
  flex-direction: column;
  gap: calc(var(--app-gap) * 1.5);
  min-inline-size: 0;
}
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
  margin-block-end: calc(var(--app-gap) * -0.5);
}
.spacer {
  flex: 1;
}
.range-switch {
  flex-wrap: wrap;
}
.auto {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
}
.updated {
  margin: calc(var(--app-gap) * -1) 0 0;
  font-size: 0.85rem;
}
.jump {
  display: flex;
  flex-wrap: wrap;
  gap: 0.25rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
</style>
