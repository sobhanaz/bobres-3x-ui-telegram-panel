import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { actionsOf, audienceKeys } from '../broadcasts'
import { announceProblems, channelProblemKey, draftFrom, refusalOf, sendBody, statusSeverity, unknownPlaceholders, type AnnounceDraftReply, type AnnouncementItem } from '../announce'

// New product announcements: the helpers first, then the Announce dialog of the Plans page against a fake core.

const draftReply = (over: Partial<AnnounceDraftReply['settings']> = {}): AnnounceDraftReply => ({
  product: { kind: 'plan', id: 'p1', names: { en: 'Gold', fa: 'طلایی' }, kinds: { en: 'VPN plan', fa: 'سرویس VPN' }, price: 150000, currency: 'IRT', scale: 0,
    toman: 0, days: 30, months: 0, traffic_bytes: 50 * 2 ** 30, stock: null, start_param: 'p_p1', buy_callback: 'an:b:p:p1' },
  texts: { fa: '🆕 {name} — {price}', en: '🆕 {name} — {price}' },
  placeholders: ['name', 'kind', 'price', 'days', 'traffic', 'stock', 'store'],
  buy_labels: { fa: '🛒 خرید', en: '🛒 Buy' },
  settings: { enabled: true, channel: '@mystore', channel_langs: 'default', default_language: 'fa', daily_max: 3, quiet_start: '23:00', quiet_end: '08:00', ...over },
  audience: { total: 12, languages: { fa: 10, en: 2 } },
  recent: [],
})

describe('the announcement', () => {
  it('starts from the prefilled cards, to the channel when there is one and to private messages', () => {
    expect(draftFrom(draftReply())).toEqual({ texts: { fa: '🆕 {name} — {price}', en: '🆕 {name} — {price}' }, channel: true, private: true })
    expect(draftFrom(draftReply({ channel: '' })).channel).toBe(false)
  })

  it('names what keeps it from going out', () => {
    const d = draftFrom(draftReply())
    expect(announceProblems(d)).toEqual([])
    expect(unknownPlaceholders('{name} {prise} {prise} {Price}')).toEqual(['{prise}', '{Price}'])
    expect(announceProblems({ ...d, channel: false, private: false, texts: { fa: ' ', en: '{prise}' } })).toEqual(['targets', 'texts.fa', 'texts.en'])
  })

  it('sends the trimmed cards, where they go and whether it is forced', () => {
    const d = { texts: { fa: ' x ', en: 'y\n' }, channel: false, private: true }
    expect(sendBody('license', 'pro:12', d)).toEqual({ kind: 'license', id: 'pro:12', texts: { fa: 'x', en: 'y' }, channel: false, private: true, force: false })
    expect(sendBody('plan', 'p1', d, true).force).toBe(true)
  })

  it("reads core's refusals", () => {
    expect(refusalOf(409, { error: 'repeat', last_at: 1700000000 })).toEqual({ kind: 'repeat', lastAt: 1700000000 })
    expect(refusalOf(409, { error: 'limit', max: 3, day: '2026-10-30' })).toEqual({ kind: 'limit', max: 3, day: '2026-10-30' })
    expect(refusalOf(409, { error: 'channel', problem: 'cannot_post', chat: '@s' })).toEqual({ kind: 'channel', problem: 'cannot_post', chat: '@s', detail: '' })
    expect(refusalOf(409, { error: 'state' })).toBeNull()
    expect(refusalOf(400, { error: 'repeat' })).toBeNull()
    expect(channelProblemKey('not_admin')).toBe('announce.channel_problem.not_admin')
    expect(channelProblemKey('weird')).toBe('announce.channel_problem.error')
  })

  it('colours its state, and its private messages are a broadcast that is never edited', () => {
    const a = { status: 'sent', to_channel: true, channel_status: 'failed' } as AnnouncementItem
    expect(statusSeverity(a)).toBe('warn')
    expect(statusSeverity({ ...a, channel_status: 'sent' })).toBe('success')
    expect(statusSeverity({ ...a, status: 'queued' })).toBe('info')
    expect(actionsOf({ status: 'scheduled', segment: 'announce' })).not.toContain('edit')
    expect(audienceKeys({ segment: 'announce', language: '', resellers: null })[0]).toBe('broadcasts.segment.announce')
  })
})

