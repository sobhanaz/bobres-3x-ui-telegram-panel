<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import SelectButton from 'primevue/selectbutton'
import { BarChart, DonutChart, Heatmap, LineChart, Sparkline } from '../charts'
import { useReducedMotion } from '../charts/useChart'
import { digits, todayIn, weekdayName, type Calendar } from '../../calendar'
import { isolate, localDate, money, moneyCompact, num, percent, planName, type Lang } from '../../format'
import { byCurrency, currenciesOf, periodLabel, share, type ReportMethod, type ReportPlan, type ReportSummary } from '../../reports'
import {
  axisLabel,
  bestBucket,
  busiest,
  currencyKpis,
  hourGrid,
  hourLabel,
  isoToJs,
  methodSlices,
  orderSeries,
  partialFlags,
  peopleKpis,
  peopleSeries,
  planStack,
  previousRange,
  revenueSeries,
  toneOf,
  trendWords,
  type Delta,
  type HoursAnswer,
  type Kpi,
  type PeopleKpi,
  type PlanSeriesAnswer,
} from '../../reportsvisual'

// The report to read, print or show someone: a headline per currency written in
// plain words, the headline numbers with their change on the period before (and a
// sentence for each), the charts, and the busiest hours. Persian first, English
// second. The 3D charts are a different chunk, loaded only when this view shows
// them: on a wide screen, unless the person prefers reduced motion; the flat
// charts are always one switch away, and on a phone they are all there is. Print
// (or save as PDF) uses the paper stylesheet at the end of this file.
const props = defineProps<{
  summary: ReportSummary
  /** the same number of days right before the range (null until it arrives, or when there is none) */
  previous: ReportSummary | null
  previousState: 'idle' | 'loading' | 'ready' | 'none' | 'failed'
  plans: ReportPlan[]
  methods: ReportMethod[]
  planSeries: PlanSeriesAnswer | null
  hours: HoursAnswer | null
  /** the calendar the page is set to: every date here is written in it */
  calendar: Calendar
  brand: string
  loading?: boolean
}>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const IsoColumns = defineAsyncComponent(() => import('../charts3d/IsoColumns.vue'))
const TiltDonut = defineAsyncComponent(() => import('../charts3d/TiltDonut.vue'))

// ---------------------------------------------------------------- 3D or flat
const reduced = useReducedMotion()
const wide = ref(false)
let mq: MediaQueryList | undefined
const setWide = () => (wide.value = !!mq?.matches)
onMounted(() => {
  if (typeof matchMedia !== 'function') return
  mq = matchMedia('(min-width: 640px)')
  setWide()
  mq.addEventListener?.('change', setWide)
})
onBeforeUnmount(() => mq?.removeEventListener?.('change', setWide))
// The person's own choice wins; until there is one, 3D on a wide screen unless they asked for less motion.
const chosen = ref<'3d' | 'flat' | null>(null)
const mode = computed<'3d' | 'flat'>(() => {
  if (!wide.value) return 'flat'
  return chosen.value ?? (reduced.value ? 'flat' : '3d')
})
const MAX_3D = 62 // beyond this many columns a 3D scene is too crowded to read
const modeOptions = computed(() => (['3d', 'flat'] as const).map((v) => ({ label: t('reports.pro.mode.' + v), value: v })))
const tooMany = computed(() => props.summary.buckets.length > MAX_3D)
const show3d = computed(() => mode.value === '3d' && !tooMany.value)

// ---------------------------------------------------------------- words and numbers
const s = computed(() => props.summary)
const currencies = computed(() => currenciesOf(s.value, props.plans, props.methods))
const currencyName = (c: string) => (te('currency.' + c) ? t('currency.' + c) : c)
const m = (amount: number, c: string) => money({ amount, currency: c }, lang.value)
const n = (v: number) => num(v, lang.value)
const date = (iso: string) => localDate(iso, lang.value, 'medium', props.calendar)
const label = (iso: string) => periodLabel(iso, s.value.group, lang.value, t, s.value.calendar, props.calendar)
const covers = computed(() => t('reports.pro.period', { from: date(s.value.from), to: date(s.value.to) }))
const prepared = computed(() => date(todayIn(s.value.tz)))
const prevRange = computed(() => previousRange(s.value.from, s.value.to))
const comparable = computed(() => props.previousState === 'ready' && props.previous !== null)

