<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Card from 'primevue/card'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import SelectButton from 'primevue/selectbutton'
import ToggleSwitch from 'primevue/toggleswitch'
import Message from 'primevue/message'
import UserLink from '../components/UserLink.vue'
import { api } from '../api'
import { errorText } from '../errors'
import { asciiNumber, currencies, date, money, num, validAmount, type Lang } from '../format'
import type { DiscountItem, ReferrerItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()

const codes = ref<DiscountItem[]>([])
const codesLoading = ref(true)
const codesError = ref('')
async function loadCodes() {
  codesLoading.value = true
  codesError.value = ''
  try {
    codes.value = (await api<{ items: DiscountItem[] }>('GET', '/discounts')).items
  } catch (e) {
    codesError.value = errorText(e, t).text
  } finally {
    codesLoading.value = false
  }
}

const reward = ref('')
const savedReward = ref(0)
const top = ref<ReferrerItem[]>([])
const refError = ref('')
async function loadReferrals() {
  refError.value = ''
  try {
    const res = await api<{ reward_percent: number; top: ReferrerItem[] }>('GET', '/referrals')
    savedReward.value = res.reward_percent
    reward.value = String(res.reward_percent)
    top.value = res.top
  } catch (e) {
    refError.value = errorText(e, t).text
  }
}
onMounted(() => {
  void loadCodes()
  void loadReferrals()
})

const rewardValid = computed(() => /^\d{1,3}$/.test(asciiNumber(reward.value)) && Number(asciiNumber(reward.value)) <= 100)
const savingReward = ref(false)
async function saveReward() {
  savingReward.value = true
  try {
    const res = await api<{ reward_percent: number }>('PUT', '/referrals', { reward_percent: Number(asciiNumber(reward.value)) })
    savedReward.value = res.reward_percent
    toast.add({ severity: 'success', summary: res.reward_percent ? t('discount.reward_saved') : t('discount.reward_off'), life: 4000 })
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 5000 })
  } finally {
    savingReward.value = false
  }
}

