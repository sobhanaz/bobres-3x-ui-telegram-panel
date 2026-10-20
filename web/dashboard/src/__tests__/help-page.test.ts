import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { DEFAULT_LIMITS } from '../help'
import type { HelpItem, HelpPage, HelpProblem } from '../types'

// The Help content tab of the Branding page, booted like a browser against a
// fake core that keeps the lists, checks versions as core does and answers the
// live check. Most tests read the page in English (the dashboard's language
// is the browser's setting, kept in localStorage); one reads it in Persian.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

function entry(id: string, kind: 'faq' | 'tutorial', sort: number, title: { fa: string; en: string }, over: Partial<HelpItem> = {}): HelpItem {
  return {
    id, kind, sort, title, version: 1, enabled: true, updated_at: 1_790_000_000, buttons: [],
    body: { fa: title.fa ? 'پاسخ ' + title.fa : '', en: title.en ? 'Answer to ' + title.en : '' },
    ...over,
  }
}

const q1 = () => entry('q1', 'faq', 1, { fa: 'آیا آی‌پی اختصاصی می‌گیرم؟', en: 'Will I get my own IP address?' })
const q2 = () => entry('q2', 'faq', 2, { fa: '', en: 'How do I renew?' }, { enabled: false })
const t1 = () =>
  entry('t1', 'tutorial', 1, { fa: 'دانلود برنامه', en: 'Download the app' }, { buttons: [{ url: 'https://example.com/get', label: { fa: 'دانلود', en: 'Download' } }] })

