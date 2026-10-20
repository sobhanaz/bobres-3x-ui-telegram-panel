import { describe, expect, it } from 'vitest'
import {
  DEFAULT_LIMITS,
  blankDraft,
  draftOf,
  faqSize,
  firstLine,
  isHelpUrl,
  langState,
  localProblems,
  maxEntries,
  move,
  newButton,
  problemsAt,
  problemsUnder,
  readIn,
  requestOf,
  sameDraft,
  tidyLine,
  titleMax,
} from '../help'
import fa from '../locales/fa.json'
import en from '../locales/en.json'
import type { HelpDraft, HelpItem } from '../types'

function item(over: Partial<HelpItem> = {}): HelpItem {
  return {
    id: 'a',
    kind: 'faq',
    title: { fa: 'سوال', en: 'Question' },
    body: { fa: 'پاسخ', en: 'Answer' },
    buttons: [],
    sort: 1,
    enabled: true,
    version: 1,
    updated_at: 0,
    ...over,
  }
}

function tutorial(over: Partial<HelpDraft> = {}): HelpDraft {
  return { ...blankDraft('tutorial'), title: { fa: 'عنوان', en: 'Title' }, body: { fa: 'متن', en: 'Text' }, ...over }
}

describe('help: limits', () => {
  it('picks the limit of a kind', () => {
    expect(titleMax('faq', DEFAULT_LIMITS)).toBe(DEFAULT_LIMITS.question)
    expect(titleMax('tutorial', DEFAULT_LIMITS)).toBe(DEFAULT_LIMITS.tutorial_title)
    expect(maxEntries('faq', DEFAULT_LIMITS)).toBe(DEFAULT_LIMITS.max_faq)
    expect(maxEntries('tutorial', DEFAULT_LIMITS)).toBe(DEFAULT_LIMITS.max_tutorials)
  })
})

describe('help: urls', () => {
  it('takes https addresses with a dotted host', () => {
    expect(isHelpUrl('https://play.google.com/store/apps/details?id=com.v2ray.ang')).toBe(true)
    expect(isHelpUrl('https://example.com')).toBe(true)
  })
  it('refuses anything else', () => {
    for (const bad of ['', 'http://example.com', 'javascript:alert(1)', 'https://localhost', 'https://user:pw@example.com', 'https://exa mple.com', 'https://.example.com', 'https://example.com.', 'https://مثال.com', 'example.com', 'tg://resolve?domain=x']) {
      expect(isHelpUrl(bad), bad).toBe(false)
    }
    expect(isHelpUrl('https://example.com/' + 'a'.repeat(DEFAULT_LIMITS.url))).toBe(false)
  })
})

describe('help: drafts', () => {
  it('starts a new entry enabled and empty', () => {
    const d = blankDraft('faq')
    expect(d).toEqual({ kind: 'faq', enabled: true, title: { fa: '', en: '' }, body: { fa: '', en: '' }, buttons: [] })
  })
  it('copies a stored entry so editing does not touch it', () => {
    const it = item({ kind: 'tutorial', buttons: [{ url: 'https://example.com', label: { fa: 'ب', en: 'B' } }] })
    const d = draftOf(it)
    d.title.en = 'Changed'
    d.buttons[0].label.en = 'Changed'
    expect(it.title.en).toBe('Question')
    expect(it.buttons[0].label.en).toBe('B')
  })
  it('knows what a language holds', () => {
    expect(langState(blankDraft('faq'), 'fa')).toBe('empty')
    expect(langState({ title: { fa: 'x', en: '' }, body: { fa: '', en: '' } }, 'fa')).toBe('half')
    expect(langState({ title: { fa: '', en: '' }, body: { fa: 'x', en: '' } }, 'fa')).toBe('half')
    expect(langState({ title: { fa: ' x ', en: '' }, body: { fa: 'y', en: '' } }, 'fa')).toBe('complete')
    expect(langState({ title: { fa: '  ', en: '' }, body: { fa: 'y', en: '' } }, 'fa')).toBe('half')
  })
  it('tidies a line', () => {
    expect(tidyLine('  a \n  b\t c ')).toBe('a b c')
  })
  it('compares drafts the way they are sent', () => {
    const a = draftOf(item())
    const b = draftOf(item())
    expect(sameDraft(a, b)).toBe(true)
    b.title.fa = ' سوال  '
    expect(sameDraft(a, b)).toBe(true)
    b.title.fa = 'دیگر'
    expect(sameDraft(a, b)).toBe(false)
    const c = draftOf(item())
    c.enabled = false
    expect(sameDraft(a, c)).toBe(false)
  })
})

