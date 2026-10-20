import { afterEach, describe, expect, it, vi } from 'vitest'
import fa from '../locales/fa.json'
import { isolate } from '../format'
import type { RestoreState } from '../types'

// The Backups tab with the restore and the download: what the owner sees and what the page
// sends. The server is a fetch that answers by address; the page is the real app.

const NOW = Math.floor(Date.now() / 1000)
const ID = '0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f60'
const GOOD = 'bobres-20261011T033000Z.bkp'
const FAILED = 'bobres-20261012T033000Z.bkp'
const DOMAIN = 'panel.example.com'
const GIB = 1024 * 1024 * 1024
const TICKET = '/api/v1/system/backups/download/' + 'A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v'

const adminPerms = ['overview.read', 'users.read', 'plans.write', 'settings.write', 'servers.write', 'audit.read', 'system.read']
const ownerPerms = [...adminPerms, 'gateways.write', 'staff.write', 'system.write', 'license.write']

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const lookup = (path: string): unknown => path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa)
const text = (path: string) => {
  const s = lookup(path)
  if (typeof s !== 'string') throw new Error(`fa.json has no text "${path}"`)
  return s
}
const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replaceAll('{' + k + '}', v), s)

const restoreState = (over: Partial<RestoreState> = {}): RestoreState => ({
  version: '0.8.0',
  helper: { available: true, problem: '', version: '0.8.0', state: 'idle', id: '', from: '', to: '', channel: '', step: '', message: '', started_at: null, finished_at: null, log: [], action: '', restore: true },
  run: null, last: null, other: null, can: { allowed: true, ready: true, reason: '' }, domain: DOMAIN, upload: null, limits: { max_upload: 2 * GIB, staged_seconds: 1800 }, ...over,
})
const backups = {
  installed: true, running: false, last_error: '', next_run: NOW + 3600, disk_free: 5 * GIB, dir: '/backups',
  backups: [
    { file: FAILED, size: 0, sha256: '', created_at: NOW - 60, sent_at: null, error: 'pg_dump failed' },
    { file: GOOD, size: 4096, sha256: 'ab'.repeat(32), created_at: NOW - 86400, sent_at: NOW - 86300 },
  ],
}

type Answer = (url: string, init: RequestInit | undefined, body: Record<string, unknown>) => Response | undefined

