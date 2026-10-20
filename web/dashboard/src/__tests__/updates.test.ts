import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { ApiError } from '../api'
import { dateTime } from '../format'
import {
  copyText,
  helperProblem,
  isFinal,
  isPrerelease,
  issuedAt,
  page,
  pollDelay,
  progressView,
  refusalOf,
  stepList,
  updateCommand,
  updatePoll,
  UPDATE_STEPS,
} from '../updates'
import type { UpdateHelper, UpdateRun, UpdatesProgress, UpdatesState } from '../types'

// The Updates tab: what is newest, "Check again", the channel and "Update now",
// with the progress of an update that restarts core in the middle of it.

const NOW = Math.floor(Date.now() / 1000)
const ID = '0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f60'

const helper = (over: Partial<UpdateHelper> = {}): UpdateHelper => ({
  available: true, problem: '', version: '0.7.0', state: 'idle', id: '', from: '', to: '', channel: '', step: '', message: '', started_at: null, finished_at: null, log: [], ...over,
})
const run = (over: Partial<UpdateRun> = {}): UpdateRun => ({
  id: ID, by: 'boss', from: '0.6.1', to: '0.7.0', channel: 'stable', requested_at: NOW - 60, outcome: '', outcome_at: null, message: '', phase: 'queued', ...over,
})
const state = (over: Partial<UpdatesState> = {}): UpdatesState => ({
  version: '0.6.1',
  check: {
    current: '0.6.1', latest: '0.7.0', newer: true, verified: true, checked_at: NOW - 3600, error: '', detail: '', channel: 'stable', issued: '2026-10-20T10:00Z',
    prerelease: false, notes_url: 'https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.7.0',
  },
  channel: { setting: '', effective: 'stable', source: 'server' },
  helper: helper(),
  run: null,
  last: null,
  apply: { allowed: true, ready: true, reason: '', target: '0.7.0' },
  ...over,
})

