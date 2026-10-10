// Branding and bot-text helpers for the Branding page: colour checks, link
// checks, the text filter, the import reader and the preview tokenizer.
// Pure functions only (no DOM, no API), so they are unit-tested.
import { latinDigits, type Lang } from './format'
import type { TextItem } from './types'

/** A brand colour as stored: #rrggbb. */
export const HEX_RE = /^#[0-9a-f]{6}$/i
/** The colour the dashboard uses when none is set (Aura teal 500). */
export const DEFAULT_COLOR = '#14b8a6'

/** normalizeHex reads a typed or picked colour ("0F766E", "#0f766e") as
 * "#0f766e"; anything else gives "". */
export function normalizeHex(s: string): string {
  const v = s.trim().toLowerCase()
  const hex = v.startsWith('#') ? v : '#' + v
  return HEX_RE.test(hex) ? hex : ''
}

function linear(c: number): number {
  const s = c / 255
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
}

/** luminance is the WCAG relative luminance of a #rrggbb colour (0 black … 1 white). */
export function luminance(hex: string): number {
  const n = parseInt(hex.slice(1), 16)
  return 0.2126 * linear((n >> 16) & 255) + 0.7152 * linear((n >> 8) & 255) + 0.0722 * linear(n & 255)
}

/** contrastWithWhite is the WCAG contrast ratio of white text on the colour
 * (1 … 21). Normal text needs 4.5 or more. */
export function contrastWithWhite(hex: string): number {
  return 1.05 / (luminance(hex) + 0.05)
}

/** isHttpsUrl accepts a full https link with a host and no user name or password. */
export function isHttpsUrl(s: string): boolean {
  if (/\s/.test(s)) return false
  try {
    const u = new URL(s)
    return u.protocol === 'https:' && u.hostname !== '' && u.username === '' && u.password === ''
  } catch {
    return false
  }
}

/** brandingField maps the field a branding error names ("branding.terms_url",
 * "currency_fa") to the form's own name ("terms_url", "currency.fa"). */
export function brandingField(field: string): string {
  return field
    .trim()
    .replace(/^branding\./, '')
    .replace(/^currency[._](fa|en)$/, 'currency.$1')
}

// Logo files: what core accepts.
export const LOGO_TYPES = ['image/png', 'image/jpeg', 'image/webp']
export const LOGO_MAX_BYTES = 512 * 1024

/** logoProblem says why a chosen file cannot be a logo, or "" when it can. */
export function logoProblem(file: { type: string; size: number }): '' | 'bad_type' | 'too_large' {
  if (!LOGO_TYPES.includes(file.type)) return 'bad_type'
  if (file.size > LOGO_MAX_BYTES) return 'too_large'
  return ''
}

/** logoSizeOk checks an image's pixel size (16 to 2048 on each side). */
export function logoSizeOk(width: number, height: number): boolean {
  return width >= 16 && height >= 16 && width <= 2048 && height <= 2048
}

// ---------------------------------------------------------------------------
// Bot texts

/** The languages bot texts have. */
export const TEXT_LANGS: readonly Lang[] = ['fa', 'en']

/** textLangs are the languages of a text that the bot reads. */
export function textLangs(it: TextItem): Lang[] {
  return TEXT_LANGS.filter((l) => it.langs.includes(l))
}

/** isChanged: the text has an override in some language. */
export function isChanged(it: TextItem): boolean {
  return TEXT_LANGS.some((l) => (it.override[l] ?? '') !== '')
}

/** currentText is what the bot sends now: the override, else the default. */
export function currentText(it: TextItem, lang: Lang): string {
  return it.override[lang] || it.default[lang] || ''
}

/** isHtmlContext: the text is sent with Telegram's HTML parse mode. */
export function isHtmlContext(context: string): boolean {
  return context === 'html' || context === 'caption'
}

/** fold makes search forgiving: case, Arabic or Persian ye/kaf, half-spaces, digits. */
function fold(s: string): string {
  return latinDigits(s)
    .toLowerCase()
    .replace(/ي/g, 'ی')
    .replace(/ك/g, 'ک')
    .replace(/[‌\s]+/g, ' ')
}

