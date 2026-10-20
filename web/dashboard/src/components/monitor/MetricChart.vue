<script setup lang="ts">
import { computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { LineChart, type GapSeries } from '../charts'
import { lastKnown, onGrid, peak, unitFormat, type Unit } from '../../monitor'
import { feedKey, useFmt, type MetricDef } from './feed'

// One chart of one or several metrics over the range the tab shows. It reads the series the tab fetched, puts
// each on the time grid (a moment nobody sampled stays a gap, never zero) and draws them with the chart kit.
const props = defineProps<{
  title: string
  subtitle?: string
  defs: MetricDef[]
  unit: Unit
  /** a fixed top of the vertical axis (100 for a percent) */
  yMax?: number
  height?: number
}>()
const { t } = useI18n()
const feed = inject(feedKey)
if (!feed) throw new Error('MetricChart needs the Monitoring tab around it')
const { lang } = useFmt()

const format = computed(() => unitFormat(props.unit, lang.value))
const axisFormat = computed(() => unitFormat(props.unit, lang.value, true))

const series = computed<GapSeries[]>(() =>
  props.defs
    .filter((d) => feed.answers.value[d.metric])
    .map((d) => ({ key: d.metric, label: d.label, slot: d.slot, dashed: d.dashed, area: d.area, values: onGrid(feed.grid.value.times, feed.grid.value.step, feed.answers.value[d.metric].points) })),
)
const measured = computed(() => series.value.some((s) => s.values.some((v) => v !== null)))
// no sample at all: the chart says so instead of drawing an empty frame of axes
const categories = computed(() => (measured.value ? feed.grid.value.categories : []))

const summary = computed(() => {
  const first = series.value.find((s) => lastKnown(s.values) !== null)
  if (!first) return props.title
  return t('monitor.chart.summary', {
    title: props.title,
    series: first.label,
    last: format.value(lastKnown(first.values)!),
    peak: format.value(peak(first.values)!),
    range: t('monitor.range.' + feed.grid.value.range),
  })
})
</script>

<template>
  <LineChart
    class="metric-chart"
    :title="title"
    :subtitle="subtitle"
    :categories="categories"
    :ticks="feed.grid.value.ticks"
    :series="series"
    :format="format"
    :axis-format="axisFormat"
    :y-min="unit === 'percent' ? 0 : undefined"
    :y-max="yMax"
    :height="height ?? 200"
    :loading="feed.loading.value"
    :empty-text="t('monitor.chart.empty')"
    :corner="t('monitor.chart.time')"
    :summary="summary"
    :markers="false"
  />
</template>
