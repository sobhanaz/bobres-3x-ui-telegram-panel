<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import ToggleSwitch from 'primevue/toggleswitch'
import Checkbox from 'primevue/checkbox'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import { api } from '../api'
import { errorText } from '../errors'
import { asciiNumber, bytes, currencies, decimalOf, gbOf, money, num, validAmount, type Lang } from '../format'
import type { PlanItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()

const plans = ref<PlanItem[]>([])
const loading = ref(true)
const loadError = ref('')
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    plans.value = (await api<{ items: PlanItem[] }>('GET', '/plans')).items
  } catch (e) {
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

// The editor: traffic in GB, the price in major units, as staff type them.
interface Form {
  id: string
  name_fa: string
  name_en: string
  kind: string
  days: string
  gb: string
  price: string
  currency: string
  enabled: boolean
  is_trial: boolean
  is_topup: boolean
  sort: string
}
const blank = (): Form => ({ id: '', name_fa: '', name_en: '', kind: 'both', days: '30', gb: '', price: '', currency: 'IRT',
  enabled: true, is_trial: false, is_topup: false, sort: '0' })
const form = ref<Form>(blank())
const editing = ref(false)
const busy = ref(false)
const formError = ref('')
const formDetail = ref('')

function edit(p?: PlanItem) {
  formError.value = ''
  formDetail.value = ''
  if (!p) {
    form.value = blank()
  } else {
    form.value = {
      id: p.id, name_fa: p.name.fa ?? '', name_en: p.name.en ?? '', kind: p.kind,
      days: p.duration_days ? String(p.duration_days) : '', gb: p.traffic_bytes ? gbOf(p.traffic_bytes) : '',
      price: decimalOf(p.price), currency: p.price.currency, enabled: p.enabled, is_trial: p.is_trial,
      is_topup: p.is_topup, sort: String(p.sort),
    }
  }
  editing.value = true
}

const kinds = computed(() => [
  { label: t('plan.kind.time'), value: 'time' },
  { label: t('plan.kind.traffic'), value: 'traffic' },
  { label: t('plan.kind.both'), value: 'both' },
])
const needsDays = computed(() => form.value.kind !== 'traffic' && !form.value.is_topup)
const needsGB = computed(() => form.value.kind !== 'time' || form.value.is_topup)
const valid = computed(() => {
  const f = form.value
  const price = asciiNumber(f.price)
  return (
    (f.name_fa.trim() || f.name_en.trim()) &&
    validAmount(price || '0', f.currency) &&
    (!f.is_trial || Number(price || 0) === 0) &&
    (!needsDays.value || /^\d{1,4}$/.test(asciiNumber(f.days))) &&
    (!needsGB.value || (/^\d{1,6}(\.\d{1,3})?$/.test(asciiNumber(f.gb)) && Number(asciiNumber(f.gb)) > 0))
  )
})

async function save() {
  const f = form.value
  busy.value = true
  formError.value = ''
  const body = {
    name: { fa: f.name_fa.trim(), en: f.name_en.trim() },
    kind: f.is_topup ? 'traffic' : f.kind,
    duration_days: needsDays.value ? Number(asciiNumber(f.days)) : null,
    traffic_gb: needsGB.value ? asciiNumber(f.gb) : '',
    price: asciiNumber(f.price) || '0',
    currency: f.currency,
    enabled: f.enabled,
    is_trial: f.is_trial,
    is_topup: f.is_topup,
    sort: Number(asciiNumber(f.sort)) || 0,
  }
  try {
    await api(f.id ? 'PUT' : 'POST', f.id ? `/plans/${f.id}` : '/plans', body)
    editing.value = false
    toast.add({ severity: 'success', summary: t('plan.saved'), life: 3000 })
    await load()
  } catch (e) {
    const err = errorText(e, t)
    formError.value = err.text
    formDetail.value = err.detail
  } finally {
    busy.value = false
  }
}

// Turning a plan on or off keeps everything else.
async function toggle(p: PlanItem, enabled: boolean) {
  try {
    await api('PUT', `/plans/${p.id}`, {
      name: p.name, kind: p.kind, duration_days: p.duration_days, traffic_gb: p.traffic_bytes ? gbOf(p.traffic_bytes) : '',
      price: decimalOf(p.price), currency: p.price.currency, enabled, is_trial: p.is_trial, is_topup: p.is_topup, sort: p.sort,
    })
    p.enabled = enabled
  } catch (e) {
    toast.add({ severity: 'error', summary: errorText(e, t).text, life: 5000 })
    await load()
  }
}
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">{{ t('section.plans.title') }}</h1>
    <Button :label="t('plan.new')" icon="pi pi-plus" @click="edit()" />
  </div>
  <Message v-if="loadError" severity="error" :closable="false">{{ loadError }}</Message>
  <p class="app-muted intro">{{ t('plan.intro') }}</p>

  <DataTable :value="plans" :loading="loading" data-key="id" size="small" scrollable row-hover>
    <template #empty>{{ t('plan.none') }}</template>
    <Column :header="t('plan.name')">
      <template #body="{ data: p }">
        <div><bdi>{{ p.name[lang] || p.name.en || p.name.fa }}</bdi></div>
        <div class="tags">
          <Tag v-if="p.is_trial" :value="t('plan.trial')" severity="info" />
          <Tag v-if="p.is_topup" :value="t('plan.topup')" severity="secondary" />
        </div>
      </template>
    </Column>
    <Column :header="t('plan.duration')">
      <template #body="{ data: p }">
        <span v-if="p.duration_days">{{ t('plan.days', { n: num(p.duration_days, lang) }) }}</span>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
    <Column :header="t('service.traffic')">
      <template #body="{ data: p }">
        <span v-if="p.traffic_bytes">{{ bytes(p.traffic_bytes, lang) }}</span>
        <span v-else class="app-muted">{{ t('plan.unlimited') }}</span>
      </template>
    </Column>
    <Column :header="t('plan.price')">
      <template #body="{ data: p }">{{ p.price.amount ? money(p.price, lang) : t('plan.free') }}</template>
    </Column>
    <Column :header="t('plan.sales')">
      <template #body="{ data: p }">{{ num(p.sales, lang) }}</template>
    </Column>
    <Column :header="t('plan.on_sale')">
      <template #body="{ data: p }">
        <ToggleSwitch :model-value="p.enabled" :aria-label="t('plan.on_sale')" @update:model-value="(v: boolean) => toggle(p, v)" />
      </template>
    </Column>
    <Column>
      <template #body="{ data: p }">
        <Button icon="pi pi-pencil" text rounded :aria-label="t('common.edit')" @click="edit(p)" />
      </template>
    </Column>
  </DataTable>

  <Dialog v-model:visible="editing" :header="form.id ? t('plan.edit') : t('plan.new')" modal :style="{ inlineSize: 'min(36rem, 94vw)' }" :draggable="false">
    <form class="app-form" @submit.prevent="save">
      <div class="app-row">
        <label>
          <span>{{ t('plan.name_fa') }}</span>
          <InputText v-model="form.name_fa" dir="rtl" maxlength="100" />
        </label>
        <label>
          <span>{{ t('plan.name_en') }}</span>
          <InputText v-model="form.name_en" dir="ltr" maxlength="100" />
        </label>
      </div>
      <label class="app-check">
        <Checkbox v-model="form.is_topup" binary />
        <span>{{ t('plan.topup_hint') }}</span>
      </label>
      <label v-if="!form.is_topup">
        <span>{{ t('plan.kind_label') }}</span>
        <Select v-model="form.kind" :options="kinds" option-label="label" option-value="value" />
      </label>
      <div class="app-row">
        <label v-if="needsDays">
          <span>{{ t('plan.duration_days') }}</span>
          <InputText v-model="form.days" inputmode="numeric" dir="ltr" />
        </label>
        <label v-if="needsGB">
          <span>{{ t('plan.traffic_gb') }}</span>
          <InputText v-model="form.gb" inputmode="decimal" dir="ltr" />
        </label>
      </div>
      <div class="app-row">
        <label>
          <span>{{ t('plan.price') }}</span>
          <InputText v-model="form.price" inputmode="decimal" dir="ltr" :disabled="form.is_trial" />
        </label>
        <label>
          <span>{{ t('common.currency') }}</span>
          <Select v-model="form.currency" :options="currencies.map((c) => ({ value: c, label: t('currency.' + c) }))" option-label="label" option-value="value" />
        </label>
        <label>
          <span>{{ t('plan.sort') }}</span>
          <InputText v-model="form.sort" inputmode="numeric" dir="ltr" />
        </label>
      </div>
      <label v-if="!form.is_topup" class="app-check">
        <Checkbox v-model="form.is_trial" binary @update:model-value="(v: boolean) => v && (form.price = '0')" />
        <span>{{ t('plan.trial_hint') }}</span>
      </label>
      <label class="app-check">
        <ToggleSwitch v-model="form.enabled" />
        <span>{{ t('plan.on_sale') }}</span>
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
.intro {
  margin-block: 0 var(--app-gap);
}
.tags {
  display: flex;
  gap: 0.35rem;
  margin-block-start: 0.2rem;
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
