// Helpers for the broadcast pages: the audience, the draft and what it sends,
// what each status allows, progress numbers, the schedule input, and a small
// poller. Pure functions apart from the poller's timers (no API, no Vue).
import { isHttpsUrl, textLength } from './branding'
import { contentLang, numberLocale, type ContentLang, type Lang } from './format'
import type { BroadcastItem, LangPair, TextProblem } from './types'

export type BroadcastStatus = BroadcastItem['status']
/** The customer groups the composer offers (messages to resellers are written on the Resellers page, product announcements from the product's page). */
export type Segment = Exclude<BroadcastItem['segment'], 'resellers' | 'announce'>
export type AudienceLang = BroadcastItem['language']

export const SEGMENTS: readonly Segment[] = ['all', 'active', 'expired', 'never_bought']
export const AUDIENCE_LANGS: readonly AudienceLang[] = ['', 'fa', 'en']
export const STATUSES: readonly BroadcastStatus[] = ['draft', 'scheduled', 'running', 'paused', 'done', 'cancelled']
/** The recipient lists the detail page can show. */
export const PROBLEM_STATUSES = ['failed', 'blocked', 'unknown'] as const

/** Telegram's limit for one message, in UTF-16 units after entities. */
export const MAX_TEXT = 4096
/** A button label's limit, in UTF-16 units. */
export const MAX_LABEL = 64
/** A button link's limit, in bytes. */
export const MAX_URL = 1024
/** How far ahead a broadcast may be scheduled. */
export const MAX_AHEAD = 30 * 86_400

/** Delays in milliseconds; tests may shorten them (the app's own copy).
 * clock: how often the list looks again at what falls due. */
export const timing = { poll: 2000, listPoll: 5000, check: 400, audience: 400, clock: 30_000 }

/** neededLangs are the languages an audience needs a text in. */
export function neededLangs(language: AudienceLang): ContentLang[] {
  return language === '' ? ['fa', 'en'] : [language]
}

/** The editable part of a broadcast. */
export interface Draft {
  segment: Segment
  language: AudienceLang
  texts: LangPair
  button: { on: boolean; url: string; labels: LangPair }
}

export function blankDraft(): Draft {
  return { segment: 'all', language: '', texts: { fa: '', en: '' }, button: { on: false, url: '', labels: { fa: '', en: '' } } }
}

export function draftOf(b: BroadcastItem): Draft {
  return {
    segment: b.segment === 'resellers' || b.segment === 'announce' ? 'all' : b.segment,
    language: b.language,
    texts: { fa: b.texts.fa ?? '', en: b.texts.en ?? '' },
    button: b.button
      ? { on: true, url: b.button.url, labels: { fa: b.button.labels.fa ?? '', en: b.button.labels.en ?? '' } }
      : { on: false, url: '', labels: { fa: '', en: '' } },
  }
}

export interface DraftBody {
  segment: Segment
  language: AudienceLang
  texts: LangPair
  button: { url: string; labels: LangPair } | null
}

/** bodyOf is what POST and PUT /broadcasts take: only the languages the
 * audience needs (the others go empty), and no button when it is off. */
export function bodyOf(d: Draft): DraftBody {
  const need = neededLangs(d.language)
  const pick = (p: LangPair): LangPair => ({ fa: need.includes('fa') ? p.fa.trim() : '', en: need.includes('en') ? p.en.trim() : '' })
  return {
    segment: d.segment,
    language: d.language,
    texts: pick(d.texts),
    button: d.button.on ? { url: d.button.url.trim(), labels: pick(d.button.labels) } : null,
  }
}

/** sameDraft: nothing a save would send has changed. */
export function sameDraft(a: Draft, b: Draft): boolean {
  return JSON.stringify(bodyOf(a)) === JSON.stringify(bodyOf(b))
}

/** utf16Length counts a plain text (a label) as Telegram does: trimmed, UTF-16 units. */
export function utf16Length(s: string): number {
  return s.replace(/\r\n?/g, '\n').trim().length
}

