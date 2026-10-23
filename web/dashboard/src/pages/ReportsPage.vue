<script setup lang="ts">
import { computed, defineAsyncComponent, h, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import Message from 'primevue/message'
import Select from 'primevue/select'
import SelectButton from 'primevue/selectbutton'
import Skeleton from 'primevue/skeleton'
import { BarChart, DonutChart, Heatmap, LineChart, Sparkline } from '../components/charts'
import DateField from '../components/reports/DateField.vue'
import ReportsStart from '../components/reports/ReportsStart.vue'
import { api, query } from '../api'
import { DATE_MAX, DATE_MIN, digits, formatInput, todayIn, weekdayName } from '../calendar'
import { fieldId } from '../settings'
import { useAuth } from '../stores/auth'
import { errorText } from '../errors'
import { isolate, localDate, money, moneyCompact, num, percent, planName, type Lang } from '../format'
import {
  byCurrency,
  calendars,
  currenciesOf,
  customProblem,
  exportParams,
  groups,
  hasData,
  initialCalendar,
  periodLabel,
  ranges,
  rememberCalendar,
  reportParams,
  share,
  validCustom,
  type DailySummaryStatus,
  type ReportMethod,
  type ReportPlan,
  type ReportSummary,
  type Selection,
} from '../reports'
import {
  axisLabel,
  busiest,
  hourGrid,
  hourLabel,
  isoToJs,
  methodSlices,
  orderBars,
  orderSeries,
  partialFlags,
  peopleSeries,
  planStack,
  previousRange,
  revenueSeries,
  trendWords,
  type HoursAnswer,
  type PlanSeriesAnswer,
} from '../reportsvisual'

const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const auth = useAuth()
const route = useRoute()
const router = useRouter()

// The professional report is its own chunk (it carries the 3D charts): the page
// downloads it only when someone opens that view.
const ProfessionalReport = defineAsyncComponent({
  loader: () => import('../components/reports/ProfessionalReport.vue'),
  loadingComponent: { render: () => h(Skeleton, { height: '24rem', class: 'report-loading' }) },
  delay: 0,
})

// Whether the daily sales summary is on is a hint above the report: when core
// cannot say, the line is left out. The link to its settings is for staff who
// may change settings.
const daily = ref<DailySummaryStatus | null>(null)
async function loadDaily() {
  try {
    daily.value = await api<DailySummaryStatus>('GET', '/reports/daily-summary')
  } catch {
    daily.value = null
  }
}
onMounted(loadDaily)
const dailyLink = { name: 'settings', hash: '#' + fieldId('reports.daily_summary') }

// Two views of the same numbers: the charts and tables to work with, and the
// professional report to read, print or show. The choice is in the address
// (?view=report), so it can be linked and survives a reload.
type View = 'analysis' | 'report'
const view = computed<View>({
  get: () => (route.query.view === 'report' ? 'report' : 'analysis'),
  set: (v) => void router.replace({ query: { ...route.query, view: v === 'report' ? 'report' : undefined } }),
})
const viewOptions = computed(() => (['analysis', 'report'] as const).map((v) => ({ label: t('reports.view.' + v), value: v })))

// The calendar starts as the person left it (kept per browser, like the language and the theme), else the
// language's own. It decides every date of the page: how they are written, typed and picked.
const sel = reactive<Selection>({ range: 'last_30_days', group: 'day', calendar: initialCalendar(lang.value), from: '', to: '' })
watch(() => sel.calendar, rememberCalendar)
const summary = ref<ReportSummary | null>(null)
const plans = ref<ReportPlan[]>([])
const methods = ref<ReportMethod[]>([])
// What the extra charts read. They are a nicety: when core cannot answer, the page
// shows everything else and leaves those charts out.
const planSeries = ref<PlanSeriesAnswer | null>(null)
const hours = ref<HoursAnswer | null>(null)
const loading = ref(false)
const failure = reactive({ text: '', detail: '' })
let seq = 0

// The summary first: it says which dates, in which time zone, "today" or
// "this month" came to, and the others ask for exactly those, so the answers
// always describe the same days.
async function load() {
  if (sel.range === 'custom' && !validCustom(sel.from, sel.to)) return
  const mine = ++seq
  loading.value = true
  failure.text = failure.detail = ''
  try {
    const s = await api<ReportSummary>('GET', '/reports/summary' + query(reportParams(sel)))
    const pin = query({ from: s.from, to: s.to, tz: s.tz })
    const series = query({ from: s.from, to: s.to, tz: s.tz, group: s.group, ...(s.calendar === 'jalali' ? { calendar: 'jalali' } : {}) })
    const [p, m, ps, hs] = await Promise.all([
      api<{ plans: ReportPlan[] }>('GET', '/reports/plans' + pin),
      api<{ methods: ReportMethod[] }>('GET', '/reports/methods' + pin),
      api<PlanSeriesAnswer>('GET', '/reports/plan-series' + series).catch(() => null),
      api<HoursAnswer>('GET', '/reports/hours' + pin).catch(() => null),
    ])
    if (mine !== seq) return
    summary.value = s
    plans.value = p.plans
    methods.value = m.methods
    planSeries.value = ps
    hours.value = hs
  } catch (e) {
    if (mine !== seq) return
    const r = errorText(e, t)
    failure.text = r.text
    failure.detail = r.detail
    summary.value = null // numbers of another range must not stay under this one's title
    planSeries.value = hours.value = null
  } finally {
    if (mine === seq) loading.value = false
  }
}
onMounted(load)
// A choice asks at once. Dates typed in the custom range ask a moment after the last key, so "1405/07/1" on the
// way to "1405/07/10" does not make three requests for a day nobody wanted.
let typing: ReturnType<typeof setTimeout> | undefined
watch(
  () => [sel.range, sel.group, sel.calendar],
  () => {
    clearTimeout(typing)
    void load()
  },
)
watch(
  () => [sel.from, sel.to],
  () => {
    clearTimeout(typing)
    typing = setTimeout(() => void load(), 300)
  },
)
onBeforeUnmount(() => clearTimeout(typing))

// Picking "custom" starts from the dates on screen.
watch(
  () => sel.range,
  (r) => {
    if (r === 'custom' && summary.value && !sel.from) {
      sel.from = summary.value.from
      sel.to = summary.value.to
    }
  },
)

// The period before, for the professional report's "compared with": the same number of days right
// before the range. Asked only while that view is open (it is a whole report of its own to compute).
const previous = ref<ReportSummary | null>(null)
const previousState = ref<'idle' | 'loading' | 'ready' | 'none' | 'failed'>('idle')
let prevSeq = 0
async function loadPrevious() {
  const s = summary.value
  const mine = ++prevSeq
  previous.value = null
  if (view.value !== 'report' || !s) {
    previousState.value = 'idle'
    return
  }
  const range = previousRange(s.from, s.to)
  if (!range) {
    previousState.value = 'none'
    return
  }
  previousState.value = 'loading'
  try {
    // Only the totals are used, so the cheapest grouping asks for it.
    const p = await api<ReportSummary>('GET', '/reports/summary' + query({ from: range.from, to: range.to, tz: s.tz, group: 'month' }))
    if (mine !== prevSeq) return
    previous.value = p
    previousState.value = 'ready'
  } catch {
    if (mine !== prevSeq) return
    previousState.value = 'failed'
  }
}
watch([summary, view], () => void loadPrevious(), { immediate: true })

const rangeOptions = computed(() => ranges.map((r) => ({ label: t('reports.range.' + r), value: r })))
const groupOptions = computed(() => groups.map((g) => ({ label: t('reports.group.' + g), value: g })))
const calendarOptions = computed(() => calendars.map((c) => ({ label: t('reports.calendar.' + c), value: c })))
const problem = computed(() => (sel.range === 'custom' ? customProblem(sel.from, sel.to) : null))
const customInvalid = computed(() => problem.value !== null)
// "Today" for the grid and the example is the store's today once a report has said which zone that is.
const today = computed(() => todayIn(summary.value?.tz))
const problemText = computed(() => {
  const cal = sel.calendar
  const write = (iso: string) => isolate(formatInput(iso, lang.value, cal))
  switch (problem.value) {
    case 'format':
      return t('reports.custom_error.format', { calendar: t('reports.calendar.' + cal), example: write(today.value) })
    case 'range':
      return t('reports.custom_error.range', { min: write(DATE_MIN), max: write(DATE_MAX) })
    case 'order':
      return t('reports.custom_error.order')
    case 'span':
      return t('reports.custom_error.span')
    default:
      return ''
  }
})

const currencies = computed(() => (summary.value ? currenciesOf(summary.value, plans.value, methods.value) : []))
const currencyName = (c: string) => (te('currency.' + c) ? t('currency.' + c) : c)
const m = (amount: number, currency: string) => money({ amount, currency }, lang.value)
// Dates are written in the calendar the page is set to the moment it changes; the periods (a Jalali month, a week
// from Saturday) change when the new report arrives.
const label = (date: string) => (summary.value ? periodLabel(date, summary.value.group, lang.value, t, summary.value.calendar, sel.calendar) : date)

// The days the report covers, in the calendar the page is set to.
const covers = computed(() =>
  summary.value
    ? t('reports.covers', {
        from: localDate(summary.value.from, lang.value, 'medium', sel.calendar),
        to: localDate(summary.value.to, lang.value, 'medium', sel.calendar),
        tz: isolate(summary.value.tz),
      })
    : '',
)

// The files hold the range the page shows, not the choice that made it.
const csv = (name: string) => (summary.value ? `/api/v1/reports/${name}.csv` + query(exportParams(summary.value)) : '')

const totals = (cur: string) => summary.value?.totals.currencies[cur]
const flows = ['topups', 'refunds', 'rejected', 'gateway_failed', 'cancelled'] as const
const showShare = (rows: ReportMethod[], row: ReportMethod) => share(row.revenue, rows.reduce((n, r) => n + r.revenue, 0))
const methodLabel = (k: string) => (te('reports.method.' + k) ? t('reports.method.' + k) : k)
// Who took the payment; for a gateway or staff-approved order whose event
// predates the provider, a note that it was not recorded.
const providerLabel = (r: ReportMethod) => {
  if (r.provider) return te('payment.provider.' + r.provider) ? t('payment.provider.' + r.provider) : r.provider
  return te('reports.provider_unrecorded.' + r.method) ? t('reports.provider_unrecorded.' + r.method) : ''
}

// ---------------------------------------------------------------- the charts
// One currency at a time: amounts of different currencies never share an axis. The
// switch is shown only when there is more than one.
const chartCur = ref('')
watch(
  currencies,
  (list) => {
    if (!list.includes(chartCur.value)) chartCur.value = list[0] ?? ''
  },
  { immediate: true },
)
const currencyOptions = computed(() => currencies.value.map((c) => ({ label: currencyName(c), value: c })))
const cur = computed(() => chartCur.value)
const fmtMoney = computed(() => (n: number) => m(n, cur.value))
const fmtAxis = computed(() => (n: number) => moneyCompact({ amount: n, currency: cur.value }, lang.value))
const fmtCount = (n: number) => num(n, lang.value)
const fmtShare = (p: number) => percent(Math.round(p), lang.value)
const buckets = computed(() => summary.value?.buckets ?? [])
const categories = computed(() => buckets.value.map((b) => label(b.date)))
const ticks = computed(() => (summary.value ? buckets.value.map((b) => axisLabel(b.date, summary.value!.group, lang.value, summary.value!.calendar, sel.calendar)) : []))
const partials = computed(() => (summary.value ? partialFlags(summary.value) : []))
const periodCorner = computed(() => t('reports.table.period'))
const refreshing = computed(() => loading.value)

const revenueChart = computed(() => {
  if (!summary.value || !cur.value) return []
  const s = revenueSeries(summary.value, cur.value)
  return [
    { key: 'revenue', label: t('reports.chart.revenue'), values: s.revenue, slot: 0, area: true },
    { key: 'net', label: t('reports.chart.net'), values: s.net, slot: 2, dashed: true },
  ]
})
// What a screen reader hears for the chart: where revenue peaked, as the old bars said.
const revenueSummary = computed(() => {
  const rev = revenueChart.value[0]?.values ?? []
  const top = rev.reduce((best, v, i) => (v > rev[best] ? i : best), 0)
  const name = currencyName(cur.value)
  return rev.length && rev[top] > 0
    ? t('reports.chart.peak', { currency: name, amount: m(rev[top], cur.value), period: categories.value[top] })
    : t('reports.chart.zero', { currency: name })
})
const orderChart = computed(() => {
  if (!summary.value || !cur.value) return []
  return orderBars(orderSeries(summary.value, cur.value), (k) => t('reports.chart.type.' + k))
})
const planNameOf = (p: { name: Record<string, string> }) => planName(p.name, lang.value)
const planStackChart = computed(() => {
  if (!summary.value || !planSeries.value || !cur.value) return []
  return planStack(planSeries.value.series, summary.value, cur.value).map((p) => ({
    key: p.key,
    label: p.planId ? planNameOf(p) : t('charts.other'),
    values: p.values,
    slot: p.slot,
  }))
})
const topPlansChart = computed(() => {
  const rows = byCurrency(plans.value, cur.value).slice(0, 8)
  return { categories: rows.map((p) => planNameOf(p)), series: [{ key: 'revenue', label: t('reports.plans.revenue'), values: rows.map((p) => p.revenue), slot: 0 }] }
})
const methodChart = computed(() =>
  methodSlices(methods.value, cur.value, (r) => (r.provider ? providerLabel(r) : methodLabel(r.method)), t('charts.other')),
)
const peopleChart = computed(() => {
  if (!summary.value) return []
  const p = peopleSeries(summary.value)
  return [
    { key: 'new', label: t('reports.customers.new'), values: p.newCustomers, slot: 2 },
    { key: 'trials', label: t('reports.customers.trials'), values: p.trials, slot: 3 },
    { key: 'lost', label: t('reports.churn.services'), values: p.lost, slot: 7 },
  ]
})
const hourText = (h: number) => hourLabel(h, lang.value)
const heat = computed(() => {
  const grid = hourGrid(hours.value?.hours ?? [], cur.value, 'orders', sel.calendar)
  return {
    rows: grid.weekdays.map((wd) => weekdayName(isoToJs(wd), lang.value, 'long')),
    cols: Array.from({ length: 24 }, (_, h) => hourText(h)),
    ticks: Array.from({ length: 24 }, (_, h) => digits(String(h), lang.value)),
    values: grid.values,
    peak: busiest(grid),
  }
})
const heatSummary = computed(() => {
  const p = heat.value.peak
  return p ? t('reports.heat.peak', { day: weekdayName(isoToJs(p.weekday), lang.value, 'long'), hour: hourText(p.hour), n: num(p.value, lang.value) }, p.value) : t('reports.heat.none')
})
const sparkLabel = (what: string, values: number[]) => t('reports.spark.' + trendWords(values), { what })
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.reports.title') }}</h1>
    <div v-if="summary" class="exports app-no-print">
      <a :href="csv('summary')" download class="p-button p-component p-button-outlined export export-summary">
        <i class="pi pi-download" aria-hidden="true" />
        <span>{{ t('reports.export_summary') }}</span>
      </a>
      <a :href="csv('orders')" download class="p-button p-component p-button-outlined export export-orders">
        <i class="pi pi-download" aria-hidden="true" />
        <span>{{ t('reports.export_orders') }}</span>
      </a>
    </div>
  </div>
  <p class="app-muted hint app-no-print">{{ t('reports.hint') }}</p>
  <ReportsStart @changed="load" />
  <p v-if="daily" class="app-muted hint daily-summary app-no-print" :class="daily.enabled ? 'is-on' : 'is-off'">
    {{ daily.enabled ? t('reports.daily.on', { time: isolate(daily.time), tz: isolate(daily.tz) }) : t('reports.daily.off') }}
    <RouterLink v-if="auth.can('settings.write')" :to="dailyLink" class="daily-link">{{ t('reports.daily.link') }}</RouterLink>
  </p>

  <div class="app-toolbar app-no-print">
    <SelectButton
      v-model="sel.range"
      :options="rangeOptions"
      option-label="label"
      option-value="value"
      :allow-empty="false"
      :aria-label="t('reports.period_label')"
      class="range"
    />
    <span v-if="sel.range === 'custom'" class="date-range">
      <span class="date-field">
        <label for="report-from">{{ t('reports.from') }}</label>
        <DateField id="report-from" v-model="sel.from" :calendar="sel.calendar" :today="today" :invalid="customInvalid" input-class="date-from" />
      </span>
      <span class="date-field">
        <label for="report-to">{{ t('reports.to') }}</label>
        <DateField id="report-to" v-model="sel.to" :calendar="sel.calendar" :today="today" :invalid="customInvalid" input-class="date-to" />
      </span>
    </span>
    <Select
      v-model="sel.group"
      :options="groupOptions"
      option-label="label"
      option-value="value"
      :aria-label="t('reports.group_label')"
      class="group"
    />
    <SelectButton
      v-model="sel.calendar"
      :options="calendarOptions"
      option-label="label"
      option-value="value"
      :allow-empty="false"
      :aria-label="t('reports.calendar_label')"
      class="calendar"
    />
    <Button v-if="loading" text loading :aria-label="t('app.loading')" />
  </div>
  <p v-if="problemText" class="custom-problem app-no-print" role="alert">{{ problemText }}</p>
  <p v-if="summary" class="app-muted covers" :class="{ 'app-no-print': view === 'report' }">{{ covers }}</p>

  <Message v-if="failure.text" severity="error" :closable="false" class="report-error">
    {{ failure.text }}
    <div v-if="failure.detail" class="app-ltr detail">{{ failure.detail }}</div>
    <Button :label="t('app.retry')" text size="small" class="retry" @click="load" />
  </Message>

  <div v-if="!summary && loading" class="app-grid skeletons">
    <Skeleton v-for="i in 3" :key="i" height="9rem" />
  </div>

  <template v-if="summary">
    <p v-if="!hasData(summary)" class="app-muted empty">{{ t('reports.empty') }}</p>
    <template v-else>
      <SelectButton
        v-model="view"
        :options="viewOptions"
        option-label="label"
        option-value="value"
        :allow-empty="false"
        :aria-label="t('reports.view_label')"
        class="view-switch app-no-print"
      />

      <ProfessionalReport
        v-if="view === 'report'"
        :summary="summary"
        :previous="previous"
        :previous-state="previousState"
        :plans="plans"
        :methods="methods"
        :plan-series="planSeries"
        :hours="hours"
        :calendar="sel.calendar"
        :brand="auth.brand"
        :loading="loading"
      />

      <template v-else>
        <div class="app-grid cards">
          <Card v-for="c in currencies" :key="c" class="stat currency-card" :data-currency="c">
            <template #subtitle>{{ currencyName(c) }}</template>
            <template #content>
              <template v-if="totals(c)">
                <div class="big revenue">{{ m(totals(c)!.revenue, c) }}</div>
                <div class="app-muted">{{ t('reports.kpi.revenue') }}</div>
                <Sparkline
                  class="card-spark"
                  :values="revenueSeries(summary, c).revenue"
                  :label="sparkLabel(t('reports.kpi.revenue') + ' (' + currencyName(c) + ')', revenueSeries(summary, c).revenue)"
                  :color-slot="0"
                />
                <div class="line net">{{ t('reports.kpi.net', { amount: m(totals(c)!.net, c) }) }}</div>
                <div class="line orders">{{ t('reports.kpi.orders', { n: num(totals(c)!.orders, lang) }, totals(c)!.orders) }}</div>
                <div class="app-muted small split">
                  {{ t('reports.kpi.split', { new: num(totals(c)!.new_orders, lang), renew: num(totals(c)!.renewals, lang), traffic: num(totals(c)!.traffic_topups, lang) }) }}
                </div>
                <div v-if="totals(c)!.licenses?.count" class="app-muted small sales licenses">
                  {{ t('reports.kpi.licenses', { n: num(totals(c)!.licenses!.count, lang), amount: m(totals(c)!.licenses!.amount, c) }) }}
                </div>
                <div v-if="totals(c)!.digital?.count" class="app-muted small sales digital">
                  {{ t('reports.kpi.digital', { n: num(totals(c)!.digital!.count, lang), amount: m(totals(c)!.digital!.amount, c) }) }}
                </div>
                <dl class="flows">
                  <template v-for="f in flows" :key="f">
                    <dt>{{ t('reports.kpi.' + f) }}</dt>
                    <dd :class="'flow-' + f">
                      {{ m(totals(c)![f].amount, c) }}
                      <span class="app-muted">· {{ t('reports.kpi.' + f + '_n', { n: num(totals(c)![f].count, lang) }, totals(c)![f].count) }}</span>
                    </dd>
                  </template>
                </dl>
              </template>
            </template>
          </Card>
          <Card class="stat customers-card">
            <template #subtitle>{{ t('reports.customers.title') }}</template>
            <template #content>
              <div class="big new-customers">{{ num(summary.totals.new_customers, lang) }}</div>
              <div class="app-muted">{{ t('reports.customers.new') }}</div>
              <Sparkline class="card-spark" kind="bar" :values="peopleSeries(summary).newCustomers" :label="sparkLabel(t('reports.customers.new'), peopleSeries(summary).newCustomers)" :color-slot="2" />
              <div class="app-muted small">{{ t('reports.customers.new_hint') }}</div>
              <div class="line trials">{{ t('reports.customers.trials') }}: {{ num(summary.totals.trials, lang) }}</div>
            </template>
          </Card>
          <Card class="stat churn-card">
            <template #subtitle>{{ t('reports.churn.title') }}</template>
            <template #content>
              <div class="big churned-services">{{ num(summary.totals.churned_services, lang) }}</div>
              <div class="app-muted">{{ t('reports.churn.services') }}</div>
              <Sparkline class="card-spark" kind="bar" :values="peopleSeries(summary).lost" :label="sparkLabel(t('reports.churn.services'), peopleSeries(summary).lost)" :color-slot="7" />
              <div class="app-muted small">{{ t('reports.churn.hint', { n: num(summary.churn_grace_days, lang) }, summary.churn_grace_days) }}</div>
              <div class="line churned-customers">{{ t('reports.churn.customers') }}: {{ num(summary.totals.churned_customers, lang) }}</div>
              <div class="line at-risk">{{ t('reports.churn.at_risk') }}: {{ num(summary.totals.at_risk, lang) }}</div>
            </template>
          </Card>
        </div>

        <Card v-if="currencies.length" class="block charts-card">
          <template #title>{{ t('reports.charts.title') }}</template>
          <template #content>
            <div v-if="currencies.length > 1" class="currency-switch">
              <SelectButton
                v-model="chartCur"
                :options="currencyOptions"
                option-label="label"
                option-value="value"
                :allow-empty="false"
                :aria-label="t('reports.charts.currency')"
              />
            </div>
            <div class="charts" :data-currency="cur">
              <div class="wide">
                <LineChart
                  class="chart revenue-chart"
                  :data-currency="cur"
                  :title="t('reports.chart.title.' + summary.group)"
                  :subtitle="currencyName(cur)"
                  :categories="categories"
                  :ticks="ticks"
                  :series="revenueChart"
                  :format="fmtMoney"
                  :axis-format="fmtAxis"
                  :partial="partials"
                  :loading="refreshing"
                  :corner="periodCorner"
                  :summary="revenueSummary"
                />
              </div>
              <BarChart
                v-if="planStackChart.length"
                class="chart plans-chart"
                :title="t('reports.chart.plans_title')"
                :subtitle="currencyName(cur)"
                :categories="categories"
                :ticks="ticks"
                :series="planStackChart"
                mode="stacked"
                :format="fmtMoney"
                :axis-format="fmtAxis"
                :partial="partials"
                :loading="refreshing"
                :corner="periodCorner"
                :height="260"
              />
              <BarChart
                class="chart orders-chart"
                :title="t('reports.chart.orders_title')"
                :subtitle="currencyName(cur)"
                :categories="categories"
                :ticks="ticks"
                :series="orderChart"
                mode="stacked"
                :format="fmtCount"
                :partial="partials"
                :loading="refreshing"
                :corner="periodCorner"
                :height="260"
              />
              <DonutChart
                class="chart methods-chart"
                :title="t('reports.methods.title')"
                :subtitle="currencyName(cur)"
                :slices="methodChart"
                :format="fmtMoney"
                :center-format="fmtAxis"
                :percent="fmtShare"
                :loading="refreshing"
                :empty-text="t('reports.methods.none')"
                :corner="t('reports.methods.method')"
              />
              <BarChart
                class="chart top-plans-chart"
                :title="t('reports.plans.title')"
                :subtitle="currencyName(cur)"
                :categories="topPlansChart.categories"
                :series="topPlansChart.series"
                orientation="horizontal"
                :format="fmtMoney"
                :loading="refreshing"
                :empty-text="t('reports.plans.none')"
                :corner="t('reports.plans.plan')"
              />
              <div v-if="hours" class="wide">
                <Heatmap
                  class="chart heat-chart"
                  :title="t('reports.heat.title')"
                  :subtitle="currencyName(cur)"
                  :rows="heat.rows"
                  :cols="heat.cols"
                  :col-ticks="heat.ticks"
                  :values="heat.values"
                  :format="fmtCount"
                  :value-label="t('reports.methods.orders')"
                  :loading="refreshing"
                  :empty-text="t('reports.heat.none')"
                  :corner="t('reports.heat.day')"
                  :summary="heatSummary"
                />
                <p class="app-muted small heat-note">{{ t('reports.heat.hint', { tz: isolate(summary.tz) }) }}</p>
              </div>
            </div>
            <div class="people">
              <LineChart
                class="chart people-chart"
                :title="t('reports.chart.people_title')"
                :categories="categories"
                :ticks="ticks"
                :series="peopleChart"
                :format="fmtCount"
                :partial="partials"
                :loading="refreshing"
                :height="220"
                :corner="periodCorner"
              />
            </div>
            <p v-if="summary.buckets.some((b) => b.partial)" class="app-muted small">{{ t('reports.chart.partial') }}</p>
            <p class="app-muted small">{{ t('reports.calendar_note.' + summary.calendar) }}</p>
          </template>
        </Card>

        <Card class="block period-card">
          <template #title>{{ t('reports.table.title') }}</template>
          <template #content>
            <!-- The table scrolls inside its own box (sideways and down) with its header and its
                 first column kept in view, so a long range or a phone never makes the page long or wide. -->
            <div class="period-scroll" tabindex="0" role="region" :aria-label="t('reports.table.title')">
              <table class="period-table">
                <thead>
                  <tr>
                    <th scope="col">{{ t('reports.table.period') }}</th>
                    <template v-for="c in currencies" :key="c">
                      <th scope="col">{{ t('reports.table.revenue_in', { currency: currencyName(c) }) }}</th>
                      <th scope="col">{{ t('reports.table.orders_in', { currency: currencyName(c) }) }}</th>
                    </template>
                    <th scope="col">{{ t('reports.table.new_customers') }}</th>
                    <th scope="col">{{ t('reports.table.trials') }}</th>
                    <th scope="col">{{ t('reports.table.churned') }}</th>
                    <th scope="col">{{ t('reports.table.at_risk') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="b in summary.buckets" :key="b.date">
                    <th scope="row">
                      <bdi>{{ label(b.date) }}</bdi>
                      <span v-if="b.partial" class="app-muted small partial"> ({{ t('reports.period.partial') }})</span>
                    </th>
                    <template v-for="c in currencies" :key="c">
                      <td>{{ m(b.currencies[c]?.revenue ?? 0, c) }}</td>
                      <td>{{ num(b.currencies[c]?.orders ?? 0, lang) }}</td>
                    </template>
                    <td>{{ num(b.new_customers, lang) }}</td>
                    <td>{{ num(b.trials, lang) }}</td>
                    <td>{{ num(b.churned_services, lang) }}</td>
                    <td>{{ num(b.at_risk, lang) }}</td>
                  </tr>
                </tbody>
                <tfoot>
                  <tr>
                    <th scope="row">{{ t('reports.table.total') }}</th>
                    <template v-for="c in currencies" :key="c">
                      <td>{{ m(totals(c)?.revenue ?? 0, c) }}</td>
                      <td>{{ num(totals(c)?.orders ?? 0, lang) }}</td>
                    </template>
                    <td>{{ num(summary.totals.new_customers, lang) }}</td>
                    <td>{{ num(summary.totals.trials, lang) }}</td>
                    <td>{{ num(summary.totals.churned_services, lang) }}</td>
                    <td>{{ num(summary.totals.at_risk, lang) }}</td>
                  </tr>
                </tfoot>
              </table>
            </div>
          </template>
        </Card>

        <div class="two">
          <Card class="block plans-card">
            <template #title>{{ t('reports.plans.title') }}</template>
            <template #content>
              <p v-if="!plans.length" class="app-muted">{{ t('reports.plans.none') }}</p>
              <section v-for="c in currencies" v-show="byCurrency(plans, c).length" :key="c" class="per-currency" :data-currency="c">
                <h3 class="currency-title">{{ currencyName(c) }}</h3>
                <DataTable :value="byCurrency(plans, c)" data-key="plan_id" size="small" scrollable>
                  <Column :header="t('reports.plans.plan')">
                    <template #body="{ data: p }"><bdi>{{ planName(p.name, lang) }}</bdi></template>
                  </Column>
                  <Column field="orders" :header="t('reports.plans.orders')" sortable>
                    <template #body="{ data: p }">{{ num(p.orders, lang) }}</template>
                  </Column>
                  <Column field="revenue" :header="t('reports.plans.revenue')" sortable>
                    <template #body="{ data: p }">{{ m(p.revenue, c) }}</template>
                  </Column>
                </DataTable>
              </section>
            </template>
          </Card>

          <Card class="block methods-card">
            <template #title>{{ t('reports.methods.title') }}</template>
            <template #content>
              <p v-if="!methods.length" class="app-muted">{{ t('reports.methods.none') }}</p>
              <section v-for="c in currencies" v-show="byCurrency(methods, c).length" :key="c" class="per-currency" :data-currency="c">
                <h3 class="currency-title">{{ currencyName(c) }}</h3>
                <DataTable :value="byCurrency(methods, c)" size="small" scrollable>
                  <Column :header="t('reports.methods.method')">
                    <template #body="{ data: r }">
                      <div>{{ methodLabel(r.method) }}</div>
                      <div v-if="providerLabel(r)" class="provider">{{ providerLabel(r) }}</div>
                      <div v-if="te('reports.method_hint.' + r.method)" class="app-muted small">{{ t('reports.method_hint.' + r.method) }}</div>
                    </template>
                  </Column>
                  <Column :header="t('reports.methods.orders')">
                    <template #body="{ data: r }">{{ num(r.orders, lang) }}</template>
                  </Column>
                  <Column :header="t('reports.methods.revenue')">
                    <template #body="{ data: r }">{{ m(r.revenue, c) }}</template>
                  </Column>
                  <Column :header="t('reports.methods.share')">
                    <template #body="{ data: r }">{{ percent(showShare(byCurrency(methods, c), r), lang) }}</template>
                  </Column>
                </DataTable>
              </section>
              <p class="app-muted small">{{ t('reports.methods.hint') }}</p>
            </template>
          </Card>
        </div>
      </template>
    </template>
  </template>
</template>

<style scoped>
.exports {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.export {
  gap: 0.5rem;
  text-decoration: none;
}
.hint {
  margin-block: 0 0.75rem;
  font-size: 0.9rem;
}
.daily-link {
  margin-inline-start: 0.5rem;
}
.covers {
  margin-block: 0 1rem;
  font-size: 0.9rem;
}
/* The six ranges do not fit in one row on a phone: they wrap rather than widen the page. */
.range {
  flex-wrap: wrap;
  max-inline-size: 100%;
}
.view-switch {
  margin-block-end: var(--app-gap);
}
/* From and To wrap together, never one at the end of a row and the other at the start of the next. */
.date-range {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 0.6rem;
  align-items: center;
}
.date-field {
  display: inline-flex;
  gap: 0.4rem;
  align-items: center;
}
.custom-problem {
  margin-block: 0 0.75rem;
  font-size: 0.9rem;
  color: var(--p-message-error-simple-color, var(--p-red-600));
}
.report-error {
  margin-block-end: 1rem;
}
.detail {
  margin-block-start: 0.4rem;
  font-size: 0.85rem;
}
.big {
  font-size: 1.6rem;
  font-weight: 700;
  line-height: 1.25;
}
.line {
  margin-block-start: 0.5rem;
}
.small {
  font-size: 0.85rem;
}
.card-spark {
  margin-block: 0.35rem;
}
.provider {
  font-size: 0.85rem;
  font-weight: 600;
}
.flows {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 0.25rem 0.75rem;
  margin: 0.75rem 0 0;
  font-size: 0.9rem;
}
.flows dt {
  color: var(--p-text-muted-color);
}
.flows dd {
  margin: 0;
}
.block {
  margin-block-start: var(--app-gap);
}
.currency-switch {
  margin-block-end: 1rem;
}
.charts {
  display: grid;
  gap: 1.5rem;
  grid-template-columns: repeat(auto-fit, minmax(min(28rem, 100%), 1fr));
}
.charts .wide {
  grid-column: 1 / -1;
  min-inline-size: 0;
}
.charts > :deep(*) {
  min-inline-size: 0;
}
.people {
  margin-block-start: 1.5rem;
}
.heat-note {
  margin-block: 0.4rem 0;
}
/* The period table keeps to its own box: it scrolls down and sideways inside it, with the header row
   and the period column held in place (and the totals row at the foot), so neither a long range nor a
   phone's width makes the page long or wide. */
.period-scroll {
  max-block-size: min(30rem, 70vh);
  overflow: auto;
  overscroll-behavior: contain;
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.5rem;
}
.period-scroll:focus-visible {
  outline: 2px solid var(--p-primary-color);
  outline-offset: 2px;
}
.period-table {
  inline-size: 100%;
  font-size: 0.9rem;
  border-collapse: separate;
  border-spacing: 0;
}
.period-table th,
.period-table td {
  padding: 0.5rem 0.8rem;
  text-align: start;
  white-space: nowrap;
  background: var(--p-content-background);
  border-block-end: 1px solid var(--p-content-border-color);
}
.period-table td {
  font-variant-numeric: tabular-nums;
}
.period-table thead th {
  position: sticky;
  inset-block-start: 0;
  z-index: 2;
  font-weight: 600;
}
.period-table tbody th,
.period-table tfoot th {
  position: sticky;
  inset-inline-start: 0;
  z-index: 1;
  font-weight: 500;
  /* a hairline on the edge the other columns slide under */
  border-inline-end: 1px solid var(--p-content-border-color);
}
.period-table thead th:first-child {
  inset-inline-start: 0;
  z-index: 3;
  border-inline-end: 1px solid var(--p-content-border-color);
}
.period-table tfoot th,
.period-table tfoot td {
  position: sticky;
  inset-block-end: 0;
  z-index: 2;
  font-weight: 600;
  border-block-end: 0;
  border-block-start: 1px solid var(--p-content-border-color);
}
.period-table tfoot th {
  z-index: 3;
}
.two {
  display: grid;
  gap: var(--app-gap);
  grid-template-columns: repeat(auto-fit, minmax(min(26rem, 100%), 1fr));
  align-items: start;
}
.currency-title {
  margin: 1rem 0 0.5rem;
  font-size: 1rem;
}
.per-currency:first-of-type .currency-title {
  margin-block-start: 0;
}
.empty {
  margin-block: 2rem;
  text-align: center;
}
@media print {
  .exports,
  .view-switch,
  .app-toolbar {
    display: none;
  }
}
</style>
