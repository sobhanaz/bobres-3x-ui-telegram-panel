<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import Message from 'primevue/message'
import Select from 'primevue/select'
import SelectButton from 'primevue/selectbutton'
import Skeleton from 'primevue/skeleton'
import BarChart from '../components/reports/BarChart.vue'
import DateField from '../components/reports/DateField.vue'
import { api, query } from '../api'
import { DATE_MAX, DATE_MIN, formatInput, todayIn } from '../calendar'
import { fieldId } from '../settings'
import { useAuth } from '../stores/auth'
import { errorText } from '../errors'
import { isolate, localDate, money, num, percent, planName, type Lang } from '../format'
import {
  bars,
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

const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const auth = useAuth()

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

// The calendar starts as the person left it (kept per browser, like the language and the theme), else the
// language's own. It decides every date of the page: how they are written, typed and picked.
const sel = reactive<Selection>({ range: 'last_30_days', group: 'day', calendar: initialCalendar(lang.value), from: '', to: '' })
watch(() => sel.calendar, rememberCalendar)
const summary = ref<ReportSummary | null>(null)
const plans = ref<ReportPlan[]>([])
const methods = ref<ReportMethod[]>([])
const loading = ref(false)
const failure = reactive({ text: '', detail: '' })
let seq = 0

// The summary first: it says which dates, in which time zone, "today" or
// "this month" came to, and the other two ask for exactly those, so the three
// answers always describe the same days.
async function load() {
  if (sel.range === 'custom' && !validCustom(sel.from, sel.to)) return
  const mine = ++seq
  loading.value = true
  failure.text = failure.detail = ''
  try {
    const s = await api<ReportSummary>('GET', '/reports/summary' + query(reportParams(sel)))
    const pin = query({ from: s.from, to: s.to, tz: s.tz })
    const [p, m] = await Promise.all([
      api<{ plans: ReportPlan[] }>('GET', '/reports/plans' + pin),
      api<{ methods: ReportMethod[] }>('GET', '/reports/methods' + pin),
    ])
    if (mine !== seq) return
    summary.value = s
    plans.value = p.plans
    methods.value = m.methods
  } catch (e) {
    if (mine !== seq) return
    const r = errorText(e, t)
    failure.text = r.text
    failure.detail = r.detail
    summary.value = null // numbers of another range must not stay under this one's title
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
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.reports.title') }}</h1>
    <div v-if="summary" class="exports">
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
  <p class="app-muted hint">{{ t('reports.hint') }}</p>
  <p v-if="daily" class="app-muted hint daily-summary" :class="daily.enabled ? 'is-on' : 'is-off'">
    {{ daily.enabled ? t('reports.daily.on', { time: isolate(daily.time), tz: isolate(daily.tz) }) : t('reports.daily.off') }}
    <RouterLink v-if="auth.can('settings.write')" :to="dailyLink" class="daily-link">{{ t('reports.daily.link') }}</RouterLink>
  </p>

  <div class="app-toolbar">
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
  <p v-if="problemText" class="custom-problem" role="alert">{{ problemText }}</p>
  <p v-if="summary" class="app-muted covers">{{ covers }}</p>

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
      <div class="app-grid cards">
        <Card v-for="cur in currencies" :key="cur" class="stat currency-card" :data-currency="cur">
          <template #subtitle>{{ currencyName(cur) }}</template>
          <template #content>
            <template v-if="totals(cur)">
              <div class="big revenue">{{ m(totals(cur)!.revenue, cur) }}</div>
              <div class="app-muted">{{ t('reports.kpi.revenue') }}</div>
              <div class="line net">{{ t('reports.kpi.net', { amount: m(totals(cur)!.net, cur) }) }}</div>
              <div class="line orders">{{ t('reports.kpi.orders', { n: num(totals(cur)!.orders, lang) }, totals(cur)!.orders) }}</div>
              <div class="app-muted small split">
                {{ t('reports.kpi.split', { new: num(totals(cur)!.new_orders, lang), renew: num(totals(cur)!.renewals, lang), traffic: num(totals(cur)!.traffic_topups, lang) }) }}
              </div>
              <dl class="flows">
                <template v-for="f in flows" :key="f">
                  <dt>{{ t('reports.kpi.' + f) }}</dt>
                  <dd :class="'flow-' + f">
                    {{ m(totals(cur)![f].amount, cur) }}
                    <span class="app-muted">· {{ t('reports.kpi.' + f + '_n', { n: num(totals(cur)![f].count, lang) }, totals(cur)![f].count) }}</span>
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
            <div class="app-muted small">{{ t('reports.customers.new_hint') }}</div>
            <div class="line trials">{{ t('reports.customers.trials') }}: {{ num(summary.totals.trials, lang) }}</div>
          </template>
        </Card>
        <Card class="stat churn-card">
          <template #subtitle>{{ t('reports.churn.title') }}</template>
          <template #content>
            <div class="big churned-services">{{ num(summary.totals.churned_services, lang) }}</div>
            <div class="app-muted">{{ t('reports.churn.services') }}</div>
            <div class="app-muted small">{{ t('reports.churn.hint', { n: num(summary.churn_grace_days, lang) }, summary.churn_grace_days) }}</div>
            <div class="line churned-customers">{{ t('reports.churn.customers') }}: {{ num(summary.totals.churned_customers, lang) }}</div>
            <div class="line at-risk">{{ t('reports.churn.at_risk') }}: {{ num(summary.totals.at_risk, lang) }}</div>
          </template>
        </Card>
      </div>

      <Card v-if="currencies.length" class="block charts-card">
        <template #content>
          <div class="charts">
            <BarChart
              v-for="cur in currencies"
              :key="cur"
              :bars="bars(summary, cur)"
              :currency="cur"
              :group="summary.group"
              :calendar="summary.calendar"
              :dates="sel.calendar"
              :title="t('reports.chart.title.' + summary.group)"
            />
          </div>
          <p v-if="summary.buckets.some((b) => b.partial)" class="app-muted small">{{ t('reports.chart.partial') }}</p>
          <p class="app-muted small">{{ t('reports.calendar_note.' + summary.calendar) }}</p>
        </template>
      </Card>

      <Card class="block period-card">
        <template #title>{{ t('reports.table.title') }}</template>
        <template #content>
          <DataTable :value="summary.buckets" data-key="date" size="small" scrollable class="period-table">
            <Column :header="t('reports.table.period')">
              <template #body="{ data: b }">
                <bdi>{{ label(b.date) }}</bdi>
                <span v-if="b.partial" class="app-muted small partial"> ({{ t('reports.period.partial') }})</span>
              </template>
            </Column>
            <template v-for="cur in currencies" :key="cur">
              <Column :header="t('reports.table.revenue_in', { currency: currencyName(cur) })">
                <template #body="{ data: b }">{{ m(b.currencies[cur]?.revenue ?? 0, cur) }}</template>
              </Column>
              <Column :header="t('reports.table.orders_in', { currency: currencyName(cur) })">
                <template #body="{ data: b }">{{ num(b.currencies[cur]?.orders ?? 0, lang) }}</template>
              </Column>
            </template>
            <Column :header="t('reports.table.new_customers')">
              <template #body="{ data: b }">{{ num(b.new_customers, lang) }}</template>
            </Column>
            <Column :header="t('reports.table.trials')">
              <template #body="{ data: b }">{{ num(b.trials, lang) }}</template>
            </Column>
            <Column :header="t('reports.table.churned')">
              <template #body="{ data: b }">{{ num(b.churned_services, lang) }}</template>
            </Column>
            <Column :header="t('reports.table.at_risk')">
              <template #body="{ data: b }">{{ num(b.at_risk, lang) }}</template>
            </Column>
          </DataTable>
        </template>
      </Card>

      <div class="two">
        <Card class="block plans-card">
          <template #title>{{ t('reports.plans.title') }}</template>
          <template #content>
            <p v-if="!plans.length" class="app-muted">{{ t('reports.plans.none') }}</p>
            <section v-for="cur in currencies" v-show="byCurrency(plans, cur).length" :key="cur" class="per-currency" :data-currency="cur">
              <h3 class="currency-title">{{ currencyName(cur) }}</h3>
              <DataTable :value="byCurrency(plans, cur)" data-key="plan_id" size="small" scrollable>
                <Column :header="t('reports.plans.plan')">
                  <template #body="{ data: p }"><bdi>{{ planName(p.name, lang) }}</bdi></template>
                </Column>
                <Column field="orders" :header="t('reports.plans.orders')" sortable>
                  <template #body="{ data: p }">{{ num(p.orders, lang) }}</template>
                </Column>
                <Column field="revenue" :header="t('reports.plans.revenue')" sortable>
                  <template #body="{ data: p }">{{ m(p.revenue, cur) }}</template>
                </Column>
              </DataTable>
            </section>
          </template>
        </Card>

        <Card class="block methods-card">
          <template #title>{{ t('reports.methods.title') }}</template>
          <template #content>
            <p v-if="!methods.length" class="app-muted">{{ t('reports.methods.none') }}</p>
            <section v-for="cur in currencies" v-show="byCurrency(methods, cur).length" :key="cur" class="per-currency" :data-currency="cur">
              <h3 class="currency-title">{{ currencyName(cur) }}</h3>
              <DataTable :value="byCurrency(methods, cur)" size="small" scrollable>
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
                  <template #body="{ data: r }">{{ m(r.revenue, cur) }}</template>
                </Column>
                <Column :header="t('reports.methods.share')">
                  <template #body="{ data: r }">{{ percent(showShare(byCurrency(methods, cur), r), lang) }}</template>
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
.charts {
  display: grid;
  gap: 1.5rem;
  grid-template-columns: repeat(auto-fit, minmax(min(22rem, 100%), 1fr));
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
</style>
