import { ApiError } from './api'
import { UPDATE_COMMAND } from './system'
import type { UpdateCheck, UpdatesProgress } from './types'

// The Updates tab's "Update now": what the page shows of an update that core
// asked the server's updater to make (internal/sysupdate, docs/04-dashboard.md),
// and how it follows it, also across the restart the update itself causes.

/** How often the page reads the update's progress (ms): every `ms`, `failMs`
 * while core does not answer (it restarts in the middle of the update), `slowMs`
 * once it has gone on for `slowAfterMs`; it gives up after `maxMs` (core
 * itself gives up on an update the updater has said it runs for an hour). */
export const updatePoll = { ms: 2000, failMs: 3000, slowAfterMs: 5 * 60_000, slowMs: 5000, maxMs: 90 * 60_000 }

/** pollDelay is how long to wait before reading the progress again. */
export function pollDelay(elapsedMs: number, failing: boolean): number {
  if (failing) return updatePoll.failMs
  return elapsedMs > updatePoll.slowAfterMs ? updatePoll.slowMs : updatePoll.ms
}

/** Where an update is: asked for and waiting for the updater (queued), being
 * made (running), or over: done, failed, rolled back (the new version did not
 * come up healthy and the install went back to the old one) or never started
 * (the updater did not take the request up). */
export type RunPhase = 'queued' | 'running' | 'done' | 'failed' | 'rolled_back' | 'not_started'

const PHASES: readonly string[] = ['queued', 'running', 'done', 'failed', 'rolled_back', 'not_started']
const asPhase = (v: unknown): RunPhase | null => (typeof v === 'string' && PHASES.includes(v) ? (v as RunPhase) : null)

/** isFinal: the update is over, whichever way. */
export function isFinal(phase: RunPhase | ''): boolean {
  return phase === 'done' || phase === 'failed' || phase === 'rolled_back' || phase === 'not_started'
}

/** What the progress panel shows. */
export interface UpdateRunView {
  phase: RunPhase
  /** asked for in the dashboard (false: one started on the server) */
  mine: boolean
  id: string
  from: string
  to: string
  channel: string
  /** who asked */
  by: string
  /** the step the updater says it is at ('' when it has not said) */
  step: string
  /** the updater's one sentence, or core's for an update that never ended well */
  message: string
  log: string[]
  requestedAt: number | null
  finishedAt: number | null
}

/** progressView reads the progress the way the panel shows it: the update core
 * has open, else the one this page started (tracked) once it ended, else an
 * update the updater says it is running that nobody here asked for (started on
 * the server); null when nothing is going on. The updater's status counts for
 * an update only when it names its id. */
export function progressView(p: Pick<UpdatesProgress, 'helper' | 'run' | 'last'> | null | undefined, tracked: string): UpdateRunView | null {
  if (!p) return null
  const h = p.helper
  const about = (id: string) => h.available && h.id === id
  if (p.run) {
    const r = p.run
    const mine = about(r.id)
    return {
      phase: asPhase(r.phase) ?? 'queued', mine: true, id: r.id, from: r.from, to: r.to, channel: r.channel, by: r.by,
      step: mine ? h.step : '', message: mine ? h.message : '', log: mine ? (h.log ?? []) : [],
      requestedAt: r.requested_at, finishedAt: mine ? h.finished_at : null,
    }
  }
  if (tracked && p.last && p.last.id === tracked) {
    const r = p.last
    const mine = about(r.id)
    return {
      phase: asPhase(r.outcome) ?? 'failed', mine: true, id: r.id, from: r.from, to: r.to, channel: r.channel, by: r.by,
      step: mine ? h.step : '', message: r.message || (mine ? h.message : ''), log: mine ? (h.log ?? []) : [],
      requestedAt: r.requested_at, finishedAt: r.outcome_at,
    }
  }
  if (h.available && h.state === 'running') {
    return {
      phase: 'running', mine: false, id: h.id, from: h.from, to: h.to, channel: h.channel, by: '', step: h.step, message: h.message,
      log: h.log ?? [], requestedAt: h.started_at, finishedAt: null,
    }
  }
  return null
}

