import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import type { ServiceItem } from '../types'

// The bulk actions of the Services page with "Not on the panel" on: checkboxes on the rows, one that selects the
// whole page, and Make again / Delete for the selection with one reason; each service is sent on its own with
// only_missing, and the summary says what became of each (done, skipped with why, failed with why).
// Booted like the real app against a fake core that keeps the services (see panel-missing.test.ts).

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
const q = <T extends Element = HTMLElement>(selector: string) => document.querySelector<T>(selector)
const all = (selector: string) => [...document.querySelectorAll<HTMLElement>(selector)]
const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replaceAll('{' + k + '}', v), s)
const plural = (s: string, n: number) => {
  const forms = s.split(' | ')
  return fill(forms[n === 1 || forms.length === 1 ? 0 : 1], { n: String(n) })
}
const until = <T>(check: () => T) => vi.waitFor(check, { timeout: 8000 })

const SINCE = 1_790_000_000
const sid = (n: number) => `eeeeeeee-1111-4222-8333-44444444444${n}`
const service = (n: number, over: Partial<ServiceItem> = {}): ServiceItem => ({
  id: sid(n), user: { id: 'u' + n, telegram_id: 100 + n, username: 'cust' + n }, plan: { id: 'p1', name: { fa: 'ماهانه', en: 'Monthly' } }, status: 'active',
  client_email: 'cust' + n + '-abc', sub_link: 'https://shop.example.com/sub/abc' + n, expires_at: 1_795_000_000, traffic_total: 50 * 2 ** 30, traffic_used: 5 * 2 ** 30,
  last_synced_at: 1_790_100_000, created_at: 1_780_000_000, panel_missing_since: SINCE, ...over,
})

const OWNER = ['overview.read', 'services.read', 'services.extend', 'services.write']
const SUPPORT = ['overview.read', 'services.read', 'services.extend']

