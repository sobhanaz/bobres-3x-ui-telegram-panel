// Support helpers: the inbox tabs and search, waiting times, the reply
// counter, the saved-reply language fallback, attachment links, the
// assignee choices, request keys for replies, and polling (the open thread
// and the menu's waiting count). Ticket bodies are data: nothing here sends
// them through vue-i18n or turns them into HTML.
import { ref } from 'vue'
import { api, newKey } from './api'
import { latinDigits, type Lang } from './format'
import type { CannedReply, TicketCounts, TicketItem, TicketMessageItem, UserBrief } from './types'

/** A staff reply's limit in UTF-16 units (Telegram counts the same way). */
export const REPLY_MAX = 3500
/** A saved reply's title limit. */
export const CANNED_TITLE_MAX = 64

/** A staff member who may be given tickets (GET /tickets/assignees). */
export interface Assignee extends UserBrief {
  role: string
}

// ---------------------------------------------------------------------------
// Inbox

export type InboxTab = 'open' | 'answered' | 'closed' | 'all'
export const INBOX_TABS: readonly InboxTab[] = ['open', 'answered', 'closed', 'all']
export const CATEGORIES = ['payment', 'service', 'other'] as const
export const ASSIGNED = ['me', 'none'] as const

/** inboxTab reads ?status= (anything unknown is the waiting tab). */
export function inboxTab(v: unknown): InboxTab {
  return INBOX_TABS.includes(v as InboxTab) ? (v as InboxTab) : 'open'
}

/** statusParam is the tab's ?status= for GET /tickets ('' = every status). */
export function statusParam(tab: InboxTab): string {
  return tab === 'all' ? '' : tab
}

/** ticketSearch tidies the search box: a ticket number or Telegram id typed
 * with Persian digits (or a full-width #) becomes Latin; words stay as typed,
 * since customers write Persian digits in their messages too. */
