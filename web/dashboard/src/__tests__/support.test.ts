import { afterEach, describe, expect, it, vi } from 'vitest'
import fa from '../locales/fa.json'
import {
  assigneeChoices,
  cannedBody,
  cannedLangs,
  customerBlocked,
  duration,
  fileHref,
  fileSize,
  inboxTab,
  isImageFile,
  isLate,
  plainLength,
  poller,
  replyOk,
  requestKeys,
  staffName,
  statusParam,
  ticketSearch,
  waitingSeconds,
  REPLY_MAX,
} from '../support'
import { auditLink } from '../staff'
import type { CannedReply, TicketItem, TicketMessageItem } from '../types'

const now = 1_760_000_000

describe('inbox helpers', () => {
  it('reads the status tab from the address', () => {
    expect(inboxTab('answered')).toBe('answered')
    expect(inboxTab('all')).toBe('all')
    expect(inboxTab('bogus')).toBe('open')
    expect(inboxTab(undefined)).toBe('open')
    expect(statusParam('all')).toBe('')
    expect(statusParam('closed')).toBe('closed')
  })
  it('tidies numbers typed in Persian, keeps words as typed', () => {
    expect(ticketSearch(' #۱۰۲۴ ')).toBe('#1024')
    expect(ticketSearch('＃12')).toBe('#12')
    expect(ticketSearch('۱۲۳۴۵۶۷۸۹')).toBe('123456789')
    expect(ticketSearch('@Ali')).toBe('@Ali')
    expect(ticketSearch('سفارش ۱۲')).toBe('سفارش ۱۲')
    expect(ticketSearch('a1b2c3')).toBe('a1b2c3')
  })
  it('measures waiting only for waiting tickets', () => {
    expect(waitingSeconds({ status: 'open', last_customer_at: now - 90 }, now)).toBe(90)
    expect(waitingSeconds({ status: 'open', last_customer_at: now + 5 }, now)).toBe(0)
    expect(waitingSeconds({ status: 'answered', last_customer_at: now - 90 }, now)).toBeNull()
    expect(waitingSeconds({ status: 'open', last_customer_at: null }, now)).toBeNull()
    expect(isLate(86_400)).toBe(true)
    expect(isLate(3_600)).toBe(false)
    expect(isLate(null)).toBe(false)
  })
  it('writes durations in the largest sensible unit', () => {
    expect(duration(10, 'en')).toBe('1 minute')
    expect(duration(5 * 60 + 59, 'en')).toBe('5 minutes')
    expect(duration(2 * 3600 + 100, 'en')).toBe('2 hours')
    expect(duration(47 * 3600, 'en')).toBe('47 hours')
    expect(duration(3 * 86_400 + 5, 'en')).toBe('3 days')
    expect(duration(2 * 3600, 'fa')).toBe('۲ ساعت')
    expect(duration(5 * 60, 'fa')).toBe('۵ دقیقه')
  })
  it('names staff by username, else Telegram id', () => {
    expect(staffName({ username: 'ali', telegram_id: 5 })).toBe('@ali')
    expect(staffName({ username: '', telegram_id: 5 })).toBe('5')
    expect(staffName(null)).toBe('')
  })
})

