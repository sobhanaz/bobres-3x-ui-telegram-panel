<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import Tag from 'primevue/tag'
import { api } from '../../api'
import { errorText } from '../../errors'
import { dateTime, isolate, type Lang } from '../../format'
import { updateCommand, updateDetail, updateReason, updateState, type UpdateState } from '../../system'
import type { SystemReply } from '../../types'

// Whether a newer BOBRES release is out, as the bot last read it (core has
// no way out; the bot checks every 12 h). Installing it stays on the server's
// command line: the page shows the command.
const { t, locale } = useI18n()
const toast = useToast()
const lang = computed(() => locale.value as Lang)

const reply = ref<SystemReply | null>(null)
const loading = ref(true)
const loadError = ref('')
const loadDetail = ref('')
async function load() {
  loading.value = true
  loadError.value = ''
  loadDetail.value = ''
  try {
    reply.value = await api<SystemReply>('GET', '/system')
  } catch (e) {
    const err = errorText(e, t)
    loadError.value = t('system.load_error') + ' ' + err.text
    loadDetail.value = err.detail
  } finally {
    loading.value = false
  }
}
onMounted(load)

const updates = computed(() => reply.value?.updates ?? null)
const state = computed<UpdateState>(() => updateState(updates.value))
const reason = computed(() => (reply.value ? updateReason(updates.value) : null))
const detail = computed(() => updateDetail(updates.value, reason.value))
const current = computed(() => updates.value?.current || reply.value?.version || '')
const latest = computed(() => updates.value?.latest || '')
const severity: Record<UpdateState, 'warn' | 'success' | 'secondary'> = { newer: 'warn', current: 'success', unknown: 'secondary' }

async function copyCommand() {
  try {
    await navigator.clipboard.writeText(updateCommand(latest.value))
    toast.add({ severity: 'info', summary: t('common.copied'), life: 2000 })
  } catch {
    /* clipboard blocked: the command is selectable */
  }
}
</script>

<template>
  <div class="updates">
    <div class="toolbar">
      <span v-if="updates?.checked_at" class="app-muted checked">{{ t('system.updates.checked_at', { time: dateTime(updates.checked_at, lang) }) }}</span>
      <span class="spacer" />
      <Button :label="t('system.refresh')" icon="pi pi-refresh" size="small" severity="secondary" outlined :loading="loading" class="refresh" @click="load" />
    </div>

    <Message v-if="loadError" severity="error" :closable="false" class="banner load-error">
      {{ loadError }}
      <div v-if="loadDetail" class="app-ltr detail">{{ loadDetail }}</div>
    </Message>

    <Card v-if="loading && !reply" class="versions-card">
      <template #content>
        <Skeleton height="1.2rem" width="60%" class="skeleton" />
        <Skeleton height="1.2rem" width="40%" />
      </template>
    </Card>

    <template v-else-if="reply">
      <Card class="versions-card">
        <template #title>
          <div class="card-title">
            <span>{{ t('system.updates.title') }}</span>
            <Tag :severity="severity[state]" :value="t('system.updates.state.' + state)" class="update-state" />
          </div>
        </template>
        <template #content>
          <dl class="app-facts facts">
            <dt>{{ t('system.updates.current') }}</dt>
            <dd class="app-ltr current-version">{{ current || '—' }}</dd>
            <dt>{{ t('system.updates.latest') }}</dt>
            <dd class="app-ltr latest-version">{{ latest || '—' }}</dd>
          </dl>
          <p v-if="state === 'current'" class="app-muted up-to-date">{{ t('system.updates.up_to_date') }}</p>
          <!-- not checked here; a release that failed the check says so below -->
          <p v-if="latest && updates && !updates.verified && reason !== 'bad_signature'" class="app-muted unverified">
            <i class="pi pi-info-circle" aria-hidden="true" />
            {{ t('system.updates.unverified') }}
          </p>
        </template>
      </Card>

      <Message v-if="state === 'newer'" severity="warn" icon="pi pi-arrow-circle-up" :closable="false" class="update-available">
        <div class="available-title">{{ t('system.updates.available', { version: isolate(latest) }) }}</div>
        <p class="available-hint">{{ t('system.updates.how') }}</p>
        <div class="command-row">
          <code class="app-ltr app-mono command">{{ updateCommand(latest) }}</code>
          <Button
            icon="pi pi-copy"
            text
            rounded
            size="small"
            :aria-label="t('system.updates.copy_command')"
            v-tooltip.top="t('common.copy')"
            class="copy-command"
            @click="copyCommand"
          />
        </div>
        <p class="available-note">{{ t('system.updates.backup_first') }}</p>
      </Message>

      <Message v-if="reason" :severity="state === 'unknown' ? 'secondary' : 'warn'" icon="pi pi-question-circle" :closable="false" class="not-checked">
        <div class="reason-title">{{ t(state === 'unknown' ? 'system.updates.could_not_check' : 'system.updates.last_check_failed') }}</div>
        <div class="reason">{{ t('system.updates.reason.' + reason) }}</div>
        <div v-if="detail" class="app-ltr detail error">{{ detail }}</div>
      </Message>
    </template>
  </div>
</template>

<style scoped>
.updates {
  display: flex;
  flex-direction: column;
  gap: var(--app-gap);
}
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
}
.spacer {
  flex: 1;
}
.card-title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
}
.skeleton {
  margin-block-end: 0.6rem;
}
.up-to-date,
.unverified {
  margin-block: 0.75rem 0;
}
.unverified .pi {
  margin-inline-end: 0.3rem;
  vertical-align: middle;
}
.available-title,
.reason-title {
  font-weight: 600;
}
.available-hint,
.available-note {
  margin-block: 0.4rem;
}
.available-note {
  font-size: 0.85rem;
}
.command-row {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
}
.command {
  padding: 0.2rem 0.6rem;
  border-radius: 0.375rem;
  background: var(--p-content-background);
  border: 1px solid var(--p-content-border-color);
  user-select: all;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
</style>
