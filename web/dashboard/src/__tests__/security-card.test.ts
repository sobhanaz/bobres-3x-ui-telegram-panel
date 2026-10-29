import { afterEach, describe, expect, it, vi } from 'vitest'
import fa from '../locales/fa.json'
import { APPROVAL_COMMAND, clock, isPending, pendingState, reasonKey, secondsLeft, SLOW_AFTER_S, type PendingLogin, type SecurityReply } from '../security'

// Settings > Sign-in security: the state of the approval of sign-ins, and the owner's way to turn it on (with what it does shown and their word
// given) and off. The server is a fetch that answers by address; the page is the real app.

const lookup = (path: string): string => {
  const s = path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa)
  if (typeof s !== 'string') throw new Error(`fa.json has no text "${path}"`)
  return s
}
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

const security = (over: Partial<SecurityReply['login_approval']> = {}, canChange = true): SecurityReply => ({
  login_approval: { enabled: false, notify_owner: true, ttl_s: 300, bot_ready: true, waiting: 0, command: APPROVAL_COMMAND, ...over },
  can_change: canChange,
})

type Answer = (method: string, url: string, body: Record<string, unknown>) => Response | undefined

describe('Settings > Sign-in security', () => {
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  async function boot(o: { state?: SecurityReply; role?: string; reauth?: boolean; answer?: Answer } = {}) {
    unmount()
    vi.resetModules()
    window.history.replaceState(null, '', '/admin/settings')
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    let state = o.state ?? security()
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        const raw = typeof init?.body === 'string' ? init.body : ''
        calls.push(`${method} ${url} ${raw}`.trim())
        const body = raw ? (JSON.parse(raw) as Record<string, unknown>) : {}
        const own = o.answer?.(method, url, body)
        if (own) return own
        if (url === '/api/v1/me') {
          return json(200, {
            user: { id: 'u1', telegram_id: 1, username: 'boss', role: o.role ?? 'owner', language: 'fa' },
            permissions: ['overview.read', 'settings.write', 'system.read', ...(o.role === 'admin' ? [] : ['security.write'])], csrf: 'c',
            session: { method: 'link', expires_at: 2e9 }, password: o.reauth ? { enabled: true, reauth: true } : { enabled: false }, password_available: true, brand: 'Shop',
          })
        }
        if (url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null })
        if (url === '/api/v1/settings') return json(200, { items: [], groups: [] })
        if (method === 'GET' && url === '/api/v1/security') return json(200, state)
        if (method === 'PUT' && url === '/api/v1/security/login-approval') {
          state = security({ enabled: body.enabled === true, notify_owner: body.notify_owner !== false })
          return json(200, state)
        }
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { calls }
  }
  afterEach(() => {
    unmount()
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
  const type = (el: HTMLInputElement, value: string) => {
    el.value = value
    el.dispatchEvent(new Event('input'))
  }
  const puts = (calls: string[]) => calls.filter((c) => c.startsWith('PUT /api/v1/security/login-approval ')).map((c) => JSON.parse(c.slice('PUT /api/v1/security/login-approval '.length)) as Record<string, unknown>)

  it('shows the owner that it is off, and turns it on only with the dialog read and ticked', async () => {
    const { calls } = await boot()
    await vi.waitFor(() => expect(text()).toContain(lookup('security.approval.title')))
    expect(text()).toContain(lookup('security.approval.off'))
    const on = await found<HTMLButtonElement>('.turn-on')
    expect(on.disabled).toBe(false)
    on.click()
    const dialog = await found<HTMLElement>('.security-dialog')
    // What it does, in the dialog: the command that is the way out among it.
    expect(dialog.textContent).toContain(lookup('security.approval.dialog.point_what'))
    expect(dialog.querySelector('code')?.textContent).toBe(APPROVAL_COMMAND)
    const submit = dialog.querySelector<HTMLButtonElement>('.submit')!
    expect(submit.disabled).toBe(true) // not until the owner says they read it
    expect(dialog.querySelector('input[inputmode="numeric"]')).toBeNull() // no code asked: no password login with an authenticator

    dialog.querySelector<HTMLInputElement>('#security-acknowledge')!.click()
    await vi.waitFor(() => expect(submit.disabled).toBe(false))
    submit.click()
    await vi.waitFor(() => expect(puts(calls)).toHaveLength(1))
    expect(puts(calls)[0]).toEqual({ enabled: true, acknowledge: true, notify_owner: true })
    await vi.waitFor(() => expect(document.querySelector('.security-dialog')).toBeNull())
    await vi.waitFor(() => expect(document.querySelector('.turn-off')).not.toBeNull())
    expect(text()).toContain(lookup('security.approval.on'))
    // The way out is always in view once it is on.
    expect(document.querySelector('.way-out code')?.textContent).toBe(APPROVAL_COMMAND)
  })

  it('asks for the authenticator code when the session needs one, and holds the button back until it is six digits', async () => {
    const { calls } = await boot({ reauth: true })
    ;(await found<HTMLButtonElement>('.turn-on')).click()
    const dialog = await found<HTMLElement>('.security-dialog')
    dialog.querySelector<HTMLInputElement>('#security-acknowledge')!.click()
    const code = dialog.querySelector<HTMLInputElement>('input[inputmode="numeric"]')!
    const submit = dialog.querySelector<HTMLButtonElement>('.submit')!
    type(code, '12345')
    await vi.waitFor(() => expect(submit.disabled).toBe(true))
    type(code, '123456')
    await vi.waitFor(() => expect(submit.disabled).toBe(false))
    submit.click()
    await vi.waitFor(() => expect(puts(calls)).toHaveLength(1))
    expect(puts(calls)[0]).toMatchObject({ enabled: true, acknowledge: true, code: '123456' })
  })

  it('asks for the code when core says it is needed, and reads a wrong one', async () => {
    let n = 0
    await boot({
      answer: (method, url) => {
        if (method !== 'PUT' || url !== '/api/v1/security/login-approval') return undefined
        n++
        return n === 1 ? json(403, { error: 'code_required', message: 'x' }) : json(400, { error: 'code_wrong', message: 'x' })
      },
    })
    ;(await found<HTMLButtonElement>('.turn-on')).click()
    const dialog = await found<HTMLElement>('.security-dialog')
    dialog.querySelector<HTMLInputElement>('#security-acknowledge')!.click()
    const submit = dialog.querySelector<HTMLButtonElement>('.submit')!
    await vi.waitFor(() => expect(submit.disabled).toBe(false))
    submit.click()
    await vi.waitFor(() => expect(dialog.textContent).toContain(lookup('account.code_required')))
    const code = await found<HTMLInputElement>('.security-dialog input[inputmode="numeric"]')
    type(code, '000000')
    await vi.waitFor(() => expect(submit.disabled).toBe(false))
    submit.click()
    await vi.waitFor(() => expect(dialog.textContent).toContain(lookup('account.code_wrong')))
    expect(text()).toContain(lookup('security.approval.off')) // nothing changed
  })

  it('keeps it off and says why when the bot cannot reach the owner', async () => {
    await boot({
      answer: (method, url) => {
        if (method === 'PUT' && url === '/api/v1/security/login-approval') return json(409, { error: 'approval_unavailable', reason: 'blocked', command: APPROVAL_COMMAND, message: 'x' })
      },
    })
    ;(await found<HTMLButtonElement>('.turn-on')).click()
    const dialog = await found<HTMLElement>('.security-dialog')
    dialog.querySelector<HTMLInputElement>('#security-acknowledge')!.click()
    const submit = dialog.querySelector<HTMLButtonElement>('.submit')!
    await vi.waitFor(() => expect(submit.disabled).toBe(false))
    submit.click()
    await vi.waitFor(() => expect(dialog.textContent).toContain(lookup('security.approval.unavailable.blocked')))
    expect(document.querySelector('.turn-off')).toBeNull()
    expect(text()).toContain(lookup('security.approval.off'))
  })

  it('does not offer to turn it on when no bot is connected', async () => {
    await boot({ state: security({ bot_ready: false }) })
    const on = await found<HTMLButtonElement>('.turn-on')
    expect(on.disabled).toBe(true)
    expect(text()).toContain(lookup('security.approval.no_bot'))
  })

  it('turns it off with a short confirmation, and changes who is asked', async () => {
    const { calls } = await boot({ state: security({ enabled: true, waiting: 2 }) })
    await vi.waitFor(() => expect(text()).toContain(lookup('security.approval.on')))
    expect(text()).toContain(lookup('security.approval.waiting').replace('{n}', '۲'))

    // The switch for the owners opens its own confirmation.
    const notify = await found<HTMLInputElement>('#security-notify-owner')
    expect(notify.checked).toBe(true)
    notify.click()
    const ask = await found<HTMLElement>('.security-dialog')
    expect(ask.textContent).toContain(lookup('security.approval.dialog.notify_off_text'))
    ask.querySelector<HTMLButtonElement>('.submit')!.click()
    await vi.waitFor(() => expect(puts(calls)).toHaveLength(1))
    expect(puts(calls)[0]).toEqual({ enabled: true, notify_owner: false })
    await vi.waitFor(() => expect(document.querySelector('.security-dialog')).toBeNull())

    ;(await found<HTMLButtonElement>('.turn-off')).click()
    const off = await found<HTMLElement>('.security-dialog')
    expect(off.textContent).toContain(lookup('security.approval.dialog.off_text'))
    expect(off.querySelector('#security-acknowledge')).toBeNull() // turning it off needs no tick
    off.querySelector<HTMLButtonElement>('.submit')!.click()
    await vi.waitFor(() => expect(puts(calls)).toHaveLength(2))
    expect(puts(calls)[1]).toEqual({ enabled: false })
    await vi.waitFor(() => expect(document.querySelector('.turn-on')).not.toBeNull())
  })

  it('shows an admin the state and no way to change it', async () => {
    await boot({ role: 'admin', state: security({ enabled: true }, false) })
    await vi.waitFor(() => expect(text()).toContain(lookup('security.approval.read_only')))
    expect(document.querySelector('.turn-on')).toBeNull()
    expect(document.querySelector('.turn-off')).toBeNull()
    expect((document.querySelector('#security-notify-owner') as HTMLInputElement).disabled).toBe(true)
  })
})

describe('the rules of a wait', () => {
  const wait = (over: Partial<PendingLogin> = {}): PendingLogin => ({
    status: 'pending', expires_at: 1000, approvers: ['self'], delivered: true, failed: false, ttl_s: 300, command: APPROVAL_COMMAND, ...over,
  })

  it('tells a wait from a session', () => {
    expect(isPending(wait())).toBe(true)
    expect(isPending({ status: 'approved' })).toBe(false)
    expect(isPending({ user: {}, csrf: 'x' })).toBe(false)
    expect(isPending(null)).toBe(false)
    expect(isPending('pending')).toBe(false)
  })

  it('counts down to zero and writes minutes and seconds', () => {
    expect(secondsLeft(1000, 700_000)).toBe(300)
    expect(secondsLeft(1000, 999_100)).toBe(1)
    expect(secondsLeft(1000, 1_000_000)).toBe(0)
    expect(secondsLeft(1000, 2_000_000)).toBe(0)
    expect(clock(300)).toBe('5:00')
    expect(clock(65)).toBe('1:05')
    expect(clock(9)).toBe('0:09')
    expect(clock(-3)).toBe('0:00')
  })

  it('says who was asked, and when something is wrong with the message', () => {
    expect(pendingState(wait(), 3)).toBe('self')
    expect(pendingState(wait({ approvers: ['self', 'owner'] }), 3)).toBe('both')
    expect(pendingState(wait({ delivered: false }), SLOW_AFTER_S - 1)).toBe('self')
    expect(pendingState(wait({ delivered: false }), SLOW_AFTER_S)).toBe('slow')
    expect(pendingState(wait({ delivered: true }), 200)).toBe('self')
    expect(pendingState(wait({ delivered: false, failed: true }), 0)).toBe('failed')
  })

  it('words a reason core named, and any other as the general one', () => {
    for (const r of ['no_bot', 'bot_down', 'blocked', 'telegram', 'no_answer', 'no_approver']) expect(reasonKey(r)).toBe(r)
    expect(reasonKey('something new')).toBe('other')
    expect(reasonKey(undefined)).toBe('other')
    for (const r of ['no_bot', 'bot_down', 'blocked', 'telegram', 'no_answer', 'no_approver', 'other']) {
      expect(lookup('login.approval.' + r)).toContain('{cmd}')
      expect(lookup('security.approval.unavailable.' + r).length).toBeGreaterThan(10)
    }
  })
})
