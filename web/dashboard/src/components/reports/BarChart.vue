<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { localDate, money, moneyCompact, type Lang } from '../../format'
import { periodLabel, type Bar, type Group } from '../../reports'

// One currency's revenue per period as plain bars, scaled to that currency
// alone (a chart never mixes two currencies on one axis). Bars are laid out
// in the reading direction, so the oldest period is at the start in both
// languages. The figures are also in the table below the charts; the chart
// itself is one image with a text summary.
const props = defineProps<{ bars: Bar[]; currency: string; group: Group; title: string }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const peak = computed(() => props.bars.reduce((a, b) => (b.value > a.value ? b : a), props.bars[0] ?? { date: '', value: 0, pct: 0, partial: false }))
const name = computed(() => (te('currency.' + props.currency) ? t('currency.' + props.currency) : props.currency))
const label = (b: Bar) => periodLabel(b.date, props.group, lang.value, t)
const summary = computed(() =>
  peak.value.value > 0
    ? t('reports.chart.peak', { currency: name.value, amount: money({ amount: peak.value.value, currency: props.currency }, lang.value), period: label(peak.value) })
    : t('reports.chart.zero', { currency: name.value }),
)
const tip = (b: Bar) => `${label(b)}: ${money({ amount: b.value, currency: props.currency }, lang.value)}` + (b.partial ? ` (${t('reports.period.partial')})` : '')
// A bar with revenue is never thinner than a hair line, so it shows.
const height = (b: Bar) => (b.value > 0 ? Math.max(b.pct, 2) : 0) + '%'
const first = computed(() => (props.bars.length ? localDate(props.bars[0].date, lang.value, 'short') : ''))
const last = computed(() => (props.bars.length > 1 ? localDate(props.bars[props.bars.length - 1].date, lang.value, 'short') : ''))
</script>

<template>
  <figure class="chart" :data-currency="currency">
    <figcaption class="caption">
      <strong>{{ title }}</strong>
      <span class="app-muted">{{ name }}</span>
    </figcaption>
    <div class="frame">
      <div class="scale app-muted">{{ moneyCompact({ amount: peak.value, currency }, lang) }}</div>
      <div class="plot" :class="{ dense: bars.length > 120 }" role="img" :aria-label="summary">
        <div v-for="b in bars" :key="b.date" class="slot" :title="tip(b)">
          <div class="bar" :class="{ partial: b.partial }" :style="{ height: height(b) }" />
        </div>
      </div>
    </div>
    <div class="axis app-muted" aria-hidden="true">
      <span>{{ first }}</span>
      <span>{{ last }}</span>
    </div>
  </figure>
</template>

<style scoped>
/* 400 daily bars must shrink to the card, never widen it: a grid or flex
   item does not go below its content unless it says min-inline-size: 0. */
.chart {
  min-inline-size: 0;
  margin: 0;
  overflow: hidden;
}
.caption {
  display: flex;
  gap: 0.5rem;
  align-items: baseline;
  justify-content: space-between;
  margin-block-end: 0.5rem;
}
.frame {
  display: grid;
  gap: 0.25rem;
  grid-template-columns: minmax(0, 1fr);
  min-inline-size: 0;
}
.scale {
  font-size: 0.8rem;
}
.plot {
  display: flex;
  align-items: flex-end;
  gap: 1px;
  min-inline-size: 0;
  block-size: 9rem;
  border-block-end: 1px solid var(--p-content-border-color);
}
.plot.dense {
  gap: 0;
}
.slot {
  display: flex;
  flex: 1 1 0;
  align-items: flex-end;
  min-inline-size: 0;
  block-size: 100%;
}
.bar {
  inline-size: 100%;
  background: var(--p-primary-color);
  border-start-start-radius: 2px;
  border-start-end-radius: 2px;
}
.bar.partial {
  opacity: 0.5;
}
.slot:hover .bar {
  background: var(--p-primary-hover-color, var(--p-primary-color));
  opacity: 0.85;
}
.axis {
  display: flex;
  justify-content: space-between;
  margin-block-start: 0.25rem;
  font-size: 0.8rem;
}
</style>
