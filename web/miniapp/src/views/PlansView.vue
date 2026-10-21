<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import type { Plan } from '../api'
import { errorKey } from '../errors'
import { tr } from '../i18n'
import { fmtMoney, groupNameOf, groupTextOf, nameOf, planFacts } from '../fmt'
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

// A new purchase from a store with plan groups goes group first: the customer picks a group, reads its text, then picks a
// plan of it (renewals and traffic packages stay a flat list). The plans with no group are under "Other plans".
const OTHER = '-'
const sections = computed(() => {
  if (props.mode && props.mode !== 'new') return null
  const list = shown()
  const known = new Set(state.groups.map((g) => g.id))
  const groups = state.groups
    .map((g) => ({ group: g, plans: list.filter((p) => p.group === g.id) }))
    .filter((s) => s.plans.length > 0)
  if (!groups.length) return null
  return { groups, other: list.filter((p) => !p.group || !known.has(p.group)) }
})
const open = ref<string | null>(null)
// the group being read: its heading, text and plans (null while the list of groups is shown)
const current = computed(() => {
  const s = sections.value
  if (!s || open.value === null) return null
  if (open.value === OTHER) return s.other.length ? { name: tr('plans.other'), text: '', plans: s.other } : null
  const g = s.groups.find((x) => x.group.id === open.value)
  return g ? { name: groupNameOf(g.group), text: groupTextOf(g.group), plans: g.plans } : null
})
const plans = computed(() => (sections.value ? (current.value?.plans ?? []) : shown()))
</script>

<template>
  <section>
    <h2 v-if="mode && mode !== 'new'">{{ tr('services.choose_plan') }}</h2>
    <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="load">{{ tr('common.retry') }}</button></p>
    <p v-else-if="state.plans === null" class="muted">{{ tr('common.loading') }}</p>
    <p v-else-if="!shown().length" class="muted">{{ mode && mode !== 'new' ? tr('services.no_plans') : tr('plans.empty') }}</p>
    <template v-else-if="sections && !current">
      <h2>{{ tr('plans.choose_group') }}</h2>
      <ul class="list">
        <li v-for="s in sections.groups" :key="s.group.id">
          <button class="btn secondary group" type="button" @click="open = s.group.id">{{ groupNameOf(s.group) }}</button>
        </li>
        <li v-if="sections.other.length">
          <button class="btn secondary group" type="button" @click="open = OTHER">{{ tr('plans.other') }}</button>
        </li>
      </ul>
    </template>
    <template v-else>
      <template v-if="current">
        <button class="link" type="button" @click="open = null">{{ tr('plans.all_groups') }}</button>
        <h2>{{ current.name }}</h2>
        <p v-if="current.text" class="group-text">{{ current.text }}</p>
      </template>
      <ul class="list">
        <li v-for="p in plans" :key="p.id" class="card plan">
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
    </template>
  </section>
</template>
