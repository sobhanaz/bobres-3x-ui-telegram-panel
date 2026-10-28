// The Settings page's rules: what each kind of setting accepts, what a card
// sends, and how a channel check reads. Core checks everything again (it is
// the source of truth); this only spares staff a round trip.
import { ApiError } from './api'
import { asciiNumber, latinDigits } from './format'
import type { ChannelCheck, SettingItem } from './types'

/** A value a setting does not accept: a code for settings.invalid.<code>. */
export interface Problem {
  code: string
  params?: Record<string, number>
}

/** A tier of the reseller targets: how many new services a reseller sells in a month, and the bonus in Toman for reaching it. */
export interface Tier {
  threshold: number
  bonus: number
}

/** Limits core checks again. */
export const MAX_TIERS = 10
export const MAX_THRESHOLD = 100_000
export const MAX_BONUS = 100_000_000_000

/** parseTiers reads the list the setting targets.tiers holds, by core's rules: whole numbers only, at most MAX_TIERS tiers, each threshold from 1
 * to MAX_THRESHOLD and each bonus from 1 to MAX_BONUS, no threshold twice. The tiers come back sorted; a code names what is wrong. */
export function parseTiers(raw: string): { tiers: Tier[] } | { code: 'tiers' | 'tiers_many' | 'tiers_dup' } {
  if (raw.trim() === '') return { tiers: [] }
  let v: unknown
  try {
    v = JSON.parse(raw)
  } catch {
    return { code: 'tiers' }
  }
  if (v === null) return { tiers: [] }
  if (!Array.isArray(v)) return { code: 'tiers' }
  if (v.length > MAX_TIERS) return { code: 'tiers_many' }
  const tiers: Tier[] = []
  for (const row of v) {
    if (typeof row !== 'object' || row === null || Array.isArray(row)) return { code: 'tiers' }
    const keys = Object.keys(row)
    if (keys.length !== 2 || !keys.includes('threshold') || !keys.includes('bonus')) return { code: 'tiers' }
    const { threshold, bonus } = row as Record<string, unknown>
    if (typeof threshold !== 'number' || typeof bonus !== 'number' || !Number.isInteger(threshold) || !Number.isInteger(bonus)) return { code: 'tiers' }
    if (threshold < 1 || threshold > MAX_THRESHOLD || bonus < 1 || bonus > MAX_BONUS) return { code: 'tiers' }
    tiers.push({ threshold, bonus })
  }
  tiers.sort((a, b) => a.threshold - b.threshold)
  for (let i = 1; i < tiers.length; i++) if (tiers[i].threshold === tiers[i - 1].threshold) return { code: 'tiers_dup' }
  return { tiers }
}

/** formatTiers writes the list the way core stores it: sorted, compact JSON, "" for none. */
export function formatTiers(tiers: Tier[]): string {
  if (!tiers.length) return ''
  return JSON.stringify([...tiers].sort((a, b) => a.threshold - b.threshold).map((t) => ({ threshold: t.threshold, bonus: t.bonus })))
}

/** normalizeTiers is a list as it will be stored when it is valid, else as typed (for validate to refuse). */
export function normalizeTiers(raw: string): string {
  const p = parseTiers(raw)
  return 'tiers' in p ? formatTiers(p.tiers) : raw.trim()
}

/** Groups core may still list that are edited on another page: the
 * payment details are on the Payment gateways page. */
export const movedGroups = ['payments']

/** effective is what the bot uses: the stored value, or the default. */
export function effective(item: SettingItem): string {
  return item.value !== '' ? item.value : item.default
}

const channelName = /^@[A-Za-z][A-Za-z0-9_]{4,31}$/
const channelId = /^-100\d+$/
const trc20 = /^T[1-9A-HJ-NP-Za-km-z]{33}$/
const erc20 = /^0x[0-9a-fA-F]{40}$/
const controlChars = /[\u0000-\u001f\u007f]/

/** normalizeChannel accepts what staff paste: "name", "@name", a t.me link
 * of a public channel, or a numeric id typed with Persian digits. */
export function normalizeChannel(raw: string): string {
  const v = latinDigits(raw.trim()).replace(/^[−‒–]/, '-')
  const link = /^(?:https?:\/\/)?(?:www\.)?(?:t|telegram)\.me\/([A-Za-z][A-Za-z0-9_]{4,31})\/?$/i.exec(v)
  if (link) return '@' + link[1]
  if (/^[A-Za-z][A-Za-z0-9_]{4,31}$/.test(v)) return '@' + v
  return v
}

