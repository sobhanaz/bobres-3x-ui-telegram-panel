<script setup lang="ts">
import { computed, onMounted, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Card from 'primevue/card'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Select from 'primevue/select'
import Skeleton from 'primevue/skeleton'
import ToggleSwitch from 'primevue/toggleswitch'
import Tabs from 'primevue/tabs'
import TabList from 'primevue/tablist'
import Tab from 'primevue/tab'
import BroadcastText from '../broadcasts/BroadcastText.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { contentLang, dateTime, isolate, num, type ContentLang, type Lang } from '../../format'
import { splitProblems, testOutcome, type TestOutcome } from '../../broadcasts'
import { reminderChanges, reminderForm, reminderProblems, timeOf, type ReminderForm, type ReminderReply } from '../../resellerMessages'
import type { TextProblem } from '../../types'

// The weekly settlement reminder: off by default; on, core sends the resellers (those in debt, by default) their debt once a week at the
// weekday and time of the store's time zone, each in their own bot language. The card shows when it goes next and how the last one went,
// saves its settings (each change is in the audit log) and sends a test to your own Telegram.
const props = defineProps<{ resellers: { label: string; value: string }[] }>()
const { t, locale } = useI18n()
const viewer = computed(() => locale.value as Lang)
const toast = useToast()
const uid = useId()
const LANGS: readonly ContentLang[] = ['fa', 'en']

const reply = ref<ReminderReply | null>(null)
const form = ref<ReminderForm | null>(null)
const loading = ref(true)
const loadError = ref('')
const tab = ref<ContentLang>(contentLang(viewer.value))
const textProblems = ref<Record<ContentLang, TextProblem[]>>({ fa: [], en: [] })
const saveError = ref('')
const showAll = ref(false)

function take(r: ReminderReply) {
  reply.value = r
  form.value = reminderForm(r)
  textProblems.value = { fa: [], en: [] }
  showAll.value = false
}
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    take(await api<ReminderReply>('GET', '/reseller-messages/reminder'))
  } catch (e) {
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

const weekdays = computed(() => (reply.value?.weekdays ?? []).map((d) => ({ label: t('reseller.reminder.day.' + d), value: d })))
const changes = computed(() => (reply.value && form.value ? reminderChanges(reminderForm(reply.value), form.value) : {}))
const dirty = computed(() => Object.keys(changes.value).length > 0)
const problems = computed(() => (form.value ? reminderProblems(form.value) : []))
const timeBad = computed(() => !!form.value && !timeOf(form.value.time))
const isDefault = (l: ContentLang) => !!reply.value && !!form.value && form.value.texts[l].trim() === reply.value.defaults[l].trim()
function resetText(l: ContentLang) {
  if (form.value && reply.value) form.value.texts[l] = reply.value.defaults[l]
}

const saving = ref(false)
async function save() {
  showAll.value = true
  saveError.value = ''
  if (problems.value.length) {
    saveError.value = t('broadcasts.save.fix_first')
    const bad = LANGS.find((l) => problems.value.includes('texts.' + l))
    if (bad) tab.value = bad
    return
  }
  saving.value = true
  try {
    take(await api<ReminderReply>('PUT', '/reseller-messages/reminder', { values: changes.value }))
    toast.add({ severity: 'success', summary: t('reseller.reminder.saved'), life: 3000 })
  } catch (e) {
    if (e instanceof ApiError && e.status === 400) {
      // A text's problems name their language.
      textProblems.value = splitProblems(e.data.problems, '')
      const bad = LANGS.find((l) => textProblems.value[l].length)
      if (bad) tab.value = bad
    }
    saveError.value = errorText(e, t).text
  } finally {
    saving.value = false
  }
}

// A test of the saved reminder, in your bot language, as the chosen reseller would get it now.
const testFor = ref('')
const testing = ref(false)
const test = ref<TestOutcome | null>(null)
async function sendTest() {
  testing.value = true
  test.value = null
  try {
    const res = await api<{ ok: boolean; problem?: string }>('POST', '/reseller-messages/reminder/test', { reseller_id: testFor.value })
    const outcome = testOutcome(res)
    if (outcome === 'ok') toast.add({ severity: 'success', summary: t('broadcasts.test.ok'), life: 4000 })
    else test.value = outcome
  } catch (e) {
    test.value = e instanceof ApiError ? testOutcome(null, e) : 'failed'
  } finally {
    testing.value = false
  }
}

const last = computed(() => reply.value?.last ?? null)
</script>

<template>
  <Card class="reminder">
    <template #title>{{ t('reseller.reminder.title') }}</template>
    <template #subtitle>{{ t('reseller.reminder.intro') }}</template>
    <template #content>
      <Skeleton v-if="loading" height="10rem" />
      <Message v-else-if="loadError" severity="error" :closable="false">
        {{ loadError }}
        <Button :label="t('app.retry')" text size="small" @click="load" />
      </Message>
      <div v-else-if="form && reply" class="body">
        <label class="switch">
          <ToggleSwitch v-model="form.enabled" :input-id="`${uid}-on`" class="enabled" />
          <span class="strong">{{ form.enabled ? t('reseller.reminder.on') : t('reseller.reminder.off') }}</span>
        </label>

        <div class="when">
          <div class="field">
            <label :for="`${uid}-day`">{{ t('reseller.reminder.weekday') }}</label>
            <Select v-model="form.weekday" :input-id="`${uid}-day`" :options="weekdays" option-label="label" option-value="value" class="day" />
          </div>
          <div class="field">
            <label :for="`${uid}-time`">{{ t('reseller.reminder.time') }}</label>
            <InputText :id="`${uid}-time`" v-model="form.time" class="time app-ltr" inputmode="numeric" placeholder="21:00" :invalid="timeBad" />
            <small v-if="timeBad" class="err">{{ t('reseller.reminder.time_bad') }}</small>
          </div>
        </div>
        <p class="app-muted small zone">{{ t('reseller.reminder.zone', { zone: isolate(reply.zone) }) }}</p>

        <label class="switch">
          <ToggleSwitch v-model="form.debtOnly" :input-id="`${uid}-debt`" />
          <span>{{ t('reseller.reminder.debt_only') }}</span>
        </label>
        <label class="switch">
          <ToggleSwitch v-model="form.settle" :input-id="`${uid}-settle`" />
          <span>{{ t('reseller.messages.settle') }}</span>
        </label>

        <div class="texts">
          <Tabs :value="tab" @update:value="(v: string | number) => (tab = v as ContentLang)">
            <TabList>
              <Tab v-for="l in LANGS" :key="l" :value="l">{{ t('texts.' + l) }}</Tab>
            </TabList>
          </Tabs>
          <BroadcastText
            :key="tab"
            v-model="form.texts[tab]"
            :lang="tab"
            :problems="textProblems[tab]"
            :flag-empty="showAll"
            check-path="/reseller-messages/check"
          />
          <Button v-if="!isDefault(tab)" :label="t('reseller.reminder.reset_text')" icon="pi pi-replay" text size="small" class="reset" @click="resetText(tab)" />
        </div>

        <dl class="app-facts facts">
          <dt>{{ t('reseller.reminder.next') }}</dt>
          <dd>{{ reply.enabled && reply.next_due_at ? dateTime(reply.next_due_at, viewer) : t('reseller.reminder.next_none') }}</dd>
          <dt>{{ t('reseller.reminder.last') }}</dt>
          <dd v-if="last" class="last">
            {{ dateTime(last.queued_at, viewer) }}
            <template v-if="last.broadcast">
              ·
              {{ t('reseller.reminder.last_counts', { sent: num(last.broadcast.counts.sent, viewer), total: num(last.broadcast.counts.total, viewer) }) }}
              <RouterLink :to="{ name: 'broadcast', params: { id: last.broadcast.id } }" class="open">{{ t('reseller.reminder.open') }}</RouterLink>
            </template>
          </dd>
          <dd v-else class="app-muted">{{ t('reseller.reminder.never') }}</dd>
        </dl>

        <Message v-if="saveError" severity="error" :closable="false" class="save-error">{{ saveError }}</Message>
        <Message v-if="test" :severity="test === 'blocked' || test === 'rate_limited' ? 'warn' : 'error'" :closable="false">{{ t('broadcasts.test.' + test) }}</Message>
        <div class="actions">
          <Select v-model="testFor" :options="props.resellers" option-label="label" option-value="value" filter :aria-label="t('reseller.messages.preview_as')" class="who" />
          <Button :label="t('reseller.reminder.test')" icon="pi pi-user" severity="secondary" outlined class="test" :loading="testing" :disabled="dirty" @click="sendTest" />
          <span class="grow" />
          <Button :label="t('common.save')" icon="pi pi-save" class="save" :loading="saving" :disabled="!dirty" @click="save" />
        </div>
        <p v-if="dirty" class="app-muted small">{{ t('reseller.reminder.save_first') }}</p>
      </div>
    </template>
  </Card>
</template>

<style scoped>
.body {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.switch {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  cursor: pointer;
}
.strong {
  font-weight: 600;
}
.when {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}
.day {
  min-inline-size: 10rem;
}
.time {
  inline-size: 7rem;
}
.small {
  font-size: 0.8rem;
}
.zone {
  margin: 0;
}
.err {
  color: var(--p-message-error-simple-color, var(--p-red-600));
}
.texts {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.reset {
  align-self: flex-start;
}
.open {
  margin-inline-start: 0.35rem;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  align-items: center;
}
.who {
  min-inline-size: 12rem;
}
.grow {
  flex: 1 1 0;
}
@media (max-width: 480px) {
  .actions > :deep(.p-button),
  .who {
    flex: 1 1 100%;
  }
  .grow {
    display: none;
  }
}
</style>
