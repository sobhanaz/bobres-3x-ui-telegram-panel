// The Mini App's calls to core (/api/mini/v1). The session token lives in
// memory only; when it stops working the app signs in again with the init
// data Telegram gave it (which Telegram itself keeps for the whole visit).
import { initData } from './telegram'

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public reason = '',
  ) {
    super(message)
  }
}

export interface Wallet { currency: string; balance: number; scale: number }
export interface Me {
  user: { id: string; username: string; language: string }
  wallets: Wallet[]
  brand: { name: string; color: string; support: string; support_url: string; bot_url: string }
  maintenance: boolean
  top_up: { currency: string; min: number; max: number }
}
export interface Plan {
  id: string
  names: Record<string, string>
  kind: 'traffic' | 'time' | 'both'
  days: number | null
  traffic_bytes: number | null
  price: number
  list_price: number
  currency: string
  scale: number
  topup: boolean
}
export interface Service {
  id: string
  label: string
  status: string
  expires_at: number | null
  traffic_total: number | null
  traffic_used: number
  link: string
  page_url: string
  can_renew: boolean
  can_topup: boolean
}
export interface LedgerEntry { kind: string; amount: number; currency: string; balance_after: number; at: number; scale: number }
export interface Order {
  id: string
  plan_id: string
  type: string
  status: string
  amount: number
  discount_amount: number
  currency: string
  scale: number
  subscription_id: string
}
export interface Quote { list_price: number; discount: number; total: number; currency: string; scale: number; discount_code: string }
export interface Intent {
  intent_id: string
  status: string
  provider: string
  order_id: string
  amount: number
  currency: string
  scale: number
  pay_url: string
  gateway_amount: number
  gateway_currency: string
  scale_gateway: number
  details: { pay_address?: string; network?: string; expires_at?: string; reference?: string }
}
export interface OrderRequest { plan_id: string; type?: string; subscription_id?: string; discount_code?: string; key?: string }

let token = ''
let signingIn: Promise<Me> | null = null

async function raw<T>(method: string, path: string, body?: unknown, auth = true): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (auth && token) headers.Authorization = `Bearer ${token}`
  let res: Response
  try {
    res = await fetch(`/api/mini/v1${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), cache: 'no-store' })
  } catch {
    throw new ApiError(0, 'network', 'network error')
  }
  let data: Record<string, unknown> = {}
  try {
    data = (await res.json()) as Record<string, unknown>
  } catch {
    // an answer without a body: the status says it all
  }
  if (!res.ok) {
    throw new ApiError(res.status, String(data.error ?? 'error'), String(data.message ?? res.statusText), String(data.reason ?? ''))
  }
  return data as T
}

/** signIn trades the Telegram init data for a session. */
export function signIn(): Promise<Me> {
  signingIn ??= (async () => {
    const data = initData()
    if (!data) throw new ApiError(401, 'auth', 'not opened from Telegram')
    const out = await raw<Me & { token: string }>('POST', '/auth', { init_data: data }, false)
    token = out.token
    return out
  })().finally(() => {
    signingIn = null
  })
  return signingIn
}

/** call makes an authenticated call; one expired session is renewed and the call repeated. */
export async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  if (!token) await signIn()
  try {
    return await raw<T>(method, path, body)
  } catch (e) {
    if (e instanceof ApiError && e.status === 401 && e.code === 'unauthorized') {
      token = ''
      await signIn()
      return raw<T>(method, path, body)
    }
    throw e
  }
}

export const api = {
  me: () => call<Me>('GET', '/me'),
  plans: () => call<{ plans: Plan[] }>('GET', '/plans').then((r) => r.plans),
  services: () => call<{ services: Service[] }>('GET', '/services').then((r) => r.services),
  wallet: () => call<{ wallets: Wallet[]; ledger: LedgerEntry[] }>('GET', '/wallet'),
  methods: (currency: string, amount: number) =>
    call<{ methods: string[] }>('GET', `/methods?currency=${encodeURIComponent(currency)}&amount=${amount}`).then((r) => r.methods),
  quote: (req: OrderRequest) => call<Quote>('POST', '/quote', req),
  createOrder: (req: OrderRequest) => call<Order>('POST', '/orders', req),
  order: (id: string) => call<Order>('GET', `/orders/${id}`),
  payWallet: (id: string) => call<Order>('POST', `/orders/${id}/pay-wallet`, {}),
  pay: (id: string, method: string) => call<Intent>('POST', `/orders/${id}/pay`, { method }),
  topup: (amount: number, method: string, key: string) => call<Intent>('POST', '/topup', { amount, method, key }),
  checkPayment: (id: string) => call<{ intent_id: string; status: string; order_id: string; failure_reason: string }>('POST', `/payments/${id}/check`, {}),
  logout: () => raw<{ ok: boolean }>('POST', '/logout', {}).finally(() => (token = '')),
}

/** resetSession forgets the token (tests). */
export function resetSession(): void {
  token = ''
}