const periodText = computed(() => {
  const days = prevRange.value?.days ?? 0
  return t('reports.pro.prev_period', { n: n(days) }, days)
})
const comparedNote = computed(() => {
  const r = prevRange.value
  switch (props.previousState) {
    case 'ready':
      return r ? t('reports.pro.compared', { n: n(r.days), from: date(r.from), to: date(r.to) }, r.days) : ''
    case 'loading':
      return t('reports.pro.compared_loading')
    case 'failed':
      return t('reports.pro.compared_failed')
    case 'none':
      return t('reports.pro.compared_none')
    default:
      return ''
  }
})

/** changeText says how a number moved, in words ("12% higher than the previous 30 days"). */
function changeText(d: Delta): string {
  if (!comparable.value) return ''
  const p = { pct: percent(d.pct, lang.value), period: periodText.value }
  switch (d.kind) {
    case 'up':
      return d.lots ? t('reports.pro.change.up_lots', p) : t('reports.pro.change.up', p)
    case 'down':
      return t('reports.pro.change.down', p)
    case 'same':
      return t('reports.pro.change.same', p)
    case 'about':
      return t('reports.pro.change.about', p)
    case 'new':
      return t('reports.pro.change.new', p)
    default:
      return ''
  }
}
/** badge is the short mark on a card: an arrow and the percent, or a word. */
function badge(d: Delta): string {
  if (!comparable.value) return ''
  switch (d.kind) {
    case 'up':
      return '▲ ' + (d.lots ? '>' : '') + percent(d.pct, lang.value)
    case 'down':
      return '▼ ' + percent(d.pct, lang.value)
    case 'same':
    case 'about':
      return '= ' + t('reports.pro.badge.same')
    case 'new':
      return '▲ ' + t('reports.pro.badge.new')
    default:
      return ''
  }
}
/** story is the sentence under a card: what happened, and how it compares. */
function story(key: string, value: string, d: Delta): string {
  const ch = changeText(d)
  return ch ? t(`reports.pro.story.${key}`, { value, change: ch }) : t(`reports.pro.story.${key}_alone`, { value })
}
const count = (key: 'orders' | 'customers' | 'people' | 'services', v: number) => t(`reports.pro.count.${key}`, { n: n(v) }, v)

// ---------------------------------------------------------------- the headline numbers
const kpisOf = (c: string) => currencyKpis(c, s.value, props.previous)
const people = computed(() => peopleKpis(s.value, props.previous))
function kpiValue(k: Kpi, c: string): string {
  return k.key === 'orders' ? n(k.value) : m(k.value, c)
}
function kpiStory(k: Kpi, c: string): string {
  return story(k.key, k.key === 'orders' ? count('orders', k.value) : m(k.value, c), k.delta)
}
const peopleValue = (k: PeopleKpi) => n(k.value)
function peopleStory(k: PeopleKpi): string {
  const word = k.key === 'trials' ? 'people' : k.key === 'churned_services' ? 'services' : 'customers'
  return story(k.key, count(word, k.value), k.delta)
}
const slotOf = { revenue: 0, net: 2, orders: 1, aov: 3, new_customers: 2, trials: 3, churned_services: 7, churned_customers: 7 } as const
const sparkText = (what: string, values: number[]) => t('reports.spark.' + trendWords(values), { what })

