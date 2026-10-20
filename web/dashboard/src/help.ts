// Help content (Branding, "Help content"): the FAQ under Support and the
// tutorials behind the bot's Tutorials button. Pure helpers for the editor: the
// drafts, the checks core also makes (so the page can say what is wrong before
// asking), how an entry reads in a language with the fallback to the other one,
// the length of the whole FAQ, and moving an entry in its list. Nothing here
// touches the DOM or the API, so it is unit-tested; core stays the judge of
// what is saved (the markup of a text is checked by core, not here).
import type { Lang } from './format'
import { textLength } from './branding'
import type { HelpButton, HelpDraft, HelpItem, HelpKind, HelpLimits, HelpProblem } from './types'

export const HELP_LANGS: readonly Lang[] = ['fa', 'en']
export const HELP_KINDS: readonly HelpKind[] = ['faq', 'tutorial']

/** The limits core applies, as of this version: used until core's own arrive. */
export const DEFAULT_LIMITS: HelpLimits = {
  max_faq: 50,
  max_tutorials: 30,
  question: 200,
  tutorial_title: 64,
  body: 3000,
  buttons: 8,
  button_label: 64,
  url: 1024,
  one_message_entries: 12,
  one_message_units: 3000,
}

/** titleMax is the longest title of a kind of entry. */
export function titleMax(kind: HelpKind, limits: HelpLimits): number {
  return kind === 'faq' ? limits.question : limits.tutorial_title
}

/** maxEntries is how many entries of a kind a store keeps. */
export function maxEntries(kind: HelpKind, limits: HelpLimits): number {
  return kind === 'faq' ? limits.max_faq : limits.max_tutorials
}

const blankPair = () => ({ fa: '', en: '' })

/** blankDraft is a new entry: shown at once, nothing written. */
export function blankDraft(kind: HelpKind): HelpDraft {
  return { kind, enabled: true, title: blankPair(), body: blankPair(), buttons: [] }
}

/** draftOf copies a stored entry for editing. */
export function draftOf(item: HelpItem): HelpDraft {
  return {
    kind: item.kind,
    enabled: item.enabled,
    title: { ...item.title },
    body: { ...item.body },
    buttons: item.buttons.map((b) => ({ url: b.url, label: { ...b.label } })),
  }
}

/** newButton is an empty URL button. */
export function newButton(): HelpButton {
  return { url: '', label: blankPair() }
}

/** tidyLine reads a title or a label as core stores it: one line, its white
 * space gathered into single spaces. */
export function tidyLine(s: string): string {
  return s.replace(/\s+/g, ' ').trim()
}

/** units counts a plain text the way Telegram counts a message: UTF-16 units. */
export function units(s: string): number {
  return s.length
}

/** isHelpUrl accepts what core lets on a button or in a link: https, a host
 * with a dot, no user name or password, plain ASCII with no space, short. */
export function isHelpUrl(s: string, limits: HelpLimits = DEFAULT_LIMITS): boolean {
  if (s === '' || s.length > limits.url || !/^[\x21-\x7e]+$/.test(s)) return false
  try {
    const u = new URL(s)
    const host = u.hostname
    return u.protocol === 'https:' && u.username === '' && u.password === '' && host.includes('.') && !host.startsWith('.') && !host.endsWith('.')
  } catch {
    return false
  }
}

export type LangState = 'empty' | 'half' | 'complete'

/** langState says what a language of a draft has: nothing, a title or a text
 * without the other (core refuses that), or both (a translation customers can read). */
export function langState(d: Pick<HelpDraft, 'title' | 'body'>, lang: Lang): LangState {
  const t = tidyLine(d.title[lang]) !== ''
  const b = d.body[lang].trim() !== ''
  if (t && b) return 'complete'
  return t || b ? 'half' : 'empty'
}

/** localProblems are what the page can tell without asking core: the lengths,
 * a language with half a pair, an entry nobody can read, and the buttons.
 * Core checks more (the markup of a text) and has the last word. */
export function localProblems(d: HelpDraft, limits: HelpLimits = DEFAULT_LIMITS): HelpProblem[] {
  const out: HelpProblem[] = []
  const add = (field: string, code: string, arg = '') => out.push({ field, code, arg })
  let written = 0
  for (const l of HELP_LANGS) {
    const title = tidyLine(d.title[l])
    const body = d.body[l].trim()
    if (units(title) > titleMax(d.kind, limits)) add(`title.${l}`, 'too_long', String(titleMax(d.kind, limits)))
    if (textLength(body) > limits.body) add(`body.${l}`, 'too_long', String(limits.body))
    if (!title && !body) continue
    written++
    if (!title) add(`title.${l}`, 'pair_missing', 'title')
    if (!body) add(`body.${l}`, 'pair_missing', 'body')
  }
  if (written === 0) add('', 'need_one_language')
  if (d.kind === 'faq') {
    if (d.buttons.length) add('buttons', 'buttons_not_allowed')
    return out
  }
  if (d.buttons.length > limits.buttons) {
    add('buttons', 'too_many_buttons', String(limits.buttons))
    return out
  }
  d.buttons.forEach((b, i) => {
    const url = b.url.trim()
    if (url === '') add(`buttons.${i}.url`, 'url_missing')
    else if (!isHelpUrl(url, limits)) add(`buttons.${i}.url`, 'bad_url', url)
    let any = false
    for (const l of HELP_LANGS) {
      const label = tidyLine(b.label[l])
      if (label) any = true
      if (units(label) > limits.button_label) add(`buttons.${i}.label.${l}`, 'too_long', String(limits.button_label))
    }
    if (!any) add(`buttons.${i}.label.fa`, 'label_missing')
  })
  return out
}

