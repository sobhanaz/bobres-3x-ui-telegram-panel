import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { actionsOf, audienceKeys } from '../broadcasts'
import {
  blankResellerDraft,
  insertPlaceholder,
  messageBody,
  reminderChanges,
  reminderProblems,
  resellerDraftProblems,
  timeOf,
  unknownPlaceholders,
  type ReminderForm,
  type ReminderReply,
} from '../resellerMessages'

// The Resellers page's Messages tab: a message to resellers only, with values filled in for each of them, and the weekly settlement
// reminder. The helpers first, then the real page against a fake core.

describe('placeholders', () => {
  it('names the words in braces that are not placeholders, each once', () => {
    expect(unknownPlaceholders('Hi {name}, you owe {debt} ({balance}, {credit_limit}, {tier}, {customers})')).toEqual([])
    expect(unknownPlaceholders('{dept} {Debt} {dept} { debt } {}')).toEqual(['{dept}', '{Debt}'])
  })

  it('adds one where the caret is, apart from the word before it', () => {
    expect(insertPlaceholder('', 'debt')).toEqual({ text: '{debt}', caret: 6 })
    expect(insertPlaceholder('You owe', 'debt')).toEqual({ text: 'You owe {debt}', caret: 14 })
    expect(insertPlaceholder('Hi ', 'name')).toEqual({ text: 'Hi {name}', caret: 9 })
    expect(insertPlaceholder('Hi !', 'name', 3).text).toBe('Hi {name}!')
  })
})

describe('the message', () => {
  it('sends only the texts its audience reads, and the audience as core takes it', () => {
    const d = blankResellerDraft()
    expect(d.target).toBe('debt')
    expect(d.settle).toBe(true)
    d.texts = { fa: ' بدهی {debt} ', en: 'You owe {debt}' }
    expect(messageBody(d)).toEqual({ debt_only: true, tier_id: '', language: '', texts: { fa: 'بدهی {debt}', en: 'You owe {debt}' }, button: null, settle_button: true })
    d.target = 'tier'
    d.tierId = 't1'
    d.language = 'en'
    d.button = { on: true, url: ' https://t.me/shop ', labels: { fa: 'کانال', en: 'Channel' } }
    expect(messageBody(d)).toEqual({ debt_only: false, tier_id: 't1', language: 'en', texts: { fa: '', en: 'You owe {debt}' },
      button: { url: 'https://t.me/shop', labels: { fa: '', en: 'Channel' } }, settle_button: true })
  })

  it('is not sent without its texts, with an unknown placeholder, or without the tier it names', () => {
    const d = blankResellerDraft()
    expect(resellerDraftProblems(d)).toEqual(['texts.fa', 'texts.en'])
    d.texts = { fa: '{debt}', en: 'You owe {owed}' }
    expect(resellerDraftProblems(d)).toEqual(['texts.en'])
    d.texts.en = 'You owe {debt}'
    d.target = 'tier'
    expect(resellerDraftProblems(d)).toEqual(['tier_id'])
  })

  it('shows who a message to resellers went to, and is never edited in the composer', () => {
    const b = { segment: 'resellers' as const, language: '' as const, resellers: { debt_only: true, tier_id: '', settle_button: true, kind: 'reminder' as const } }
    expect(audienceKeys(b)).toEqual(['broadcasts.segment.resellers', 'broadcasts.resellers.debt_only', 'broadcasts.resellers.reminder', 'broadcasts.language.all'])
    expect(audienceKeys({ segment: 'active', language: 'fa', resellers: null })).toEqual(['broadcasts.segment.active', 'broadcasts.language.fa'])
    expect(actionsOf({ segment: 'resellers', status: 'scheduled' })).toEqual(['cancel'])
    expect(actionsOf({ segment: 'all', status: 'scheduled' })).toEqual(['edit', 'cancel'])
    for (const k of audienceKeys({ segment: 'resellers', language: 'en', resellers: { debt_only: true, tier_id: 'x', settle_button: false, kind: 'reminder' } })) {
      const get = (o: unknown) => k.split('.').reduce<unknown>((v, p) => (v as Record<string, unknown>)?.[p], o)
      expect(typeof get(en), k).toBe('string')
      expect(typeof get(fa), k).toBe('string')
    }
  })
})