/** headline writes a currency's story in a few plain sentences. */
function headline(c: string): string[] {
  const cell = s.value.totals.currencies[c]
  const out: string[] = []
  if (!cell || cell.orders === 0) return [t('reports.pro.hl.none', { currency: currencyName(c) })]
  out.push(t('reports.pro.hl.sales', { orders: count('orders', cell.orders), revenue: m(cell.revenue, c) }))
  const best = bestBucket(s.value, c)
  if (best && s.value.buckets.length > 1) out.push(t('reports.pro.hl.best', { unit: t('reports.pro.unit.' + s.value.group), period: label(best.date), amount: m(best.revenue, c) }))
  if (cell.refunds.count > 0) out.push(t('reports.pro.hl.refunds', { refunds: m(cell.refunds.amount, c), what: t('reports.pro.count.refunds', { n: n(cell.refunds.count) }, cell.refunds.count) }))
  const rows = byCurrency(props.plans, c)
  if (rows.length) {
    const total = rows.reduce((sum, r) => sum + r.revenue, 0)
    out.push(t('reports.pro.hl.plan', { plan: planName(rows[0].name, lang.value), share: percent(share(rows[0].revenue, total), lang.value) }))
  }
  const ways = methodSlices(props.methods, c, methodName, t('charts.other'))
  if (ways.length) {
    const total = ways.reduce((sum, w) => sum + w.value, 0)
    out.push(t('reports.pro.hl.method', { method: ways[0].label, share: percent(share(ways[0].value, total), lang.value) }))
  }
  const peak = busiest(hourGrid(props.hours?.hours ?? [], c, 'orders', props.calendar))
  if (peak) out.push(t('reports.pro.hl.busiest', { day: weekdayName(isoToJs(peak.weekday), lang.value, 'long'), hour: hourText(peak.hour) }))
  return out
}
function methodName(r: ReportMethod): string {
  if (r.provider && te('payment.provider.' + r.provider)) return t('payment.provider.' + r.provider)
  if (r.provider) return r.provider
  return te('reports.method.' + r.method) ? t('reports.method.' + r.method) : r.method
}

// ---------------------------------------------------------------- the charts
const categories = computed(() => s.value.buckets.map((b) => label(b.date)))
const ticks = computed(() => s.value.buckets.map((b) => axisLabel(b.date, s.value.group, lang.value, s.value.calendar, props.calendar)))
const partials = computed(() => partialFlags(s.value))
const corner = computed(() => t('reports.table.period'))
const moneyFmt = (c: string) => (v: number) => m(v, c)
const axisFmt = (c: string) => (v: number) => moneyCompact({ amount: v, currency: c }, lang.value)
const sharePct = (p: number) => percent(Math.round(p), lang.value)
const hourText = (h: number) => hourLabel(h, lang.value)

function revenueLines(c: string) {
  const r = revenueSeries(s.value, c)
  return [
    { key: 'revenue', label: t('reports.chart.revenue'), values: r.revenue, slot: 0, area: true },
    { key: 'net', label: t('reports.chart.net'), values: r.net, slot: 2, dashed: true },
  ]
}
function orderBars(c: string) {
  const o = orderSeries(s.value, c)
  return [
    { key: 'new', label: t('reports.chart.type.new'), values: o.new, slot: 0 },
    { key: 'renew', label: t('reports.chart.type.renew'), values: o.renew, slot: 1 },
    { key: 'traffic', label: t('reports.chart.type.traffic'), values: o.traffic, slot: 2 },
  ]
}
function planBars(c: string) {
  if (!props.planSeries) return []
  return planStack(props.planSeries.series, s.value, c).map((p) => ({
    key: p.key,
    label: p.planId ? planName(p.name, lang.value) : t('charts.other'),
    values: p.values,
    slot: p.slot,
  }))
}
function topPlans(c: string) {
  const rows = byCurrency(props.plans, c).slice(0, 8)
  return { categories: rows.map((p) => planName(p.name, lang.value)), series: [{ key: 'revenue', label: t('reports.plans.revenue'), values: rows.map((p) => p.revenue), slot: 0 }] }
}
const methodPie = (c: string) => methodSlices(props.methods, c, methodName, t('charts.other'))
function heat(c: string) {
  const g = hourGrid(props.hours?.hours ?? [], c, 'orders', props.calendar)
  return {
    rows: g.weekdays.map((wd) => weekdayName(isoToJs(wd), lang.value, 'long')),
    values: g.values,
    peak: busiest(g),
  }
}
const hourCols = computed(() => Array.from({ length: 24 }, (_, h) => hourText(h)))
const hourTicks = computed(() => Array.from({ length: 24 }, (_, h) => digits(String(h), lang.value)))
function revenueSummary(c: string): string {
  const rev = revenueSeries(s.value, c).revenue
  const top = rev.reduce((best, v, i) => (v > rev[best] ? i : best), 0)
  return rev.length && rev[top] > 0
    ? t('reports.chart.peak', { currency: currencyName(c), amount: m(rev[top], c), period: categories.value[top] })
    : t('reports.chart.zero', { currency: currencyName(c) })
}
const peopleLines = computed(() => {
  const p = peopleSeries(s.value)
  return [
    { key: 'new', label: t('reports.customers.new'), values: p.newCustomers, slot: 2 },
    { key: 'trials', label: t('reports.customers.trials'), values: p.trials, slot: 3 },
    { key: 'lost', label: t('reports.churn.services'), values: p.lost, slot: 7 },
  ]
})

