<script setup lang="ts">
// Buying a service for a customer: the plans at the reseller's own price (the
// shop's /plans answers with it), then a name for the service, and on to the
// shop's checkout. Nothing is priced here: the checkout and core price the
// order from the plan.
import { onMounted, ref } from 'vue'
import type { Plan } from '../../api'
import { errorKey } from '../../errors'
import { fmtMoney, fmtNum, nameOf, planFacts } from '../../fmt'
import { tr } from '../../i18n'
import { fail, loadPlans, state } from '../../store'
import { cleanLabel, MAX_LABEL } from './text'

const emit = defineEmits<{ buy: [plan: Plan, label: string] }>()
const error = ref('') // the plans could not be loaded
const nameError = ref('') // the name is not one core would take
const picked = ref<Plan | null>(null)
const label = ref('')

async function load() {
  error.value = ''
  try {
    await loadPlans()
  } catch (e) {
    if (!fail(e)) error.value = tr(errorKey(e))
  }
}
onMounted(load)

// Traffic packages are bought for a service, from its page; these are new services.
const shown = (): Plan[] => (state.plans ?? []).filter((p) => !p.topup)

function pick(p: Plan) {
  picked.value = p
  nameError.value = ''
}

function go() {
  if (!picked.value) return
  const name = cleanLabel(label.value)
  if (name === null) {
    nameError.value = tr('reseller.err.bad_text')
    return
  }
  nameError.value = ''
  emit('buy', picked.value, name)
}
</script>

<template>
  <div class="rs-grid">
    <h2>{{ tr('reseller.buy.title') }}</h2>
    <p class="muted">{{ tr('reseller.buy.hint') }}</p>
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-else-if="state.plans === null" class="muted">{{ tr('common.loading') }}</p>
    <p v-else-if="!shown().length" class="muted">{{ tr('plans.empty') }}</p>
    <ul v-else class="list">
      <li v-for="p in shown()" :key="p.id" class="card" :class="{ 'rs-picked': picked?.id === p.id }">
        <div class="row between">
          <div>
            <strong>{{ nameOf(p) }}</strong>
            <p class="muted">{{ planFacts(p) }}</p>
            <p v-if="p.list_price > p.price" class="muted small"><s>{{ tr('plans.was', { price: fmtMoney(p.list_price, p.currency, p.scale) }) }}</s></p>
          </div>
          <button class="btn" type="button" :aria-pressed="picked?.id === p.id" @click="pick(p)">{{ fmtMoney(p.price, p.currency, p.scale) }}</button>
        </div>
        <form v-if="picked?.id === p.id" class="rs-grid" @submit.prevent="go">
          <label class="field">
            <span>{{ tr('reseller.buy.name') }}</span>
            <input v-model="label" type="text" :maxlength="MAX_LABEL" autocomplete="off" enterkeyhint="done" />
            <span class="muted small">{{ tr('reseller.buy.name_hint', { max: fmtNum(MAX_LABEL) }) }}</span>
          </label>
          <p v-if="nameError" class="error" role="alert">{{ nameError }}</p>
          <button class="btn" type="submit">{{ tr('reseller.buy.continue') }}</button>
        </form>
      </li>
    </ul>
  </div>
</template>
