<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Card from 'primevue/card'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import Tag from 'primevue/tag'
import { api } from '../../api'
import { errorText } from '../../errors'
import { isolate, num, type Lang } from '../../format'
import {
  LABEL_MAX,
  blank,
  canAdd,
  changed,
  draftsOf,
  firstProblem,
  inactiveFrom,
  isLimit,
  moveRow,
  normalizeRow,
  over,
  payload,
  rowProblems,
  serverField,
  type DraftRow,
  type RowField,
} from '../../joinChats'
import { describeCheck, unreachable, validChannel, type Problem } from '../../settings'
import type { ChannelCheck, JoinChatsReply } from '../../types'

// The channels and groups customers must join before they can use the bot:
// the whole list is edited and saved at once. The first row is the setting
// join.channel (the bot's /set and older bots read it there) and stays first;
// the others are extras, and the license says how many count. Core checks
// every row again and refuses to add one past the license.
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const toast = useToast()

const server = ref<JoinChatsReply | null>(null)
const rows = ref<DraftRow[]>([])
const loading = ref(true)
const loadError = ref('')

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const r = await api<JoinChatsReply>('GET', '/join-chats')
    server.value = r
    rows.value = draftsOf(r.chats)
    reset()
  } catch (e) {
    loadError.value = errorText(e, t).text
  } finally {
    loading.value = false
  }
}
onMounted(load)

const touched = ref<string[]>([])
const attempted = ref(false)
const serverErrors = ref<Record<string, string>>({})
const busy = ref(false)
const error = ref('')
const detail = ref('')
const checks = ref<Record<number, { severity: 'success' | 'warn' | 'error'; text: string; detail: string }>>({})
const checking = ref<number | null>(null)

const editable = computed(() => server.value?.editable === true)
const limit = computed(() => server.value?.limit ?? 1)
const max = computed(() => server.value?.max ?? 50)
const problems = computed(() => rowProblems(rows.value))
const dirty = computed(() => (server.value ? changed(rows.value, server.value.chats) : false))
const firstInactive = computed(() => inactiveFrom(limit.value, rows.value.length))
const overLimit = computed(() => over(limit.value, rows.value.length))
const addable = computed(() => editable.value && canAdd(limit.value, rows.value.length, max.value))
// The requirement is off while the first chat is empty; extras saved then do nothing.
const dormant = computed(() => !!server.value && !server.value.on && server.value.chats.length > 0)

const countText = computed(() =>
  limit.value < 0
    ? t('settings.join.count_unlimited', { used: num(rows.value.length, lang.value) })
    : t('settings.join.count', { used: num(rows.value.length, lang.value), limit: num(limit.value, lang.value) }),
)
const limitNote = computed(() => t('settings.join.limit_note', { limit: num(Math.max(limit.value, 1), lang.value) }))

const fieldId = (row: DraftRow, field: RowField) => `join-${row.id}-${field}`
const linkExample = 'https://t.me/…'
const titleId = 'settings-join-title'
const rowTitle = (i: number) => (i === 0 ? t('settings.join.first') : t('settings.join.nth', { n: num(i + 1, lang.value) }))

function problemText(p: Problem): string {
  const params: Record<string, string> = {}
  for (const [k, v] of Object.entries(p.params ?? {})) params[k] = num(v, lang.value)
  return t(`settings.invalid.${p.code}`, params, p.params?.max ?? 0)
}

// A problem shows once the field was left, or on Save.
function problemOf(row: DraftRow, i: number, field: RowField): string {
  const p = problems.value[i]?.[field]
  if (!p) return ''
  const seen = touched.value.includes(`${row.id}:${field}`) || (field === 'link' && touched.value.includes(`${row.id}:chat`))
  return attempted.value || seen ? problemText(p) : ''
}
const serverErrorOf = (row: DraftRow, field: RowField) => serverErrors.value[`${row.id}:${field}`] ?? ''

function touch(row: DraftRow, field: RowField) {
  const k = `${row.id}:${field}`
  if (!touched.value.includes(k)) touched.value = [...touched.value, k]
}

function edit(row: DraftRow, field: RowField, v: string) {
  rows.value = rows.value.map((r) => (r.id === row.id ? { ...r, [field]: v } : r))
  const k = `${row.id}:${field}`
  if (serverErrors.value[k]) {
    const rest = { ...serverErrors.value }
    delete rest[k]
    serverErrors.value = rest
  }
  if (field === 'chat') {
    const rest = { ...checks.value }
    delete rest[row.id]
    checks.value = rest
  }
  error.value = ''
  detail.value = ''
}