const print = () => window.print()
</script>

<template>
  <article class="pro-report" :class="{ refreshing: loading }" :aria-label="t('reports.pro.title')">
    <header class="pro-head">
      <div class="titles">
        <div class="brand">{{ brand }}</div>
        <h2 class="pro-title">{{ t('reports.pro.title') }}</h2>
        <p class="pro-period">{{ covers }}</p>
        <p class="pro-meta">{{ t('reports.pro.zone', { tz: isolate(s.tz) }) }} · {{ t('reports.pro.prepared', { date: prepared }) }}</p>
        <p v-if="comparedNote" class="pro-meta compared">{{ comparedNote }}</p>
      </div>
      <div class="head-actions no-print">
        <SelectButton
          v-if="wide && !tooMany"
          :model-value="mode"
          :options="modeOptions"
          option-label="label"
          option-value="value"
          :allow-empty="false"
          :aria-label="t('reports.pro.mode_label')"
          class="mode"
          @update:model-value="(v: '3d' | 'flat') => (chosen = v)"
        />
        <Button class="print-btn" icon="pi pi-print" :label="t('reports.pro.print')" outlined @click="print" />
      </div>
    </header>

    <section v-for="c in currencies" :key="c" class="section cur-block" :data-currency="c" :aria-labelledby="'pro-cur-' + c">
      <h3 :id="'pro-cur-' + c" class="section-title">{{ currencyName(c) }}</h3>
      <div class="lead">
        <p v-for="(line, i) in headline(c)" :key="i" :class="i === 0 ? 'lead-main' : 'lead-more'">{{ line }}</p>
      </div>

      <div class="kpis">
        <div v-for="k in kpisOf(c)" :key="k.key" class="kpi" :data-kpi="k.key">
          <div class="kpi-label">{{ t('reports.pro.kpi.' + k.key) }}</div>
          <div class="kpi-value">{{ kpiValue(k, c) }}</div>
          <div v-if="badge(k.delta)" class="kpi-delta" :class="'tone-' + toneOf(k.delta)">
            <span aria-hidden="true">{{ badge(k.delta) }}</span>
          </div>
          <Sparkline :values="k.spark" kind="line" :color-slot="slotOf[k.key]" :label="sparkText(t('reports.pro.kpi.' + k.key), k.spark)" />
          <p class="kpi-story">{{ kpiStory(k, c) }}</p>
        </div>
      </div>

      <div class="viz">
        <div class="viz-wide">
          <IsoColumns
            v-if="show3d && planBars(c).length"
            class="viz-chart plans-3d"
            :title="t('reports.chart.plans_title')"
            :subtitle="currencyName(c)"
            :categories="categories"
            :ticks="ticks"
            :series="planBars(c)"
            :format="moneyFmt(c)"
            :axis-format="axisFmt(c)"
            :partial="partials"
            :corner="corner"
          />
          <BarChart
            v-else-if="planBars(c).length"
            class="viz-chart plans-flat"
            :title="t('reports.chart.plans_title')"
            :subtitle="currencyName(c)"
            :categories="categories"
            :ticks="ticks"
            :series="planBars(c)"
            mode="stacked"
            :format="moneyFmt(c)"
            :axis-format="axisFmt(c)"
            :partial="partials"
            :corner="corner"
            :height="280"
          />
        </div>
        <div class="viz-wide">
          <LineChart
            class="viz-chart revenue-lines"
            :title="t('reports.chart.title.' + s.group)"
            :subtitle="currencyName(c)"
            :categories="categories"
            :ticks="ticks"
            :series="revenueLines(c)"
            :format="moneyFmt(c)"
            :axis-format="axisFmt(c)"
            :partial="partials"
            :corner="corner"
            :summary="revenueSummary(c)"
          />
        </div>
        <div class="viz-half">
          <TiltDonut
            v-if="show3d && methodPie(c).length"
            class="viz-chart methods-3d"
            :title="t('reports.methods.title')"
            :subtitle="currencyName(c)"
            :slices="methodPie(c)"
            :format="moneyFmt(c)"
            :center-format="axisFmt(c)"
            :percent="sharePct"
            :corner="t('reports.methods.method')"
          />
          <DonutChart
            v-else
            class="viz-chart methods-flat"
            :title="t('reports.methods.title')"
            :subtitle="currencyName(c)"
            :slices="methodPie(c)"
            :format="moneyFmt(c)"
            :center-format="axisFmt(c)"
            :percent="sharePct"
            :empty-text="t('reports.methods.none')"
            :corner="t('reports.methods.method')"
          />
        </div>
        <div class="viz-half">
          <BarChart
            class="viz-chart top-plans"
            :title="t('reports.plans.title')"
            :subtitle="currencyName(c)"
            :categories="topPlans(c).categories"
            :series="topPlans(c).series"
            orientation="horizontal"
            :format="moneyFmt(c)"
            :empty-text="t('reports.plans.none')"
            :corner="t('reports.plans.plan')"
          />
        </div>
        <div class="viz-half">
          <BarChart
            class="viz-chart order-types"
            :title="t('reports.chart.orders_title')"
            :subtitle="currencyName(c)"
            :categories="categories"
            :ticks="ticks"
            :series="orderBars(c)"
            mode="stacked"
            :format="n"
            :partial="partials"
            :corner="corner"
            :height="240"
          />
        </div>
        <div v-if="hours" class="viz-half">
          <Heatmap
            class="viz-chart busy-hours"
            :title="t('reports.heat.title')"
            :subtitle="currencyName(c)"
            :rows="heat(c).rows"
            :cols="hourCols"
            :col-ticks="hourTicks"
            :values="heat(c).values"
            :format="n"
            :value-label="t('reports.methods.orders')"
            :empty-text="t('reports.heat.none')"
            :corner="t('reports.heat.day')"
          />
        </div>
      </div>
    </section>

    <section class="section people-block" aria-labelledby="pro-people">
      <h3 id="pro-people" class="section-title">{{ t('reports.pro.people') }}</h3>
      <div class="kpis">
        <div v-for="k in people" :key="k.key" class="kpi" :data-kpi="k.key">
          <div class="kpi-label">{{ t('reports.pro.kpi.' + k.key) }}</div>
          <div class="kpi-value">{{ peopleValue(k) }}</div>
          <div v-if="badge(k.delta)" class="kpi-delta" :class="'tone-' + toneOf(k.delta, k.goodWhenUp)">
            <span aria-hidden="true">{{ badge(k.delta) }}</span>
          </div>
          <Sparkline :values="k.spark" kind="bar" :color-slot="slotOf[k.key]" :label="sparkText(t('reports.pro.kpi.' + k.key), k.spark)" />
          <p class="kpi-story">{{ peopleStory(k) }}</p>
        </div>
      </div>
      <div class="viz">
        <div class="viz-wide">
          <LineChart
            class="viz-chart people-lines"
            :title="t('reports.chart.people_title')"
            :categories="categories"
            :ticks="ticks"
            :series="peopleLines"
            :format="n"
            :partial="partials"
            :corner="corner"
            :height="240"
          />
        </div>
      </div>
    </section>

    <footer class="notes">
      <p>{{ t('reports.pro.note_money') }}</p>
      <p>{{ t('reports.pro.note_churn') }}</p>
      <p v-if="s.buckets.some((b) => b.partial)">{{ t('reports.chart.partial') }}</p>
      <p>{{ t('reports.calendar_note.' + s.calendar) }}</p>
      <p class="made">{{ t('reports.pro.made', { brand }) }}</p>
    </footer>
  </article>
