<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Card from 'primevue/card'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import UserLink from '../components/UserLink.vue'
import ReasonDialog from '../components/ReasonDialog.vue'
import TicketStatusTag from '../components/support/TicketStatusTag.vue'
import TicketBubble from '../components/support/TicketBubble.vue'
import TicketComposer from '../components/support/TicketComposer.vue'
import AssigneeSelect from '../components/support/AssigneeSelect.vue'
import { api } from '../api'
import { errorText } from '../errors'
import { useAuth } from '../stores/auth'
import { dateTime, type Lang } from '../format'
import { customerBlocked, duration, loadCounts, pollEvery, poller, requestKeys, waitingSeconds, type RequestKeys } from '../support'
import type { TicketDetail, TicketItem, TicketMessageItem } from '../types'

// The user part of a ticket; bot_blocked if core adds the customer's
// blocked flag (otherwise the last delivery tells).
type Customer = TicketItem['user'] & { bot_blocked?: boolean }

const { t, te, locale } = useI18n()
const lang = computed(() => locale.value as Lang)
const route = useRoute()
const router = useRouter()
const toast = useToast()
const confirm = useConfirm()
const auth = useAuth()
const uid = useId()
const id = computed(() => route.params.id as string)

const detail = ref<TicketDetail | null>(null)
const loadError = ref('')
const ticket = computed(() => detail.value?.ticket ?? null)
const customer = computed(() => (ticket.value?.user ?? null) as Customer | null)
const messages = computed(() => detail.value?.messages ?? [])
const closed = computed(() => ticket.value?.status === 'closed')
const blocked = computed(() => customerBlocked(messages.value, customer.value?.bot_blocked))
const now = ref(Math.floor(Date.now() / 1000))

// The thread scrolls by itself; it follows new messages while the reader is
// at the bottom (or just sent one), and stays put while they read back.
const threadEl = ref<HTMLElement | null>(null)
function atBottom(): boolean {
  const el = threadEl.value
  return !el || el.scrollHeight - el.scrollTop - el.clientHeight < 80
}
async function toBottom() {
  await nextTick()
  const el = threadEl.value
  if (el) el.scrollTop = el.scrollHeight
}

let seq = 0
/** load reads the ticket; quiet (polling) keeps what is shown on failure. */
async function load(quiet = false) {
  if (route.name !== 'ticket') return
  const mine = ++seq
  const ticketId = id.value
  if (!quiet) loadError.value = ''
  try {
    const res = await api<TicketDetail>('GET', `/tickets/${ticketId}`)
    if (mine !== seq || ticketId !== id.value) return
    const follow = !detail.value || atBottom() || res.messages.length < messages.value.length
    const grew = !detail.value || res.messages.length !== messages.value.length
    detail.value = res
    now.value = Math.floor(Date.now() / 1000)
    loadError.value = ''
    if (follow && grew) await toBottom()
  } catch (e) {
    if (mine !== seq || quiet) return
    loadError.value = errorText(e, t).text
  }
}

const poll = poller(() => load(true), () => pollEvery.ticket)
// Left during the first load: no poller for a page that is gone.
let alive = true
onMounted(async () => {
  await load()
  if (alive) poll.start(false)
})
onBeforeUnmount(() => {
  alive = false
  poll.stop()
})
watch(id, async (v, old) => {
  // Another ticket in the same page (leaving for a user page changes :id too).
  if (route.name !== 'ticket' || !v || v === old) return
  detail.value = null
  await load()
})

// Replies being written, by ticket: one outlives the ticket being closed
// meanwhile (the box gives way to Reopen) and is back after Reopen. Its
// request keys stay with it, so sending it again then is the same send.
const drafts = ref<Record<string, string>>({})
const keys: Record<string, RequestKeys> = {}
// done() after a send also drops the page's copy of the text: the box may be
// gone by the time core answers, and its own clearing goes nowhere then.
function keysOf(ticketId: string): RequestKeys {
  if (!keys[ticketId]) {
    const k = requestKeys()
    keys[ticketId] = {
      key: (sig) => k.key(sig),
      done: () => {
        k.done()
        drafts.value[ticketId] = ''
      },
    }
  }
  return keys[ticketId]
}
const draftKept = computed(() => !!ticket.value && !!drafts.value[ticket.value.id]?.trim())
// Leaving with an unsent reply asks first.
onBeforeRouteLeave(() => {
  if (!Object.values(drafts.value).some((d) => d.trim())) return true
  return new Promise<boolean>((resolve) => {
    confirm.require({
      header: t('support.discard_title'),
      message: t('support.discard_message'),
      acceptProps: { label: t('support.discard'), severity: 'danger' },
      rejectProps: { label: t('support.keep_writing'), severity: 'secondary', outlined: true },
      accept: () => resolve(true),
      reject: () => resolve(false),
      onHide: () => resolve(false),
    })
  })
})

