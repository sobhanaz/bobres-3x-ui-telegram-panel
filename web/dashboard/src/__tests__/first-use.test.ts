import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import ar from '../locales/ar.json'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import ru from '../locales/ru.json'
import tr from '../locales/tr.json'
import type { ServiceItem, SettingItem } from '../types'

// Days from the first connection: the Settings page's card (two switches, off by default), and a service that waits for
// its config's first connection (no end date, first_use_days set) shown as "30 days · from the first connection" in the
// services list and on its page, where Extend adds the days to the wait. Booted like the real app against a fake core.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
const until = <T>(check: () => T) => vi.waitFor(check, { timeout: 8000 })

const SID = 'eeeeeeee-1111-4222-8333-444444444451'
const service = (over: Partial<ServiceItem> = {}): ServiceItem => ({
  id: SID, user: { id: 'u1', telegram_id: 101, username: 'nima' }, plan: { id: 'p1', name: { fa: 'ماهانه', en: 'Monthly' } }, status: 'active',
  client_email: 'u101-abcdefgh', sub_link: 'https://shop.example.com/sub/abc', expires_at: null, first_use_days: 30, traffic_total: 50 * 2 ** 30,
  traffic_used: 0, last_synced_at: 1_790_100_000, created_at: 1_780_000_000, ...over,
})

const OWNER = ['overview.read', 'services.read', 'services.extend', 'services.write', 'settings.write', 'audit.read']

