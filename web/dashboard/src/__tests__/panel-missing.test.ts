import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { dateTime } from '../format'
import type { ServiceItem } from '../types'

// A service the panel has no client for (an operator deleted it there): the badge and the filter on the
// Services page, the warning and "Make the client again" on the service's page, and the card on the Overview.
// Booted like the real app against a fake core that keeps the services and answers the calls the pages make.
// (The "×12" of the Monitoring events is in monitoring.test.ts.)

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
const q = <T extends Element = HTMLElement>(selector: string) => document.querySelector<T>(selector)
const all = (selector: string) => [...document.querySelectorAll<HTMLElement>(selector)]
const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replaceAll('{' + k + '}', v), s)
// the app is slow to boot and to answer when the machine is busy: wait longer than vitest's second
const until = <T>(check: () => T) => vi.waitFor(check, { timeout: 8000 })
const found = async <T extends Element>(selector: string) =>
  until(() => {
    const el = document.querySelector<T>(selector)
    expect(el, selector).not.toBeNull()
    return el!
  })

const SINCE = 1_790_000_000 // when the panel was first seen without the client
const sid = (n: number) => `eeeeeeee-1111-4222-8333-44444444444${n}`
const service = (n: number, over: Partial<ServiceItem> = {}): ServiceItem => ({
  id: sid(n), user: { id: 'u' + n, telegram_id: 100 + n, username: 'cust' + n }, plan: { id: 'p1', name: { fa: 'ماهانه', en: 'Monthly' } }, status: 'active',
  client_email: 'cust' + n + '-abc', sub_link: 'https://shop.example.com/sub/abc' + n, expires_at: 1_795_000_000, traffic_total: 50 * 2 ** 30, traffic_used: 5 * 2 ** 30,
  last_synced_at: 1_790_100_000, created_at: 1_780_000_000, ...over,
})

const OWNER = ['overview.read', 'services.read', 'services.extend', 'services.write', 'audit.read']
const SUPPORT = ['overview.read', 'services.read', 'services.extend']

/** The fake core: the services as it keeps them, what it was asked, and how it answers a "make the client again". */
class Core {
  // the first is fine, the second is not on the panel, the third carries an explicit null (the field is there, empty)
  services: ServiceItem[] = [service(1), service(2, { panel_missing_since: SINCE }), service(3, { panel_missing_since: null })]
  calls: string[] = []
  overview: Record<string, unknown> = {}
  recreate: (id: string) => Response = (id) => this.fix(id)
  /** what core does when it made the client again: the mark goes, the counter starts from zero */
  fix(id: string): Response {
    const s = this.services.find((x) => x.id === id)!
    s.panel_missing_since = null
    s.traffic_used = 0
    return json(200, { service: s })
  }
  answer(url: string, init?: RequestInit): Response | undefined {
    const method = init?.method ?? 'GET'
    if (url === '/api/v1/overview') return json(200, this.overview)
    if (method === 'GET' && url.startsWith('/api/v1/services?')) {
      const p = new URL(url, 'https://x.example').searchParams
      const items = this.services.filter((s) => p.get('panel') !== 'missing' || !!s.panel_missing_since)
      return json(200, { items, total: items.length, page: 1, size: 25 })
    }
    const one = /^\/api\/v1\/services\/([^/]+)(\/recreate-client)?$/.exec(url)
    if (one && !one[2] && method === 'GET') return json(200, { service: this.services.find((s) => s.id === one[1]), orders: [] })
    if (one && one[2] && method === 'POST') return this.recreate(one[1])
    return undefined
  }
  posted(id: string): Record<string, unknown>[] {
    const head = `POST /api/v1/services/${id}/recreate-client `
    return this.calls.filter((c) => c.startsWith(head)).map((c) => JSON.parse(c.slice(head.length)) as Record<string, unknown>)
  }
  reads(id: string): number {
    return this.calls.filter((c) => c === `GET /api/v1/services/${id}`).length
  }
}

interface Env {
  lang?: 'en' | 'fa'
  perms?: string[]
}

