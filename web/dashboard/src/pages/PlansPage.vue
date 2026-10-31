<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Textarea from 'primevue/textarea'
import { useConfirm } from 'primevue/useconfirm'
import ToggleSwitch from 'primevue/toggleswitch'
import Checkbox from 'primevue/checkbox'
import MultiSelect from 'primevue/multiselect'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import { api } from '../api'
import { errorText } from '../errors'
import { asciiNumber, bytes, currencies, decimalOf, gbOf, isolate, listSeparator, money, num, validAmount, type Lang } from '../format'
import { choiceOf, goneInbounds, inboundOptions, inboundsBody, noUsableServer, serverNames, serverOptions, type InboundChoice } from '../servers'
import { useAuth } from '../stores/auth'
import PlanGroups from '../components/plans/PlanGroups.vue'
import DeletedPlans from '../components/plans/DeletedPlans.vue'
import AnnounceDialog from '../components/announce/AnnounceDialog.vue'
import type { InboundItem, PlanGroupItem, PlanItem, ServerItem } from '../types'

const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()
const auth = useAuth()

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
// The plan groups: a customer picks a group and reads its text before choosing one of its plans.
const groups = ref<PlanGroupItem[]>([])
const groupsReady = ref(false)
async function loadGroups() {
  try {
    groups.value = (await api<{ items: PlanGroupItem[] }>('GET', '/plan-groups')).items
    groupsReady.value = true
  } catch {
    // an older core has no groups: the plan editor then leaves the group alone
  }
  await load() // the plans' group names and the groups' counts move together
}
onMounted(() => void loadGroups())
const groupName = (id: string | null | undefined) => {
  const g = groups.value.find((x) => x.id === id)
  return g ? g.name[lang.value] || g.name.en || g.name.fa || '' : ''
}
const groupOptions = computed(() => [
  { value: '', label: t('plan.group_none') },
  ...groups.value.map((g) => ({ value: g.id, label: g.name[lang.value] || g.name.en || g.name.fa || '' })),
])

// The 3x-ui servers, for the plan's server choice (those who manage servers).
const canServers = computed(() => auth.can('servers.write'))
// Only the owner deletes a plan, and only with this button: nothing deletes one by itself.
const canDelete = computed(() => auth.can('plans.delete'))
// Announce a plan on sale (the store's channel and/or private messages): broadcasts.write.
const canAnnounce = computed(() => auth.can('broadcasts.write'))
const announcing = ref<PlanItem | null>(null)
const announceOpen = ref(false)
function openAnnounce(p: PlanItem) {
  announcing.value = p
  announceOpen.value = true
}
const servers = ref<ServerItem[]>([])
const serversReady = ref(false)
const serversError = ref('')
async function loadServers() {
  serversError.value = ''
  try {
    servers.value = (await api<{ items: ServerItem[] }>('GET', '/servers')).items
    serversReady.value = true
  } catch (e) {
    serversError.value = errorText(e, t).text
  }
}
onMounted(() => {
  if (canServers.value) void loadServers()
})
function serversText(p: PlanItem): string {
  const ids = p.server_ids ?? []
  if (!ids.length) return t('plan.servers_any')
  // Until the servers are in, their names are not known.
  if (!serversReady.value) return t('plan.servers_count', { n: num(ids.length, lang.value) }, ids.length)
  return serverNames(ids, servers.value)
    .map((n) => n || t('plan.server_removed'))
    .join(listSeparator(lang.value))
}

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
  /** the plan group's id; empty = none */
  group_id: string
  /** the reward code a purchase gives: none, a percent or an amount; the value as typed, the days it lasts, the text for the buyer */
  reward_kind: 'none' | 'percent' | 'amount'
  reward_value: string
  reward_days: string
  reward_text_fa: string
  reward_text_en: string
  /** empty = any server */
  server_ids: string[]
  /** the plan came with its servers (core knows them): they go back on save */
  has_servers: boolean
  /** server id -> the inbounds new services get there; none = every attachable one */
  inbounds: InboundChoice
  /** the plan came with its inbound choice: it goes back on save */
  has_inbounds: boolean
}
const blank = (): Form => ({ id: '', name_fa: '', name_en: '', kind: 'both', days: '30', gb: '', price: '', currency: 'IRT',
  enabled: true, is_trial: false, is_topup: false, sort: '0', group_id: '',
  reward_kind: 'none', reward_value: '', reward_days: '10', reward_text_fa: '', reward_text_en: '', server_ids: [], has_servers: false, inbounds: {}, has_inbounds: false })
