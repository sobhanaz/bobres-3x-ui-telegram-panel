import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { ApiError } from '../api'
import {
  canDownload,
  confirmMatches,
  isRestoreFinal,
  limitText,
  page,
  RESTORE_REFUSALS,
  RESTORE_STEPS,
  restoreRefusalOf,
  restoreStepList,
  restoreView,
  startDownload,
  uploadBackup,
} from '../restore'
import { progressView } from '../updates'
import type { RestoreState, UpdateHelper, UpdateRun } from '../types'

// Downloading a backup and restoring one (System > Backups): what the page shows of a
// restore that replaces core's own database, the confirmation, and the download and upload.

const NOW = Math.floor(Date.now() / 1000)
const ID = '0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f60'
const FILE = 'bobres-20261011T033000Z.bkp'
const GIB = 1024 * 1024 * 1024

const helper = (over: Partial<UpdateHelper> = {}): UpdateHelper => ({
  available: true, problem: '', version: '0.8.0', state: 'idle', id: '', from: '', to: '', channel: '', step: '', message: '', started_at: null, finished_at: null, log: [],
  action: '', restore: true, subject: '', source: '', by: '', requested_at: null, ...over,
})
const run = (over: Partial<UpdateRun> = {}): UpdateRun => ({
  id: ID, by: 'boss', from: '0.8.0', to: '0.8.0', channel: '', requested_at: NOW - 60, outcome: '', outcome_at: null, message: '', phase: 'queued',
  action: 'restore', subject: FILE, source: 'backup', ...over,
})
const state = (over: Partial<RestoreState> = {}): RestoreState => ({
  version: '0.8.0', helper: helper(), run: null, last: null, other: null, can: { allowed: true, ready: true, reason: '' }, domain: 'panel.example.com', upload: null,
  limits: { max_upload: GIB * 2, staged_seconds: 1800 }, ...over,
})
const mine = (over: Partial<UpdateHelper> = {}) =>
  helper({ state: 'running', id: ID, action: 'restore', subject: FILE, source: 'backup', by: 'boss', requested_at: NOW - 60, started_at: NOW - 50, ...over })

describe('restoreView', () => {
  it('shows nothing when nothing is going on', () => {
    expect(restoreView(null, '')).toBeNull()
    expect(restoreView(state(), '')).toBeNull()
    expect(restoreView(state({ last: run({ outcome: 'done' }) }), '')).toBeNull() // an old end that this page did not ask for
  })

  it('follows the restore core has open, with the updater\'s steps once it names the id', () => {
    const queued = restoreView(state({ run: run() }), '')!
    expect(queued).toMatchObject({ phase: 'queued', mine: true, id: ID, subject: FILE, source: 'backup', by: 'boss', step: '', message: '' })
    const running = restoreView(state({ run: run({ phase: 'running' }), helper: mine({ step: 'restore', message: 'Restoring the database.', log: ['Database restored.'] }) }), '')!
    expect(running).toMatchObject({ phase: 'running', step: 'restore', message: 'Restoring the database.', log: ['Database restored.'] })
    // another run's status says nothing of this one
    const other = restoreView(state({ run: run(), helper: mine({ id: '0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f99', step: 'stop', message: 'x' }) }), '')!
    expect(other).toMatchObject({ step: '', message: '', log: [] })
    // and an update's status with the same id is not a restore's
    expect(restoreView(state({ run: run(), helper: mine({ action: 'update', step: 'pull' }) }), '')!.step).toBe('')
  })

  it('finds the end of the restore this page asked for', () => {
    for (const outcome of ['done', 'failed', 'rolled_back', 'not_started'] as const) {
      const v = restoreView(state({ last: run({ outcome, outcome_at: NOW, message: 'the sentence' }) }), ID)!
      expect(v).toMatchObject({ phase: outcome, mine: true, id: ID, message: 'the sentence', finishedAt: NOW })
      expect(isRestoreFinal(v)).toBe(true)
    }
    // the end of another restore is not shown
    expect(restoreView(state({ last: run({ outcome: 'done', id: '0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f99' }) }), ID)).toBeNull()
  })

  it('does not go blank when the restore replaced core\'s database and core has no row for it yet', () => {
    // core is back on the restored data: no open restore, no record of it, but the updater is still running it
    const during = restoreView(state({ helper: mine({ step: 'health', message: 'Waiting for the services to be healthy.' }) }), ID)!
    expect(during).toMatchObject({ phase: 'running', mine: true, id: ID, subject: FILE, source: 'backup', by: 'boss', step: 'health' })
    expect(during.requestedAt).toBe(NOW - 60)
    // the updater finished before core recorded the end: the page shows the end from its status
    const done = restoreView(state({ helper: mine({ state: 'done', step: 'done', message: 'BOBRES now runs the data of x.', finished_at: NOW }) }), ID)!
    expect(done).toMatchObject({ phase: 'done', mine: true, step: 'done', finishedAt: NOW })
    for (const s of ['failed', 'rolled_back'] as const) {
      expect(restoreView(state({ helper: mine({ state: s, finished_at: NOW }) }), ID)!.phase).toBe(s)
    }
    // but only for the restore this page asked for
    expect(restoreView(state({ helper: mine({ state: 'done', finished_at: NOW }) }), '')).toBeNull()
    expect(restoreView(state({ helper: mine({ state: 'done', action: 'update' }) }), ID)).toBeNull()
    // the updater is not usable: nothing is claimed from its status
    expect(restoreView(state({ helper: mine({ available: false }) }), ID)).toBeNull()
  })

  it('shows a restore that is running elsewhere', () => {
    const v = restoreView(state({ helper: mine() }), '')!
    expect(v).toMatchObject({ phase: 'running', mine: false, id: ID, subject: FILE })
  })

  it('is not the Updates tab\'s to show', () => {
    const s = state({ run: run({ phase: 'running' }), helper: mine({ step: 'restore' }) })
    expect(progressView(s, ID)).toBeNull()
    expect(progressView({ helper: mine(), run: null, last: null }, '')).toBeNull()
    // an update still shows, and its own status too
    const upd = { helper: helper({ state: 'running', id: ID, action: 'update', step: 'pull' }), run: null, last: null }
    expect(progressView(upd, '')?.phase).toBe('running')
  })
})

