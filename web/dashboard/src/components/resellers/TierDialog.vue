<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Dialog from 'primevue/dialog'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { decimalOf, money, num, planName, type Lang } from '../../format'
import {
  blankTier, MAX_COMMISSION, MAX_DISCOUNT, percentOf, planPriceFor, priceablePlans, tierBody, tierFormOf, tierPrice, tierProblems, TIER_NAME_MAX,
} from '../../resellers'
import type { PlanItem, TierItem } from '../../types'

// Create or edit a reseller tier: its name, the discount off store prices,
// the commission on customers, and an optional fixed price per plan (which
// beats the discount). Each plan shows what a reseller on the tier pays.
const props = defineProps<{ tier: TierItem | null; plans: PlanItem[]; plansReady: boolean; plansError: string }>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ saved: []; stale: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

const form = ref(blankTier())
const busy = ref(false)
const error = ref('')
const detail = ref('')
const fieldErrors = ref<Record<string, string>>({})
// Core refused this form as out of date: no save until it is reopened.
const stale = ref(false)

watch(visible, (v) => {
  if (!v) return
  form.value = props.tier ? tierFormOf(props.tier, props.plans) : blankTier()
  stale.value = false
  error.value = ''
  detail.value = ''
  fieldErrors.value = {}
})

const rows = computed(() => priceablePlans(props.plans))
const problems = computed(() => tierProblems(form.value, props.plans))
const issue = (f: string) => problems.value.includes(f) || !!fieldErrors.value[f]
const discount = computed(() => percentOf(form.value.discount, MAX_DISCOUNT) ?? 0)
const fromDiscount = (p: PlanItem) => ({ amount: tierPrice(p.price.amount, discount.value), currency: p.price.currency })
const pays = (p: PlanItem) => {
  const r = planPriceFor(p, form.value)
  return { amount: r.amount, currency: p.price.currency, fixed: r.fixed }
}
// Without the plans a save would drop every fixed price: wait for them.
const canSave = computed(() => !stale.value && props.plansReady && !props.plansError && problems.value.length === 0)

async function save() {
  if (!canSave.value) return
  busy.value = true
  error.value = ''
  detail.value = ''
  fieldErrors.value = {}
  const body = tierBody(form.value, props.plans)
  try {
    if (props.tier) await api('PUT', `/reseller-tiers/${props.tier.id}`, { ...body, version: props.tier.version })
    else await api('POST', '/reseller-tiers', body)
    visible.value = false
    emit('saved')
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && props.tier) {
      // Changed since this dialog opened: the page reads the tiers again, so
      // reopening shows the new values; this form keeps its old version.
      stale.value = true
      error.value = t('tier.stale')
      detail.value = e.message
      emit('stale')
      return
    }
    if (e instanceof ApiError && e.status === 400 && typeof e.data.field === 'string') fieldErrors.value = { [e.data.field]: e.message }
    const err = errorText(e, t)
    error.value = err.text
    detail.value = err.detail
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    v-model:visible="visible"
    :header="props.tier ? t('tier.edit') : t('tier.new')"
    modal
    :style="{ inlineSize: 'min(48rem, 96vw)' }"
    :draggable="false"
    class="tier-dialog"
  >
    <form class="app-form" @submit.prevent="save">
      <div class="app-row">
        <label>
          <span>{{ t('tier.name') }}</span>
          <InputText v-model="form.name" :maxlength="TIER_NAME_MAX" dir="auto" :invalid="issue('name')" class="name" />
          <small v-if="fieldErrors.name" class="app-ltr field-error">{{ fieldErrors.name }}</small>
        </label>
        <label>
          <span>{{ t('tier.discount') }}</span>
          <InputText v-model="form.discount" inputmode="numeric" dir="ltr" maxlength="3" :invalid="issue('discount_percent')" class="discount" />
          <small class="app-muted">{{ t('tier.discount_hint', { max: num(MAX_DISCOUNT, lang) }) }}</small>
        </label>
        <label>
          <span>{{ t('tier.commission') }}</span>
          <InputText v-model="form.commission" inputmode="numeric" dir="ltr" maxlength="3" :invalid="issue('commission_percent')" class="commission" />
          <small class="app-muted">{{ t('tier.commission_hint', { max: num(MAX_COMMISSION, lang) }) }}</small>
        </label>
      </div>

      <div class="app-field">
        <span>{{ t('tier.prices') }}</span>
        <small class="app-muted">{{ t('tier.prices_hint') }}</small>
        <Message v-if="props.plansError" severity="error" :closable="false">{{ t('tier.plans_error') }} {{ props.plansError }}</Message>
        <DataTable v-else :value="rows" :loading="!props.plansReady" data-key="id" size="small" scrollable class="prices">
          <template #empty>{{ t('tier.no_plans') }}</template>
          <Column :header="t('common.plan')">
            <template #body="{ data: p }">
              <bdi>{{ planName(p.name, lang) }}</bdi>
              <div class="tags">
                <Tag v-if="p.is_topup" :value="t('plan.topup')" severity="secondary" />
                <Tag v-if="!p.enabled" :value="t('tier.plan_off')" severity="secondary" />
              </div>
            </template>
          </Column>
          <Column :header="t('tier.col_store')" body-class="app-nowrap">
            <template #body="{ data: p }">{{ money(p.price, lang) }}</template>
          </Column>
          <Column :header="t('tier.col_fixed')">
            <template #body="{ data: p }">
              <InputText
                v-model="form.prices[p.id]"
                inputmode="decimal"
                dir="ltr"
                autocomplete="off"
                size="small"
                :placeholder="decimalOf(fromDiscount(p))"
                :invalid="issue('prices.' + p.id)"
                :aria-label="t('tier.fixed_for', { plan: planName(p.name, lang) })"
                :class="'fixed fixed-' + p.id"
              />
            </template>
          </Column>
          <Column :header="t('tier.col_pays')" body-class="app-nowrap">
            <template #body="{ data: p }">
              <strong :class="'pays pays-' + p.id">{{ money(pays(p), lang) }}</strong>
              <div class="app-muted small">{{ pays(p).fixed ? t('tier.fixed') : t('tier.from_discount') }}</div>
            </template>
          </Column>
        </DataTable>
      </div>

      <Message v-if="error" severity="error" :closable="false">
        {{ error }}
        <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
      </Message>
      <div class="buttons">
        <Button type="button" :label="t('app.cancel')" severity="secondary" outlined @click="visible = false" />
        <Button type="submit" :label="t('common.save')" :loading="busy" :disabled="!canSave" class="save" />
      </div>
    </form>
  </Dialog>
</template>

<style scoped>
.tags {
  display: flex;
  gap: 0.35rem;
  margin-block-start: 0.2rem;
}
.fixed {
  inline-size: 9rem;
}
.small {
  font-size: 0.8rem;
}
.buttons {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
.detail {
  font-size: 0.85rem;
}
.field-error {
  color: var(--p-red-500);
}
</style>
