<script setup lang="ts">
// My codes: the customer's own discount (reward) codes and the gift codes they applied, newest first, a page at a time. A discount code
// is shown whole, with a button that copies it; a gift code is never kept by the store, so it is its last characters and what it
// added to the wallet. Core answers for the signed-in customer only: there is nothing here to ask for another's.
import { computed, onMounted, ref } from 'vue'
import CopyField from '../../components/CopyField.vue'
import { errorKey } from '../../errors'
import { fmtDate, fmtMoney, fmtNum } from '../../fmt'
import { isolate } from '../../format'
import { lang, planName, tr, type Key } from '../../i18n'
import { fail } from '../../store'
import { codesApi, type Code, type CodesPage } from './api'

const SIZE = 10

const data = ref<CodesPage | null>(null)
const page = ref(1)
const busy = ref(false)
const error = ref('')

const pages = computed(() => (data.value ? Math.max(1, Math.ceil(data.value.total / SIZE)) : 1))

async function load(p: number) {
  error.value = ''
  busy.value = true
  try {
    data.value = await codesApi.page(p, SIZE)
    page.value = p
  } catch (e) {
    if (!fail(e)) error.value = tr(errorKey(e))
  } finally {
    busy.value = false
  }
}
onMounted(() => load(1))

// The state's word and look: the same badges as a service's status.
const stateKeys: Record<Code['state'], Key> = {
  unused: 'codes.state.unused',
  used: 'codes.state.used',
  expired: 'codes.state.expired',
  off: 'codes.state.off',
}
const stateLook: Record<Code['state'], string> = { unused: 'active', used: '', expired: 'expired', off: 'disabled' }

/** what a discount code takes off, and where it works */
function worth(c: Code): string {
  const value = c.amount ? fmtMoney(c.amount, c.currency ?? 'IRT', c.scale) : tr('codes.percent', { n: fmtNum(c.percent ?? 0) })
  const only = c.only_plan ? planName(c.only_plan, lang.value) : ''
  return only ? tr('codes.off_plan', { value, plan: isolate(only) }) : tr('codes.off_any', { value })
}

/** when it ends, was used or was switched off */
function when(c: Code): string {
  switch (c.state) {
    case 'unused':
      return c.expires_at ? tr('codes.when.unused', { date: fmtDate(c.expires_at) }) : tr('codes.when.unused_open')
    case 'used':
      return c.used_at ? tr('codes.when.used', { date: fmtDate(c.used_at) }) : ''
    case 'expired':
      return c.expires_at ? tr('codes.when.expired', { date: fmtDate(c.expires_at) }) : ''
    case 'off':
      return tr('codes.when.off')
    default:
      return ''
  }
}

/** the purchase that gave a discount code */
function from(c: Code): string {
  const date = fmtDate(c.received_at)
  const plan = c.plan ? planName(c.plan, lang.value) : ''
  return plan ? tr('codes.from_plan', { plan: isolate(plan), date }) : tr('codes.from_none', { date })
}
</script>

<template>
  <section>
    <h1>{{ tr('codes.title') }}</h1>
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load(page)">{{ tr('common.retry') }}</button></p>
    <p v-else-if="data === null" class="muted">{{ tr('common.loading') }}</p>
    <p v-else-if="!data.codes.length && page === 1" class="muted">{{ tr('codes.empty') }}</p>
    <template v-else>
      <p class="muted">{{ tr('codes.intro') }}</p>
      <ul class="list">
        <li v-for="c in data.codes" :key="c.id" class="card code" :data-kind="c.kind" :data-state="c.state">
          <!-- a gift code: only its last characters are known -->
          <template v-if="c.kind === 'gift'">
            <div class="row between">
              <strong>{{ tr('codes.gift_tail', { tail: isolate(c.tail ?? '') }) }}</strong>
              <span class="badge">{{ tr('codes.state.gift_used') }}</span>
            </div>
            <p>{{ tr('codes.gift_amount', { amount: isolate(fmtMoney(c.amount ?? 0, c.currency ?? 'IRT', c.scale)) }) }}</p>
            <p class="muted small">{{ tr('codes.when.gift', { date: fmtDate(c.used_at ?? c.received_at) }) }}</p>
          </template>
          <!-- a discount code: whole, to copy -->
          <template v-else>
            <CopyField :label="tr('codes.kind_reward')" :value="c.code ?? ''" />
            <div class="row between">
              <strong>{{ worth(c) }}</strong>
              <span v-if="stateKeys[c.state]" class="badge" :class="stateLook[c.state]">{{ tr(stateKeys[c.state]) }}</span>
            </div>
            <p v-if="when(c)" class="muted small">{{ when(c) }}</p>
            <p class="muted small">{{ from(c) }}</p>
          </template>
        </li>
      </ul>
      <nav v-if="pages > 1" class="row between" :aria-label="tr('codes.pages')">
        <button class="btn secondary" type="button" :disabled="busy || page <= 1" @click="load(page - 1)">{{ tr('codes.prev') }}</button>
        <span class="muted small">{{ tr('codes.page', { page: fmtNum(page), pages: fmtNum(pages) }) }}</span>
        <button class="btn secondary" type="button" :disabled="busy || page >= pages" @click="load(page + 1)">{{ tr('codes.next') }}</button>
      </nav>
    </template>
  </section>
</template>
