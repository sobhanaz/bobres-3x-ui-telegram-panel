import { afterEach, describe, expect, it, vi } from 'vitest'
import { palette, updatePrimaryPalette } from '@primeuix/themes'
import {
  brandingField,
  contrastWithWhite,
  filterTexts,
  insertAt,
  isChanged,
  isHttpsUrl,
  logoProblem,
  logoSizeOk,
  normalizeHex,
  parseImport,
  parsePreview,
  problemArg,
  sampleValue,
  textLength,
  type PreviewNode,
} from '../branding'
import { money, setCurrencyNames } from '../format'
import { applyBrandColor, applyDocumentBrand } from '../theme'
import type { TextItem } from '../types'

// The palette update needs a mounted PrimeVue; here we only watch the calls.
vi.mock('@primeuix/themes', async (importOriginal) => {
  const mod = await importOriginal<typeof import('@primeuix/themes')>()
  return { ...mod, updatePrimaryPalette: vi.fn() }
})

describe('brand colour', () => {
  it('reads typed and picked colours', () => {
    expect(normalizeHex('0F766E')).toBe('#0f766e')
    expect(normalizeHex(' #14B8A6 ')).toBe('#14b8a6')
    expect(normalizeHex('#14b8a')).toBe('')
    expect(normalizeHex('teal')).toBe('')
    expect(normalizeHex('')).toBe('')
  })
  it('measures white text on the colour (WCAG)', () => {
    expect(contrastWithWhite('#ffffff')).toBeCloseTo(1, 5)
    expect(contrastWithWhite('#000000')).toBeCloseTo(21, 5)
    expect(contrastWithWhite('#767676')).toBeCloseTo(4.54, 2) // the lightest grey that passes AA
    expect(contrastWithWhite('#14b8a6')).toBeLessThan(4.5) // the default teal is light
    expect(contrastWithWhite('#0f766e')).toBeGreaterThan(4.5)
    expect(contrastWithWhite('#facc15')).toBeLessThan(2) // yellow
  })
  it('applies a colour once and resets to teal', () => {
    const update = vi.mocked(updatePrimaryPalette)
    update.mockClear()
    applyBrandColor('#0F766E')
    expect(update).toHaveBeenCalledTimes(1)
    expect(update).toHaveBeenLastCalledWith(palette('#0f766e'))
    applyBrandColor('#0f766e') // same colour: nothing to redo
    expect(update).toHaveBeenCalledTimes(1)
    applyBrandColor('not a colour')
    expect(update).toHaveBeenCalledTimes(2)
    expect(update).toHaveBeenLastCalledWith(palette('{teal}'))
    applyBrandColor(undefined) // already teal
    applyBrandColor('')
    expect(update).toHaveBeenCalledTimes(2)
  })
  it('names the browser tab and uses the logo as its icon', () => {
    document.head.innerHTML = '<link rel="icon" type="image/svg+xml" href="/admin/favicon.svg" />'
    const link = document.querySelector('link[rel="icon"]')!
    applyDocumentBrand('Shop · Dashboard', '/api/v1/brand/logo?v=1')
    expect(document.title).toBe('Shop · Dashboard')
    expect(link.getAttribute('href')).toBe('/api/v1/brand/logo?v=1')
    expect(link.hasAttribute('type')).toBe(false)
    applyDocumentBrand('BOBRES · Dashboard', null)
    expect(link.getAttribute('href')).toBe(import.meta.env.BASE_URL + 'favicon.svg')
    expect(link.getAttribute('type')).toBe('image/svg+xml')
    document.head.innerHTML = ''
  })
})

describe('branding form', () => {
  it('accepts https links only', () => {
    expect(isHttpsUrl('https://example.com/terms')).toBe(true)
    expect(isHttpsUrl('http://example.com')).toBe(false)
    expect(isHttpsUrl('https://user:pw@example.com')).toBe(false)
    expect(isHttpsUrl('example.com')).toBe(false)
    expect(isHttpsUrl('https://exa mple.com')).toBe(false)
    expect(isHttpsUrl('javascript:alert(1)')).toBe(false)
  })
  it('maps the field core names to the form', () => {
    expect(brandingField('branding.terms_url')).toBe('terms_url')
    expect(brandingField('color')).toBe('color')
    expect(brandingField('branding.currency.fa')).toBe('currency.fa')
    expect(brandingField('currency_en')).toBe('currency.en')
  })
  it('checks logo files before sending them', () => {
    expect(logoProblem({ type: 'image/png', size: 100_000 })).toBe('')
    expect(logoProblem({ type: 'image/webp', size: 512 * 1024 })).toBe('')
    expect(logoProblem({ type: 'image/png', size: 512 * 1024 + 1 })).toBe('too_large')
    expect(logoProblem({ type: 'image/svg+xml', size: 1000 })).toBe('bad_type')
    expect(logoProblem({ type: 'image/gif', size: 1000 })).toBe('bad_type')
    expect(logoSizeOk(512, 512)).toBe(true)
    expect(logoSizeOk(15, 512)).toBe(false)
    expect(logoSizeOk(2048, 2049)).toBe(false)
  })
})

