import { api, ApiError, upload } from './api'
import { isFinal, type RunPhase, type StepState } from './updates'
import type { DownloadTicket, RestoreState, RestoreUpload } from './types'

// System > Backups: downloading a backup and restoring one (internal/sysupdate,
// docs/04-dashboard.md). A download is the browser's own (a link, so the file goes
// to disk and never through the page's memory); a restore is asked of the server's
// updater, which stops the store, restores, checks it is healthy and goes back on its
// own if it is not. The page follows it step by step, across the restart of core
// that the restore itself causes (core's own database is replaced by it).

/** The steps a restore goes through, in order. A rollback is one more, shown only
 * when the restored data did not come up healthy. */
export const RESTORE_STEPS = ['verify', 'safety', 'stop', 'restore', 'start', 'health'] as const

/** What the progress panel shows of a restore. */
export interface RestoreRunView {
  phase: RunPhase
  /** asked for in the dashboard (false: one started elsewhere that the updater is running) */
  mine: boolean
  id: string
  /** the backup: its file name, or the name an upload had */
  subject: string
  /** backup | upload */
  source: string
  by: string
  /** the step the updater says it is at ('' when it has not said) */
  step: string
  /** the updater's one sentence, or core's for a restore that never ended well */
  message: string
  log: string[]
  requestedAt: number | null
  finishedAt: number | null
}

const PHASES: readonly string[] = ['queued', 'running', 'done', 'failed', 'rolled_back', 'not_started']
const asPhase = (v: unknown): RunPhase | null => (typeof v === 'string' && PHASES.includes(v) ? (v as RunPhase) : null)

/** restoreView reads the state the way the panel shows it:
 *   - the restore core has open;
 *   - else the one this page started (tracked) once it ended;
 *   - else, for the one this page started, the updater's own word: a restore that
 *     replaced core's database leaves core with no row until its worker records the
 *     end from the updater's status (a few seconds after core is back), and the page
 *     must not go blank in between;
 *   - else a restore the updater says it is running that this page did not ask for.
 * null when nothing is going on. The updater's status counts for a restore only
 * when it names its id and says it is a restore. */
export function restoreView(s: Pick<RestoreState, 'helper' | 'run' | 'last'> | null | undefined, tracked: string): RestoreRunView | null {
  if (!s) return null
  const h = s.helper
  const about = (id: string) => h.available && h.action === 'restore' && h.id === id
  if (s.run) {
    const r = s.run
    const mine = about(r.id)
    return {
      phase: asPhase(r.phase) ?? 'queued', mine: true, id: r.id, subject: r.subject ?? '', source: r.source ?? '', by: r.by,
      step: mine ? h.step : '', message: mine ? h.message : '', log: mine ? (h.log ?? []) : [], requestedAt: r.requested_at, finishedAt: mine ? h.finished_at : null,
    }
  }
  if (tracked && s.last && s.last.id === tracked) {
    const r = s.last
    const mine = about(r.id)
    return {
      phase: asPhase(r.outcome) ?? 'failed', mine: true, id: r.id, subject: r.subject ?? '', source: r.source ?? '', by: r.by,
      step: mine ? h.step : '', message: r.message || (mine ? h.message : ''), log: mine ? (h.log ?? []) : [], requestedAt: r.requested_at, finishedAt: r.outcome_at,
    }
  }
  if (tracked && about(tracked)) {
    const phase = h.state === 'running' ? 'running' : asPhase(h.state) ?? 'failed'
    return {
      phase, mine: true, id: tracked, subject: h.subject ?? '', source: h.source ?? '', by: h.by ?? '', step: h.step, message: h.message, log: h.log ?? [],
      requestedAt: h.requested_at ?? null, finishedAt: h.finished_at,
    }
  }
  if (h.available && h.action === 'restore' && h.state === 'running') {
    return {
      phase: 'running', mine: false, id: h.id, subject: h.subject ?? '', source: h.source ?? '', by: h.by ?? '', step: h.step, message: h.message, log: h.log ?? [],
      requestedAt: h.requested_at ?? h.started_at, finishedAt: null,
    }
  }
  return null
}

/** restoreStepList is the steps as the panel lists them, each done, being made, or
 * still to come. When the restored data did not come up healthy the install goes back
 * to the data from before: that is one more step, and the health check is the one
 * that failed. */
