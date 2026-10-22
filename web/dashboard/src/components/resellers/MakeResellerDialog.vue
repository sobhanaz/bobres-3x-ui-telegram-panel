<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import InputText from 'primevue/inputtext'
import IconField from 'primevue/iconfield'
import InputIcon from 'primevue/inputicon'
import Listbox from 'primevue/listbox'
import Select from 'primevue/select'
import Message from 'primevue/message'
import ReasonDialog from '../ReasonDialog.vue'
import { api, query } from '../../api'
import { errorText } from '../../errors'
import { isolate, latinDigits, type Lang } from '../../format'
import { addable } from '../../staff'
import { blankTerms, PANEL_GROUP_PREFIX, panelGroupName, percent, termsBody, termsProblems } from '../../resellers'
import { useAuth } from '../../stores/auth'
import type { Page, TierItem, UserBrief, UserItem } from '../../types'

// Make a customer a reseller: their tier, a credit limit in Toman, and a
// reason. Opened from a customer's page (user is set) or from the Resellers
// page, where the customer is found first. Only active customers qualify.
const props = defineProps<{ user?: UserBrief | null }>()
const visible = defineModel<boolean>('visible', { required: true })
const emit = defineEmits<{ made: [id: string] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const auth = useAuth()
// The group name in the panel is the owner's to give (an admin makes the reseller and the owner gives the group later).
const isOwner = computed(() => auth.me?.user.role === 'owner')
const group = ref('')
const wantedGroup = computed(() => (group.value.trim() ? panelGroupName(group.value) : ''))
const groupBad = computed(() => !!group.value.trim() && !wantedGroup.value)

const tiers = ref<TierItem[]>([])
const tiersLoaded = ref(false)
const form = ref(blankTerms())
const busy = ref(false)
const error = ref('')
const detail = ref('')

const q = ref('')
const results = ref<UserItem[]>([])
const searching = ref(false)
const searchError = ref('')
const picked = ref<string | null>(null)
const userId = computed(() => props.user?.id ?? picked.value)

watch(visible, (v) => {
  if (!v) return
  form.value = blankTerms()
  group.value = ''
  error.value = ''
  detail.value = ''
  q.value = ''
  results.value = []
  picked.value = null
  searchError.value = ''
  void loadTiers()
})
async function loadTiers() {
  tiersLoaded.value = false
  try {
    tiers.value = (await api<{ items: TierItem[] }>('GET', '/reseller-tiers')).items
    if (tiers.value.length === 1) form.value.tier = tiers.value[0].id
  } catch (e) {
    error.value = errorText(e, t).text
  } finally {
    tiersLoaded.value = true
  }
}

// Search as they type, a moment after the last key; older answers are dropped.
let seq = 0
let timer: ReturnType<typeof setTimeout> | undefined
watch(q, (v) => {
  clearTimeout(timer)
  const mine = ++seq
  const text = latinDigits(v.trim())
  if (!text) {
    results.value = []
    searching.value = false
    return
  }
  searching.value = true
  timer = setTimeout(() => void search(text, mine), 300)
})
async function search(text: string, mine: number) {
  searchError.value = ''
  try {
    const res = await api<Page<UserItem>>('GET', '/users' + query({ q: text, role: 'user', status: 'active', size: 10 }))
    if (mine !== seq) return
    results.value = res.items.filter(addable)
    if (picked.value && !results.value.some((u) => u.id === picked.value)) picked.value = null
  } catch (e) {
    if (mine === seq) searchError.value = errorText(e, t).text
  } finally {
    if (mine === seq) searching.value = false
  }
}
const people = computed(() =>
  results.value.map((u) => ({ id: u.id, username: u.username, telegram_id: u.telegram_id, label: u.username ? '@' + u.username : String(u.telegram_id) })),
)
const empty = computed(() => (q.value.trim() ? (searching.value ? t('app.loading') : t('staff.search_none')) : t('staff.search_empty')))

const tierOptions = computed(() =>
  tiers.value.map((tr) => ({
    value: tr.id,
    label: t('reseller.tier_option', { name: isolate(tr.name), discount: percent(tr.discount_percent, lang.value), commission: percent(tr.commission_percent, lang.value) }),
  })),
)
const problems = computed(() => termsProblems(form.value))
const message = computed(() =>
  props.user ? t('reseller.make_intro_for', { who: isolate(props.user.username ? '@' + props.user.username : String(props.user.telegram_id)) }) : t('reseller.make_intro'),
)

async function make(reason: string) {
  if (!userId.value) return
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    await api('POST', '/resellers', { user_id: userId.value, ...termsBody(form.value, reason), ...(isOwner.value && wantedGroup.value ? { panel_group: wantedGroup.value } : {}) })
    visible.value = false
    emit('made', userId.value)
  } catch (e) {
    const err = errorText(e, t)
    error.value = err.text
    detail.value = err.detail
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <ReasonDialog
    v-model:visible="visible"
    :title="t('reseller.make')"
    :message="message"
    :confirm-label="t('reseller.make')"
    :busy="busy"
    :error="error"
    :detail="detail"
    :can-confirm="!!userId && problems.length === 0 && !groupBad"
    class="make-reseller"
    @confirm="make"
  >
    <div class="app-form">
      <template v-if="!props.user">
        <label>
          <span>{{ t('reseller.find_customer') }}</span>
          <IconField>
            <InputIcon :class="searching ? 'pi pi-spin pi-spinner' : 'pi pi-search'" />
            <InputText v-model="q" fluid autocomplete="off" dir="auto" maxlength="64" class="search" />
          </IconField>
        </label>
        <Message v-if="searchError" severity="error" :closable="false">{{ searchError }}</Message>
        <Listbox
          v-model="picked"
          :options="people"
          option-label="label"
          option-value="id"
          :empty-message="empty"
          :aria-label="t('common.customer')"
          list-style="max-block-size: 10rem"
          class="people"
        >
          <template #option="{ option }">
            <span class="person">
              <bdi>{{ option.username ? '@' + option.username : '' }}</bdi>
              <span class="app-ltr app-muted">{{ option.telegram_id }}</span>
            </span>
          </template>
        </Listbox>
      </template>

      <Message v-if="tiersLoaded && !tiers.length && !error" severity="warn" :closable="false" class="no-tiers">
        {{ t('reseller.no_tiers') }}
        <RouterLink :to="{ name: 'resellers', query: { tab: 'tiers' } }" @click="visible = false">{{ t('tier.open_tab') }}</RouterLink>
      </Message>
      <label>
        <span>{{ t('reseller.tier') }}</span>
        <Select
          v-model="form.tier"
          :options="tierOptions"
          option-label="label"
          option-value="value"
          :placeholder="t('reseller.pick_tier')"
          :loading="!tiersLoaded"
          class="tier"
        />
      </label>
      <label>
        <span>{{ t('reseller.credit_limit_in', { currency: t('currency.IRT') }) }}</span>
        <InputText
          v-model="form.limits.IRT"
          inputmode="decimal"
          dir="ltr"
          autocomplete="off"
          placeholder="0"
          :invalid="problems.includes('credit_limit.IRT')"
          class="limit"
        />
        <small class="app-muted">{{ t('reseller.limit_hint') }}</small>
      </label>
      <label v-if="isOwner">
        <span>{{ t('reseller.panel_group') }}</span>
        <span class="group-row">
          <span class="app-ltr app-mono lead">{{ PANEL_GROUP_PREFIX }}</span>
          <InputText v-model="group" dir="ltr" maxlength="30" autocomplete="off" :invalid="groupBad" class="group-name" />
        </span>
        <small :class="groupBad ? 'app-error' : 'app-muted'">{{ groupBad ? t('reseller.panel_group_rule') : t('reseller.panel_group_make_hint') }}</small>
      </label>
    </div>
  </ReasonDialog>
</template>

<style scoped>
.people {
  inline-size: 100%;
}
.person {
  display: inline-flex;
  gap: 0.5rem;
  align-items: baseline;
}
.no-tiers a {
  margin-inline-start: 0.35rem;
}
.group-row {
  display: flex;
  align-items: center;
  gap: 0.35rem;
}
.group-name {
  flex: 1;
}
</style>
