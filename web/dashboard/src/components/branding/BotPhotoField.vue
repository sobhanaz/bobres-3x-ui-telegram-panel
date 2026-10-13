<script setup lang="ts">
import { computed, nextTick, ref, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Button from 'primevue/button'
import Message from 'primevue/message'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { dateTime, num, type Lang } from '../../format'
import { BOT_PHOTO_TYPES, botPhotoProblem, botPhotoSizeOk, botPhotoUploadProblem, isWebP } from '../../branding'
import type { BrandingInfo } from '../../types'
import { imageSize, readDataUrl, readHead } from './images'

// The bot's Telegram profile photo: pick a PNG or JPEG, see it in a circle
// as Telegram shows it, then upload it (raw body, CSRF through api()). Core
// stores it as a square JPEG on white, which the white frame previews. The
// stored photo is read with the session (GET /branding/bot-photo); a fresh
// pick is a data: URL because the dashboard's CSP does not allow blob:
// images.
const props = defineProps<{ photo: BrandingInfo['bot_photo'] }>()
const emit = defineEmits<{ update: [info: BrandingInfo] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()
const confirm = useConfirm()

const input = ref<HTMLInputElement | null>(null)
const chooseBtn = ref<ComponentPublicInstance | null>(null)
const uploadBtn = ref<ComponentPublicInstance | null>(null)
const file = ref<File | null>(null)
const preview = ref('')
const square = ref(true)
const busy = ref(false)
const error = ref('')
const detail = ref('')

const problemText = { webp: 'branding.bot_photo_webp', bad_type: 'branding.bot_photo_bad_type', too_large: 'branding.bot_photo_too_large' }

// The buttons change with the state (v-if), so the one that had the focus
// is gone: the focus goes to the button that does the next step.
async function focusOn(button: { value: ComponentPublicInstance | null }) {
  await nextTick()
  ;(button.value?.$el as HTMLElement | undefined)?.focus()
}

async function picked(e: Event) {
  const el = e.target as HTMLInputElement
  const f = el.files?.[0]
  el.value = '' // the same file can be chosen again
  error.value = ''
  detail.value = ''
  if (!f) return
  try {
    // A file's type comes from its extension: a WebP saved as ".jpg" says it
    // is a JPEG, so its first bytes tell.
    const problem = isWebP(await readHead(f, 12)) ? 'webp' : botPhotoProblem(f)
    if (problem) {
      error.value = t(problemText[problem])
      return
    }
    const url = await readDataUrl(f)
    const { width, height } = await imageSize(url)
    if (!botPhotoSizeOk(width, height)) {
      error.value = t('branding.bot_photo_bad_size')
      return
    }
    file.value = f
    preview.value = url
    square.value = width === height
  } catch {
    error.value = t('branding.bot_photo_unreadable')
    return
  }
  void focusOn(uploadBtn)
}

function cancel() {
  file.value = null
  preview.value = ''
  error.value = ''
  void focusOn(chooseBtn)
}

async function upload() {
  if (!file.value) return
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    const res = await api<BrandingInfo>('PUT', '/branding/bot-photo', file.value)
    cancel()
    toast.add({ severity: 'success', summary: t('branding.bot_photo_saved'), life: 3000 })
    emit('update', res)
  } catch (e) {
    const problem = e instanceof ApiError ? botPhotoUploadProblem(e) : ''
    if (problem) error.value = t(problemText[problem])
    else {
      const err = errorText(e, t)
      error.value = err.text
      detail.value = err.detail
    }
    void focusOn(uploadBtn) // busy disabled it: ready to try again
  } finally {
    busy.value = false
  }
}

function remove() {
  confirm.require({
    header: t('branding.bot_photo_remove'),
    message: t('branding.bot_photo_remove_confirm'),
    acceptProps: { label: t('branding.bot_photo_remove'), severity: 'danger' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      busy.value = true
      error.value = ''
      try {
        const res = await api<BrandingInfo>('DELETE', '/branding/bot-photo')
        toast.add({ severity: 'info', summary: t('branding.bot_photo_removed'), life: 3000 })
        emit('update', res)
        void focusOn(chooseBtn) // the Remove button is gone
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

const shown = computed(() => preview.value || props.photo?.url || '')
const kb = (bytes: number) => num(Math.max(1, Math.ceil(bytes / 1024)), lang.value)
</script>

<template>
  <div class="photo-field">
    <div class="frame" :class="{ empty: !shown }">
      <img v-if="shown" :src="shown" :alt="preview ? t('branding.bot_photo_new') : t('branding.bot_photo_current')" />
      <i v-else class="pi pi-user" aria-hidden="true" />
    </div>
    <div class="side">
      <div v-if="preview" class="state">
        {{ t('branding.bot_photo_new') }}
        <div v-if="!square" class="app-muted small">{{ t('branding.bot_photo_padded') }}</div>
      </div>
      <div v-else-if="photo" class="state">
        {{ t('branding.bot_photo_current') }}
        <div class="app-muted small">{{ t('branding.bot_photo_meta', { size: kb(photo.size), date: dateTime(photo.updated_at, lang) }) }}</div>
      </div>
      <div v-else class="app-muted small">{{ t('branding.bot_photo_none') }}</div>

      <input ref="input" type="file" :accept="BOT_PHOTO_TYPES.join(',')" class="file" tabindex="-1" aria-hidden="true" @change="picked" />
      <div class="buttons">
        <template v-if="preview">
          <Button ref="uploadBtn" :label="t('branding.bot_photo_upload')" icon="pi pi-upload" size="small" :loading="busy" @click="upload" />
          <Button :label="t('app.cancel')" size="small" severity="secondary" outlined :disabled="busy" @click="cancel" />
        </template>
        <template v-else>
          <Button ref="chooseBtn" :label="t('branding.bot_photo_choose')" icon="pi pi-image" size="small" severity="secondary" outlined :disabled="busy" @click="input?.click()" />
          <Button v-if="photo" :label="t('branding.bot_photo_remove')" icon="pi pi-trash" size="small" severity="danger" text :loading="busy" @click="remove" />
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
.photo-field {
  display: flex;
  gap: 1rem;
  align-items: center;
  flex-wrap: wrap;
}
/* A circle, as Telegram shows a profile photo; white, as core pads it. */
.frame {
  inline-size: 5.5rem;
  block-size: 5.5rem;
  flex: none;
  display: grid;
  place-items: center;
  border: 1px solid var(--p-content-border-color);
  border-radius: 50%;
  background: #fff;
  overflow: hidden;
}
.frame.empty {
  border-style: dashed;
  background: var(--p-content-hover-background);
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
