// The reseller section's calls to core (/api/mini/v1/reseller...). They ride
// on the app's own session (src/api.ts `call`): the reseller is whoever is
// signed in, and nothing here names another user or sends a price or an
// amount. Core checks again on every call that the caller is an active
// reseller whose license includes resellers.
import { ApiError, call, type Service } from '../../api'
import { tr } from '../../i18n'
import { errorKey } from '../../errors'

export interface Money { currency: string; amount: number; scale: number }
export interface Balance { currency: string; balance: number; credit_limit: number; available: number; scale: number }

/** ServiceCounts is how many services the reseller has in each group (every service is in exactly one). */
export interface ServiceCounts { total: number; active: number; expiring_soon: number; expired: number; paused: number; pending: number }

export interface Panel {
  tier: string
  discount_percent: number
  commission_percent: number
  balances: Balance[]
  customers: number
  commission_earned: Money[]
  ref_code: string
  invite_link: string
  counts: ServiceCounts
}

/** ResellerService is a customer's service as the section shows it: the shop's Service plus what only a reseller has. */
export interface ResellerService extends Service {
  note: string
  created_at: number
  can_pause: boolean
  can_resume: boolean
  can_delete: boolean
}

/** Filter is a group of services: '' is all of them. */
export type Filter = '' | 'active' | 'expiring_soon' | 'expired' | 'paused'
export const filters: Filter[] = ['', 'active', 'expiring_soon', 'expired', 'paused']

export interface ServicePage { services: ResellerService[]; total: number; page: number; size: number; counts: ServiceCounts }

export interface Customer {
  telegram_id: number
  username: string
  joined_at: number
  paid_orders: number
  spent: Money[]
  commission: Money[]
}
export interface CustomerPage { customers: Customer[]; total: number; page: number; size: number }

export interface StatementEntry { kind: string; amount: number; currency: string; balance_after: number; at: number; scale: number }
export interface StatementPage { entries: StatementEntry[]; page: number; size: number; has_more: boolean }

const base = '/reseller'

function query(params: Record<string, string | number | undefined>): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== '') q.set(k, String(v))
  const s = q.toString()
  return s ? `?${s}` : ''
}

export const resellerApi = {
  panel: () => call<Panel>('GET', base),
  services: (p: { status?: Filter; q?: string; page?: number; size?: number } = {}) =>
    call<ServicePage>('GET', `${base}/services${query({ status: p.status, q: p.q, page: p.page, size: p.size })}`),
  pause: (id: string) => call<{ service: ResellerService }>('POST', `${base}/services/${id}/pause`, {}).then((r) => r.service),
  resume: (id: string) => call<{ service: ResellerService }>('POST', `${base}/services/${id}/resume`, {}).then((r) => r.service),
  /** remove deletes a service for good (no refund); core refuses a call without the confirmation. */
  remove: (id: string) => call<{ ok: boolean }>('POST', `${base}/services/${id}/delete`, { confirm: true }),
  rename: (id: string, label: string) => call<{ service: ResellerService }>('POST', `${base}/services/${id}/label`, { label }).then((r) => r.service),
  note: (id: string, note: string) => call<{ service: ResellerService }>('POST', `${base}/services/${id}/note`, { note }).then((r) => r.service),
  customers: (page = 1) => call<CustomerPage>('GET', `${base}/customers${query({ page })}`),
  statement: (page = 1) => call<StatementPage>('GET', `${base}/statement${query({ page })}`),
}

/** errText words a failed call of the section for the reseller. */
export function errText(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case 'paused_by_staff':
        return tr('reseller.err.paused_by_staff')
      case 'not_found':
        return tr('reseller.err.gone')
      case 'forbidden':
        return tr('reseller.err.forbidden')
      case 'state':
        return tr('reseller.err.state')
    }
  }
  return tr(errorKey(e))
}

/** isGone: the service is no longer the reseller's (deleted, or never theirs). */
export const isGone = (e: unknown): boolean => e instanceof ApiError && e.status === 404
