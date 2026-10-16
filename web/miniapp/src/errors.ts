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
    default:
      return 'err.unknown'
  }
}

export type Gate = '' | 'not_telegram' | 'maintenance' | 'banned' | 'expired' | 'unavailable' | 'off'

/** gateFor is the full-screen state a failure of signing in or of any call means, or "" for an ordinary error. */
export function gateFor(e: unknown): Gate {
  if (!(e instanceof ApiError)) return ''
  if (e.code === 'maintenance') return 'maintenance'
  if (e.code === 'banned') return 'banned'
  if (e.status === 404 && e.code === 'disabled') return 'off'
  if (e.status === 401) return 'expired'
  if (e.status === 503 && e.code === 'unavailable') return 'unavailable'
  return ''
}
