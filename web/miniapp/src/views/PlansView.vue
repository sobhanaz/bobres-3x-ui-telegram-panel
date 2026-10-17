<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { Plan } from '../api'
import { errorKey } from '../errors'
import { tr } from '../i18n'
import { fmtMoney, nameOf, planFacts } from '../fmt'
import { fail, loadPlans, state } from '../store'

const props = defineProps<{ mode?: 'new' | 'renew' | 'traffic_topup' }>()
const emit = defineEmits<{ pick: [plan: Plan] }>()
const error = ref('')

async function load() {
  error.value = ''
  try {
    await loadPlans()
  } catch (e) {
    if (!fail(e)) error.value = tr(errorKey(e))
  }
}
onMounted(load)

// Which plans fit: top-up plans only for adding traffic, the others for buying and renewing.
const shown = (): Plan[] =>
  (state.plans ?? []).filter((p) => (props.mode === 'traffic_topup' ? p.topup : !p.topup))
</script>

<template>
  <section>
    <h2 v-if="mode && mode !== 'new'">{{ tr('services.choose_plan') }}</h2>
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-else-if="state.plans === null" class="muted">{{ tr('common.loading') }}</p>
    <p v-else-if="!shown().length" class="muted">{{ mode && mode !== 'new' ? tr('services.no_plans') : tr('plans.empty') }}</p>
    <ul v-else class="list">
      <li v-for="p in shown()" :key="p.id" class="card plan">
        <div>
          <strong>{{ nameOf(p) }}</strong>
          <p class="muted">{{ planFacts(p) }}</p>
          <p v-if="p.list_price > p.price" class="muted small"><s>{{ tr('plans.was', { price: fmtMoney(p.list_price, p.currency, p.scale) }) }}</s></p>
        </div>
        <button class="btn" type="button" @click="emit('pick', p)">
          {{ fmtMoney(p.price, p.currency, p.scale) }}
        </button>
      </li>
    </ul>
  </section>
</template>
