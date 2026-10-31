// Helpers for the messages to resellers and the weekly settlement reminder (the Resellers page, Messages tab): the placeholders, what the
// composer sends, and the reminder's settings. Pure functions (no API, no Vue).
import { labelProblem, urlProblem, type AudienceLang } from './broadcasts'
import type { Lang } from './format'
import type { LangPair } from './types'

/** The values a message to resellers may use, as {name}; core fills them in for each reseller. */
export const PLACEHOLDERS = ['name', 'debt', 'balance', 'credit_limit', 'tier', 'customers'] as const
export type Placeholder = (typeof PLACEHOLDERS)[number]

/** unknownPlaceholders lists the words in braces that are not placeholders, as typed ({dept}), each once. */
export function unknownPlaceholders(text: string): string[] {
  const out: string[] = []
  for (const m of text.matchAll(/\{([A-Za-z0-9_]+)\}/g)) {
    if (!(PLACEHOLDERS as readonly string[]).includes(m[1]) && !out.includes(m[0])) out.push(m[0])
  }
  return out
}

/** insertPlaceholder puts {name} into text at the caret (at the end when there is none), with a space before it when it would touch a word. */
export function insertPlaceholder(text: string, name: Placeholder, at?: number): { text: string; caret: number } {
  const pos = at === undefined || at < 0 || at > text.length ? text.length : at
  const before = text.slice(0, pos)
  const gap = before && !/\s$/.test(before) ? ' ' : ''
  const piece = gap + '{' + name + '}'
  return { text: before + piece + text.slice(pos), caret: pos + piece.length }
}

/** Who of the resellers a message goes to. */
export type Target = 'all' | 'debt' | 'tier'

/** The composer's state. */
export interface ResellerDraft {
  target: Target
  tierId: string
  language: AudienceLang
  texts: LangPair
  button: { on: boolean; url: string; labels: LangPair }
  settle: boolean
}

export function blankResellerDraft(): ResellerDraft {
  return { target: 'debt', tierId: '', language: '', texts: { fa: '', en: '' }, button: { on: false, url: '', labels: { fa: '', en: '' } }, settle: true }
}

/** neededLangs are the languages the audience needs a text in. */
export function draftLangs(d: ResellerDraft): Lang[] {
  return d.language === '' ? ['fa', 'en'] : [d.language]
}

/** audienceBody is what POST /reseller-messages/audience (and the rest) take as the audience. */
export function audienceBody(d: ResellerDraft): { debt_only: boolean; tier_id: string; language: AudienceLang } {
  return { debt_only: d.target === 'debt', tier_id: d.target === 'tier' ? d.tierId : '', language: d.language }
}

/** messageBody is what POST /reseller-messages takes: only the texts the audience needs, and no link button when it is off. */
export function messageBody(d: ResellerDraft) {
  const need = draftLangs(d)
  const pick = (p: LangPair): LangPair => ({ fa: need.includes('fa') ? p.fa.trim() : '', en: need.includes('en') ? p.en.trim() : '' })
  return {
    ...audienceBody(d),
    texts: pick(d.texts),
    button: d.button.on ? { url: d.button.url.trim(), labels: pick(d.button.labels) } : null,
    settle_button: d.settle,
  }
}

/** draftProblems lists what keeps the message from being sent, as the fields core would name. */
export function resellerDraftProblems(d: ResellerDraft): string[] {
  const out: string[] = []
  if (d.target === 'tier' && !d.tierId) out.push('tier_id')
  for (const l of draftLangs(d)) if (!d.texts[l].trim() || unknownPlaceholders(d.texts[l]).length) out.push('texts.' + l)
  if (d.button.on) {
    if (urlProblem(d.button.url)) out.push('button.url')
    for (const l of draftLangs(d)) if (labelProblem(d.button.labels[l])) out.push('button.labels.' + l)
  }
  return out
}

/** The weekly reminder as GET /reseller-messages/reminder answers it. */
export interface ReminderReply {
  enabled: boolean
  weekday: string
  time: string
  debt_only: boolean
  settle_button: boolean
  texts: LangPair
  custom: Record<Lang, boolean>
  defaults: LangPair
  weekdays: string[]
  zone: string
  next_due_at: number | null
  last: null | {
    day: string
    zone: string
    due_at: number
    queued_at: number
    broadcast: null | {
      id: string
      status: string
      started_at: number | null
      finished_at: number | null
      counts: { total: number; pending: number; sent: number; blocked: number; failed: number; unknown: number }
    }
  }
}

/** The reminder's form. */
export interface ReminderForm {
  enabled: boolean
  weekday: string
  time: string
  debtOnly: boolean
  settle: boolean
  texts: LangPair
}

export function reminderForm(r: ReminderReply): ReminderForm {
  return { enabled: r.enabled, weekday: r.weekday, time: r.time, debtOnly: r.debt_only, settle: r.settle_button, texts: { fa: r.texts.fa, en: r.texts.en } }
}

/** The settings the reminder is kept in. */
export const REMINDER_KEYS = {
  enabled: 'resellers.reminder.enabled',
  weekday: 'resellers.reminder.weekday',
  time: 'resellers.reminder.time',
  debtOnly: 'resellers.reminder.debt_only',
  settle: 'resellers.reminder.settle_button',
  text: (l: Lang) => 'resellers.reminder.text.' + l,
}

/** timeOf reads a time of day typed as H:MM or HH:MM (Persian digits too) as HH:MM; null when it is not one. */
export function timeOf(s: string): string | null {
  const v = s.trim().replace(/[۰-۹]/g, (d) => String('۰۱۲۳۴۵۶۷۸۹'.indexOf(d)))
  const m = /^([01]?\d|2[0-3]):([0-5]\d)$/.exec(v)
  return m ? `${m[1].padStart(2, '0')}:${m[2]}` : null
}

/** reminderChanges is what PUT /reseller-messages/reminder takes: the settings that differ from what was loaded. */
export function reminderChanges(before: ReminderForm, after: ReminderForm): Record<string, string> {
  const out: Record<string, string> = {}
  if (before.enabled !== after.enabled) out[REMINDER_KEYS.enabled] = String(after.enabled)
  if (before.weekday !== after.weekday) out[REMINDER_KEYS.weekday] = after.weekday
  const time = timeOf(after.time) ?? after.time
  if (before.time !== time) out[REMINDER_KEYS.time] = time
  if (before.debtOnly !== after.debtOnly) out[REMINDER_KEYS.debtOnly] = String(after.debtOnly)
  if (before.settle !== after.settle) out[REMINDER_KEYS.settle] = String(after.settle)
  for (const l of ['fa', 'en'] as Lang[]) if (before.texts[l].trim() !== after.texts[l].trim()) out[REMINDER_KEYS.text(l)] = after.texts[l].trim()
  return out
}

/** reminderProblems lists the reminder's fields that cannot be saved. */
export function reminderProblems(f: ReminderForm): string[] {
  const out: string[] = []
  if (!timeOf(f.time)) out.push('time')
  for (const l of ['fa', 'en'] as Lang[]) if (!f.texts[l].trim() || unknownPlaceholders(f.texts[l]).length) out.push('texts.' + l)
  return out
}