/** The steps an update goes through, in order. */
export const UPDATE_STEPS = ['verify', 'backup', 'pull', 'restart', 'health'] as const

export type StepState = 'done' | 'active' | 'pending' | 'failed'

/** stepList is the steps as the panel lists them, each done, being made, or
 * still to come. While the install goes back to the old version (or after it
 * did) only that step is listed: which step failed is not known. */
export function stepList(v: Pick<UpdateRunView, 'phase' | 'step'>): { key: string; state: StepState }[] {
  if (v.step === 'rollback' || v.phase === 'rolled_back') return [{ key: 'rollback', state: v.phase === 'rolled_back' ? 'done' : 'active' }]
  const at = (UPDATE_STEPS as readonly string[]).indexOf(v.step)
  return UPDATE_STEPS.map((key, i): { key: string; state: StepState } => {
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

/** isPrerelease: a release number with a label after its numbers (1.5.0-rc.1). */
export function isPrerelease(version: string | null | undefined): boolean {
  return /^\d+\.\d+\.\d+-/.test((version ?? '').trim())
}

/** issuedAt is when the release was signed, in unix seconds (the bot gives
 * 2026-10-20T10:00Z); null when it does not say. */
export function issuedAt(u: Pick<UpdateCheck, 'issued'> | null | undefined): number | null {
  const v = (u?.issued ?? '').trim()
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}Z$/.test(v)) return null
  const ms = Date.parse(v)
  return Number.isNaN(ms) ? null : Math.floor(ms / 1000)
}

/** updateCommand installs the release on the server by hand, on the channel the
 * page shows: the install's own .env may name another channel than the one
 * chosen in the dashboard, and `sudo bobres update` alone would follow that. */
export function updateCommand(channel: string | null | undefined): string {
  const c = (channel ?? '').trim().toLowerCase()
  return c === 'stable' || c === 'beta' ? `${UPDATE_COMMAND} --channel ${c}` : UPDATE_COMMAND
}

/** The refusals the page explains, each with its own text; any other is "other". */
export const REFUSALS = [
  'helper_missing',
  'already_running',
  'check_unavailable',
  'no_release',
  'not_newer',
  'version_changed',
  'channel_changed',
  'prerelease_unconfirmed',
  'dev_build',
  'request_failed',
  'confirm_required',
  'code_required',
  'code_wrong',
  'locked',
] as const

/** refusalOf reads why core refused an update (or the step-up code): its code
 * (the authenticator code's answers are the error itself, the update's refusals
 * are a code beside it), and for a release that changed, the one that is newest
 * now. null for anything the page has no text for (the network, a limit, a
 * server error: errorText says those). */
export function refusalOf(e: unknown): { code: string; latest: string } | null {
  if (!(e instanceof ApiError)) return null
  const code = typeof e.data.code === 'string' ? e.data.code : e.code
  if (!(REFUSALS as readonly string[]).includes(code)) return null
  return { code, latest: typeof e.data.latest === 'string' ? e.data.latest : '' }
}

/** The updater's problems the page explains (UpdateHelper.problem). */
export const HELPER_PROBLEMS = ['no_dir', 'no_status', 'not_installed', 'bad_status', 'unsupported'] as const

/** helperProblem is the problem as the page has a text for it. */
export function helperProblem(problem: string | null | undefined): string {
  return (HELPER_PROBLEMS as readonly string[]).includes(problem ?? '') ? (problem as string) : 'bad_status'
}

/** copyText puts text on the clipboard. The browser's clipboard exists only on
 * pages served over HTTPS (or localhost): on any other it is missing, and the
 * old copy button then did nothing at all. Without it, the page tries the older
 * way (selecting a text and copying it); false when neither works, so the page
 * can say so and leave the text to be selected by hand. */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    /* blocked: try the older way */
  }
  try {
    const el = document.createElement('textarea')
    el.value = text
    el.setAttribute('readonly', '')
    el.style.position = 'fixed'
    el.style.insetBlockStart = '0'
    el.style.opacity = '0'
    document.body.appendChild(el)
    el.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(el)
    return ok
  } catch {
    return false
  }
}

/** page is the browser's page; tests replace reload. */
export const page = {
  reload: (): void => window.location.reload(),
}
