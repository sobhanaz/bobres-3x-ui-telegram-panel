<script setup lang="ts">
// Support: write to the store's staff and follow the conversation. It is the
// bot's support (one open conversation per customer, the same inbox for staff);
// answers also arrive in the bot. Photos and files are sent from the bot.
import { computed, onMounted, ref } from 'vue'
import { ApiError } from '../../api'
import { errorKey } from '../../errors'
import { fmtDateTime, fmtNum } from '../../fmt'
import { tr, type Key } from '../../i18n'
import { fail } from '../../store'
import { haptic } from '../../telegram'
import { supportApi, type Overview, type Ticket, type TicketMessage } from './api'

const overview = ref<Overview | null>(null)
const ticket = ref<Ticket | null>(null) // the conversation shown; null: the list
const messages = ref<TicketMessage[]>([])
const text = ref('')
const category = ref('other')
const busy = ref(false)
const error = ref('')

// The label of every category a ticket can have. A customer picks one of the first three when they
// write; a reseller application (the fourth) is filed from the bot's "Become a reseller" button and
// only shows its label here, on the ticket.
const categoryKeys: Record<string, Key> = {
  payment: 'support.cat.payment',
  service: 'support.cat.service',
  other: 'support.cat.other',
  reseller: 'support.cat.reseller',
}
const pickable = ['payment', 'service', 'other']
const statusKeys: Record<Ticket['status'], Key> = { open: 'support.status.open', answered: 'support.status.answered', closed: 'support.status.closed' }
const categoryOf = (c: string): Key => categoryKeys[c] ?? 'support.cat.other'
const statusOf = (s: Ticket['status']): Key => statusKeys[s] ?? 'support.status.open'

const categories = computed(() => (overview.value?.categories ?? []).filter((c) => pickable.includes(c)))
const earlier = computed(() => (overview.value?.tickets ?? []).filter((t) => t.id !== overview.value?.open?.id))
const canWrite = computed(() => !!text.value.trim() && !busy.value)

function oops(e: unknown) {
  if (fail(e)) return
  error.value = e instanceof ApiError && e.code === 'daily_limit' ? tr('support.limit') : tr(errorKey(e))
  haptic('error')
}

async function loadList() {
  error.value = ''
  try {
    overview.value = await supportApi.overview()
  } catch (e) {
    oops(e)
  }
}
onMounted(loadList)

async function show(id: string) {
  error.value = ''
  busy.value = true
  try {
    const r = await supportApi.thread(id)
    ticket.value = r.ticket
    messages.value = r.messages
  } catch (e) {
    oops(e)
  } finally {
    busy.value = false
  }
}

async function toList() {
  ticket.value = null
  messages.value = []
  await loadList()
}

// A message joins the open conversation, or starts one when there is none.
async function send() {
  const body = text.value.trim()
  if (!body || busy.value) return
  error.value = ''
  busy.value = true
  try {
    const r = await supportApi.send(body, category.value)
    text.value = ''
    haptic('success')
    await show(r.ticket.id)
  } catch (e) {
    oops(e)
  } finally {
    busy.value = false
  }
}

