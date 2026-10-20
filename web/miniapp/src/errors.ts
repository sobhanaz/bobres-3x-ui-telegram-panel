import { ApiError } from './api'
import type { Key } from './i18n'

/** errorKey words an API failure for the customer. */
export function errorKey(e: unknown): Key {
  if (!(e instanceof ApiError)) return 'err.unknown'
  switch (e.code) {
    case 'network':
      return 'err.network'
    case 'rate_limited':
      return 'err.rate_limited'
    case 'insufficient_funds':
      return 'err.insufficient_funds'
    case 'plan_unavailable':
      return 'err.plan_unavailable'
    case 'not_extendable':
      return 'err.not_extendable'
    case 'not_payable':
      return 'err.not_payable'
    case 'unavailable_now':
      return 'err.unavailable_now'
    case 'maintenance':
      return 'err.maintenance'
    case 'banned':
      return 'err.banned'
    case 'state':
    case 'conflict':
      return 'err.state'
    case 'invalid':
    case 'discount':
      return 'err.invalid'
    // Stars and manual payments (the texts are in sections/payments/messages.ts)
    case 'method_off':
    case 'too_large':
    case 'bad_type':
    case 'empty':
    case 'no_receipt':
    case 'one_receipt':
    case 'reference_required':
    case 'bad_txid':
    case 'already_submitted':
    case 'payment_closed':
    case 'duplicate':
    case 'txid_announced':
    case 'chat_closed':
    case 'invoice_refused':
    case 'busy':
    case 'bot_unavailable':
      return `err.${e.code}`
    case 'unavailable': // payments or the store behind core is away for a moment
      return 'err.busy'
    default:
      return 'err.unknown'
  }
}

export type Gate = '' | 'not_telegram' | 'maintenance' | 'banned' | 'join_required' | 'expired' | 'unavailable' | 'off'

/** gateFor is the full-screen state a failure of signing in or of any call means, or "" for an ordinary error. */
export function gateFor(e: unknown): Gate {
  if (!(e instanceof ApiError)) return ''
  if (e.code === 'maintenance') return 'maintenance'
  if (e.code === 'banned') return 'banned'
  if (e.code === 'join_required') return 'join_required'
  if (e.status === 404 && e.code === 'disabled') return 'off'
  if (e.status === 401) return 'expired'
  if (e.status === 503 && e.code === 'unavailable') return 'unavailable'
  return ''
}