// A reply or note just sent shows at once (its delivery fills in on the
// next refresh); a refresh already under way is older, so it is dropped.
async function onSent(res: { ticket: TicketItem; message: TicketMessageItem }) {
  seq++
  if (detail.value) {
    const list = detail.value.messages.some((m) => m.id === res.message.id) ? detail.value.messages : [...detail.value.messages, res.message]
    detail.value = { ...detail.value, ticket: res.ticket, messages: list }
    await toBottom()
  }
  void loadCounts()
}
function onAssigned(tk: TicketItem) {
  seq++
  if (detail.value) detail.value = { ...detail.value, ticket: tk }
  void loadCounts()
}

// Close (with an optional note for the audit log) or reopen.
const closeOpen = ref(false)
const busy = ref(false)
const actionError = ref('')
const actionDetail = ref('')
function openClose() {
  actionError.value = ''
  actionDetail.value = ''
  closeOpen.value = true
}
async function setStatus(status: 'closed' | 'open', reason = '') {
  busy.value = true
  actionError.value = ''
  actionDetail.value = ''
  try {
    const res = await api<{ ticket: TicketItem }>('POST', `/tickets/${id.value}/status`, { status, reason })
    seq++
    if (detail.value) detail.value = { ...detail.value, ticket: res.ticket }
    closeOpen.value = false
    toast.add({ severity: 'success', summary: status === 'closed' ? t('support.closed_done') : t('support.reopened_done'), life: 3000 })
    void loadCounts()
  } catch (e) {
    const err = errorText(e, t)
    if (status === 'closed') {
      actionError.value = err.text
      actionDetail.value = err.detail
    } else {
      toast.add({ severity: 'error', summary: err.text, detail: err.detail || undefined, life: 8000 })
    }
  } finally {
    busy.value = false
  }
}

const category = computed(() => {
  const c = ticket.value?.category ?? ''
  return te('support.category.' + c) ? t('support.category.' + c) : c
})
const closeReason = computed(() => {
  const r = ticket.value?.close_reason ?? ''
  return r && te('support.close_reason.' + r) ? t('support.close_reason.' + r) : r
})
const waiting = computed(() => (ticket.value ? waitingSeconds(ticket.value, now.value) : null))
const languageName = (l: string) => ({ fa: 'فارسی', en: 'English' })[l] ?? (l || '—')
</script>