async function boot(path: string, answer: (url: string, init?: RequestInit) => Response | undefined, calls: string[], lang: 'en' | 'fa' = 'en') {
  unmount()
  localStorage.setItem('bobres.lang', lang)
  vi.resetModules()
  window.history.replaceState(null, '', path)
  document.body.innerHTML = '<div id="app"></div>'
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url} ${typeof init?.body === 'string' ? init.body : ''}`.trim())
      const own = answer(url, init)
      if (own) return own
      if (url === '/api/v1/me') {
        return json(200, {
          user: { id: 'u0', telegram_id: 1, username: 'boss', role: 'owner', language: lang }, permissions: OWNER, csrf: 'c',
          session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
        })
      }
      return json(503, { error: 'unavailable', message: 'not in this test' })
    }),
  )
  await import('../main')
  const { router } = await import('../router')
  await router.isReady()
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

const confirmButton = () => [...document.querySelectorAll('.p-dialog-footer button')].pop() as HTMLButtonElement
function type(el: Element | null, value: string) {
  const input = el as HTMLInputElement | HTMLTextAreaElement
  input.value = value
  input.dispatchEvent(new Event('input'))
}

describe('the texts of days from the first connection', () => {
  const get = (o: unknown, path: string): unknown => path.split('.').reduce<unknown>((a, k) => (a as Record<string, unknown> | undefined)?.[k], o)
  const keys = [
    'settings.group.first_use.title', 'settings.group.first_use.desc', 'settings.group.first_use.note',
    'settings.field.first_use.customers.label', 'settings.field.first_use.customers.help',
    'settings.field.first_use.resellers.label', 'settings.field.first_use.resellers.help',
    'service.first_use', 'service.first_use_hint', 'service.new_first_use',
  ]
  it('are in every language', () => {
    for (const [lang, dict] of Object.entries({ en, fa, ar, ru, tr })) {
      for (const k of keys) expect(typeof get(dict, k), `${lang} ${k}`).toBe('string')
    }
  })
})

describe('the Settings page', () => {
  it('has a Days from the first connection card with both switches, off by default, and saves what changed', async () => {
    const item = (key: string): SettingItem => ({ key, group: 'first_use', kind: 'bool', value: '', default: 'false', options: [], min: null, max: null, editable: true })
    let items = [item('first_use.customers'), item('first_use.resellers')]
    const calls: string[] = []
    const reply = () => json(200, { items, groups: ['first_use'] })
    await boot('/admin/settings', (url, init) => {
      if (url === '/api/v1/settings' && (init?.method ?? 'GET') === 'GET') return reply()
      if (url === '/api/v1/settings' && init?.method === 'PUT') {
        const values = JSON.parse(String(init.body)).values as Record<string, string>
        items = items.map((i) => (i.key in values ? { ...i, value: values[i.key] } : i))
        return reply()
      }
      return undefined
    }, calls)
    await until(() => expect(document.querySelector('.settings-card')).not.toBeNull())
    const card = document.querySelector('.settings-card') as HTMLElement
    expect(text(card.querySelector('h2'))).toBe(en.settings.group.first_use.title)
    expect(text(card)).toContain(en.settings.group.first_use.desc)
    expect(text(card)).toContain(en.settings.field.first_use.customers.label)
    expect(text(card)).toContain(en.settings.field.first_use.resellers.label)
    const customers = card.querySelector('input#setting-first-use-customers') as HTMLInputElement
    const resellers = card.querySelector('input#setting-first-use-resellers') as HTMLInputElement
    expect(customers.checked).toBe(false)
    expect(resellers.checked).toBe(false)
    resellers.click()
    await nextTick()
    const save = [...card.querySelectorAll('button')].find((b) => b.getAttribute('type') === 'submit') as HTMLButtonElement
    save.click()
    await until(() => expect(calls.some((c) => c.startsWith('PUT /api/v1/settings'))).toBe(true))
    const put = calls.find((c) => c.startsWith('PUT /api/v1/settings')) as string
    expect(JSON.parse(put.slice(put.indexOf('{'))).values).toEqual({ 'first_use.resellers': 'true' })
  }, 20000)
})

describe('a service waiting for its first connection', () => {
  it('reads "30 days · from the first connection" in the services list, where another service shows its date or no end', async () => {
    const items = [
      service(),
      service({ id: 'eeeeeeee-1111-4222-8333-444444444452', expires_at: 1_795_000_000, first_use_days: null }),
      service({ id: 'eeeeeeee-1111-4222-8333-444444444453', first_use_days: null }),
    ]
    await boot('/admin/services', (url) => (url.startsWith('/api/v1/services?') ? json(200, { items, total: items.length, page: 1, size: 25 }) : undefined), [])
    await until(() => expect(document.querySelectorAll('tbody tr')).toHaveLength(3))
    const rows = [...document.querySelectorAll('tbody tr')]
    expect(text(rows[0])).toContain('30 days · from the first connection')
    expect(rows[0].querySelector('.first-use')?.getAttribute('title')).toBe(en.service.first_use_hint)
    expect(text(rows[0])).not.toContain(en.service.never)
    expect(text(rows[1])).not.toContain('from the first connection')
    expect(text(rows[1])).toMatch(/days? (left|ago)/)
    expect(text(rows[2])).toContain(en.service.never)
    expect(text(rows[2])).not.toContain('from the first connection')
  }, 20000)

  it('says it on its page, in Persian digits too', async () => {
    const core = (url: string, init?: RequestInit) =>
      url === `/api/v1/services/${SID}` && (init?.method ?? 'GET') === 'GET' ? json(200, { service: service(), orders: [] }) : undefined
    await boot(`/admin/services/${SID}`, core, [])
    await until(() => expect(text(document.querySelector('.app-facts'))).toContain('30 days · from the first connection'))
    const facts = text(document.querySelector('.app-facts'))
    expect(facts).toContain(en.service.first_use_hint)
    expect(facts).not.toContain(en.service.never)

    await boot(`/admin/services/${SID}`, core, [], 'fa')
    await until(() => expect(text(document.querySelector('.app-facts'))).toContain('۳۰ روز · از اولین اتصال'))
    expect(text(document.querySelector('.app-facts'))).toContain(fa.service.first_use_hint)
  }, 20000)

  it('takes days on Extend and says the new length from the first connection', async () => {
    const calls: string[] = []
    await boot(`/admin/services/${SID}`, (url, init) => {
      if (url === `/api/v1/services/${SID}` && (init?.method ?? 'GET') === 'GET') return json(200, { service: service(), orders: [] })
      if (url === `/api/v1/services/${SID}/extend` && init?.method === 'POST') return json(200, { service: service({ first_use_days: 60 }) })
      return undefined
    }, calls)
    const extend = () => [...document.querySelectorAll('button')].find((b) => text(b) === en.service.extend)
    await until(() => expect(extend()).toBeDefined())
    extend()!.click()
    await until(() => expect(document.querySelector('.p-dialog input[inputmode="numeric"]')).not.toBeNull())
    const days = document.querySelector('.p-dialog input[inputmode="numeric"]') as HTMLInputElement
    expect(days.disabled).toBe(false)
    type(days, '30')
    type(document.querySelector('.p-dialog textarea'), 'a gift')
    await nextTick()
    expect(text(document.querySelector('.p-dialog'))).toContain('It will run 60 days from the first connection')
    expect(text(document.querySelector('.p-dialog'))).not.toContain('New end date')
    confirmButton().click()
    await until(() => expect(calls.some((c) => c.startsWith(`POST /api/v1/services/${SID}/extend`))).toBe(true))
    const post = calls.find((c) => c.startsWith(`POST /api/v1/services/${SID}/extend`)) as string
    expect(JSON.parse(post.slice(post.indexOf('{')))).toMatchObject({ days: 30, reason: 'a gift' })
  }, 20000)
})
