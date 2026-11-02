import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import ar from '../locales/ar.json'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import ru from '../locales/ru.json'
import tr from '../locales/tr.json'
import type { ServiceItem, SettingItem } from '../types'

// Config names: the Settings page's card (two switches, off by default) and the service page's Rename (only where the
// server keeps the link, with a name and a reason, and core's reason for a name it does not take said in the page's
// language). Booted like the real app against a fake core.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
const until = <T>(check: () => T) => vi.waitFor(check, { timeout: 8000 })

const SID = 'eeeeeeee-1111-4222-8333-444444444441'
const service = (over: Partial<ServiceItem> = {}): ServiceItem => ({
  id: SID, user: { id: 'u1', telegram_id: 101, username: 'nima' }, plan: { id: 'p1', name: { fa: 'ماهانه', en: 'Monthly' } }, status: 'active',
  client_email: 'u101-abcdefgh', sub_link: 'https://shop.example.com/sub/abc', expires_at: 1_795_000_000, traffic_total: 50 * 2 ** 30, traffic_used: 0,
  last_synced_at: 1_790_100_000, created_at: 1_780_000_000, renamable: true, ...over,
})

const OWNER = ['overview.read', 'services.read', 'services.extend', 'services.write', 'settings.write', 'audit.read']

async function boot(path: string, answer: (url: string, init?: RequestInit) => Response | undefined, calls: string[], perms = OWNER, lang: 'en' | 'fa' = 'en') {
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
          user: { id: 'u0', telegram_id: 1, username: 'boss', role: 'owner', language: lang }, permissions: perms, csrf: 'c',
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

const buttons = () => [...document.querySelectorAll('button')]
const renameButton = () => buttons().find((b) => text(b) === en.service.rename)
const confirmButton = () => [...document.querySelectorAll('.p-dialog-footer button')].pop() as HTMLButtonElement
function type(el: Element | null, value: string) {
  const input = el as HTMLInputElement | HTMLTextAreaElement
  input.value = value
  input.dispatchEvent(new Event('input'))
}

describe('the texts of config names', () => {
  const get = (o: unknown, path: string): unknown => path.split('.').reduce<unknown>((a, k) => (a as Record<string, unknown> | undefined)?.[k], o)
  const keys = [
    'settings.group.config_names.title', 'settings.group.config_names.desc', 'settings.group.config_names.note',
    'settings.field.config_names.customers.label', 'settings.field.config_names.customers.help',
    'settings.field.config_names.resellers.label', 'settings.field.config_names.resellers.help',
    'service.rename', 'service.rename_intro', 'service.rename_name', 'service.rename_hint', 'service.done_rename',
    ...['off', 'length', 'chars', 'mix', 'automatic', 'taken', 'unchecked', 'not_renamable'].map((r) => 'service.rename_reason.' + r),
    'audit.action.subscription.rename',
  ]
  it('are in every language', () => {
    for (const [lang, dict] of Object.entries({ en, fa, ar, ru, tr })) {
      for (const k of keys) expect(typeof get(dict, k), `${lang} ${k}`).toBe('string')
    }
  })
})

describe('the Settings page', () => {
  it('has a Config names card with both switches, off by default, and saves what changed', async () => {
    const item = (key: string): SettingItem => ({ key, group: 'config_names', kind: 'bool', value: '', default: 'false', options: [], min: null, max: null, editable: true })
    let items = [item('config_names.customers'), item('config_names.resellers')]
    const calls: string[] = []
    const reply = () => json(200, { items, groups: ['config_names'] })
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
    expect(text(card.querySelector('h2'))).toBe(en.settings.group.config_names.title)
    expect(text(card)).toContain(en.settings.field.config_names.customers.label)
    expect(text(card)).toContain(en.settings.field.config_names.resellers.label)
    const customers = card.querySelector('input#setting-config-names-customers') as HTMLInputElement
    expect(customers.checked).toBe(false)
    customers.click()
    await nextTick()
    const save = [...card.querySelectorAll('button')].find((b) => b.getAttribute('type') === 'submit') as HTMLButtonElement
    save.click()
    await until(() => expect(calls.some((c) => c.startsWith('PUT /api/v1/settings'))).toBe(true))
    const put = calls.find((c) => c.startsWith('PUT /api/v1/settings')) as string
    expect(JSON.parse(put.slice(put.indexOf('{'))).values).toEqual({ 'config_names.customers': 'true' })
  }, 20000)
})

describe('the service page', () => {
  /** A fake core for one service: rename answers with answerRename, or renames it. */
  function core(svc: ServiceItem, answerRename?: () => Response) {
    return (url: string, init?: RequestInit): Response | undefined => {
      if (url === `/api/v1/services/${SID}` && (init?.method ?? 'GET') === 'GET') return json(200, { service: svc, orders: [] })
      if (url === `/api/v1/services/${SID}/rename` && init?.method === 'POST') {
        if (answerRename) return answerRename()
        svc.client_email = String(JSON.parse(String(init.body)).name)
        return json(200, { service: svc })
      }
      return undefined
    }
  }

  it('renames a service with a name and a reason, and shows the new name', async () => {
    const calls: string[] = []
    await boot(`/admin/services/${SID}`, core(service()), calls)
    await until(() => expect(renameButton()).toBeDefined())
    renameButton()!.click()
    await until(() => expect(document.querySelector('#rename-name')).not.toBeNull())
    expect(text(document.querySelector('.p-dialog'))).toContain(en.service.rename_intro)
    expect(confirmButton().disabled).toBe(true)
    type(document.querySelector('#rename-name'), ' nima1234 ')
    type(document.querySelector('.p-dialog textarea'), 'the customer asked')
    await nextTick()
    expect(confirmButton().disabled).toBe(false)
    confirmButton().click()
    await until(() => expect(calls.some((c) => c.startsWith(`POST /api/v1/services/${SID}/rename`))).toBe(true))
    const post = calls.find((c) => c.startsWith(`POST /api/v1/services/${SID}/rename`)) as string
    expect(JSON.parse(post.slice(post.indexOf('{')))).toEqual({ name: 'nima1234', reason: 'the customer asked' })
    await until(() => expect(text(document.querySelector('.app-facts'))).toContain('nima1234'))
  }, 20000)

  it("says why core did not take the name, in the page's language", async () => {
    const calls: string[] = []
    await boot(`/admin/services/${SID}`, core(service(), () => json(400, { error: 'config_name', reason: 'taken', message: 'config name: taken' })), calls, OWNER, 'fa')
    const label = fa.service.rename
    await until(() => expect(buttons().find((b) => text(b) === label)).toBeDefined())
    buttons().find((b) => text(b) === label)!.click()
    await until(() => expect(document.querySelector('#rename-name')).not.toBeNull())
    type(document.querySelector('#rename-name'), 'taken123')
    type(document.querySelector('.p-dialog textarea'), 'x')
    await nextTick()
    confirmButton().click()
    await until(() => expect(text(document.querySelector('.p-dialog'))).toContain(fa.service.rename_reason.taken))
  }, 20000)

  it('has no Rename where the server cannot keep the link, nor for staff who may not change services', async () => {
    const calls: string[] = []
    await boot(`/admin/services/${SID}`, core(service({ renamable: false })), calls)
    await until(() => expect(text(document.querySelector('.app-facts'))).toContain('u101-abcdefgh'))
    expect(renameButton()).toBeUndefined()

    await boot(`/admin/services/${SID}`, core(service()), calls, ['overview.read', 'services.read', 'services.extend'])
    await until(() => expect(text(document.querySelector('.app-facts'))).toContain('u101-abcdefgh'))
    expect(renameButton()).toBeUndefined()
  }, 20000)
})