describe('help: local problems', () => {
  it('accepts a complete entry in one language', () => {
    expect(localProblems({ ...blankDraft('faq'), title: { fa: '', en: 'Q' }, body: { fa: '', en: 'A' } })).toEqual([])
  })
  it('wants one language', () => {
    expect(localProblems(blankDraft('faq'))).toEqual([{ field: '', code: 'need_one_language', arg: '' }])
  })
  it('wants a title and a text together', () => {
    const p = localProblems({ ...blankDraft('faq'), title: { fa: 'x', en: 'Q' }, body: { fa: '', en: 'A' } })
    expect(p).toEqual([{ field: 'body.fa', code: 'pair_missing', arg: 'body' }])
    const q = localProblems({ ...blankDraft('faq'), title: { fa: '', en: 'Q' }, body: { fa: 'y', en: 'A' } })
    expect(q).toEqual([{ field: 'title.fa', code: 'pair_missing', arg: 'title' }])
  })
  it('limits a title by kind and a body by units without tags', () => {
    const long = { ...blankDraft('faq'), title: { fa: '', en: 'x'.repeat(DEFAULT_LIMITS.question + 1) }, body: { fa: '', en: 'a' } }
    expect(localProblems(long)).toEqual([{ field: 'title.en', code: 'too_long', arg: String(DEFAULT_LIMITS.question) }])
    const tut = { ...blankDraft('tutorial'), title: { fa: '', en: 'x'.repeat(DEFAULT_LIMITS.question) }, body: { fa: '', en: 'a' } }
    expect(localProblems(tut)[0]).toMatchObject({ field: 'title.en', code: 'too_long', arg: String(DEFAULT_LIMITS.tutorial_title) })
    // tags do not count, an exact fit passes, one more fails
    const tagged = '<b>' + 'a'.repeat(DEFAULT_LIMITS.body) + '</b>'
    const ok = { ...blankDraft('faq'), title: { fa: '', en: 'Q' }, body: { fa: '', en: tagged } }
    expect(localProblems(ok)).toEqual([])
    const over = { ...ok, body: { fa: '', en: tagged + 'a' } }
    expect(localProblems(over)).toEqual([{ field: 'body.en', code: 'too_long', arg: String(DEFAULT_LIMITS.body) }])
  })
  it('allows no buttons on a question', () => {
    const d = { ...blankDraft('faq'), title: { fa: '', en: 'Q' }, body: { fa: '', en: 'A' }, buttons: [{ url: 'https://example.com', label: { fa: '', en: 'x' } }] }
    expect(localProblems(d).map((p) => p.code)).toEqual(['buttons_not_allowed'])
  })
  it('checks a tutorial button address and label', () => {
    const ok = tutorial({ buttons: [{ url: 'https://example.com', label: { fa: '', en: 'Get' } }] })
    expect(localProblems(ok)).toEqual([])
    const bad = tutorial({
      buttons: [
        { url: '', label: { fa: 'x', en: '' } },
        { url: 'http://example.com', label: { fa: 'x', en: '' } },
        { url: 'https://example.com', label: { fa: '', en: '' } },
        { url: 'https://example.com', label: { fa: '', en: 'y'.repeat(DEFAULT_LIMITS.button_label + 1) } },
      ],
    })
    expect(localProblems(bad)).toEqual([
      { field: 'buttons.0.url', code: 'url_missing', arg: '' },
      { field: 'buttons.1.url', code: 'bad_url', arg: 'http://example.com' },
      { field: 'buttons.2.label.fa', code: 'label_missing', arg: '' },
      { field: 'buttons.3.label.en', code: 'too_long', arg: String(DEFAULT_LIMITS.button_label) },
    ])
  })
  it('limits the number of buttons', () => {
    const buttons = Array.from({ length: DEFAULT_LIMITS.buttons + 1 }, () => ({ url: 'https://example.com', label: { fa: '', en: 'x' } }))
    expect(localProblems(tutorial({ buttons }))).toEqual([{ field: 'buttons', code: 'too_many_buttons', arg: String(DEFAULT_LIMITS.buttons) }])
  })
  it('finds problems by field and by prefix', () => {
    const ps = [
      { field: 'buttons.1.url', code: 'bad_url', arg: '' },
      { field: 'buttons.1.label.fa', code: 'label_missing', arg: '' },
      { field: 'buttons.10.url', code: 'bad_url', arg: '' },
      { field: 'title.fa', code: 'too_long', arg: '1' },
    ]
    expect(problemsAt(ps, 'title.fa')).toHaveLength(1)
    expect(problemsUnder(ps, 'buttons.1')).toHaveLength(2)
    expect(problemsUnder(ps, 'buttons')).toHaveLength(3)
  })
})

