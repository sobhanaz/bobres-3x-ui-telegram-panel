import { afterEach, describe, expect, it, vi } from 'vitest'
import fa from '../locales/fa.json'
import { APPROVAL_COMMAND, POLL_MS } from '../security'

// The login page while the owner has the approval of sign-ins in Telegram on: a login answers a wait instead of a session, the page asks
// how the wait stands until it ends, and says what to do when no approval can come. The server is a fetch that answers by address; the
// page is the real app.

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

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const lookup = (path: string): string => {
  const s = path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa)
  if (typeof s !== 'string') throw new Error(`fa.json has no text "${path}"`)
  return s
}
const nowS = () => Math.floor(Date.now() / 1000)
const waitBody = (over: Record<string, unknown> = {}) => ({
  status: 'pending', expires_at: nowS() + 290, approvers: ['self'], delivered: true, failed: false, ttl_s: 300, command: APPROVAL_COMMAND, ...over,
})

type Answer = (method: string, url: string, body: string) => Response | undefined

describe('login while sign-ins wait for an approval', () => {
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  async function open(path: string, answer: Answer) {
    unmount()
    vi.resetModules()
    vi.useFakeTimers({ shouldAdvanceTime: true })
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        const body = typeof init?.body === 'string' ? init.body : ''
        calls.push(`${method} ${url} ${body}`.trim())
        const own = answer(method, url, body)
        if (own) return own
        if (url === '/api/v1/me') return json(401, { error: 'unauthorized', message: 'log in first' })
        if (url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null })
        if (url === '/api/v1/auth/pending') return json(200, { status: 'none' })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { calls, router }
  }
  afterEach(() => {
    unmount()
    vi.useRealTimers()
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
  })
  const text = () => document.body.textContent ?? ''
  const found = <T extends Element>(selector: string) =>
    vi.waitFor(() => {
      const el = document.querySelector<T>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })
  const polls = (calls: string[]) => calls.filter((c) => c.startsWith('POST /api/v1/auth/pending')).length

  it('shows the wait after a login link, keeps asking, and goes in once it was approved', async () => {
    let asked = 0
    const { calls, router } = await open('/admin/login#t=' + token, (method, url) => {
      if (method === 'POST' && url === '/api/v1/auth/link') return json(202, waitBody({ approvers: ['self', 'owner'] }))
      if (method === 'POST' && url === '/api/v1/auth/pending') {
        asked++
        return asked < 3 ? json(200, waitBody({ approvers: ['self', 'owner'] })) : json(200, { ...me, status: 'approved' })
      }
    })
    await vi.waitFor(() => expect(text()).toContain(lookup('login.pending_title')))
    expect(text()).toContain(lookup('login.pending_both'))
    expect(document.querySelector('.waiting .bar')).not.toBeNull()
    expect(document.querySelector('form')).toBeNull() // the password form is out of the way
    expect(window.location.hash).toBe('') // the spent token left the address bar
    expect(router.currentRoute.value.name).toBe('login')
    // Nothing about the way out while the message is on its way.
    expect(text()).not.toContain(APPROVAL_COMMAND)

    await vi.advanceTimersByTimeAsync(POLL_MS * 3 + 100)
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('overview'))
    expect(polls(calls)).toBeGreaterThanOrEqual(3)
    expect(calls).toContain(`POST /api/v1/auth/link {"token":"${token}"}`)
  })

  it('asks only the staff member when nobody else was asked', async () => {
    await open('/admin/login#t=' + token, (method, url) => {
      if (method === 'POST' && url === '/api/v1/auth/link') return json(202, waitBody())
    })
    await vi.waitFor(() => expect(text()).toContain(lookup('login.pending_self')))
  })

  it('waits after a password login too, and a denial ends it with a word and the form back', async () => {
    let denied = false
    let started = false // the page looks once at load, before any login: nothing waits then
    const { calls } = await open('/admin/login', (method, url) => {
      if (method === 'POST' && url === '/api/v1/auth/login') {
        started = true
        return json(202, waitBody())
      }
      if (method === 'POST' && url === '/api/v1/auth/pending') {
        if (!started) return json(200, { status: 'none' })
        return denied ? json(200, { status: 'denied' }) : json(200, waitBody())
      }
    })
    const type = (el: HTMLInputElement, value: string) => {
      el.value = value
      el.dispatchEvent(new Event('input', { bubbles: true }))
    }
    type(await found<HTMLInputElement>('input[autocomplete="username"]'), 'boss')
    type(await found<HTMLInputElement>('.p-password input'), 'a long password')
    const submit = await found<HTMLButtonElement>('button[type="submit"]')
    await vi.waitFor(() => expect(submit.disabled).toBe(false))
    submit.click()
    await vi.waitFor(() => expect(text()).toContain(lookup('login.pending_title')))
    expect(calls.some((c) => c.startsWith('POST /api/v1/auth/login') && c.includes('"username":"boss"'))).toBe(true)

    denied = true
    await vi.advanceTimersByTimeAsync(POLL_MS + 100)
    await vi.waitFor(() => expect(text()).toContain(lookup('login.ended.denied')))
    expect(text()).not.toContain(lookup('login.pending_title'))
    await found('form') // the sign-in form is back
  })

  it('lets the person give up, and the form comes back', async () => {
    const { calls } = await open('/admin/login#t=' + token, (method, url) => {
      if (method === 'POST' && url === '/api/v1/auth/link') return json(202, waitBody())
      if (method === 'DELETE' && url === '/api/v1/auth/pending') return new Response(null, { status: 204 })
      if (method === 'POST' && url === '/api/v1/auth/pending') return json(200, waitBody())
    })
    const cancel = await found<HTMLButtonElement>('.waiting .cancel')
    cancel.click()
    await vi.waitFor(() => expect(document.querySelector('.waiting')).toBeNull())
    expect(calls).toContain('DELETE /api/v1/auth/pending')
    await found('form')
  })

  it('says why no approval can come, with the command that signs in without one', async () => {
    for (const reason of ['bot_down', 'blocked', 'no_bot']) {
      await open('/admin/login#t=' + token, (method, url) => {
        if (method === 'POST' && url === '/api/v1/auth/link') {
          return json(503, { error: 'approval_unavailable', reason, command: APPROVAL_COMMAND, message: 'x' })
        }
      })
      await vi.waitFor(() => expect(text()).toContain(lookup('login.approval.' + reason).replace('{cmd}', APPROVAL_COMMAND)))
      expect(document.querySelector('.unavailable code')?.textContent).toBe(APPROVAL_COMMAND)
      expect(document.querySelector('.waiting')).toBeNull()
    }
  })

  it('tells an account that asked too often to wait', async () => {
    await open('/admin/login#t=' + token, (method, url) => {
      if (method === 'POST' && url === '/api/v1/auth/link') return json(429, { error: 'approval_rate_limited', message: 'x' })
    })
    await vi.waitFor(() => expect(text()).toContain(lookup('login.approval_rate_limited')))
  })

  it('finds a wait again after a reload, without any login', async () => {
    const { calls } = await open('/admin/login', (method, url) => {
      if (method === 'POST' && url === '/api/v1/auth/pending') return json(200, waitBody({ approvers: ['self', 'owner'] }))
    })
    await vi.waitFor(() => expect(text()).toContain(lookup('login.pending_both')))
    expect(calls.some((c) => c.startsWith('POST /api/v1/auth/link') || c.startsWith('POST /api/v1/auth/login'))).toBe(false)
  })

  it('goes in at once when the sign-in was approved while the page was reloading', async () => {
    const { router } = await open('/admin/login', (method, url) => {
      if (method === 'POST' && url === '/api/v1/auth/pending') return json(200, { ...me, status: 'approved' })
    })
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('overview'))
  })

  it('names the way out when the message is slow to reach Telegram, or never did', async () => {
    // 25 of the 300 seconds are gone and nothing was delivered.
    await open('/admin/login#t=' + token, (method, url) => {
      if (method === 'POST' && url === '/api/v1/auth/link') return json(202, waitBody({ delivered: false, expires_at: nowS() + 275 }))
    })
    await vi.waitFor(() => expect(text()).toContain(lookup('login.pending_slow')))
    expect(document.querySelector('.waiting code')?.textContent).toBe(APPROVAL_COMMAND)

    await open('/admin/login#t=' + token, (method, url) => {
      if (method === 'POST' && url === '/api/v1/auth/link') return json(202, waitBody({ delivered: false, failed: true }))
    })
    await vi.waitFor(() => expect(text()).toContain(lookup('login.pending_failed')))
    expect(document.querySelector('.waiting code')?.textContent).toBe(APPROVAL_COMMAND)
  })

  it('keeps waiting through a failed poll', async () => {
    let asked = 0
    const { router } = await open('/admin/login#t=' + token, (method, url) => {
      if (method === 'POST' && url === '/api/v1/auth/link') return json(202, waitBody())
      if (method === 'POST' && url === '/api/v1/auth/pending') {
        asked++
        if (asked === 1) throw new TypeError('network down') // the fetch itself fails
        return json(200, { ...me, status: 'approved' })
      }
    })
    await vi.waitFor(() => expect(text()).toContain(lookup('login.pending_title')))
    await vi.advanceTimersByTimeAsync(POLL_MS * 3)
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('overview'))
  })
})