describe('thread helpers', () => {
  it('counts a reply as plain UTF-16 text', () => {
    expect(plainLength('  a<b>c  ')).toBe(5) // not HTML: the tag is text
    expect(plainLength('&amp;')).toBe(5)
    expect(plainLength('a\r\nb')).toBe(3)
    expect(plainLength('😀')).toBe(2)
    expect(replyOk('   ')).toBe(false)
    expect(replyOk('x'.repeat(REPLY_MAX))).toBe(true)
    expect(replyOk('x'.repeat(REPLY_MAX + 1))).toBe(false)
  })
  it('picks a saved reply in the customer language, else the other', () => {
    const c: Pick<CannedReply, 'body'> = { body: { fa: 'سلام', en: 'Hello' } }
    expect(cannedBody(c, 'fa')).toBe('سلام')
    expect(cannedBody(c, 'en')).toBe('Hello')
    expect(cannedBody(c, '')).toBe('سلام')
    expect(cannedBody({ body: { fa: '  ', en: 'Hello' } }, 'fa')).toBe('Hello')
    expect(cannedBody({ body: { fa: 'سلام', en: '' } }, 'en')).toBe('سلام')
    expect(cannedLangs({ body: { fa: '', en: 'x' } })).toEqual(['en'])
  })
  it('opens files only through core', () => {
    const file = { type: 'document', mime: 'application/pdf', name: 'a.pdf', size: 1, url: '' }
    expect(fileHref('t1', { id: 'm1', file: { ...file, url: '/api/v1/tickets/t1/messages/m1/file' } })).toBe('/api/v1/tickets/t1/messages/m1/file')
    expect(fileHref('t1', { id: 'm1', file: { ...file, url: 'https://evil.example/x' } })).toBe('/api/v1/tickets/t1/messages/m1/file')
    expect(fileHref('t1', { id: 'm1', file: { ...file, url: '//evil.example/x' } })).toBe('/api/v1/tickets/t1/messages/m1/file')
    expect(fileHref('t1', { id: 'm1', file: { ...file, url: '/api/v1/../x' } })).toBe('/api/v1/tickets/t1/messages/m1/file')
    expect(isImageFile({ type: 'photo', mime: '' })).toBe(true)
    expect(isImageFile({ type: 'document', mime: 'image/png' })).toBe(true)
    expect(isImageFile({ type: 'document', mime: 'image/svg+xml' })).toBe(false)
    expect(isImageFile({ type: 'document', mime: 'application/pdf' })).toBe(false)
  })
  it('writes file sizes', () => {
    expect(fileSize(900, 'en')).toBe('900 bytes')
    expect(fileSize(12_800, 'en')).toBe('12.5 kB')
    expect(fileSize(2 * 1024 * 1024, 'en')).toBe('2 MB')
    expect(fileSize(12_800, 'fa')).toBe('۱۲٫۵ کیلوبایت')
  })
  it('tells a customer who blocked the bot from the latest delivered reply', () => {
    const staff = (status: 'pending' | 'sent' | 'blocked' | 'failed') => ({ kind: 'staff' as const, delivery: { status, at: null } })
    const customer = { kind: 'customer' as const, delivery: null }
    expect(customerBlocked([staff('blocked')])).toBe(true)
    expect(customerBlocked([staff('blocked'), customer, staff('pending')])).toBe(true)
    expect(customerBlocked([staff('blocked'), staff('sent')])).toBe(false)
    expect(customerBlocked([customer])).toBe(false)
    // core's flag wins whenever it is sent: unblocked since that reply, or blocked with no reply yet
    expect(customerBlocked([staff('blocked')], false)).toBe(false)
    expect(customerBlocked([customer], true)).toBe(true)
    expect(customerBlocked([staff('blocked')], undefined)).toBe(true)
  })
  it('lets only managers give tickets to others', () => {
    const me = { id: 's1', telegram_id: 1, username: 'me' }
    const all = [{ ...me, role: 'support' }, { id: 's2', telegram_id: 2, username: 'boss', role: 'owner' }]
    const ids = (cs: ReturnType<typeof assigneeChoices>) => cs.map((c) => `${c.id || '-'}${c.disabled ? '!' : ''}`)
    expect(ids(assigneeChoices(all, me, true, null))).toEqual(['-', 's1', 's2'])
    expect(ids(assigneeChoices(all, me, false, null))).toEqual(['-', 's1'])
    // someone else holds it: shown, but not choosable without tickets.manage
    expect(ids(assigneeChoices(all, me, false, all[1]))).toEqual(['-', 's1', 's2!'])
    expect(ids(assigneeChoices([], me, true, { id: 's9', telegram_id: 9, username: '' }))).toEqual(['-', 's1', 's9'])
    expect(assigneeChoices(all, me, false, null)[1].me).toBe(true)
  })
  it('keeps one request key per send until it succeeds', () => {
    let n = 0
    const keys = requestKeys(() => `k${++n}`)
    expect(keys.key('a')).toBe('k1')
    expect(keys.key('a')).toBe('k1') // a retry
    expect(keys.key('b')).toBe('k2') // something else
    keys.done()
    expect(keys.key('b')).toBe('k3')
  })
})

describe('poller', () => {
  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))
  afterEach(() => {
    Reflect.deleteProperty(document, 'hidden')
  })
  it('repeats, never overlaps, and stops', async () => {
    let running = 0
    let most = 0
    const fn = vi.fn(async () => {
      running++
      most = Math.max(most, running)
      await sleep(15)
      running--
    })
    const p = poller(fn, () => 2)
    p.start()
    await vi.waitFor(() => expect(fn.mock.calls.length).toBeGreaterThanOrEqual(3))
    p.stop()
    const n = fn.mock.calls.length
    await sleep(40)
    expect(fn.mock.calls.length).toBe(n)
    expect(most).toBe(1)
  })
  it('waits while the tab is hidden and catches up when it shows', async () => {
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => true })
    const fn = vi.fn()
    const p = poller(fn, () => 5)
    p.start()
    await sleep(30)
    expect(fn).not.toHaveBeenCalled()
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => false })
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.waitFor(() => expect(fn).toHaveBeenCalled())
    p.stop()
  })
  it('can wait one interval before the first run', async () => {
    const fn = vi.fn()
    const p = poller(fn, () => 40)
    p.start(false)
    await sleep(10)
    expect(fn).not.toHaveBeenCalled()
    await vi.waitFor(() => expect(fn).toHaveBeenCalled())
    p.stop()
  })
})