export function ticketSearch(s: string): string {
  const v = s.trim()
  if (/^[#＃]?\s*[0-9۰-۹٠-٩]+$/.test(v)) return latinDigits(v).replace(/^＃/, '#').replace(/\s+/g, '')
  return v
}

/** waitingSeconds: how long a waiting ticket's customer has waited since
 * their last message; null when the ticket is not waiting. */
export function waitingSeconds(t: Pick<TicketItem, 'status' | 'last_customer_at'>, now: number): number | null {
  if (t.status !== 'open' || t.last_customer_at === null) return null
  return Math.max(0, now - t.last_customer_at)
}

/** duration writes a span of time in its largest sensible unit, in the
 * dashboard's language ("2 hours", "۲ ساعت"); under a minute counts as one. */
export function duration(sec: number, lang: Lang): string {
  const locale = lang === 'fa' ? 'fa-IR' : 'en-US'
  const fmt = (n: number, unit: 'minute' | 'hour' | 'day') =>
    new Intl.NumberFormat(locale, { style: 'unit', unit, unitDisplay: 'long', maximumFractionDigits: 0 }).format(n)
  const s = Math.max(0, sec)
  if (s < 3600) return fmt(Math.max(1, Math.floor(s / 60)), 'minute')
  if (s < 48 * 3600) return fmt(Math.floor(s / 3600), 'hour')
  return fmt(Math.floor(s / 86_400), 'day')
}

/** A wait of a day or more is late (shown in a warning colour). */
export function isLate(waiting: number | null): boolean {
  return waiting !== null && waiting >= 86_400
}

/** staffName is how a staff member (or customer) is named in a line: @username, else the Telegram id. */
export function staffName(u: Pick<UserBrief, 'username' | 'telegram_id'> | null): string {
  if (!u) return ''
  return u.username ? '@' + u.username : String(u.telegram_id)
}

// ---------------------------------------------------------------------------
// Thread

/** plainLength counts a plain-text reply as core and Telegram do: UTF-16
 * units of the trimmed text, line breaks as one. Unlike branding's
 * textLength it keeps "<…>" and "&amp;" as typed, since a reply is not HTML. */
export function plainLength(s: string): number {
  return s.replace(/\r\n?/g, '\n').trim().length
}

/** replyOk: the text can be sent (not empty, not over the limit). */
export function replyOk(s: string): boolean {
  const n = plainLength(s)
  return n > 0 && n <= REPLY_MAX
}

/** cannedBody is a saved reply in the customer's language, else the other
 * one (a customer whose language is unknown reads Persian first). */
export function cannedBody(c: Pick<CannedReply, 'body'>, lang: string): string {
  const first: Lang = lang === 'en' ? 'en' : 'fa'
  const other: Lang = first === 'fa' ? 'en' : 'fa'
  return (c.body[first] ?? '').trim() ? c.body[first] : (c.body[other] ?? '')
}

/** cannedLangs says which languages a saved reply has. */
export function cannedLangs(c: Pick<CannedReply, 'body'>): Lang[] {
  return (['fa', 'en'] as Lang[]).filter((l) => (c.body[l] ?? '').trim() !== '')
}

type FileInfo = NonNullable<TicketMessageItem['file']>

/** isImageFile: the attachment can be shown as a picture (a photo, or an
 * image sent as a file in a type the browser shows). */
export function isImageFile(f: Pick<FileInfo, 'type' | 'mime'>): boolean {
  return f.type === 'photo' || /^image\/(jpeg|png|webp|gif)$/i.test(f.mime)
}

/** fileHref is where an attachment opens: core's own URL for it when it is a
 * same-origin API path (the session cookie goes with it), else the path
 * built from the ids. Never another site. */
export function fileHref(ticketId: string, m: Pick<TicketMessageItem, 'id' | 'file'>): string {
  const u = m.file?.url ?? ''
  if (/^\/api\/v1\/[^\\]*$/.test(u) && !u.includes('..')) return u
  return `/api/v1/tickets/${encodeURIComponent(ticketId)}/messages/${encodeURIComponent(m.id)}/file`
}

/** fileSize writes a file's size ("12.5 kB", "۱۲٫۵ کیلوبایت"). */
export function fileSize(n: number, lang: Lang): string {
  const locale = lang === 'fa' ? 'fa-IR' : 'en-US'
  const fmt = (v: number, unit: 'byte' | 'kilobyte' | 'megabyte') =>
    new Intl.NumberFormat(locale, {
      style: 'unit',
      unit,
      unitDisplay: lang === 'fa' || unit === 'byte' ? 'long' : 'short',
      maximumFractionDigits: 1,
    }).format(v)
  if (n < 1024) return fmt(Math.max(0, n), 'byte')
  if (n < 1024 * 1024) return fmt(n / 1024, 'kilobyte')
  return fmt(n / (1024 * 1024), 'megabyte')
}

/** customerBlocked: Telegram refused the latest delivered staff reply because
 * the customer blocked the bot (newer replies still sending do not count). */
export function customerBlocked(messages: Pick<TicketMessageItem, 'kind' | 'delivery'>[]): boolean {
  for (let i = messages.length - 1; i >= 0; i--) {
    const d = messages[i].delivery
    if (messages[i].kind !== 'staff' || !d || d.status === 'pending') continue
    return d.status === 'blocked'
  }
  return false
}

/** The icon for a staff reply's delivery state. */
export const DELIVERY_ICON: Record<string, string> = {
  pending: 'pi pi-clock',
  sent: 'pi pi-check',
  blocked: 'pi pi-ban',
  failed: 'pi pi-exclamation-triangle',
}

/** One choice in the assignee picker; id '' is nobody. */
export interface AssigneeChoice {
  id: string
  user: UserBrief | null
  me: boolean
  disabled: boolean
}

/**
 * assigneeChoices lists who the viewer may give the ticket to: nobody and
 * themselves always; anyone else only with canManage (tickets.manage). A
 * current assignee the viewer may not choose stays listed, disabled, so the
 * picker can still show it. The server checks every rule again.
 */
export function assigneeChoices(all: Assignee[], me: UserBrief | null, canManage: boolean, current: UserBrief | null): AssigneeChoice[] {
  const out: AssigneeChoice[] = [{ id: '', user: null, me: false, disabled: false }]
  if (me) out.push({ id: me.id, user: all.find((a) => a.id === me.id) ?? me, me: true, disabled: false })
  const seen = new Set(out.map((c) => c.id))
  if (canManage) {
    for (const a of all) {
      if (seen.has(a.id)) continue
      seen.add(a.id)
      out.push({ id: a.id, user: a, me: false, disabled: false })
    }
  }
  if (current && !seen.has(current.id)) out.push({ id: current.id, user: current, me: false, disabled: !canManage })
  return out
}

/**
 * requestKeys keeps one request key per thing sent: sending the same text
 * the same way again (a retry after an error) reuses the key, so core sends
 * it to the customer once; anything else gets a new key. done() forgets it
 * after a success.
 */
export function requestKeys(make: () => string = newKey) {
  let last: { sig: string; key: string } | null = null
  return {
    key(sig: string): string {
      if (last?.sig !== sig) last = { sig, key: make() }
      return last.key
    },
    done(): void {
      last = null
    },
  }
}

// ---------------------------------------------------------------------------
// Polling

/** How often the open thread and the menu's waiting count refresh (ms). */
export const pollEvery = { ticket: 15_000, counts: 60_000 }

export interface Poller {
  /** start runs fn now (or after one interval with immediate false), then every interval. */
  start(immediate?: boolean): void
  stop(): void
}

/**
 * poller runs fn every ms while started: never two at once, not while the
 * browser tab is hidden (it catches up when the tab shows again), and a
 * failed run just waits for the next one.
 */
export function poller(fn: () => unknown, ms: () => number): Poller {
  let timer: ReturnType<typeof setTimeout> | undefined
  let running = false
  let active = false
  const hidden = () => typeof document !== 'undefined' && document.hidden

  function schedule() {
    clearTimeout(timer)
    timer = active ? setTimeout(tick, ms()) : undefined
  }
  async function tick() {
    timer = undefined
    if (!active) return
    if (hidden() || running) return schedule()
    running = true
    try {
      await fn()
    } catch {
      /* the next run tries again */
    } finally {
      running = false
    }
    if (active && timer === undefined) schedule()
  }
  function onVisibility() {
    if (active && !hidden() && !running) {
      clearTimeout(timer)
      void tick()
    }
  }
  return {
    start(immediate = true) {
      if (active) return
      active = true
      document.addEventListener('visibilitychange', onVisibility)
      if (immediate) void tick()
      else schedule()
    },
    stop() {
      active = false
      clearTimeout(timer)
      timer = undefined
      document.removeEventListener('visibilitychange', onVisibility)
    },
  }
}

/** The ticket counts (GET /tickets/counts), shared by the menu badge and the inbox tabs. */
export const ticketCounts = ref<TicketCounts | null>(null)
let countsSeq = 0

/** loadCounts refreshes the counts; a failure keeps the last ones. */
export async function loadCounts(): Promise<void> {
  const mine = ++countsSeq
  try {
    const c = await api<TicketCounts>('GET', '/tickets/counts')
    if (mine === countsSeq) ticketCounts.value = c
  } catch {
    /* keep what is shown */
  }
}