function add() {
  if (!addable.value) return
  const row = blank()
  rows.value = [...rows.value, row]
  void nextTick(() => document.getElementById(fieldId(row, 'chat'))?.focus())
}

function remove(row: DraftRow) {
  rows.value = rows.value.filter((r) => r.id !== row.id)
  error.value = ''
  detail.value = ''
}

function move(i: number, to: number) {
  rows.value = moveRow(rows.value, i, to)
}

function reset() {
  if (server.value) rows.value = draftsOf(server.value.chats)
  touched.value = []
  attempted.value = false
  serverErrors.value = {}
  checks.value = {}
  error.value = ''
  detail.value = ''
}

async function runCheck(row: DraftRow) {
  checking.value = row.id
  const rest = { ...checks.value }
  delete rest[row.id]
  checks.value = rest
  try {
    const res = await api<ChannelCheck>('POST', '/settings/channel-check', { chat: normalizeRow(row).chat })
    const view = describeCheck(res)
    checks.value = { ...checks.value, [row.id]: { severity: view.severity, text: t(view.key, { title: isolate(view.title) }), detail: view.detail } }
  } catch (e) {
    const text = unreachable(e) ? t('settings.check.unreachable') : errorText(e, t).text
    checks.value = { ...checks.value, [row.id]: { severity: 'error', text, detail: unreachable(e) ? '' : errorText(e, t).detail } }
  } finally {
    checking.value = null
  }
}
const checkable = (row: DraftRow) => validChannel(normalizeRow(row).chat)

