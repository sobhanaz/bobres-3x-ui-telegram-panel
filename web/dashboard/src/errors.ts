import { ApiError } from './api'

type T = (key: string, params?: Record<string, unknown>) => string

/** errorText is what a failed request shows: a sentence in the staff
 * member's language, and the server's own reason when it says more. */
export function errorText(e: unknown, t: T): { text: string; detail: string } {
  if (!(e instanceof ApiError)) return { text: t('error.generic'), detail: '' }
  const known: Record<string, string> = {
    network: 'error.network',
    forbidden: 'error.forbidden',
    not_found: 'error.not_found',
    unavailable: 'error.unavailable',
    insufficient_funds: 'error.insufficient_funds',
    conflict: 'error.conflict',
    state: 'error.state',
    invalid: 'error.invalid',
  }
  const key = known[e.code]
  if (!key) return { text: t('error.generic'), detail: '' }
  // The server explains state and input problems (in English): shown as a detail.
  const detail = e.code === 'state' || e.code === 'invalid' ? e.message : ''
  return { text: t(key), detail }
}