describe('what the page knows of an update', () => {
  const progress = (over: Partial<UpdatesProgress> = {}): UpdatesProgress => ({ version: '0.6.1', helper: helper(), run: null, last: null, ...over })

  it('reads the update core has open, with the updater\'s words only when its status names that update', () => {
    // asked for, not taken up: queued, and nothing of what an old status says
    const queued = progressView(progress({ run: run(), helper: helper({ state: 'done', id: 'another', message: 'old', step: 'done', log: ['old line'] }) }), '')
    expect(queued).toMatchObject({ phase: 'queued', mine: true, from: '0.6.1', to: '0.7.0', by: 'boss', step: '', message: '', log: [] })
    // running: the updater's step, sentence and log
    const running = progressView(
      progress({ run: run({ phase: 'running' }), helper: helper({ state: 'running', id: ID, step: 'pull', message: 'Pulling.', log: ['a', 'b'], started_at: NOW - 30 }) }), ID)
    expect(running).toMatchObject({ phase: 'running', step: 'pull', message: 'Pulling.', log: ['a', 'b'] })
    // the updater finished, core has not ended the record yet
    expect(progressView(progress({ run: run({ phase: 'rolled_back' }), helper: helper({ state: 'rolled_back', id: ID, message: 'went back' }) }), ID)).toMatchObject({ phase: 'rolled_back', message: 'went back' })
    // a phase nobody knows is queued, never an invented state
    expect(progressView(progress({ run: run({ phase: 'exploding' }) }), '')?.phase).toBe('queued')
    // an updater that cannot be read says nothing
    expect(progressView(progress({ run: run({ phase: 'running' }), helper: helper({ available: false, problem: 'bad_status', id: ID, step: 'pull' }) }), '')).toMatchObject({ step: '', log: [] })
  })

  it('shows the end of the update this page started, and only that one', () => {
    const ended = run({ phase: undefined, outcome: 'done', outcome_at: NOW, message: 'Updated.' })
    expect(progressView(progress({ last: ended }), ID)).toMatchObject({ phase: 'done', mine: true, message: 'Updated.', finishedAt: NOW })
    // not tracked: an update that ended earlier is the Last update line's, not a panel
    expect(progressView(progress({ last: ended }), '')).toBeNull()
    expect(progressView(progress({ last: ended }), 'another')).toBeNull()
    for (const outcome of ['failed', 'rolled_back', 'not_started']) {
      expect(progressView(progress({ last: run({ outcome, outcome_at: NOW, phase: undefined }) }), ID)?.phase).toBe(outcome)
    }
  })

  it('follows an update that was started on the server', () => {
    const elsewhere = progressView(progress({ helper: helper({ state: 'running', id: '', from: '0.6.1', to: '0.7.0', channel: 'stable', step: 'backup', message: 'Backing up.', started_at: NOW - 5 }) }), '')
    expect(elsewhere).toMatchObject({ phase: 'running', mine: false, from: '0.6.1', to: '0.7.0', step: 'backup', by: '' })
    expect(progressView(progress(), '')).toBeNull()
    expect(progressView(progress({ helper: helper({ state: 'done', id: ID }) }), '')).toBeNull()
    expect(progressView(progress({ helper: helper({ available: false, problem: 'no_dir', state: 'running' }) }), '')).toBeNull()
    expect(progressView(null, ID)).toBeNull()
  })

  it('lists the steps by where the update is', () => {
    const states = (phase: Parameters<typeof stepList>[0]['phase'], step: string) => stepList({ phase, step }).map((s) => s.key + ':' + s.state[0]).join(' ')
    expect(states('queued', '')).toBe('verify:p backup:p pull:p restart:p health:p')
    expect(states('running', '')).toBe('verify:a backup:p pull:p restart:p health:p')
    expect(states('running', 'pull')).toBe('verify:d backup:d pull:a restart:p health:p')
    expect(states('running', 'health')).toBe('verify:d backup:d pull:d restart:d health:a')
    expect(states('running', 'done')).toBe('verify:d backup:d pull:d restart:d health:d')
    expect(states('done', 'done')).toBe('verify:d backup:d pull:d restart:d health:d')
    expect(states('failed', 'backup')).toBe('verify:d backup:f pull:p restart:p health:p')
    expect(states('failed', '')).toBe('verify:p backup:p pull:p restart:p health:p')
    expect(states('not_started', '')).toBe('verify:p backup:p pull:p restart:p health:p')
    // going back: which step failed is not known, so only that step is listed
    expect(states('running', 'rollback')).toBe('rollback:a')
    expect(states('rolled_back', 'rollback')).toBe('rollback:d')
    expect(states('rolled_back', '')).toBe('rollback:d')
    expect(UPDATE_STEPS).toEqual(['verify', 'backup', 'pull', 'restart', 'health'])
    for (const lang of [fa, en]) {
      for (const k of [...UPDATE_STEPS, 'rollback']) expect((lang.system.updates.progress.step as Record<string, string>)[k]).toBeTruthy()
    }
  })

  it('knows when an update is over, and which releases are pre-releases', () => {
    for (const p of ['done', 'failed', 'rolled_back', 'not_started'] as const) expect(isFinal(p)).toBe(true)
    for (const p of ['queued', 'running', ''] as const) expect(isFinal(p)).toBe(false)
    expect(isPrerelease('0.7.0-rc.1')).toBe(true)
    expect(isPrerelease('0.7.0')).toBe(false)
    expect(isPrerelease('')).toBe(false)
    expect(isPrerelease(null)).toBe(false)
    expect(isPrerelease('dev')).toBe(false)
  })

  it('reads when the release was signed', () => {
    expect(issuedAt({ issued: '2026-10-20T10:00Z' })).toBe(Date.UTC(2026, 9, 20, 10, 0) / 1000)
    for (const bad of ['', 'yesterday', '2026-10-20', '2026-10-20T10:00:00Z', undefined]) expect(issuedAt({ issued: bad })).toBeNull()
    expect(issuedAt(null)).toBeNull()
  })

  it('gives the server command for the channel the page shows, never the .env\'s', () => {
    expect(updateCommand('stable')).toBe('sudo bobres update --channel stable')
    expect(updateCommand(' Beta ')).toBe('sudo bobres update --channel beta')
    for (const none of ['', null, undefined, 'nightly']) expect(updateCommand(none)).toBe('sudo bobres update')
  })

  it('reads why core refused, by the code beside the error or the error itself', () => {
    expect(refusalOf(new ApiError(409, 'state', 'm', { error: 'state', code: 'version_changed', latest: '0.7.1' }))).toEqual({ code: 'version_changed', latest: '0.7.1' })
    expect(refusalOf(new ApiError(403, 'code_required', 'm', { error: 'code_required' }))).toEqual({ code: 'code_required', latest: '' })
    expect(refusalOf(new ApiError(400, 'code_wrong', 'm'))).toEqual({ code: 'code_wrong', latest: '' })
    expect(refusalOf(new ApiError(423, 'locked', 'm'))).toEqual({ code: 'locked', latest: '' })
    expect(refusalOf(new ApiError(503, 'unavailable', 'm', { code: 'request_failed' }))).toEqual({ code: 'request_failed', latest: '' })
    // anything else is the generic answer's
    for (const e of [new ApiError(429, 'rate_limited', 'm'), new ApiError(0, 'network', 'm'), new ApiError(500, 'internal', 'm'), new ApiError(409, 'state', 'm', { code: 'new_code' }), new Error('x'), null]) {
      expect(refusalOf(e)).toBeNull()
    }
    for (const lang of [fa, en]) {
      for (const k of ['helper_missing', 'already_running', 'check_unavailable', 'no_release', 'not_newer', 'version_changed', 'channel_changed', 'prerelease_unconfirmed', 'dev_build',
        'request_failed', 'confirm_required', 'code_required', 'code_wrong', 'locked']) {
        expect((lang.system.updates.refusal as Record<string, string>)[k], k).toBeTruthy()
      }
      for (const k of ['no_dir', 'no_status', 'not_installed', 'bad_status', 'unsupported']) expect((lang.system.updates.update.helper_problem as Record<string, string>)[k], k).toBeTruthy()
    }
    expect(helperProblem('not_installed')).toBe('not_installed')
    expect(helperProblem('something else')).toBe('bad_status')
    expect(helperProblem('')).toBe('bad_status')
  })

  it('polls quickly, slowly after a while, and at its own pace while core does not answer', () => {
    expect(pollDelay(0, false)).toBe(updatePoll.ms)
    expect(pollDelay(updatePoll.slowAfterMs + 1, false)).toBe(updatePoll.slowMs)
    expect(pollDelay(0, true)).toBe(updatePoll.failMs)
    expect(pollDelay(updatePoll.slowAfterMs + 1, true)).toBe(updatePoll.failMs)
  })
})

describe('copying the command', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    Reflect.deleteProperty(navigator, 'clipboard')
  })
  it('uses the clipboard where the browser has one', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    expect(await copyText('sudo bobres update')).toBe(true)
    expect(writeText).toHaveBeenCalledWith('sudo bobres update')
  })
  it('tries the older way where there is none (a page served over plain http), and says when neither works', async () => {
    // the old button did nothing at all here
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    const exec = vi.fn(() => true)
    Object.defineProperty(document, 'execCommand', { value: exec, configurable: true })
    expect(await copyText('sudo bobres update')).toBe(true)
    expect(exec).toHaveBeenCalledWith('copy')
    expect(document.querySelector('textarea')).toBeNull() // the helper element is gone again
    // a clipboard that refuses (not allowed) falls back too
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn(async () => { throw new Error('denied') }) }, configurable: true })
    expect(await copyText('x')).toBe(true)
    // neither works: false, so the page can say so
    exec.mockReturnValue(false)
    expect(await copyText('x')).toBe(false)
    exec.mockImplementation(() => { throw new Error('no') })
    expect(await copyText('x')).toBe(false)
    expect(typeof page.reload).toBe('function')
  })
})

