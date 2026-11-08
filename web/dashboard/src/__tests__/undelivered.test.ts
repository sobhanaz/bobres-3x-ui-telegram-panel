import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { canMove, inTime, moveChoices, moveState, serverName } from '../undelivered'
import type { OrderItem, UndeliveredServer } from '../types'

// Undelivered orders (docs/04-dashboard.md, "Undelivered orders"): the Payments page's Undelivered tab (the list,
// Retry all, Deliver on another server, Refund with a note), the same dialogs on the order page, and the Overview's
// Failed deliveries card that opens the tab. Booted like the real app against a fake core that keeps the orders and
// answers the calls the pages make.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
const q = <T extends Element = HTMLElement>(selector: string) => document.querySelector<T>(selector)
const all = (selector: string) => [...document.querySelectorAll<HTMLElement>(selector)]
const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replaceAll('{' + k + '}', v), s)
const plural = (s: string, n: number) => fill(s.split('|')[n === 1 ? 0 : 1].trim(), { n: String(n) })
// the app is slow to boot and to answer when the machine is busy: wait longer than vitest's second
const until = <T>(check: () => T) => vi.waitFor(check, { timeout: 8000 })
const found = async <T extends Element = HTMLElement>(selector: string) =>
  until(() => {
    const el = document.querySelector<T>(selector)
    expect(el, selector).not.toBeNull()
    return el!
  })

const NOW = Math.floor(Date.now() / 1000)
const oid = (n: number) => `0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f6${n}`
const S1 = 'aaaaaaaa-1111-4222-8333-444444444441'
const S2 = 'aaaaaaaa-1111-4222-8333-444444444442'
const S3 = 'aaaaaaaa-1111-4222-8333-444444444443'
const S4 = 'aaaaaaaa-1111-4222-8333-444444444444'
const SERVERS: UndeliveredServer[] = [
  { id: S1, name: 'Frankfurt', healthy: true, checked: true, error: '', full: false },
  { id: S2, name: 'Amsterdam', healthy: false, checked: true, error: 'dial tcp: i/o timeout', full: false },
  { id: S3, name: 'Helsinki', healthy: false, checked: false, error: '', full: false },
  { id: S4, name: 'Paris', healthy: true, checked: true, error: '', full: true },
]

const order = (n: number, over: Partial<OrderItem> = {}): OrderItem => ({
  id: oid(n), user: { id: 'u' + n, telegram_id: 500 + n, username: 'buyer' + n }, plan: { id: 'p1', name: { en: 'Monthly', fa: 'ماهانه' } }, type: 'new',
  status: 'provision_failed', amount: { amount: 250_000, currency: 'IRT' }, discount: null, subscription_id: null, created_at: NOW - 7200, updated_at: NOW - 60,
  attempts: 4, last_error: 'panel Frankfurt: context deadline exceeded while adding the client', next_attempt_at: NOW + 600, staff: '', extend: null, refund: null,
  list: null, seller: null, server_id: null, ...over,
})

const OWNER = ['overview.read', 'payments.read', 'payments.review', 'users.read', 'resellers.write', 'audit.read']
const SUPPORT = ['overview.read', 'payments.read', 'users.read']