</template>

<style scoped>
.pro-report {
  --pro-good: #0a7a0a;
  --pro-bad: #c22f2f;
  max-inline-size: 78rem;
  margin-block-start: var(--app-gap);
}
:global(:root.app-dark) .pro-report {
  --pro-good: #38c238;
  --pro-bad: #ef7373;
}
.refreshing .section {
  opacity: 0.6;
}
.pro-head {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
  align-items: flex-start;
  justify-content: space-between;
  padding: 1.25rem 1.4rem;
  background: var(--p-content-background);
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--app-radius);
  /* a band of the brand colour along the top, the way a letterhead has one */
  border-block-start: 4px solid var(--p-primary-color);
}
.brand {
  color: var(--p-primary-color);
  font-size: 0.9rem;
  font-weight: 700;
}
.pro-title {
  margin: 0.15rem 0 0.25rem;
  font-size: 1.6rem;
  line-height: 1.25;
}
.pro-period {
  margin: 0;
  font-size: 1.05rem;
  font-weight: 600;
}
.pro-meta {
  margin: 0.2rem 0 0;
  color: var(--p-text-muted-color);
  font-size: 0.85rem;
}
.head-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.6rem;
  align-items: center;
}
.section {
  margin-block-start: var(--app-gap);
  padding: 1.25rem 1.4rem;
  background: var(--p-content-background);
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--app-radius);
  transition: opacity 0.2s;
}
.section-title {
  margin: 0 0 0.75rem;
  font-size: 1.2rem;
}
.lead {
  margin-block-end: 1rem;
}
.lead-main {
  margin: 0 0 0.35rem;
  font-size: 1.1rem;
  font-weight: 600;
}
.lead-more {
  margin: 0.15rem 0;
  color: var(--p-text-muted-color);
}
.kpis {
  display: grid;
  gap: 0.9rem;
  grid-template-columns: repeat(auto-fit, minmax(min(14rem, 100%), 1fr));
}
.kpi {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  min-inline-size: 0;
  padding: 0.9rem 1rem;
  background: var(--p-content-background);
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.65rem;
}
.kpi-label {
  color: var(--p-text-muted-color);
  font-size: 0.85rem;
}
.kpi-value {
  font-size: 1.55rem;
  font-weight: 700;
  line-height: 1.25;
  overflow-wrap: anywhere;
}
.kpi-delta {
  align-self: flex-start;
  padding: 0.05rem 0.5rem;
  font-size: 0.82rem;
  font-weight: 700;
  border: 1px solid currentcolor;
  border-radius: 999px;
}
.tone-good {
  color: var(--pro-good);
}
.tone-bad {
  color: var(--pro-bad);
}
.tone-flat {
  color: var(--p-text-muted-color);
}
.kpi-story {
  margin: 0.35rem 0 0;
  font-size: 0.88rem;
  line-height: 1.6;
}
.viz {
  display: grid;
  gap: 1.5rem;
  grid-template-columns: repeat(auto-fit, minmax(min(26rem, 100%), 1fr));
  margin-block-start: 1.4rem;
}
.viz-wide {
  grid-column: 1 / -1;
  min-inline-size: 0;
}
.viz-half {
  min-inline-size: 0;
}
.notes {
  margin-block-start: var(--app-gap);
  padding: 0 0.4rem;
  color: var(--p-text-muted-color);
  font-size: 0.82rem;
}
.notes p {
  margin: 0.25rem 0;
}
.made {
  margin-block-start: 0.6rem;
}
</style>