async function close() {
  if (!ticket.value) return
  error.value = ''
  busy.value = true
  try {
    await supportApi.close(ticket.value.id)
    await show(ticket.value.id)
  } catch (e) {
    oops(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section>
    <!-- the list -->
    <template v-if="!ticket">
      <h1>{{ tr('support.title') }}</h1>
      <p v-if="error" class="error" role="alert">{{ error }} <button class="link" type="button" @click="loadList">{{ tr('common.retry') }}</button></p>
      <p v-else-if="overview === null" class="muted">{{ tr('common.loading') }}</p>
      <template v-else>
        <section v-if="overview.open" class="card">
          <div class="row between">
            <strong>#{{ fmtNum(overview.open.number) }}</strong>
            <span class="badge">{{ tr(statusOf(overview.open.status)) }}</span>
          </div>
          <p class="muted small category">{{ tr(categoryOf(overview.open.category)) }}</p>
          <p class="subject" dir="auto">{{ overview.open.subject }}</p>
          <button class="btn" type="button" :disabled="busy" @click="show(overview.open.id)">{{ tr('support.open') }}</button>
        </section>
        <form v-else class="card" @submit.prevent="send">
          <h2>{{ tr('support.new') }}</h2>
          <label class="field">
            <span class="muted small">{{ tr('support.category') }}</span>
            <select v-model="category">
              <option v-for="c in categories" :key="c" :value="c">{{ tr(categoryOf(c)) }}</option>
            </select>
          </label>
          <textarea v-model="text" rows="4" maxlength="3000" dir="auto" :aria-label="tr('support.text')" :placeholder="tr('support.text')"></textarea>
          <button class="btn" type="submit" :disabled="!canWrite">{{ tr('support.send') }}</button>
        </form>

        <template v-if="earlier.length">
          <h2>{{ tr('support.recent') }}</h2>
          <ul class="list">
            <li v-for="t in earlier" :key="t.id">
              <button class="card row between" type="button" :disabled="busy" @click="show(t.id)">
                <span>
                  <strong>#{{ fmtNum(t.number) }}</strong>
                  <span class="muted small block category">{{ tr(categoryOf(t.category)) }}</span>
                  <span class="muted small block subject" dir="auto">{{ t.subject }}</span>
                </span>
                <span class="badge">{{ tr(statusOf(t.status)) }}</span>
              </button>
            </li>
          </ul>
        </template>
        <p v-if="!overview.tickets.length" class="muted">{{ tr('support.empty') }}</p>
      </template>
    </template>

    <!-- one conversation -->
    <template v-else>
      <button class="link" type="button" @click="toList">{{ tr('support.all') }}</button>
      <div class="row between">
        <h1>#{{ fmtNum(ticket.number) }}</h1>
        <span class="badge">{{ tr(statusOf(ticket.status)) }}</span>
      </div>
      <p class="muted small category">{{ tr(categoryOf(ticket.category)) }}</p>
      <p class="muted subject" dir="auto">{{ ticket.subject }}</p>

      <ul class="list">
        <li v-for="m in messages" :key="m.id" class="bubble" :class="m.kind">
          <span class="muted small">{{ m.kind === 'staff' ? tr('support.staff') : tr('support.you') }} · {{ fmtDateTime(m.at) }}</span>
          <p v-if="m.body" class="body" dir="auto">{{ m.body }}</p>
          <p v-if="m.has_file" class="muted small">{{ tr('support.file') }}</p>
        </li>
      </ul>

      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <template v-if="ticket.status !== 'closed'">
        <p v-if="ticket.status === 'open'" class="muted small">{{ tr('support.waiting') }}</p>
        <form class="compose" @submit.prevent="send">
          <textarea v-model="text" rows="3" maxlength="3000" dir="auto" :aria-label="tr('support.text')" :placeholder="tr('support.text')"></textarea>
          <button class="btn" type="submit" :disabled="!canWrite">{{ tr('support.send') }}</button>
        </form>
        <div class="row wrap">
          <button class="btn secondary" type="button" :disabled="busy" @click="show(ticket.id)">{{ tr('support.refresh') }}</button>
          <button class="btn secondary" type="button" :disabled="busy" @click="close">{{ tr('support.close') }}</button>
        </div>
      </template>
      <template v-else>
        <p class="muted">{{ tr('support.closed') }}</p>
        <button class="btn" type="button" @click="toList">{{ tr('support.new') }}</button>
      </template>
    </template>
  </section>
</template>

<style scoped>
.subject { overflow-wrap: anywhere; }
.bubble { display: grid; gap: 0.2rem; padding: 0.6rem 0.75rem; border-radius: 0.75rem; background: var(--card); max-width: 90%; }
.bubble.customer { justify-self: end; box-shadow: inset 0 0 0 1px var(--accent); }
.bubble.staff { justify-self: start; }
.body { white-space: pre-wrap; overflow-wrap: anywhere; }
.compose { display: grid; gap: 0.5rem; }
textarea, select { width: 100%; padding: 0.6rem; border-radius: 0.5rem; border: 1px solid var(--muted); background: var(--bg); color: var(--fg); font: inherit; }
textarea { resize: vertical; min-height: 5rem; }
</style>