async function boot(core: Core, path: string, env: Env = {}) {
  unmount() // a test may boot again, as another viewer
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
  const hit = [...root.querySelectorAll('button')].find((b) => text(b) === label)
  if (!hit) throw new Error(`no button "${label}" in: ${[...root.querySelectorAll('button')].map(text).join(' | ')}`)
  return hit as HTMLButtonElement
}
const confirmButton = () => [...document.querySelectorAll('.p-dialog-footer button')].pop() as HTMLButtonElement
const rows = () => all('.p-datatable-tbody tr:not(.p-datatable-empty-message)')

describe('the Services page', () => {
  it('marks the services the panel has no client for, and only those', async () => {
    const core = new Core()
    await boot(core, '/admin/services')
    await until(() => expect(rows()).toHaveLength(3))
    expect(core.calls).toContain('GET /api/v1/services?page=1&size=25')
    // the first row has no field at all, the third an explicit null: neither is marked
    expect(rows().map((r) => r.querySelector('.panel-missing') !== null)).toEqual([false, true, false])
    const badge = rows()[1].querySelector('.panel-missing')!
    expect(text(badge)).toBe(en.service.panel_missing)
    expect(badge.getAttribute('title')).toBe(fill(en.service.panel_missing_since, { time: dateTime(SINCE, 'en') }))
    // the service's own status is still told beside it
    expect(text(rows()[1].querySelector('.states'))).toContain(en.service.status.active)
  }, 20000)

  it('lists only those with the toggle, and keeps the choice in the address', async () => {
    const core = new Core()
    const { router } = await boot(core, '/admin/services')
    await until(() => expect(rows()).toHaveLength(3))
    const toggle = q<HTMLInputElement>('#only-missing')!
    expect(text(toggle.closest('label'))).toBe(en.service.only_missing)
    expect(toggle.checked).toBe(false)
    expect(router.currentRoute.value.query.panel).toBeUndefined()

    toggle.click()
    await until(() => expect(core.calls).toContain('GET /api/v1/services?panel=missing&page=1&size=25'))
    expect(router.currentRoute.value.query.panel).toBe('missing')
    await until(() => expect(rows()).toHaveLength(1))
    expect(rows()[0].querySelector('.panel-missing')).not.toBeNull()

    // off again: the whole list, and the address is clean
    q<HTMLInputElement>('#only-missing')!.click()
    await until(() => expect(rows()).toHaveLength(3))
    expect(router.currentRoute.value.query.panel).toBeUndefined()
    expect(core.calls.filter((c) => c === 'GET /api/v1/services?page=1&size=25')).toHaveLength(2)
  }, 20000)

  it('starts with the toggle on when the address says so (the Overview links there), and follows the address', async () => {
    const core = new Core()
    const { router } = await boot(core, '/admin/services?panel=missing')
    await until(() => expect(rows()).toHaveLength(1))
    expect(core.calls).toContain('GET /api/v1/services?panel=missing&page=1&size=25')
    expect(core.calls).not.toContain('GET /api/v1/services?page=1&size=25') // it never asked for the whole list first
    expect(q<HTMLInputElement>('#only-missing')!.checked).toBe(true)

    // the menu's own link to Services (no filter) shows the whole list again
    await router.push({ name: 'services' })
    await until(() => expect(q<HTMLInputElement>('#only-missing')!.checked).toBe(false))
    await until(() => expect(rows()).toHaveLength(3))
    expect(core.calls).toContain('GET /api/v1/services?page=1&size=25')
  }, 20000)

  it('takes any other value of the address for no filter', async () => {
    const core = new Core()
    await boot(core, '/admin/services?panel=other')
    await until(() => expect(rows()).toHaveLength(3))
    expect(core.calls).toContain('GET /api/v1/services?page=1&size=25')
    expect(core.calls.some((c) => c.includes('panel='))).toBe(false)
    expect(q<HTMLInputElement>('#only-missing')!.checked).toBe(false)
  }, 20000)

  it('shows the badge in Persian too, and the toggle reads right to left', async () => {
    const core = new Core()
    await boot(core, '/admin/services', { lang: 'fa' })
    await until(() => expect(rows()).toHaveLength(3))
    expect(document.documentElement.dir).toBe('rtl')
    expect(text(rows()[1].querySelector('.panel-missing'))).toBe(fa.service.panel_missing)
    expect(text(q('#only-missing')!.closest('label'))).toBe(fa.service.only_missing)
  }, 20000)
})

