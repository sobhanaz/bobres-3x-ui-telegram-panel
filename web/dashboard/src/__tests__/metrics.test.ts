import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { metricsAddress, scrapeConfig, TOKEN_MAX, TOKEN_MIN, tokenProblem } from '../metrics'

// The Prometheus card of Settings > Monitoring: what a token of one's own may look like, the configuration shown for it, and the
// card itself booted like the real app against a fake core: off, making a token (shown once), a token of one's own, replacing,
// turning off, and what an admin, who may only look, is given.

describe('a token of one’s own', () => {
  const ok = 'a'.repeat(TOKEN_MIN)
  it('is 24 to 128 characters of a bearer token', () => {
    expect(TOKEN_MIN).toBe(24)
    expect(TOKEN_MAX).toBe(128)
    expect(tokenProblem(ok)).toBe('')
    expect(tokenProblem('a'.repeat(TOKEN_MAX))).toBe('')
    expect(tokenProblem('a'.repeat(TOKEN_MIN - 1))).toBe('short')
    expect(tokenProblem('')).toBe('short')
    expect(tokenProblem('a'.repeat(TOKEN_MAX + 1))).toBe('long')
    expect(tokenProblem('bm_AbC-0123456789._~+/=xyzXYZ')).toBe('')
    for (const bad of ['a'.repeat(30) + ' b', 'a'.repeat(30) + '"', 'a'.repeat(30) + 'é', 'a'.repeat(15) + '\n' + 'a'.repeat(15), 'ا'.repeat(30)]) {
      expect(tokenProblem(bad), JSON.stringify(bad)).toBe('chars')
    }
    // the ends are trimmed, as core does
    expect(tokenProblem('  ' + ok + '  ')).toBe('')
  })
})

describe('what the card shows for Prometheus', () => {
  it('writes the address on the store’s own domain', () => {
    expect(metricsAddress('https://shop.example.com')).toBe('https://shop.example.com/metrics')
    expect(metricsAddress('https://shop.example.com/')).toBe('https://shop.example.com/metrics')
  })
  it('writes a scrape job that keeps the token in a file, not in the configuration', () => {
    const c = scrapeConfig('shop.example.com')
    expect(c).toContain('job_name: bobres')
    expect(c).toContain('scheme: https')
    expect(c).toContain('metrics_path: /metrics')
    expect(c).toContain('credentials_file: /etc/prometheus/bobres-metrics-token')
    expect(c).toContain('- targets: ["shop.example.com"]')
    expect(c.endsWith('\n')).toBe(true)
    expect(c).not.toMatch(/bm_|credentials:/)
  })
})

