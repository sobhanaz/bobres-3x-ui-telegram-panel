<script setup lang="ts">
// The reseller section of the Mini App (docs/17-mini-app.md): five tabs, the
// panel, buying for a customer, the customers' services, the customers who
// joined through the invite link, and the statement. Buying, renewing and
// adding traffic go through the shop's own checkout: this screen asks the app
// (which shows the checkout) with an event, and the app brings the reseller
// back here afterwards.
import type { Plan, Service } from '../../api'
import { tr } from '../../i18n'
import BuyView from './BuyView.vue'
import CustomersView from './CustomersView.vue'
import PanelView from './PanelView.vue'
import ServicesView from './ServicesView.vue'
import StatementView from './StatementView.vue'
import { showServices, tabs, ui } from './state'

defineEmits<{
  close: []
  /** buy a new service for a customer, named label (maybe empty) */
  buy: [purchase: { plan: Plan; type: 'new'; label: string }]
  renew: [service: Service]
  topup: [service: Service]
}>()
</script>

<template>
  <section class="rs">
    <h1>{{ tr('reseller.title') }}</h1>
    <div class="rs-tabs" role="tablist" :aria-label="tr('reseller.title')">
      <button v-for="t in tabs" :key="t" type="button" role="tab" :aria-selected="ui.tab === t" @click="ui.tab = t">
        {{ tr('reseller.tab.' + t) }}
      </button>
    </div>

    <div role="tabpanel">
      <PanelView v-if="ui.tab === 'panel'" @buy="ui.tab = 'buy'" @services="showServices" @customers="ui.tab = 'customers'" />
      <BuyView v-else-if="ui.tab === 'buy'" @buy="(plan, label) => $emit('buy', { plan, type: 'new', label })" />
      <ServicesView v-else-if="ui.tab === 'services'" @renew="(s) => $emit('renew', s)" @topup="(s) => $emit('topup', s)" />
      <CustomersView v-else-if="ui.tab === 'customers'" />
      <StatementView v-else />
    </div>
  </section>
</template>