describe('Toman name', () => {
  afterEach(() => setCurrencyNames({}))
  it('writes the store name for Toman, per language', () => {
    setCurrencyNames({ fa: 'ت', en: ' T ' })
    expect(money({ amount: 150000, currency: 'IRT' }, 'fa')).toBe('۱۵۰٬۰۰۰ ت')
    expect(money({ amount: 150000, currency: 'IRT' }, 'en')).toBe('150,000 T')
    expect(money({ amount: 2_500_000, currency: 'USDT' }, 'en')).toBe('2.5 USDT') // other currencies keep theirs
    setCurrencyNames({ fa: '' })
    expect(money({ amount: 150000, currency: 'IRT' }, 'fa')).toBe('۱۵۰٬۰۰۰ تومان')
    expect(money({ amount: 150000, currency: 'IRT' }, 'en')).toBe('150,000 Toman')
  })
})

const item = (key: string, fa: string, en: string, over: Partial<TextItem> = {}): TextItem => ({
  key,
  group: key.split('.')[0],
  context: 'html',
  placeholders: [],
  max: 3500,
  langs: ['fa', 'en'],
  default: { fa, en },
  override: { fa: '', en: '' },
  ...over,
})

describe('text list', () => {
  const items = [
    item('welcome', 'به {brand} خوش آمدید', 'Welcome to {brand}'),
    item('btn.buy', 'خرید سرویس', 'Buy a plan', { context: 'button', override: { fa: 'خرید', en: '' } }),
    item('error.rate', 'درخواست‌های شما زیاد است', 'Too many requests', { group: 'general' }),
  ]
  it('finds texts by key or text, forgiving Persian spelling', () => {
    expect(filterTexts(items, { q: 'BTN.', group: '', changed: false }).map((i) => i.key)).toEqual(['btn.buy'])
    expect(filterTexts(items, { q: 'welcome to', group: '', changed: false }).map((i) => i.key)).toEqual(['welcome'])
    expect(filterTexts(items, { q: 'خريد', group: '', changed: false }).map((i) => i.key)).toEqual(['btn.buy']) // Arabic ye
    expect(filterTexts(items, { q: 'درخواست های', group: '', changed: false }).map((i) => i.key)).toEqual(['error.rate']) // no half-space
  })
  it('filters by group and by change', () => {
    expect(filterTexts(items, { q: '', group: 'general', changed: false }).map((i) => i.key)).toEqual(['error.rate'])
    expect(filterTexts(items, { q: '', group: '', changed: true }).map((i) => i.key)).toEqual(['btn.buy'])
    expect(isChanged(items[0])).toBe(false)
    expect(isChanged(items[1])).toBe(true)
  })
})

describe('text editing', () => {
  it('counts like Telegram: UTF-16, no tags, entities as one', () => {
    expect(textLength('<b>Hi</b> &amp; bye')).toBe(8)
    expect(textLength('👋 hi')).toBe(5) // the emoji is two UTF-16 units
    expect(textLength('  salam\r\n ')).toBe(5)
    expect(textLength('سلام')).toBe(4)
    // A numeric entity is the character it stands for, as core counts it.
    expect(textLength('&#128512;&#x41;')).toBe(3)
    expect(textLength('&#38;lt;')).toBe(4) // decoded once: "&lt;
  })
  it('inserts a placeholder at the cursor', () => {
    expect(insertAt('Hello !', 6, 6, '{name}')).toEqual({ value: 'Hello {name}!', cursor: 12 })
    expect(insertAt('Hello XX!', 6, 8, '{name}')).toEqual({ value: 'Hello {name}!', cursor: 12 })
    expect(insertAt('ab', 99, 99, '{x}')).toEqual({ value: 'ab{x}', cursor: 5 })
  })
  it('shows placeholders and tags in problems as they are written', () => {
    expect(problemArg({ code: 'placeholder_missing', arg: 'brand' })).toBe('{brand}')
    expect(problemArg({ code: 'placeholder_unknown', arg: '{ name }' })).toBe('{ name }')
    expect(problemArg({ code: 'too_long', arg: '64' })).toBe('64')
    expect(problemArg({ code: 'tag_not_allowed', arg: 'div' })).toBe('<div>')
    expect(problemArg({ code: 'tag_mismatch', arg: 'b' })).toBe('</b>')
    expect(problemArg({ code: 'control_char', arg: '' })).toBe('')
  })
})