describe('a service the panel has no client for', () => {
  const missing = sid(2)

  it('warns since when, and makes the client again after a reason', async () => {
    const core = new Core()
    await boot(core, `/admin/services/${missing}`)
    const banner = await found('.p-message.missing')
    expect(text(banner)).toContain(en.service.panel_missing)
    expect(text(banner)).toContain(fill(en.service.missing_text, { time: dateTime(SINCE, 'en') }))
    expect(text(q('.traffic'))).toBe('5 GB of 50 GB')

    buttonIn(banner, en.service.recreate).click()
    const d = await found('.p-dialog')
    expect(text(d)).toContain(en.service.recreate)
    expect(text(d)).toContain(en.service.recreate_intro)
    expect(confirmButton().disabled).toBe(true) // a reason first, like delete and reset
    expect(core.posted(missing)).toEqual([])
    type(d.querySelector('textarea'), 'deleted by mistake on the panel')
    await until(() => expect(confirmButton().disabled).toBe(false))
    expect(text(confirmButton())).toBe(en.service.recreate)
    confirmButton().click()

    await until(() => expect(core.posted(missing)).toEqual([{ reason: 'deleted by mistake on the panel' }]))
    await until(() => expect(q('.p-toast')?.textContent).toContain(en.service.done_recreate))
    // the service is read again and the warning is gone, with the counter from zero
    await until(() => expect(core.reads(missing)).toBe(2))
    await until(() => expect(q('.p-message.missing')).toBeNull())
    await until(() => expect(q('.p-dialog')).toBeNull())
    // what the dialog promised: the used traffic starts from zero, the quota stays as it was
    expect(text(q('.traffic'))).toBe('0 MB of 50 GB')
  }, 20000)

  it('says why in the dialog when it cannot, keeps the warning, and lets you try again', async () => {
    const core = new Core()
    const answers = [
      json(409, { error: 'state', message: 'domain: not possible in the current state: the service is being delivered' }),
      json(503, { error: 'unavailable', message: 'the panel did not answer' }),
    ]
    core.recreate = (id) => answers.shift() ?? core.fix(id)
    await boot(core, `/admin/services/${missing}`)
    buttonIn(await found('.p-message.missing'), en.service.recreate).click()
    const d = await found('.p-dialog')
    type(d.querySelector('textarea'), 'try')
    await until(() => expect(confirmButton().disabled).toBe(false))

    confirmButton().click()
    await until(() => expect(text(d.querySelector('.p-message'))).toContain(en.error.state))
    expect(text(d.querySelector('.p-message'))).toContain('the service is being delivered') // core's own words as the detail
    expect(q('.p-message.missing')).not.toBeNull()
    expect(core.reads(missing)).toBe(1) // nothing happened, so nothing was read again

    confirmButton().click()
    await until(() => expect(text(d.querySelector('.p-message'))).toContain(en.error.unavailable))
    expect(q('.p-dialog')).not.toBeNull()

    confirmButton().click()
    await until(() => expect(q('.p-message.missing')).toBeNull())
    expect(core.posted(missing)).toEqual([{ reason: 'try' }, { reason: 'try' }, { reason: 'try' }])
    expect(q('.p-toast')?.textContent).toContain(en.service.done_recreate)
  }, 20000)

  it('shows the warning to those who read services, but the button only to those who may change them', async () => {
    const core = new Core()
    await boot(core, `/admin/services/${missing}`, { perms: SUPPORT })
    const banner = await found('.p-message.missing')
    expect(text(banner)).toContain(en.service.panel_missing)
    expect(text(banner)).toContain(fill(en.service.missing_text, { time: dateTime(SINCE, 'en') }))
    expect(banner.querySelector('button')).toBeNull()
    // what support may do stays: extend, and read it from the panel
    expect(all('.app-actions button').map(text)).toContain(en.service.extend)
    expect(all('button').map(text)).not.toContain(en.service.recreate)
  }, 20000)

  it('offers no button on a service that is deleted (core would refuse), and no warning on one the panel has', async () => {
    const core = new Core()
    core.services.push(service(4, { status: 'deleted', panel_missing_since: SINCE }))
    await boot(core, `/admin/services/${sid(4)}`)
    const banner = await found('.p-message.missing')
    expect(banner.querySelector('button')).toBeNull()

    await boot(core, `/admin/services/${sid(1)}`) // no field at all
    await until(() => expect(text(q('.app-facts'))).toContain('cust1-abc'))
    expect(q('.p-message.missing')).toBeNull()
    expect(all('button').map(text)).not.toContain(en.service.recreate)

    await boot(core, `/admin/services/${sid(3)}`) // null
    await until(() => expect(text(q('.app-facts'))).toContain('cust3-abc'))
    expect(q('.p-message.missing')).toBeNull()
  }, 40000)

  it('reads in Persian, right to left, with the date in the Persian calendar', async () => {
    const core = new Core()
    await boot(core, `/admin/services/${missing}`, { lang: 'fa' })
    const banner = await found('.p-message.missing')
    expect(document.documentElement.dir).toBe('rtl')
    expect(dateTime(SINCE, 'fa')).toMatch(/[۰-۹]/)
    expect(text(banner)).toContain(fa.service.panel_missing)
    expect(text(banner)).toContain(fill(fa.service.missing_text, { time: dateTime(SINCE, 'fa') }))
    buttonIn(banner, fa.service.recreate).click()
    const d = await found('.p-dialog')
    expect(text(d)).toContain(fa.service.recreate_intro)
  }, 20000)
})