// A new code (or new terms for an existing one).
interface Form {
  code: string
  type: 'percent' | 'amount'
  value: string
  currency: string
  maxUses: string
  days: string
  enabled: boolean
}
const blank = (): Form => ({ code: '', type: 'percent', value: '', currency: 'IRT', maxUses: '', days: '', enabled: true })
const form = ref<Form>(blank())
const editing = ref(false)
const busy = ref(false)
const formError = ref('')
const formDetail = ref('')
const types = computed(() => [
  { label: t('discount.percent'), value: 'percent' },
  { label: t('discount.fixed'), value: 'amount' },
])
const valid = computed(() => {
  const f = form.value
  const v = asciiNumber(f.value)
  return (
    /^[A-Za-z0-9_-]{3,32}$/.test(f.code.trim()) &&
    (f.type === 'percent' ? /^\d{1,3}$/.test(v) && +v >= 1 && +v <= 100 : validAmount(v, f.currency) && +v > 0) &&
    /^(\d{1,6})?$/.test(asciiNumber(f.maxUses)) &&
    /^(\d{1,4})?$/.test(asciiNumber(f.days))
  )
})
function newCode() {
  form.value = blank()
  formError.value = ''
  editing.value = true
}
async function save() {
  const f = form.value
  busy.value = true
  formError.value = ''
  const days = Number(asciiNumber(f.days))
  const body = {
    code: f.code.trim(),
    percent: f.type === 'percent' ? Number(asciiNumber(f.value)) : null,
    amount: f.type === 'amount' ? asciiNumber(f.value) : '',
    currency: f.type === 'amount' ? f.currency : '',
    max_uses: f.maxUses ? Number(asciiNumber(f.maxUses)) : null,
    expires_at: days ? Math.floor(Date.now() / 1000) + days * 86400 : null,
    enabled: f.enabled,
  }
  try {
    await api('POST', '/discounts', body)
    editing.value = false
    toast.add({ severity: 'success', summary: t('discount.saved'), life: 3000 })
    await loadCodes()
  } catch (e) {
    const err = errorText(e, t)
    formError.value = err.text
    formDetail.value = err.detail
  } finally {
    busy.value = false
  }
}
async function toggle(d: DiscountItem, enabled: boolean) {
  try {
    await api('PUT', `/discounts/${encodeURIComponent(d.code)}/enabled`, { enabled })
    d.enabled = enabled
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 5000 })
  }
}
const expired = (d: DiscountItem) => d.expires_at !== null && d.expires_at * 1000 < Date.now()
const usedUp = (d: DiscountItem) => d.max_uses !== null && d.used >= d.max_uses
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.discounts.title') }}</h1>
    <Button :label="t('discount.new')" icon="pi pi-plus" @click="newCode" />
  </div>
  <Message v-if="codesError" severity="error" :closable="false">{{ codesError }}</Message>

  <Card>
    <template #title>{{ t('discount.codes') }}</template>
    <template #content>
      <DataTable :value="codes" :loading="codesLoading" data-key="code" size="small" scrollable>
        <template #empty>{{ t('discount.none') }}</template>
        <Column :header="t('discount.code')">
          <template #body="{ data: d }"><span class="app-ltr app-mono code">{{ d.code }}</span></template>
        </Column>
        <Column :header="t('discount.value')">
          <template #body="{ data: d }">
            {{ d.percent ? t('discount.percent_off', { n: num(d.percent, lang) }) : money(d.amount!, lang) }}
          </template>
        </Column>
        <Column :header="t('discount.uses')">
          <template #body="{ data: d }">
            {{ d.max_uses ? t('discount.used_of', { used: num(d.used, lang), max: num(d.max_uses, lang) }) : num(d.used, lang) }}
            <div v-if="usedUp(d)" class="app-muted small">{{ t('discount.used_up') }}</div>
          </template>
        </Column>
        <Column :header="t('discount.expires')">
          <template #body="{ data: d }">
            <span v-if="d.expires_at" :class="{ 'app-muted': expired(d) }">{{ date(d.expires_at, lang) }}</span>
            <span v-else class="app-muted">{{ t('discount.never') }}</span>
            <div v-if="expired(d)" class="app-muted small">{{ t('discount.expired') }}</div>
          </template>
        </Column>
        <Column :header="t('discount.active')">
          <template #body="{ data: d }">
            <ToggleSwitch :model-value="d.enabled" :aria-label="t('discount.active')" @update:model-value="(v: boolean) => toggle(d, v)" />
          </template>
        </Column>
      </DataTable>
    </template>
  </Card>

  <Card class="referrals">
    <template #title>{{ t('discount.referrals') }}</template>
    <template #content>
      <Message v-if="refError" severity="error" :closable="false">{{ refError }}</Message>
      <p class="app-muted">{{ t('discount.referrals_intro') }}</p>
      <form class="reward" @submit.prevent="saveReward">
        <label>
          <span>{{ t('discount.reward') }}</span>
          <InputText v-model="reward" inputmode="numeric" dir="ltr" class="pct" />
        </label>
        <Button type="submit" :label="t('common.save')" :loading="savingReward" :disabled="!rewardValid" />
        <span class="app-muted">{{ savedReward ? t('discount.reward_now', { n: num(savedReward, lang) }) : t('discount.reward_none') }}</span>
      </form>
      <h3>{{ t('discount.top') }}</h3>
      <DataTable :value="top" data-key="user.id" size="small" scrollable>
        <template #empty>{{ t('discount.top_none') }}</template>
        <Column :header="t('common.customer')">
          <template #body="{ data: r }"><UserLink :user="r.user" /></template>
        </Column>
        <Column :header="t('discount.invited')">
          <template #body="{ data: r }">{{ num(r.invited, lang) }}</template>
        </Column>
        <Column :header="t('discount.rewarded')">
          <template #body="{ data: r }">{{ num(r.rewarded, lang) }}</template>
        </Column>
        <Column :header="t('discount.earned')">
          <template #body="{ data: r }">
            <div v-for="m in r.earned" :key="m.currency">{{ money(m, lang) }}</div>
            <span v-if="!r.earned.length" class="app-muted">—</span>
          </template>
        </Column>
      </DataTable>
    </template>
  </Card>

  <Dialog v-model:visible="editing" :header="t('discount.new')" modal :style="{ inlineSize: 'min(32rem, 94vw)' }" :draggable="false">
    <form class="app-form" @submit.prevent="save">
      <label>
        <span>{{ t('discount.code') }}</span>
        <InputText v-model="form.code" dir="ltr" maxlength="32" autocomplete="off" class="upper" />
        <small class="app-muted">{{ t('discount.code_hint') }}</small>
      </label>
      <SelectButton v-model="form.type" :options="types" option-label="label" option-value="value" :allow-empty="false" />
      <div class="app-row">
        <label>
          <span>{{ form.type === 'percent' ? t('discount.percent') : t('common.amount') }}</span>
          <InputText v-model="form.value" :inputmode="form.type === 'percent' ? 'numeric' : 'decimal'" dir="ltr" />
        </label>
        <label v-if="form.type === 'amount'">
          <span>{{ t('common.currency') }}</span>
          <Select v-model="form.currency" :options="currencies.map((c) => ({ value: c, label: t('currency.' + c) }))" option-label="label" option-value="value" />
        </label>
      </div>
      <div class="app-row">
        <label>
          <span>{{ t('discount.max_uses') }}</span>
          <InputText v-model="form.maxUses" inputmode="numeric" dir="ltr" :placeholder="t('discount.unlimited')" />
        </label>
        <label>
          <span>{{ t('discount.days') }}</span>
          <InputText v-model="form.days" inputmode="numeric" dir="ltr" :placeholder="t('discount.never')" />
        </label>
      </div>
      <small class="app-muted">{{ t('discount.once_per_customer') }}</small>
      <label class="app-check">
        <ToggleSwitch v-model="form.enabled" />
        <span>{{ t('discount.active') }}</span>
      </label>
      <Message v-if="formError" severity="error" :closable="false">
        {{ formError }}
        <div v-if="formDetail" class="app-ltr detail">{{ formDetail }}</div>
      </Message>
      <div class="buttons">
        <Button type="button" :label="t('app.cancel')" severity="secondary" outlined @click="editing = false" />
        <Button type="submit" :label="t('common.save')" :loading="busy" :disabled="!valid" />
      </div>
    </form>
  </Dialog>
</template>

<style scoped>
.referrals {
  margin-block-start: var(--app-gap);
}
.code {
  font-weight: 700;
}
.small {
  font-size: 0.8rem;
}
.reward {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  align-items: flex-end;
  margin-block-end: 1rem;
}
.reward label {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.pct {
  inline-size: 6rem;
}
h3 {
  font-size: 1rem;
  margin-block: 0.5rem;
}
.upper :deep(input),
.upper {
  text-transform: uppercase;
}
.buttons {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
.detail {
  font-size: 0.85rem;
}
</style>