export type LabelProblem = '' | 'empty' | 'chars' | 'too_long'

/** labelProblem checks a button label: required, at most 64 units, no < > &. */
export function labelProblem(s: string): LabelProblem {
  const v = s.trim()
  if (!v) return 'empty'
  if (/[<>&]/.test(v)) return 'chars'
  if (utf16Length(v) > MAX_LABEL) return 'too_long'
  return ''
}

export type UrlProblem = '' | 'empty' | 'bad' | 'too_long'

/** urlProblem checks the button link: a full https link of at most 1024 bytes. */
export function urlProblem(s: string): UrlProblem {
  const v = s.trim()
  if (!v) return 'empty'
  if (new TextEncoder().encode(v).length > MAX_URL) return 'too_long'
  return isHttpsUrl(v) ? '' : 'bad'
}

/** A text's live check as the composer keeps it. */
export interface TextState {
  checking: boolean
  /** core's last answer for the current text: null = not checked (yet) */
  ok: boolean | null
}

/** textReady: a needed text is written, within the limit and not refused. */
export function textReady(text: string, s: TextState | undefined): boolean {
  if (!text.trim() || textLength(text) > MAX_TEXT) return false
  return !s || s.checking || s.ok !== false
}

/** draftProblems lists what keeps a draft from being saved or sent, as the
 * fields core would name ("texts.fa", "button.url", "button.labels.en"). */
export function draftProblems(d: Draft, checks: Partial<Record<ContentLang, TextState>>): string[] {
  const out: string[] = []
  const need = neededLangs(d.language)
  for (const l of need) if (!textReady(d.texts[l], checks[l])) out.push('texts.' + l)
  if (d.button.on) {
    if (urlProblem(d.button.url)) out.push('button.url')
    for (const l of need) if (labelProblem(d.button.labels[l])) out.push('button.labels.' + l)
  }
  return out
}

/** problemsFor keeps core's text problems that belong to one language (those
 * without a language belong to every one). "empty" is shown by the page. */
export function problemsFor(problems: unknown, lang: ContentLang): TextProblem[] {
  if (!Array.isArray(problems)) return []
  return (problems as TextProblem[]).filter((p) => p && typeof p.code === 'string' && p.code !== 'empty' && (!p.lang || p.lang === lang))
}

/** splitProblems sorts a refused save's text problems by language: a problem
 * names its language, or the field ("texts.fa") does. Problems of another
 * field (a button label) are not the texts'. */
export function splitProblems(problems: unknown, field: string): Record<ContentLang, TextProblem[]> {
  const out: Record<ContentLang, TextProblem[]> = { fa: [], en: [] }
  if (!Array.isArray(problems) || (field && !field.startsWith('texts'))) return out
  const fieldLang = /^texts\.(fa|en)$/.exec(field)?.[1] as ContentLang | undefined
  for (const p of problems as TextProblem[]) {
    if (!p || typeof p.code !== 'string' || p.code === 'empty') continue
    const l = p.lang === 'fa' || p.lang === 'en' ? p.lang : fieldLang
    if (l) out[l].push(p)
  }
  return out
}

/** What each status allows. */
export type BroadcastAction = 'edit' | 'send' | 'pause' | 'resume' | 'cancel' | 'delete'

export function actionsFor(status: BroadcastStatus): BroadcastAction[] {
  switch (status) {
    case 'draft':
      return ['edit', 'send', 'delete']
    case 'scheduled':
      return ['edit', 'cancel']
    case 'running':
      return ['pause', 'cancel']
    case 'paused':
      return ['resume', 'cancel']
  }
  return []
}

/** actionsOf is actionsFor a broadcast: a message to resellers or a product announcement is never edited in the composer (cancel it and write a new one). */
export function actionsOf(b: Pick<BroadcastItem, 'status' | 'segment'>): BroadcastAction[] {
  const out = actionsFor(b.status)
  return b.segment === 'resellers' || b.segment === 'announce' ? out.filter((a) => a !== 'edit') : out
}

