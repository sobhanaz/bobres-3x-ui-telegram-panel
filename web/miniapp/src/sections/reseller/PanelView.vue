<script setup lang="ts">
// The reseller's panel: tier, discount and commission, the balance with the
// credit and what can be spent, how many services are in each group, the
// customers who joined through the invite link, and the link to share.
import { computed, onMounted, ref } from 'vue'
import { fmtDate, fmtMoney, fmtNum } from '../../fmt'
import { isolate } from '../../format'
import { tr } from '../../i18n'
import { fail } from '../../store'
import { copyText, openTelegram } from '../../telegram'
import { errText, resellerApi, type Filter, type Money, type Panel } from './api'
import { ApiError } from '../../api'

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

// The monthly target: the tiers in order, the bar to the highest one, what is left to the next, and the days left in the month.
const targets = computed(() => panel.value?.targets ?? null)
const targetTop = computed(() => Math.max(1, ...(targets.value?.tiers ?? []).map((t) => t.threshold)))
const targetNext = computed(() => targets.value?.tiers.find((t) => !t.reached) ?? null)
const targetDays = computed(() => (targets.value ? Math.max(0, Math.ceil((targets.value.ends_at * 1000 - Date.now()) / 86_400_000)) : 0))
// a date core sends as "2026-10-01" is a day, not an instant: noon UTC keeps every zone on that day
const targetDay = (iso: string) => fmtDate(Date.parse(iso + 'T12:00:00Z') / 1000)

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

// The config prefix: chosen once (asked about before it is final), then only the owner changes it.
const prefixRe = /^[a-z0-9]{2,10}$/
const prefixInput = ref('')
const prefixAsk = ref(false) // the confirmation is showing
const prefixError = ref('')
const prefixBusy = ref(false)
const prefixWord = computed(() => prefixInput.value.trim().toLowerCase())
const prefixOk = computed(() => prefixRe.test(prefixWord.value))
async function savePrefix() {
  prefixBusy.value = true
  prefixError.value = ''
  try {
    panel.value = await resellerApi.setPrefix(prefixWord.value)
    prefixAsk.value = false
  } catch (e) {
    prefixAsk.value = false
    if (e instanceof ApiError && (e.code === 'invalid' || e.code === 'state')) {
      // taken, a shape only core knows, or already set (an old screen): read the panel again
      prefixError.value = e.code === 'state' ? '' : tr('reseller.err.prefix_bad')
      if (e.code === 'state') await load()
    } else if (!fail(e)) {
      prefixError.value = errText(e)
    }
  } finally {
    prefixBusy.value = false
  }
}

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

      <section v-if="targets" class="card rs-targets" data-targets>
        <strong>{{ tr('targets.title') }}</strong>
        <p class="muted small">{{ tr('targets.range', { from: targetDay(targets.from), to: targetDay(targets.to) }) }}</p>
        <div class="rs-kv">
          <span class="muted">{{ tr('targets.sold') }}</span>
          <strong class="big">{{ fmtNum(targets.sold) }}</strong>
        </div>
        <progress class="rs-bar" :max="targetTop" :value="Math.min(targets.sold, targetTop)" :aria-label="tr('targets.title')"></progress>
        <ul class="rs-tiers">
          <li v-for="t in targets.tiers" :key="t.threshold" :class="{ done: t.paid }">
            <span aria-hidden="true">{{ t.paid ? '✅' : '⬜' }}</span>
            {{ tr('targets.tier', { n: fmtNum(t.threshold), bonus: fmtMoney(t.bonus, targets.currency, targets.scale) }) }}
            <span class="rs-sr">{{ tr(t.paid ? 'targets.paid' : 'targets.not_paid') }}</span>
          </li>
        </ul>
        <p v-if="targetNext" class="ok">{{ tr('targets.next', { bonus: fmtMoney(targetNext.bonus, targets.currency, targets.scale), n: fmtNum(targets.remaining) }) }}</p>
        <p v-else class="ok">{{ tr('targets.done') }}</p>
        <p v-if="targets.bonus_paid > 0" class="muted small">{{ tr('targets.earned', { amount: fmtMoney(targets.bonus_paid, targets.currency, targets.scale) }) }}</p>
        <p class="muted small">{{ tr('targets.ends', { n: fmtNum(targetDays) }) }}</p>
      </section>

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

      <section class="card" data-prefix>
        <strong>{{ tr('reseller.panel.prefix') }}</strong>
        <template v-if="panel.config_prefix">
          <p class="mono" dir="ltr">{{ isolate(panel.config_prefix) }}-…</p>
          <p class="muted small">{{ tr('reseller.panel.prefix_locked') }}</p>
        </template>
        <template v-else>
          <p class="muted small">{{ tr('reseller.panel.prefix_hint') }}</p>
          <template v-if="!prefixAsk">
            <input v-model="prefixInput" class="input" dir="ltr" maxlength="10" autocapitalize="none" autocomplete="off" spellcheck="false" :aria-label="tr('reseller.panel.prefix')" />
            <p class="muted small">{{ tr('reseller.panel.prefix_rule') }}</p>
            <button class="btn" type="button" :disabled="!prefixOk" @click="prefixAsk = true">{{ tr('reseller.panel.prefix_set') }}</button>
          </template>
          <template v-else>
            <p>{{ tr('reseller.panel.prefix_confirm', { prefix: isolate(prefixWord) }) }}</p>
            <div class="row wrap">
              <button class="btn" type="button" :disabled="prefixBusy" @click="savePrefix">{{ tr('reseller.panel.prefix_yes') }}</button>
              <button class="btn secondary" type="button" :disabled="prefixBusy" @click="prefixAsk = false">{{ tr('reseller.action.cancel') }}</button>
            </div>
          </template>
          <p v-if="prefixError" class="error" role="alert">{{ prefixError }}</p>
        </template>
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