describe('updates tab', () => {
  const ownerPerms = ['overview.read', 'users.read', 'settings.write', 'servers.write', 'audit.read', 'system.read', 'system.write', 'license.write', 'staff.write', 'gateways.write']
  const adminPerms = ['overview.read', 'users.read', 'settings.write', 'servers.write', 'audit.read', 'system.read']
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const text = (path: string, lang = fa as unknown) => {
    const s = path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], lang)
    if (typeof s !== 'string') throw new Error(`no text "${path}"`)
    return s
  }
  const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replaceAll('{' + k + '}', v), s)
  const iso = (v: string) => '⁨' + v + '⁩'

  /** The fake core: one state it answers, what it was asked, and a way to make it not answer. */
  interface Core {
    state: UpdatesState
    calls: string[]
    down: number
    check?: (body: Record<string, unknown>) => Response | undefined
    channel?: (body: Record<string, unknown>) => Response | undefined
    apply?: (body: Record<string, unknown>) => Response | undefined
    progress?: () => Response | undefined
    get?: () => Response | undefined
  }
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  async function boot(core: Core, o: { admin?: boolean; reauth?: boolean; lang?: 'fa' | 'en' } = {}) {
    unmount()
    vi.resetModules()
    window.history.replaceState(null, '', '/admin/system?tab=updates')
    document.body.innerHTML = '<div id="app"></div>'
    try {
      localStorage.setItem('bobres.lang', o.lang ?? 'fa')
    } catch {
      /* storage blocked */
    }
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        const raw = typeof init?.body === 'string' ? init.body : ''
        const body = raw ? (JSON.parse(raw) as Record<string, unknown>) : {}
        core.calls.push(`${method} ${url}${raw ? ' ' + raw : ''}`)
        if (url === '/api/v1/me') {
          return json(200, {
            user: { id: 'u1', telegram_id: 1, username: 'boss', role: o.admin ? 'admin' : 'owner', language: 'fa' }, permissions: o.admin ? adminPerms : ownerPerms, csrf: 'c',
            session: { method: o.reauth ? 'password' : 'link', expires_at: 2e9 }, password: { enabled: !!o.reauth, reauth: !!o.reauth }, password_available: true, brand: 'Shop',
          })
        }
        if (url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null })
        if (url === '/api/v1/system/updates/progress' && method === 'GET') {
          if (core.down > 0) {
            core.down--
            throw new TypeError('Failed to fetch')
          }
          const own = core.progress?.()
          if (own) return own
          const { version, helper: h, run: r, last } = core.state
          return json(200, { version, helper: h, run: r, last })
        }
        if (url === '/api/v1/system/updates' && method === 'GET') return core.get?.() ?? json(200, core.state)
        if (url === '/api/v1/system/updates/check' && method === 'POST') return core.check?.(body) ?? json(200, core.state)
        if (url === '/api/v1/system/updates/channel' && method === 'PUT') return core.channel?.(body) ?? json(200, core.state)
        if (url === '/api/v1/system/updates/apply' && method === 'POST') return core.apply?.(body) ?? json(503, { error: 'unavailable', message: 'not in this test' })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    // quick polling, set before the app reads it
    const upd = await import('../updates')
    Object.assign(upd.updatePoll, { ms: 5, failMs: 5, slowMs: 5, slowAfterMs: 60_000 })
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { router, upd }
  }
  afterEach(() => {
    unmount()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    Reflect.deleteProperty(navigator, 'clipboard')
    document.body.innerHTML = ''
    try {
      localStorage.removeItem('bobres.lang')
    } catch {
      /* storage blocked */
    }
  })
  const found = async <T extends Element>(selector: string) =>
    vi.waitFor(() => {
      const el = document.querySelector<T>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })
  const gone = (selector: string) => vi.waitFor(() => expect(document.querySelector(selector), selector).toBeNull())
  const norm = (s: string | null | undefined) => (s ?? '').replace(/\s+/g, ' ').trim()
  const q = (selector: string) => norm(document.querySelector(selector)?.textContent)
  const btn = (selector: string) => document.querySelector(selector) as HTMLButtonElement
  const sent = (core: Core, method: string, path: string) =>
    core.calls.filter((c) => c.startsWith(`${method} /api/v1${path} `)).map((c) => JSON.parse(c.slice(`${method} /api/v1${path} `.length)) as Record<string, unknown>)
  const count = (core: Core, call: string) => core.calls.filter((c) => c === call || c.startsWith(call + ' ')).length
  const core = (over: Partial<UpdatesState> = {}): Core => ({ state: state(over), calls: [], down: 0 })

  it('says the install is up to date: the newest release, when it came out, its notes, when it was checked', async () => {
    const c = core({ check: { ...state().check, current: '0.7.0', latest: '0.7.0', newer: false }, version: '0.7.0', apply: { allowed: true, ready: false, reason: 'not_newer', target: '' } })
    await boot(c)
    await found('.update-state')
    expect(q('.update-state')).toBe(text('system.updates.state.current'))
    expect(q('.verdict')).toBe(fill(text('system.updates.verdict.current'), { channel: text('system.updates.channel_name.stable') }))
    expect(q('.current-version')).toBe('0.7.0')
    expect(q('.latest-version')).toBe('0.7.0')
    expect(q('.released')).toBe(dateTime(Date.UTC(2026, 9, 20, 10, 0) / 1000, 'fa'))
    expect(q('.channel-name')).toBe(text('system.updates.channel_name.stable'))
    expect(q('.checked')).toBe(fill(text('system.updates.checked_at'), { time: dateTime(NOW - 3600, 'fa') }))
    // the notes are GitHub's page for that release, opened without handing the page over
    const link = document.querySelector<HTMLAnchorElement>('a.notes-link')!
    expect(link.getAttribute('href')).toBe('https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.7.0')
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toContain('noopener')
    // nothing to update, no failure to show
    expect(document.querySelector('.update-card')).toBeNull()
    expect(document.querySelector('.not-checked')).toBeNull()
    expect(document.querySelector('.prerelease')).toBeNull()
    // the page reads one state, and nothing else of the System page
    expect(c.calls.filter((x) => x.startsWith('GET /api/v1/system'))).toEqual(['GET /api/v1/system/updates'])
  })

  it('never follows a link the answer carries', async () => {
    const c = core()
    c.state.check.notes_url = 'https://evil.example/releases/tag/v0.7.0'
    await boot(c)
    await found('.update-state')
    expect(document.querySelector('a.notes-link')).toBeNull()
    c.state.check.notes_url = 'javascript:alert(1)'
    await boot(c)
    await found('.update-state')
    expect(document.querySelector('a.notes-link')).toBeNull()
    // and every text from the updater or the bot is text
    c.state.check.notes_url = ''
    c.state.helper = helper({ state: 'running', id: ID, step: 'pull', message: '<img src=x onerror=alert(1)>', log: ['<b>bold</b>'] })
    c.state.run = run({ phase: 'running' })
    await boot(c)
    await found('.update-progress')
    expect(document.querySelector('.updater-message img')).toBeNull()
    expect(q('.updater-message')).toBe('<img src=x onerror=alert(1)>')
  })

  it('offers the newer release, with the command for the channel it shows and a copy that tells when it cannot', async () => {
    const c = core()
    await boot(c)
    await found('.update-card')
    expect(q('.update-state')).toBe(text('system.updates.state.newer'))
    expect(document.querySelector('.update-state')?.classList.contains('p-tag-warn')).toBe(true)
    expect(q('.verdict')).toBe(fill(text('system.updates.verdict.newer'), { version: iso('0.7.0') }))
    expect(q('.available-title')).toBe(fill(text('system.updates.available'), { version: iso('0.7.0') }))
    expect(q('.latest-version')).toBe('0.7.0')
    expect(document.querySelector('.update-now')).not.toBeNull()
    expect(q('.command')).toBe('sudo bobres update --channel stable')
    expect(document.querySelector('.server-way')?.hasAttribute('open')).toBe(false)
    // a page served over plain http has no clipboard: the old button did nothing at all
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    Object.defineProperty(document, 'execCommand', { value: () => false, configurable: true })
    btn('button.copy-command').click()
    await vi.waitFor(() => expect(document.body.textContent).toContain(text('system.updates.copy_failed')))
    // with one, it copies
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    btn('button.copy-command').click()
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith('sudo bobres update --channel stable'))
    await vi.waitFor(() => expect(document.body.textContent).toContain(text('common.copied')))
    // looking never changes anything
    expect(c.calls.some((x) => !x.startsWith('GET '))).toBe(false)
  })

  it('shows the beta channel\'s pre-release, and the note on a release newer than the license\'s update date (still offered)', async () => {
    const c = core({ check: { ...state().check, latest: '0.7.0-rc.2', channel: 'beta', prerelease: true, covered: false, notes_url: 'https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.7.0-rc.2' },
      channel: { setting: 'beta', effective: 'beta', source: 'setting' }, apply: { allowed: true, ready: true, reason: '', target: '0.7.0-rc.2' } })
    const { router } = await boot(c)
    await found('.update-card')
    expect(q('.verdict')).toBe(fill(text('system.updates.verdict.newer_pre'), { version: iso('0.7.0-rc.2') }))
    expect(q('.versions-card .prerelease')).toBe(text('system.updates.prerelease'))
    expect(q('.channel-name')).toBe(text('system.updates.channel_name.beta'))
    expect(q('.command')).toBe('sudo bobres update --channel beta')
    // advisory: the update is offered all the same, with a note beside it
    expect(btn('button.update-now')).not.toBeNull()
    expect(q('.license-note')).toContain(text('system.updates.newer_than_license'))
    ;(document.querySelector('a.license-link') as HTMLAnchorElement).click()
    await vi.waitFor(() => expect(router.currentRoute.value.query.tab).toBe('license'))
    // covered, or no license: no note
    const d = core({ check: { ...state().check, covered: true } })
    await boot(d)
    await found('.update-card')
    expect(document.querySelector('.license-note')).toBeNull()
  })

  it('says why the check failed, in the bot\'s words where they help, and offers nothing', async () => {
    const failing = (over: Partial<UpdatesState['check']>, reason: string): Core =>
      core({ check: { current: '0.6.1', latest: '', newer: false, verified: false, checked_at: NOW - 60, error: '', detail: '', channel: 'stable', ...over }, apply: { allowed: true, ready: false, reason, target: '' } })
    const shows = async (c: Core) => {
      await boot(c)
      await found('.update-state')
      expect(q('.update-state')).toBe(text('system.updates.state.unknown'))
      expect(document.querySelector('.update-card')).toBeNull()
      expect(document.querySelector('.update-now')).toBeNull()
    }
    // GitHub did not answer: its own words say how (a timeout, its hourly limit)
    await shows(failing({ error: 'unreachable', detail: 'checksums.txt: Get "https://github.com/x": i/o timeout' }, 'no_release'))
    expect(q('.not-checked .reason-title')).toBe(text('system.updates.could_not_check'))
    expect(q('.not-checked .reason')).toBe(text('system.updates.reason.unreachable'))
    expect(q('.not-checked .detail')).toBe('checksums.txt: Get "https://github.com/x": i/o timeout')
    expect(q('.latest-version')).toBe('—')
    // the signature did not verify: said, and the release is never offered
    await shows(failing({ error: 'bad_signature', latest: '0.7.0', detail: 'the release is not signed (no checksums.txt.sig)' }, 'no_release'))
    expect(q('.not-checked .reason')).toBe(text('system.updates.reason.bad_signature'))
    expect(q('.not-checked .detail')).toBe('the release is not signed (no checksums.txt.sig)')
    expect(q('.latest-version')).toBe('0.7.0')
    // the bot does not answer
    await shows(failing({ error: 'bot unreachable', checked_at: 0 }, 'check_unavailable'))
    expect(q('.not-checked .reason')).toBe(text('system.updates.reason.bot_unreachable'))
    expect(q('.checked')).toBe(text('system.updates.never_checked'))
    // not checked yet, nothing published, an answer the page has no text for
    await shows(failing({ error: 'not_checked', checked_at: 0 }, 'no_release'))
    expect(q('.not-checked .reason')).toBe(text('system.updates.reason.not_checked'))
    await shows(failing({ error: 'no_release', detail: 'checksums.txt: not found' }, 'no_release'))
    expect(q('.not-checked .reason')).toBe(text('system.updates.reason.no_release'))
    expect(document.querySelector('.not-checked .detail')).toBeNull()
    await shows(failing({ error: 'rate_limited', detail: 'try later' }, 'no_release'))
    expect(q('.not-checked .reason')).toBe(text('system.updates.reason.other'))
    expect(q('.not-checked .detail')).toBe('rate_limited: try later')
    // a release read before the last check failed is still offered, with the failure beside it
    const c = failing({ error: 'unreachable', latest: '0.7.0', newer: true, verified: true, detail: 'timeout' }, '')
    c.state.apply = { allowed: true, ready: true, reason: '', target: '0.7.0' }
    await boot(c)
    await found('.update-card')
    expect(q('.not-checked .reason-title')).toBe(text('system.updates.last_check_failed'))
    expect(q('.not-checked .detail')).toBe('timeout')
  })

  it('checks again for the owner: the bot looks now, and what it found shows with when', async () => {
    const c = core()
    const fresh = state({ check: { ...state().check, latest: '0.7.1', checked_at: NOW }, apply: { allowed: true, ready: true, reason: '', target: '0.7.1' } })
    c.check = () => json(200, fresh)
    await boot(c)
    await found('.update-card')
    expect(q('button.refresh')).toBe(text('system.refresh'))
    btn('button.refresh').click()
    await vi.waitFor(() => expect(count(c, 'POST /api/v1/system/updates/check')).toBe(1))
    await vi.waitFor(() => expect(q('.latest-version')).toBe('0.7.1'))
    expect(q('.checked')).toBe(fill(text('system.updates.checked_at'), { time: dateTime(NOW, 'fa') }))
    // not the cached read the old button made
    expect(count(c, 'GET /api/v1/system/updates')).toBe(1)
    // a look that failed says why
    c.check = () => json(200, state({ check: { current: '0.6.1', latest: '', newer: false, verified: false, checked_at: NOW + 5, error: 'unreachable', detail: 'dial tcp: timeout', channel: 'stable' }, apply: { allowed: true, ready: false, reason: 'no_release', target: '' } }))
    btn('button.refresh').click()
    await vi.waitFor(() => expect(q('.not-checked .reason')).toBe(text('system.updates.reason.unreachable')))
    expect(q('.not-checked .detail')).toBe('dial tcp: timeout')
    expect(q('.checked')).toBe(fill(text('system.updates.checked_at'), { time: dateTime(NOW + 5, 'fa') }))
    // the click itself failing (a limit, the network) says so
    c.check = () => json(429, { error: 'rate_limited', message: 'at most 10 checks a minute' })
    btn('button.refresh').click()
    await vi.waitFor(() => expect(q('.check-error')).toContain(text('system.updates.check_error')))
    expect(q('.check-error')).toContain(text('error.rate_limited'))
  })

  it('shows the button as busy while the bot looks', async () => {
    const c = core()
    let release: (r: Response) => void = () => {}
    c.check = () => undefined
    const wait = new Promise<Response>((res) => (release = res))
    await boot(c)
    await found('.update-card')
    const orig = globalThis.fetch
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => (url === '/api/v1/system/updates/check' ? wait : orig(url, init))))
    btn('button.refresh').click()
    await vi.waitFor(() => expect(q('button.refresh')).toBe(text('system.updates.checking')))
    expect(btn('button.refresh').disabled).toBe(true)
    release(json(200, state()))
    await vi.waitFor(() => expect(q('button.refresh')).toBe(text('system.refresh')))
  })

  it('lets the owner choose the channel: beta with its warning, saved as a setting, the bot looks again', async () => {
    const c = core()
    c.channel = (body) =>
      json(200, state({ check: { ...state().check, latest: '0.7.0-rc.2', channel: String(body.channel) || 'stable', prerelease: true }, channel: { setting: String(body.channel), effective: String(body.channel) || 'stable', source: body.channel ? 'setting' : 'server' },
        apply: { allowed: true, ready: true, reason: '', target: '0.7.0-rc.2' } }))
    await boot(c)
    await found('.channel-card')
    expect(q('.channel-card .source')).toBe(fill(text('system.updates.channel_card.source_server'), { name: iso('BOBRES_UPDATE_CHANNEL') }))
    expect(btn('button.save').disabled).toBe(true)
    expect(document.querySelector('.beta-warning')).toBeNull()
    const toggles = [...document.querySelectorAll<HTMLElement>('.channel-card .p-togglebutton')]
    expect(toggles.map((t) => norm(t.textContent))).toEqual([text('system.updates.channel_card.stable'), text('system.updates.channel_card.beta')])
    toggles[1].click()
    await found('.beta-warning')
    expect(q('.beta-warning')).toBe(text('system.updates.channel_card.beta_warning'))
    expect(q('.channel-card .hint')).toBe(text('system.updates.channel_card.beta_hint'))
    expect(btn('button.save').disabled).toBe(false)
    btn('button.save').click()
    await vi.waitFor(() => expect(sent(c, 'PUT', '/system/updates/channel')).toEqual([{ channel: 'beta' }]))
    // the new channel's release shows, and where the choice comes from
    await vi.waitFor(() => expect(q('.versions-card .prerelease')).toBe(text('system.updates.prerelease')))
    expect(q('.channel-name')).toBe(text('system.updates.channel_name.beta'))
    expect(q('.channel-card .source')).toBe(text('system.updates.channel_card.source_setting'))
    await vi.waitFor(() => expect(document.body.textContent).toContain(text('system.updates.channel_card.saved')))
    // back to what the server's .env says
    c.channel = () => json(200, state())
    btn('button.reset').click()
    await vi.waitFor(() => expect(sent(c, 'PUT', '/system/updates/channel')).toEqual([{ channel: 'beta' }, { channel: '' }]))
    await vi.waitFor(() => expect(document.querySelector('button.reset')).toBeNull())
  })

  it('says when the channel was not saved', async () => {
    const c = core()
    c.channel = () => json(400, { error: 'invalid', field: 'updates.channel', message: 'updates.channel: one of stable, beta' })
    await boot(c)
    await found('.channel-card')
    document.querySelectorAll<HTMLElement>('.channel-card .p-togglebutton')[1].click()
    await found('.beta-warning')
    btn('button.save').click()
    await vi.waitFor(() => expect(q('.channel-error')).toContain(text('system.updates.channel_card.save_error')))
  })

  it('asks before updating, and tells what happens: the versions, the minutes away, the backup, every owner', async () => {
    const c = core()
    await boot(c)
    await found('.update-now')
    btn('button.update-now').click()
    const dialog = await found('.update-confirm')
    expect(norm(dialog.querySelector('.p-dialog-title')?.textContent)).toBe(fill(text('system.updates.confirm.title'), { version: iso('0.7.0') }))
    expect(norm(dialog.querySelector('.versions')?.textContent)).toContain('0.6.1')
    expect(norm(dialog.querySelector('.versions')?.textContent)).toContain('0.7.0')
    expect(norm(dialog.querySelector('.downtime')?.textContent)).toBe(text('system.updates.confirm.downtime'))
    expect(norm(dialog.querySelector('.safe')?.textContent)).toBe(text('system.updates.confirm.safe'))
    expect(norm(dialog.querySelector('.owners')?.textContent)).toBe(text('system.updates.confirm.owners'))
    // a stable release needs no extra yes, and an owner without a password login no code
    expect(dialog.querySelector('.prerelease')).toBeNull()
    expect(dialog.querySelector('input.code')).toBeNull()
    expect(dialog.querySelector<HTMLButtonElement>('button.submit')!.disabled).toBe(false)
    // cancelling asks nothing
    dialog.querySelector<HTMLButtonElement>('button.cancel')!.click()
    await gone('.update-confirm')
    expect(count(c, 'POST /api/v1/system/updates/apply')).toBe(0)
  })

  it('updates: the request, the steps, core restarting in the middle, then the end and a look at the new release', async () => {
    const c = core()
    const queued = run({ phase: 'queued' })
    c.apply = () => {
      c.state = { ...c.state, run: queued, apply: { allowed: true, ready: false, reason: 'already_running', target: '0.7.0' } }
      return json(202, { version: '0.6.1', helper: c.state.helper, run: queued, last: null })
    }
    const { upd } = await boot(c)
    const reload = vi.spyOn(upd.page, 'reload').mockImplementation(() => {})
    await found('.update-now')
    btn('button.update-now').click()
    await found('.update-confirm')
    document.querySelector<HTMLButtonElement>('.update-confirm button.submit')!.click()
    // exactly what the owner saw and confirmed, and nothing else
    await vi.waitFor(() => expect(sent(c, 'POST', '/system/updates/apply')).toEqual([{ version: '0.7.0', channel: 'stable', confirm: true, accept_prerelease: false }]))
    await gone('.update-confirm')
    await found('.update-progress')
    expect(q('.progress-title')).toBe(text('system.updates.progress.title.queued'))
    expect(q('.update-progress .hint')).toBe(text('system.updates.progress.queued'))
    expect(document.querySelectorAll('.update-progress .step.pending')).toHaveLength(5)
    expect(document.querySelector('.update-now')).toBeNull()
    expect(q('.running-note')).toBe(text('system.updates.update.running'))
    expect(q('.update-progress .asked')).toContain('boss')

    // the updater takes it up: the steps, its sentence, the owner's log
    c.state = { ...c.state, run: run({ phase: 'running' }), helper: helper({ state: 'running', id: ID, from: '0.6.1', to: '0.7.0', channel: 'stable', step: 'backup', message: 'Backing up the database.', started_at: NOW, log: ['verified the release signature', 'backup written: 12.4 MB'] }) }
    await vi.waitFor(() => expect(q('.progress-title')).toBe(fill(text('system.updates.progress.title.running'), { to: iso('0.7.0') })))
    expect([...document.querySelectorAll('.update-progress .step')].map((s) => s.className.replace('step ', ''))).toEqual(['done', 'active', 'pending', 'pending', 'pending'])
    expect(q('.updater-message')).toBe('Backing up the database.')
    btn('.update-progress button.log-toggle').click()
    await found('.update-progress pre.log')
    expect(document.querySelector('pre.log')?.textContent).toBe('verified the release signature\nbackup written: 12.4 MB')
    expect(q('.update-progress .hint')).toBe(text('system.updates.progress.running'))

    // core restarts in the middle: the page says so and keeps trying, and shows the last step it knew
    c.down = 1_000_000
    await vi.waitFor(() => expect(document.querySelector('.reconnecting')).not.toBeNull())
    expect(q('.reconnecting')).toBe(text('system.updates.progress.reconnecting'))
    expect(document.querySelector('.update-progress .step.active')).not.toBeNull()
    c.down = 0 // core is back
    await gone('.reconnecting')

    // it ends: done, the new version, the dashboard to reload, and the bot looks again (it just restarted)
    c.state = { ...c.state, version: '0.7.0', run: null, helper: helper({ state: 'done', id: ID, from: '0.6.1', to: '0.7.0', step: 'done', message: 'Updated.', finished_at: NOW + 60 }),
      last: run({ phase: undefined, outcome: 'done', outcome_at: NOW + 60, message: 'Updated.' }), check: { ...state().check, current: '0.7.0', latest: '0.7.0', newer: false }, apply: { allowed: true, ready: false, reason: 'not_newer', target: '' } }
    c.check = () => json(200, c.state)
    await vi.waitFor(() => expect(q('.progress-title')).toBe(fill(text('system.updates.progress.title.done'), { to: iso('0.7.0') })))
    expect(q('.update-progress .hint')).toBe(fill(text('system.updates.progress.done'), { version: iso('0.7.0') }))
    expect(document.querySelectorAll('.update-progress .step.done')).toHaveLength(5)
    await vi.waitFor(() => expect(count(c, 'POST /api/v1/system/updates/check')).toBe(1))
    await vi.waitFor(() => expect(q('.current-version')).toBe('0.7.0'))
    // the page was loaded from the old version: a new dashboard is waiting
    btn('.update-progress button.reload').click()
    expect(reload).toHaveBeenCalledTimes(1)
    // it stops reading once the update is over
    const n = c.calls.length
    await new Promise((res) => setTimeout(res, 60))
    expect(c.calls.slice(n).filter((x) => x.includes('/progress'))).toEqual([])
    // closing the panel leaves the line about the last update
    btn('.update-progress button.dismiss').click()
    await gone('.update-progress')
    expect(q('.last-update .last-outcome')).toBe(text('system.updates.last.outcome.done'))
  })

  for (const [outcome, hint, severity] of [
    ['rolled_back', 'rolled_back', 'p-message-warn'],
    ['failed', 'failed', 'p-message-error'],
    ['not_started', 'not_started', 'p-message-warn'],
  ] as const) {
    it(`says plainly when the update ${outcome === 'not_started' ? 'did not start' : outcome === 'failed' ? 'failed' : 'was rolled back'}`, async () => {
      const c = core({ run: run({ phase: 'running' }), helper: helper({ state: 'running', id: ID, step: 'health', message: 'Waiting.', started_at: NOW }) })
      await boot(c)
      await found('.update-progress')
      // an update that was open when the page loaded is followed from there
      expect(q('.progress-title')).toBe(fill(text('system.updates.progress.title.running'), { to: iso('0.7.0') }))
      c.state = { ...c.state, run: null, helper: helper({ state: outcome === 'not_started' ? 'idle' : outcome, id: outcome === 'not_started' ? '' : ID, step: outcome === 'rolled_back' ? 'rollback' : 'health' }),
        last: run({ phase: undefined, outcome, outcome_at: NOW + 30, message: `the ${outcome} sentence` }) }
      await vi.waitFor(() => expect(q('.progress-title')).toBe(text('system.updates.progress.title.' + outcome)))
      expect(q('.update-progress .hint')).toBe(fill(text('system.updates.progress.' + hint), { from: iso('0.6.1') }))
      expect(document.querySelector('.update-progress .hint-box')?.classList.contains(severity)).toBe(true)
      expect(q('.updater-message')).toBe(`the ${outcome} sentence`)
      // a version that did not change offers no reload; closing is always possible
      expect(document.querySelector('.update-progress button.reload')).toBeNull()
      expect(document.querySelector('.update-progress button.dismiss')).not.toBeNull()
      if (outcome === 'rolled_back') expect([...document.querySelectorAll('.update-progress .step')].map((s) => norm(s.textContent))).toEqual([text('system.updates.progress.step.rollback')])
      if (outcome === 'failed') expect(document.querySelector('.update-progress .step.failed')).not.toBeNull()
    })
  }

  it('follows an update that was started on the server, and offers none of its own meanwhile', async () => {
    const c = core({ helper: helper({ state: 'running', id: '', from: '0.6.1', to: '0.7.0', channel: 'stable', step: 'pull', message: 'Pulling.', started_at: NOW }),
      apply: { allowed: true, ready: false, reason: 'already_running', target: '0.7.0' } })
    await boot(c)
    await found('.update-progress')
    expect(q('.progress-title')).toBe(text('system.updates.progress.title.elsewhere'))
    expect(q('.update-progress .hint')).toBe(text('system.updates.progress.elsewhere'))
    expect(document.querySelector('.update-now')).toBeNull()
    expect(q('.running-note')).toBe(text('system.updates.update.running'))
    // it ends on the server: the page stops
    c.state = { ...c.state, helper: helper({ state: 'done', id: '' }) }
    await gone('.update-progress')
  })

  it('explains that updating from the dashboard is not available yet, and how to make it so', async () => {
    for (const [problem, key] of [['not_installed', 'not_installed'], ['no_dir', 'no_dir'], ['no_status', 'no_status'], ['bad_status', 'bad_status'], ['unsupported', 'unsupported']] as const) {
      const c = core({ helper: helper({ available: false, problem, version: '' }), apply: { allowed: true, ready: false, reason: 'helper_missing', target: '0.7.0' } })
      await boot(c)
      await found('.not-available')
      expect(q('.not-available .title')).toBe(text('system.updates.update.not_available_title'))
      expect(q('.not-available .problem')).toBe(text('system.updates.update.helper_problem.' + key))
      expect(q('.not-available .how')).toBe(text('system.updates.update.not_available'))
      expect(document.querySelector('.update-now')).toBeNull()
      // the command is open, for the channel the page shows
      expect(document.querySelector('.server-way')?.hasAttribute('open')).toBe(true)
      expect(q('.command')).toBe('sudo bobres update --channel stable')
    }
  })

  it('gives an admin the state and no way to change it', async () => {
    const c = core({ apply: { allowed: false, ready: false, reason: '', target: '0.7.0' }, helper: helper({ log: undefined }) })
    await boot(c, { admin: true })
    await found('.update-card')
    expect(q('.owner-only')).toBe(text('system.updates.update.owner_only'))
    expect(document.querySelector('.update-now')).toBeNull()
    expect(q('.command')).toBe('sudo bobres update --channel stable')
    // the button only reads again: "Check again" is the owner's
    expect(q('button.refresh')).toBe(text('system.updates.reload'))
    btn('button.refresh').click()
    await vi.waitFor(() => expect(count(c, 'GET /api/v1/system/updates')).toBe(2))
    expect(count(c, 'POST /api/v1/system/updates/check')).toBe(0)
    // the channel is shown, not editable
    expect(document.querySelector('button.save')).toBeNull()
    expect(q('.channel-card .owner-only')).toBe(text('system.updates.channel_card.owner_only'))
    expect([...document.querySelectorAll<HTMLButtonElement>('.channel-card .p-togglebutton')].every((b) => b.getAttribute('aria-disabled') === 'true' || b.hasAttribute('data-p-disabled') || b.classList.contains('p-disabled'))).toBe(true)
    expect(c.calls.some((x) => !x.startsWith('GET '))).toBe(false)
  })

  it('asks a pre-release for its own yes, and an owner with an authenticator app for a current code', async () => {
    const c = core({ check: { ...state().check, latest: '0.7.0-rc.2', channel: 'beta', prerelease: true }, apply: { allowed: true, ready: true, reason: '', target: '0.7.0-rc.2' } })
    let answer: Response | undefined
    c.apply = () => answer
    await boot(c, { reauth: true })
    await found('.update-now')
    btn('button.update-now').click()
    const dialog = await found('.update-confirm')
    const submit = () => dialog.querySelector<HTMLButtonElement>('button.submit')!
    expect(norm(dialog.querySelector('.prerelease .title')?.textContent)).toBe(text('system.updates.confirm.prerelease_title'))
    const code = () => dialog.querySelector<HTMLInputElement>('input.code')!
    expect(code()).not.toBeNull()
    // not without the yes, not without six digits
    expect(submit().disabled).toBe(true)
    dialog.querySelector<HTMLInputElement>('.accept input')!.click()
    await vi.waitFor(() => expect(submit().disabled).toBe(true)) // still no code
    for (const bad of ['', '12345', '12345a', '1234567']) {
      code().value = bad
      code().dispatchEvent(new Event('input'))
      await new Promise((res) => setTimeout(res, 10))
      expect(submit().disabled, bad).toBe(true)
    }
    code().value = '000000'
    code().dispatchEvent(new Event('input'))
    await vi.waitFor(() => expect(submit().disabled).toBe(false))
    // a wrong code keeps the dialog open and says so
    answer = json(400, { error: 'code_wrong', message: 'that code is not right' })
    submit().click()
    await vi.waitFor(() => expect(norm(dialog.querySelector('.error')?.textContent)).toBe(text('system.updates.refusal.code_wrong')))
    expect(sent(c, 'POST', '/system/updates/apply')[0]).toEqual({ version: '0.7.0-rc.2', channel: 'beta', confirm: true, accept_prerelease: true, code: '000000' })
    expect(document.querySelector('.update-confirm')).not.toBeNull()
    // locked
    answer = json(423, { error: 'locked', message: 'too many wrong codes' })
    submit().click()
    await vi.waitFor(() => expect(norm(dialog.querySelector('.error')?.textContent)).toBe(text('system.updates.refusal.locked')))
  })

  it('translates a refusal, and reads the page again behind it', async () => {
    const c = core()
    const refuse = (status: number, body: Record<string, unknown>) => (c.apply = () => json(status, body))
    await boot(c)
    await found('.update-now')
    const open = async () => {
      btn('button.update-now').click()
      return found<HTMLElement>('.update-confirm')
    }
    const dialog = await open()
    const submit = () => dialog.querySelector<HTMLButtonElement>('button.submit')!
    for (const [status, body, key, params] of [
      [409, { error: 'state', code: 'version_changed', latest: '0.7.1', message: 'x' }, 'version_changed', { latest: iso('0.7.1') }],
      [409, { error: 'state', code: 'already_running', message: 'x' }, 'already_running', {}],
      [409, { error: 'state', code: 'helper_missing', message: 'x' }, 'helper_missing', {}],
      [409, { error: 'state', code: 'channel_changed', message: 'x' }, 'channel_changed', {}],
      [503, { error: 'unavailable', code: 'request_failed', message: 'x' }, 'request_failed', {}],
      [503, { error: 'unavailable', code: 'check_unavailable', message: 'x' }, 'check_unavailable', {}],
    ] as const) {
      refuse(status, body)
      const reads = count(c, 'GET /api/v1/system/updates')
      submit().click()
      await vi.waitFor(() => expect(norm(dialog.querySelector('.error')?.textContent)).toBe(fill(text('system.updates.refusal.' + key), params)))
      if (['version_changed', 'already_running', 'helper_missing', 'channel_changed'].includes(key)) await vi.waitFor(() => expect(count(c, 'GET /api/v1/system/updates')).toBeGreaterThan(reads))
    }
    // anything the page has no text for is the generic answer, with the server's words under it
    refuse(500, { error: 'internal', message: 'something went wrong; try again' })
    submit().click()
    await vi.waitFor(() => expect(norm(dialog.querySelector('.error')?.textContent)).toContain(text('error.generic')))
    expect(document.querySelector('.update-progress')).toBeNull()
  })

  it('stops following when the session ends (the app takes the page to the login page, the update goes on)', async () => {
    const c = core({ run: run({ phase: 'running' }), helper: helper({ state: 'running', id: ID, step: 'pull', started_at: NOW }) })
    await boot(c)
    await found('.update-progress')
    c.progress = () => json(401, { error: 'unauthorized', message: 'the session ended; log in again' })
    await vi.waitFor(() => expect(c.calls.filter((x) => x.includes('/progress')).length).toBeGreaterThan(1))
    await new Promise((res) => setTimeout(res, 40))
    const n = c.calls.filter((x) => x.includes('/progress')).length
    await new Promise((res) => setTimeout(res, 60))
    expect(c.calls.filter((x) => x.includes('/progress')).length).toBe(n)
  })

  it('is in English too: the same states, the same texts', async () => {
    const c = core()
    await boot(c, { lang: 'en' })
    await found('.update-card')
    expect(document.documentElement.lang).toBe('en')
    expect(q('.verdict')).toBe(fill(text('system.updates.verdict.newer', en), { version: iso('0.7.0') }))
    expect(q('.released')).toBe(dateTime(Date.UTC(2026, 9, 20, 10, 0) / 1000, 'en'))
    expect(q('button.update-now')).toBe(text('system.updates.update.now', en))
    expect(q('button.refresh')).toBe(text('system.refresh', en))
    btn('button.update-now').click()
    const dialog = await found('.update-confirm')
    expect(norm(dialog.querySelector('.downtime')?.textContent)).toBe(text('system.updates.confirm.downtime', en))
    // the arrow points the way the line reads
    expect(dialog.querySelector('.versions .arrow')?.className).toContain('pi-arrow-right')
  })

  it('reads right to left in Persian: the arrow points left, and nothing mirrors the page', async () => {
    const c = core()
    await boot(c)
    await found('.update-card')
    expect(document.documentElement.dir).toBe('rtl')
    btn('button.update-now').click()
    const dialog = await found('.update-confirm')
    expect(dialog.querySelector('.versions .arrow')?.className).toContain('pi-arrow-left')
    // the scoped styles of the tab must never turn into a rule on the page itself (a :global() selector once did)
    const css = [...document.querySelectorAll('style')].map((s) => s.textContent ?? '').join('\n')
    expect(css).not.toMatch(/\[dir=["']?rtl["']?\]\s*\{[^}]*scaleX/)
  })
})