describe('the Prometheus card', () => {
  const ownerPerms = ['overview.read', 'settings.write', 'system.read', 'system.write', 'monitor.read']
  const adminPerms = ['overview.read', 'settings.write', 'system.read', 'monitor.read']
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const text = (path: string) => (path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa) as string | undefined) ?? path
  const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replace('{' + k + '}', v), s)
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  const TOKEN = 'bm_Zk3p9Qw2Xr7LmN4vB8tY1cD6fGhJ0aSeUiOpKl'

  type Answer = (url: string, init: RequestInit | undefined, body: Record<string, unknown>) => Response | Promise<Response> | undefined
  async function boot(permissions: string[], answer: Answer = () => undefined, role = 'owner') {
    unmount()
    vi.resetModules()
    window.history.replaceState(null, '', '/admin/settings')
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const raw = typeof init?.body === 'string' ? init.body : ''
        calls.push(`${init?.method ?? 'GET'} ${url} ${raw}`.trim())
        const own = answer(url, init, raw ? (JSON.parse(raw) as Record<string, unknown>) : {})
        if (own) return own
        if (url === '/api/v1/me') {
          return json(200, { user: { id: 'u1', telegram_id: 1, username: 'boss', role, language: 'fa' }, permissions, csrf: 'c', session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop' })
        }
        if (url === '/api/v1/settings') {
          return json(200, {
            groups: ['monitor'],
            items: [{ key: 'monitor.enabled', group: 'monitor', kind: 'bool', value: '', default: 'true', options: [], min: null, max: null, editable: true }],
          })
        }
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
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  const found = async <T extends Element>(selector: string) =>
    vi.waitFor(() => {
      const el = document.querySelector<T>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })
  const sent = (calls: string[], method: string, path: string) =>
    calls.filter((c) => c.startsWith(`${method} /api/v1${path} `) || c === `${method} /api/v1${path}`).map((c) => (c.slice(`${method} /api/v1${path}`.length).trim() ? (JSON.parse(c.slice(`${method} /api/v1${path} `.length)) as Record<string, unknown>) : {}))
  const type = (el: Element | null, value: string) => {
    const input = el as HTMLInputElement
    input.value = value
    input.dispatchEvent(new Event('input'))
  }
  const card = () => document.querySelector('.metrics-card') as HTMLElement
  const button = (root: Element, selector: string) => root.querySelector(selector) as HTMLButtonElement
  const state = (enabled: boolean) => json(200, { enabled })

  it('is off until a token is made, makes one that is shown once, and does not keep it on the page', async () => {
    let enabled = false
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { calls } = await boot(ownerPerms, (url, init) => {
      if (url === '/api/v1/metrics-token' && (init?.method ?? 'GET') === 'GET') return state(enabled)
      if (url === '/api/v1/metrics-token/generate' && init?.method === 'POST') {
        enabled = true
        return json(200, { enabled: true, token: TOKEN })
      }
    })
    await found('.metrics-card')
    await vi.waitFor(() => expect(card().querySelector('.state')?.textContent).toBe(text('prometheus.off')))
    expect(card().textContent).toContain(text('prometheus.off_help'))
    expect(card().textContent).toContain(window.location.origin + '/metrics')
    expect(button(card(), 'button.turn-off')).toBeNull()
    expect(button(card(), 'button.generate').textContent).toContain(text('prometheus.make'))

    // making a token needs no confirmation while there is none to replace; it is shown once, with the job to add
    button(card(), 'button.generate').click()
    const d = await found('.metrics-token-dialog')
    expect(sent(calls, 'POST', '/metrics-token/generate')).toHaveLength(1)
    expect((d.querySelector('input.token') as HTMLInputElement).value).toBe(TOKEN)
    expect((d.querySelector('input.address') as HTMLInputElement).value).toBe(window.location.origin + '/metrics')
    expect((d.querySelector('textarea.config') as HTMLTextAreaElement).value).toContain(`- targets: ["${window.location.host}"]`)
    expect((d.querySelector('textarea.config') as HTMLTextAreaElement).value).not.toContain(TOKEN)
    await vi.waitFor(() => expect(card().querySelector('.state')?.textContent).toBe(text('prometheus.on')))

    // closing without having copied it asks first; keeping it open leaves it there
    button(d, 'button.done').click()
    ;(await found<HTMLButtonElement>('.p-confirmdialog-reject-button')).click()
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog')).toBeNull())
    expect(document.querySelector('.metrics-token-dialog')).not.toBeNull()
    // copied: it closes at once, and the token is gone from the page
    button(d, 'button.copy').click()
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(TOKEN))
    await vi.waitFor(() => expect(document.querySelector('.p-toast')?.textContent).toContain(text('prometheus.token_copied')))
    button(d, 'button.done').click()
    await vi.waitFor(() => expect(document.querySelector('.metrics-token-dialog')).toBeNull())
    expect(document.body.innerHTML).not.toContain(TOKEN)
    expect(document.querySelector('.p-confirmdialog')).toBeNull()
  })

  it('asks before replacing a token, and before turning the metrics off', async () => {
    let enabled = true
    const { calls } = await boot(ownerPerms, (url, init) => {
      if (url === '/api/v1/metrics-token' && (init?.method ?? 'GET') === 'GET') return state(enabled)
      if (url === '/api/v1/metrics-token/generate') return json(200, { enabled: true, token: TOKEN })
      if (url === '/api/v1/metrics-token' && init?.method === 'DELETE') {
        enabled = false
        return state(false)
      }
    })
    await found('.metrics-card .state')
    await vi.waitFor(() => expect(card().querySelector('.state')?.textContent).toBe(text('prometheus.on')))
    expect(card().textContent).toContain(text('prometheus.on_help'))
    expect(button(card(), 'button.generate').textContent).toContain(text('prometheus.make_new'))

    // replacing: the old token stops working at once, so the owner is told and can back out
    button(card(), 'button.generate').click()
    const ask = await found('.p-confirmdialog')
    expect(ask.textContent).toContain(text('prometheus.replace_confirm'))
    ;(ask.querySelector('.p-confirmdialog-reject-button') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog')).toBeNull())
    expect(sent(calls, 'POST', '/metrics-token/generate')).toHaveLength(0)
    button(card(), 'button.generate').click()
    ;(await found<HTMLButtonElement>('.p-confirmdialog-accept-button')).click()
    await found('.metrics-token-dialog')
    expect(sent(calls, 'POST', '/metrics-token/generate')).toHaveLength(1)
    ;(document.querySelector('.metrics-token-dialog button.copy') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(document.querySelector('.p-toast')?.textContent).toContain(text('prometheus.token_copied')))
    ;(document.querySelector('.metrics-token-dialog button.done') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(document.querySelector('.metrics-token-dialog')).toBeNull())

    // turning off
    button(card(), 'button.turn-off').click()
    const off = await found('.p-confirmdialog')
    expect(off.textContent).toContain(text('prometheus.off_confirm'))
    ;(off.querySelector('.p-confirmdialog-accept-button') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(sent(calls, 'DELETE', '/metrics-token')).toHaveLength(1))
    await vi.waitFor(() => expect(card().querySelector('.state')?.textContent).toBe(text('prometheus.off')))
    expect(button(card(), 'button.turn-off')).toBeNull()
  })

  it('takes a token of the owner’s own, checked as it is typed and never shown again', async () => {
    let refuse = true
    const { calls } = await boot(ownerPerms, (url, init, body) => {
      if (url === '/api/v1/metrics-token' && (init?.method ?? 'GET') === 'GET') return state(false)
      if (url === '/api/v1/metrics-token' && init?.method === 'PUT') {
        if (refuse) {
          refuse = false
          return json(400, { error: 'invalid', field: 'token', message: 'token: only letters, digits and . _ ~ + / = - are allowed' })
        }
        return state(typeof body.token === 'string')
      }
    })
    await found('.metrics-card .own-toggle')
    expect(card().querySelector('.own-form')).toBeNull()
    button(card(), '.own-toggle').click()
    const input = await found<HTMLInputElement>('input.own-input')
    expect(input.type).toBe('password')
    expect(input.autocomplete).toBe('new-password')
    const save = () => button(card(), 'button.save-own')
    expect(save().disabled).toBe(true)
    type(input, 'too short')
    await vi.waitFor(() => expect(card().querySelector('.own-problem')?.textContent).toBe(fill(fill(text('prometheus.invalid.short'), { min: '۲۴' }), {})))
    expect(save().disabled).toBe(true)
    type(input, 'x'.repeat(30) + ' y')
    await vi.waitFor(() => expect(card().querySelector('.own-problem')?.textContent).toBe(text('prometheus.invalid.chars')))
    type(input, 'my-own-token-0123456789-ABCDEFGH')
    await vi.waitFor(() => expect(card().querySelector('.own-problem')).toBeNull())
    await vi.waitFor(() => expect(save().disabled).toBe(false))
    // core's words come back under the field, the typed token stays for a correction
    save().click()
    await vi.waitFor(() => expect(card().textContent).toContain('only letters, digits'))
    expect(input.classList.contains('p-invalid')).toBe(true)
    save().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/metrics-token')).toHaveLength(2))
    expect(sent(calls, 'PUT', '/metrics-token')[1]).toEqual({ token: 'my-own-token-0123456789-ABCDEFGH' })
    await vi.waitFor(() => expect(document.querySelector('.p-toast')?.textContent).toContain(text('prometheus.saved')))
    // the form closes and forgets what was typed
    await vi.waitFor(() => expect(card().querySelector('.own-form')).toBeNull())
    expect(document.body.innerHTML).not.toContain('my-own-token-0123456789-ABCDEFGH')
  })

  it('shows an admin whether it is on and nothing to change it with', async () => {
    const { calls } = await boot(
      adminPerms,
      (url, init) => {
        if (url === '/api/v1/metrics-token' && (init?.method ?? 'GET') === 'GET') return state(true)
      },
      'admin',
    )
    await found('.metrics-card .state')
    await vi.waitFor(() => expect(card().querySelector('.state')?.textContent).toBe(text('prometheus.on')))
    expect(card().querySelector('.owner-only')?.textContent).toBe(text('prometheus.owner_only'))
    for (const sel of ['button.generate', 'button.own-toggle', 'button.turn-off']) expect(card().querySelector(sel), sel).toBeNull()
    expect(calls.filter((c) => c.startsWith('POST') || c.startsWith('PUT') || c.startsWith('DELETE'))).toEqual([])
  })

  it('says so when the state cannot be read, and tries again', async () => {
    let fail = true
    await boot(ownerPerms, (url, init) => {
      if (url === '/api/v1/metrics-token' && (init?.method ?? 'GET') === 'GET') {
        if (fail) return json(500, { error: 'internal', message: 'x' })
        return state(false)
      }
    })
    await found('.metrics-card .load-error')
    expect(card().querySelector('button.generate')).toBeNull()
    fail = false
    ;(card().querySelector('.load-error button') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(card().querySelector('button.generate')).not.toBeNull())
  })

  it('has its texts in both languages', () => {
    const flat = (o: Record<string, unknown>, prefix = ''): string[] => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v as Record<string, unknown>, prefix + k + '.') : [prefix + k]))
    expect(flat(fa.prometheus).sort()).toEqual(flat(en.prometheus).sort())
    for (const k of ['generate', 'set', 'clear']) {
      expect((en.audit.action as unknown as Record<string, Record<string, Record<string, string>>>).metrics.token[k]).toBeTruthy()
      expect((fa.audit.action as unknown as Record<string, Record<string, Record<string, string>>>).metrics.token[k]).toBeTruthy()
    }
  })
})
