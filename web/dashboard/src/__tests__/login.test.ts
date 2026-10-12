import { afterEach, describe, expect, it, vi } from 'vitest'
import fa from '../locales/fa.json'

// These tests start the whole app the way a browser does, from a bot login link.

const token = 'a'.repeat(43)
const me = {
  user: { id: 'u1', telegram_id: 1001, username: 'boss', role: 'owner', language: 'fa' },
  permissions: ['overview.read'],
  csrf: 'csrf-1',
  session: { method: 'link', expires_at: 2_000_000_000 },
  password: { enabled: false },
  password_available: true,
  brand: 'BOBRES',
}

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

/** Starts the app at /admin/login#t=… with a fake core; linkAnswer answers the link login. */
async function openLink(linkAnswer: () => Response) {
  vi.resetModules()
  window.history.replaceState(null, '', '/admin/login#t=' + token)
  document.body.innerHTML = '<div id="app"></div>'
  const calls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${url} ${init?.body ?? ''}`.trim())
      if (url === '/api/v1/me') return json(401, { error: 'unauthorized', message: 'log in first' })
      if (url === '/api/v1/auth/link') return linkAnswer()
      return json(503, { error: 'unavailable', message: 'not in this test' })
    }),
  )
  await import('../main')
  const { router } = await import('../router')
  await router.isReady()
  return { calls, router }
}

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('login link', () => {
  it('logs in even though the first session check is a 401', async () => {
    const { calls, router } = await openLink(() => json(200, me))
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('overview'))
    expect(calls).toContain(`POST /api/v1/auth/link {"token":"${token}"}`)
    expect(window.location.hash).toBe('') // the spent token left the address bar
  })

  it('shows why a link failed and drops the token', async () => {
    const { calls, router } = await openLink(() => json(401, { error: 'link_invalid', message: 'expired' }))
    await vi.waitFor(() => expect(document.body.textContent).toContain(fa.login.link_invalid))
    expect(calls.filter((c) => c.startsWith('POST /api/v1/auth/link'))).toHaveLength(1)
    expect(router.currentRoute.value.name).toBe('login')
    expect(window.location.hash).toBe('')
  })
})

describe('login page look', () => {
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  afterEach(unmount)

  /** Opens /admin/login logged out; brand answers GET /brand. */
  async function openLogin(brand: () => Response) {
    unmount()
    vi.resetModules()
    window.history.replaceState(null, '', '/admin/login')
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url === '/api/v1/me') return json(401, { error: 'unauthorized', message: 'log in first' })
        if (url === '/api/v1/brand') return brand()
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
  }
  const found = <T extends Element>(selector: string) =>
    vi.waitFor(() => {
      const el = document.querySelector<T>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })

  it('greets with BOBRES the beaver on the default look', async () => {
    await openLogin(() => json(200, { name: 'Shop', color: '', logo: null }))
    const img = await found<HTMLImageElement>('.login .mascot img')
    expect(img.getAttribute('src')).toContain('bobres-wave')
    expect(img.getAttribute('alt')).toBe('')
    // a still beaver for those who turn motion off
    const still = document.querySelector('.login .mascot source')
    expect(still?.getAttribute('media')).toBe('(prefers-reduced-motion: reduce)')
    expect(still?.getAttribute('srcset')).toContain('bobres-mascot')
    // bundled with the app, never from another site (the CSP allows 'self' only)
    expect(img.getAttribute('src')).toMatch(/^\//)
    expect(still?.getAttribute('srcset')).toMatch(/^\//)
    expect(document.querySelector('.login .logo')?.getAttribute('src')).toBe(import.meta.env.BASE_URL + 'favicon.svg')
  })

  it('shows a store logo and no beaver (white label)', async () => {
    await openLogin(() => json(200, { name: 'Shop', color: '', logo: '/api/v1/brand/logo?v=1' }))
    await vi.waitFor(() => expect(document.querySelector('.login .logo')?.getAttribute('src')).toBe('/api/v1/brand/logo?v=1'))
    expect(document.querySelector('.mascot')).toBeNull()
  })

  it('shows no beaver while the look is unknown', async () => {
    await openLogin(() => json(503, { error: 'unavailable', message: 'down' }))
    await found('.login .card')
    await new Promise((r) => setTimeout(r, 20))
    expect(document.querySelector('.mascot')).toBeNull()
  })
})