async function save() {
  if (!dirty.value || busy.value || !editable.value) return
  attempted.value = true
  const bad = firstProblem(problems.value)
  if (bad) {
    error.value = t('settings.fix_errors')
    detail.value = ''
    await nextTick()
    document.getElementById(fieldId(rows.value[bad[0]], bad[1]))?.focus()
    return
  }
  busy.value = true
  error.value = ''
  detail.value = ''
  try {
    const reply = await api<JoinChatsReply>('PUT', '/join-chats', payload(rows.value))
    server.value = reply
    rows.value = draftsOf(reply.chats)
    touched.value = []
    attempted.value = false
    serverErrors.value = {}
    checks.value = {}
    toast.add({ severity: 'success', summary: t('settings.saved'), detail: t('settings.saved_detail'), life: 4000 })
  } catch (e) {
    const err = errorText(e, t)
    const where = serverField(e)
    error.value = err.text
    if (where && rows.value[where.index]) {
      serverErrors.value = { ...serverErrors.value, [`${rows.value[where.index].id}:${where.field}`]: err.detail || err.text }
    } else {
      detail.value = err.detail
    }
    if (isLimit(e)) detail.value = err.detail
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Card class="settings-card join-card">
    <template #title>
      <div class="title-row">
        <h2 :id="titleId" class="card-title">{{ t('settings.group.join.title') }}</h2>
      </div>
    </template>
    <template #subtitle>{{ t('settings.group.join.desc') }}</template>
    <template #content>
      <div v-if="loading && !server" aria-busy="true">
        <Skeleton height="9rem" border-radius="var(--app-radius)" />
      </div>
      <Message v-else-if="loadError" severity="error" :closable="false" class="note">
        <div class="load-error">
          <span>{{ loadError }}</span>
          <Button :label="t('app.retry')" icon="pi pi-refresh" size="small" severity="secondary" outlined @click="load" />
        </div>
      </Message>
      <template v-else-if="server">
        <Message v-if="!editable" severity="secondary" icon="pi pi-lock" :closable="false" class="note">{{ t('settings.join.locked') }}</Message>
        <Message severity="info" :closable="false" class="note">{{ t('settings.group.join.note') }}</Message>
        <Message v-if="dormant" severity="warn" :closable="false" class="note">{{ t('settings.join.dormant') }}</Message>

        <form class="join-form" :aria-labelledby="titleId" novalidate @submit.prevent="save">
          <p class="count" :class="{ 'is-over': overLimit }" aria-live="polite">{{ countText }}</p>
          <Message v-if="overLimit" severity="warn" :closable="false" class="note">
            {{ t('settings.join.over_note', { limit: num(Math.max(limit, 1), lang) }) }}
          </Message>

          <p v-if="!rows.length" class="app-muted empty">{{ t('settings.join.empty') }}</p>
          <ol v-else class="rows">
            <li v-for="(row, i) in rows" :key="row.id" class="row" :class="{ inactive: i >= firstInactive }">
              <div class="row-head">
                <span class="row-title">{{ rowTitle(i) }}</span>
                <Tag v-if="i >= firstInactive" severity="warn" :value="t('settings.join.inactive')" :title="t('settings.join.inactive_hint')" />
                <span class="row-actions">
                  <Button
                    v-if="editable && i > 1"
                    type="button"
                    icon="pi pi-arrow-up"
                    severity="secondary"
                    text
                    rounded
                    size="small"
                    :aria-label="t('settings.join.move_up', { n: num(i + 1, lang) })"
                    @click="move(i, i - 1)"
                  />
                  <Button
                    v-if="editable && i > 0 && i < rows.length - 1"
                    type="button"
                    icon="pi pi-arrow-down"
                    severity="secondary"
                    text
                    rounded
                    size="small"
                    :aria-label="t('settings.join.move_down', { n: num(i + 1, lang) })"
                    @click="move(i, i + 1)"
                  />
                  <Button
                    v-if="editable"
                    type="button"
                    icon="pi pi-trash"
                    severity="danger"
                    text
                    rounded
                    size="small"
                    :aria-label="t('settings.join.remove', { n: num(i + 1, lang) })"
                    @click="remove(row)"
                  />
                </span>
              </div>
              <div class="row-fields">
                <div class="field">
                  <label :for="fieldId(row, 'chat')" class="label">{{ t('settings.join.field.chat') }}</label>
                  <div class="control">
                    <InputText
                      :id="fieldId(row, 'chat')"
                      :model-value="row.chat"
                      dir="ltr"
                      :maxlength="64"
                      placeholder="@mychannel"
                      :readonly="!editable"
                      :invalid="!!problemOf(row, i, 'chat') || !!serverErrorOf(row, 'chat')"
                      :aria-describedby="problemOf(row, i, 'chat') || serverErrorOf(row, 'chat') ? fieldId(row, 'chat') + '-error' : undefined"
                      autocomplete="off"
                      :spellcheck="false"
                      class="input"
                      @update:model-value="(v) => edit(row, 'chat', v ?? '')"
                      @blur="touch(row, 'chat')"
                    />
                    <Button
                      type="button"
                      :label="t('settings.check.button')"
                      icon="pi pi-search"
                      severity="secondary"
                      outlined
                      :loading="checking === row.id"
                      :disabled="!checkable(row)"
                      @click="runCheck(row)"
                    />
                  </div>
                  <div v-if="problemOf(row, i, 'chat') || serverErrorOf(row, 'chat')" :id="fieldId(row, 'chat') + '-error'" role="alert">
                    <Message v-if="problemOf(row, i, 'chat')" severity="error" size="small" variant="simple">{{ problemOf(row, i, 'chat') }}</Message>
                    <Message v-if="serverErrorOf(row, 'chat')" severity="error" size="small" variant="simple">
                      <span class="app-ltr">{{ serverErrorOf(row, 'chat') }}</span>
                    </Message>
                  </div>
                </div>
                <div class="field">
                  <label :for="fieldId(row, 'link')" class="label">{{ t('settings.join.field.link') }}</label>
                  <InputText
                    :id="fieldId(row, 'link')"
                    :model-value="row.link"
                    dir="ltr"
                    :maxlength="1000"
                    :placeholder="linkExample"
                    :readonly="!editable"
                    :invalid="!!problemOf(row, i, 'link') || !!serverErrorOf(row, 'link')"
                    :aria-describedby="problemOf(row, i, 'link') || serverErrorOf(row, 'link') ? fieldId(row, 'link') + '-error' : undefined"
                    autocomplete="off"
                    :spellcheck="false"
                    @update:model-value="(v) => edit(row, 'link', v ?? '')"
                    @blur="touch(row, 'link')"
                  />
                  <div v-if="problemOf(row, i, 'link') || serverErrorOf(row, 'link')" :id="fieldId(row, 'link') + '-error'" role="alert">
                    <Message v-if="problemOf(row, i, 'link')" severity="error" size="small" variant="simple">{{ problemOf(row, i, 'link') }}</Message>
                    <Message v-if="serverErrorOf(row, 'link')" severity="error" size="small" variant="simple">
                      <span class="app-ltr">{{ serverErrorOf(row, 'link') }}</span>
                    </Message>
                  </div>
                </div>
                <div class="field">
                  <label :for="fieldId(row, 'label')" class="label">{{ t('settings.join.field.label') }}</label>
                  <InputText
                    :id="fieldId(row, 'label')"
                    :model-value="row.label"
                    dir="auto"
                    :maxlength="LABEL_MAX * 2"
                    :placeholder="t('settings.join.label_example')"
                    :readonly="!editable"
                    :invalid="!!problemOf(row, i, 'label') || !!serverErrorOf(row, 'label')"
                    :aria-describedby="problemOf(row, i, 'label') || serverErrorOf(row, 'label') ? fieldId(row, 'label') + '-error' : undefined"
                    autocomplete="off"
                    @update:model-value="(v) => edit(row, 'label', v ?? '')"
                    @blur="touch(row, 'label')"
                  />
                  <div v-if="problemOf(row, i, 'label') || serverErrorOf(row, 'label')" :id="fieldId(row, 'label') + '-error'" role="alert">
                    <Message v-if="problemOf(row, i, 'label')" severity="error" size="small" variant="simple">{{ problemOf(row, i, 'label') }}</Message>
                    <Message v-if="serverErrorOf(row, 'label')" severity="error" size="small" variant="simple">
                      <span class="app-ltr">{{ serverErrorOf(row, 'label') }}</span>
                    </Message>
                  </div>
                </div>
              </div>
              <Message v-if="checks[row.id]" :severity="checks[row.id].severity" :closable="false" class="check" aria-live="polite">
                {{ checks[row.id].text }}
                <div v-if="checks[row.id].detail" class="app-ltr detail">{{ checks[row.id].detail }}</div>
              </Message>
            </li>
          </ol>

          <div v-if="editable" class="add">
            <Button type="button" :label="t('settings.join.add')" icon="pi pi-plus" severity="secondary" outlined :disabled="!addable" @click="add" />
            <small v-if="!addable && rows.length >= max" class="app-muted">{{ t('settings.join.max', { max: num(max, lang) }) }}</small>
            <small v-else-if="!addable && limit >= 0" class="app-muted limit-note">
              {{ limitNote }}
              <RouterLink :to="{ name: 'system', query: { tab: 'license' } }">{{ t('settings.join.see_license') }}</RouterLink>
            </small>
          </div>

          <Message v-if="error" severity="error" :closable="false" class="error">
            {{ error }}
            <div v-if="detail" class="app-ltr detail">{{ detail }}</div>
          </Message>
          <div v-if="editable" class="actions">
            <span v-if="dirty" class="app-muted unsaved" aria-live="polite">{{ t('settings.unsaved') }}</span>
            <Button type="button" :label="t('settings.reset')" icon="pi pi-undo" severity="secondary" text :disabled="!dirty || busy" @click="reset" />
            <Button type="submit" :label="t('common.save')" icon="pi pi-check" :loading="busy" :disabled="!dirty" />
          </div>
        </form>
      </template>
    </template>
  </Card>
</template>

<style scoped>
.title-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}
.card-title {
  margin: 0;
  font-size: 1.1rem;
  font-weight: 700;
}
.note {
  margin-block-end: 1rem;
}
.load-error {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
}
.settings-card :deep(.p-card-body),
.settings-card :deep(.p-card-content) {
  flex: 1 1 auto;
}
.join-form {
  display: flex;
  flex-direction: column;
}
.count {
  margin: 0 0 0.75rem;
  font-weight: 600;
}
.count.is-over {
  color: var(--p-orange-600, #ea580c);
}
.empty {
  margin-block: 0.5rem 1rem;
}
.rows {
  list-style: none;
  margin: 0 0 1rem;
  padding: 0;
  display: grid;
  gap: 1rem;
}
.row {
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--app-radius);
  padding: 0.75rem 1rem 1rem;
}
.row.inactive {
  opacity: 0.8;
  border-style: dashed;
}
.row-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  margin-block-end: 0.5rem;
}
.row-title {
  font-weight: 700;
}
.row-actions {
  margin-inline-start: auto;
  display: inline-flex;
  gap: 0.25rem;
}
.row-fields {
  display: grid;
  gap: 0.9rem 1.25rem;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 15rem), 1fr));
}
.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  min-inline-size: 0;
}
.label {
  font-weight: 600;
}
.control {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.input {
  flex: 1 1 10rem;
  min-inline-size: 0;
}
.check {
  margin-block-start: 0.75rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
.add {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.75rem;
  margin-block-end: 1.25rem;
}
.limit-note a {
  margin-inline-start: 0.25rem;
}
.error {
  margin-block-end: 1.25rem;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 0.5rem;
  margin-block-start: auto;
  padding-block-start: 1rem;
  border-block-start: 1px solid var(--p-content-border-color);
}
.unsaved {
  margin-inline-end: auto;
  font-size: 0.85rem;
}
</style>
