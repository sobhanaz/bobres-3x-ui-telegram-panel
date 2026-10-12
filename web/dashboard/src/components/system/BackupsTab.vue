<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Card from 'primevue/card'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import Tag from 'primevue/tag'
import { api, ApiError } from '../../api'
import { errorText } from '../../errors'
import { bytes, dateTime, type Lang } from '../../format'
import { backupPoll, backupSent, backupsState, INSTALL_COMMAND, notSentReason, unixOf, type BackupSent } from '../../system'
import { useAuth } from '../../stores/auth'
import type { BackupRun, SystemBackups } from '../../types'

// The backups the backup service keeps on the server (the last 7), made daily
// and on demand, each sent by the bot to the owner on Telegram. The owner can
// start one now; restoring stays on the server, with the backup key.
const { t, locale } = useI18n()
const toast = useToast()
const auth = useAuth()
const lang = computed(() => locale.value as Lang)
const canRun = computed(() => auth.can('system.write'))

const reply = ref<SystemBackups | null>(null)
const loading = ref(true)
const loadError = ref('')
const loadDetail = ref('')
// while a backup is being made the list is read again every few seconds, a
// limited number of times; a slower answer never replaces a newer one
let seq = 0
let timer: ReturnType<typeof setTimeout> | undefined
let tries = 0
let gone = false
async function load() {
  const n = ++seq
  loading.value = true
  loadError.value = ''
  loadDetail.value = ''
  try {
    const r = await api<SystemBackups>('GET', '/system/backups')
    if (n !== seq) return
    reply.value = r
    if (r.running) again()
    else tries = 0
  } catch (e) {
    if (n !== seq) return
    const err = errorText(e, t)
    loadError.value = t('system.backups.load_error') + ' ' + err.text
    loadDetail.value = err.detail
  } finally {
    if (n === seq) loading.value = false
  }
}
function again() {
  if (gone || timer || tries >= backupPoll.tries) return
  tries++
  timer = setTimeout(() => {
    timer = undefined
    void load()
  }, tries > backupPoll.slowAfter ? backupPoll.ms * 5 : backupPoll.ms)
}
// reading again by hand starts the count over
function refresh() {
  tries = 0
  void load()
}
onMounted(load)
onBeforeUnmount(() => {
  gone = true
  clearTimeout(timer)
})

const state = computed(() => (reply.value ? backupsState(reply.value) : null))
const running = computed(() => state.value === 'ok' && reply.value?.running === true)
const list = computed(() =>
  (reply.value?.backups ?? []).map((b) => ({
    ...b,
    at: unixOf(b.created_at),
    sentAt: unixOf(b.sent_at),
    sent: backupSent(b),
    why: notSentReason(b.telegram_error),
  })),
)
const nextRun = computed(() => unixOf(reply.value?.next_run))
const sentSeverity: Record<BackupSent, 'success' | 'warn' | 'danger'> = { sent: 'success', not_sent: 'warn', failed: 'danger' }

const starting = ref(false)
const runError = ref('')
const runDetail = ref('')
async function runNow() {
  starting.value = true
  runError.value = ''
  runDetail.value = ''
  try {
    await api<BackupRun>('POST', '/system/backups/run')
    toast.add({ severity: 'success', summary: t('system.backups.started'), life: 3000 })
  } catch (e) {
    if (!(e instanceof ApiError && e.status === 409)) {
      const err = errorText(e, t)
      runError.value = t('system.backups.run_error') + ' ' + err.text
      runDetail.value = err.detail
      return
    }
    toast.add({ severity: 'info', summary: t('system.backups.already_running'), life: 3000 })
  } finally {
    starting.value = false
  }
  // it runs in the background: shown as running until the service says done
  if (reply.value) reply.value = { ...reply.value, running: true }
  tries = 0
  again()
}

async function copyCommand() {
  try {
    await navigator.clipboard.writeText(INSTALL_COMMAND)
    toast.add({ severity: 'info', summary: t('common.copied'), life: 2000 })
  } catch {
    /* clipboard blocked: the command is selectable */
  }
}
</script>

