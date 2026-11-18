<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import Message from 'primevue/message'
import { api } from '../../api'
import { errorText } from '../../errors'
import { isolate, LANGS, num, type Lang } from '../../format'
import { describeAccess, MAX_CARD_LABEL, MAX_CHANNEL_CARDS, normalizeChannelCards, parseChannelCards, tmePostChat, unreachable } from '../../settings'
import type { ChannelCheck } from '../../types'

// The channel buttons of the bot's menu (setting bot.channel_cards): rows of the button's text in Persian and in English, the link of the channel and
// the link of the channel's first post, in the order the buttons come in the menu. The setting is compact JSON; the rows keep what was typed, so a
// half-typed link is not lost, and the value sent up is the rows as JSON (the card flags a row that is not right and saves the list made canonical).
// A text in another language that is already saved (Arabic, Russian, Turkish) has no input here: it stays with its row. A button under each post link asks
// the bot whether it can copy the posts of that channel (it must be an administrator, and the channel must not restrict saving content): the answer is
// shown under the row, belongs to the row (it moves with it) and goes when the post link changes.
const props = defineProps<{ modelValue: string; inputId: string; invalid?: boolean; locked?: boolean; describedBy?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; touch: [] }>()
const { t, locale } = useI18n()
const lang = computed(() => locale.value as Lang)

type Field = 'fa' | 'en' | 'channel' | 'post'

interface Check {
  /** tells this check from the next one: an answer that comes after the row changed, or after another check began, is dropped */
  id: number
  busy: boolean
  result: { severity: 'success' | 'warn' | 'error'; text: string; detail: string } | null
}

interface Row {
  /** tells this row from the others while rows are added, removed and moved (an answer of the bot finds its row by it) */
  uid: number
  fa: string
  en: string
  channel: string
  post: string
  /** the texts in the other languages, as saved */
  more: Partial<Record<Lang, string>>
  check: Check | null
}

let lastRow = 0
let lastCheck = 0

function rowsOf(text: string): Row[] {
  const p = parseChannelCards(text)
  return 'cards' in p ? p.cards.map(({ label: { fa = '', en = '', ...more }, channel, post }) => ({ uid: ++lastRow, fa, en, channel, post, more, check: null })) : []
}

/** draft is the rows as the value of the setting ("" for none). */
function draft(rs: Row[]): string {
  if (!rs.length) return ''
  return JSON.stringify(
    rs.map((r) => {
      const texts: Partial<Record<Lang, string>> = { fa: r.fa, en: r.en, ...r.more }
      const label: Partial<Record<Lang, string>> = {}
      for (const l of LANGS) if (texts[l]?.trim()) label[l] = texts[l]
      const channel = r.channel.trim()
      return channel ? { label, channel, post: r.post.trim() } : { label, post: r.post.trim() }
    }),
  )
}

const rows = ref<Row[]>(rowsOf(props.modelValue))
// A value that comes down from the card (a save, a reset) replaces the rows; the echo of our own edit does not.
watch(
  () => props.modelValue,
  (v) => {
    if (normalizeChannelCards(v) !== normalizeChannelCards(draft(rows.value))) rows.value = rowsOf(v)
  },
)

// The first input of the first row has the field's id (the card focuses it on Save, the label points at it); every other input has one of its own.
const fid = (i: number, f: Field | 'up' | 'down' | 'check') => (i === 0 && f === 'fa' ? props.inputId : `${props.inputId}-${i}-${f}`)

function changed() {
  emit('update:modelValue', draft(rows.value))
}
function setField(i: number, field: Field, v: string | undefined) {
  // another post link is another channel to check: what the bot said of the old one goes
  rows.value = rows.value.map((r, j) => (j === i ? { ...r, [field]: v ?? '', ...(field === 'post' ? { check: null } : {}) } : r))
  changed()
}
async function add() {
  rows.value = [...rows.value, { uid: ++lastRow, fa: '', en: '', channel: '', post: '', more: {}, check: null }]
  changed()
  emit('touch')
  // the new button is what is typed next
  await nextTick()
  document.getElementById(fid(rows.value.length - 1, 'fa'))?.focus()
}
function remove(i: number) {
  rows.value = rows.value.filter((_, j) => j !== i)
  changed()
  emit('touch')
}
async function move(i: number, by: -1 | 1) {
  const j = i + by
  if (j < 0 || j >= rows.value.length) return
  const next = [...rows.value]
  ;[next[i], next[j]] = [next[j], next[i]]
  rows.value = next
  changed()
  emit('touch')
  // The arrows stay where they are and the rows move: keep the focus on the row that moved, on the same arrow or, at the end of the list, the other one.
  await nextTick()
  const same = document.getElementById(fid(j, by < 0 ? 'up' : 'down')) as HTMLButtonElement | null
  ;(same && !same.disabled ? same : document.getElementById(fid(j, by < 0 ? 'down' : 'up')))?.focus()
}

