import { afterEach, describe, expect, it, vi } from 'vitest'
import fa from '../locales/fa.json'

// The initial password (bobres admin password, the installer) signs in
// without Telegram, but only opens a restricted session: the dashboard shows
// the page that sets a new password and an authenticator, and nothing else.

const base = {
  user: { id: 'u1', telegram_id: 1001, username: 'boss', role: 'owner', language: 'fa' },
  permissions: ['overview.read', 'users.read'],
  csrf: 'csrf-1',
  password_available: true,
  brand: 'BOBRES',
}
const restricted = {
  ...base,
  must_change: true,
  session: { method: 'password_initial', expires_at: 2_000_000_000 },
  password: { enabled: false, username: 'owner', initial: true },
}
const full = {
  ...base,
  must_change: false,
  session: { method: 'password', expires_at: 2_000_000_000 },
  password: { enabled: true, username: 'boss' },
}

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

// jsdom has no matchMedia, which PrimeVue's Select asks for.
if (typeof window.matchMedia === 'undefined') {
  window.matchMedia = ((query: string) => ({
    matches: false, media: query, onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
}

const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
afterEach(() => {
  unmount()
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

type Handler = (method: string, body: Record<string, unknown>) => Response | undefined

/** Starts the app at `path` with a fake core; `meAnswer` answers GET /me. */
async function open(path: string, meAnswer: () => Response, handler: Handler = () => undefined) {
  unmount()
  vi.resetModules()
  window.history.replaceState(null, '', path)
  document.body.innerHTML = '<div id="app"></div>'
  const calls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push(`${method} ${url}`)
      if (method === 'GET' && url === '/api/v1/me') return meAnswer()
      const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : {}
      const answer = handler(method, body)
      if (answer && url !== '') return answer
      return json(503, { error: 'unavailable', message: 'not in this test' })
    }),
  )
  await import('../main')
  const { router } = await import('../router')
  await router.isReady()
  return { calls, router }
}

const found = <T extends Element>(selector: string) =>
  vi.waitFor(() => {
    const el = document.querySelector<T>(selector)
    expect(el, selector).not.toBeNull()
    return el!
  })

function type(el: HTMLInputElement, value: string) {
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('signing in with the initial password', () => {
  it('needs no code and lands on the page that sets a new password', async () => {
    const bodies: Record<string, unknown>[] = []
    const { router, calls } = await open(
      '/admin/login',
      () => json(401, { error: 'unauthorized', message: 'log in first' }),
      (method, body) => {
        if (method === 'POST') {
          bodies.push(body)
          return json(200, restricted)
        }
      },
    )
    const user = await found<HTMLInputElement>('input[autocomplete="username"]')
    type(user, 'owner')
    type(await found<HTMLInputElement>('.p-password input'), 'Kq7mWz3NpR9xTv2H')
    const submit = await found<HTMLButtonElement>('button[type="submit"]')
    await vi.waitFor(() => expect(submit.disabled).toBe(false)) // the authenticator code is optional
    submit.click()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('account'))
    expect(bodies).toEqual([{ username: 'owner', password: 'Kq7mWz3NpR9xTv2H', code: '' }])
    expect(calls).not.toContain('GET /api/v1/me/sessions') // core refuses it to a restricted session
    await vi.waitFor(() => expect(document.body.textContent).toContain(fa.account.forced_intro))
  })

  it('keeps the submit button off for a half-typed code', async () => {
    await open('/admin/login', () => json(401, { error: 'unauthorized', message: 'log in first' }))
    type(await found<HTMLInputElement>('input[autocomplete="username"]'), 'owner')
    type(await found<HTMLInputElement>('.p-password input'), 'Kq7mWz3NpR9xTv2H')
    const submit = await found<HTMLButtonElement>('button[type="submit"]')
    await vi.waitFor(() => expect(submit.disabled).toBe(false))
    const otp = document.querySelectorAll<HTMLInputElement>('.p-inputotp-input')
    expect(otp.length).toBe(6)
    type(otp[0], '1')
    await vi.waitFor(() => expect(submit.disabled).toBe(true))
  })
})

describe('a restricted session', () => {
  it('is kept on the account page, with no menu and no sessions list', async () => {
    const { router, calls } = await open('/admin/users', () => json(200, restricted))
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('account'))
    await vi.waitFor(() => expect(document.body.textContent).toContain(fa.account.forced_title))
    expect(document.querySelector('.sidebar nav a')).toBeNull()
    expect(document.body.textContent).not.toContain(fa.account.sessions_title)
    expect(calls).not.toContain('GET /api/v1/me/sessions')
    // and cannot navigate away
    await router.push({ name: 'overview' })
    expect(router.currentRoute.value.name).toBe('account')
    await router.push({ name: 'login' })
    expect(router.currentRoute.value.name).toBe('account')
  })

  it('shows why the initial password cannot be kept', async () => {
    await open('/admin/account', () => json(200, restricted), (method, body) => {
      if (method === 'POST' && body.password) return json(400, { error: 'password_reused', message: 'reused' })
    })
    const pw = await found<HTMLInputElement>('.p-password input')
    type(pw, 'Kq7mWz3NpR9xTv2H')
    const go = await found<HTMLButtonElement>('form button[type="submit"]')
    await vi.waitFor(() => expect(go.disabled).toBe(false))
    go.click()
    await vi.waitFor(() => expect(document.body.textContent).toContain(fa.account.password_reused))
  })

  it('goes on to the dashboard once the new password is confirmed', async () => {
    const { router, calls } = await open('/admin/account', () => json(200, restricted), (method, body) => {
      if (method === 'POST' && body.password) return json(200, { qr: 'data:image/png;base64,AAAA', secret: 'JBSWY3DPEHPK3PXP' })
      if (method === 'POST' && body.code) return json(200, full)
    })
    type(await found<HTMLInputElement>('.p-password input'), 'a brand new passphrase')
    const go = await found<HTMLButtonElement>('form button[type="submit"]')
    await vi.waitFor(() => expect(go.disabled).toBe(false))
    go.click()
    await vi.waitFor(() => expect(document.body.textContent).toContain('JBSWY3DPEHPK3PXP'))
    const otp = document.querySelectorAll<HTMLInputElement>('.p-inputotp-input')
    '123456'.split('').forEach((d, i) => type(otp[i], d))
    const confirm = await vi.waitFor(() => {
      const b = [...document.querySelectorAll<HTMLButtonElement>('form button[type="submit"]')].find((x) => !x.disabled)
      expect(b).toBeDefined()
      return b!
    })
    confirm.click()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('overview'))
    expect(calls.filter((c) => c === 'POST /api/v1/me/password/confirm')).toHaveLength(1)
  })
})

describe('a full session', () => {
  it('is not held on the account page', async () => {
    const { router } = await open('/admin/users', () => json(200, full))
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('users'))
  })
})