/** problemsAt are the problems of one field. */
export function problemsAt(problems: readonly HelpProblem[], field: string): HelpProblem[] {
  return problems.filter((p) => p.field === field)
}

/** problemsUnder are the problems of a field and of everything inside it
 * ("buttons.1" covers "buttons.1.url" and "buttons.1.label.fa"). */
export function problemsUnder(problems: readonly HelpProblem[], prefix: string): HelpProblem[] {
  return problems.filter((p) => p.field === prefix || p.field.startsWith(prefix + '.'))
}

/** What the bot shows of an entry to a customer: the text in one language and the buttons. */
export interface Reading {
  /** the language the text is in: the customer's, or the other when theirs is missing */
  lang: Lang
  title: string
  body: string
  buttons: { label: string; url: string }[]
}

/** otherLang is the language an entry falls back to. */
const others = (lang: Lang): Lang[] => HELP_LANGS.filter((l) => l !== lang)

/** readIn reads an entry (or a draft of one) as a customer of a language does:
 * their own language when it has a title and a text, else the other; null when
 * neither has both. A button takes the other language's label where its own is
 * missing, and one with no label or an address the bot cannot use is left out. */
export function readIn(d: Pick<HelpDraft, 'title' | 'body' | 'buttons'>, lang: Lang, limits: HelpLimits = DEFAULT_LIMITS): Reading | null {
  for (const l of [lang, ...others(lang)]) {
    const title = tidyLine(d.title[l])
    const body = d.body[l].trim()
    if (!title || !body) continue
    const buttons: Reading['buttons'] = []
    for (const b of d.buttons) {
      const url = b.url.trim()
      const label = [lang, ...others(lang)].map((x) => tidyLine(b.label[x])).find((s) => s !== '')
      if (label && isHelpUrl(url, limits)) buttons.push({ label, url })
    }
    return { lang: l, title, body, buttons }
  }
  return null
}

/** faqSize is what the bot weighs to decide between one message and a list:
 * how many enabled questions customers of a language read and how many units
 * their questions and answers take together (core's rule: FitsOneMessage). */
export function faqSize(items: readonly HelpItem[], lang: Lang, limits: HelpLimits = DEFAULT_LIMITS): { entries: number; units: number } {
  let entries = 0
  let total = 0
  for (const it of items) {
    if (it.kind !== 'faq' || !it.enabled) continue
    const r = readIn(it, lang, limits)
    if (!r) continue
    entries++
    total += units(r.title) + textLength(r.body)
  }
  return { entries, units: total }
}

/** move puts the id one place up (-1) or down (1) in a list of ids; the same
 * list when it is at the edge or not there. */
export function move(ids: readonly string[], id: string, by: -1 | 1): string[] {
  const out = [...ids]
  const at = out.indexOf(id)
  const to = at + by
  if (at < 0 || to < 0 || to >= out.length) return out
  ;[out[at], out[to]] = [out[to], out[at]]
  return out
}

/** The words of the bot's own buttons around an entry, in each language, as
 * the bot has them by default (the preview cannot know the store's edits). */
export const BOT_WORDS: Record<Lang, { support: string; back: string; home: string }> = {
  fa: { support: '💬 ارسال پیام به پشتیبانی', back: '» بازگشت', home: '🏠 منوی اصلی' },
  en: { support: '💬 Send a message to support', back: '« Back', home: '🏠 Main menu' },
}

/** requestOf is a draft as core takes it (POST and PUT /help): texts as typed,
 * titles and labels tidied, empty button labels left as empty strings. */
export function requestOf(d: HelpDraft): Record<string, unknown> {
  return {
    kind: d.kind,
    enabled: d.enabled,
    title: { fa: tidyLine(d.title.fa), en: tidyLine(d.title.en) },
    body: { fa: d.body.fa.trim(), en: d.body.en.trim() },
    buttons: d.kind === 'tutorial' ? d.buttons.map((b) => ({ url: b.url.trim(), label: { fa: tidyLine(b.label.fa), en: tidyLine(b.label.en) } })) : [],
  }
}

/** sameDraft says whether two drafts hold the same thing (the editor asks
 * before it closes on unsaved changes). */
export function sameDraft(a: HelpDraft, b: HelpDraft): boolean {
  return JSON.stringify(requestOf(a)) === JSON.stringify(requestOf(b))
}

/** firstLine is the first non-empty line of a text without its tags, for a list row. */
export function firstLine(s: string): string {
  return (s ?? '')
    .replace(/<[^<>]*>/g, '')
    .split('\n')
    .map((x) => x.trim())
    .find((x) => x !== '') ?? ''
}