describe('restoreStepList', () => {
  const keys = (v: Parameters<typeof restoreStepList>[0]) => restoreStepList(v).map((s) => `${s.key}:${s.state}`)
  it('lists the steps in order', () => {
    expect(RESTORE_STEPS).toEqual(['verify', 'safety', 'stop', 'restore', 'start', 'health'])
    expect(keys({ phase: 'queued', step: '' })).toEqual(RESTORE_STEPS.map((k) => `${k}:pending`))
    expect(keys({ phase: 'running', step: '' })).toEqual(['verify:active', 'safety:pending', 'stop:pending', 'restore:pending', 'start:pending', 'health:pending'])
    expect(keys({ phase: 'running', step: 'restore' })).toEqual(['verify:done', 'safety:done', 'stop:done', 'restore:active', 'start:pending', 'health:pending'])
    expect(keys({ phase: 'running', step: 'done' })).toEqual(RESTORE_STEPS.map((k) => `${k}:done`))
    expect(keys({ phase: 'done', step: 'done' })).toEqual(RESTORE_STEPS.map((k) => `${k}:done`))
    expect(keys({ phase: 'not_started', step: '' })).toEqual(RESTORE_STEPS.map((k) => `${k}:pending`))
  })
  it('marks the step that failed', () => {
    expect(keys({ phase: 'failed', step: 'verify' })).toEqual(['verify:failed', 'safety:pending', 'stop:pending', 'restore:pending', 'start:pending', 'health:pending'])
    expect(keys({ phase: 'failed', step: 'restore' })).toEqual(['verify:done', 'safety:done', 'stop:done', 'restore:failed', 'start:pending', 'health:pending'])
    expect(keys({ phase: 'failed', step: '' })).toEqual(RESTORE_STEPS.map((k) => `${k}:pending`))
  })
  it('adds the rollback when the restored data did not come up healthy', () => {
    expect(keys({ phase: 'running', step: 'rollback' })).toEqual(['verify:done', 'safety:done', 'stop:done', 'restore:done', 'start:done', 'health:failed', 'rollback:active'])
    expect(keys({ phase: 'rolled_back', step: 'rollback' })).toEqual(['verify:done', 'safety:done', 'stop:done', 'restore:done', 'start:done', 'health:failed', 'rollback:done'])
    expect(keys({ phase: 'failed', step: 'rollback' }).at(-1)).toBe('rollback:failed')
  })
})