/** numericChannel: a private channel's id (-100…), which needs an invite link. */
export function numericChannel(v: string): boolean {
  return channelId.test(v)
}

/** normalizeTime reads a time of day typed in any digits as HH:MM ("9:30"
 * becomes "09:30"); anything else is returned as typed, for validate to refuse. */
export function normalizeTime(raw: string): string {
  const v = latinDigits(raw.trim())
  const m = /^(\d{1,2}):(\d{2})$/.exec(v)
  return m ? m[1].padStart(2, '0') + ':' + m[2] : v
}

/** normalize turns a typed value into what is sent: trimmed, numbers in
 * ASCII digits, a channel as @name or -100id, a time of day as HH:MM. */
export function normalize(item: SettingItem, raw: string): string {
  const s = raw.trim()
  if (item.kind === 'int') return asciiNumber(s)
  if (item.kind === 'channel') return normalizeChannel(s)
  if (item.kind === 'time') return normalizeTime(s)
  if (item.kind === 'list') return normalizeTiers(s)
  if (item.key === 'cashback.percent') {
    const bp = percentBP(s)
    return bp === null ? s : String(bp / 100)
  }
  if (item.key === 'payments.card_number') return latinDigits(s).replace(/^ir/i, 'IR')
  return s
}

/** canonical is the value a draft stands for ("" falls back to the default). */
export function canonical(item: SettingItem, raw: string): string {
  const v = normalize(item, raw)
  return v === '' ? item.default : v
}

/** changed: the draft would change what the bot uses. */
export function changed(item: SettingItem, raw: string): boolean {
  return canonical(item, raw) !== effective(item)
}

/** Keys checked again when another key changes (core checks them together). */
export const dependsOn: Record<string, string[]> = { 'join.link': ['join.channel'] }

function httpsURL(v: string): boolean {
  if (/\s/.test(v)) return false
  try {
    const u = new URL(v)
    return u.protocol === 'https:' && u.hostname !== '' && u.username === '' && u.password === ''
  } catch {
    return false
  }
}

/** validChannel: a normalized channel or group, a public one's @name or a private one's -100… id. */
export function validChannel(v: string): boolean {
  return channelName.test(v) || channelId.test(v)
}

/** validJoinLink: the https://t.me/… link of a Join button (core takes the same). */
export function validJoinLink(v: string): boolean {
  return /^https:\/\/t\.me\/[^\s/]/.test(v) && httpsURL(v)
}

/** validate checks a normalized value. others holds every key's canonical
 * value in the same card, for rules that join two keys. "" always means
 * "back to the default" and is accepted, except join.link for a private channel. */
export function validate(item: SettingItem, value: string, others: Record<string, string> = {}): Problem | null {
  if (value === '') {
    if (item.key === 'join.link' && numericChannel(others['join.channel'] ?? '')) return { code: 'link_required' }
    return null
  }
  switch (item.kind) {
    case 'bool':
      return value === 'true' || value === 'false' ? null : { code: 'option' }
    case 'enum':
      return item.options.includes(value) ? null : { code: 'option' }
    case 'tz':
      return value !== 'Local' && /^[A-Za-z]+(?:[/_+-][A-Za-z0-9]+)*$/.test(value) ? null : { code: 'timezone' }
    case 'time':
      return /^([01]\d|2[0-3]):[0-5]\d$/.test(value) ? null : { code: 'time' }
    case 'int': {
      if (!/^\d{1,15}$/.test(value)) return { code: 'int' }
      const n = Number(value)
      const { min, max } = item
      if (min !== null && max !== null && (n < min || n > max)) return { code: 'range', params: { min, max } }
      if (min !== null && n < min) return { code: 'min', params: { min } }
      if (max !== null && n > max) return { code: 'max', params: { max } }
      return null
    }
    case 'channel':
      return channelName.test(value) || channelId.test(value) ? null : { code: 'channel' }
    case 'url':
      if (item.key === 'join.link') return /^https:\/\/t\.me\/[^\s/]/.test(value) && httpsURL(value) ? null : { code: 'tme_link' }
      return httpsURL(value) ? null : { code: 'https' }
    case 'text':
      return validateText(item.key, value)
    case 'list': {
      const p = parseTiers(value)
      return 'code' in p ? { code: p.code } : null
    }
  }
  return null
}

