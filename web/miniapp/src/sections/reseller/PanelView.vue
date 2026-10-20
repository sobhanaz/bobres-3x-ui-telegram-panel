<script setup lang="ts">
// The reseller's panel: tier, discount and commission, the balance with the
// credit and what can be spent, how many services are in each group, the
// customers who joined through the invite link, and the link to share.
import { computed, onMounted, ref } from 'vue'
import { fmtMoney, fmtNum } from '../../fmt'
import { isolate } from '../../format'
import { tr } from '../../i18n'
import { fail } from '../../store'
import { copyText, openTelegram } from '../../telegram'
import { errText, resellerApi, type Filter, type Money, type Panel } from './api'

const emit = defineEmits<{ buy: []; services: [filter: Filter]; customers: [] }>()
const panel = ref<Panel | null>(null)
const error = ref('')
const copied = ref(false)

async function load() {
  error.value = ''
  try {
    panel.value = await resellerApi.panel()
  } catch (e) {
    if (!fail(e)) error.value = errText(e)
  }
}
onMounted(load)

const money = (m: Money) => fmtMoney(m.amount, m.currency, m.scale)
const moneyList = (ms: Money[]) => (ms.length ? ms.map(money).join(' · ') : fmtMoney(0, 'IRT', 0))

const groups = computed(() => {
  const c = panel.value?.counts
  if (!c) return []
  return [
    { filter: '' as Filter, n: c.total },
    { filter: 'active' as Filter, n: c.active },
    { filter: 'expiring_soon' as Filter, n: c.expiring_soon },
    { filter: 'expired' as Filter, n: c.expired },
    { filter: 'paused' as Filter, n: c.paused },
  ]
})

async function copy() {
  try {
    await copyText(panel.value?.invite_link ?? '')
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    // no clipboard: the link stays selectable
  }
}

function share() {
  const link = panel.value?.invite_link
  if (link) openTelegram(`https://t.me/share/url?url=${encodeURIComponent(link)}&text=${encodeURIComponent(tr('reseller.panel.share_text'))}`)
}
</script>

<template>
  <div class="rs-grid">
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-else-if="!panel" class="muted">{{ tr('common.loading') }}</p>
    <template v-else>
      <section class="card">
        <strong>{{ tr('reseller.panel.tier', { tier: panel.tier }) }}</strong>
        <p class="muted">{{ tr('reseller.panel.discount', { n: fmtNum(panel.discount_percent) }) }}</p>
        <p class="muted">{{ tr('reseller.panel.commission', { n: fmtNum(panel.commission_percent) }) }}</p>
      </section>

      <section v-for="b in panel.balances" :key="b.currency" class="card" :aria-label="tr('reseller.panel.balance')">
        <div class="rs-kv">
          <span class="muted">{{ tr('reseller.panel.balance') }}</span>
          <strong class="big" :class="{ neg: b.balance < 0 }">
            {{ b.balance < 0 ? tr('reseller.panel.debt', { amount: fmtMoney(-b.balance, b.currency, b.scale) }) : fmtMoney(b.balance, b.currency, b.scale) }}
          </strong>
        </div>
        <div class="rs-kv">
          <span class="muted">{{ tr('reseller.panel.credit') }}</span>
          <span>{{ fmtMoney(b.credit_limit, b.currency, b.scale) }}</span>
        </div>
        <div class="rs-kv">
          <span class="muted">{{ tr('reseller.panel.available') }}</span>
          <strong>{{ fmtMoney(b.available, b.currency, b.scale) }}</strong>
        </div>
      </section>

      <button class="btn" type="button" @click="emit('buy')">{{ tr('reseller.panel.buy') }}</button>

      <section class="card">
        <strong>{{ tr('reseller.panel.services') }}</strong>
        <div class="rs-chips">
          <button v-for="g in groups" :key="g.filter" class="rs-chip" type="button" @click="emit('services', g.filter)">
            {{ tr('reseller.filter.' + (g.filter || 'all')) }} · {{ fmtNum(g.n) }}
          </button>
        </div>
      </section>

      <section class="card">
        <div class="rs-kv">
          <span class="muted">{{ tr('reseller.panel.customers') }}</span>
          <button class="link" type="button" @click="emit('customers')">{{ fmtNum(panel.customers) }}</button>
        </div>
        <div class="rs-kv">
          <span class="muted">{{ tr('reseller.panel.earned') }}</span>
          <strong>{{ moneyList(panel.commission_earned) }}</strong>
        </div>
      </section>

      <section class="card">
        <strong>{{ tr('reseller.panel.link') }}</strong>
        <template v-if="panel.invite_link">
          <p class="mono" dir="ltr">{{ isolate(panel.invite_link) }}</p>
          <p class="muted small">{{ tr('reseller.panel.link_hint') }}</p>
          <div class="row wrap">
            <button class="btn" type="button" @click="share">{{ tr('reseller.panel.share') }}</button>
            <button class="btn secondary" type="button" @click="copy">{{ copied ? tr('common.copied') : tr('common.copy') }}</button>
          </div>
        </template>
        <p v-else class="muted">{{ tr('reseller.panel.no_link') }}</p>
      </section>
    </template>
  </div>
</template>
