<script setup lang="ts">
// The people who joined through the reseller's invite link, newest first:
// who they are on Telegram, when they joined, how many orders they paid, what
// they spent and what it earned the reseller. Core sends nothing else about
// them, and nobody who did not join through this link.
import { onMounted, ref } from 'vue'
import { fmtDate, fmtMoney, fmtNum } from '../../fmt'
import { isolate } from '../../format'
import { tr } from '../../i18n'
import { fail } from '../../store'
import { errText, resellerApi, type Customer, type CustomerPage, type Money } from './api'
import Pager from './Pager.vue'

const data = ref<CustomerPage | null>(null)
const error = ref('')
const loading = ref(false)
const page = ref(1)

async function load() {
  error.value = ''
  loading.value = true
  try {
    const got = await resellerApi.customers(page.value)
    if (!got.customers.length && page.value > 1 && got.total > 0) { // past the end
      page.value = 1
      await load()
      return
    }
    data.value = got
  } catch (e) {
    if (!fail(e)) error.value = errText(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

function go(p: number) {
  page.value = p
  void load()
}

const who = (c: Customer) => (c.username ? isolate('@' + c.username) : tr('reseller.customers.id', { id: String(c.telegram_id) }))
const money = (ms: Money[]) => (ms.length ? ms.map((m) => fmtMoney(m.amount, m.currency, m.scale)).join(' · ') : fmtMoney(0, 'IRT', 0))
const pages = () => Math.max(1, Math.ceil((data.value?.total ?? 0) / (data.value?.size || 1)))
</script>

<template>
  <div class="rs-grid">
    <h2>{{ tr('reseller.customers.title') }}<template v-if="data"> · {{ fmtNum(data.total) }}</template></h2>
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-else-if="!data" class="muted">{{ tr('common.loading') }}</p>
    <p v-else-if="data.total === 0" class="muted">{{ tr('reseller.customers.empty') }}</p>
    <ul v-else class="list">
      <li v-for="c in data.customers" :key="c.telegram_id" class="card">
        <div class="row between">
          <strong>{{ who(c) }}</strong>
          <span class="muted small">{{ tr('reseller.customers.joined', { date: fmtDate(c.joined_at) }) }}</span>
        </div>
        <p class="muted">{{ tr('reseller.customers.orders', { n: fmtNum(c.paid_orders) }) }} · {{ tr('reseller.customers.spent', { amount: money(c.spent) }) }}</p>
        <p class="pos">{{ tr('reseller.customers.commission', { amount: money(c.commission) }) }}</p>
      </li>
    </ul>
    <Pager v-if="data" :page="data.page" :pages="pages()" :busy="loading" @go="go" />
  </div>
</template>