const form = ref<Form>(blank())
const editing = ref(false)
const busy = ref(false)
const formError = ref('')
const formDetail = ref('')

// The reward part of the form for a plan: what it gives now (a plan that gives none shows the defaults).
function rewardForm(p: PlanItem): Pick<Form, 'reward_kind' | 'reward_value' | 'reward_days' | 'reward_text_fa' | 'reward_text_en'> {
  const r = p.reward
  if (!r) return { reward_kind: 'none', reward_value: '', reward_days: '10', reward_text_fa: '', reward_text_en: '' }
  return {
    reward_kind: r.kind,
    reward_value: r.kind === 'percent' ? String(r.percent ?? '') : r.amount ? decimalOf(r.amount) : '',
    reward_days: String(r.days),
    reward_text_fa: r.text?.fa ?? '',
    reward_text_en: r.text?.en ?? '',
  }
}

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
      is_topup: p.is_topup, sort: String(p.sort), group_id: p.group_id ?? '',
      ...rewardForm(p), server_ids: [...(p.server_ids ?? [])], has_servers: Array.isArray(p.server_ids),
      inbounds: choiceOf(p.inbounds), has_inbounds: Array.isArray(p.inbounds),
    }
  }
  if (canServers.value && serversError.value) void loadServers()
  editing.value = true
}

// The server choice shows once the servers are in; a new plan sends it, an
// edited one sends it back when core gave it (so nothing is lost).
const showServers = computed(() => canServers.value && serversReady.value)
const sendServers = computed(() => (form.value.id ? form.value.has_servers : showServers.value))
const serverChoices = computed(() =>
  serverOptions(servers.value, form.value.server_ids).map((o) => ({
    value: o.value,
    label: o.removed ? t('plan.server_removed_id', { id: o.label }) : o.off ? t('plan.server_off', { name: o.label }) : o.label,
  })),
)
const noUsable = computed(() => showServers.value && noUsableServer(servers.value, form.value.server_ids))

// The inbound choice: per server the plan can go to (the ones it names, else
// all), read from the panels when the editor is open. A server with nothing
// chosen gets every attachable inbound (what BOBRES does by default).
interface Inbounds {
  items: InboundItem[]
  error: string
  loading: boolean
}
const panelInbounds = ref<Record<string, Inbounds>>({})
async function loadInbounds(id: string) {
  const known = panelInbounds.value[id]
  if (known && (known.loading || !known.error)) return
  panelInbounds.value[id] = { items: [], error: '', loading: true }
  try {
    const r = await api<{ items: InboundItem[] }>('GET', `/servers/${id}/inbounds`)
    panelInbounds.value[id] = { items: r.items ?? [], error: '', loading: false }
  } catch (e) {
    panelInbounds.value[id] = { items: [], error: errorText(e, t).text, loading: false }
  }
}
const showInbounds = computed(() => showServers.value && (form.value.id ? form.value.has_inbounds : true))
const inboundServers = computed(() => {
  const named = form.value.server_ids
  return servers.value.filter((s) => (named.length ? named.includes(s.id) : true))
})
watch(
  () => [editing.value, showInbounds.value, inboundServers.value.map((s) => s.id).join(',')] as const,
  ([open, show]) => {
    if (!open || !show) return
    for (const s of inboundServers.value) void loadInbounds(s.id)
  },
)
function inboundChoices(id: string) {
  const p = panelInbounds.value[id]
  return inboundOptions(p?.items ?? [], form.value.inbounds[id] ?? []).map((o) => ({
    value: o.value,
    label: o.gone ? t('plan.inbound_gone_id', { id: o.value }) : o.off ? t('plan.inbound_off', { name: o.label }) : o.label,
  }))
}
function setChoice(id: string, ids: number[]) {
  form.value.inbounds = { ...form.value.inbounds, [id]: ids }
}
function goneOf(id: string): number[] {
  const p = panelInbounds.value[id]
  // Until the panel answered, nothing is known to be gone.
  if (!p || p.loading || p.error) return []
  return goneInbounds(p.items, form.value.inbounds[id] ?? [])
}