export interface TextFilter {
  q: string
  group: string
  changed: boolean
}

/** filterTexts finds texts by key or by text in either language. */
export function filterTexts(items: TextItem[], f: TextFilter): TextItem[] {
  const q = fold(f.q).trim()
  return items.filter(
    (it) =>
      (!f.group || it.group === f.group) &&
      (!f.changed || isChanged(it)) &&
      (!q || [it.key, it.default.fa, it.default.en, it.override.fa, it.override.en].some((s) => s && fold(s).includes(q))),
  )
}

/** textLength counts a text the way the bot's limits do: UTF-16 units of the
 * trimmed text without tags, an entity counting as one character. */
export function textLength(s: string): number {
  return s
    .replace(/\r\n?/g, '\n')
    .trim()
    .replace(/<[^<>]*>/g, '')
    .replace(/&(?:lt|gt|amp|quot|#[0-9]{1,7}|#x[0-9a-fA-F]{1,6});/g, '&').length
}

/** insertAt puts text in place of the selection [start, end) and says where
 * the cursor goes after it. */
export function insertAt(value: string, start: number, end: number, text: string): { value: string; cursor: number } {
  const s = Math.max(0, Math.min(start, value.length))
  const e = Math.max(s, Math.min(end, value.length))
  return { value: value.slice(0, s) + text + value.slice(e), cursor: s + text.length }
}

/** problemArg is how a problem's argument is shown: placeholders with their
 * braces, tags with their angle brackets. */
export function problemArg(p: { code: string; arg: string }): string {
  if (!p.arg) return ''
  switch (p.code) {
    case 'placeholder_missing':
    case 'placeholder_unknown':
      return p.arg.startsWith('{') ? p.arg : '{' + p.arg + '}'
    case 'tag_not_allowed':
    case 'tag_unclosed':
    case 'tag_in_code':
    case 'bad_attr':
      return '<' + p.arg + '>'
    case 'tag_mismatch':
      return '</' + p.arg + '>'
  }
  return p.arg
}

/** The entity that writes a character Telegram's HTML reserves. */
export const ENTITY_FOR: Record<string, string> = { '<': '&lt;', '>': '&gt;', '&': '&amp;' }

export type ImportResult =
  | { ok: true; lang: Lang | null; texts: Record<string, string> }
  | { ok: false; error: 'not_json' | 'shape' | 'empty' }

/** parseImport reads an import file: an export ({"lang","texts"}) or a plain
 * key → text object (then the language is chosen by hand). */
export function parseImport(raw: string): ImportResult {
  let data: unknown
  try {
    data = JSON.parse(raw.replace(/^﻿/, ''))
  } catch {
    return { ok: false, error: 'not_json' }
  }
  const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
  if (!isObject(data)) return { ok: false, error: 'shape' }
  let lang: Lang | null = null
  let texts: unknown = data
  if ('texts' in data && isObject(data.texts)) {
    if (data.lang !== undefined && data.lang !== 'fa' && data.lang !== 'en') return { ok: false, error: 'shape' }
    lang = (data.lang as Lang | undefined) ?? null
    texts = data.texts
  }
  if (!isObject(texts)) return { ok: false, error: 'shape' }
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(texts)) {
    if (!k || typeof v !== 'string') return { ok: false, error: 'shape' }
    out[k] = v
  }
  if (!Object.keys(out).length) return { ok: false, error: 'empty' }
  return { ok: true, lang, texts: out }
}

// ---------------------------------------------------------------------------
// Preview: the Telegram HTML a text may use, as a tree the page turns into
// Vue nodes. Nothing here is HTML for the browser: unknown tags stay text.

export type PreviewTag = 'b' | 'i' | 'u' | 's' | 'code' | 'pre' | 'blockquote' | 'spoiler' | 'a'

export type PreviewNode =
  | { type: 'text'; text: string }
  | { type: 'ph'; name: string }
  | { type: 'el'; tag: PreviewTag; href?: string; children: PreviewNode[] }

const tagAlias: Record<string, PreviewTag> = {
  b: 'b', strong: 'b', i: 'i', em: 'i', u: 'u', ins: 'u', s: 's', strike: 's', del: 's',
  code: 'code', pre: 'pre', blockquote: 'blockquote', 'tg-spoiler': 'spoiler', span: 'spoiler', a: 'a',
}
const tagRe = /^<(\/?)([a-zA-Z][a-zA-Z0-9-]*)((?:\s+[a-zA-Z-]+(?:\s*=\s*"[^"<>]*")?)*)\s*>/
const attrRe = /([a-zA-Z-]+)(?:\s*=\s*"([^"]*)")?/g
const entityRe = /^&(?:(lt|gt|amp|quot)|#([0-9]{1,7})|#x([0-9a-fA-F]{1,6}));/
const placeholderRe = /^\{([a-z_]+)\}/
const named: Record<string, string> = { lt: '<', gt: '>', amp: '&', quot: '"' }

/** openTag checks a start tag against Telegram's rules; null when it is not one. */
function openTag(name: string, attrs: string): { tag: PreviewTag; href?: string } | null {
  const tag = tagAlias[name]
  if (!tag) return null
  const got: Record<string, string> = {}
  for (const m of attrs.matchAll(attrRe)) got[m[1].toLowerCase()] = m[2] ?? ''
  const n = Object.keys(got).length
  if (name === 'a') {
    const href = got.href
    if (n !== 1 || href === undefined || href.length < 9 || !/^(https|tg):\/\//.test(href)) return null
    return { tag, href }
  }
  if (name === 'span') return n === 1 && got.class === 'tg-spoiler' ? { tag } : null
  if (name === 'blockquote') return n === 0 || (n === 1 && 'expandable' in got) ? { tag } : null
  return n === 0 ? { tag } : null
}

/** parsePreview turns a bot text into preview nodes. Only the allowed tags
 * (in HTML contexts) become elements; every other tag, broken markup and
 * unknown {name} stays literal text, as Telegram would show it or refuse it.
 * Placeholders the text may use become ph nodes. */
export function parsePreview(src: string, html: boolean, placeholders: readonly string[]): PreviewNode[] {
  const root: PreviewNode[] = []
  const stack: { name: string; children: PreviewNode[] }[] = [{ name: '', children: root }]
  const top = () => stack[stack.length - 1].children
  const text = (s: string) => {
    const list = top()
    const last = list[list.length - 1]
    if (last?.type === 'text') last.text += s
    else list.push({ type: 'text', text: s })
  }
  let i = 0
  while (i < src.length) {
    const rest = src.slice(i)
    const c = src[i]
    if (html && c === '<') {
      const m = tagRe.exec(rest)
      if (m) {
        const closing = m[1] === '/'
        const name = m[2].toLowerCase()
        if (closing) {
          if (stack.length > 1 && stack[stack.length - 1].name === name) stack.pop()
          else text(m[0])
        } else {
          const open = openTag(name, m[3])
          if (open) {
            const el: Extract<PreviewNode, { type: 'el' }> = { type: 'el', tag: open.tag, children: [] }
            if (open.href !== undefined) el.href = open.href
            top().push(el)
            stack.push({ name, children: el.children })
          } else {
            text(m[0])
          }
        }
        i += m[0].length
        continue
      }
    } else if (html && c === '&') {
      const m = entityRe.exec(rest)
      if (m) {
        const code = m[2] ? parseInt(m[2], 10) : m[3] ? parseInt(m[3], 16) : -1
        const ch = m[1] ? named[m[1]] : code >= 0 && code <= 0x10ffff ? String.fromCodePoint(code) : m[0]
        text(ch)
        i += m[0].length
        continue
      }
    } else if (c === '{') {
      const m = placeholderRe.exec(rest)
      if (m && placeholders.includes(m[1])) {
        top().push({ type: 'ph', name: m[1] })
        i += m[0].length
        continue
      }
    }
    // Plain text up to the next character that may start something.
    const next = rest.slice(1).search(html ? /[<&{]/ : /\{/)
    const run = next < 0 ? rest : rest.slice(0, next + 1)
    text(run)
    i += run.length
  }
  return root
}

// Sample values for the placeholders, in the text's own language (data, not
// dashboard messages: a Persian text previews with Persian values).
const samples: Record<Lang, Record<string, string>> = {
  fa: {
    brand: 'فروشگاه شما', name: 'ماهانه', plan: 'ماهانه', limits: '۳۰ روز · ۵۰ گیگابایت', price: '۱۵۰٬۰۰۰ تومان',
    amount: '۱۵۰٬۰۰۰ تومان', balance: '۲۰۰٬۰۰۰ تومان', earned: '۴۵٬۰۰۰ تومان', wallets: '۲۰۰٬۰۰۰ تومان',
    max: '۱۰٬۰۰۰٬۰۰۰ تومان', min: '۵۰٬۰۰۰ تومان', n: '۳', days: '۳', size: '۵۰ گیگابایت', traffic: '۵۰ گیگابایت',
    total: '۵۰ گیگابایت', used: '۱۲ گیگابایت', bar: '▰▰▰▱▱', card: '6037 9975 1234 5678', holder: 'علی رضایی',
    ref: 'A1B2C3', addresses: 'TRC20: TXa1…9k', addr: 'TXa1…9k', usdt: '۲٫۵ USDT', stars: '۱۲۰',
    reason: 'مبلغ واریزی کامل نیست', code: 'OFF20', off: '۲۰٪', contact: '@support', expires: '۱۵ اردیبهشت ۱۴۰۵',
    date: '۱۵ اردیبهشت ۱۴۰۵', status: 'فعال', id: 'a1b2c3', order: 'a1b2c3', link: 'https://example.com/sub/a1b2',
    invited: '۷', percent: '۱۰', active: '۱۲۰', failed: '۰', panel: 'سالم', pending: '۲', today: '۹', users: '۱٬۲۵۰',
    orders: '۴', subs: '۲', attempts: '۳', detail: 'timeout', error: 'timeout', dup: 'احتمال تکرار', method: 'کارت به کارت',
    proof: 'A1B2C3', user: '@customer', when: '۵ دقیقه پیش', network: 'TRC20', txid: '9f2c…e1', lang: 'fa',
    role: 'user', tg: '123456789', key: 'notify.sales', value: 'true', list: '…', text: 'سلام، کمک می‌خواهم',
    charge: 'ch_123', payload: 'order:a1b2c3', line: 'OFF20 · ۲۰٪',
  },
  en: {
    brand: 'Your store', name: 'Monthly', plan: 'Monthly', limits: '30 days · 50 GB', price: '150,000 Toman',
    amount: '150,000 Toman', balance: '200,000 Toman', earned: '45,000 Toman', wallets: '200,000 Toman',
    max: '10,000,000 Toman', min: '50,000 Toman', n: '3', days: '3', size: '50 GB', traffic: '50 GB',
    total: '50 GB', used: '12 GB', bar: '▰▰▰▱▱', card: '6037 9975 1234 5678', holder: 'Ali Rezaei',
    ref: 'A1B2C3', addresses: 'TRC20: TXa1…9k', addr: 'TXa1…9k', usdt: '2.5 USDT', stars: '120',
    reason: 'The amount is not complete', code: 'OFF20', off: '20%', contact: '@support', expires: '5 May 2026',
    date: '5 May 2026', status: 'Active', id: 'a1b2c3', order: 'a1b2c3', link: 'https://example.com/sub/a1b2',
    invited: '7', percent: '10', active: '120', failed: '0', panel: 'healthy', pending: '2', today: '9', users: '1,250',
    orders: '4', subs: '2', attempts: '3', detail: 'timeout', error: 'timeout', dup: 'possible duplicate', method: 'Card transfer',
    proof: 'A1B2C3', user: '@customer', when: '5 minutes ago', network: 'TRC20', txid: '9f2c…e1', lang: 'en',
    role: 'user', tg: '123456789', key: 'notify.sales', value: 'true', list: '…', text: 'Hi, I need help',
    charge: 'ch_123', payload: 'order:a1b2c3', line: 'OFF20 · 20%',
  },
}

/** sampleValue is what the preview shows for {name}; the store's own name for {brand}. */
export function sampleValue(name: string, lang: Lang, brand = ''): string {
  if (name === 'brand' && brand) return brand
  return samples[lang][name] ?? name
}