describe('import file', () => {
  it('reads an export and a plain object', () => {
    expect(parseImport('﻿{"lang":"en","texts":{"welcome":"Hi {brand}"}}')).toEqual({ ok: true, lang: 'en', texts: { welcome: 'Hi {brand}' } })
    expect(parseImport('{"welcome":"سلام","btn.buy":"خرید"}')).toEqual({ ok: true, lang: null, texts: { welcome: 'سلام', 'btn.buy': 'خرید' } })
    expect(parseImport('{"texts":{"welcome":"x"}}')).toEqual({ ok: true, lang: null, texts: { welcome: 'x' } })
  })
  it('refuses what it cannot use', () => {
    expect(parseImport('{nope')).toEqual({ ok: false, error: 'not_json' })
    expect(parseImport('[1,2]')).toEqual({ ok: false, error: 'shape' })
    expect(parseImport('{"welcome":5}')).toEqual({ ok: false, error: 'shape' })
    expect(parseImport('{"lang":"de","texts":{"welcome":"x"}}')).toEqual({ ok: false, error: 'shape' })
    expect(parseImport('{}')).toEqual({ ok: false, error: 'empty' })
    expect(parseImport('{"lang":"fa","texts":{}}')).toEqual({ ok: false, error: 'empty' })
  })
})

describe('preview', () => {
  const p = (s: string, ph: string[] = [], html = true) => parsePreview(s, html, ph)
  const text = (t: string): PreviewNode => ({ type: 'text', text: t })

  it('turns the allowed tags into elements', () => {
    expect(p('Hi <b>bold <i>both</i></b>!')).toEqual([
      text('Hi '),
      { type: 'el', tag: 'b', children: [text('bold '), { type: 'el', tag: 'i', children: [text('both')] }] },
      text('!'),
    ])
    expect(p('<strong>a</strong><em>b</em><ins>c</ins><del>d</del><strike>e</strike>').map((n) => (n.type === 'el' ? n.tag : n.type))).toEqual(['b', 'i', 'u', 's', 's'])
    expect(p('<tg-spoiler>x</tg-spoiler><span class="tg-spoiler">y</span>').map((n) => (n.type === 'el' ? n.tag : ''))).toEqual(['spoiler', 'spoiler'])
    expect(p('<blockquote expandable>q</blockquote><pre><code>c</code></pre>')).toEqual([
      { type: 'el', tag: 'blockquote', children: [text('q')] },
      { type: 'el', tag: 'pre', children: [{ type: 'el', tag: 'code', children: [text('c')] }] },
    ])
  })
  it('keeps links as text with their address', () => {
    expect(p('<a href="https://t.me/shop">join</a>')).toEqual([{ type: 'el', tag: 'a', href: 'https://t.me/shop', children: [text('join')] }])
    expect(p('<a href="javascript:alert(1)">x</a>')).toEqual([text('<a href="javascript:alert(1)">x</a>')])
  })
  it('shows anything else literally', () => {
    expect(p('<script>alert(1)</script>')).toEqual([text('<script>alert(1)</script>')])
    expect(p('<img src=x onerror=alert(1)>')).toEqual([text('<img src=x onerror=alert(1)>')])
    expect(p('<b class="x">a</b>')).toEqual([text('<b class="x">a</b>')])
    expect(p('<span>a</span>')).toEqual([text('<span>a</span>')])
    expect(p('a < b & c')).toEqual([text('a < b & c')])
    expect(p('<b>a</i></b>')).toEqual([{ type: 'el', tag: 'b', children: [text('a</i>')] }])
    expect(p('<b>open')).toEqual([{ type: 'el', tag: 'b', children: [text('open')] }])
  })
  it('decodes the entities Telegram knows', () => {
    expect(p('&lt;b&gt; &amp; &quot;x&quot; &#1587; &#x1F44B; &nbsp;')).toEqual([text('<b> & "x" س 👋 &nbsp;')])
  })
  it('marks the placeholders the text may use', () => {
    expect(p('Hi {brand}, {nope} {Brand}', ['brand'])).toEqual([text('Hi '), { type: 'ph', name: 'brand' }, text(', {nope} {Brand}')])
    expect(p('<code>{link}</code>', ['link'])).toEqual([{ type: 'el', tag: 'code', children: [{ type: 'ph', name: 'link' }] }])
  })
  it('keeps tags as text where Telegram shows plain text', () => {
    expect(p('<b>{name}</b> & co', ['name'], false)).toEqual([text('<b>'), { type: 'ph', name: 'name' }, text('</b> & co')])
  })
  it('has sample values in the text language', () => {
    expect(sampleValue('amount', 'fa')).toBe('۱۵۰٬۰۰۰ تومان')
    expect(sampleValue('amount', 'en')).toBe('150,000 Toman')
    expect(sampleValue('brand', 'en', 'My Shop')).toBe('My Shop')
    expect(sampleValue('unknown_name', 'en')).toBe('unknown_name')
  })
})