const canAdd = computed(() => !props.locked && rows.value.length < MAX_CHANNEL_CARDS)
// The chat each post is in, which is where the bot copies from ("" while the post link is not one: there is nothing to check yet).
const chats = computed(() => rows.value.map((r) => tmePostChat(r.post)))

async function runCheck(i: number) {
  const row = rows.value[i]
  const chat = chats.value[i]
  if (!row || !chat || row.check?.busy) return
  const { uid } = row
  const id = ++lastCheck
  row.check = { id, busy: true, result: null }
  let result: NonNullable<Check['result']>
  try {
    const view = describeAccess(await api<ChannelCheck>('POST', '/settings/channel-check', { chat }))
    result = { severity: view.severity, text: t(view.key, { title: isolate(view.title || chat) }), detail: view.detail }
  } catch (e) {
    if (unreachable(e)) result = { severity: 'error', text: t('settings.check.unreachable'), detail: '' }
    else result = { severity: 'error', ...errorText(e, t) }
  }
  // The answer is for the row it was asked for, if that is still there with the same post link: rows are added, removed and moved while the bot answers.
  const now = rows.value.find((r) => r.uid === uid)
  if (now?.check?.id === id) now.check = { id, busy: false, result }
}
const channelExample = 'https://t.me/yourchannel'
const postExample = 'https://t.me/yourchannel/2'
</script>