describe('the reminder', () => {
  const form = (over: Partial<ReminderForm> = {}): ReminderForm => ({
    enabled: false, weekday: 'thursday', time: '21:00', debtOnly: true, settle: true, texts: { fa: 'بدهی {debt}', en: 'Debt {debt}' }, ...over,
  })

  it('reads a time of day in either script', () => {
    expect(timeOf('21:00')).toBe('21:00')
    expect(timeOf('۸:۳۰')).toBe('08:30')
    expect(timeOf(' 9:05 ')).toBe('09:05')
    expect(timeOf('24:00')).toBeNull()
    expect(timeOf('9')).toBeNull()
  })

  it('saves only what changed, in the settings it is kept in', () => {
    expect(reminderChanges(form(), form())).toEqual({})
    expect(reminderChanges(form(), form({ enabled: true, weekday: 'friday', time: '۸:۳۰', debtOnly: false, settle: false, texts: { fa: 'بدهی {debt}', en: ' Pay {debt} ' } }))).toEqual({
      'resellers.reminder.enabled': 'true',
      'resellers.reminder.weekday': 'friday',
      'resellers.reminder.time': '08:30',
      'resellers.reminder.debt_only': 'false',
      'resellers.reminder.settle_button': 'false',
      'resellers.reminder.text.en': 'Pay {debt}',
    })
  })

  it('is not saved with a bad time or text', () => {
    expect(reminderProblems(form())).toEqual([])
    expect(reminderProblems(form({ time: '25:00', texts: { fa: '', en: '{owed}' } }))).toEqual(['time', 'texts.fa', 'texts.en'])
  })
})