/** audienceKeys are the texts that say who a broadcast goes to (the dashboard's keys, joined with " · "). */
export function audienceKeys(b: Pick<BroadcastItem, 'segment' | 'language' | 'resellers'>): string[] {
  const out = ['broadcasts.segment.' + b.segment]
  if (b.segment === 'resellers' && b.resellers) {
    if (b.resellers.debt_only) out.push('broadcasts.resellers.debt_only')
    if (b.resellers.tier_id) out.push('broadcasts.resellers.one_tier')
    if (b.resellers.kind === 'reminder') out.push('broadcasts.resellers.reminder')
  }
  out.push('broadcasts.language.' + (b.language || 'all'))
  return out
}

/** editable: the composer may open it (core takes PUT then). */
export function editable(status: BroadcastStatus): boolean {
  return status === 'draft' || status === 'scheduled'
}

/** live: its numbers or status may still change by themselves (poll it). */
export function live(status: BroadcastStatus): boolean {
  return status === 'running' || status === 'paused' || status === 'scheduled'
}

/** changingSoon: a list row whose numbers or status change by themselves
 * soon: sending, or due within ten minutes (the list refreshes for those). */
export function changingSoon(b: Pick<BroadcastItem, 'status' | 'scheduled_at'>, now: number): boolean {
  if (b.status === 'running') return true
  return b.status === 'scheduled' && b.scheduled_at !== null && b.scheduled_at - now <= 600
}

/** sending: a progress bar makes sense. */
export function sending(status: BroadcastStatus): boolean {
  return status === 'running' || status === 'paused'
}

/** progress: recipients done (sent, blocked, failed or unknown) out of all. */
export function progress(c: BroadcastItem['counts']): { done: number; total: number; percent: number } {
  const done = c.sent + c.blocked + c.failed + c.unknown
  const total = Math.max(c.total, 0)
  const percent = total ? Math.min(100, Math.floor((done * 100) / total)) : 0
  return { done, total, percent }
}

/** firstLine is a short raw preview of a text: its first non-empty line, cut. */
export function firstLine(s: string, max = 80): string {
  const line = s.split(/\r?\n/).map((l) => l.trim()).find((l) => l) ?? ''
  const chars = Array.from(line)
  return chars.length > max ? chars.slice(0, max - 1).join('') + '…' : line
}

/** previewOf is a broadcast's text in the viewer's language (English for a
 * beta language), else the other one. */
export function previewOf(texts: LangPair, viewer: Lang): { text: string; lang: ContentLang } {
  const lang = contentLang(viewer)
  const other: ContentLang = lang === 'fa' ? 'en' : 'fa'
  if (texts[lang]?.trim()) return { text: firstLine(texts[lang]), lang }
  return { text: firstLine(texts[other] ?? ''), lang: other }
}

const pad = (n: number) => String(n).padStart(2, '0')

/** localInput writes a unix time as a datetime-local value in this device's
 * time zone ("2026-10-10T18:30"). */
