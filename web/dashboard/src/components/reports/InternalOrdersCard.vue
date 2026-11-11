<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Card from 'primevue/card'
import UserLink from '../UserLink.vue'
import { money, num, type Lang } from '../../format'
import type { InternalCurrency, InternalReport } from '../../internalOrders'

// What owner accounts bought in the range: left out of revenue and every figure on this page, shown here on its own, with a line for each
// account. Amounts of different currencies are never added.
const props = defineProps<{ report: InternalReport }>()
const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const currencyName = (c: string) => (te('currency.' + c) ? t('currency.' + c) : c)
const m = (amount: number, currency: string) => money({ amount, currency }, lang.value)
const line = (c: InternalCurrency) => t('reports.internal.line', { n: num(c.orders, lang.value), amount: m(c.amount, c.currency) })
const refunded = (c: InternalCurrency) => t('reports.internal.refunded', { n: num(c.refunds, lang.value), amount: m(c.refunds_amount, c.currency) })
const empty = computed(() => props.report.orders === 0 && !props.report.currencies.some((c) => c.refunds > 0))
</script>

<template>
  <Card class="stat internal-card app-no-print">
    <template #title>{{ t('reports.internal.title') }}</template>
    <template #subtitle>{{ t('reports.internal.desc') }}</template>
    <template #content>
      <p v-if="!report.enabled" class="app-muted small internal-off">{{ t('reports.internal.off') }}</p>
      <p v-if="empty" class="app-muted internal-none">{{ t('reports.internal.none') }}</p>
      <template v-else>
        <div class="big internal-count">{{ num(report.orders, lang) }}</div>
        <div class="app-muted">{{ t('reports.internal.orders') }}</div>
        <dl class="internal-currencies">
          <template v-for="c in report.currencies" :key="c.currency">
            <dt>{{ currencyName(c.currency) }}</dt>
            <dd class="internal-currency" :data-currency="c.currency">
              <span class="line">{{ line(c) }}</span>
              <span v-if="c.refunds" class="app-muted small refunded">{{ refunded(c) }}</span>
            </dd>
          </template>
        </dl>
        <table class="internal-accounts">
          <caption class="app-muted small">{{ t('reports.internal.accounts') }}</caption>
          <tbody>
            <tr v-for="a in report.accounts" :key="a.user?.id ?? ''" class="internal-account">
              <th scope="row"><UserLink :user="a.user" /></th>
              <td class="n">{{ num(a.orders, lang) }}</td>
              <td class="amounts">
                <span v-for="c in a.currencies" :key="c.currency" class="amount">{{ m(c.amount, c.currency) }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </template>
    </template>
  </Card>
</template>

<style scoped>
.big {
  font-size: 1.6rem;
  font-weight: 700;
}
.internal-currencies {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 0.25rem 0.75rem;
  margin: 0.75rem 0;
}
.internal-currencies dt {
  font-weight: 600;
}
.internal-currencies dd {
  margin: 0;
  display: flex;
  flex-wrap: wrap;
  gap: 0.15rem 0.6rem;
}
.internal-accounts {
  width: 100%;
  border-collapse: collapse;
}
.internal-accounts caption {
  text-align: start;
  padding-block-end: 0.25rem;
}
.internal-accounts th,
.internal-accounts td {
  padding-block: 0.25rem;
  text-align: start;
  font-weight: 400;
}
.internal-accounts .amounts {
  display: flex;
  flex-wrap: wrap;
  gap: 0 0.6rem;
}
.small {
  font-size: 0.85rem;
}
</style>