describe('Help content tab', () => {
  const me = (role: string, permissions: string[]) => ({
    user: { id: 'u1', telegram_id: 1001, username: 'boss', role, language: 'fa' },
    permissions,
    csrf: 'csrf-1',
    session: { method: 'link', expires_at: 2_000_000_000 },
    password: { enabled: false },
    password_available: true,
    brand: 'Shop',
    branding: { color: '#0f766e', logo: null, currency: { fa: '', en: 'T' } },
  })

  /** The fake core: the lists as core keeps them, and the calls it was given. */
  class Core {
    faq: HelpItem[] = [q1(), q2()]
    tutorials: HelpItem[] = [t1()]
    calls: string[] = []
    /** problems the live check answers for a body: by a marker the test types */
    staleNext = false
    layout: HelpPage['faq_layout'] = { fa: 'message', en: 'message' }
    next = 1
    limits = { ...DEFAULT_LIMITS }
    page(): HelpPage {
      return { faq: this.faq, tutorials: this.tutorials, faq_layout: this.layout, limits: this.limits, languages: ['fa', 'en'] }
    }
    list(kind: string) {
      return kind === 'faq' ? this.faq : this.tutorials
    }
    find(id: string) {
      return [...this.faq, ...this.tutorials].find((i) => i.id === id)
    }
    checkProblems(body: { body: { fa: string; en: string } }): HelpProblem[] {
      const open = /<(\w+)>[^<]*$/.exec(body.body.en)
      return open ? [{ field: 'body.en', code: 'tag_unclosed', arg: open[1] }] : []
    }
    answer(url: string, init?: RequestInit): Response | undefined {
      const method = init?.method ?? 'GET'
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
      if (url === '/api/v1/help' && method === 'GET') return json(200, this.page())
      if (url === '/api/v1/help/check') {
        const problems = this.checkProblems(body)
        return json(200, { ok: problems.length === 0, problems })
      }
      if (url === '/api/v1/help' && method === 'POST') {
        const item = entry('n' + this.next++, body.kind, this.list(body.kind).length + 1, body.title, { body: body.body, buttons: body.buttons, enabled: body.enabled })
        this.list(body.kind).push(item)
        return json(200, { item })
      }
      if (url === '/api/v1/help/order') {
        const list = this.list(body.kind)
        const ordered = (body.ids as string[]).map((id) => list.find((i) => i.id === id)!)
        ordered.forEach((it, i) => (it.sort = i + 1))
        if (body.kind === 'faq') this.faq = ordered
        else this.tutorials = ordered
        return json(200, this.page())
      }
      if (url === '/api/v1/help/defaults') {
        this.list(body.kind).push(entry('d' + this.next++, body.kind, 1, { fa: 'پیش‌فرض', en: 'A default' }))
        return json(200, this.page())
      }
      const enabled = /^\/api\/v1\/help\/([^/]+)\/enabled$/.exec(url)
      if (enabled && method === 'PUT') {
        const it = this.find(enabled[1])!
        it.enabled = body.enabled
        it.version++
        return json(200, { item: it })
      }
      const one = /^\/api\/v1\/help\/([^/]+)$/.exec(url)
      if (one && method === 'PUT') {
        const it = this.find(one[1])!
        if (this.staleNext || body.version !== it.version) {
          this.staleNext = false
          return json(409, { error: 'state', message: 'domain: not possible in the current state: someone changed this entry meanwhile; reload it' })
        }
        Object.assign(it, { title: body.title, body: body.body, buttons: body.buttons, enabled: body.enabled, version: it.version + 1 })
        return json(200, { item: it })
      }
      if (one && method === 'DELETE') {
        this.faq = this.faq.filter((i) => i.id !== one[1])
        this.tutorials = this.tutorials.filter((i) => i.id !== one[1])
        return new Response(null, { status: 204 })
      }
      return undefined
    }
    sent(method: string, path: string): Record<string, unknown>[] {
      return this.calls
        .filter((c) => c.startsWith(`${method} ${path} `) || c === `${method} ${path}`)
        .map((c) => (c.includes('{') ? (JSON.parse(c.slice(c.indexOf('{'))) as Record<string, unknown>) : {}))
    }
    count(method: string, path: string): number {
      return this.calls.filter((c) => c.startsWith(`${method} ${path}`)).length
    }
  }

  afterEach(() => {
    // stop this test's app: PrimeVue's confirmation bus is shared between tests
    ;(document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
    vi.unstubAllGlobals()
    try {
      localStorage.removeItem('bobres.lang')
    } catch {
      /* no storage in this environment */
    }
    document.body.innerHTML = ''
  })

  /** boot starts the app at path with the fake core, in a language. */
  async function boot(core: Core, lang: 'en' | 'fa' = 'en', role = 'owner', permissions = ['overview.read', 'branding.write'], path = '/admin/branding?tab=help') {
    vi.resetModules()
    localStorage.setItem('bobres.lang', lang)
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    // jsdom has no layout APIs; PrimeVue's tabs, selects and text areas ask for them.
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        core.calls.push(`${init?.method ?? 'GET'} ${url} ${typeof init?.body === 'string' ? init.body : ''}`.trim())
        const own = core.answer(url, init)
        if (own) return own
        if (url === '/api/v1/me') return json(200, me(role, permissions))
        if (url === '/api/v1/branding') return json(200, { name: 'Shop', support: '', color: '#0f766e', terms_url: '', privacy_url: '', currency: { fa: '', en: 'T' }, logo: null, defaults: { name: 'BOBRES', color: '#14b8a6', currency: { fa: 'تومان', en: 'Toman' } } })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { router }
  }

  const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
  const section = (kind: 'faq' | 'tutorial') => document.querySelector(`section[aria-labelledby="help-${kind}"]`) as HTMLElement
  const buttonIn = (root: ParentNode, label: string) => {
    const hit = [...root.querySelectorAll('button')].find((b) => text(b) === label || b.getAttribute('aria-label') === label)
    if (!hit) throw new Error(`no button "${label}" in: ${[...root.querySelectorAll('button')].map((b) => text(b) || b.getAttribute('aria-label')).join(' | ')}`)
    return hit as HTMLButtonElement
  }
  const labelled = (prefix: string, contains: string) => document.querySelector(`[aria-label^="${prefix}"][aria-label*="${contains}"]`) as HTMLElement | null
  const dialog = () => document.querySelector('.p-dialog') as HTMLElement
  async function type(el: HTMLInputElement | HTMLTextAreaElement, value: string) {
    el.value = value
    el.dispatchEvent(new Event('input', { bubbles: true }))
    await nextTick()
  }
  const field = (selector: string) => dialog().querySelector(selector) as HTMLInputElement & HTMLTextAreaElement
  const loaded = async () => vi.waitFor(() => expect(document.querySelectorAll('.help-section')).toHaveLength(2))

  it('lists the questions and the tutorials with how the bot shows the FAQ', async () => {
    const core = new Core()
    core.layout = { fa: 'message', en: 'list' }
    await boot(core)
    await loaded()
    expect(core.calls).toContain('GET /api/v1/help')
    expect(document.querySelector('.p-tabs')).not.toBeNull()
    const faq = section('faq')
    expect(text(faq.querySelector('h2'))).toBe('Frequently asked questions')
    expect(text(section('tutorial').querySelector('h2'))).toBe('Tutorials')
    // a row for each entry, the two languages with their own direction, and what is off or half written
    const rows = [...faq.querySelectorAll('.p-datatable-tbody tr')]
    expect(rows).toHaveLength(2)
    expect(text(rows[0])).toContain('Will I get my own IP address?')
    const fa = rows[0].querySelector('[lang="fa"]') as HTMLElement
    expect(fa.getAttribute('dir')).toBe('rtl')
    expect(text(fa)).toBe('آیا آی‌پی اختصاصی می‌گیرم؟')
    expect(rows[0].querySelector('[lang="en"]')?.getAttribute('dir')).toBe('ltr')
    expect(text(rows[1])).toContain('Persian: not written')
    expect(text(rows[1])).toContain('English only')
    const switches = [...faq.querySelectorAll('input[role="switch"]')] as HTMLInputElement[]
    expect(switches.map((s) => s.checked)).toEqual([true, false])
    expect(text(faq)).toContain('Shown to customers: 1 of 2')
    // how the bot lays it out, per language, and the rule
    const layout = [...faq.querySelectorAll('.layout li')].map((li) => text(li))
    expect(layout[0]).toContain('Persian: one message')
    expect(layout[1]).toContain('English: a numbered list of questions')
    expect(text(faq.querySelector('.layout'))).toContain('Up to 12 questions with 3,000 characters in all')
    // the tutorial has its buttons counted
    expect(text(section('tutorial').querySelector('.p-datatable-tbody'))).toContain('1 button')
  })

  it('reads in Persian, right to left, with Persian digits', async () => {
    const core = new Core()
    await boot(core, 'fa')
    await loaded()
    expect(document.documentElement.dir).toBe('rtl')
    expect(text(section('faq').querySelector('h2'))).toBe('سوالات متداول')
    expect(text(section('tutorial').querySelector('h2'))).toBe('آموزش‌ها')
    expect(text(section('faq'))).toContain('نمایش برای مشتریان: ۱ از ۲')
    expect(text(section('faq').querySelector('.layout'))).toContain('فارسی: یک پیام')
    ;(buttonIn(section('faq'), 'افزودن سوال') as HTMLElement).click()
    await vi.waitFor(() => expect(dialog()).not.toBeNull())
    expect(field('textarea[lang="fa"]').getAttribute('dir')).toBe('rtl')
    expect(field('input[lang="fa"]').getAttribute('dir')).toBe('rtl')
    expect(field('textarea[lang="en"]').getAttribute('dir')).toBe('ltr')
    expect(text(dialog())).toContain('سوال')
    expect(text(dialog())).toContain('۰ / ۲۰۰')
  })

  it('adds a question: checks it as it is typed, previews it and saves it', async () => {
    const core = new Core()
    await boot(core)
    await loaded()
    buttonIn(section('faq'), 'Add a question').click()
    await vi.waitFor(() => expect(dialog()).not.toBeNull())
    expect(text(dialog())).toContain('New question')
    // nothing written: Save waits, and no error is shown yet
    expect(buttonIn(dialog(), 'Save').disabled).toBe(true)
    expect(dialog().querySelector('.problems')).toBeNull()

    await type(field('input[lang="en"]'), 'Do you refund?')
    expect(text(dialog())).toContain('Write both a title and a text in this language, or leave it empty.')
    expect(buttonIn(dialog(), 'Save').disabled).toBe(true)
    await type(field('textarea[lang="en"]'), 'Ask <b>support</b>.')
    await vi.waitFor(() => expect(core.sent('POST', '/api/v1/help/check').length).toBeGreaterThan(0), { timeout: 4000 })
    await vi.waitFor(() => expect(buttonIn(dialog(), 'Save').disabled).toBe(false), { timeout: 4000 })
    expect(dialog().querySelector('.problems')).toBeNull()
    const check = core.sent('POST', '/api/v1/help/check').at(-1)!
    expect(check).toMatchObject({ kind: 'faq', title: { fa: '', en: 'Do you refund?' }, body: { fa: '', en: 'Ask <b>support</b>.' }, buttons: [] })

    // the preview of each language is the bot's answer screen; the empty one says what customers of it read
    const previews = [...dialog().querySelectorAll('[data-testid="help-preview"]')]
    expect(previews).toHaveLength(2)
    expect(text(previews[1])).toContain('❓ Do you refund?')
    expect(text(previews[1])).toContain('Ask support.')
    expect([...previews[1].querySelectorAll('b')].map((b) => b.textContent)).toEqual(['Do you refund?', 'support'])
    expect(text(previews[1])).toContain('« Back')
    expect(text(previews[1])).toContain('💬 Send a message to support')
    expect(text(previews[0])).toContain('Customers whose language is Persian read the English text.')
    expect(text(previews[0])).toContain('❓ Do you refund?')

    buttonIn(dialog(), 'Save').click()
    await vi.waitFor(() => expect(core.sent('POST', '/api/v1/help')).toHaveLength(1))
    expect(core.sent('POST', '/api/v1/help')[0]).toMatchObject({ kind: 'faq', enabled: true, title: { fa: '', en: 'Do you refund?' }, body: { en: 'Ask <b>support</b>.' } })
    // the dialog closes, the list is loaded again and shows the new entry
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())
    await vi.waitFor(() => expect(text(section('faq'))).toContain('Do you refund?'))
    expect(core.count('GET', '/api/v1/help')).toBeGreaterThanOrEqual(2)
  }, 20000)

  it('says what is wrong with a text, from the page and from core, and takes the entry once it is right', async () => {
    const core = new Core()
    await boot(core)
    await loaded()
    buttonIn(section('faq'), 'Add a question').click()
    await vi.waitFor(() => expect(dialog()).not.toBeNull())
    await type(field('input[lang="fa"]'), 'سوال')
    expect(text(dialog())).toContain('Write both a title and a text in this language, or leave it empty.')
    await type(field('textarea[lang="fa"]'), 'پاسخ')
    await type(field('input[lang="en"]'), 'Question')
    await type(field('textarea[lang="en"]'), 'Answer <b>open')
    // core's word on the markup, in the staff member's language and under the field it concerns
    await vi.waitFor(() => expect(text(dialog())).toContain('is opened but never closed'), { timeout: 4000 })
    expect(buttonIn(dialog(), 'Save').disabled).toBe(true)
    const bodyEn = field('textarea[lang="en"]')
    expect(bodyEn.getAttribute('aria-invalid')).toBe('true')
    await type(bodyEn, 'Answer <b>closed</b>')
    await vi.waitFor(() => expect(buttonIn(dialog(), 'Save').disabled).toBe(false), { timeout: 4000 })
    expect(text(dialog())).not.toContain('is opened but never closed')
    // lengths are counted and refused at once
    await type(field('input[lang="en"]'), 'q'.repeat(201))
    expect(text(dialog())).toContain('201 / 200')
    expect(text(dialog())).toContain('Too long: at most 200 characters.')
    expect(buttonIn(dialog(), 'Save').disabled).toBe(true)
  }, 20000)

  it('shows what core refuses when a save is refused', async () => {
    const core = new Core()
    const original = core.answer.bind(core)
    core.answer = (url, init) => {
      if (url === '/api/v1/help' && init?.method === 'POST') {
        return json(400, { error: 'invalid', message: 'domain: invalid argument: help entry: body.en: bad_link', problems: [{ field: 'body.en', code: 'bad_link', arg: 'tg://user?id=1' }] })
      }
      return original(url, init)
    }
    await boot(core)
    await loaded()
    buttonIn(section('faq'), 'Add a question').click()
    await vi.waitFor(() => expect(dialog()).not.toBeNull())
    await type(field('input[lang="en"]'), 'Question')
    await type(field('textarea[lang="en"]'), 'See <a href="tg://user?id=1">me</a>')
    await vi.waitFor(() => expect(buttonIn(dialog(), 'Save').disabled).toBe(false), { timeout: 4000 })
    buttonIn(dialog(), 'Save').click()
    // its own words for the problem (https only), the dialog stays open with what was typed
    await vi.waitFor(() => expect(text(dialog())).toContain('Links in the text must start with'), { timeout: 4000 })
    expect(text(dialog())).toContain('https://')
    expect(field('textarea[lang="en"]').value).toContain('tg://user?id=1')
  }, 20000)

  it('edits an entry with the version it read, and loads the list again when it changed meanwhile', async () => {
    const core = new Core()
    await boot(core)
    await loaded()
    labelled('Edit', 'Will I get')!.click()
    await vi.waitFor(() => expect(dialog()).not.toBeNull())
    expect(text(dialog())).toContain('Edit question')
    expect(field('input[lang="en"]').value).toBe('Will I get my own IP address?')
    expect(field('textarea[lang="fa"]').value).toBe('پاسخ آیا آی‌پی اختصاصی می‌گیرم؟')
    // nothing changed: nothing to save, and no check is made of what was saved already
    expect(buttonIn(dialog(), 'Save').disabled).toBe(true)
    expect(core.count('POST', '/api/v1/help/check')).toBe(0)

    await type(field('input[lang="en"]'), 'Will I get a dedicated IP address?')
    await vi.waitFor(() => expect(buttonIn(dialog(), 'Save').disabled).toBe(false), { timeout: 4000 })
    buttonIn(dialog(), 'Save').click()
    await vi.waitFor(() => expect(core.sent('PUT', '/api/v1/help/q1')).toHaveLength(1))
    expect(core.sent('PUT', '/api/v1/help/q1')[0]).toMatchObject({ version: 1, title: { en: 'Will I get a dedicated IP address?' } })
    await vi.waitFor(() => expect(text(section('faq'))).toContain('dedicated IP'))
    expect(core.faq[0].version).toBe(2)

    // someone else saved it first: the dialog closes, the list is loaded again, and the page says so
    core.staleNext = true
    const loads = core.count('GET', '/api/v1/help')
    labelled('Edit', 'dedicated')!.click()
    await vi.waitFor(() => expect(dialog()).not.toBeNull())
    await type(field('input[lang="en"]'), 'Mine')
    await vi.waitFor(() => expect(buttonIn(dialog(), 'Save').disabled).toBe(false), { timeout: 4000 })
    buttonIn(dialog(), 'Save').click()
    await vi.waitFor(() => expect(core.count('GET', '/api/v1/help')).toBe(loads + 1))
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())
    await vi.waitFor(() => expect(document.body.textContent).toContain('Someone changed this meanwhile. The list was loaded again.'))
  }, 30000)

  it('asks before closing on unsaved changes', async () => {
    const core = new Core()
    await boot(core)
    await loaded()
    buttonIn(section('faq'), 'Add a question').click()
    await vi.waitFor(() => expect(dialog()).not.toBeNull())
    await type(field('input[lang="en"]'), 'Half written')
    buttonIn(dialog(), 'Cancel').click()
    await vi.waitFor(() => expect(document.body.textContent).toContain('Discard changes?'))
    ;(document.querySelector('.p-confirmdialog .p-confirmdialog-reject-button') as HTMLElement).click()
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog')).toBeNull())
    expect(field('input[lang="en"]').value).toBe('Half written') // still there
    buttonIn(dialog(), 'Cancel').click()
    await vi.waitFor(() => expect(document.body.textContent).toContain('Discard changes?'))
    ;(document.querySelector('.p-confirmdialog .p-confirmdialog-accept-button') as HTMLElement).click()
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())
    expect(core.count('POST', '/api/v1/help')).toBe(0)
  }, 20000)

  it('switches an entry on and off with the version it read', async () => {
    const core = new Core()
    await boot(core)
    await loaded()
    const first = labelled('Show', 'Will I get') as HTMLInputElement
    expect(first.checked).toBe(true)
    first.click()
    await vi.waitFor(() => expect(core.sent('PUT', '/api/v1/help/q1/enabled')).toEqual([{ enabled: false, version: 1 }]))
    await vi.waitFor(() => expect(text(section('faq'))).toContain('Shown to customers: 0 of 2'))
    expect((labelled('Show', 'Will I get') as HTMLInputElement).checked).toBe(false)
    ;(labelled('Show', 'How do I renew') as HTMLInputElement).click()
    await vi.waitFor(() => expect(core.sent('PUT', '/api/v1/help/q2/enabled')).toEqual([{ enabled: true, version: 1 }]))
  }, 20000)

  it('moves an entry up or down, and the first and the last have no way out of the list', async () => {
    const core = new Core()
    await boot(core)
    await loaded()
    expect((labelled('Move', 'Will I get')?.parentElement as HTMLElement).querySelector('[aria-label*="up"]')?.hasAttribute('disabled')).toBe(true)
    expect(labelled('Move', 'How do I renew')!.getAttribute('aria-label')).toContain('up')
    const down = [...document.querySelectorAll('[aria-label^="Move"][aria-label$="down"]')] as HTMLButtonElement[]
    expect(down.map((b) => b.disabled)).toEqual([false, true, true]) // two questions and one tutorial: the last of each has no way down
    ;(labelled('Move', 'Will I get') as HTMLElement).parentElement!.querySelector<HTMLButtonElement>('[aria-label$="down"]')!.click()
    await vi.waitFor(() => expect(core.sent('POST', '/api/v1/help/order')).toEqual([{ kind: 'faq', ids: ['q2', 'q1'] }]))
    await vi.waitFor(() => expect(text(section('faq').querySelector('.p-datatable-tbody tr'))).toContain('How do I renew?'))
    // the order came with the answer: the list was not asked for again
    expect(core.count('GET', '/api/v1/help')).toBe(1)
  }, 20000)

  it('deletes an entry after a confirmation that names it', async () => {
    const core = new Core()
    await boot(core)
    await loaded()
    labelled('Delete', 'Download the app')!.click()
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog')).not.toBeNull())
    expect(text(document.querySelector('.p-confirmdialog'))).toContain('Delete “')
    expect(text(document.querySelector('.p-confirmdialog'))).toContain('Download the app')
    ;(document.querySelector('.p-confirmdialog .p-confirmdialog-reject-button') as HTMLElement).click()
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog')).toBeNull())
    expect(core.count('DELETE', '/api/v1/help')).toBe(0)
    labelled('Delete', 'Download the app')!.click()
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog')).not.toBeNull())
    ;(document.querySelector('.p-confirmdialog .p-confirmdialog-accept-button') as HTMLElement).click()
    await vi.waitFor(() => expect(core.count('DELETE', '/api/v1/help/t1')).toBe(1))
    // an empty list offers the default content (and says the bot leaves the button out)
    await vi.waitFor(() => expect(text(section('tutorial'))).toContain('No tutorials yet: the Tutorials button is left out of the bot.'))
    buttonIn(section('tutorial'), 'Start from the default tutorials').click()
    await vi.waitFor(() => expect(core.sent('POST', '/api/v1/help/defaults')).toEqual([{ kind: 'tutorial' }]))
    await vi.waitFor(() => expect(text(section('tutorial'))).toContain('A default'))
    expect([...section('tutorial').querySelectorAll('button')].some((b) => text(b) === 'Start from the default tutorials')).toBe(false)
  }, 30000)

  it('edits the link buttons of a tutorial and checks each address and label', async () => {
    const core = new Core()
    await boot(core)
    await loaded()
    buttonIn(section('tutorial'), 'Add a tutorial').click()
    await vi.waitFor(() => expect(dialog()).not.toBeNull())
    expect(text(dialog())).toContain('New tutorial')
    await type(field('input[lang="en"]'), 'Install it')
    await type(field('textarea[lang="en"]'), 'Steps.')
    buttonIn(dialog(), 'Add a button').click()
    await vi.waitFor(() => expect(dialog().querySelectorAll('.button-row')).toHaveLength(1))
    const row = () => dialog().querySelector('.button-row') as HTMLElement
    // no address and no label yet
    expect(text(row())).toContain('Add the link.')
    expect(text(row())).toContain('Give the button a label in at least one language.')
    expect(buttonIn(dialog(), 'Save').disabled).toBe(true)
    await type(row().querySelector('input[dir="ltr"]') as HTMLInputElement, 'http://example.com/a')
    expect(text(row())).toContain('Not a link the bot can use.')
    await type(row().querySelector('input[dir="ltr"]') as HTMLInputElement, 'https://example.com/a')
    expect(text(row())).not.toContain('Not a link the bot can use.')
    await type(row().querySelector('input[lang="fa"]') as HTMLInputElement, 'دانلود')
    expect(row().querySelector('input[lang="fa"]')?.getAttribute('dir')).toBe('rtl')
    await vi.waitFor(() => expect(buttonIn(dialog(), 'Save').disabled).toBe(false), { timeout: 4000 })
    // the preview shows the button with its label in each language (the other language's where one is missing)
    const previews = [...dialog().querySelectorAll('[data-testid="help-preview"]')]
    expect(previews.map((p) => [...p.querySelectorAll('.key.link')].map((k) => text(k)))).toEqual([['دانلود'], ['دانلود']])
    buttonIn(dialog(), 'Save').click()
    await vi.waitFor(() => expect(core.sent('POST', '/api/v1/help')).toHaveLength(1))
    expect(core.sent('POST', '/api/v1/help')[0]).toMatchObject({
      kind: 'tutorial', buttons: [{ url: 'https://example.com/a', label: { fa: 'دانلود', en: '' } }],
    })
  }, 30000)

  it('stops at eight buttons and lets one go', async () => {
    const core = new Core()
    await boot(core)
    await loaded()
    labelled('Edit', 'Download the app')!.click()
    await vi.waitFor(() => expect(dialog()).not.toBeNull())
    const add = () => buttonIn(dialog(), 'Add a button')
    for (let i = 1; i < 8; i++) add().click()
    await vi.waitFor(() => expect(dialog().querySelectorAll('.button-row')).toHaveLength(8))
    expect(add().disabled).toBe(true)
    ;(dialog().querySelector('button[aria-label="Remove button 8"]') as HTMLElement).click()
    await vi.waitFor(() => expect(dialog().querySelectorAll('.button-row')).toHaveLength(7))
    expect(add().disabled).toBe(false)
  }, 20000)

  it('never runs what is typed: the preview shows markup the bot does not allow as text', async () => {
    const core = new Core()
    await boot(core)
    await loaded()
    buttonIn(section('faq'), 'Add a question').click()
    await vi.waitFor(() => expect(dialog()).not.toBeNull())
    await type(field('input[lang="en"]'), '<img src=x onerror=alert(1)>')
    await type(field('textarea[lang="en"]'), '<script>alert(1)</script> <i>fine</i> & <a href="https://example.com">link</a>')
    const preview = dialog().querySelectorAll('[data-testid="help-preview"]')[1]
    expect(text(preview)).toContain('<script>alert(1)</script>')
    expect(text(preview)).toContain('<img src=x onerror=alert(1)>')
    expect(preview.querySelector('script, img')).toBeNull()
    expect(preview.querySelector('i')?.textContent).toBe('fine')
    expect(document.querySelector('.p-dialog img')).toBeNull()
  }, 20000)

  it('will not add past the limit of entries', async () => {
    const core = new Core()
    core.limits = { ...core.limits, max_faq: 2 }
    await boot(core)
    await loaded()
    const add = buttonIn(section('faq'), 'Add a question')
    expect(add.disabled).toBe(true)
    expect(buttonIn(section('tutorial'), 'Add a tutorial').disabled).toBe(false)
  })

  it('is only for staff who may change the branding', async () => {
    const core = new Core()
    const { router } = await boot(core, 'en', 'support', ['overview.read', 'tickets.write'])
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('overview'))
    expect(core.count('GET', '/api/v1/help')).toBe(0)
    expect(document.querySelector('.help-section')).toBeNull()
  })

  it('keeps the tab in the address and loads it only when it is opened', async () => {
    const core = new Core()
    const { router } = await boot(core, 'en', 'owner', ['overview.read', 'branding.write'], '/admin/branding')
    await vi.waitFor(() => expect(document.querySelector('.p-tabs')).not.toBeNull())
    expect(core.count('GET', '/api/v1/help')).toBe(0)
    const tab = [...document.querySelectorAll('[role="tab"]')].find((t) => text(t) === 'Help content') as HTMLElement
    tab.click()
    await vi.waitFor(() => expect(router.currentRoute.value.query.tab).toBe('help'))
    await loaded()
    expect(core.count('GET', '/api/v1/help')).toBe(1)
  }, 20000)

  it('says when the list cannot be loaded and tries again', async () => {
    const core = new Core()
    let down = true
    const original = core.answer.bind(core)
    core.answer = (url, init) => (url === '/api/v1/help' && (init?.method ?? 'GET') === 'GET' && down ? json(503, { error: 'unavailable', message: 'x' }) : original(url, init))
    await boot(core)
    await vi.waitFor(() => expect(document.body.textContent).toContain('Try again'))
    expect(document.querySelector('.help-section')).toBeNull()
    down = false
    buttonIn(document.body, 'Try again').click()
    await loaded()
  }, 20000)
})
