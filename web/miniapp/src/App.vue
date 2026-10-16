<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { Plan, Service } from './api'
import { tr, type Key } from './i18n'
import { boot, state } from './store'
import { onBack, openLink, openTelegram } from './telegram'
import CheckoutView from './views/CheckoutView.vue'
import PlansView from './views/PlansView.vue'
import ServicesView from './views/ServicesView.vue'
import WalletView from './views/WalletView.vue'

type Tab = 'store' | 'services' | 'wallet'
interface Buying { plan: Plan; type: 'new' | 'renew' | 'traffic_topup'; subscriptionId?: string }

const tab = ref<Tab>('store')
const buying = ref<Buying | null>(null)
// a renew / top-up started from a service waits here for the plan choice
const choosing = ref<{ type: 'renew' | 'traffic_topup'; service: Service } | null>(null)

const tabs: Tab[] = ['store', 'services', 'wallet']
const gateText = computed(() => (state.gate ? tr(('gate.' + state.gate) as Key) : ''))
const bot = computed(() => state.me?.brand.bot_url ?? '')
const support = computed(() => state.me?.brand.support_url ?? '')

function pick(plan: Plan) {
  if (choosing.value) buying.value = { plan, type: choosing.value.type, subscriptionId: choosing.value.service.id }
  else buying.value = { plan, type: 'new' }
}
function inServices() {
  buying.value = null
  choosing.value = null
  tab.value = 'services'
}
function home() {
  buying.value = null
  choosing.value = null
}

// Telegram's back button steps out of a purchase or a plan choice.
let off = () => {}
watch(
  () => [buying.value, choosing.value],
  () => {
    off()
    off = buying.value || choosing.value ? onBack(() => (buying.value ? (buying.value = null) : (choosing.value = null))) : () => {}
  },
)
onBeforeUnmount(() => off())
onMounted(boot)
</script>

<template>
  <p v-if="!state.booted" class="muted center" role="status">{{ tr('common.loading') }}</p>

  <main v-else-if="state.gate" class="screen gate" role="alert">
    <p>{{ gateText }}</p>
    <button v-if="bot && state.gate !== 'banned'" class="btn" type="button" @click="openTelegram(bot)">{{ tr('common.open_bot') }}</button>
    <button v-if="support && state.gate === 'banned'" class="btn secondary" type="button" @click="openLink(support)">{{ tr('common.support') }}</button>
  </main>

  <template v-else>
    <CheckoutView
      v-if="buying"
      :key="buying.plan.id + buying.type"
      :plan="buying.plan"
      :type="buying.type"
      :subscription-id="buying.subscriptionId"
      @close="home"
      @services="inServices"
    />
    <main v-else-if="choosing" class="screen">
      <PlansView :mode="choosing.type" @pick="pick" />
      <button class="btn secondary" type="button" @click="choosing = null">{{ tr('common.back') }}</button>
    </main>
    <main v-else class="screen">
      <PlansView v-if="tab === 'store'" mode="new" @pick="pick" />
      <ServicesView v-else-if="tab === 'services'" @renew="(s) => (choosing = { type: 'renew', service: s })" @topup="(s) => (choosing = { type: 'traffic_topup', service: s })" />
      <WalletView v-else />
    </main>

    <nav v-if="!buying && !choosing" class="tabs" aria-label="Main">
      <button v-for="t in tabs" :key="t" type="button" :aria-current="tab === t ? 'page' : undefined" @click="tab = t">
        {{ tr(('tab.' + t) as Key) }}
      </button>
    </nav>
  </template>
</template>