describe('help: what the bot shows', () => {
  const btn = { url: 'https://example.com/app', label: { fa: 'دانلود', en: 'Download' } }
  it('reads in the customer language', () => {
    const r = readIn({ title: { fa: ' عنوان ', en: 'T' }, body: { fa: 'متن', en: 'text' }, buttons: [btn] }, 'fa')
    expect(r).toEqual({ lang: 'fa', title: 'عنوان', body: 'متن', buttons: [{ label: 'دانلود', url: btn.url }] })
  })
  it('falls back to the other language when theirs is missing or half written', () => {
    const d = { title: { fa: 'only title', en: 'T' }, body: { fa: '', en: 'text' }, buttons: [] }
    expect(readIn(d, 'fa')?.lang).toBe('en')
    expect(readIn({ title: { fa: '', en: '' }, body: { fa: '', en: '' }, buttons: [] }, 'fa')).toBeNull()
    expect(readIn({ title: { fa: 'x', en: '' }, body: { fa: '', en: 'y' }, buttons: [] }, 'en')).toBeNull()
  })
  it('takes the other label of a button and drops one it cannot use', () => {
    const d = {
      title: { fa: 't', en: 't' },
      body: { fa: 'b', en: 'b' },
      buttons: [
        { url: 'https://example.com', label: { fa: '', en: 'Only English' } },
        { url: 'http://example.com', label: { fa: 'x', en: 'x' } },
        { url: 'https://example.com', label: { fa: '', en: '' } },
      ],
    }
    expect(readIn(d, 'fa')?.buttons).toEqual([{ label: 'Only English', url: 'https://example.com' }])
  })
  it('sizes the FAQ the way the bot chooses between a message and a list', () => {
    const items = [
      item({ id: '1' }),
      item({ id: '2', enabled: false }),
      item({ id: '3', kind: 'tutorial' }),
      item({ id: '4', title: { fa: '', en: '' }, body: { fa: '', en: '' } }),
      item({ id: '5', title: { fa: '', en: 'QQ' }, body: { fa: '', en: '<b>AAA</b>' } }),
    ]
    expect(faqSize(items, 'en')).toEqual({ entries: 2, units: 'Question'.length + 'Answer'.length + 2 + 3 })
    // a Persian customer reads the first one in Persian, the fifth in English
    expect(faqSize(items, 'fa')).toEqual({ entries: 2, units: 'سوال'.length + 'پاسخ'.length + 2 + 3 })
    expect(faqSize([], 'en')).toEqual({ entries: 0, units: 0 })
  })
  it('gets the first line of a text without tags', () => {
    expect(firstLine('\n  <b>One</b> two\nthree')).toBe('One two')
    expect(firstLine('')).toBe('')
  })
})

describe('help: ordering and requests', () => {
  it('moves an id one place', () => {
    const ids = ['a', 'b', 'c']
    expect(move(ids, 'b', -1)).toEqual(['b', 'a', 'c'])
    expect(move(ids, 'b', 1)).toEqual(['a', 'c', 'b'])
    expect(move(ids, 'a', -1)).toEqual(ids)
    expect(move(ids, 'c', 1)).toEqual(ids)
    expect(move(ids, 'z', 1)).toEqual(ids)
    expect(ids).toEqual(['a', 'b', 'c'])
  })
  it('sends a draft the way core takes it', () => {
    const d = tutorial({ title: { fa: ' ع  ', en: 'T' }, buttons: [{ url: ' https://example.com ', label: { fa: ' د ', en: '' } }] })
    expect(requestOf(d)).toEqual({
      kind: 'tutorial',
      enabled: true,
      title: { fa: 'ع', en: 'T' },
      body: { fa: 'متن', en: 'Text' },
      buttons: [{ url: 'https://example.com', label: { fa: 'د', en: '' } }],
    })
  })
  it('never sends buttons with a question', () => {
    const d = { ...blankDraft('faq'), buttons: [newButton()] }
    expect(requestOf(d).buttons).toEqual([])
  })
})

describe('help: locale', () => {
  const keys = (o: unknown, p = ''): string[] =>
    o && typeof o === 'object' ? Object.entries(o).flatMap(([k, v]) => keys(v, p ? `${p}.${k}` : k)) : [p]
  it('has the same help keys in Persian and English', () => {
    expect(keys((fa as Record<string, unknown>).help).sort()).toEqual(keys((en as Record<string, unknown>).help).sort())
  })
})
