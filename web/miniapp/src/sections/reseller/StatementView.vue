<script setup lang="ts">
// The reseller's statement: their wallet movements, newest first, with the
// commissions they earned and the payments they made to the store.
import { onMounted, ref } from 'vue'
import { fmtDateTime, fmtMoney, ledgerKey } from '../../fmt'
import { tr } from '../../i18n'
import { fail } from '../../store'
import { errText, resellerApi, type StatementEntry, type StatementPage } from './api'
import Pager from './Pager.vue'

const data = ref<StatementPage | null>(null)
const error = ref('')
const loading = ref(false)
const page = ref(1)

async function load() {
  error.value = ''
  loading.value = true
  try {
    const got = await resellerApi.statement(page.value)
    if (!got.entries.length && page.value > 1) { // past the end
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

/** balance words a balance after a movement: a negative one is what the reseller owes, written as a debt (a bare minus reads wrongly in Persian). */
function balance(e: StatementEntry): string {
  return e.balance_after < 0
    ? tr('reseller.panel.debt', { amount: fmtMoney(-e.balance_after, e.currency, e.scale) })
    : fmtMoney(e.balance_after, e.currency, e.scale)
}

/** kind words a ledger entry: the shop's names for the usual ones, ours for a reseller's. */
function kind(e: StatementEntry): string {
  switch (e.kind) {
    case 'commission':
    case 'settlement':
    case 'referral':
    case 'trial_grant':
      return tr('reseller.kind.' + e.kind)
  }
  return tr(ledgerKey(e.kind))
}
</script>

<template>
  <div class="rs-grid">
    <h2>{{ tr('reseller.statement.title') }}</h2>
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-else-if="!data" class="muted">{{ tr('common.loading') }}</p>
    <p v-else-if="!data.entries.length" class="muted">{{ tr('reseller.statement.empty') }}</p>
    <ul v-else class="list">
      <li v-for="(e, i) in data.entries" :key="e.at + ':' + i" class="card row between">
        <span>
          {{ kind(e) }}<br />
          <span class="muted small">{{ fmtDateTime(e.at) }}</span><br />
          <span class="muted small">{{ tr('reseller.statement.balance_after', { amount: balance(e) }) }}</span>
        </span>
        <strong :class="e.amount < 0 ? 'neg' : 'pos'" dir="ltr">{{ (e.amount > 0 ? '+' : '') + fmtMoney(e.amount, e.currency, e.scale) }}</strong>
      </li>
    </ul>
    <Pager v-if="data" :page="page" :has-more="data.has_more" :busy="loading" @go="go" />
  </div>
</template>
