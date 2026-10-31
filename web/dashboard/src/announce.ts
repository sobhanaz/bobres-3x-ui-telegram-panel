// Helpers for new product announcements (the Announce dialog of the plan, supplier product and license pages): the placeholders, the
// draft and what is sent, and how core's refusals read. Pure functions (no API, no Vue).
import type { ContentLang } from './format'
import type { UserBrief } from './types'

/** The kinds of product that can be announced. */
export type AnnounceKind = 'plan' | 'supplier' | 'license'

/** The values a card may use, as {name}; core fills them in with the product's values when it goes out. */
export const ANNOUNCE_PLACEHOLDERS = ['name', 'kind', 'price', 'days', 'traffic', 'stock', 'store'] as const
export type AnnouncePlaceholder = (typeof ANNOUNCE_PLACEHOLDERS)[number]

/** The languages a card is written in (every customer gets their own). */
export const ANNOUNCE_LANGS: readonly ContentLang[] = ['fa', 'en']

/** A product as GET /announcements/draft answers it. */
export interface AnnounceProduct {
  kind: AnnounceKind
  id: string
  names: Record<string, string>
  kinds: Record<string, string>
  price: number
  currency: string
  scale: number
  toman: number
  days: number | null
  months: number
  traffic_bytes: number | null
  stock: number | null
  start_param: string
  buy_callback: string
}

/** One announcement, as core lists it. */
export interface AnnouncementItem {
  id: string
  product_kind: AnnounceKind
  product_id: string
  product_name: string
  texts: Record<string, string>
  to_channel: boolean
  to_private: boolean
  channel_langs: 'default' | 'both'
  forced: boolean
  status: 'queued' | 'sent' | 'cancelled'
  due_at: number
  sent_at: number | null
  broadcast_id: string
  channel_chat: string
  channel_status: 'none' | 'pending' | 'sent' | 'failed'
  channel_message_id: number
  channel_error: string
  created_by: UserBrief | null
  created_at: number
}

/** What the dialog starts from. */
export interface AnnounceDraftReply {
  product: AnnounceProduct
  texts: Record<string, string>
  placeholders: string[]
  buy_labels: Record<string, string>
  settings: {
    enabled: boolean
    channel: string
    channel_langs: 'default' | 'both'
    default_language: ContentLang
    daily_max: number
    quiet_start: string
    quiet_end: string
  }
  audience: { total: number; languages: Record<string, number> }
  recent: AnnouncementItem[]
}

/** The dialog's state. */
export interface AnnounceDraft {
  texts: Record<ContentLang, string>
  channel: boolean
  private: boolean
}

/** draftFrom starts the dialog from core's prefilled cards: the channel when one is set, private messages always. */
export function draftFrom(r: AnnounceDraftReply): AnnounceDraft {
  return { texts: { fa: r.texts.fa ?? '', en: r.texts.en ?? '' }, channel: r.settings.channel !== '', private: true }
}

/** unknownPlaceholders lists the words in braces that are not placeholders, as typed ({prise}), each once. */
export function unknownPlaceholders(text: string): string[] {
  const out: string[] = []
  for (const m of text.matchAll(/\{([A-Za-z0-9_]+)\}/g)) {
    if (!(ANNOUNCE_PLACEHOLDERS as readonly string[]).includes(m[1]) && !out.includes(m[0])) out.push(m[0])
  }
  return out
}

/** announceProblems lists what keeps the announcement from being sent, as the fields core would name. */
export function announceProblems(d: AnnounceDraft): string[] {
  const out: string[] = []
  if (!d.channel && !d.private) out.push('targets')
  for (const l of ANNOUNCE_LANGS) if (!d.texts[l].trim() || unknownPlaceholders(d.texts[l]).length) out.push('texts.' + l)
  return out
}

/** sendBody is what POST /announcements takes. */
export function sendBody(kind: AnnounceKind, id: string, d: AnnounceDraft, force = false) {
  return { kind, id, texts: { fa: d.texts.fa.trim(), en: d.texts.en.trim() }, channel: d.channel, private: d.private, force }
}

/** A refusal of POST /announcements that the dialog answers in its own words. */
export type SendRefusal =
  | { kind: 'repeat'; lastAt: number }
  | { kind: 'limit'; max: number; day: string }
  | { kind: 'channel'; problem: string; chat: string; detail: string }

/** refusalOf reads a 409 answer of POST /announcements (null for any other error). */
export function refusalOf(status: number, data: Record<string, unknown>): SendRefusal | null {
  if (status !== 409) return null
  const str = (v: unknown) => (typeof v === 'string' ? v : '')
  switch (data.error) {
    case 'repeat':
      return { kind: 'repeat', lastAt: typeof data.last_at === 'number' ? data.last_at : 0 }
    case 'limit':
      return { kind: 'limit', max: typeof data.max === 'number' ? data.max : 0, day: str(data.day) }
    case 'channel':
      return { kind: 'channel', problem: str(data.problem), chat: str(data.chat), detail: str(data.detail) }
  }
  return null
}

/** channelProblemKey is the text key that explains why the bot cannot post in the channel. */
export function channelProblemKey(problem: string): string {
  switch (problem) {
    case 'no_channel':
    case 'not_found':
    case 'not_admin':
    case 'not_channel':
    case 'cannot_post':
      return 'announce.channel_problem.' + problem
  }
  return 'announce.channel_problem.error'
}

/** statusSeverity is the colour of an announcement's state. */
export function statusSeverity(a: AnnouncementItem): 'success' | 'info' | 'warn' | 'secondary' | 'danger' {
  if (a.status === 'cancelled') return 'secondary'
  if (a.status === 'queued') return 'info'
  if (a.to_channel && a.channel_status === 'failed') return 'warn'
  return 'success'
}
