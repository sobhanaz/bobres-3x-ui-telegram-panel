<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Button from 'primevue/button'
import Message from 'primevue/message'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { dateTime, num, type Lang } from '../../format'
import { LOGO_TYPES, logoProblem, logoSizeOk } from '../../branding'
import type { BrandingInfo } from '../../types'

// The store logo: pick an image, see it, then upload it (raw body, CSRF
// through api()). The preview is a data: URL because the dashboard's CSP
// does not allow blob: images.
const props = defineProps<{ logo: BrandingInfo['logo'] }>()
const emit = defineEmits<{ update: [info: BrandingInfo] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()

const input = ref<HTMLInputElement | null>(null)
const file = ref<File | null>(null)
const preview = ref('')
const busy = ref(false)
const error = ref('')
const detail = ref('')

function readDataUrl(f: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result))
    r.onerror = () => reject(r.error)
    r.readAsDataURL(f)
  })
}

function imageSize(url: string): Promise<{ width: number; height: number }> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => resolve({ width: img.naturalWidth, height: img.naturalHeight })
    img.onerror = () => reject(new Error('unreadable'))
    img.src = url
  })
}

async function picked(e: Event) {
  const el = e.target as HTMLInputElement
  const f = el.files?.[0]
  el.value = '' // the same file can be chosen again
  error.value = ''
  detail.value = ''
  if (!f) return
  const problem = logoProblem(f)
  if (problem) {
    error.value = problem === 'too_large' ? t('branding.logo_too_large') : t('branding.logo_bad_type')
    return
  }
  try {
    const url = await readDataUrl(f)
    const { width, height } = await imageSize(url)
    if (!logoSizeOk(width, height)) {
      error.value = t('branding.logo_bad_size')
      return
    }
    file.value = f
    preview.value = url
  } catch {
    error.value = t('branding.logo_unreadable')
  }
}

function cancel() {
  file.value = null
  preview.value = ''
  error.value = ''
}

async function upload() {
  if (!file.value) return
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    const res = await api<BrandingInfo>('PUT', '/branding/logo', file.value)
    cancel()
    toast.add({ severity: 'success', summary: t('branding.logo_saved'), life: 3000 })
    emit('update', res)
  } catch (e) {
    if (e instanceof ApiError && (e.code === 'too_large' || e.status === 413)) error.value = t('branding.logo_too_large')
    else if (e instanceof ApiError && (e.code === 'unsupported' || e.status === 415)) error.value = t('branding.logo_bad_type')
    else {
      const err = errorText(e, t)
      error.value = err.text
      detail.value = err.detail
    }
  } finally {
    busy.value = false
  }
}

function remove() {
  confirm.require({
    header: t('branding.logo_remove'),
    message: t('branding.logo_remove_confirm'),
    acceptProps: { label: t('branding.logo_remove'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      busy.value = true
      error.value = ''
      try {
        const res = await api<BrandingInfo>('DELETE', '/branding/logo')
        toast.add({ severity: 'info', summary: t('branding.logo_removed'), life: 3000 })
        emit('update', res)
      } catch (e) {
        const err = errorText(e, t)
        error.value = err.text
        detail.value = err.detail
      } finally {
        busy.value = false
      }
    },
  })
}

const shown = computed(() => preview.value || props.logo?.url || '')
const kb = (bytes: number) => num(Math.max(1, Math.ceil(bytes / 1024)), lang.value)
</script>

<template>
  <div class="logo-field">
    <div class="frame" :class="{ empty: !shown }">
      <img v-if="shown" :src="shown" :alt="preview ? t('branding.logo_new') : t('branding.logo_current')" />
      <i v-else class="pi pi-image" aria-hidden="true" />
    </div>
    <div class="side">
      <div v-if="preview" class="state">{{ t('branding.logo_new') }}</div>
      <div v-else-if="logo" class="state">
        {{ t('branding.logo_current') }}
        <div class="app-muted small">{{ t('branding.logo_meta', { size: kb(logo.size), date: dateTime(logo.updated_at, lang) }) }}</div>
      </div>
      <div v-else class="app-muted small">{{ t('branding.logo_none') }}</div>

      <input ref="input" type="file" :accept="LOGO_TYPES.join(',')" class="file" tabindex="-1" aria-hidden="true" @change="picked" />
      <div class="buttons">
        <template v-if="preview">
          <Button :label="t('branding.logo_upload')" icon="pi pi-upload" size="small" :loading="busy" @click="upload" />
          <Button :label="t('app.cancel')" size="small" severity="secondary" outlined :disabled="busy" @click="cancel" />
        </template>
        <template v-else>
          <Button :label="t('branding.logo_choose')" icon="pi pi-image" size="small" severity="secondary" outlined :disabled="busy" @click="input?.click()" />
          <Button v-if="logo" :label="t('branding.logo_remove')" icon="pi pi-trash" size="small" severity="danger" text :loading="busy" @click="remove" />
        </template>
      </div>
    </div>
  </div>
  <Message v-if="error" severity="error" :closable="false" size="small" class="msg">
    {{ error }}
    <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
  </Message>
</template>

<style scoped>
.logo-field {
  display: flex;
  gap: 1rem;
  align-items: center;
  flex-wrap: wrap;
}
.frame {
  inline-size: 5.5rem;
  block-size: 5.5rem;
  flex: none;
  display: grid;
  place-items: center;
  border: 1px solid var(--p-content-border-color);
  border-radius: 0.75rem;
  background: var(--p-content-hover-background);
  overflow: hidden;
}
.frame.empty {
  border-style: dashed;
  color: var(--p-text-muted-color);
  font-size: 1.5rem;
}
.frame img {
  inline-size: 100%;
  block-size: 100%;
  object-fit: contain;
}
.side {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  min-inline-size: 0;
  flex: 1 1 12rem;
}
.state {
  font-weight: 500;
}
.small {
  font-size: 0.8rem;
  font-weight: 400;
}
.buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.file {
  position: absolute;
  inline-size: 1px;
  block-size: 1px;
  opacity: 0;
  pointer-events: none;
}
.msg {
  margin-block-start: 0.5rem;
}
.detail {
  font-size: 0.85rem;
}
</style>