describe('backups page: download and restore', () => {
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  async function boot(o: { perms?: string[]; role?: string; reauth?: boolean; restore?: RestoreState; answer?: Answer; fast?: boolean } = {}) {
    unmount()
    vi.resetModules()
    window.history.replaceState(null, '', '/admin/system?tab=backups')
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const raw = typeof init?.body === 'string' ? init.body : ''
        calls.push(`${init?.method ?? 'GET'} ${url} ${raw}`.trim())
        const own = o.answer?.(url, init, raw ? (JSON.parse(raw) as Record<string, unknown>) : {})
        if (own) return own
        const get = (init?.method ?? 'GET') === 'GET'
        if (url === '/api/v1/me') {
          return json(200, {
            user: { id: 'u1', telegram_id: 1, username: 'boss', role: o.role ?? 'owner', language: 'fa' }, permissions: o.perms ?? ownerPerms, csrf: 'c',
            session: { method: 'link', expires_at: 2e9 }, password: o.reauth ? { enabled: true, reauth: true } : { enabled: false }, password_available: true, brand: 'Shop',
          })
        }
        if (get && url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null })
        if (get && url === '/api/v1/system') return json(200, { version: 'v0.8.0', services: [], checked_at: NOW })
        if (get && url === '/api/v1/system/backups') return json(200, backups)
        if (get && url === '/api/v1/system/restore') return json(200, o.restore ?? restoreState())
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    if (o.fast) {
      // the page reads again every few milliseconds, not every few seconds
      const { updatePoll } = await import('../updates')
      updatePoll.ms = 15
      updatePoll.failMs = 15
    }
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    const restore = await import('../restore')
    return { calls, page: restore.page }
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
  const btn = (selector: string) => document.querySelector(selector) as HTMLButtonElement
  const input = (selector: string) => document.querySelector(selector) as HTMLInputElement
  const type = (selector: string, value: string) => {
    const el = input(selector)
    el.value = value
    el.dispatchEvent(new Event('input'))
  }
  const posts = (calls: string[], path: string) => calls.filter((c) => c.startsWith(`POST ${path} `)).map((c) => JSON.parse(c.slice(`POST ${path} `.length)) as Record<string, unknown>)

  it('offers a download and a restore for each backup that is a file, to the owner', async () => {
    await boot()
    await found('.backup-list')
    await vi.waitFor(() => expect(btn('.backup .restore')?.disabled).toBe(false))
    const rows = [...document.querySelectorAll('.backup')] as HTMLElement[]
    expect(rows).toHaveLength(2)
    // newest first: the failed run has no file to get
    expect(rows[0].querySelector('.download')).toBeNull()
    expect(rows[0].querySelector('.restore')).toBeNull()
    expect(rows[1].querySelector('.download')?.textContent?.trim()).toBe(text('system.backups.dl.button'))
    expect(rows[1].querySelector('.restore')?.textContent?.trim()).toBe(text('system.backups.rs.button'))
    // the part for a file the owner brings
    expect(document.querySelector('.restore-upload-card .p-card-title')?.textContent?.trim()).toBe(text('system.backups.rs.panel_title'))
    expect(btn('.restore-upload-card .choose').disabled).toBe(false)
    expect(document.querySelector('.restore-note .title')?.textContent?.trim()).toBe(text('system.backups.restore_title'))
  })

  it('shows an admin the list and nothing to press', async () => {
    const { calls } = await boot({ role: 'admin', perms: adminPerms, restore: restoreState({ can: { allowed: false, ready: false, reason: '' }, domain: undefined }) })
    await found('.backup-list')
    await found('.restore-note')
    expect(document.querySelector('.backup .download')).toBeNull()
    expect(document.querySelector('.backup .restore')).toBeNull()
    expect(document.querySelector('.restore-upload-card')).toBeNull()
    expect(calls.some((c) => !c.startsWith('GET '))).toBe(false)
  })

  it('asks for the domain typed and an acknowledgement before it restores a backup of the server', async () => {
    const { calls } = await boot({
      answer: (url, init) => {
        if (url === '/api/v1/system/restore' && init?.method === 'POST') {
          return json(202, restoreState({ run: { id: ID, by: 'boss', from: '0.8.0', to: '0.8.0', channel: '', requested_at: NOW, outcome: '', outcome_at: null, message: '', phase: 'queued', action: 'restore', subject: GOOD, source: 'backup' } }))
        }
        return undefined
      },
    })
    await vi.waitFor(() => expect(btn('.backup .restore')?.disabled).toBe(false))
    btn('.backup .restore').click()
    const dialog = await found('.restore-confirm')
    // what is lost, plainly
    expect(dialog.querySelector('.lost .title')?.textContent?.trim()).toBe(text('system.backups.rs.lost_title'))
    expect(dialog.querySelector('.lost .line')?.textContent?.trim()).toBe(text('system.backups.rs.lost'))
    expect(dialog.querySelector('.downtime')?.textContent).toBe(text('system.backups.rs.downtime'))
    expect(dialog.querySelector('.safe')?.textContent).toBe(text('system.backups.rs.safe'))
    expect(dialog.querySelector('.name')?.textContent).toBe(GOOD)
    expect(dialog.querySelector('.domain')?.textContent).toBe(DOMAIN)
    expect(dialog.querySelector('.code')).toBeNull() // no authenticator app on this login
    const submit = btn('.restore-confirm .submit')
    expect(submit.disabled).toBe(true)
    input('.restore-confirm .ack input').click()
    type('.restore-confirm .typed', 'panel.example.org')
    await vi.waitFor(() => expect(submit.disabled).toBe(true)) // acknowledged, but the wrong domain
    type('.restore-confirm .typed', ' Panel.Example.com ')
    await vi.waitFor(() => expect(submit.disabled).toBe(false))
    submit.click()
    await vi.waitFor(() => expect(posts(calls, '/api/v1/system/restore')).toHaveLength(1))
    // only what the owner confirmed: the backup's name, the domain, the acknowledgement (core takes the size and hash from the backup service)
    expect(posts(calls, '/api/v1/system/restore')[0]).toEqual({ confirm: 'Panel.Example.com', acknowledge: true, backup: GOOD })
    // the progress takes over, and the dialog closes
    const progress = await found('.restore-progress')
    expect(progress.querySelector('.progress-title')?.textContent).toBe(fill(text('system.backups.rs.progress.title.queued'), { name: isolate(GOOD) }))
    expect(progress.querySelector('.hint')?.textContent).toBe(text('system.backups.rs.progress.queued'))
    await vi.waitFor(() => expect(document.querySelector('.restore-confirm')).toBeNull())
    // nothing more can be asked while it runs
    expect(btn('.backup .restore').disabled).toBe(true)
    expect(btn('.restore-upload-card .choose').disabled).toBe(true)
  })

  it('says why core refused, in the owner\'s language, and keeps the dialog', async () => {
    await boot({
      answer: (url, init) =>
        url === '/api/v1/system/restore' && init?.method === 'POST'
          ? json(404, { error: 'not_found', code: 'backup_unknown', message: 'the backup service has no such backup' })
          : undefined,
    })
    await vi.waitFor(() => expect(btn('.backup .restore')?.disabled).toBe(false))
    btn('.backup .restore').click()
    await found('.restore-confirm')
    input('.restore-confirm .ack input').click()
    type('.restore-confirm .typed', DOMAIN)
    await vi.waitFor(() => expect(btn('.restore-confirm .submit').disabled).toBe(false))
    btn('.restore-confirm .submit').click()
    const err = await found('.restore-confirm .error')
    expect(err.textContent).toContain(text('system.backups.rs.refusal.backup_unknown'))
    expect(document.querySelector('.restore-progress')).toBeNull()
  })

  it('asks for the authenticator code when the owner has one, and sends it', async () => {
    const { calls } = await boot({
      reauth: true,
      answer: (url, init) =>
        url === '/api/v1/system/restore' && init?.method === 'POST' ? json(403, { error: 'code_wrong', message: 'wrong' }) : undefined,
    })
    await vi.waitFor(() => expect(btn('.backup .restore')?.disabled).toBe(false))
    btn('.backup .restore').click()
    await found('.restore-confirm .code')
    input('.restore-confirm .ack input').click()
    type('.restore-confirm .typed', DOMAIN)
    const submit = btn('.restore-confirm .submit')
    await vi.waitFor(() => expect(submit.disabled).toBe(true)) // the code is missing
    type('.restore-confirm .code', '12345')
    expect(submit.disabled).toBe(true)
    type('.restore-confirm .code', '123456')
    await vi.waitFor(() => expect(submit.disabled).toBe(false))
    submit.click()
    await vi.waitFor(() => expect(posts(calls, '/api/v1/system/restore')).toHaveLength(1))
    expect(posts(calls, '/api/v1/system/restore')[0]).toMatchObject({ code: '123456', backup: GOOD })
    const err = await found('.restore-confirm .error')
    expect(err.textContent).toContain(text('system.backups.rs.refusal.code_wrong'))
  })

  it('says what a download is before it starts one, and has the browser take the address', async () => {
    const { calls, page } = await boot({
      answer: (url, init) => (url === '/api/v1/system/backups/download-ticket' && init?.method === 'POST' ? json(200, { url: TICKET, expires_in: 60 }) : undefined),
    })
    const dl = vi.spyOn(page, 'download').mockImplementation(() => {})
    await found('.backup .download')
    btn('.backup .download').click()
    const dialog = await found('.download-confirm')
    expect(dialog.querySelector('.encrypted')?.textContent).toBe(text('system.backups.dl.encrypted'))
    expect(dialog.querySelector('.key')?.textContent).toBe(text('system.backups.dl.key'))
    expect(dialog.querySelector('.audited')?.textContent).toBe(text('system.backups.dl.audited'))
    expect(dialog.querySelector('.name')?.textContent).toContain(GOOD)
    expect(dialog.querySelector('.code')).toBeNull()
    btn('.download-confirm .submit').click()
    await vi.waitFor(() => expect(dl).toHaveBeenCalledWith(TICKET))
    expect(posts(calls, '/api/v1/system/backups/download-ticket')).toEqual([{ file: GOOD }])
    await vi.waitFor(() => expect(document.querySelector('.download-confirm')).toBeNull())
  })

  it('asks for the code for a download too, and does not download without it', async () => {
    const { calls, page } = await boot({ reauth: true })
    const dl = vi.spyOn(page, 'download').mockImplementation(() => {})
    await found('.backup .download')
    btn('.backup .download').click()
    await found('.download-confirm .code')
    expect(btn('.download-confirm .submit').disabled).toBe(true)
    expect(posts(calls, '/api/v1/system/backups/download-ticket')).toHaveLength(0)
    expect(dl).not.toHaveBeenCalled()
  })

  it('uploads a file with its progress, then asks for the same confirmation', async () => {
    const xhrs: FakeXHR[] = []
    class FakeXHR {
      url = ''
      headers: Record<string, string> = {}
      sent: unknown
      status = 0
      responseText = ''
      statusText = ''
      upload: { onprogress: ((e: { lengthComputable: boolean; loaded: number; total: number }) => void) | null } = { onprogress: null }
      onload: (() => void) | null = null
      onerror: (() => void) | null = null
      onabort: (() => void) | null = null
      ontimeout: (() => void) | null = null
      constructor() {
        xhrs.push(this)
      }
      open(_m: string, u: string) {
        this.url = u
      }
      setRequestHeader(k: string, v: string) {
        this.headers[k] = v
      }
      send(b: unknown) {
        this.sent = b
      }
      abort() {
        this.onabort?.()
      }
    }
    const { calls } = await boot({
      answer: (url, init) =>
        url === '/api/v1/system/restore' && init?.method === 'POST'
          ? json(202, restoreState({ run: { id: ID, by: 'boss', from: '0.8.0', to: '0.8.0', channel: '', requested_at: NOW, outcome: '', outcome_at: null, message: '', phase: 'queued', action: 'restore', subject: 'mine.bkp', source: 'upload' } }))
          : undefined,
    })
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    await vi.waitFor(() => expect(btn('.restore-upload-card .choose')?.disabled).toBe(false))
    const picker = input('.restore-upload-card .picker')
    const file = new File([new Uint8Array(2048)], 'mine.bkp')
    Object.defineProperty(picker, 'files', { value: [file], configurable: true })
    picker.dispatchEvent(new Event('change'))
    await vi.waitFor(() => expect(xhrs).toHaveLength(1))
    expect(xhrs[0].url).toBe('/api/v1/system/restore/upload?name=mine.bkp')
    expect(xhrs[0].headers['X-CSRF-Token']).toBe('c')
    expect(xhrs[0].sent).toBe(file)
    xhrs[0].upload.onprogress?.({ lengthComputable: true, loaded: 1024, total: 2048 })
    await vi.waitFor(() => expect(document.querySelector('.restore-upload-card .progress .amount')?.textContent).toContain('۵۰%'))
    xhrs[0].status = 201
    xhrs[0].responseText = JSON.stringify({ upload: { id: ID, name: 'mine.bkp', size: 2048, sha256: 'cd'.repeat(32), at: NOW, expires_at: NOW + 1800 } })
    xhrs[0].onload?.()
    const staged = await found('.restore-upload-card .staged')
    expect(staged.querySelector('.name')?.textContent).toBe('mine.bkp')
    expect(staged.querySelector('.sum')?.textContent).toContain('cd'.repeat(32))
    btn('.restore-upload').click()
    const dialog = await found('.restore-confirm')
    expect(dialog.querySelector('.name')?.textContent).toBe('mine.bkp')
    expect(dialog.querySelector('.from')?.textContent).toBe(text('system.backups.rs.source.upload'))
    input('.restore-confirm .ack input').click()
    type('.restore-confirm .typed', DOMAIN)
    await vi.waitFor(() => expect(btn('.restore-confirm .submit').disabled).toBe(false))
    btn('.restore-confirm .submit').click()
    await vi.waitFor(() => expect(posts(calls, '/api/v1/system/restore')).toHaveLength(1))
    expect(posts(calls, '/api/v1/system/restore')[0]).toEqual({ confirm: DOMAIN, acknowledge: true, upload_id: ID })
  })

  it('refuses a file over the limit before it is sent, and says why a restore cannot be asked for', async () => {
    const xhrs: unknown[] = []
    vi.stubGlobal('XMLHttpRequest', class { constructor() { xhrs.push(this) } })
    await boot({ restore: restoreState({ can: { allowed: true, ready: false, reason: 'restore_unsupported' }, limits: { max_upload: 1024, staged_seconds: 1800 } }) })
    const why = await found('.restore-upload-card .why-not')
    expect(why.textContent).toContain(text('system.backups.rs.not_ready.restore_unsupported'))
    expect(btn('.restore-upload-card .choose').disabled).toBe(true)
    expect(btn('.backup .restore').disabled).toBe(true)
    // a file picked in spite of that, over the limit
    const picker = input('.restore-upload-card .picker')
    Object.defineProperty(picker, 'files', { value: [new File([new Uint8Array(4096)], 'big.bkp')], configurable: true })
    picker.dispatchEvent(new Event('change'))
    const err = await found('.restore-upload-card .upload-error')
    expect(err.textContent).toContain(fill(text('system.backups.rs.refusal.upload_too_large'), { n: '0', unit: 'MiB' }))
    expect(xhrs).toHaveLength(0)
  })

  it('follows a restore that is running, also across the restart, and shows its end', async () => {
    const running = restoreState({
      run: { id: ID, by: 'boss', from: '0.8.0', to: '0.8.0', channel: '', requested_at: NOW - 30, outcome: '', outcome_at: null, message: '', phase: 'running', action: 'restore', subject: GOOD, source: 'backup' },
      helper: { ...restoreState().helper, state: 'running', id: ID, action: 'restore', step: 'restore', message: 'Restoring the database.', subject: GOOD, source: 'backup', by: 'boss', log: ['Database restored.'] },
    })
    await boot({ restore: running })
    const progress = await found('.restore-progress')
    expect(progress.querySelector('.progress-title')?.textContent).toBe(fill(text('system.backups.rs.progress.title.running'), { name: isolate(GOOD) }))
    const steps = [...progress.querySelectorAll('.step')] as HTMLElement[]
    expect(steps.map((s) => s.className.replace('step ', ''))).toEqual(['done', 'done', 'done', 'active', 'pending', 'pending'])
    expect(steps[3].textContent?.trim()).toBe(text('system.backups.rs.progress.step.restore'))
    expect(progress.querySelector('.updater-message')?.textContent).toBe('Restoring the database.')
    expect(progress.querySelector('.hint')?.textContent).toBe(text('system.backups.rs.progress.running'))
  })

  it('keeps trying while core is down, finds the end from the updater when core has no record, and refreshes the list', async () => {
    const mineHelper = (over: Record<string, unknown>) => ({
      ...restoreState().helper, id: ID, action: 'restore', subject: GOOD, source: 'backup', by: 'boss', requested_at: NOW - 30, ...over,
    })
    const open = restoreState({
      run: { id: ID, by: 'boss', from: '0.8.0', to: '0.8.0', channel: '', requested_at: NOW - 30, outcome: '', outcome_at: null, message: '', phase: 'running', action: 'restore', subject: GOOD, source: 'backup' },
      helper: mineHelper({ state: 'running', step: 'stop', message: 'Stopping the BOBRES services.' }),
    })
    // core replaced its own database: no row for the request, but the updater is still running it, then done
    const noRecord = (helperOver: Record<string, unknown>) => restoreState({ helper: mineHelper(helperOver) })
    let restoreCalls = 0
    let phase = 0 // what the server is doing now: the test moves it along
    const { calls } = await boot({
      fast: true,
      answer: (url, init) => {
        if (url !== '/api/v1/system/restore' || (init?.method ?? 'GET') !== 'GET') return undefined
        restoreCalls++
        switch (phase) {
          case 0:
            return json(200, open)
          case 1:
            throw new TypeError('network') // core is stopped
          case 2:
            return json(200, noRecord({ state: 'running', step: 'health', message: 'Waiting for the services to be healthy.' }))
          default:
            return json(200, noRecord({ state: 'done', step: 'done', message: 'BOBRES now runs the data of x.', finished_at: NOW }))
        }
      },
    })
    await found('.restore-progress')
    phase = 1
    await found('.restore-progress .reconnecting')
    // back on the restored data: shown from the updater's own status
    phase = 2
    await vi.waitFor(() => expect(document.querySelector('.restore-progress .step.active')?.textContent?.trim()).toBe(text('system.backups.rs.progress.step.health')))
    expect(document.querySelector('.restore-progress .reconnecting')).toBeNull()
    // and the end
    phase = 3
    await vi.waitFor(() => expect(document.querySelector('.restore-progress .progress-title')?.textContent).toBe(fill(text('system.backups.rs.progress.title.done'), { name: isolate(GOOD) })))
    expect(document.querySelector('.restore-progress .hint')?.textContent).toBe(text('system.backups.rs.progress.done'))
    expect(document.querySelector('.restore-progress .reload')).not.toBeNull()
    expect(document.querySelector('.restore-progress .dismiss')).not.toBeNull()
    // the list is read again: the backup of the data from before is in it now
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/system/backups').length).toBeGreaterThanOrEqual(2))
    // it stops looking once it has ended
    const n = restoreCalls
    await new Promise((r) => setTimeout(r, 80))
    expect(restoreCalls).toBe(n)
    btn('.restore-progress .dismiss').click()
    await vi.waitFor(() => expect(document.querySelector('.restore-progress')).toBeNull())
  })
})