/** The fake core: the undelivered orders as it keeps them, what it was asked, and how it answers. */
class Core {
  orders: OrderItem[] = [
    order(1, { seller: { kind: 'referrer', user: { id: 'r1', telegram_id: 900, username: 'agent' } } }),
    order(2, { type: 'renew', subscription_id: 'eeeeeeee-1111-4222-8333-444444444440', created_at: NOW - 3600, attempts: 1, last_error: '', next_attempt_at: NOW - 5 }),
    order(3, { seller: { kind: 'reseller', user: { id: 'r2', telegram_id: 901, username: 'shop2' } }, server_id: S1, created_at: NOW - 600 }),
  ]
  servers = SERVERS
  serversError = ''
  calls: string[] = []
  overview: Record<string, unknown> = {
    users: { total: 1, new_24h: 0, new_7d: 0 }, services: { active: 0, expiring: 0, ended: 0 }, orders: { paid_24h: 0, provision_failed: 3 },
    pending_payments: 0, panel: { healthy: true, detail: '' }, recent_orders: [],
  }
  move: (id: string, body: { server_id: string }) => Response = (id, body) => {
    const o = this.orders.find((x) => x.id === id)!
    o.server_id = body.server_id
    return json(200, { order: o, leftover: false })
  }
  answer(url: string, init?: RequestInit): Response | undefined {
    const method = init?.method ?? 'GET'
    const body = typeof init?.body === 'string' ? (JSON.parse(init.body) as Record<string, string>) : {}
    if (url === '/api/v1/overview') return json(200, this.overview)
    if (method === 'GET' && url.startsWith('/api/v1/orders/undelivered?')) {
      const size = Number(new URL(url, 'https://x.example').searchParams.get('size'))
      return json(200, { items: this.orders.slice(0, size), total: this.orders.length, page: 1, size, servers: this.servers, servers_error: this.serversError })
    }
    if (method === 'POST' && url === '/api/v1/orders/undelivered/retry') return json(200, { count: this.orders.length })
    const one = /^\/api\/v1\/orders\/([^/?]+)(\/(?:retry|move|refund))?$/.exec(url)
    if (!one) return undefined
    const o = this.orders.find((x) => x.id === one[1])
    if (!o) return json(404, { error: 'not_found', message: 'no such order' })
    if (method === 'GET' && !one[2]) {
      return json(200, { order: o, payments: [], payments_error: '', can: { retry: o.status === 'provision_failed', refund: true, move: canMove(o) } })
    }
    if (one[2] === '/retry') return json(200, { order: o })
    if (one[2] === '/move') return this.move(o.id, body as { server_id: string })
    if (one[2] === '/refund') {
      this.orders = this.orders.filter((x) => x.id !== o.id)
      return json(200, { order: { ...o, status: 'cancelled' }, wallet: { amount: 250_000, currency: 'IRT' }, commission_taken: { amount: 25_000, currency: 'IRT' } })
    }
    return undefined
  }
  sent(method: string, path: string): Record<string, unknown>[] {
    const head = `${method} ${path} `
    return this.calls.filter((c) => c.startsWith(head)).map((c) => JSON.parse(c.slice(head.length)) as Record<string, unknown>)
  }
  asked(prefix: string): string[] {
    return this.calls.filter((c) => c.startsWith(prefix))
  }
}

async function boot(core: Core, path: string, env: { lang?: 'en' | 'fa'; perms?: string[] } = {}) {
  unmount()
  const lang = env.lang ?? 'en'
  localStorage.setItem('bobres.lang', lang)
  vi.resetModules()
  window.history.replaceState(null, '', path)
  document.body.innerHTML = '<div id="app"></div>'
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      core.calls.push(`${init?.method ?? 'GET'} ${url} ${typeof init?.body === 'string' ? init.body : ''}`.trim())
      const own = core.answer(url, init)
      if (own) return own
      if (url === '/api/v1/me') {
        return json(200, {
          user: { id: 'u0', telegram_id: 1, username: 'boss', role: 'owner', language: lang }, permissions: env.perms ?? OWNER, csrf: 'c',
          session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
        })
      }
      if (url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null })
      if (url.startsWith('/api/v1/payments') || url.startsWith('/api/v1/orders?')) return json(200, { items: [], total: 0, page: 1, size: 25 })
      return json(503, { error: 'unavailable', message: 'not in this test' })
    }),
  )
  await import('../main')
  const { router } = await import('../router')
  await router.isReady()
  return { router }
}

