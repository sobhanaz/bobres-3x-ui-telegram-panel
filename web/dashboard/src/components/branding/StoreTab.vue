<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Card from 'primevue/card'
import InputText from 'primevue/inputtext'
import Textarea from 'primevue/textarea'
import ColorPicker from 'primevue/colorpicker'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import LogoField from './LogoField.vue'
import BotPhotoField from './BotPhotoField.vue'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { useAuth } from '../../stores/auth'
import { num, type Lang } from '../../format'
import { applyBrandColor } from '../../theme'
import { DEFAULT_COLOR, HEX_RE, brandingField, contrastWithWhite, isHttpsUrl, normalizeHex } from '../../branding'
import type { BrandingInfo } from '../../types'

// The store's name, support contact, colour, logo, the bot's profile photo,
// legal links and Toman name (GET/PUT /branding). The colour previews live
// while it is edited.
const props = defineProps<{ active: boolean }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const auth = useAuth()
const uid = useId()
const id = (field: string) => `${uid}-${field.replace('.', '-')}`

interface Form {
  name: string
  support: string
  color: string
  terms_url: string
  privacy_url: string
  'currency.fa': string
  'currency.en': string
  // the text of the store's own Terms and Privacy pages: what is shown now (the owner's, or the default to start from)
  'legal.terms.fa': string
  'legal.terms.en': string
  'legal.privacy.fa': string
  'legal.privacy.en': string
}
type Field = keyof Form
const fields: Field[] = ['name', 'support', 'color', 'terms_url', 'privacy_url', 'currency.fa', 'currency.en', 'legal.terms.fa', 'legal.terms.en', 'legal.privacy.fa', 'legal.privacy.en']
type PageKind = 'terms' | 'privacy'
const pages: PageKind[] = ['terms', 'privacy']
const pageLangs = ['fa', 'en'] as const
const legalField = (kind: PageKind, l: 'fa' | 'en') => `legal.${kind}.${l}` as Field
// The addresses of the store's own pages, which the bot and the subscription page use while the fields above are empty.
const origin = window.location.origin
const legalFields = [{ field: 'terms_url' }, { field: 'privacy_url' }] as const
const defaultUrl = (field: 'terms_url' | 'privacy_url') => origin + (field === 'terms_url' ? '/terms' : '/privacy')
const currencyFields = [
  { lang: 'fa', field: 'currency.fa' },
  { lang: 'en', field: 'currency.en' },
] as const

const blank = (): Form => ({
  name: '', support: '', color: '', terms_url: '', privacy_url: '', 'currency.fa': '', 'currency.en': '',
  'legal.terms.fa': '', 'legal.terms.en': '', 'legal.privacy.fa': '', 'legal.privacy.en': '',
})
const formOf = (b: BrandingInfo): Form => ({
  name: b.name ?? '',
  support: b.support ?? '',
  color: b.color ?? '',
  terms_url: b.terms_url ?? '',
  privacy_url: b.privacy_url ?? '',
  'currency.fa': b.currency?.fa ?? '',
  'currency.en': b.currency?.en ?? '',
  'legal.terms.fa': legalShown(b, 'terms', 'fa'),
  'legal.terms.en': legalShown(b, 'terms', 'en'),
  'legal.privacy.fa': legalShown(b, 'privacy', 'fa'),
  'legal.privacy.en': legalShown(b, 'privacy', 'en'),
})
// A page shows the owner's own text, or the default to start from.
function legalShown(b: BrandingInfo, kind: PageKind, l: 'fa' | 'en'): string {
  return (b.legal?.[kind]?.[l] || b.legal_defaults?.[kind]?.[l] || '').toString()
}
const trimmed = (f: Form): Form => {
  const out = { ...f }
  for (const k of fields) out[k] = out[k].trim()
  out.color = out.color.toLowerCase()
  return out
}