function validateText(key: string, value: string): Problem | null {
  if (controlChars.test(value)) return { code: 'one_line' }
  switch (key) {
    case 'payments.card_number': {
      const d = value.replace(/[\s‐-―-]/g, '')
      return /^\d{16}$/.test(d) || /^IR\d{24}$/.test(d) ? null : { code: 'card' }
    }
    case 'payments.usdt_trc20':
      return trc20.test(value) ? null : { code: 'trc20' }
    case 'payments.usdt_erc20':
      return erc20.test(value) ? null : { code: 'erc20' }
    case 'payments.card_holder':
      return [...value].length > 64 ? { code: 'too_long', params: { max: 64 } } : null
    case 'cashback.percent':
      return percentBP(value) === null ? { code: 'percent' } : null
  }
  return new TextEncoder().encode(value).length > 1000 ? { code: 'too_long_text' } : null
}

const draftOf = (item: SettingItem, drafts: Record<string, string>) => drafts[item.key] ?? effective(item)

/** dirtyKeys are the editable keys whose draft changes something. */
export function dirtyKeys(items: SettingItem[], drafts: Record<string, string>): string[] {
  return items.filter((i) => i.editable && changed(i, draftOf(i, drafts))).map((i) => i.key)
}

/** problems checks the keys a save would send, and the keys that depend on them. */
export function problems(items: SettingItem[], drafts: Record<string, string>): Record<string, Problem> {
  const canon: Record<string, string> = {}
  for (const i of items) canon[i.key] = canonical(i, draftOf(i, drafts))
  const dirty = new Set(dirtyKeys(items, drafts))
  const out: Record<string, Problem> = {}
  for (const i of items) {
    if (!i.editable) continue
    if (!dirty.has(i.key) && !(dependsOn[i.key] ?? []).some((k) => dirty.has(k))) continue
    const p = validate(i, normalize(i, draftOf(i, drafts)), canon)
    if (p) out[i.key] = p
  }
  return out
}

/** changes is a card's PUT body: only the keys that changed, normalized. */
export function changes(items: SettingItem[], drafts: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const i of items) {
    if (i.editable && changed(i, draftOf(i, drafts))) out[i.key] = normalize(i, draftOf(i, drafts))
  }
  return out
}

/** rebase moves drafts onto fresh items: keys in reset (just saved) and
 * keys staff did not touch take the new value; other edits are kept. */
export function rebase(
  drafts: Record<string, string>,
  prev: SettingItem[],
  next: SettingItem[],
  reset: string[] = [],
): Record<string, string> {
  const before = new Map(prev.map((i) => [i.key, i]))
  const out: Record<string, string> = {}
  for (const item of next) {
    const old = before.get(item.key)
    const d = drafts[item.key]
    const keep = d !== undefined && old !== undefined && !reset.includes(item.key) && changed(old, d)
    out[item.key] = keep ? d : effective(item)
  }
  return out
}

/** serverField is the key a 400 from core names, or "". */
export function serverField(e: unknown): string {
  return e instanceof ApiError && e.status === 400 && typeof e.data.field === 'string' ? e.data.field : ''
}

/** unreachable: core says the bot did not answer (the channel check goes through it). */
export function unreachable(e: unknown): boolean {
  return e instanceof ApiError && (e.status === 503 || e.code === 'unavailable')
}

export interface CheckView {
  severity: 'success' | 'warn' | 'error'
  /** message key; {title} is the channel's title */
  key: string
  title: string
  /** Telegram's own words (English), when it said more */
  detail: string
}

const checkProblems = ['not_found', 'not_admin', 'not_channel', 'error']

/** describeCheck turns the bot's channel-check answer into a message. */
export function describeCheck(res: ChannelCheck): CheckView {
  const title = res.title ?? ''
  if (res.ok) {
    if (res.bot_admin === false) return { severity: 'warn', key: 'settings.check.ok_not_admin', title, detail: '' }
    return { severity: 'success', key: 'settings.check.ok', title, detail: '' }
  }
  const p = checkProblems.includes(res.problem ?? '') ? res.problem : 'error'
  return { severity: p === 'not_admin' ? 'warn' : 'error', key: `settings.check.problem.${p}`, title, detail: res.detail ?? '' }
}