afterEach(() => {
  unmount()
  try {
    localStorage.removeItem('bobres.lang')
  } catch {
    /* nothing was stored */
  }
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

function type(el: Element | null, value: string) {
  const input = el as HTMLInputElement | HTMLTextAreaElement
  input.value = value
  input.dispatchEvent(new Event('input'))
}
const buttonIn = (root: ParentNode, label: string) => {
  const hit = [...root.querySelectorAll('button')].find((b) => text(b) === label || b.getAttribute('aria-label') === label)
  if (!hit) throw new Error(`no button "${label}" in: ${[...root.querySelectorAll('button')].map((b) => text(b) || b.getAttribute('aria-label')).join(' | ')}`)
  return hit as HTMLButtonElement
}
const dialog = () => found('.p-dialog')
const confirmButton = () => [...document.querySelectorAll<HTMLButtonElement>('.p-dialog-footer button')].pop()!
const rows = () => all('.undelivered .p-datatable-tbody tr:not(.p-datatable-empty-message)')
const toasts = () => text(q('.p-toast'))

describe('the helpers', () => {
  it('read a server the way the move dialog shows it', () => {
    expect(SERVERS.map(moveState)).toEqual(['ok', 'down', 'unknown', 'full'])
    // full wins over health: a full server takes no new client however it answers
    expect(moveState({ healthy: false, checked: true, full: true })).toBe('full')
    expect(moveChoices(SERVERS).map((s) => s.name)).toEqual(['Frankfurt', 'Helsinki', 'Amsterdam', 'Paris'])
    expect(serverName(S2, SERVERS)).toBe('Amsterdam')
    expect(serverName('nope', SERVERS)).toBe('')
    expect(serverName(null, SERVERS)).toBe('')
  })
  it('move only a new service that waits', () => {
    expect(canMove(order(1))).toBe(true)
    expect(canMove(order(1, { subscription_id: 'x' }))).toBe(false)
    expect(canMove(order(1, { status: 'active' }))).toBe(false)
  })
  it('say how long until the next try, and nothing once it is due', () => {
    const now = Date.UTC(2026, 10, 5, 12) // ms
    const at = now / 1000
    expect(inTime(at + 600, now, 'en')).toBe('in 10 minutes')
    expect(inTime(at + 30, now, 'en')).toBe('in 30 seconds')
    expect(inTime(at + 3 * 3600, now, 'en')).toBe('in 3 hours')
    expect(inTime(at, now, 'en')).toBe('')
    expect(inTime(at - 60, now, 'en')).toBe('')
    expect(inTime(at + 600, now, 'fa')).toBe('۱۰ دقیقه بعد')
  })
})

describe('the Undelivered tab', () => {
  it('lists the undelivered orders with their buyer, seller, tries, error and server, and counts them on the tab', async () => {
    const core = new Core()
    await boot(core, '/admin/payments?tab=undelivered')
    await until(() => expect(rows()).toHaveLength(3))
    expect(core.calls).toContain('GET /api/v1/orders/undelivered?page=1&size=25')
    const tab = q('.undelivered-tab')!
    expect(text(tab)).toContain(en.undelivered.tab)
    expect(text(tab.querySelector('.undelivered-count'))).toBe('3')
    expect(text(q('.p-tablist'))).toContain(en.undelivered.tab)
    expect(tab.getAttribute('aria-selected')).toBe('true')

    const [first, renewal, moved] = rows()
    // the buyer and the reseller who brought them, linked to the reseller's page
    expect(text(first)).toContain('@buyer1')
    expect(first.querySelector('a[href="/admin/users/u1"]')).not.toBeNull()
    expect(first.querySelector('.seller-link')?.getAttribute('href')).toBe('/admin/resellers/r1')
    expect(text(first)).toContain(en.order.seller_kind.referrer)
    expect(text(first)).toContain('Monthly')
    expect(text(first)).toContain(en.order.type.new)
    expect(text(first)).toContain('250,000 Toman')
    expect(text(first)).toContain('4 attempts')
    expect(text(first)).toContain(fill(en.order.next_attempt, { time: 'in 10 minutes' }))
    expect(text(first)).toContain(en.order.server_plan)
    // the plan opens the order, for the keyboard too
    expect(first.querySelector('a.plan')?.getAttribute('href')).toBe(`/admin/payments/orders/${oid(1)}`)
    // the error is cut to a line, whole in its title and when opened
    const err = first.querySelector<HTMLButtonElement>('.last-error')!
    expect(err.getAttribute('title')).toBe(order(1).last_error)
    expect(err.getAttribute('aria-expanded')).toBe('false')
    err.click()
    await until(() => expect(err.getAttribute('aria-expanded')).toBe('true'))
    expect(err.classList.contains('open')).toBe(true)
    // "waiting since": how long ago, the date in the title
    expect(text(first.querySelector('.waiting'))).toBe('2 hours ago')
    expect(first.querySelector('.waiting')?.getAttribute('title')).toBeTruthy()

    // the renewal stays on its service's server: no move; its try is due
    expect(text(renewal)).toContain(en.order.type.renew)
    expect(text(renewal)).toContain(en.order.seller_store)
    expect(text(renewal)).toContain(en.undelivered.next_now)
    expect(renewal.querySelector('.act-move')).toBeNull()
    expect(renewal.querySelector('.act-retry')).not.toBeNull()
    expect(renewal.querySelector('.act-refund')).not.toBeNull()
    expect(first.querySelector('.act-move')).not.toBeNull()

    // a reseller buying for a customer, moved to a server by staff
    expect(text(moved)).toContain(en.order.seller_kind.reseller)
    expect(text(moved.querySelector('.server'))).toBe('Frankfurt')
  }, 30000)

  it('says when every paid order is delivered, and cannot retry nothing', async () => {
    const core = new Core()
    core.orders = []
    await boot(core, '/admin/payments?tab=undelivered')
    await until(() => expect(text(q('.undelivered .empty'))).toBe(en.undelivered.empty))
    expect(q('.undelivered-count')).toBeNull()
    expect(q<HTMLButtonElement>('.retry-all')!.disabled).toBe(true)
  }, 30000)

  it('loads with the page for the count on the tab, and keeps the tab in the address', async () => {
    const core = new Core()
    const { router } = await boot(core, '/admin/payments')
    await until(() => expect(text(q('.undelivered-count'))).toBe('3'))
    expect(core.asked('GET /api/v1/orders/undelivered')).toHaveLength(1)
    q<HTMLElement>('.undelivered-tab')!.click()
    await until(() => expect(router.currentRoute.value.query.tab).toBe('undelivered'))
    // opened after loading with the page: not asked again
    expect(core.asked('GET /api/v1/orders/undelivered')).toHaveLength(1)
  }, 30000)

  it('retries one order now', async () => {
    const core = new Core()
    await boot(core, '/admin/payments?tab=undelivered')
    await until(() => expect(rows()).toHaveLength(3))
    rows()[0].querySelector<HTMLButtonElement>('.act-retry')!.click()
    await until(() => expect(core.calls).toContain(`POST /api/v1/orders/${oid(1)}/retry`))
    await until(() => expect(toasts()).toContain(en.order.retry_started))
    await until(() => expect(core.asked('GET /api/v1/orders/undelivered')).toHaveLength(2))
  }, 30000)

  it('retries all of them after a confirmation, and says how many', async () => {
    const core = new Core()
    await boot(core, '/admin/payments?tab=undelivered')
    await until(() => expect(rows()).toHaveLength(3))
    q<HTMLButtonElement>('.retry-all')!.click()
    const ask = await found('.p-confirmdialog')
    expect(text(ask)).toContain(plural(en.undelivered.retry_all_confirm, 3))
    expect(core.asked('POST /api/v1/orders/undelivered/retry')).toHaveLength(0) // nothing before the confirmation
    q<HTMLButtonElement>('.p-confirmdialog-accept-button')!.click()
    await until(() => expect(core.asked('POST /api/v1/orders/undelivered/retry')).toHaveLength(1))
    await until(() => expect(toasts()).toContain(plural(en.undelivered.retry_all_done, 3)))
    await until(() => expect(core.asked('GET /api/v1/orders/undelivered')).toHaveLength(2))
  }, 30000)

  it('delivers a new service on the server picked, with the reason', async () => {
    const core = new Core()
    await boot(core, '/admin/payments?tab=undelivered')
    await until(() => expect(rows()).toHaveLength(3))
    rows()[0].querySelector<HTMLButtonElement>('.act-move')!.click()
    const d = await dialog()
    expect(text(d.querySelector('.p-dialog-header'))).toContain(en.order.move)
    expect(text(d)).toContain(en.order.move_intro)
    // every server, the working ones first, each with its state
    const options = [...d.querySelectorAll<HTMLElement>('.p-listbox-option')]
    expect(options.map((o) => text(o.querySelector('.name')))).toEqual(['Frankfurt', 'Helsinki', 'Amsterdam', 'Paris'])
    expect(options.map((o) => text(o.querySelector('.state')))).toEqual([
      en.server.health_state.ok, en.server.health_state.unknown, en.server.health_state.down, en.order.move_state_full,
    ])
    // nothing picked, no reason: no confirm
    expect(confirmButton().disabled).toBe(true)
    // a server that did not answer: a warning with its error
    options[2].click()
    await until(() => expect(text(d.querySelector('.server-warn'))).toContain(en.order.move_warn.down))
    expect(text(d.querySelector('.server-warn'))).toContain('dial tcp: i/o timeout')
    options[0].click()
    await until(() => expect(d.querySelector('.server-warn')).toBeNull())
    expect(confirmButton().disabled).toBe(true) // the reason is required
    type(d.querySelector('textarea'), 'Frankfurt answers again')
    await until(() => expect(confirmButton().disabled).toBe(false))
    expect(text(confirmButton())).toBe(en.order.move_confirm)
    confirmButton().click()
    await until(() => expect(core.sent('POST', `/api/v1/orders/${oid(1)}/move`)).toEqual([{ server_id: S1, reason: 'Frankfurt answers again' }]))
    await until(() => expect(toasts()).toContain(fill(en.order.move_done, { server: '⁨Frankfurt⁩' })))
    await until(() => expect(q('.p-dialog')).toBeNull())
    await until(() => expect(core.asked('GET /api/v1/orders/undelivered')).toHaveLength(2))
  }, 30000)

  it('warns, and keeps the warning, when a half-made account may be left on the old server', async () => {
    const core = new Core()
    core.move = (id) => json(200, { order: core.orders.find((o) => o.id === id), leftover: true, warning: 'moved; the old server…' })
    await boot(core, '/admin/payments?tab=undelivered')
    await until(() => expect(rows()).toHaveLength(3))
    rows()[0].querySelector<HTMLButtonElement>('.act-move')!.click()
    const d = await dialog()
    d.querySelector<HTMLElement>('.p-listbox-option')!.click()
    type(d.querySelector('textarea'), 'move it')
    await until(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await until(() => expect(toasts()).toContain(en.order.move_leftover))
    expect(q('.p-toast-message-warn')).not.toBeNull()
  }, 30000)

  it('says when the servers could not be listed', async () => {
    const core = new Core()
    core.servers = []
    core.serversError = 'the provisioner did not answer'
    await boot(core, '/admin/payments?tab=undelivered')
    await until(() => expect(rows()).toHaveLength(3))
    rows()[0].querySelector<HTMLButtonElement>('.act-move')!.click()
    const d = await dialog()
    expect(text(d.querySelector('.servers-error'))).toContain(en.order.move_servers_error)
    expect(text(d.querySelector('.servers-error'))).toContain('the provisioner did not answer')
    expect(text(d)).toContain(en.order.move_none)
  }, 30000)

  it('refunds with a note to the buyer, and says what was taken back from the reseller', async () => {
    const core = new Core()
    await boot(core, '/admin/payments?tab=undelivered')
    await until(() => expect(rows()).toHaveLength(3))
    rows()[0].querySelector<HTMLButtonElement>('.act-refund')!.click()
    const d = await dialog()
    expect(text(d)).toContain(fill(en.order.refund_intro, { amount: '250,000 Toman' }))
    // the reseller who brought the buyer loses the commission on a sale that never happened
    expect(text(d.querySelector('.commission'))).toBe(fill(en.order.refund_commission_named, { who: '⁨@agent⁩' }))
    expect(text(d)).toContain(en.order.refund_note)
    expect(text(d)).toContain(en.order.refund_note_hint)
    const [note, reason] = [...d.querySelectorAll('textarea')]
    expect(note.getAttribute('maxlength')).toBe('500')
    type(note, 'Sorry, the server was down. Your money is in your wallet.')
    await until(() => expect(text(d.querySelector('.counter'))).toBe('57 / 500'))
    type(reason, 'server down for a day')
    await until(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await until(() =>
      expect(core.sent('POST', `/api/v1/orders/${oid(1)}/refund`)).toEqual([{ reason: 'server down for a day', note: 'Sorry, the server was down. Your money is in your wallet.' }]),
    )
    await until(() => expect(toasts()).toContain(fill(en.order.refund_done, { balance: '250,000 Toman' })))
    expect(toasts()).toContain(fill(en.order.refund_commission_taken, { amount: '25,000 Toman' }))
    // the list is read again: the refunded order is gone
    await until(() => expect(rows()).toHaveLength(2))
  }, 30000)

  it('sends an empty note when none is written, and names no reseller when none brought the buyer', async () => {
    const core = new Core()
    await boot(core, '/admin/payments?tab=undelivered')
    await until(() => expect(rows()).toHaveLength(3))
    rows()[1].querySelector<HTMLButtonElement>('.act-refund')!.click()
    const d = await dialog()
    expect(text(d.querySelector('.commission'))).toBe(en.order.refund_commission)
    type(d.querySelectorAll('textarea')[1], 'renewal never applied')
    await until(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await until(() => expect(core.sent('POST', `/api/v1/orders/${oid(2)}/refund`)).toEqual([{ reason: 'renewal never applied', note: '' }]))
  }, 30000)

  it('lists them for support too, without the actions', async () => {
    const core = new Core()
    await boot(core, '/admin/payments?tab=undelivered', { perms: SUPPORT })
    await until(() => expect(rows()).toHaveLength(3))
    expect(q('.retry-all')).toBeNull()
    expect(q('.act-retry, .act-move, .act-refund')).toBeNull()
    // the reseller is linked to the user page for those who do not manage resellers
    expect(rows()[0].querySelector('.seller-link')).toBeNull()
    expect(rows()[0].querySelector('a[href="/admin/users/r1"]')).not.toBeNull()
  }, 30000)

  it('reads in Persian, right to left', async () => {
    const core = new Core()
    await boot(core, '/admin/payments?tab=undelivered', { lang: 'fa' })
    await until(() => expect(rows()).toHaveLength(3))
    expect(document.documentElement.dir).toBe('rtl')
    expect(text(q('.undelivered-tab'))).toContain(fa.undelivered.tab)
    expect(text(q('.undelivered-count'))).toBe('۳')
    expect(text(rows()[0])).toContain(fa.order.seller_kind.referrer)
    expect(text(rows()[0])).toContain('۴ تلاش')
    // the error stays left to right inside the Persian page
    expect(rows()[0].querySelector('.last-error')?.classList.contains('app-ltr')).toBe(true)
  }, 30000)
})

describe('the order page', () => {
  it('shows the seller and the server chosen, and delivers on another server', async () => {
    const core = new Core()
    await boot(core, `/admin/payments/orders/${oid(3)}`)
    const facts = await found('.app-facts')
    await until(() => expect(text(facts)).toContain(en.order.seller_kind.reseller))
    expect(text(facts.querySelector('.seller'))).toContain('@shop2')
    // the chosen server's name, read from the servers a move offers
    await until(() => expect(text(facts.querySelector('.chosen-server'))).toContain('Frankfurt'))
    expect(text(facts.querySelector('.chosen-server'))).toContain(en.order.server_chosen)
    expect(core.calls).toContain('GET /api/v1/orders/undelivered?page=1&size=1')

    buttonIn(document, en.order.move).click()
    const d = await dialog()
    // the server it was moved to before is picked already
    await until(() => expect(d.querySelector('.p-listbox-option-selected .name')?.textContent).toBe('Frankfurt'))
    d.querySelectorAll<HTMLElement>('.p-listbox-option')[1].click() // Helsinki: not checked yet
    await until(() => expect(text(d.querySelector('.server-warn'))).toContain(en.order.move_warn.unknown))
    type(d.querySelector('textarea'), 'try Helsinki')
    await until(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await until(() => expect(core.sent('POST', `/api/v1/orders/${oid(3)}/move`)).toEqual([{ server_id: S3, reason: 'try Helsinki' }]))
    // the page is read again
    await until(() => expect(core.asked(`GET /api/v1/orders/${oid(3)}`)).toHaveLength(2))
  }, 30000)

  it('offers no move for a renewal, nor to those who may not review payments', async () => {
    const core = new Core()
    await boot(core, `/admin/payments/orders/${oid(2)}`)
    await until(() => expect(text(q('.app-facts'))).toContain(en.order.type.renew))
    await until(() => expect(text(q('.app-actions'))).toContain(en.order.retry))
    expect(q('.app-actions .move')).toBeNull()
    expect(text(q('.app-facts'))).not.toContain(en.order.seller)
    // nothing to read for a move or a chosen server
    expect(core.asked('GET /api/v1/orders/undelivered')).toHaveLength(0)

    await boot(core, `/admin/payments/orders/${oid(1)}`, { perms: SUPPORT })
    await until(() => expect(text(q('.app-facts'))).toContain('@agent'))
    expect(q('.app-actions .move')).toBeNull()
    expect(text(q('.app-actions'))).not.toContain(en.order.refund)
  }, 30000)

  it('refunds with a note on the order page too', async () => {
    const core = new Core()
    await boot(core, `/admin/payments/orders/${oid(1)}`)
    await until(() => expect(q('.app-actions .move')).not.toBeNull())
    buttonIn(document, en.order.refund).click()
    const d = await dialog()
    const [note, reason] = [...d.querySelectorAll('textarea')]
    type(note, 'Back in your wallet')
    type(reason, 'customer asked')
    await until(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await until(() => expect(core.sent('POST', `/api/v1/orders/${oid(1)}/refund`)).toEqual([{ reason: 'customer asked', note: 'Back in your wallet' }]))
  }, 30000)
})

describe('the Overview', () => {
  it('opens the Undelivered tab from the Failed deliveries card', async () => {
    const core = new Core()
    const { router } = await boot(core, '/admin/')
    const card = await found<HTMLAnchorElement>('a.failed-card')
    expect(text(card.querySelector('.p-card-subtitle'))).toBe(en.overview.failed)
    await until(() => expect(text(card.querySelector('.big'))).toBe('3'))
    expect(card.getAttribute('href')).toBe('/admin/payments?tab=undelivered')
    card.click()
    await until(() => expect(router.currentRoute.value.name).toBe('payments'))
    expect(router.currentRoute.value.query.tab).toBe('undelivered')
    await until(() => expect(rows()).toHaveLength(3))
  }, 30000)

  it('shows the card, not a link, to those who cannot open the payments', async () => {
    const core = new Core()
    await boot(core, '/admin/', { perms: ['overview.read'] })
    const card = await found('.failed-card')
    expect(card.tagName).toBe('DIV')
    expect(card.getAttribute('href')).toBeNull()
  }, 30000)
})

describe('the audit log and the ledger', () => {
  const catalogs = { en, fa } as Record<string, Record<string, unknown>>
  const at = (cat: Record<string, unknown>, key: string) => key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], cat)
  it('name the new actions and the new ledger kind', () => {
    for (const [lang, cat] of Object.entries(catalogs)) {
      for (const key of ['audit.action.order.move', 'audit.action.order.retry_all', 'audit.action.order.commission_reversal', 'ledger.kind.commission_reversal']) {
        expect(typeof at(cat, key), `${lang}: ${key}`).toBe('string')
      }
    }
    expect(en.audit.action.order.commission_reversal).toBe('Reseller commission taken back')
  })
  it('offers the commission taken back in the ledger filter', () => {
    const [page] = Object.values(import.meta.glob('../pages/LedgerPage.vue', { query: '?raw', import: 'default', eager: true }) as Record<string, string>)
    expect(page).toContain("'commission_reversal'")
  })
})
