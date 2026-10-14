<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import SelectButton from 'primevue/selectbutton'
import Message from 'primevue/message'
import { api } from '../../api'
import { errorText } from '../../errors'
import { dateTime, num, type Lang } from '../../format'
import {
  MAX_AHEAD,
  defaultScheduleAt,
  fromLocalInput,
  localInput,
  scheduleProblem,
  type AudienceLang,
  type Segment,
} from '../../broadcasts'
import type { AudienceCount } from '../../types'

// The last step before a broadcast goes out: now or at a chosen time, with
// how many customers it reaches (counted again as the dialog opens) and the
// time in the staff member's calendar. Confirming emits the time (null = now).
const visible = defineModel<boolean>('visible', { required: true })
const props = withDefaults(
  defineProps<{
    segment: Segment
    language: AudienceLang
    mode?: 'now' | 'later'
    at?: number | null
    busy?: boolean
    error?: string
    detail?: string
    atError?: string
  }>(),
  { mode: 'now', at: null, busy: false, error: '', detail: '', atError: '' },
)
const emit = defineEmits<{ confirm: [at: number | null] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const uid = useId()

const when = ref<'now' | 'later'>(props.mode)
const input = ref('')
const now = ref(Math.floor(Date.now() / 1000))
const count = ref<AudienceCount | null>(null)
const counting = ref(false)
const countError = ref('')
const zone = (() => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
})()

async function recount() {
  counting.value = true
  countError.value = ''
  try {
    count.value = await api<AudienceCount>('POST', '/broadcasts/audience', { segment: props.segment, language: props.language })
  } catch (e) {
    count.value = null
    countError.value = errorText(e, t).text
  } finally {
    counting.value = false
  }
}

// Each opening starts from the chosen way and a fresh count.
watch(
  visible,
  (v) => {
    if (!v) return
    now.value = Math.floor(Date.now() / 1000)
    when.value = props.mode
    const start = props.at && props.at > now.value ? props.at : defaultScheduleAt(now.value)
    input.value = localInput(start)
    count.value = null
    void recount()
  },
  { immediate: true },
)

const at = computed(() => fromLocalInput(input.value))
const problem = computed(() => (when.value === 'later' ? scheduleProblem(at.value, now.value) : ''))
const problemText = computed(() => {
  if (!problem.value) return ''
  if (problem.value === 'too_far') return t('broadcasts.send.too_far')
  if (problem.value === 'past') return t('broadcasts.send.past')
  return t('broadcasts.send.invalid')
})
const min = computed(() => localInput(now.value + 60))
const max = computed(() => localInput(now.value + MAX_AHEAD))
const ways = computed(() => [
  { label: t('broadcasts.send.when_now'), value: 'now' },
  { label: t('broadcasts.send.when_later'), value: 'later' },
])
const n = computed(() => (count.value ? num(count.value.total, lang.value) : '…'))
// what the message counts ("1 customer", "2 customers")
const recipients = computed(() => count.value?.total ?? 0)
const canConfirm = computed(() => !props.busy && !counting.value && !!count.value && count.value.total > 0 && !problem.value)

function confirm() {
  // The clock moved while the dialog was open: check the time again.
  now.value = Math.floor(Date.now() / 1000)
  if (!canConfirm.value) return
  emit('confirm', when.value === 'now' ? null : at.value)
}
</script>

<template>
  <Dialog
    v-model:visible="visible"
    :header="when === 'now' ? t('broadcasts.send.title_now') : t('broadcasts.send.title_later')"
    modal
    :draggable="false"
    :style="{ inlineSize: 'min(32rem, 94vw)' }"
    class="send-dialog"
  >
    <div class="body">
      <SelectButton v-model="when" :options="ways" option-label="label" option-value="value" :allow-empty="false" :aria-label="t('broadcasts.send.when')" />

      <div v-if="when === 'later'" class="field">
        <label :for="`${uid}-at`">{{ t('broadcasts.send.at_label') }}</label>
        <input
          :id="`${uid}-at`"
          v-model="input"
          type="datetime-local"
          class="p-inputtext p-component at app-ltr"
          dir="ltr"
          :min="min"
          :max="max"
          :aria-invalid="!!problemText || !!props.atError"
          :aria-describedby="`${uid}-at-note`"
        />
        <small :id="`${uid}-at-note`" class="app-muted">{{ t('broadcasts.send.picker_note', { zone: zone || '—' }) }}</small>
        <div v-if="at !== null && !problemText" class="chosen">
          <i class="pi pi-calendar" aria-hidden="true" />
          {{ t('broadcasts.send.chosen', { time: dateTime(at, lang) }) }}
        </div>
        <small v-if="problemText" class="err">{{ problemText }}</small>
        <small v-else-if="props.atError" class="err app-ltr">{{ props.atError }}</small>
      </div>

      <div class="count" aria-live="polite">
        <span v-if="counting" class="app-muted"><i class="pi pi-spin pi-spinner" aria-hidden="true" /> {{ t('broadcasts.audience.counting') }}</span>
        <span v-else-if="countError" class="err">{{ countError }}</span>
        <template v-else-if="count && count.total === 0">{{ t('broadcasts.send.zero') }}</template>
        <template v-else-if="count">
          <p class="lead">
            {{
              when === 'now'
                ? t('broadcasts.send.confirm_now', { n }, recipients)
                : t('broadcasts.send.confirm_later', { n, time: at !== null ? dateTime(at, lang) : '…' }, recipients)
            }}
          </p>
          <p v-if="language === ''" class="app-muted by-lang">
            {{ t('broadcasts.audience.by_lang', { fa: num(count.languages.fa, lang), en: num(count.languages.en, lang) }) }}
          </p>
          <p class="app-muted note">{{ t('broadcasts.send.count_note') }}</p>
        </template>
      </div>

      <Message v-if="props.error" severity="error" :closable="false">
        {{ props.error }}
        <div v-if="props.detail" class="app-ltr detail">{{ props.detail }}</div>
      </Message>
    </div>
    <template #footer>
      <Button :label="t('app.cancel')" severity="secondary" outlined @click="visible = false" />
      <Button
        :label="when === 'now' ? t('broadcasts.send.now') : t('broadcasts.send.schedule')"
        :icon="when === 'now' ? 'pi pi-send' : 'pi pi-clock'"
        class="send-confirm"
        :loading="props.busy"
        :disabled="!canConfirm"
        @click="confirm"
      />
    </template>
  </Dialog>
</template>

<style scoped>
.body {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.at {
  inline-size: 100%;
  max-inline-size: 18rem;
  font-family: var(--app-font-en);
}
.chosen {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-weight: 600;
}
.count p {
  margin: 0;
}
.count {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.lead {
  font-weight: 600;
}
.by-lang,
.note {
  font-size: 0.85rem;
}
.err {
  color: var(--p-message-error-simple-color, var(--p-red-600));
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
}
</style>