/** The fake core: what it keeps, what it was asked, and how it acts (as core does with only_missing). */
class Core {
  services: ServiceItem[] = [service(1), service(2), service(3), service(4, { panel_missing_since: null })]
  calls: string[] = []
  /** a forced answer for one service's action */
  answers: Record<string, () => Response> = {}
  act(id: string, what: 'recreate' | 'delete', body: Record<string, unknown>): Response {
    const forced = this.answers[id]
    if (forced) return forced()
    const s = this.services.find((x) => x.id === id)
    if (!s) return json(404, { error: 'not_found', message: 'not found' })
    if (body.only_missing) {
      if (s.status === 'deleted') return json(200, { service: s, skipped: 'deleted' })
      if (!s.panel_missing_since) return json(200, { service: s, skipped: 'back' })
    }
    if (what === 'recreate') s.panel_missing_since = null
    else s.status = 'deleted'
    return json(200, { service: s })
  }
  answer(url: string, init?: RequestInit): Response | undefined {
    const method = init?.method ?? 'GET'
    if (method === 'GET' && url.startsWith('/api/v1/services?')) {
      const p = new URL(url, 'https://x.example').searchParams
      const items = this.services.filter((s) => p.get('panel') !== 'missing' || (!!s.panel_missing_since && s.status !== 'deleted'))
      return json(200, { items, total: items.length, page: 1, size: 25 })
    }
    const m = /^\/api\/v1\/services\/([^/]+)\/(recreate-client|delete)$/.exec(url)
    if (m && method === 'POST') {
      return this.act(m[1], m[2] === 'delete' ? 'delete' : 'recreate', JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>)
    }
    return undefined
  }
  posts(): string[] {
    return this.calls.filter((c) => c.startsWith('POST /api/v1/services/'))
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
  const input = el as HTMLTextAreaElement
  input.value = value
  input.dispatchEvent(new Event('input'))
}
const rows = () => all('.p-datatable-tbody tr:not(.p-datatable-empty-message)')
const rowBox = (i: number) => rows()[i].querySelector<HTMLInputElement>('.pick input')!
const pageBox = () => q<HTMLInputElement>('#pick-page')!
const confirmButton = () => [...document.querySelectorAll('.p-dialog-footer button')].pop() as HTMLButtonElement
const bulkButton = (cls: string) => q<HTMLButtonElement>(`.bulk-bar .${cls}`)!

describe('bulk actions on the services not on the panel', () => {
  it('selects one by one or the whole page, and makes their clients again with one reason', async () => {
    const core = new Core()
    const { router } = await boot(core, '/admin/services?panel=missing')
    await until(() => expect(rows()).toHaveLength(3))
    expect(text(q('.bulk-bar'))).toContain(en.service.bulk.hint)
    expect(bulkButton('bulk-recreate').disabled).toBe(true)
    expect(bulkButton('bulk-delete').disabled).toBe(true)
    expect(pageBox().getAttribute('aria-label')).toBe(en.service.bulk.select_page)
    expect(rowBox(0).getAttribute('aria-label')).toBe(fill(en.service.bulk.select_one, { email: 'cust1-abc' }))

    // A tap on a row's box selects it and does not open the service.
    rowBox(0).click()
    await until(() => expect(text(q('.bulk-count'))).toBe(plural(en.service.bulk.selected, 1)))
    expect(router.currentRoute.value.name).toBe('services')
    expect(bulkButton('bulk-recreate').disabled).toBe(false)
    // The page's box selects all three, and again none.
    pageBox().click()
    await until(() => expect(text(q('.bulk-count'))).toBe(plural(en.service.bulk.selected, 3)))
    expect(rows().every((r) => r.querySelector<HTMLInputElement>('.pick input')!.checked)).toBe(true)
    pageBox().click()
    await until(() => expect(text(q('.bulk-count'))).toBe(en.service.bulk.hint))
    pageBox().click()
    await until(() => expect(text(q('.bulk-count'))).toBe(plural(en.service.bulk.selected, 3)))

    bulkButton('bulk-recreate').click()
    const d = await until(() => {
      const el = q('.p-dialog')
      expect(el).not.toBeNull()
      return el!
    })
    expect(text(d)).toContain(plural(en.service.bulk.recreate_title, 3))
    expect(text(d)).toContain(en.service.bulk.recreate_intro)
    expect(all('.bulk-targets li').map(text)).toEqual(['cust1-abc', 'cust2-abc', 'cust3-abc'])
    expect(confirmButton().disabled).toBe(true) // the reason first
    type(d.querySelector('textarea'), 'the operator wiped the inbound')
    await until(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()

    // One call per service, each with the reason and only_missing, one after the other.
    await until(() => expect(q('.bulk-tally')).not.toBeNull())
    const body = JSON.stringify({ reason: 'the operator wiped the inbound', only_missing: true })
    expect(core.posts()).toEqual([1, 2, 3].map((n) => `POST /api/v1/services/${sid(n)}/recreate-client ${body}`))
    expect(text(q('.bulk-tally'))).toBe(fill(en.service.bulk.summary, { done: '3', skipped: '0', failed: '0' }))
    expect(all('.bulk-result.is-done')).toHaveLength(3)
    await until(() => expect(q('.p-toast')?.textContent).toContain(en.service.bulk.done_recreate))
    // The list is read again: none is left, and nothing stays selected.
    await until(() => expect(rows()).toHaveLength(0))
    buttonByText(en.service.bulk.close).click()
    await until(() => expect(q('.p-dialog')).toBeNull())
  }, 30000)

  it('deletes, skipping one that changed meanwhile and saying why the others failed', async () => {
    const core = new Core()
    core.answers[sid(2)] = () => json(409, { error: 'state', message: 'a renewal of this service is still being applied' })
    core.answers[sid(3)] = () => json(503, { error: 'unavailable', message: 'the panel did not answer' })
    await boot(core, '/admin/services?panel=missing')
    await until(() => expect(rows()).toHaveLength(3))
    pageBox().click()
    await until(() => expect(bulkButton('bulk-delete').disabled).toBe(false))
    // Service 1 comes back on the panel before the delete runs.
    core.services[0].panel_missing_since = null
    bulkButton('bulk-delete').click()
    const d = await until(() => {
      const el = q('.p-dialog')
      expect(el).not.toBeNull()
      return el!
    })
    expect(text(d)).toContain(plural(en.service.bulk.delete_title, 3))
    expect(text(d)).toContain(en.service.bulk.delete_intro)
    type(d.querySelector('textarea'), 'customers left')
    await until(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()

    await until(() => expect(q('.bulk-tally')).not.toBeNull())
    expect(core.posts()).toHaveLength(3)
    expect(text(q('.bulk-tally'))).toBe(fill(en.service.bulk.summary, { done: '0', skipped: '1', failed: '2' }))
    const lines = all('.bulk-result').map(text)
    expect(lines[0]).toContain('cust1-abc')
    expect(lines[0]).toContain(en.service.bulk.result_skipped)
    expect(lines[0]).toContain(en.service.bulk.skipped_back)
    expect(lines[1]).toContain(en.service.bulk.result_failed)
    expect(lines[1]).toContain(en.error.state)
    expect(lines[1]).toContain('a renewal of this service is still being applied')
    expect(lines[2]).toContain(en.error.unavailable)
    expect(core.services[0].status).toBe('active') // the one back on the panel was not deleted
    await until(() => expect(q('.p-toast .p-toast-message-warn')).not.toBeNull())
  }, 30000)

  it('says a service deleted meanwhile was skipped', async () => {
    const core = new Core()
    await boot(core, '/admin/services?panel=missing')
    await until(() => expect(rows()).toHaveLength(3))
    rowBox(1).click()
    await until(() => expect(bulkButton('bulk-recreate').disabled).toBe(false))
    core.services[1].status = 'deleted'
    bulkButton('bulk-recreate').click()
    const d = await until(() => {
      const el = q('.p-dialog')
      expect(el).not.toBeNull()
      return el!
    })
    type(d.querySelector('textarea'), 'x')
    await until(() => expect(confirmButton().disabled).toBe(false))
    confirmButton().click()
    await until(() => expect(q('.bulk-tally')).not.toBeNull())
    expect(text(q('.bulk-result'))).toContain(en.service.bulk.skipped_deleted)
    expect(core.posts()).toEqual([`POST /api/v1/services/${sid(2)}/recreate-client ${JSON.stringify({ reason: 'x', only_missing: true })}`])
  }, 20000)

  it('offers them only with the filter on, and only to those who may change services', async () => {
    const core = new Core()
    await boot(core, '/admin/services')
    await until(() => expect(rows()).toHaveLength(4))
    expect(q('.pick')).toBeNull()
    expect(q('.bulk-bar')).toBeNull()

    await boot(core, '/admin/services?panel=missing', { perms: SUPPORT })
    await until(() => expect(rows()).toHaveLength(3))
    expect(q('.pick')).toBeNull()
    expect(q('#pick-page')).toBeNull()
    expect(q('.bulk-bar')).toBeNull()

    // A tap on a row still opens the service, as before.
    rows()[0].click()
    await until(() => expect(window.location.pathname).toBe(`/admin/services/${sid(1)}`))
  }, 30000)

  it('reads in Persian', async () => {
    const core = new Core()
    await boot(core, '/admin/services?panel=missing', { lang: 'fa' })
    await until(() => expect(rows()).toHaveLength(3))
    expect(document.documentElement.dir).toBe('rtl')
    expect(text(q('.bulk-bar'))).toContain(fa.service.bulk.hint)
    expect(text(bulkButton('bulk-recreate'))).toBe(fa.service.bulk.recreate)
    expect(text(bulkButton('bulk-delete'))).toBe(fa.service.bulk.delete)
    expect(pageBox().getAttribute('aria-label')).toBe(fa.service.bulk.select_page)
    pageBox().click()
    await until(() => expect(text(q('.bulk-count'))).toBe(fill(fa.service.bulk.selected, { n: '۳' })))
    bulkButton('bulk-delete').click()
    await until(() => expect(text(q('.p-dialog'))).toContain(fill(fa.service.bulk.delete_title, { n: '۳' })))
  }, 20000)
})

function buttonByText(label: string): HTMLButtonElement {
  const hit = [...document.querySelectorAll('button')].find((b) => text(b) === label)
  if (!hit) throw new Error(`no button "${label}"`)
  return hit as HTMLButtonElement
}