describe('refusals and small helpers', () => {
  it('reads the code the page translates', () => {
    const err = (status: number, error: string, extra: Record<string, unknown> = {}) => new ApiError(status, error, 'm', { error, ...extra })
    expect(restoreRefusalOf(err(400, 'invalid', { code: 'confirm_wrong' }))).toBe('confirm_wrong')
    expect(restoreRefusalOf(err(409, 'state', { code: 'restore_unsupported' }))).toBe('restore_unsupported')
    expect(restoreRefusalOf(err(413, 'upload_too_large', { code: 'upload_too_large' }))).toBe('upload_too_large')
    expect(restoreRefusalOf(err(403, 'code_required'))).toBe('code_required')
    expect(restoreRefusalOf(err(400, 'code_wrong'))).toBe('code_wrong')
    expect(restoreRefusalOf(err(423, 'locked'))).toBe('locked')
    expect(restoreRefusalOf(err(429, 'rate_limited'))).toBeNull()
    expect(restoreRefusalOf(err(500, 'internal'))).toBeNull()
    expect(restoreRefusalOf(new Error('x'))).toBeNull()
  })
  it('has a text in both languages for each refusal', () => {
    for (const code of RESTORE_REFUSALS) {
      expect((en.system.backups.rs.refusal as Record<string, string>)[code], code).toBeTruthy()
      expect((fa.system.backups.rs.refusal as Record<string, string>)[code], code).toBeTruthy()
    }
  })
  it('compares the typed domain as core does', () => {
    expect(confirmMatches('panel.example.com', 'panel.example.com')).toBe(true)
    expect(confirmMatches('  Panel.Example.COM ', 'panel.example.com')).toBe(true)
    expect(confirmMatches('panel.example.org', 'panel.example.com')).toBe(false)
    expect(confirmMatches('https://panel.example.com', 'panel.example.com')).toBe(false)
    expect(confirmMatches('', '')).toBe(false) // no domain known: nothing confirms
    expect(confirmMatches('x', undefined)).toBe(false)
  })
  it('offers a download for a file, not for the record of a failed run', () => {
    expect(canDownload({ file: FILE, size: 10 })).toBe(true)
    expect(canDownload({ file: FILE, size: 0 })).toBe(false)
    expect(canDownload({ file: FILE, size: 10, error: 'pg_dump failed' })).toBe(false)
    expect(canDownload({ size: 10 })).toBe(false)
  })
  it('says the limit in MiB or GiB', () => {
    expect(limitText(GIB * 2)).toEqual({ n: 2, unit: 'GiB' })
    expect(limitText(1500 * 1024 * 1024)).toEqual({ n: 1500, unit: 'MiB' })
    expect(limitText(1000)).toEqual({ n: 0, unit: 'MiB' })
  })
  it('keeps the placeholders of both catalogs the same', () => {
    const flat = (o: Record<string, unknown>, p = ''): [string, string][] =>
      Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v as Record<string, unknown>, p + k + '.') : [[p + k, String(v)] as [string, string]]))
    const holes = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join(',')
    const faMap = new Map(flat(fa.system.backups.rs, 'rs.').concat(flat(fa.system.backups.dl, 'dl.')))
    for (const [k, v] of flat(en.system.backups.rs, 'rs.').concat(flat(en.system.backups.dl, 'dl.'))) {
      expect(faMap.has(k), k).toBe(true)
      expect(holes(faMap.get(k)!), k).toBe(holes(v))
    }
  })
})