let zones: string[] | null = null

/** timeZones lists the IANA zones the browser knows (plus UTC and Tehran,
 * which some browsers leave out of the list). */
export function timeZones(): string[] {
  if (!zones) {
    let list: string[] = []
    try {
      list = Intl.supportedValuesOf('timeZone')
    } catch {
      /* an older browser: the extras below */
    }
    const all = new Set(list)
    all.add('UTC')
    all.add('Asia/Tehran')
    zones = [...all].sort()
  }
  return zones
}

/** zoneOptions are the time zones to offer, always including the current value. */
export function zoneOptions(current: string): string[] {
  const list = timeZones()
  return current && !list.includes(current) ? [current, ...list] : list
}

/** zoneNow is the time in a zone and its offset ("14:05 · GMT+3:30"), or "". */
export function zoneNow(zone: string, at = new Date()): string {
  if (!zone) return ''
  try {
    const parts = new Intl.DateTimeFormat('en-GB', {
      timeZone: zone,
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
      timeZoneName: 'shortOffset',
    }).formatToParts(at)
    const part = (type: string) => parts.find((p) => p.type === type)?.value ?? ''
    return `${part('hour')}:${part('minute')} · ${part('timeZoneName')}`
  } catch {
    return ''
  }
}

/** ltr: values that read left to right (numbers, links, ids, addresses). */
export function ltr(item: SettingItem): boolean {
  return (
    ['int', 'url', 'channel', 'tz', 'time', 'color'].includes(item.kind) ||
    ['payments.card_number', 'payments.usdt_trc20', 'payments.usdt_erc20', 'cashback.percent'].includes(item.key)
  )
}

/** percentBP reads a percent typed as "5", "2.5", "۲٫۵" or "7%" (the point may be . , / or the Persian ٫ and ،) as hundredths of a
 * percent, 0.01 to 20 with at most two decimals; null when it is none of that. Core reads it the same way. */
export function percentBP(raw: string): number | null {
  const v = latinDigits(raw.trim()).replace(/%$/, '').trim().replace(/[,/٫،]/g, '.')
  const m = /^(\d{0,3})(?:\.(\d{0,2}))?$/.exec(v)
  if (!m || (m[1] === '' && !m[2])) return null
  const bp = Number(m[1] || '0') * 100 + Number((m[2] ?? '').padEnd(2, '0') || '0')
  return bp >= 1 && bp <= 2000 ? bp : null
}

/** maxLength keeps inputs near what core accepts. */
export function maxLength(item: SettingItem): number {
  const byKey: Record<string, number> = {
    'payments.card_holder': 64,
    'payments.card_number': 40,
    'payments.usdt_trc20': 34,
    'payments.usdt_erc20': 42,
  }
  if (byKey[item.key]) return byKey[item.key]
  if (item.kind === 'int') return 16
  if (item.kind === 'time') return 5
  if (item.kind === 'channel') return 64
  return 1000
}

/** example is a placeholder that shows the expected shape (or the default). */
export function example(item: SettingItem, channel = ''): string {
  switch (item.key) {
    case 'join.channel':
      return '@mychannel'
    case 'join.link':
      return channel.startsWith('@') ? 'https://t.me/' + channel.slice(1) : 'https://t.me/+…'
    case 'payments.usdt_trc20':
      return 'T…'
    case 'payments.usdt_erc20':
      return '0x…'
    case 'payments.zarinpal_link':
      return 'https://zarinp.al/…'
  }
  return item.default
}

/** wideGroups picks the cards that take the page's full width on a
 * two-column page: those with many fields, and the last card of a run with
 * an odd count (so it does not sit next to an empty column). */
export function wideGroups(groups: { group: string; count: number }[]): string[] {
  const wide: string[] = []
  let run: string[] = []
  const close = () => {
    if (run.length % 2 === 1) wide.push(run[run.length - 1])
    run = []
  }
  for (const g of groups) {
    if (g.count >= 6) {
      close()
      wide.push(g.group)
    } else {
      run.push(g.group)
    }
  }
  close()
  return wide
}

/** fieldId is a DOM id for a setting's input. */
export function fieldId(key: string): string {
  return 'setting-' + key.replace(/[^A-Za-z0-9]/g, '-')
}