<template>
  <div class="channel-cards" role="group" :aria-describedby="describedBy">
    <p v-if="!rows.length" class="app-muted empty">{{ t('settings.channel_cards.empty') }}</p>
    <div
      v-for="(r, i) in rows"
      :key="i"
      class="channel-card"
      role="group"
      :aria-label="t('settings.channel_cards.row', { n: num(i + 1, lang) })"
      :aria-busy="r.check?.busy || undefined"
    >
      <div class="head">
        <span class="title">{{ t('settings.channel_cards.row', { n: num(i + 1, lang) }) }}</span>
        <span v-if="!locked" class="actions">
          <Button
            :id="fid(i, 'up')"
            type="button"
            icon="pi pi-arrow-up"
            severity="secondary"
            text
            rounded
            size="small"
            :disabled="i === 0"
            :aria-label="t('settings.channel_cards.move_up', { n: num(i + 1, lang) })"
            @click="move(i, -1)"
          />
          <Button
            :id="fid(i, 'down')"
            type="button"
            icon="pi pi-arrow-down"
            severity="secondary"
            text
            rounded
            size="small"
            :disabled="i === rows.length - 1"
            :aria-label="t('settings.channel_cards.move_down', { n: num(i + 1, lang) })"
            @click="move(i, 1)"
          />
          <Button
            type="button"
            icon="pi pi-trash"
            severity="secondary"
            text
            rounded
            size="small"
            :aria-label="t('settings.channel_cards.remove', { n: num(i + 1, lang) })"
            @click="remove(i)"
          />
        </span>
      </div>
      <div class="fields">
        <div class="cell">
          <label class="cap" :for="fid(i, 'fa')">{{ t('settings.channel_cards.label_fa') }}</label>
          <InputText
            :id="fid(i, 'fa')"
            :model-value="r.fa"
            dir="auto"
            :maxlength="MAX_CARD_LABEL * 2"
            autocomplete="off"
            :readonly="locked"
            :invalid="invalid"
            class="input"
            @update:model-value="(v) => setField(i, 'fa', v)"
            @blur="emit('touch')"
          />
        </div>
        <div class="cell">
          <label class="cap" :for="fid(i, 'en')">{{ t('settings.channel_cards.label_en') }}</label>
          <InputText
            :id="fid(i, 'en')"
            :model-value="r.en"
            dir="auto"
            :maxlength="MAX_CARD_LABEL * 2"
            autocomplete="off"
            :readonly="locked"
            :invalid="invalid"
            class="input"
            @update:model-value="(v) => setField(i, 'en', v)"
            @blur="emit('touch')"
          />
        </div>
        <div class="cell">
          <label class="cap" :for="fid(i, 'channel')">{{ t('settings.channel_cards.channel') }}</label>
          <InputText
            :id="fid(i, 'channel')"
            :model-value="r.channel"
            dir="ltr"
            inputmode="url"
            maxlength="300"
            :placeholder="channelExample"
            autocomplete="off"
            :spellcheck="false"
            :readonly="locked"
            :invalid="invalid"
            class="input"
            @update:model-value="(v) => setField(i, 'channel', v)"
            @blur="emit('touch')"
          />
        </div>
        <div class="cell">
          <label class="cap" :for="fid(i, 'post')">{{ t('settings.channel_cards.post') }}</label>
          <InputText
            :id="fid(i, 'post')"
            :model-value="r.post"
            dir="ltr"
            inputmode="url"
            maxlength="300"
            :placeholder="postExample"
            autocomplete="off"
            :spellcheck="false"
            :readonly="locked"
            :invalid="invalid"
            :aria-describedby="fid(i, 'post') + '-hint'"
            class="input"
            @update:model-value="(v) => setField(i, 'post', v)"
            @blur="emit('touch')"
          />
          <small :id="fid(i, 'post') + '-hint'" class="app-muted hint">{{ t('settings.channel_cards.post_hint') }}</small>
          <Button
            v-if="!locked"
            :id="fid(i, 'check')"
            type="button"
            icon="pi pi-search"
            :label="t('settings.channel_cards.check')"
            severity="secondary"
            outlined
            size="small"
            class="check-btn"
            :loading="!!r.check?.busy"
            :disabled="!chats[i] || !!r.check?.busy"
            :title="chats[i] ? undefined : t('settings.channel_cards.check_need_post')"
            @click="runCheck(i)"
          />
        </div>
      </div>
      <Message v-if="r.check?.result" :severity="r.check.result.severity" size="small" :closable="false" aria-live="polite">
        {{ r.check.result.text }}
        <div v-if="r.check.result.detail" class="app-ltr detail">{{ r.check.result.detail }}</div>
      </Message>
    </div>
    <div v-if="!locked" class="foot">
      <Button
        :id="rows.length ? undefined : inputId"
        type="button"
        icon="pi pi-plus"
        :label="t('settings.channel_cards.add')"
        severity="secondary"
        outlined
        size="small"
        :disabled="!canAdd"
        @click="add"
      />
      <small class="app-muted">
        {{ rows.length >= MAX_CHANNEL_CARDS ? t('settings.channel_cards.max', { n: num(MAX_CHANNEL_CARDS, lang) }, MAX_CHANNEL_CARDS) : t('settings.channel_cards.order') }}
      </small>
    </div>
  </div>
</template>

<style scoped>
.channel-cards {
  display: grid;
  gap: 0.75rem;
}
.channel-card {
  display: grid;
  gap: 0.5rem;
  min-inline-size: 0;
  padding: 0.5rem 0.9rem 0.9rem;
  border: 1px solid var(--p-content-border-color);
  border-radius: var(--app-radius);
}
.head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.title {
  font-weight: 700;
}
.actions {
  margin-inline-start: auto;
  display: inline-flex;
  gap: 0.25rem;
}
/* two columns (the texts, then the links) where the card is wide enough, one under about 30rem */
.fields {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, max(14.5rem, (100% - 1rem) / 2)), 1fr));
  gap: 0.6rem 1rem;
  align-items: start;
}
.cell {
  display: grid;
  gap: 0.2rem;
  min-inline-size: 0;
}
.cap {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
}
.input {
  inline-size: 100%;
}
.hint {
  font-size: 0.78rem;
  line-height: 1.6;
}
.check-btn {
  justify-self: start;
  margin-block-start: 0.2rem;
}
.detail {
  font-size: 0.85rem;
  margin-block-start: 0.25rem;
  overflow-wrap: anywhere;
}
.foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.6rem;
}
.empty {
  margin: 0;
  font-size: 0.9rem;
}
</style>