const kinds = computed(() => [
  { label: t('plan.kind.time'), value: 'time' },
  { label: t('plan.kind.traffic'), value: 'traffic' },
  { label: t('plan.kind.both'), value: 'both' },
])
const needsDays = computed(() => form.value.kind !== 'traffic' && !form.value.is_topup)
const needsGB = computed(() => form.value.kind !== 'time' || form.value.is_topup)
// A reward is nothing, a whole percent from 1 to 90, or an amount in the plan's currency, lasting 1 to 365 days; a trial and a traffic
// package give none.
const rewardValueOk = computed(() => {
  const f = form.value
  const v = asciiNumber(f.reward_value).trim()
  if (f.reward_kind === 'percent') return /^\d{1,2}$/.test(v) && Number(v) >= 1 && Number(v) <= 90
  if (f.reward_kind === 'amount') return v !== '' && validAmount(v, f.currency) && Number(v) > 0
  return true
})
const rewardDaysOk = computed(() => {
  const d = asciiNumber(form.value.reward_days).trim()
  return form.value.reward_kind === 'none' || (/^\d{1,3}$/.test(d) && Number(d) >= 1 && Number(d) <= 365)
})
const rewardOk = computed(() => rewardValueOk.value && rewardDaysOk.value)
const rewardKinds = computed(() => [
  { label: t('plan.reward_kind_none'), value: 'none' },
  { label: t('plan.reward_kind_percent'), value: 'percent' },
  { label: t('plan.reward_kind_amount'), value: 'amount' },
])
const valid = computed(() => {
  const f = form.value
  const price = asciiNumber(f.price)
  return (
    rewardOk.value &&
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
    // a trial and a traffic package are never sold from a group; an older core does not know groups at all
    ...(groupsReady.value ? { group_id: f.is_trial || f.is_topup ? '' : f.group_id } : {}),
    // a trial and a traffic package give no reward; an older core does not know the form of it
    reward: f.is_trial || f.is_topup || f.reward_kind === 'none'
      ? { kind: 'none' }
      : {
          kind: f.reward_kind,
          value: asciiNumber(f.reward_value).trim(),
          days: Number(asciiNumber(f.reward_days).trim()),
          text: { fa: f.reward_text_fa.trim(), en: f.reward_text_en.trim() },
        },
    ...(sendServers.value ? { server_ids: f.server_ids } : {}),
    ...(showInbounds.value ? { inbounds: inboundsBody(f.inbounds, f.server_ids) } : {}),
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

// Deleting a plan takes it off sale and out of every list; it is never erased (its orders, reports and refunds keep it).
function remove(p: PlanItem) {
  confirm.require({
    header: t('plan.delete'),
    message: t('plan.delete_confirm', { name: isolate(p.name[lang.value] || p.name.en || p.name.fa || '') }),
    acceptProps: { label: t('plan.delete'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      try {
        await api('DELETE', `/plans/${p.id}`)
        toast.add({ severity: 'success', summary: t('plan.deleted'), life: 5000 })
      } catch (e) {
        toast.add({ severity: 'error', summary: errorText(e, t).text, life: 6000 })
      }
      await load()
    },
  })
}

// Turning a plan on or off keeps everything else.
async function toggle(p: PlanItem, enabled: boolean) {
  try {
    await api('PUT', `/plans/${p.id}`, {
      name: p.name, kind: p.kind, duration_days: p.duration_days, traffic_gb: p.traffic_bytes ? gbOf(p.traffic_bytes) : '',
      price: decimalOf(p.price), currency: p.price.currency, enabled, is_trial: p.is_trial, is_topup: p.is_topup, sort: p.sort,
      ...(p.server_ids ? { server_ids: p.server_ids } : {}),
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

  <PlanGroups v-if="groupsReady" :groups="groups" @changed="loadGroups" />

  <DataTable :value="plans" :loading="loading" data-key="id" size="small" scrollable row-hover>
    <template #empty>{{ t('plan.none') }}</template>
    <Column :header="t('plan.name')">
      <template #body="{ data: p }">
        <div><bdi>{{ p.name[lang] || p.name.en || p.name.fa }}</bdi></div>
        <div class="tags">
          <Tag v-if="p.is_trial" :value="t('plan.trial')" severity="info" />
          <Tag v-if="p.is_topup" :value="t('plan.topup')" severity="secondary" />
          <Tag v-if="p.reward?.kind === 'percent'" :value="t('plan.reward_tag', { n: num(p.reward.percent ?? 0, lang) })" severity="success" />
          <Tag v-else-if="p.reward?.kind === 'amount' && p.reward.amount" :value="t('plan.reward_tag_amount', { amount: money(p.reward.amount, lang) })" severity="success" />
        </div>
      </template>
    </Column>
    <Column v-if="groupsReady && groups.length" :header="t('plan.group')">
      <template #body="{ data: p }">
        <span v-if="!p.is_trial && !p.is_topup && groupName(p.group_id)"><bdi>{{ groupName(p.group_id) }}</bdi></span>
        <span v-else class="app-muted">—</span>
      </template>
    </Column>
    <Column :header="t('plan.duration')">
      <template #body="{ data: p }">
        <span v-if="p.duration_days">{{ t('plan.days', { n: num(p.duration_days, lang) }, p.duration_days) }}</span>
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
    <Column v-if="canServers" :header="t('plan.servers')">
      <template #body="{ data: p }">
        <span :class="['plan-servers', { 'app-muted': !p.server_ids?.length }]">{{ serversText(p) }}</span>
      </template>
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
        <Button
          v-if="canAnnounce && p.enabled && !p.is_trial && !p.is_topup"
          icon="pi pi-megaphone"
          text
          rounded
          class="announce"
          :aria-label="t('announce.button')"
          v-tooltip.top="t('announce.button')"
          @click="openAnnounce(p)"
        />
        <Button v-if="canDelete" icon="pi pi-trash" text rounded severity="danger" :aria-label="t('plan.delete')" @click="remove(p)" />
      </template>
    </Column>
  </DataTable>

  <DeletedPlans v-if="canDelete" @restored="load" />
  <AnnounceDialog v-if="announcing" v-model:visible="announceOpen" kind="plan" :product-id="announcing.id" :title="announcing.name[lang] || announcing.name.en || announcing.name.fa" />

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
      <fieldset v-if="!form.is_topup && !form.is_trial" class="reward">
        <legend>{{ t('plan.reward') }}</legend>
        <small class="app-muted">{{ t('plan.reward_hint') }}</small>
        <div class="app-row">
          <label>
            <span>{{ t('plan.reward_kind') }}</span>
            <Select v-model="form.reward_kind" :options="rewardKinds" option-label="label" option-value="value" />
          </label>
          <label v-if="form.reward_kind !== 'none'">
            <span>{{ form.reward_kind === 'percent' ? t('plan.reward_value_percent') : t('plan.reward_value_amount', { currency: t('currency.' + form.currency) }) }}</span>
            <InputText v-model="form.reward_value" inputmode="decimal" dir="ltr" :invalid="!rewardValueOk" />
          </label>
          <label v-if="form.reward_kind !== 'none'">
            <span>{{ t('plan.reward_days') }}</span>
            <InputText v-model="form.reward_days" inputmode="numeric" dir="ltr" maxlength="3" :invalid="!rewardDaysOk" />
          </label>
        </div>
        <small v-if="!rewardOk" class="app-error">{{ t('plan.reward_rule') }}</small>
        <template v-if="form.reward_kind !== 'none'">
          <label>
            <span>{{ t('plan.reward_text_fa') }}</span>
            <Textarea v-model="form.reward_text_fa" dir="rtl" rows="2" maxlength="500" auto-resize />
          </label>
          <label>
            <span>{{ t('plan.reward_text_en') }}</span>
            <Textarea v-model="form.reward_text_en" dir="ltr" rows="2" maxlength="500" auto-resize />
          </label>
        </template>
      </fieldset>
      <label v-if="groupsReady && groups.length && !form.is_topup && !form.is_trial">
        <span>{{ t('plan.group') }}</span>
        <Select v-model="form.group_id" :options="groupOptions" option-label="label" option-value="value" />
        <small class="app-muted">{{ t('plan.group_help') }}</small>
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
      <div v-if="showServers" class="app-field">
        <span id="plan-servers-label">{{ t('plan.servers') }}</span>
        <MultiSelect
          v-model="form.server_ids"
          :options="serverChoices"
          option-label="label"
          option-value="value"
          display="chip"
          :placeholder="t('plan.servers_any')"
          :show-toggle-all="false"
          aria-labelledby="plan-servers-label"
          aria-describedby="plan-servers-help"
          class="servers"
          fluid
        />
        <small id="plan-servers-help" class="app-muted">{{ t('plan.servers_help') }}</small>
        <Message v-if="noUsable" severity="warn" :closable="false" class="no-usable">{{ t('plan.servers_none_usable') }}</Message>
      </div>
      <Message v-else-if="canServers && serversError" severity="warn" :closable="false" class="servers-error">
        {{ t('plan.servers_error') }} {{ serversError }}
      </Message>
      <div v-if="showInbounds && inboundServers.length" class="app-field inbounds">
        <span id="plan-inbounds-label">{{ t('plan.inbounds') }}</span>
        <small id="plan-inbounds-help" class="app-muted">{{ t('plan.inbounds_help') }}</small>
        <div v-for="sv in inboundServers" :key="sv.id" class="inbound-row" :data-server="sv.id">
          <span :id="'plan-inbounds-' + sv.id" class="inbound-server"><bdi>{{ sv.name }}</bdi></span>
          <MultiSelect
            :model-value="form.inbounds[sv.id] ?? []"
            :options="inboundChoices(sv.id)"
            option-label="label"
            option-value="value"
            display="chip"
            :loading="panelInbounds[sv.id]?.loading"
            :placeholder="t('plan.inbounds_all')"
            :show-toggle-all="false"
            :aria-labelledby="'plan-inbounds-label plan-inbounds-' + sv.id"
            aria-describedby="plan-inbounds-help"
            class="inbound-select"
            fluid
            @update:model-value="(v: number[]) => setChoice(sv.id, v)"
          />
          <Message v-if="panelInbounds[sv.id]?.error" severity="warn" :closable="false" class="inbound-error">
            {{ t('plan.inbounds_error') }} {{ panelInbounds[sv.id]?.error }}
          </Message>
          <Message v-else-if="goneOf(sv.id).length" severity="warn" :closable="false" class="inbound-gone">
            {{ t('plan.inbounds_gone', { ids: goneOf(sv.id).map((n) => '#' + n).join(', ') }) }}
          </Message>
        </div>
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
.reward {
  display: grid;
  gap: 0.6rem;
  margin: 0;
  padding: 0.75rem;
  border: 1px solid var(--p-content-border-color, rgba(128, 128, 128, 0.3));
  border-radius: 8px;
}
.reward legend {
  padding-inline: 0.35rem;
  font-weight: 600;
}
.tags {
  display: flex;
  gap: 0.35rem;
  margin-block-start: 0.2rem;
}
.inbound-row {
  display: grid;
  gap: 0.35rem;
  margin-block-start: 0.5rem;
}
.inbound-server {
  font-size: 0.9rem;
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