describe('the Overview', () => {
  const overview = (panelMissing?: number) => ({
    users: { total: 10, new_24h: 1, new_7d: 2 },
    services: { active: 5, expiring: 1, ended: 0, ...(panelMissing === undefined ? {} : { panel_missing: panelMissing }) },
    orders: { paid_24h: 1, provision_failed: 0 }, revenue: { day: [], month: [] }, pending_payments: 0, panel: { healthy: true, detail: '' }, recent_orders: [],
  })
  const loaded = async () => {
    await found('.recent')
    await until(() => expect(q('.stat .big')).not.toBeNull())
  }

  it('says how many services the panel has no client for, and opens the list with the filter on', async () => {
    const core = new Core()
    core.overview = overview(3)
    const { router } = await boot(core, '/admin/')
    const card = await found<HTMLAnchorElement>('a.missing-card')
    expect(text(card.querySelector('.p-card-subtitle'))).toBe(en.overview.panel_missing)
    expect(text(card.querySelector('.big'))).toBe('3')
    expect(text(card)).toContain(en.overview.panel_missing_hint)
    expect(card.getAttribute('href')).toBe('/admin/services?panel=missing')
    // it sits with the service counters: right after the Active services card
    const cards = all('.app-grid > *').map((c) => text(c.querySelector('.p-card-subtitle')))
    expect(cards.indexOf(en.overview.panel_missing)).toBe(cards.indexOf(en.overview.services) + 1)

    card.click()
    await until(() => expect(router.currentRoute.value.name).toBe('services'))
    expect(router.currentRoute.value.query.panel).toBe('missing')
    await until(() => expect(core.calls).toContain('GET /api/v1/services?panel=missing&page=1&size=25'))
    expect(q<HTMLInputElement>('#only-missing')!.checked).toBe(true)
  }, 20000)

  it('shows no card while there are none, or when core does not say', async () => {
    const core = new Core()
    core.overview = overview(0)
    await boot(core, '/admin/')
    await loaded()
    expect(q('.missing-card')).toBeNull()

    core.overview = overview() // an older core: the field is not there
    await boot(core, '/admin/')
    await loaded()
    expect(q('.missing-card')).toBeNull()
    expect(text(q('.app-grid'))).toContain(en.overview.services)
  }, 30000)

  it('shows the card to those who cannot open the services too, but not as a link', async () => {
    const core = new Core()
    core.overview = overview(2)
    await boot(core, '/admin/', { perms: ['overview.read'] })
    const card = await found('.missing-card')
    expect(card.tagName).toBe('DIV')
    expect(card.getAttribute('href')).toBeNull()
    expect(text(card.querySelector('.big'))).toBe('2')
  }, 20000)

  it('reads in Persian with Persian digits', async () => {
    const core = new Core()
    core.overview = overview(3)
    await boot(core, '/admin/', { lang: 'fa' })
    const card = await found<HTMLAnchorElement>('a.missing-card')
    expect(text(card.querySelector('.p-card-subtitle'))).toBe(fa.overview.panel_missing)
    expect(text(card.querySelector('.big'))).toBe('۳')
    expect(text(card)).toContain(fa.overview.panel_missing_hint)
  }, 20000)
})
