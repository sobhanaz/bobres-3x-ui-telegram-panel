// The approval of dashboard sign-ins in Telegram (docs/04-dashboard.md, "Signing in"): what a sign-in that waits looks like to the login
// page, what Settings > Sign-in security reads and writes, and the small rules both share.

/** Who was asked to approve: the staff member themself, and the owners for another staff member's sign-in. */
export type Approver = 'self' | 'owner'

/** The sign-in that waits for an approval (core answers a login, and each poll, with it while it waits). */
export interface PendingLogin {
  status: 'pending'
  /** unix seconds: when the request ends */
  expires_at: number
  approvers: Approver[]
  /** at least one approver's message was sent */
  delivered: boolean
  /** none could be sent: nothing will come of the wait */
  failed: boolean
  ttl_s: number
  /** what to type on the server to sign in without approval */
  command: string
}

/** How a wait ends without a session; none is a browser with nothing to wait for. */
export type PendingEnd = 'denied' | 'expired' | 'cancelled' | 'undeliverable' | 'none'

export interface LoginApprovalInfo {
  enabled: boolean
  notify_owner: boolean
  ttl_s: number
  /** core has a bot to ask in Telegram */
  bot_ready: boolean
  /** sign-ins waiting for an answer now */
  waiting: number
  command: string
}

/** GET /security. */
export interface SecurityReply {
  login_approval: LoginApprovalInfo
  /** the viewer may switch it (the owner) */
  can_change: boolean
}

/** The command that signs in without approval: shown wherever an approval cannot come. */
export const APPROVAL_COMMAND = 'sudo bobres admin approval off'

/** How often a waiting page asks how its sign-in stands, in milliseconds. */
export const POLL_MS = 2000

/** After this many seconds without a message delivered the page says the bot may be down. */
export const SLOW_AFTER_S = 20

/** isPending: the answer of a login call is a waiting sign-in, not a session. */
export function isPending(r: unknown): r is PendingLogin {
  return typeof r === 'object' && r !== null && (r as { status?: unknown }).status === 'pending'
}

/** secondsLeft counts down to the end of the request (never below zero). */
export function secondsLeft(expiresAt: number, nowMs: number): number {
  return Math.max(0, Math.ceil(expiresAt - nowMs / 1000))
}

/** clock writes seconds as m:ss. */
export function clock(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

const REASONS = ['no_bot', 'bot_down', 'blocked', 'telegram', 'no_answer', 'no_approver']

/** reasonKey picks the sentence for why no approval could be asked for (a reason core did not name reads as the general one). */
export function reasonKey(reason: unknown): string {
  return REASONS.includes(String(reason)) ? String(reason) : 'other'
}

/** pendingState picks the line a wait shows: who was asked, or what is wrong when the message did not go out (or is slow to). */
export function pendingState(p: PendingLogin, waitedS: number): 'failed' | 'slow' | 'both' | 'self' {
  if (p.failed) return 'failed'
  if (!p.delivered && waitedS >= SLOW_AFTER_S) return 'slow'
  return p.approvers.includes('owner') ? 'both' : 'self'
}