describe('download', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const URL_OK = '/api/v1/system/backups/download/' + 'A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v'

  it('buys a single-use address with the code and has the browser download it', async () => {
    const fetchMock = vi.fn(async () => json(200, { url: URL_OK, expires_in: 60 }))
    vi.stubGlobal('fetch', fetchMock)
    const dl = vi.spyOn(page, 'download').mockImplementation(() => {})
    await startDownload(FILE, '123456')
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/v1/system/backups/download-ticket')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body as string)).toEqual({ file: FILE, code: '123456' })
    expect(dl).toHaveBeenCalledWith(URL_OK)
    // no code, none sent
    await startDownload(FILE, '')
    expect(JSON.parse((fetchMock.mock.calls[1] as unknown as [string, RequestInit])[1].body as string)).toEqual({ file: FILE })
  })

  it('opens only an address of core\'s own', async () => {
    const dl = vi.spyOn(page, 'download').mockImplementation(() => {})
    for (const url of ['https://evil.example/x', '//evil.example/api/v1/system/backups/download/' + 'a'.repeat(30), '/api/v1/other', '/api/v1/system/backups/download/short', 'javascript:alert(1)']) {
      vi.stubGlobal('fetch', vi.fn(async () => json(200, { url, expires_in: 60 })))
      await expect(startDownload(FILE, '')).rejects.toBeInstanceOf(ApiError)
    }
    expect(dl).not.toHaveBeenCalled()
  })

  it('does not download when the code is refused', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(403, { error: 'code_required', message: 'enter the current code' })))
    const dl = vi.spyOn(page, 'download').mockImplementation(() => {})
    const err = await startDownload(FILE, '').catch((e: unknown) => e)
    expect(restoreRefusalOf(err)).toBe('code_required')
    expect(dl).not.toHaveBeenCalled()
  })

  it('makes an anchor the browser follows as a download, and removes it', () => {
    let seen: { href: string | null; download: boolean } | null = null
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      seen = { href: this.getAttribute('href'), download: this.hasAttribute('download') }
    })
    page.download(URL_OK)
    expect(seen).toEqual({ href: URL_OK, download: true })
    expect(document.querySelector('a[download]')).toBeNull()
  })
})

describe('upload', () => {
  afterEach(() => vi.unstubAllGlobals())
  class FakeXHR {
    static last: FakeXHR
    method = ''
    url = ''
    headers: Record<string, string> = {}
    sent: unknown = null
    aborted = false
    status = 0
    statusText = ''
    responseText = ''
    upload: { onprogress: ((e: { lengthComputable: boolean; loaded: number; total: number }) => void) | null } = { onprogress: null }
    onload: (() => void) | null = null
    onerror: (() => void) | null = null
    onabort: (() => void) | null = null
    ontimeout: (() => void) | null = null
    constructor() {
      FakeXHR.last = this
    }
    open(m: string, u: string) {
      this.method = m
      this.url = u
    }
    setRequestHeader(k: string, v: string) {
      this.headers[k] = v
    }
    send(b: unknown) {
      this.sent = b
    }
    abort() {
      this.aborted = true
      this.onabort?.()
    }
    finish(status: number, body: unknown) {
      this.status = status
      this.responseText = JSON.stringify(body)
      this.onload?.()
    }
  }

  it('streams the file as the raw body, with its name in the address and the CSRF token', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    const file = new File([new Uint8Array(10)], 'my backup (1).bkp')
    const progress: number[] = []
    const job = uploadBackup(file, (s) => progress.push(s))
    const x = FakeXHR.last
    expect(x.method).toBe('POST')
    expect(x.url).toBe('/api/v1/system/restore/upload?name=my%20backup%20(1).bkp')
    expect(x.headers['Content-Type']).toBe('application/octet-stream')
    expect(x.sent).toBe(file) // the file itself: the browser streams it from disk
    x.upload.onprogress?.({ lengthComputable: true, loaded: 5, total: 10 })
    x.upload.onprogress?.({ lengthComputable: false, loaded: 7, total: 0 })
    expect(progress).toEqual([5])
    x.finish(201, { upload: { id: ID, name: 'my backup (1).bkp', size: 10, sha256: 'ab', at: NOW, expires_at: NOW + 1800 } })
    await expect(job.done).resolves.toMatchObject({ upload: { id: ID, size: 10 } })
  })

  it('turns an answer that is an error into the error the page reads', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    const job = uploadBackup(new File([new Uint8Array(1)], 'x.bkp'))
    FakeXHR.last.finish(413, { error: 'upload_too_large', code: 'upload_too_large', message: 'the file is larger than the limit' })
    const err = await job.done.catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(413)
    expect(restoreRefusalOf(err)).toBe('upload_too_large')
    // the network
    const job2 = uploadBackup(new File([new Uint8Array(1)], 'x.bkp'))
    FakeXHR.last.onerror?.()
    expect(((await job2.done.catch((e: unknown) => e)) as ApiError).code).toBe('network')
    // the owner cancels
    const job3 = uploadBackup(new File([new Uint8Array(1)], 'x.bkp'))
    job3.abort()
    expect(FakeXHR.last.aborted).toBe(true)
    expect(((await job3.done.catch((e: unknown) => e)) as ApiError).code).toBe('aborted')
  })
})
