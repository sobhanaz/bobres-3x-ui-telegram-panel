<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import RadioButton from 'primevue/radiobutton'
import { api } from '../../api'
import { errorText } from '../../errors'
import { num, type Lang } from '../../format'
import { AUDIENCE_LANGS, SEGMENTS, timing, type AudienceLang, type Segment } from '../../broadcasts'
import type { AudienceCount } from '../../types'

// Who gets the broadcast: a customer group and a bot language, with a live
// count of the customers that matches (core leaves banned customers and those
// who blocked the bot out).
const segment = defineModel<Segment>('segment', { required: true })
const language = defineModel<AudienceLang>('language', { required: true })
const props = defineProps<{ disabled?: boolean }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const uid = useId()

// PrimeVue's RadioButton never shows '' as checked: "all languages" uses
// a stand-in value on the radio only.
const langChoice = computed({
  get: () => language.value || 'all',
  set: (v: string) => (language.value = (v === 'all' ? '' : v) as AudienceLang),
})

const icons: Record<Segment, string> = {
  all: 'pi pi-users',
  active: 'pi pi-bolt',
  expired: 'pi pi-history',
  never_bought: 'pi pi-user-plus',
}

const count = ref<AudienceCount | null>(null)
const counting = ref(false)
const error = ref('')
let seq = 0
let timer: ReturnType<typeof setTimeout> | undefined

// Count once the choice settles; only the answer to the latest choice counts.
function schedule(delay: number) {
  clearTimeout(timer)
  const mine = ++seq
  counting.value = true
  error.value = ''
  const body = { segment: segment.value, language: language.value }
  timer = setTimeout(async () => {
    try {
      const res = await api<AudienceCount>('POST', '/broadcasts/audience', body)
      if (mine !== seq) return
      count.value = res
    } catch (e) {
      if (mine !== seq) return
      count.value = null
      error.value = errorText(e, t).text
    } finally {
      if (mine === seq) counting.value = false
    }
  }, delay)
}
watch([segment, language], () => schedule(timing.audience))
schedule(0)
onBeforeUnmount(() => {
  clearTimeout(timer)
  seq++
})

const languageLabel = (l: AudienceLang) => t('broadcasts.language.' + (l || 'all'))
</script>

<template>
  <div class="audience">
    <fieldset class="group" :disabled="props.disabled">
      <legend class="legend">{{ t('broadcasts.audience.segment_label') }}</legend>
      <div class="cards">
        <label v-for="s in SEGMENTS" :key="s" :for="`${uid}-seg-${s}`" class="card" :class="{ on: segment === s }">
          <RadioButton v-model="segment" :input-id="`${uid}-seg-${s}`" :name="`${uid}-segment`" :value="s" :disabled="props.disabled" />
          <span class="card-text">
            <span class="card-title"><i :class="icons[s]" aria-hidden="true" /> {{ t('broadcasts.segment.' + s) }}</span>
            <small class="app-muted">{{ t('broadcasts.segment_desc.' + s) }}</small>
          </span>
        </label>
      </div>
    </fieldset>

    <fieldset class="group" :disabled="props.disabled">
      <legend class="legend">{{ t('broadcasts.audience.language_label') }}</legend>
      <div class="langs">
        <div v-for="l in AUDIENCE_LANGS" :key="l || 'all'" class="lang">
          <RadioButton v-model="langChoice" :input-id="`${uid}-lang-${l || 'all'}`" :name="`${uid}-language`" :value="l || 'all'" :disabled="props.disabled" />
          <label :for="`${uid}-lang-${l || 'all'}`">{{ languageLabel(l) }}</label>
        </div>
      </div>
    </fieldset>

    <div class="count" aria-live="polite">
      <i class="pi pi-users" aria-hidden="true" />
      <span v-if="counting && !count" class="app-muted">{{ t('broadcasts.audience.counting') }}</span>
      <span v-else-if="error" class="err">{{ error }}</span>
      <template v-else-if="count">
        <span class="total" :class="{ stale: counting }">{{ t('broadcasts.audience.count', { n: num(count.total, lang) }) }}</span>
        <span v-if="language === '' && count.total" class="app-muted by-lang">
          {{ t('broadcasts.audience.by_lang', { fa: num(count.languages.fa, lang), en: num(count.languages.en, lang) }) }}
        </span>
        <span v-if="!count.total" class="app-muted">{{ t('broadcasts.audience.none') }}</span>
      </template>
    </div>
    <p class="app-muted note">{{ t('broadcasts.audience.left_out') }}</p>
  </div>
</template>

<style scoped>
.audience {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.group {
  border: 0;
  margin: 0;
  padding: 0;
  min-inline-size: 0;
}
.legend {
  padding: 0;
  margin-block-end: 0.5rem;
  font-weight: 600;
}
.cards {
  display: grid;
  gap: 0.6rem;
  grid-template-columns: repeat(auto-fit, minmax(min(13rem, 100%), 1fr));
}
.card {
  display: flex;
  gap: 0.6rem;
  align-items: flex-start;
  padding: 0.7rem 0.8rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--app-radius);
  background: var(--p-content-background);
  cursor: pointer;
  transition: border-color 0.15s, background-color 0.15s;
}
.card:hover {
  border-color: var(--p-primary-color);
}
.card.on {
  border-color: var(--p-primary-color);
  background: var(--p-highlight-background);
}
.group:disabled .card {
  cursor: default;
  opacity: 0.7;
}
.card-text {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  min-inline-size: 0;
}
.card-title {
  font-weight: 600;
}
.card-title i {
  font-size: 0.9rem;
  margin-inline-end: 0.2rem;
}
.card small {
  font-size: 0.8rem;
  line-height: 1.5;
}
.langs {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem 1.25rem;
}
.lang {
  display: flex;
  align-items: center;
  gap: 0.45rem;
}
.count {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.25rem 0.6rem;
  padding: 0.6rem 0.8rem;
  border-radius: 0.5rem;
  background: var(--p-content-hover-background);
}
.count .pi {
  align-self: center;
  color: var(--p-primary-color);
}
.total {
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.total.stale {
  opacity: 0.6;
}
.by-lang {
  font-size: 0.875rem;
}
.err {
  color: var(--p-message-error-simple-color, var(--p-red-600));
}
.note {
  margin: 0;
  font-size: 0.8rem;
}
</style>