describe('audit links for support and broadcasts', () => {
  const uuid = '0b6f0f3e-6a39-4c55-9a1e-2b9a1c0d4e5f'
  const entry = (entity: string, entity_id: string) => ({ entity, entity_id, after: null, actor: null })
  it('opens the thread, the broadcast and the saved replies', () => {
    expect(auditLink(entry('ticket', uuid))).toEqual({ name: 'ticket', params: { id: uuid } })
    expect(auditLink(entry('broadcast', uuid))).toEqual({ name: 'broadcast', params: { id: uuid } })
    expect(auditLink(entry('canned_reply', uuid))).toEqual({ name: 'support', query: { tab: 'canned' } })
    expect(auditLink(entry('ticket', 'x'))).toBeNull()
    expect(auditLink(entry('broadcast', ''))).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// The pages, booted with the whole app against a fake core.

describe('support pages', () => {
  const tid = '11111111-2222-4333-8444-555555555555'
  const cid = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'
  const helper = { id: 's1', telegram_id: 1001, username: 'helper' }
  const customer = { id: cid, telegram_id: 777, username: 'cust', status: 'banned', language: 'fa' }
  const t0 = Math.floor(Date.now() / 1000)
  const baseTicket: TicketItem = {
    id: tid, number: 1024, status: 'open', category: 'payment', subject: 'Payment not received', user: customer, assignee: null,
    created_at: t0 - 7200, updated_at: t0 - 60, last_customer_at: t0 - 7200, last_staff_at: t0 - 3600, closed_at: null, close_reason: '',
    last_message: { kind: 'customer', preview: 'hello <b>there</b>', at: t0 - 60 },
  }
  const baseMessages = (): TicketMessageItem[] => [
    { id: 'm1', kind: 'customer', author: null, body: '<script>alert(1)</script> help me', file: null, delivery: null, created_at: t0 - 7200 },
    { id: 'm2', kind: 'customer', author: null, body: '', file: { type: 'photo', mime: 'image/jpeg', name: '', size: 12_345, url: `/api/v1/tickets/${tid}/messages/m2/file` }, delivery: null, created_at: t0 - 7100 },
    { id: 'm3', kind: 'customer', author: null, body: 'receipt', file: { type: 'document', mime: 'application/pdf', name: 'receipt.pdf', size: 2_048_000, url: 'https://evil.example/x' }, delivery: null, created_at: t0 - 7000 },
    { id: 'm4', kind: 'staff', author: helper, body: 'We are checking.', file: null, delivery: { status: 'blocked', at: t0 - 3500 }, created_at: t0 - 3600 },
    { id: 'm5', kind: 'note', author: helper, body: 'checked the bank', file: null, delivery: null, created_at: t0 - 3000 },
  ]
  const canned: CannedReply[] = [{ id: 'c1', title: 'Greeting', body: { fa: 'سلام، در خدمتیم.', en: 'Hello, how can we help?' }, sort: 0 }]
  const counts = { waiting: 3, mine: 1, unassigned: 2, answered: 5 }
  const supportPerms = ['overview.read', 'users.read', 'services.read', 'payments.read', 'tickets.write']
  const meOf = (permissions: string[], role = 'support') => ({
    user: { ...helper, role, language: 'fa' },
    permissions,
    csrf: 'csrf-1',
    session: { method: 'link', expires_at: 2_000_000_000 },
    password: { enabled: false },
    password_available: true,
    brand: 'Shop',
  })
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const text = (path: string) => (path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa) as string | undefined) ?? path

  type Answer = (url: string, init?: RequestInit) => Response | Promise<Response> | undefined
  async function boot(path: string, permissions: string[], answer: Answer = () => undefined, ticketPollMs = 60_000) {
    vi.resetModules()
    vi.spyOn(console, 'warn').mockImplementation(() => {}) // texts not merged into the catalogs yet
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        calls.push(`${init?.method ?? 'GET'} ${url} ${typeof init?.body === 'string' ? init.body : ''}`.trim())
        const own = answer(url, init)
        if (own) return own
        if (url === '/api/v1/me') return json(200, meOf(permissions, permissions.includes('tickets.manage') ? 'admin' : 'support'))
        if (url === '/api/v1/tickets/counts') return json(200, counts)
        if (url.startsWith('/api/v1/tickets?')) return json(200, { items: [baseTicket], total: 1, page: 1, size: 25 })
        if (url === `/api/v1/tickets/${tid}`) return json(200, { ticket: baseTicket, messages: baseMessages(), can_manage: false })
        if (url === '/api/v1/tickets/assignees') return json(200, { items: [{ ...helper, role: 'support' }] })
        if (url === '/api/v1/canned-replies') return json(200, { items: canned })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    // The app's own copy of the helpers (after resetModules): poll fast in tests.
    const support = await import('../support')
    support.pollEvery.ticket = ticketPollMs
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { calls, router }
  }

  afterEach(() => {
    // stop this test's app (and its pollers): Vue and PrimeVue are shared between tests
    ;(document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  function type(el: HTMLInputElement | HTMLTextAreaElement, value: string) {
    el.value = value
    el.dispatchEvent(new Event('input'))
  }
  const posts = (calls: string[], path: string) =>
    calls.filter((c) => c.startsWith(`POST /api/v1${path} `)).map((c) => JSON.parse(c.slice(`POST /api/v1${path} `.length)) as Record<string, unknown>)

  it('lists waiting tickets with counts, filters them and opens one', async () => {
    const { calls, router } = await boot('/admin/support', supportPerms)
    await vi.waitFor(() => expect(document.querySelector('.p-datatable-tbody')?.textContent).toContain('#1024'))
    expect(calls).toContain('GET /api/v1/tickets?status=open&page=1&size=25')
    expect(calls).toContain('GET /api/v1/tickets/counts')
    const row = document.querySelector('.p-datatable-tbody tr') as HTMLElement
    expect(row.textContent).toContain('hello <b>there</b>') // the preview is text
    expect(row.querySelector('b')).toBeNull()
    expect(row.textContent).toContain('@cust')
    // tab counts and the menu badge, in Persian digits
    await vi.waitFor(() => expect(document.querySelector('[role=tab] .p-tag')?.textContent).toBe('۳'))
    expect(document.querySelector('.sidebar .badge')?.textContent).toBe('۳')
    // no saved replies tab without tickets.manage; broadcasts not in the menu
    expect(document.querySelectorAll('[role=tab]')).toHaveLength(4)
    expect(document.querySelector('.sidebar')?.textContent).not.toContain(text('section.broadcasts.title'))

    ;(document.querySelectorAll('[role=tab]')[1] as HTMLElement).click()
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/tickets?status=answered&page=1&size=25'))
    expect(router.currentRoute.value.query.status).toBe('answered')

    type(document.querySelector('input[type=search]') as HTMLInputElement, '#۱۰۲۴')
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/tickets?status=answered&q=%231024&page=1&size=25'))

    ;(document.querySelector('.p-datatable-tbody tr td:nth-child(3)') as HTMLElement).click()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('ticket'))
    expect(router.currentRoute.value.params.id).toBe(tid)
  })

  it('names a reseller application in the inbox and on its ticket, and filters the inbox by it', async () => {
    // The bot files an application from its "Become a reseller" button as a ticket of its own category.
    const application: TicketItem = { ...baseTicket, category: 'reseller', subject: 'I run a shop in Tehran' }
    const { calls, router } = await boot('/admin/support', supportPerms, (url) => {
      if (url.startsWith('/api/v1/tickets?')) return json(200, { items: [application], total: 1, page: 1, size: 25 })
      if (url === `/api/v1/tickets/${tid}`) return json(200, { ticket: application, messages: baseMessages(), can_manage: false })
    })
    await vi.waitFor(() => expect(document.querySelector('.p-datatable-tbody')?.textContent).toContain('#1024'))
    // the table names the category, in words (not the raw "reseller")
    const row = document.querySelector('.p-datatable-tbody tr') as HTMLElement
    expect(row.textContent).toContain(text('support.category.reseller'))
    expect(row.textContent).not.toContain('reseller')

    // the filter lists it after the others, and choosing it asks core for that category only
    const selects = document.querySelectorAll('.app-toolbar .p-select')
    expect(selects).toHaveLength(2)
    ;(selects[1] as HTMLElement).click()
    const options = await vi.waitFor(() => {
      const found = [...document.querySelectorAll('.p-select-option')]
      expect(found.length).toBeGreaterThan(0)
      return found
    })
    expect(options.map((o) => o.textContent?.trim())).toEqual([
      text('support.category_all'),
      text('support.category.payment'),
      text('support.category.service'),
      text('support.category.other'),
      text('support.category.reseller'),
    ])
    options[4].dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) // PrimeVue picks on mousedown
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/tickets?status=open&category=reseller&page=1&size=25'))

    // the ticket's page says so too
    ;(document.querySelector('.p-datatable-tbody tr td:nth-child(3)') as HTMLElement).click()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('ticket'))
    await vi.waitFor(() => expect(document.querySelector('.side')?.textContent).toContain(text('support.category.reseller')))
  })

  it('shows a thread safely and replies with request keys', async () => {
    // A small stateful core: replies and notes join the thread.
    let ticket: TicketItem = { ...baseTicket }
    const messages = baseMessages()
    let failNext = true
    const { calls } = await boot(`/admin/support/${tid}`, supportPerms, (url, init) => {
      if (url === `/api/v1/tickets/${tid}` && (init?.method ?? 'GET') === 'GET') return json(200, { ticket, messages, can_manage: false })
      if (url === `/api/v1/tickets/${tid}/messages` && init?.method === 'POST') {
        if (failNext) {
          failNext = false
          return json(409, { error: 'state', message: 'ticket is closed' })
        }
        const b = JSON.parse(String(init.body)) as { body: string; kind: 'reply' | 'note'; close: boolean }
        const message: TicketMessageItem = {
          id: 'n' + messages.length, kind: b.kind === 'note' ? 'note' : 'staff', author: helper, body: b.body, file: null,
          delivery: b.kind === 'note' ? null : { status: 'pending', at: null }, created_at: t0,
        }
        messages.push(message)
        ticket = { ...ticket, status: b.close ? 'closed' : b.kind === 'note' ? ticket.status : 'answered', closed_at: b.close ? t0 : null }
        return json(200, { ticket, message })
      }
    }, 40)

    const thread = () => document.querySelector('.thread') as HTMLElement
    await vi.waitFor(() => expect(thread()?.querySelectorAll('.bubble')).toHaveLength(5))
    // customer text stays text
    expect(thread().textContent).toContain('<script>alert(1)</script> help me')
    expect(thread().querySelector('script')).toBeNull()
    // the photo shows through core; another site's file URL is never used
    const img = thread().querySelector('img') as HTMLImageElement
    expect(img.getAttribute('src')).toBe(`/api/v1/tickets/${tid}/messages/m2/file`)
    expect(img.closest('a')?.getAttribute('href')).toBe(`/api/v1/tickets/${tid}/messages/m2/file`)
    const doc = thread().querySelector('a.file') as HTMLAnchorElement
    expect(doc.getAttribute('href')).toBe(`/api/v1/tickets/${tid}/messages/m3/file`)
    expect(doc.getAttribute('download')).toBe('receipt.pdf')
    expect(doc.textContent).toContain('receipt.pdf')
    // sides, notes and delivery
    expect(thread().querySelectorAll('.bubble.customer')).toHaveLength(3)
    expect(thread().querySelector('.bubble.note')?.textContent).toContain(text('support.note_label'))
    expect(thread().querySelector('.bubble.staff .delivery.blocked')?.getAttribute('aria-label')).toBe(text('support.delivery.blocked'))
    const side = document.querySelector('.side') as HTMLElement
    expect(side.textContent).toContain(text('support.banned'))
    expect(side.textContent).toContain(text('support.blocked_bot'))

    // a saved reply goes in, in the customer's language (fa)
    const select = await vi.waitFor(() => {
      const el = document.querySelector('.composer .canned') as HTMLElement | null
      expect(el).not.toBeNull()
      return el!
    })
    select.click()
    const option = await vi.waitFor(() => {
      const el = [...document.querySelectorAll('.p-select-option')].find((o) => o.textContent?.includes('Greeting')) as HTMLElement | undefined
      expect(el).toBeDefined()
      return el!
    })
    option.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) // PrimeVue picks on mousedown
    const area = document.querySelector('.composer textarea') as HTMLTextAreaElement
    await vi.waitFor(() => expect(area.value).toBe('سلام، در خدمتیم.'))

    // the first send fails (shown), the retry reuses its key
    const send = () => document.querySelector('.composer .send-btn') as HTMLButtonElement
    send().click()
    await vi.waitFor(() => expect(document.querySelector('.composer .p-message-error')?.textContent).toContain('ticket is closed'))
    expect(document.querySelector('.composer .p-message-error')?.textContent).toContain(text('error.state'))
    send().click()
    await vi.waitFor(() => expect(posts(calls, `/tickets/${tid}/messages`)).toHaveLength(2))
    const [first, retry] = posts(calls, `/tickets/${tid}/messages`)
    expect(first).toMatchObject({ body: 'سلام، در خدمتیم.', kind: 'reply', close: false })
    expect(typeof first.key).toBe('string')
    expect(retry.key).toBe(first.key)
    await vi.waitFor(() => expect(area.value).toBe(''))
    await vi.waitFor(() => expect(thread().querySelectorAll('.bubble')).toHaveLength(6))
    expect(thread().querySelector('.bubble:last-child .delivery.pending')).not.toBeNull()

    // a note
    type(area, 'internal only')
    await vi.waitFor(() => expect((document.querySelector('.composer .note-btn') as HTMLButtonElement).disabled).toBe(false))
    ;(document.querySelector('.composer .note-btn') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(posts(calls, `/tickets/${tid}/messages`)).toHaveLength(3))
    const note = posts(calls, `/tickets/${tid}/messages`)[2]
    expect(note).toMatchObject({ body: 'internal only', kind: 'note', close: false })
    expect(note.key).not.toBe(first.key)

    // send and close: the composer gives way to Reopen
    await vi.waitFor(() => expect(area.value).toBe(''))
    type(area, 'Done, bye')
    await vi.waitFor(() => expect((document.querySelector('.composer .close-btn') as HTMLButtonElement).disabled).toBe(false))
    ;(document.querySelector('.composer .close-btn') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(posts(calls, `/tickets/${tid}/messages`)).toHaveLength(4))
    expect(posts(calls, `/tickets/${tid}/messages`)[3]).toMatchObject({ body: 'Done, bye', kind: 'reply', close: true })
    await vi.waitFor(() => expect(document.querySelector('.closed-box')).not.toBeNull())
    expect(document.querySelector('.composer textarea')).toBeNull()

    // the thread refreshes by itself while open
    const reads = () => calls.filter((c) => c === `GET /api/v1/tickets/${tid}`).length
    const before = reads()
    await vi.waitFor(() => expect(reads()).toBeGreaterThan(before + 1))
  })

  it('keeps a reply being written when the ticket is closed meanwhile', async () => {
    let ticket: TicketItem = { ...baseTicket }
    const { calls, router } = await boot(`/admin/support/${tid}`, supportPerms, (url, init) => {
      if (url === `/api/v1/tickets/${tid}` && (init?.method ?? 'GET') === 'GET') return json(200, { ticket, messages: baseMessages(), can_manage: false })
      if (url === `/api/v1/tickets/${tid}/status` && init?.method === 'POST') {
        const b = JSON.parse(String(init.body)) as { status: 'open' | 'closed' }
        ticket = { ...ticket, status: b.status, closed_at: b.status === 'closed' ? t0 : null }
        return json(200, { ticket })
      }
    }, 40)
    const area = await vi.waitFor(() => {
      const el = document.querySelector('.composer textarea') as HTMLTextAreaElement | null
      expect(el).not.toBeNull()
      return el!
    })
    type(area, 'Half a reply')
    // The customer closes it in Telegram; the next refresh sees it.
    ticket = { ...ticket, status: 'closed', closed_at: t0 }
    await vi.waitFor(() => expect(document.querySelector('.closed-box')).not.toBeNull())
    expect(document.querySelector('.composer textarea')).toBeNull()
    expect(document.querySelector('.closed-box')?.textContent).toContain(text('support.draft_kept'))

    // Leaving still asks first; staying keeps it.
    const nav = router.push({ name: 'support' })
    const keep = await vi.waitFor(() => {
      const b = document.querySelector('.p-confirmdialog-reject-button') as HTMLButtonElement | null
      expect(b).not.toBeNull()
      return b!
    })
    keep.click()
    await nav
    expect(router.currentRoute.value.name).toBe('ticket')

    // Reopened: the reply is back in the box.
    ;(document.querySelector('.closed-box button') as HTMLButtonElement).click()
    await vi.waitFor(() => expect((document.querySelector('.composer textarea') as HTMLTextAreaElement | null)?.value).toBe('Half a reply'))
    expect(posts(calls, `/tickets/${tid}/status`)).toEqual([{ status: 'open', reason: '' }])
  })

  // A core that keeps one reply per request key, as the real one does (the
  // key is checked before the closed status). answer decides how the first
  // reply's answer comes back.
  function keyedCore(answer: (reply: () => Response) => Response | Promise<Response>) {
    let ticket: TicketItem = { ...baseTicket }
    const messages = baseMessages()
    const byKey = new Map<string, TicketMessageItem>()
    let first = true
    const core: Answer = (url, init) => {
      if (url === `/api/v1/tickets/${tid}` && (init?.method ?? 'GET') === 'GET') return json(200, { ticket, messages, can_manage: false })
      if (url === `/api/v1/tickets/${tid}/status` && init?.method === 'POST') {
        const b = JSON.parse(String(init.body)) as { status: 'open' | 'closed' }
        ticket = { ...ticket, status: b.status, closed_at: b.status === 'closed' ? t0 : null }
        return json(200, { ticket })
      }
      if (url === `/api/v1/tickets/${tid}/messages` && init?.method === 'POST') {
        const b = JSON.parse(String(init.body)) as { body: string; close: boolean; key: string }
        let message = byKey.get(b.key)
        if (!message) {
          if (ticket.status === 'closed') return json(409, { error: 'state', message: 'the ticket is closed; reopen it to write' })
          message = { id: 'n' + messages.length, kind: 'staff', author: helper, body: b.body, file: null, delivery: { status: 'pending', at: null }, created_at: t0 }
          byKey.set(b.key, message)
          messages.push(message)
          ticket = { ...ticket, status: b.close ? 'closed' : 'answered', closed_at: b.close ? t0 : null }
        }
        const m = message
        const reply = () => json(200, { ticket, message: m })
        if (first) {
          first = false
          return answer(reply)
        }
        return reply()
      }
    }
    const replies = (body: string) => messages.filter((m) => m.kind === 'staff' && m.body === body).length
    return { core, replies }
  }
  // Reply & close; the ticket shows closed, with the text kept.
  async function replyAndClose(body: string) {
    const area = await vi.waitFor(() => {
      const el = document.querySelector('.composer textarea') as HTMLTextAreaElement | null
      expect(el).not.toBeNull()
      return el!
    })
    type(area, body)
    await vi.waitFor(() => expect((document.querySelector('.composer .close-btn') as HTMLButtonElement).disabled).toBe(false))
    ;(document.querySelector('.composer .close-btn') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(document.querySelector('.closed-box')?.textContent).toContain(text('support.draft_kept')))
    // the reply is in the thread already
    await vi.waitFor(() => expect(document.querySelector('.thread .bubble:last-child')?.textContent).toContain(body))
  }
  // Reopen, and Send the kept text: the two sends as they went out.
  async function reopenAndSend(calls: string[], body: string) {
    ;(document.querySelector('.closed-box button') as HTMLButtonElement).click()
    await vi.waitFor(() => expect((document.querySelector('.composer textarea') as HTMLTextAreaElement | null)?.value).toBe(body))
    ;(document.querySelector('.composer .send-btn') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(posts(calls, `/tickets/${tid}/messages`)).toHaveLength(2))
    await vi.waitFor(() => expect((document.querySelector('.composer textarea') as HTMLTextAreaElement).value).toBe(''))
    return posts(calls, `/tickets/${tid}/messages`)
  }

  it('sends a kept reply once when the answer to its first send was lost', async () => {
    // Core saves the reply and closes the ticket, but the answer never arrives.
    const { core, replies } = keyedCore(() => Promise.reject(new TypeError('Failed to fetch')))
    const { calls } = await boot(`/admin/support/${tid}`, supportPerms, core, 40)
    await replyAndClose('All sorted, thanks')
    const [first, again] = await reopenAndSend(calls, 'All sorted, thanks')
    expect(first).toMatchObject({ kind: 'reply', close: true })
    expect(again).toMatchObject({ kind: 'reply', close: false })
    expect(again.key).toBe(first.key)
    expect(replies('All sorted, thanks')).toBe(1)
    expect(document.querySelectorAll('.thread .bubble')).toHaveLength(6)
  })

  it('forgets a reply whose answer came after the ticket showed closed', async () => {
    // The answer is slow: the refresh sees the ticket closed first.
    let release!: () => void
    const slow = new Promise<void>((r) => (release = r))
    const { core, replies } = keyedCore((reply) => slow.then(reply))
    const { calls, router } = await boot(`/admin/support/${tid}`, supportPerms, core, 40)
    await replyAndClose('All sorted, thanks')
    release() // the answer comes to a box that is gone: the reply went out
    await vi.waitFor(() => expect(document.querySelector('.closed-box')?.textContent).toContain(text('support.closed_hint')))
    ;(document.querySelector('.closed-box button') as HTMLButtonElement).click()
    await vi.waitFor(() => expect((document.querySelector('.composer textarea') as HTMLTextAreaElement | null)?.value).toBe(''))
    expect(posts(calls, `/tickets/${tid}/messages`)).toHaveLength(1)
    expect(replies('All sorted, thanks')).toBe(1)
    // nothing unsent: leaving asks nothing
    await router.push({ name: 'support' })
    expect(router.currentRoute.value.name).toBe('support')
  })

  it('closes after Reply & close repeats a Send whose answer was lost', async () => {
    // Core saves the plain reply, but its answer never arrives.
    const { core, replies } = keyedCore(() => Promise.reject(new TypeError('Failed to fetch')))
    const { calls } = await boot(`/admin/support/${tid}`, supportPerms, core, 40)
    const area = await vi.waitFor(() => {
      const el = document.querySelector('.composer textarea') as HTMLTextAreaElement | null
      expect(el).not.toBeNull()
      return el!
    })
    type(area, 'All sorted, thanks')
    await vi.waitFor(() => expect((document.querySelector('.composer .send-btn') as HTMLButtonElement).disabled).toBe(false))
    ;(document.querySelector('.composer .send-btn') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(document.querySelector('.composer .msg')).not.toBeNull())
    expect(area.value).toBe('All sorted, thanks')
    // Reply & close with the same text: core hands back the saved reply, still answered.
    ;(document.querySelector('.composer .close-btn') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(document.querySelector('.closed-box')).not.toBeNull())
    const sends = posts(calls, `/tickets/${tid}/messages`)
    expect(sends).toHaveLength(2)
    expect(sends[1]).toMatchObject({ close: true })
    expect(sends[1].key).toBe(sends[0].key)
    expect(posts(calls, `/tickets/${tid}/status`)).toEqual([{ status: 'closed', reason: '' }])
    expect(replies('All sorted, thanks')).toBe(1)
    expect(document.querySelector('.closed-box')?.textContent).toContain(text('support.closed_hint'))
  })

  it('trusts core’s blocked flag and shows another staff member’s name whole', async () => {
    const boss = { id: 's2', telegram_id: 1002, username: 'boss' }
    // The customer started the bot again after the blocked reply (m4): core says so.
    const ticket: TicketItem = { ...baseTicket, user: { ...customer, status: 'active', bot_blocked: false }, assignee: boss }
    await boot(`/admin/support/${tid}`, [...supportPerms, 'tickets.manage'], (url) => {
      if (url === `/api/v1/tickets/${tid}`) return json(200, { ticket, messages: baseMessages(), can_manage: true })
      if (url === '/api/v1/tickets/assignees') return json(200, { items: [{ ...helper, role: 'admin' }, { ...boss, role: 'owner' }] })
    })
    await vi.waitFor(() => expect(document.querySelectorAll('.thread .bubble')).toHaveLength(5))
    expect(document.querySelector('.side')?.textContent).not.toContain(text('support.blocked_bot'))
    expect(document.body.textContent).not.toContain(text('support.blocked_hint'))
    // isolated, so the Persian page cannot show it as "boss@"
    await vi.waitFor(() => expect(document.querySelector('.assignee .p-select-label')?.textContent).toBe('⁨@boss⁩'))
  })

  it('starts no refresh for a thread left during its first load', async () => {
    // Each running poller listens for the tab showing again: count those listeners.
    const listening = new Set<unknown>()
    const add = document.addEventListener.bind(document)
    const remove = document.removeEventListener.bind(document)
    vi.spyOn(document, 'addEventListener').mockImplementation((type: string, fn: EventListenerOrEventListenerObject, opts?: boolean | AddEventListenerOptions) => {
      if (type === 'visibilitychange') listening.add(fn)
      add(type, fn, opts)
    })
    vi.spyOn(document, 'removeEventListener').mockImplementation((type: string, fn: EventListenerOrEventListenerObject, opts?: boolean | EventListenerOptions) => {
      if (type === 'visibilitychange') listening.delete(fn)
      remove(type, fn, opts)
    })
    let release!: () => void
    const slow = new Promise<void>((r) => (release = r))
    const { calls, router } = await boot(`/admin/support/${tid}`, supportPerms, (url) => {
      if (url === `/api/v1/tickets/${tid}`) return slow.then(() => json(200, { ticket: baseTicket, messages: baseMessages(), can_manage: false }))
    }, 20)
    await vi.waitFor(() => expect(calls).toContain(`GET /api/v1/tickets/${tid}`))
    // Back to the inbox before the thread arrives.
    await router.push({ name: 'support' })
    await vi.waitFor(() => expect(document.querySelector('.p-datatable-tbody')?.textContent).toContain('#1024'))
    const before = listening.size
    release()
    await new Promise((r) => setTimeout(r, 100))
    expect(listening.size).toBe(before)
  })

  it('manages saved replies for those who may', async () => {
    const { calls } = await boot('/admin/support?tab=canned', [...supportPerms, 'tickets.manage'], (url, init) => {
      if (url === '/api/v1/canned-replies' && init?.method === 'POST') return json(200, { item: { id: 'c2' } })
      if (url === '/api/v1/canned-replies/c1' && init?.method === 'DELETE') return new Response(null, { status: 204 })
    })
    await vi.waitFor(() => expect(document.querySelector('.canned .p-datatable-tbody')?.textContent).toContain('Greeting'))
    expect(document.querySelectorAll('[role=tab]')).toHaveLength(5)
    expect(calls.some((c) => c.startsWith('GET /api/v1/tickets?'))).toBe(false) // the inbox waits until shown

    const newButton = [...document.querySelectorAll('button')].find((b) => b.textContent?.includes(text('support.canned.new'))) as HTMLButtonElement
    newButton.click()
    const dialog = await vi.waitFor(() => {
      const d = document.querySelector('.p-dialog') as HTMLElement | null
      expect(d).not.toBeNull()
      return d!
    })
    const save = () => [...dialog.querySelectorAll('.p-dialog-footer button')].pop() as HTMLButtonElement
    expect(save().disabled).toBe(true)
    type(dialog.querySelector('input:not([inputmode])') as HTMLInputElement, ' Thanks ')
    type(dialog.querySelector('textarea[lang="fa"]') as HTMLTextAreaElement, 'ممنون از صبر شما')
    type(dialog.querySelector('input[inputmode="numeric"]') as HTMLInputElement, '۲')
    await vi.waitFor(() => expect(save().disabled).toBe(false))
    save().click()
    await vi.waitFor(() => expect(posts(calls, '/canned-replies')).toEqual([{ title: 'Thanks', body: { fa: 'ممنون از صبر شما', en: '' }, sort: 2 }]))

    ;(document.querySelector('.canned .pi-trash')?.closest('button') as HTMLButtonElement).click()
    const accept = await vi.waitFor(() => {
      const b = document.querySelector('.p-confirmdialog-accept-button') as HTMLButtonElement | null
      expect(b).not.toBeNull()
      return b!
    })
    expect(calls.some((c) => c.startsWith('DELETE'))).toBe(false)
    accept.click()
    await vi.waitFor(() => expect(calls).toContain('DELETE /api/v1/canned-replies/c1'))
  })

  it('lists a customer’s tickets on their page', async () => {
    const user = { id: cid, telegram_id: 777, username: 'cust', language: 'fa', role: 'user', status: 'active', created_at: t0 - 86_400, wallets: [] }
    const { calls } = await boot(`/admin/users/${cid}?tab=tickets`, supportPerms, (url) => {
      if (url === `/api/v1/users/${cid}`) {
        return json(200, { user, counts: { subscriptions: 0, orders: 0, tickets: 2 }, referral: { referred_by: null, invited: 0, rewarded: 0, earned: [] }, can_ban: false })
      }
    })
    await vi.waitFor(() => expect(calls).toContain(`GET /api/v1/tickets?user=${cid}&page=1&size=10`))
    await vi.waitFor(() => expect([...document.querySelectorAll('.p-tabpanels .p-datatable-tbody')].some((b) => b.textContent?.includes('#1024'))).toBe(true))
    expect(document.querySelector('[role=tab][aria-selected=true]')?.textContent).toContain('۲')
  })

  it('shows the waiting tickets on the overview', async () => {
    const overview = {
      users: { total: 1, new_24h: 0, new_7d: 0 }, services: { active: 0, expiring: 0, ended: 0 }, orders: { paid_24h: 0, provision_failed: 0 },
      revenue: { day: [], month: [] }, pending_payments: 0, panel: { healthy: true, detail: '' }, recent_orders: [],
      tickets: { waiting: 4, oldest_waiting_at: Math.floor(Date.now() / 1000) - 2 * 3600 - 60 },
    }
    await boot('/admin/', supportPerms, (url) => (url === '/api/v1/overview' ? json(200, overview) : undefined))
    const card = await vi.waitFor(() => {
      const a = document.querySelector('a.card-link') as HTMLAnchorElement | null
      expect(a).not.toBeNull()
      return a!
    })
    expect(card.getAttribute('href')).toBe('/admin/support?status=open')
    expect(card.textContent).toContain('۴')
    // the oldest wait shows once the texts are in the catalog
    if (text('overview.tickets_oldest') !== 'overview.tickets_oldest') expect(card.textContent).toContain('۲ ساعت')
  })
})