<template>
  <div class="app-page-head">
    <h1 class="app-page-title">
      <Button icon="pi pi-arrow-left" class="app-flip back" text rounded :aria-label="t('common.back')" @click="router.push({ name: 'support' })" />
      <template v-if="ticket">
        <span class="app-ltr">#{{ ticket.number }}</span>
        <TicketStatusTag :status="ticket.status" />
      </template>
      <template v-else>{{ t('support.ticket') }}</template>
    </h1>
    <div v-if="ticket" class="app-actions">
      <RouterLink v-if="auth.can('audit.read')" :to="{ name: 'audit', query: { entity_id: ticket.id } }" class="p-button p-component p-button-text history">
        <i class="pi pi-history" aria-hidden="true" />
        <span>{{ t('audit.history') }}</span>
      </RouterLink>
      <Button v-if="closed" :label="t('support.reopen')" icon="pi pi-replay" outlined :loading="busy" @click="setStatus('open')" />
      <Button v-else :label="t('support.close')" icon="pi pi-check-circle" severity="secondary" outlined @click="openClose" />
    </div>
  </div>
  <Message v-if="loadError" severity="error" :closable="false">
    {{ loadError }}
    <Button :label="t('app.retry')" text size="small" @click="load()" />
  </Message>

  <div class="layout">
    <Card class="side">
      <template #content>
        <dl v-if="ticket && customer" class="app-facts">
          <dt>{{ t('common.customer') }}</dt>
          <dd>
            <UserLink :user="customer" :link="auth.can('users.read')" />
            <div v-if="customer.status === 'banned' || blocked" class="tags">
              <Tag v-if="customer.status === 'banned'" :value="t('support.banned')" severity="danger" />
              <Tag v-if="blocked" :value="t('support.blocked_bot')" severity="warn" />
            </div>
          </dd>
          <dt>{{ t('user.language') }}</dt>
          <dd>{{ languageName(customer.language) }}</dd>
          <dt>{{ t('support.category_label') }}</dt>
          <dd>{{ category }}</dd>
          <dt :id="uid + '-assignee'">{{ t('support.assignee') }}</dt>
          <dd>
            <AssigneeSelect
              :labelled-by="uid + '-assignee'"
              :ticket-id="ticket.id"
              :current="ticket.assignee"
              :can-manage="detail?.can_manage ?? false"
              @changed="onAssigned"
            />
          </dd>
          <dt>{{ t('support.opened') }}</dt>
          <dd>{{ dateTime(ticket.created_at, lang) }}</dd>
          <template v-if="waiting !== null">
            <dt>{{ t('support.waiting_label') }}</dt>
            <dd class="wait">{{ duration(waiting, lang) }}</dd>
          </template>
          <template v-if="closed && ticket.closed_at">
            <dt>{{ t('support.closed_at') }}</dt>
            <dd>
              {{ dateTime(ticket.closed_at, lang) }}
              <div v-if="closeReason" class="app-muted small">{{ closeReason }}</div>
            </dd>
          </template>
        </dl>
        <Skeleton v-else-if="!loadError" height="10rem" />
        <RouterLink
          v-if="customer && auth.can('users.read')"
          :to="{ name: 'user', params: { id: customer.id }, query: { tab: 'tickets' } }"
          class="others"
        >
          <i class="pi pi-list" aria-hidden="true" />
          {{ t('support.other_tickets') }}
        </RouterLink>
      </template>
    </Card>

    <Card class="main">
      <template #content>
        <bdi v-if="ticket?.subject" class="subject" dir="auto">{{ ticket.subject }}</bdi>
        <div ref="threadEl" class="thread" role="log" :aria-label="t('support.thread')" aria-live="polite">
          <template v-if="detail">
            <TicketBubble v-for="m in messages" :key="m.id" :message="m" :ticket-id="detail.ticket.id" :customer-lang="customer?.language ?? ''" />
            <p v-if="!messages.length" class="app-muted empty">{{ t('support.no_messages') }}</p>
          </template>
          <template v-else-if="!loadError">
            <Skeleton height="3.5rem" width="60%" />
            <Skeleton height="3.5rem" width="50%" class="end" />
          </template>
        </div>

        <Message v-if="blocked && !closed" severity="warn" :closable="false" class="note">{{ t('support.blocked_hint') }}</Message>
        <div v-if="ticket && closed" class="closed-box">
          <span class="app-muted">{{ draftKept ? t('support.draft_kept') : t('support.closed_hint') }}</span>
          <Button :label="t('support.reopen')" icon="pi pi-replay" size="small" outlined :loading="busy" @click="setStatus('open')" />
        </div>
        <TicketComposer
          v-else-if="ticket"
          :key="ticket.id"
          v-model:draft="drafts[ticket.id]"
          :keys="keysOf(ticket.id)"
          :ticket-id="ticket.id"
          :customer-lang="customer?.language ?? ''"
          class="composer"
          @sent="onSent"
        />
      </template>
    </Card>
  </div>

  <ReasonDialog
    v-model:visible="closeOpen"
    :title="t('support.close')"
    :message="t('support.close_intro')"
    :confirm-label="t('support.close')"
    reason-optional
    :busy="busy"
    :error="actionError"
    :detail="actionDetail"
    @confirm="(reason) => setStatus('closed', reason)"
  />
</template>

<style scoped>
.app-page-title {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.back {
  margin-inline-end: -0.25rem;
}
.history {
  text-decoration: none;
  gap: 0.5rem;
}
.layout {
  display: grid;
  gap: var(--app-gap);
  grid-template-columns: minmax(0, 1fr);
  align-items: start;
}
@media (min-width: 1000px) {
  .layout {
    grid-template-columns: minmax(0, 1fr) 19rem;
  }
  .side {
    grid-column: 2;
    grid-row: 1;
    position: sticky;
    inset-block-start: 4.5rem;
  }
  .main {
    grid-column: 1;
    grid-row: 1;
  }
}
.tags {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
  margin-block-start: 0.35rem;
}
.small {
  font-size: 0.8rem;
}
.wait {
  color: var(--p-orange-500);
  font-weight: 500;
}
.others {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  margin-block-start: 1rem;
  color: var(--p-primary-color);
  text-decoration: none;
  font-size: 0.9rem;
}
.others:hover {
  text-decoration: underline;
}
.subject {
  display: block;
  font-weight: 700;
  margin-block-end: 0.75rem;
  overflow-wrap: anywhere;
}
.thread {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  max-block-size: min(62vh, 44rem);
  overflow-y: auto;
  padding: 0.25rem;
  margin-inline: -0.25rem;
  overscroll-behavior: contain;
}
.thread .end {
  align-self: flex-end;
}
.empty {
  text-align: center;
  padding-block: 1.5rem;
}
.note {
  margin-block-start: 0.75rem;
}
.closed-box {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 0.6rem;
  margin-block-start: 1rem;
  padding: 0.75rem 1rem;
  border: 1px dashed var(--p-content-border-color);
  border-radius: 0.6rem;
}
.composer {
  margin-block-start: 1rem;
  padding-block-start: 1rem;
  border-block-start: 1px solid var(--p-content-border-color);
}
</style>