describe('the Announce dialog', () => {
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
  const plan = (id: string, over: Record<string, unknown> = {}) => ({
    id, name: { en: id === 'p1' ? 'Gold' : 'Trial' }, kind: 'both', duration_days: 30, traffic_bytes: 50 * 2 ** 30, price: { amount: 150000, currency: 'IRT' },
    enabled: true, is_trial: false, is_topup: false, sort: 0, sales: 0, ...over,
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

  async function boot(permissions: string[], sends: Response[] = []) {
    vi.resetModules()
    localStorage.setItem('bobres.lang', 'en')
    window.history.replaceState(null, '', '/admin/plans')
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
        if (url === '/api/v1/plans') return json(200, { items: [plan('p1'), plan('p2', { is_trial: true })] })
        if (url === '/api/v1/plan-groups' || url === '/api/v1/servers' || url === '/api/v1/plans/deleted') return json(200, { items: [] })
        if (url.startsWith('/api/v1/announcements/draft?')) return json(200, draftReply())
        if (url === '/api/v1/announcements/channel-check') return json(200, { chat: '@mystore', ok: true, can_post: true, bot_admin: true, title: 'Store', problem: '', detail: '' })
        if (url === '/api/v1/announcements/check') return json(200, { ok: true, value: body.text, problems: [], length: 10, max: 4096 })
        if (url === '/api/v1/announcements/preview') {
          const texts = body.texts as Record<string, string>
          return json(200, { text: texts[String(body.lang)].replace('{name}', 'Gold').replace('{price}', '150,000 Toman'), channel: texts.fa.replace('{name}', 'طلایی'), channel_lang: 'fa', buy_label: '🛒 Buy' })
        }
        if (url === '/api/v1/announcements/test') return json(200, { ok: true, message_id: 9 })
        if (url === '/api/v1/announcements' && method === 'POST') return sends.shift() ?? json(503, { error: 'unavailable', message: 'no more' })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { calls }
  }
  const text = () => (document.body.textContent ?? '').replace(/\s+/g, ' ')
  const posted = (calls: string[], path: string) => calls.filter((c) => c.startsWith(`POST /api/v1${path} `)).map((c) => JSON.parse(c.slice(`POST /api/v1${path} `.length)) as Record<string, unknown>)
  const wait = { timeout: 4000 }
  const accept = () =>
    vi.waitFor(() => {
      const b = document.querySelector<HTMLButtonElement>('.p-confirmdialog-accept-button')
      expect(b).not.toBeNull()
      return b!
    })

  it('is not offered without broadcasts.write, nor for a trial', async () => {
    await boot(['overview.read', 'plans.write'])
    await vi.waitFor(() => expect(text()).toContain('Gold'), wait)
    expect(document.querySelector('.announce')).toBeNull()
  })

  it('previews the card, tests it on me, then announces it after asking, again when it was announced recently', async () => {
    const sent = { announcement: { id: 'a1', due_at: 1_800_000_000, status: 'queued' }, queued: false, audience: 12 }
    const { calls } = await boot(['overview.read', 'plans.write', 'broadcasts.write'], [
      json(409, { error: 'repeat', last_at: 1_799_990_000, message: 'recent' }),
      json(200, sent),
    ])
    await vi.waitFor(() => expect(document.querySelectorAll('.announce')).toHaveLength(1), wait) // the plan, not the trial
    document.querySelector<HTMLButtonElement>('.announce')!.click()
    await vi.waitFor(() => expect(text()).toContain('Announce “Gold”'), wait)
    await vi.waitFor(() => expect(text()).toContain('12 customers said yes to product news'), wait)
    await vi.waitFor(() => expect(text()).toContain('🆕 Gold — 150,000 Toman'), wait) // the private message, in the open tab's language
    expect(text()).toContain('🆕 طلایی — {price}') // the channel post, in Persian
    expect(posted(calls, '/announcements/channel-check')).toHaveLength(1)

    const testButton = [...document.querySelectorAll<HTMLButtonElement>('.p-dialog-footer button')].find((b) => b.textContent?.includes(en.broadcasts.test.send))!
    testButton.click()
    await vi.waitFor(() => expect(posted(calls, '/announcements/test')).toHaveLength(1))
    expect(posted(calls, '/announcements/test')[0]).toMatchObject({ kind: 'plan', id: 'p1', lang: 'en' })

    const send = [...document.querySelectorAll<HTMLButtonElement>('.p-dialog-footer button')].find((b) => b.textContent?.includes(en.announce.send))!
    await vi.waitFor(() => expect(send.disabled).toBe(false)) // not while the test is on its way
    send.click()
    const ok = await accept()
    expect(document.querySelector('.p-confirmdialog')?.textContent).toContain('It is posted in @mystore. 12 customers get a private message.')
    ok.click()
    await vi.waitFor(() => expect(text()).toContain(en.announce.repeat_title), wait)
    ;(await accept()).click()
    await vi.waitFor(() => expect(posted(calls, '/announcements')).toHaveLength(2))
    const [first, second] = posted(calls, '/announcements')
    expect(first).toEqual({ kind: 'plan', id: 'p1', texts: { fa: '🆕 {name} — {price}', en: '🆕 {name} — {price}' }, channel: true, private: true, force: false })
    expect(second.force).toBe(true)
    await vi.waitFor(() => expect(text()).toContain(en.announce.sent), wait)
  })
})

describe('the catalogs', () => {
  it('have every text of the dialog in both languages, with the same values', () => {
    const flat = (o: Record<string, unknown>, p = ''): [string, string][] =>
      Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v as Record<string, unknown>, p + k + '.') : [[p + k, String(v)] as [string, string]]))
    const names = (s: string) => [...new Set([...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))].sort()
    const faMap = new Map(flat(fa as Record<string, unknown>))
    const keys = flat(en as Record<string, unknown>).filter(([k]) => k.startsWith('announce.') || k.startsWith('settings.field.announce.') || k.startsWith('settings.group.announce.'))
    expect(keys.length).toBeGreaterThan(50)
    for (const [k, v] of keys) {
      expect(faMap.has(k), k).toBe(true)
      expect(names(faMap.get(k)!), k).toEqual(names(v))
    }
  })
})