const info = ref<BrandingInfo | null>(null)
const form = ref<Form>(blank())
const saved = ref<Form>(blank())
const loading = ref(false)
const loadError = ref('')
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    setInfo(await api<BrandingInfo>('GET', '/branding'))
  } catch (e) {
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
function setInfo(b: BrandingInfo) {
  info.value = b
  saved.value = formOf(b)
  form.value = formOf(b)
}
watch(
  () => props.active,
  (a) => {
    if (a && !info.value && !loading.value) void load()
  },
  { immediate: true },
)

const defaults = computed(() => info.value?.defaults ?? { name: 'BOBRES', color: DEFAULT_COLOR, currency: { fa: 'تومان', en: 'Toman' } })

// Checks the browser can make; core checks again and names a bad field.
const serverErrors = ref<Partial<Record<Field, string>>>({})
const colorOk = computed(() => form.value.color.trim() === '' || HEX_RE.test(form.value.color.trim()))
const urlOk = (v: string) => v.trim() === '' || isHttpsUrl(v.trim())
const clientErrors = computed<Partial<Record<Field, string>>>(() => ({
  ...(colorOk.value ? {} : { color: t('branding.color_bad') }),
  ...(urlOk(form.value.terms_url) ? {} : { terms_url: t('branding.url_bad') }),
  ...(urlOk(form.value.privacy_url) ? {} : { privacy_url: t('branding.url_bad') }),
}))
const valid = computed(() => Object.keys(clientErrors.value).length === 0)
const dirty = computed(() => JSON.stringify(trimmed(form.value)) !== JSON.stringify(trimmed(saved.value)))
for (const f of fields) {
  watch(
    () => form.value[f],
    () => {
      if (serverErrors.value[f]) serverErrors.value = { ...serverErrors.value, [f]: undefined }
    },
  )
}

// Colour: the picker works without "#"; the text field takes #rrggbb.
const pickerValue = computed({
  get: () => (HEX_RE.test(form.value.color.trim()) ? form.value.color.trim() : defaults.value.color || DEFAULT_COLOR).slice(1),
  set: (v: unknown) => {
    const hex = normalizeHex(String(v ?? ''))
    if (hex) form.value.color = hex
  },
})
function tidyColor() {
  const hex = normalizeHex(form.value.color)
  if (hex) form.value.color = hex
}
const contrast = computed(() => {
  const c = form.value.color.trim()
  return HEX_RE.test(c) ? contrastWithWhite(c) : null
})
const previewing = computed(() => colorOk.value && form.value.color.trim().toLowerCase() !== saved.value.color.toLowerCase())

// Live preview of the colour on the whole dashboard; the saved colour comes
// back when the page closes unsaved.
let previewTimer: ReturnType<typeof setTimeout> | undefined
function preview() {
  clearTimeout(previewTimer)
  if (!colorOk.value) return
  const c = form.value.color.trim()
  previewTimer = setTimeout(() => applyBrandColor(c), 150)
}
watch(() => form.value.color, preview)
onBeforeUnmount(() => {
  clearTimeout(previewTimer)
  applyBrandColor(auth.me?.branding?.color)
})

const busy = ref(false)
const formError = ref('')
const formDetail = ref('')
async function save() {
  if (!valid.value || !dirty.value) return
  const f = trimmed(form.value)
  busy.value = true
  formError.value = ''
  formDetail.value = ''
  serverErrors.value = {}
  try {
    const res = await api<BrandingInfo>('PUT', '/branding', {
      name: f.name,
      support: f.support,
      color: f.color,
      terms_url: f.terms_url,
      privacy_url: f.privacy_url,
      currency: { fa: f['currency.fa'], en: f['currency.en'] },
      // a text equal to the default is saved as "" so that it keeps following the default; an older server knows no pages
      ...(info.value?.legal_defaults ? { legal: legalBody(f) } : {}),
    })
    setInfo(res)
    toast.add({ severity: 'success', summary: t('branding.saved'), life: 3000 })
    await auth.refresh() // header, colour and Toman name everywhere
  } catch (e) {
    const field = e instanceof ApiError && typeof e.data.field === 'string' ? (brandingField(e.data.field) as Field) : null
    if (e instanceof ApiError && field && fields.includes(field)) {
      serverErrors.value = { [field]: e.message }
    } else {
      const err = errorText(e, t)
      formError.value = err.text
      formDetail.value = err.detail
    }
  } finally {
    busy.value = false
  }
}
function legalBody(f: Form): Record<PageKind, { fa: string; en: string }> {
  const def = info.value?.legal_defaults
  const own = (kind: PageKind, l: 'fa' | 'en') => {
    const v = f[legalField(kind, l)].trim()
    return v === (def?.[kind]?.[l] ?? '').trim() ? '' : v
  }
  return {
    terms: { fa: own('terms', 'fa'), en: own('terms', 'en') },
    privacy: { fa: own('privacy', 'fa'), en: own('privacy', 'en') },
  }
}
// Back to the default text of a page in a language.
function useDefaultText(kind: PageKind, l: 'fa' | 'en') {
  form.value[legalField(kind, l)] = info.value?.legal_defaults?.[kind]?.[l] ?? ''
}
function discard() {
  form.value = { ...saved.value }
  serverErrors.value = {}
  formError.value = ''
}

// A new or removed logo also changes the header: reload /me, then keep the
// colour preview that reloading replaced.
async function logoChanged(b: BrandingInfo) {
  if (info.value) info.value = { ...info.value, logo: b.logo }
  await auth.refresh()
  if (colorOk.value) applyBrandColor(form.value.color.trim())
}

// The bot photo changes nothing else on the page or in the header.
function botPhotoChanged(b: BrandingInfo) {
  if (info.value) info.value = { ...info.value, bot_photo: b.bot_photo ?? null }
}

const errorOf = (f: Field) => clientErrors.value[f] ?? ''
</script>

<template>
  <Message v-if="loadError" severity="error" :closable="false">
    {{ loadError }}
    <Button :label="t('app.retry')" text size="small" @click="load" />
  </Message>
  <div v-if="loading && !info" class="cards">
    <Skeleton v-for="i in 4" :key="i" height="12rem" />
  </div>

  <form v-else-if="info" class="app-form store" novalidate @submit.prevent="save">
    <p class="app-muted intro">{{ t('branding.intro') }}</p>
    <div class="cards">
      <Card>
        <template #title>{{ t('branding.identity') }}</template>
        <template #content>
          <div class="fields">
            <div class="app-field">
              <label :for="id('name')">{{ t('branding.name') }}</label>
              <InputText :id="id('name')" v-model="form.name" maxlength="64" :placeholder="defaults.name" :invalid="!!serverErrors.name" fluid />
              <small class="app-muted">{{ t('branding.name_hint', { fallback: defaults.name }) }}</small>
              <Message v-if="serverErrors.name" severity="error" size="small" variant="simple">
                {{ t('branding.not_accepted') }} <span class="app-ltr">{{ serverErrors.name }}</span>
              </Message>
            </div>
            <div class="app-field">
              <label :for="id('support')">{{ t('branding.support') }}</label>
              <InputText :id="id('support')" v-model="form.support" maxlength="64" dir="ltr" placeholder="@support" :invalid="!!serverErrors.support" fluid />
              <small class="app-muted">{{ t('branding.support_hint') }}</small>
              <Message v-if="serverErrors.support" severity="error" size="small" variant="simple">
                {{ t('branding.not_accepted') }} <span class="app-ltr">{{ serverErrors.support }}</span>
              </Message>
            </div>
          </div>
        </template>
      </Card>

      <Card>
        <template #title>{{ t('branding.look') }}</template>
        <template #content>
          <div class="fields">
            <div class="app-field">
              <label :for="id('color')">{{ t('branding.color') }}</label>
              <div class="color-row">
                <ColorPicker v-model="pickerValue" format="hex" :pt="{ preview: { 'aria-label': t('branding.color_pick') } }" />
                <InputText
                  :id="id('color')"
                  v-model="form.color"
                  dir="ltr"
                  maxlength="7"
                  inputmode="text"
                  autocomplete="off"
                  spellcheck="false"
                  class="app-mono hex"
                  :placeholder="defaults.color"
                  :invalid="!!errorOf('color') || !!serverErrors.color"
                  @blur="tidyColor"
                />
                <Button
                  v-if="form.color"
                  :label="t('branding.color_default')"
                  text
                  size="small"
                  severity="secondary"
                  @click="form.color = ''"
                />
              </div>
              <small class="app-muted">{{ t('branding.color_hint') }}</small>
              <Message v-if="errorOf('color')" severity="error" size="small" variant="simple">{{ errorOf('color') }}</Message>
              <Message v-if="serverErrors.color" severity="error" size="small" variant="simple">
                {{ t('branding.not_accepted') }} <span class="app-ltr">{{ serverErrors.color }}</span>
              </Message>
              <Message v-if="form.color && contrast !== null && contrast < 4.5" severity="warn" size="small" :closable="false">
                {{ t('branding.color_contrast', { ratio: num(Math.round(contrast * 10) / 10, lang) }) }}
              </Message>
              <small v-if="previewing" class="app-muted"><i class="pi pi-eye" aria-hidden="true" /> {{ t('branding.color_preview') }}</small>
            </div>
            <div class="app-field" role="group" :aria-labelledby="id('logo-label')" :aria-describedby="id('logo-hint')">
              <span :id="id('logo-label')" class="label">{{ t('branding.logo') }}</span>
              <LogoField :logo="info.logo" @update="logoChanged" />
              <small :id="id('logo-hint')" class="app-muted">{{ t('branding.logo_hint') }}</small>
            </div>
          </div>
        </template>
      </Card>

      <Card>
        <template #title>{{ t('branding.bot_card') }}</template>
        <template #content>
          <div class="fields">
            <div class="app-field" role="group" :aria-labelledby="id('bot-photo-label')" :aria-describedby="id('bot-photo-hint')">
              <span :id="id('bot-photo-label')" class="label">{{ t('branding.bot_photo') }}</span>
              <BotPhotoField :photo="info.bot_photo ?? null" @update="botPhotoChanged" />
              <small :id="id('bot-photo-hint')" class="app-muted">{{ t('branding.bot_photo_hint') }}</small>
            </div>
          </div>
        </template>
      </Card>

      <Card>
        <template #title>{{ t('branding.legal') }}</template>
        <template #content>
          <div class="fields">
            <p class="app-muted note">{{ t('branding.legal_hint') }}</p>
            <p class="app-muted note">{{ t('branding.legal_default_hint') }}</p>
            <div v-for="lf in legalFields" :key="lf.field" class="app-field">
              <label :for="id(lf.field)">{{ t('branding.' + lf.field) }}</label>
              <InputText
                :id="id(lf.field)"
                v-model="form[lf.field]"
                type="url"
                inputmode="url"
                dir="ltr"
                maxlength="1000"
                autocomplete="off"
                :placeholder="defaultUrl(lf.field)"
                :invalid="!!errorOf(lf.field) || !!serverErrors[lf.field]"
                fluid
              />
              <Message v-if="errorOf(lf.field)" severity="error" size="small" variant="simple">{{ errorOf(lf.field) }}</Message>
              <Message v-if="serverErrors[lf.field]" severity="error" size="small" variant="simple">
                {{ t('branding.not_accepted') }} <span class="app-ltr">{{ serverErrors[lf.field] }}</span>
              </Message>
            </div>
          </div>
        </template>
      </Card>

      <Card v-if="info.legal_defaults" class="wide-card">
        <template #title>{{ t('branding.pages') }}</template>
        <template #content>
          <div class="fields">
            <p class="app-muted note">{{ t('branding.pages_hint') }}</p>
            <section v-for="kind in pages" :key="kind" class="page">
              <div class="page-head">
                <h3>{{ t('branding.page_' + kind) }}</h3>
                <a class="app-ltr page-link" :href="origin + '/' + kind" target="_blank" rel="noopener">{{ origin + '/' + kind }}</a>
              </div>
              <div v-for="l in pageLangs" :key="l" class="app-field">
                <div class="page-lang">
                  <label :for="id(legalField(kind, l))">{{ t('branding.page_lang_' + l) }}</label>
                  <Button type="button" text size="small" :label="t('branding.use_default_text')" @click="useDefaultText(kind, l)" />
                </div>
                <Textarea :id="id(legalField(kind, l))" v-model="form[legalField(kind, l)]" :dir="l === 'fa' ? 'rtl' : 'ltr'" rows="10" maxlength="30000" auto-resize fluid />
              </div>
            </section>
          </div>
        </template>
      </Card>

      <Card>
        <template #title>{{ t('branding.currency') }}</template>
        <template #content>
          <div class="fields">
            <p class="app-muted note">{{ t('branding.currency_hint') }}</p>
            <div class="app-row">
              <div v-for="c in currencyFields" :key="c.field" class="app-field">
                <label :for="id(c.field)">{{ t('branding.currency_' + c.lang) }}</label>
                <InputText
                  :id="id(c.field)"
                  v-model="form[c.field]"
                  maxlength="16"
                  :dir="c.lang === 'fa' ? 'rtl' : 'ltr'"
                  :placeholder="defaults.currency[c.lang]"
                  :invalid="!!serverErrors[c.field]"
                  fluid
                />
                <Message v-if="serverErrors[c.field]" severity="error" size="small" variant="simple">
                  {{ t('branding.not_accepted') }} <span class="app-ltr">{{ serverErrors[c.field] }}</span>
                </Message>
              </div>
            </div>
          </div>
        </template>
      </Card>
    </div>

    <Message v-if="formError" severity="error" :closable="false">
      {{ formError }}
      <div v-if="formDetail" class="app-ltr detail">{{ formDetail }}</div>
    </Message>
    <div class="save-bar">
      <span v-if="dirty" class="app-muted small">{{ t('branding.unsaved') }}</span>
      <span class="spacer" />
      <Button type="button" :label="t('branding.discard')" severity="secondary" outlined :disabled="!dirty || busy" @click="discard" />
      <Button type="submit" :label="t('common.save')" icon="pi pi-check" :loading="busy" :disabled="!dirty || !valid" />
    </div>
  </form>
</template>

<style scoped>
.intro {
  margin: 0;
}
.cards {
  display: grid;
  gap: var(--app-gap);
  grid-template-columns: repeat(auto-fit, minmax(min(24rem, 100%), 1fr));
  align-items: start;
}
.fields {
  display: flex;
  flex-direction: column;
  gap: 1.1rem;
}
.label {
  font-weight: 400;
}
.color-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
.hex {
  inline-size: 7.5rem;
}
.note {
  margin: 0;
  font-size: 0.875rem;
}
.wide-card {
  grid-column: 1 / -1;
}
.page {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  padding-block-start: 0.75rem;
  border-block-start: 1px solid var(--p-content-border-color);
}
.page-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 0.5rem;
}
.page-head h3 {
  margin: 0;
  font-size: 1rem;
}
.page-link {
  font-size: 0.9rem;
  overflow-wrap: anywhere;
}
.page-lang {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}
.save-bar {
  position: sticky;
  inset-block-end: 0;
  z-index: 1;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  padding-block: 0.75rem;
  background: var(--p-surface-50);
  border-block-start: 1px solid var(--p-content-border-color);
}
:root.app-dark .save-bar {
  background: var(--p-surface-950);
}
.spacer {
  flex: 1;
}
.small {
  font-size: 0.85rem;
}
.detail {
  font-size: 0.85rem;
}
</style>