<style>
/* Paper. The report alone, light, one column of cards that do not break across a page; the screen's
   own chrome (menus, buttons, switches) stays off. Colours are the light ones whatever theme is on
   screen, and fills are printed as they are. */
@media print {
  @page {
    size: A4;
    margin: 12mm;
  }
  html,
  body {
    background: #fff !important;
  }
  .pro-report {
    --p-text-color: #0f172a;
    --p-text-muted-color: #475569;
    --p-content-background: #fff;
    --p-content-border-color: #cbd5e1;
    --p-primary-color: #0f766e;
    --pro-good: #0a7a0a;
    --pro-bad: #c22f2f;
    max-inline-size: none;
    margin: 0;
    color: #0f172a;
    -webkit-print-color-adjust: exact;
    print-color-adjust: exact;
  }
  .pro-report .no-print {
    display: none !important;
  }
  .pro-report .section,
  .pro-report .pro-head {
    break-inside: auto;
    border-radius: 0;
  }
  .pro-report .kpi,
  .pro-report .chart-frame,
  .pro-report .viz-chart {
    break-inside: avoid;
  }
  .pro-report .viz {
    gap: 1rem;
  }
  .pro-report .section {
    break-before: auto;
  }
  .pro-report .cur-block + .cur-block,
  .pro-report .people-block {
    break-before: page;
  }
}
</style>
