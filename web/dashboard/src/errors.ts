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
    rate_limited: 'error.rate_limited',
  }
  const key = known[e.code] ?? (e.status === 429 ? 'error.rate_limited' : '')
  if (!key) return { text: t('error.generic'), detail: '' }
  return { text: t(key), detail: explains(e) ? e.message : '' }
}

/** The server explains state and input problems, and a refusal of one field
 * this role may not set (in English): shown as a detail. */
function explains(e: ApiError): boolean {
  return e.code === 'state' || e.code === 'invalid' || (e.code === 'forbidden' && typeof e.data.field === 'string')
}

/** fieldError is core's words about one field it refused, by the field's
 * name: input it does not accept (400), or a field this role may not set
 * (403). Empty for any other failure. */
export function fieldError(e: unknown): Record<string, string> {
  if (!(e instanceof ApiError) || (e.status !== 400 && e.status !== 403) || typeof e.data.field !== 'string') return {}
  return { [e.data.field]: e.message }
}