<template>
  <div class="backups">
    <div class="toolbar">
      <Button
        v-if="canRun && state === 'ok'"
        :label="t('system.backups.run')"
        icon="pi pi-database"
        size="small"
        :loading="starting"
        :disabled="running || starting"
        class="run-backup"
        @click="runNow"
      />
      <span class="spacer" />
      <Button :label="t('system.refresh')" icon="pi pi-refresh" size="small" severity="secondary" outlined :loading="loading" class="refresh" @click="refresh" />
    </div>

    <Message v-if="loadError" severity="error" :closable="false" class="banner load-error">
      {{ loadError }}
      <div v-if="loadDetail" class="app-ltr detail">{{ loadDetail }}</div>
    </Message>
    <Message v-if="runError" severity="error" :closable="false" class="banner run-error">
      {{ runError }}
      <div v-if="runDetail" class="app-ltr detail">{{ runDetail }}</div>
    </Message>

    <Card v-if="loading && !reply" class="backups-card">
      <template #content>
        <div class="skeletons">
          <Skeleton v-for="i in 3" :key="i" height="1.2rem" :width="80 - i * 15 + '%'" />
        </div>
      </template>
    </Card>

    <Message v-else-if="state === 'not_installed'" severity="secondary" icon="pi pi-box" :closable="false" class="backups-not-installed">
      <div class="title">{{ t('system.backups.not_installed_title') }}</div>
      <p class="hint">{{ t('system.backups.not_installed') }}</p>
      <div class="command-row">
        <code class="app-ltr app-mono command">{{ INSTALL_COMMAND }}</code>
        <Button
          icon="pi pi-copy"
          text
          rounded
          size="small"
          :aria-label="t('system.backups.copy_command')"
          v-tooltip.top="t('common.copy')"
          class="copy-command"
          @click="copyCommand"
        />
      </div>
    </Message>

    <Message v-else-if="state === 'unreachable'" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="backups-unreachable">
      {{ t('system.backups.unreachable') }}
    </Message>

    <template v-else-if="state === 'ok' && reply">
      <Message v-if="running" severity="info" icon="pi pi-spin pi-spinner" :closable="false" class="backup-running">
        {{ t('system.backups.running') }}
      </Message>
      <Message v-if="reply.last_error" severity="warn" icon="pi pi-exclamation-triangle" :closable="false" class="last-error">
        <div class="title">{{ t('system.backups.last_failed') }}</div>
        <div class="app-ltr detail error">{{ reply.last_error }}</div>
      </Message>

      <Card class="schedule-card">
        <template #content>
          <dl class="app-facts facts">
            <dt>{{ t('system.backups.next_run') }}</dt>
            <dd class="next-run">{{ nextRun ? dateTime(nextRun, lang) : '—' }}</dd>
            <dt>{{ t('system.backups.disk_free') }}</dt>
            <dd class="disk-free">{{ reply.disk_free != null ? bytes(reply.disk_free, lang) : '—' }}</dd>
            <template v-if="reply.dir">
              <dt>{{ t('system.backups.dir') }}</dt>
              <dd class="app-ltr app-mono dir">{{ reply.dir }}</dd>
            </template>
          </dl>
          <p class="app-muted kept">{{ t('system.backups.kept') }}</p>
        </template>
      </Card>

      <Card class="backups-card">
        <template #title>{{ t('system.backups.list') }}</template>
        <template #content>
          <p v-if="!list.length" class="app-muted empty">{{ t('system.backups.empty') }}</p>
          <ol v-else class="backup-list" :aria-label="t('system.backups.list_label')">
            <li v-for="b in list" :key="b.file || String(b.created_at)" class="backup" :class="b.sent">
              <div class="backup-head">
                <time class="time" :datetime="b.at ? new Date(b.at * 1000).toISOString() : undefined">{{ b.at ? dateTime(b.at, lang) : '—' }}</time>
                <span v-if="b.sent !== 'failed'" class="size">{{ bytes(b.size, lang) }}</span>
                <Tag :severity="sentSeverity[b.sent]" :value="t('system.backups.sent.' + b.sent)" class="sent-state" />
              </div>
              <div v-if="b.sent === 'failed'" class="why">
                {{ t('system.backups.failed') }}
                <div class="app-ltr detail error">{{ b.error }}</div>
              </div>
              <div v-else-if="b.sent === 'sent' && b.sentAt" class="app-muted sent-at">{{ t('system.backups.sent_at', { time: dateTime(b.sentAt, lang) }) }}</div>
              <div v-else-if="b.why" class="why">
                <span class="reason">{{ t('system.backups.reason.' + b.why.reason) }}</span>
                <div v-if="b.why.detail" class="app-ltr detail error">{{ b.why.detail }}</div>
              </div>
              <div v-if="b.file" class="app-ltr app-mono file">{{ b.file }}</div>
            </li>
          </ol>
        </template>
      </Card>
    </template>

    <Message v-if="state === 'ok'" severity="secondary" icon="pi pi-replay" :closable="false" class="restore-note">
      <div class="title">{{ t('system.backups.restore_title') }}</div>
      <p class="hint">{{ t('system.backups.restore') }}</p>
      <p class="hint later">{{ t('system.backups.restore_later') }}</p>
    </Message>
  </div>
</template>

<style scoped>
.backups {
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
.skeletons {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}
.title {
  font-weight: 600;
}
.hint {
  margin-block: 0.4rem 0;
}
.later {
  font-size: 0.85rem;
}
.command-row {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  margin-block-start: 0.4rem;
}
.command {
  padding: 0.2rem 0.6rem;
  border-radius: 0.375rem;
  background: var(--p-content-background);
  border: 1px solid var(--p-content-border-color);
  user-select: all;
}
.kept {
  margin-block: 0.75rem 0;
  font-size: 0.85rem;
}
.empty {
  margin: 0;
}
.backup-list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.backup {
  padding-block: 0.6rem;
  border-block-end: 1px solid var(--p-content-border-color);
}
.backup:first-child {
  padding-block-start: 0;
}
.backup:last-child {
  border-block-end: 0;
}
.backup-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem 0.75rem;
}
.time {
  font-weight: 600;
  unicode-bidi: isolate;
}
.size {
  color: var(--p-text-muted-color);
  unicode-bidi: isolate;
}
.why,
.sent-at {
  margin-block-start: 0.3rem;
  font-size: 0.9rem;
}
.file {
  margin-block-start: 0.25rem;
  color: var(--p-text-muted-color);
  font-size: 0.8rem;
  overflow-wrap: anywhere;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
</style>