describe('the Messages tab', () => {
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const me = (permissions: string[]) => ({
    user: { id: 'u0', telegram_id: 1, username: 'boss', role: 'owner', language: 'en' },
    permissions,
    csrf: 'csrf-1',
    session: { method: 'link', expires_at: 2_000_000_000 },
    password: { enabled: false },
    password_available: true,
    brand: 'Shop',
    branding: { color: '#0f766e', logo: null, currency: { fa: '', en: '' } },
  })
  const reminder = (over: Partial<ReminderReply> = {}): ReminderReply => ({
    enabled: false, weekday: 'thursday', time: '21:00', debt_only: true, settle_button: true,
    texts: { fa: 'بدهی شما {debt} است.', en: 'Your account debt is {debt}.' }, custom: { fa: false, en: false },
    defaults: { fa: 'بدهی شما {debt} است.', en: 'Your account debt is {debt}.' },
    weekdays: ['saturday', 'sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday'], zone: 'Asia/Tehran', next_due_at: null,
    last: { day: '2026-10-22', zone: 'Asia/Tehran', due_at: 1_792_000_000, queued_at: 1_792_000_010,
      broadcast: { id: 'b-last', status: 'done', started_at: 1_792_000_015, finished_at: 1_792_000_030, counts: { total: 3, pending: 0, sent: 2, blocked: 1, failed: 0, unknown: 0 } } },
    ...over,
  })
  const reseller = (id: string, username: string) => ({
    user: { id, telegram_id: 7, username, status: 'active' }, tier: { id: 't1', name: 'Gold', discount_percent: 10, commission_percent: 5 },
    commission_percent: 5, commission_override: null, balances: [], customers: 0, commission_earned: [], created_at: 1,
  })

  afterEach(() => {
    ;(document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
    vi.unstubAllGlobals()
    try {
      localStorage.removeItem('bobres.lang')
    } catch {
      /* no storage here */
    }
    document.body.innerHTML = ''
  })

  async function boot(permissions: string[], state = { reminder: reminder() }) {
    vi.resetModules()
    localStorage.setItem('bobres.lang', 'en')
    window.history.replaceState(null, '', '/admin/resellers?tab=messages')
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        const raw = typeof init?.body === 'string' ? init.body : ''
        calls.push(`${method} ${url} ${raw}`.trim())
        const body = raw ? (JSON.parse(raw) as Record<string, unknown>) : {}
        if (url === '/api/v1/me') return json(200, me(permissions))
        if (url.startsWith('/api/v1/resellers?')) return json(200, { items: [reseller('r1', 'tabriz_shop'), reseller('r2', 'calm')], total: 2, page: 1, size: 100 })
        if (url === '/api/v1/reseller-tiers') return json(200, { items: [{ id: 't1', name: 'Gold', discount_percent: 10, commission_percent: 5, resellers: 2, prices: [], version: 1 }] })
        if (url === '/api/v1/reseller-messages/audience') return json(200, { total: body.debt_only ? 1 : 2, languages: { fa: 1, en: body.debt_only ? 0 : 1 } })
        if (url === '/api/v1/reseller-messages/check') {
          const text = String(body.text)
          const bad = [...text.matchAll(/\{(\w+)\}/g)].filter((m) => !['name', 'debt'].includes(m[1]))
          return json(200, { ok: !bad.length, value: text, problems: bad.map((m) => ({ code: 'placeholder_unknown', arg: m[0] })), length: text.length, max: 4096 })
        }
        if (url === '/api/v1/reseller-messages/preview') {
          const who = body.reseller_id === 'r2' ? '@calm' : '@tabriz_shop'
          return json(200, { text: String(body.text).replace('{name}', who).replace('{debt}', '250,000 Toman'), reseller: { id: body.reseller_id || 'r1', telegram_id: 7, username: who.slice(1) } })
        }
        if (url === '/api/v1/reseller-messages/test' || url === '/api/v1/reseller-messages/reminder/test') return json(200, { ok: true, message_id: 9 })
        if (url === '/api/v1/reseller-messages' && method === 'POST') return json(200, { item: { id: 'b-new', status: 'scheduled', segment: 'resellers' } })
        if (url === '/api/v1/reseller-messages/reminder' && method === 'GET') return json(200, state.reminder)
        if (url === '/api/v1/reseller-messages/reminder' && method === 'PUT') {
          const values = body.values as Record<string, string>
          state.reminder = { ...state.reminder, enabled: values['resellers.reminder.enabled'] === 'true' || state.reminder.enabled, next_due_at: 1_792_600_000 }
          return json(200, state.reminder)
        }
        if (url.startsWith('/api/v1/broadcasts/b-new')) return json(200, { item: { id: 'b-new', status: 'running', segment: 'resellers', language: '', texts: { fa: 'x', en: 'x' }, button: null,
          resellers: { debt_only: true, tier_id: '', settle_button: true, kind: 'manual' }, scheduled_at: null, started_at: 1, finished_at: null,
          counts: { total: 1, pending: 1, sent: 0, blocked: 0, failed: 0, unknown: 0 }, rate_per_sec: 20, eta_seconds: 1, last_error: '', created_by: null, created_at: 1, version: 1 } })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { calls, router }
  }
  const text = () => (document.body.textContent ?? '').replace(/\s+/g, ' ')
  const type = (el: HTMLTextAreaElement | HTMLInputElement, v: string) => {
    el.value = v
    el.dispatchEvent(new Event('input', { bubbles: true }))
  }
  const posted = (calls: string[], path: string) => calls.filter((c) => c.startsWith(`POST /api/v1${path} `)).map((c) => JSON.parse(c.slice(`POST /api/v1${path} `.length)) as Record<string, unknown>)
  const area = () => document.querySelector<HTMLTextAreaElement>('.messages textarea')!
  const wait = { timeout: 4000 }

  it('is not there without broadcasts.write', async () => {
    await boot(['overview.read', 'resellers.write'])
    await vi.waitFor(() => expect(text()).toContain(en.reseller.tab_tiers))
    expect(text()).not.toContain(en.reseller.tab_messages)
    expect(document.querySelector('.messages')).toBeNull()
  })

  it('previews the text as a reseller gets it, refuses an unknown placeholder, tests it on me, then sends it', async () => {
    const { calls, router } = await boot(['overview.read', 'resellers.write', 'broadcasts.write', 'audit.read'])
    await vi.waitFor(() => expect(text()).toContain(en.reseller.messages.title), wait)
    // Only those who owe, by default: one of them.
    await vi.waitFor(() => expect(text()).toContain('Recipients: 1'), wait)
    expect(posted(calls, '/reseller-messages/audience')[0]).toEqual({ debt_only: true, tier_id: '', language: '' })

    // A value from the chips goes into the open tab's text.
    const langTab = (name: string) => [...document.querySelectorAll<HTMLElement>('.messages [role=tab]')].find((el) => el.textContent?.includes(name))!
    await vi.waitFor(() => expect(langTab(en.texts.en)).toBeTruthy())
    langTab(en.texts.en).click()
    await vi.waitFor(() => expect(area()?.getAttribute('lang')).toBe('en'))
    type(area(), 'Hello {name}, you owe')
    ;[...document.querySelectorAll<HTMLButtonElement>('.chip')].find((b) => b.textContent?.includes('{debt}'))!.click()
    await vi.waitFor(() => expect(area().value).toBe('Hello {name}, you owe {debt}'))
    await vi.waitFor(() => expect(document.querySelector('.rendered')?.textContent).toBe('Hello @tabriz_shop, you owe 250,000 Toman'), wait)

    // An unknown placeholder: the check says so and nothing is sent.
    type(area(), 'Hello {nme}')
    await vi.waitFor(() => expect(text()).toContain('{nme}'), wait)
    document.querySelector<HTMLButtonElement>('.send-now')!.click()
    await vi.waitFor(() => expect(text()).toContain(en.broadcasts.save.fix_first))
    expect(posted(calls, '/reseller-messages')).toHaveLength(0)
    // The first language with a problem opens: the Persian text is not written yet.
    await vi.waitFor(() => expect(area().getAttribute('lang')).toBe('fa'))
    type(area(), 'سلام {name}، بدهی شما {debt} است')
    await vi.waitFor(() => expect(posted(calls, '/reseller-messages/preview').some((b) => b.lang === 'fa' && b.text === 'سلام {name}، بدهی شما {debt} است')).toBe(true), wait)

    langTab(en.texts.en).click()
    await vi.waitFor(() => expect(area().getAttribute('lang')).toBe('en'))
    type(area(), 'Hello {name}, you owe {debt}')
    document.querySelector<HTMLButtonElement>('.messages .footer .test')!.click()
    await vi.waitFor(() => expect(posted(calls, '/reseller-messages/test')).toHaveLength(1))
    expect(posted(calls, '/reseller-messages/test')[0]).toMatchObject({ lang: 'en', text: 'Hello {name}, you owe {debt}', settle_button: true, reseller_id: '', debt_only: true })

    await vi.waitFor(() => expect(text()).toContain(en.broadcasts.test.ok))

    // Send: asked first, with the count.
    await vi.waitFor(() => expect(document.querySelector<HTMLButtonElement>('.send-now')!.disabled).toBe(false))
    document.querySelector<HTMLButtonElement>('.send-now')!.click()
    const accept = await vi.waitFor(() => {
      const b = document.querySelector<HTMLButtonElement>('.p-confirmdialog-accept-button')
      expect(b).not.toBeNull()
      return b!
    })
    expect(document.querySelector('.p-confirmdialog')?.textContent).toContain('It goes to 1 reseller now.')
    accept.click()
    await vi.waitFor(() => expect(posted(calls, '/reseller-messages')).toHaveLength(1))
    expect(posted(calls, '/reseller-messages')[0]).toEqual({ debt_only: true, tier_id: '', language: '', settle_button: true, button: null,
      texts: { fa: 'سلام {name}، بدهی شما {debt} است', en: 'Hello {name}, you owe {debt}' } })
    // Its page shows the progress, and who it went to.
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('broadcast'))
    await vi.waitFor(() => expect(text()).toContain(`${en.broadcasts.segment.resellers} · ${en.broadcasts.resellers.debt_only}`), wait)
    expect(text()).toContain(en.broadcasts.detail.by_core)
  })

  it('shows the reminder, saves a change of it and sends a test of it', async () => {
    const { calls } = await boot(['overview.read', 'resellers.write', 'broadcasts.write'])
    await vi.waitFor(() => expect(document.querySelector('.reminder .save')).not.toBeNull(), wait)
    expect(text()).toContain(en.reseller.reminder.off)
    expect(text()).toContain('delivered to 2 of 3')
    expect(text()).toContain('Asia/Tehran')
    const save = document.querySelector<HTMLButtonElement>('.reminder .save')!
    expect(save.disabled).toBe(true)
    document.querySelector<HTMLInputElement>('.reminder .enabled input')!.click()
    await vi.waitFor(() => expect(save.disabled).toBe(false))
    // The test sends the saved reminder: not while a change waits.
    expect(document.querySelector<HTMLButtonElement>('.reminder .test')!.disabled).toBe(true)
    save.click()
    await vi.waitFor(() => expect(calls.some((c) => c.startsWith('PUT /api/v1/reseller-messages/reminder '))).toBe(true))
    const put = calls.find((c) => c.startsWith('PUT /api/v1/reseller-messages/reminder '))!
    expect(JSON.parse(put.slice('PUT /api/v1/reseller-messages/reminder '.length))).toEqual({ values: { 'resellers.reminder.enabled': 'true' } })
    await vi.waitFor(() => expect(text()).toContain(en.reseller.reminder.on))
    await vi.waitFor(() => expect(document.querySelector<HTMLButtonElement>('.reminder .test')!.disabled).toBe(false))
    document.querySelector<HTMLButtonElement>('.reminder .test')!.click()
    await vi.waitFor(() => expect(posted(calls, '/reseller-messages/reminder/test')).toEqual([{ reseller_id: '' }]))
  })
})

describe('the catalogs', () => {
  it('have every text of the tab in both languages, with the same values', () => {
    const flat = (o: Record<string, unknown>, p = ''): [string, string][] =>
      Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v as Record<string, unknown>, p + k + '.') : [[p + k, String(v)] as [string, string]]))
    const names = (s: string) => [...new Set([...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))].sort()
    const faMap = new Map(flat(fa as Record<string, unknown>))
    for (const [k, v] of flat(en as Record<string, unknown>).filter(([k]) => k.startsWith('reseller.messages.') || k.startsWith('reseller.reminder.'))) {
      expect(faMap.has(k), k).toBe(true)
      expect(names(faMap.get(k)!), k).toEqual(names(v))
    }
  })
})