export function restoreStepList(v: Pick<RestoreRunView, 'phase' | 'step'>): { key: string; state: StepState }[] {
  const base = RESTORE_STEPS as readonly string[]
  const rolling = v.step === 'rollback' || v.phase === 'rolled_back'
  if (rolling) {
    const out = RESTORE_STEPS.map((key): { key: string; state: StepState } => ({ key, state: key === 'health' ? 'failed' : 'done' }))
    return [...out, { key: 'rollback', state: v.phase === 'rolled_back' ? 'done' : v.phase === 'failed' ? 'failed' : 'active' }]
  }
  const at = base.indexOf(v.step)
  return RESTORE_STEPS.map((key, i): { key: string; state: StepState } => {
    switch (v.phase) {
      case 'done':
        return { key, state: 'done' }
      case 'queued':
      case 'not_started':
        return { key, state: 'pending' }
      case 'failed':
        return { key, state: at < 0 ? 'pending' : i < at ? 'done' : i === at ? 'failed' : 'pending' }
      default: // running
        if (v.step === 'done') return { key, state: 'done' }
        if (at < 0) return { key, state: i === 0 ? 'active' : 'pending' }
        return { key, state: i < at ? 'done' : i === at ? 'active' : 'pending' }
    }
  })
}

/** isRestoreFinal: the restore is over, whichever way. */
export const isRestoreFinal = (v: Pick<RestoreRunView, 'phase'> | null): boolean => !!v && isFinal(v.phase)

/** The refusals the page explains, each with its own text; any other is "other". */
export const RESTORE_REFUSALS = [
  'helper_missing',
  'restore_unsupported',
  'no_domain',
  'already_running',
  'confirm_wrong',
  'ack_required',
  'backup_unknown',
  'upload_missing',
  'upload_busy',
  'upload_too_large',
  'upload_empty',
  'request_failed',
  'code_required',
  'code_wrong',
  'locked',
] as const

/** restoreRefusalOf reads why core refused a download or a restore: its code (the
 * authenticator code's answers are the error itself, the restore's refusals a code
 * beside it). null for anything the page has no text for (the network, a limit, a
 * server error: errorText says those). */
export function restoreRefusalOf(e: unknown): string | null {
  if (!(e instanceof ApiError)) return null
  const code = typeof e.data.code === 'string' ? e.data.code : e.code
  return (RESTORE_REFUSALS as readonly string[]).includes(code) ? code : null
}

/** confirmMatches: what the owner typed is the store's domain (case and the spaces
 * around it do not matter; core checks it again). */
export function confirmMatches(typed: string, domain: string | undefined): boolean {
  const d = (domain ?? '').trim().toLowerCase()
  return d !== '' && typed.trim().toLowerCase() === d
}

/** canDownload: a backup file that can be fetched (not the record of a run that failed). */
export function canDownload(b: { file?: string; size: number; error?: string }): boolean {
  return !!b.file && !b.error && b.size > 0
}

/** uploadBackup streams the owner's file to core, which keeps it for the restore. */
export function uploadBackup(file: File, onProgress?: (sent: number, total: number) => void): { done: Promise<{ upload: RestoreUpload }>; abort: () => void } {
  return upload<{ upload: RestoreUpload }>('/system/restore/upload?name=' + encodeURIComponent(file.name), file, onProgress)
}

/** The browser's page; tests replace download. */
export const page = {
  /** download has the browser save the address as a file (it answers with
   * Content-Disposition: attachment, so the page itself stays where it is). */
  download: (url: string): void => {
    const a = document.createElement('a')
    a.href = url
    a.download = ''
    a.rel = 'noopener'
    a.style.display = 'none'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
  },
}

const TICKET_URL = /^\/api\/v1\/system\/backups\/download\/[A-Za-z0-9_-]{20,}$/

/** startDownload asks core for a single-use address for the backup (the owner's
 * authenticator code is checked there, where there is one) and has the browser
 * download it. The file is encrypted, and the key is not in it. */
export async function startDownload(file: string, code: string): Promise<void> {
  const body: Record<string, unknown> = { file }
  if (code) body.code = code
  const t = await api<DownloadTicket>('POST', '/system/backups/download-ticket', body)
  // only an address of core's own is ever opened
  if (!TICKET_URL.test(t.url)) throw new ApiError(502, 'error', 'unexpected download address')
  page.download(t.url)
}

/** A byte count in MiB for a limit ("2048 MiB"); a whole GiB reads "2 GiB". */
export function limitText(bytes: number): { n: number; unit: 'MiB' | 'GiB' } {
  const mib = Math.floor(bytes / (1 << 20))
  return mib >= 1024 && mib % 1024 === 0 ? { n: mib / 1024, unit: 'GiB' } : { n: mib, unit: 'MiB' }
}
