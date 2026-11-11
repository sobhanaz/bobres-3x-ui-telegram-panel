<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import { api } from '../../api'
import { errorText } from '../../errors'
import type { Lang } from '../../format'
import { counts, moveEmpty, type InternalMove } from '../../internalOrders'
import { useAuth } from '../../stores/auth'

// Under the switch that marks owner accounts' new purchases internal: count their earlier purchases as internal too (a dry run says how many
// rows and how much money would move, then the owner confirms), or put them back. Only the flag changes; nothing is deleted or edited.
const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const auth = useAuth()
const confirm = useConfirm()
const toast = useToast()
const busy = ref<'' | 'backfill' | 'undo'>('')

async function failed(e: unknown) {
  toast.add({ severity: 'error', summary: errorText(e, t).text, life: 6000 })
}

async function run(path: 'backfill' | 'undo', body: Record<string, unknown>): Promise<InternalMove | null> {
  busy.value = path
  try {
    return await api<InternalMove>('POST', '/reports/internal/' + path, body)
  } catch (e) {
    await failed(e)
    return null
  } finally {
    busy.value = ''
  }
}

// Count: ask what would move (a dry run), say so, and move it only when the owner confirms.
async function count() {
  const plan = await run('backfill', { dry_run: true })
  if (!plan) return
  if (moveEmpty(plan)) {
    toast.add({ severity: 'info', summary: t('settings.internal.none'), life: 6000 })
    return
  }
  confirm.require({
    header: t('settings.internal.confirm_title'),
    message: t('settings.internal.confirm', counts(plan, lang.value)),
    icon: 'pi pi-exclamation-triangle',
    acceptProps: { label: t('settings.internal.count'), severity: 'warn' },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      const done = await run('backfill', { dry_run: false })
      if (!done) return
      toast.add({ severity: 'success', summary: t('settings.internal.counted', counts(done, lang.value)), life: 8000 })
      emit('changed')
    },
  })
}

function undo() {
  confirm.require({
    header: t('settings.internal.undo_title'),
    message: t('settings.internal.undo_confirm'),
    icon: 'pi pi-undo',
    acceptProps: { label: t('settings.internal.undo') },
    rejectProps: { label: t('app.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      const done = await run('undo', {})
      if (!done) return
      toast.add({
        severity: 'success',
        summary: moveEmpty(done) ? t('settings.internal.undone_none') : t('settings.internal.undone', counts(done, lang.value)),
        life: 8000,
      })
      emit('changed')
    },
  })
}
</script>

<template>
  <section v-if="auth.can('settings.write')" class="internal-actions" aria-labelledby="internal-actions-title">
    <h3 id="internal-actions-title" class="title">{{ t('settings.internal.title') }}</h3>
    <p class="app-muted help">{{ t('settings.internal.help') }}</p>
    <div class="buttons">
      <Button
        class="count-internal"
        :label="t('settings.internal.count')"
        icon="pi pi-eye-slash"
        severity="secondary"
        outlined
        :loading="busy === 'backfill'"
        :disabled="busy !== ''"
        @click="count"
      />
      <Button
        class="undo-internal"
        :label="t('settings.internal.undo')"
        icon="pi pi-undo"
        severity="secondary"
        text
        :loading="busy === 'undo'"
        :disabled="busy !== ''"
        @click="undo"
      />
    </div>
  </section>
</template>

<style scoped>
.internal-actions {
  margin-block-end: 1.25rem;
  padding-block-start: 1rem;
  border-block-start: 1px solid var(--p-content-border-color);
}
.title {
  margin: 0 0 0.25rem;
  font-size: 0.95rem;
  font-weight: 600;
}
.help {
  margin: 0 0 0.75rem;
  font-size: 0.85rem;
}
.buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
</style>