export function localInput(unix: number): string {
  const d = new Date(unix * 1000)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** fromLocalInput reads a datetime-local value (this device's time zone) as
 * unix seconds; null when it is not a valid date and time. */
export function fromLocalInput(v: string): number | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(v.trim())
  if (!m) return null
  const [y, mo, d, h, mi, s] = [m[1], m[2], m[3], m[4], m[5], m[6] ?? '0'].map(Number)
  const date = new Date(y, mo - 1, d, h, mi, s)
  // Date rolls 31 April over to 1 May: refuse what it had to change.
  if (date.getFullYear() !== y || date.getMonth() !== mo - 1 || date.getDate() !== d || date.getHours() !== h || date.getMinutes() !== mi) {
    return null
  }
  return Math.floor(date.getTime() / 1000)
}

/** scheduleProblem checks a time to send at: in the future, at most 30 days ahead. */
export function scheduleProblem(at: number | null, now: number): '' | 'invalid' | 'past' | 'too_far' {
  if (at === null) return 'invalid'
  if (at <= now) return 'past'
  if (at > now + MAX_AHEAD) return 'too_far'
  return ''
}

/** defaultScheduleAt is the time the schedule input starts at: in an hour,
 * on the next quarter hour. */
export function defaultScheduleAt(now: number): number {
  const quarter = 15 * 60
  return Math.ceil((now + 3600) / quarter) * quarter
}

/** duration writes a number of seconds as the largest units that matter
 * ("8 minutes", "1 hour, 5 minutes"), in the language's own words and digits. */
export function duration(sec: number, lang: Lang): string {
  const locale = numberLocale(lang)
  const unit = (n: number, u: 'second' | 'minute' | 'hour' | 'day') =>
    new Intl.NumberFormat(locale, { style: 'unit', unit: u, unitDisplay: 'long' }).format(n)
  const s = Math.max(0, Math.round(sec))
  if (s < 60) return unit(s, 'second')
  if (s < 3600) return unit(Math.ceil(s / 60), 'minute')
  const join = (a: string, b: string) => (lang === 'fa' ? `${a} و ${b}` : lang === 'ar' ? `${a} و${b}` : `${a}, ${b}`)
  if (s < 86_400) {
    const h = Math.floor(s / 3600)
    const m = Math.round((s % 3600) / 60)
    return m ? join(unit(h, 'hour'), unit(m, 'minute')) : unit(h, 'hour')
  }
  const d = Math.floor(s / 86_400)
  const h = Math.round((s % 86_400) / 3600)
  return h ? join(unit(d, 'day'), unit(h, 'hour')) : unit(d, 'day')
}

/** rate writes messages per second with at most one decimal. */
export function rate(n: number, lang: Lang): string {
  return new Intl.NumberFormat(numberLocale(lang), { maximumFractionDigits: 1 }).format(n)
}

/** rateCount is the number rate()'s text is pluralized by: only exactly 1 as
 * shown (one decimal) is singular, so "0.5 messages a second" is plural. */
export function rateCount(n: number): number {
  return Math.round(n * 10) / 10 === 1 ? 1 : 2
}

/** percent writes 0..100 as a percentage in the language's digits. */
export function percent(n: number, lang: Lang): string {
  return new Intl.NumberFormat(numberLocale(lang), { style: 'percent', maximumFractionDigits: 0 }).format(n / 100)
}

/** The outcome of "Send to me", for the page's message. */
export type TestOutcome = 'ok' | 'blocked' | 'bad_request' | 'error' | 'unreachable' | 'rate_limited' | 'invalid' | 'failed'

/** testOutcome sorts core's answer to POST /broadcasts/test, or its error
 * (status and code of an ApiError). */
export function testOutcome(res: { ok?: boolean; problem?: string } | null, err?: { status: number; code: string }): TestOutcome {
  if (err) {
    if (err.status === 503 || err.code === 'unavailable') return 'unreachable'
    if (err.status === 429 || err.code === 'rate_limited') return 'rate_limited'
    if (err.code === 'invalid') return 'invalid'
    return 'failed'
  }
  if (res?.ok) return 'ok'
  if (res?.problem === 'blocked') return 'blocked'
  if (res?.problem === 'bad_request') return 'bad_request'
  return 'error'
}

/**
 * poller calls fn every ms() milliseconds after the last call finished
 * (never two at once), skipping turns while the browser tab is hidden.
 */
export function poller(fn: () => Promise<unknown>, ms: () => number) {
  let timer: ReturnType<typeof setTimeout> | undefined
  let on = false
  const hidden = () => typeof document !== 'undefined' && document.hidden
  function next() {
    clearTimeout(timer)
    if (on) timer = setTimeout(tick, ms())
  }
  async function tick() {
    if (!on) return
    if (!hidden()) {
      try {
        await fn()
      } catch {
        /* the next turn tries again */
      }
    }
    next()
  }
  return {
    start() {
      if (on) return
      on = true
      next()
    },
    stop() {
      on = false
      clearTimeout(timer)
    },
    get running() {
      return on
    },
  }
}
