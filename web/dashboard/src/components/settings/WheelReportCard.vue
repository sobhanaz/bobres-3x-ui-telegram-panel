<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Card from 'primevue/card'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import { api } from '../../api'
import { errorText } from '../../errors'
import { localDate, money, num, type Lang } from '../../format'
import type { WheelReportView } from '../../types'

// The daily wheel's small report, under its settings card: today against the daily budget, and one row for each day of the last month that had a
// spin. Core works it out in the store's time zone (the day is the store's day).
const props = defineProps<{ refreshKey?: number }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const report = ref<WheelReportView | null>(null)
const error = ref('')
async function load() {
  error.value = ''
  try {
    report.value = await api<WheelReportView>('GET', '/wheel/report')
  } catch (e) {
    error.value = errorText(e, t).text
  }
}
onMounted(load)
watch(() => props.refreshKey, load)

const day = (ymd: string) => localDate(ymd, lang.value)
</script>

<template>
  <Card class="wheel-report" data-wheel-report>
    <template #title>{{ t('wheel.report.title') }}</template>
    <template #subtitle>{{ t('wheel.report.desc') }}</template>
    <template #content>
      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
      <Skeleton v-else-if="!report" height="6rem" />
      <template v-else>
        <Message v-if="!report.on" severity="info" :closable="false" class="off">{{ t('wheel.report.off') }}</Message>
        <dl class="app-facts today">
          <dt>{{ t('wheel.report.today') }}</dt>
          <dd>
            {{ day(report.day) }} · {{ t('wheel.report.spins') }}: {{ num(report.today.spins, lang) }} · {{ t('wheel.report.winners') }}:
            {{ num(report.today.winners, lang) }} · {{ t('wheel.report.paid') }}: {{ money(report.today.paid, lang) }}
          </dd>
          <dt>{{ t('wheel.report.budget') }}</dt>
          <dd>{{ money(report.budget, lang) }}</dd>
          <dt>{{ t('wheel.report.remaining') }}</dt>
          <dd>{{ money(report.remaining, lang) }}</dd>
        </dl>
        <p v-if="!report.days.length" class="app-muted">{{ t('wheel.report.empty') }}</p>
        <table v-else class="days">
          <thead>
            <tr>
              <th scope="col">{{ t('wheel.report.day') }}</th>
              <th scope="col">{{ t('wheel.report.spins') }}</th>
              <th scope="col">{{ t('wheel.report.winners') }}</th>
              <th scope="col">{{ t('wheel.report.paid') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="d in report.days" :key="d.day">
              <th scope="row">{{ day(d.day) }}</th>
              <td>{{ num(d.spins, lang) }}</td>
              <td>{{ num(d.winners, lang) }}</td>
              <td>{{ money(d.paid, lang) }}</td>
            </tr>
          </tbody>
          <tfoot>
            <tr>
              <th scope="row">{{ t('wheel.report.total') }}</th>
              <td>{{ num(report.total.spins, lang) }}</td>
              <td>{{ num(report.total.winners, lang) }}</td>
              <td>{{ money(report.total.paid, lang) }}</td>
            </tr>
          </tfoot>
        </table>
      </template>
    </template>
  </Card>
</template>

<style scoped>
.off {
  margin-block-end: 0.75rem;
}
.today {
  margin-block-end: 0.75rem;
}
.days {
  inline-size: 100%;
  border-collapse: collapse;
  font-variant-numeric: tabular-nums;
}
.days th,
.days td {
  padding: 0.35rem 0.5rem;
  text-align: start;
  border-block-end: 1px solid var(--p-content-border-color);
}
.days tfoot th,
.days tfoot td {
  font-weight: 700;
}
</style>