// The page itself, booted like a browser with a fake core.
describe('Branding page', () => {
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const me = {
    user: { id: 'u1', telegram_id: 1001, username: 'boss', role: 'owner', language: 'fa' },
    permissions: ['overview.read', 'branding.write'],
    csrf: 'csrf-1',
    session: { method: 'link', expires_at: 2_000_000_000 },
    password: { enabled: false },
    password_available: true,
    brand: 'Shop',
    branding: { color: '#0f766e', logo: null, currency: { fa: '', en: 'T' } },
  }
  const welcome = {
    key: 'welcome', group: 'general', context: 'html', placeholders: ['brand'], max: 3500, langs: ['fa', 'en'],
    default: { fa: 'به <b>{brand}</b> خوش آمدید', en: 'Welcome to <b>{brand}</b>' }, override: { fa: '', en: '' },
  }
  const branding = {
    name: 'Shop', support: '@help', color: '#0f766e', terms_url: '', privacy_url: '', currency: { fa: '', en: 'T' }, logo: null,
    defaults: { name: 'BOBRES', color: '#14b8a6', currency: { fa: 'تومان', en: 'Toman' } },
  }

  afterEach(() => {
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
  })

  /** boot starts the app at path with a fake core; answer may override replies. */
  async function boot(path: string, answer: (url: string, init?: RequestInit) => Response | undefined = () => undefined) {
    vi.resetModules()
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    const calls: string[] = []
    // jsdom has no layout APIs; PrimeVue's tabs, selects and text areas ask for them.
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        calls.push(`${init?.method ?? 'GET'} ${url} ${typeof init?.body === 'string' ? init.body : ''}`.trim())
        const own = answer(url, init)
        if (own) return own
        if (url === '/api/v1/me') return json(200, me)
        if (url === '/api/v1/texts') return json(200, { languages: ['fa', 'en'], items: [welcome] })
        if (url === '/api/v1/branding') return json(200, branding)
        if (url === '/api/v1/texts/check') return json(200, { ok: false, value: '', problems: [{ code: 'placeholder_missing', arg: 'brand' }], length: 9, max: 3500 })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { calls, router }
  }

  it('lists bot texts and previews an edit without running its markup', async () => {
    const { calls } = await boot('/admin/branding?tab=texts')

    // The texts tab loads by itself; the store tab waits until it is opened.
    await vi.waitFor(() => expect(document.querySelector('.p-datatable-tbody')?.textContent).toContain('welcome'))
    expect(calls).toContain('GET /api/v1/texts')
    expect(calls).not.toContain('GET /api/v1/branding')
    const fmt = await import('../format') // the app's own copy after resetModules
    expect(fmt.money({ amount: 5000, currency: 'IRT' }, 'en')).toBe('5,000 T') // /me's Toman name applied

    // A row opens the editor with the default texts.
    ;(document.querySelector('.p-datatable-tbody td') as HTMLElement).click()
    await vi.waitFor(() => expect(document.querySelectorAll('textarea')).toHaveLength(2))
    const en = document.querySelector('textarea[lang="en"]') as HTMLTextAreaElement
    expect(en.value).toBe('Welcome to <b>{brand}</b>')
    const preview = () => document.querySelector('.preview[lang="en"]') as HTMLElement
    expect(preview().querySelector('b')?.textContent).toBe('Shop') // {brand} shows the store name

    en.value = 'Hi <script>alert(1)</script> <i>there</i>'
    en.dispatchEvent(new Event('input'))
    await vi.waitFor(() => expect(preview().textContent).toContain('<script>alert(1)</script>'))
    expect(preview().querySelector('script')).toBeNull()
    expect(preview().querySelector('i')?.textContent).toBe('there')

    // The live check runs once typing pauses and its problems are listed.
    await vi.waitFor(() => expect(calls.some((c) => c.startsWith('POST /api/v1/texts/check'))).toBe(true), { timeout: 2000 })
    expect(calls.find((c) => c.startsWith('POST /api/v1/texts/check'))).toContain('"lang":"en","key":"welcome"')
    await vi.waitFor(() => expect(document.querySelectorAll('.problems li')).toHaveLength(1))
  })

  it('edits the store details, previews the colour and shows field errors', async () => {
    const update = vi.mocked(updatePrimaryPalette)
    let saved = ''
    const { calls } = await boot('/admin/branding', (url, init) => {
      if (url === '/api/v1/branding' && init?.method === 'PUT') {
        saved = String(init.body)
        return json(400, { error: 'invalid', message: 'support: one line, at most 64 characters', field: 'branding.support' })
      }
    })
    await vi.waitFor(() => expect(document.querySelector('input[placeholder="#14b8a6"]')).not.toBeNull())
    expect(calls).not.toContain('GET /api/v1/texts') // the texts tab waits until it is opened
    const color = document.querySelector('input[placeholder="#14b8a6"]') as HTMLInputElement
    expect(color.value).toBe('#0f766e')

    // A light colour previews at once and warns about white text on it.
    update.mockClear()
    color.value = '#fde047'
    color.dispatchEvent(new Event('input'))
    await vi.waitFor(() => expect(update).toHaveBeenLastCalledWith(palette('#fde047')))
    expect(document.querySelector('.p-message-warn')).not.toBeNull()

    // A link that is not https is refused before sending.
    const terms = document.querySelectorAll('input[type="url"]')[0] as HTMLInputElement
    terms.value = 'http://example.com'
    terms.dispatchEvent(new Event('input'))
    await vi.waitFor(() => expect((document.querySelector('button[type="submit"]') as HTMLButtonElement).disabled).toBe(true))
    terms.value = 'https://example.com/terms'
    terms.dispatchEvent(new Event('input'))
    await vi.waitFor(() => expect((document.querySelector('button[type="submit"]') as HTMLButtonElement).disabled).toBe(false))

    // Core's field error shows under that field.
    ;(document.querySelector('button[type="submit"]') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(saved).not.toBe(''))
    expect(JSON.parse(saved)).toEqual({
      name: 'Shop', support: '@help', color: '#fde047', terms_url: 'https://example.com/terms', privacy_url: '',
      currency: { fa: '', en: 'T' },
    })
    await vi.waitFor(() => expect(document.body.textContent).toContain('support: one line, at most 64 characters'))
    const supportField = (document.querySelector('input[placeholder="@support"]') as HTMLElement).closest('.app-field')!
    expect(supportField.textContent).toContain('support: one line, at most 64 characters')

    // Leaving the page puts the saved colour back.
    update.mockClear()
    const { router } = await import('../router')
    await router.push({ name: 'overview' })
    await vi.waitFor(() => expect(update).toHaveBeenLastCalledWith(palette('#0f766e')))
  })

  it('refuses a logo over 512 KB without sending it', async () => {
    const { calls } = await boot('/admin/branding')
    const logoInput = 'input[type="file"][accept="image/png,image/jpeg,image/webp"]'
    await vi.waitFor(() => expect(document.querySelector(logoInput)).not.toBeNull())
    const input = document.querySelector(logoInput) as HTMLInputElement
    const big = new File([new Uint8Array(512 * 1024 + 1)], 'big.png', { type: 'image/png' })
    Object.defineProperty(input, 'files', { value: [big], configurable: true })
    input.dispatchEvent(new Event('change'))
    await vi.waitFor(() => expect(document.querySelector('.p-message-error')).not.toBeNull())
    expect(calls.some((c) => c.includes('/branding/logo'))).toBe(false)
  })
})
