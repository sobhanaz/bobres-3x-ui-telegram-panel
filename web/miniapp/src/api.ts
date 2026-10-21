// The Mini App's calls to core (/api/mini/v1). The session token lives in
// memory only; when it stops working the app signs in again with the init
// data Telegram gave it (which Telegram itself keeps for the whole visit).
import { initData } from './telegram'

/** A channel or group the customer still has to join: its link ("" when the store gave none) and the name to show ("" when none). */
export interface JoinChatLink { url: string; label: string }

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public reason = '',
    /** join_required: where to join the store's required channel (a t.me link; "" when the store gave none) */
    public joinUrl = '',
    /** join_required, when the store requires several channels and groups: every one the customer still lacks */
    public joinChats: JoinChatLink[] = [],
  ) {
    super(message)
  }
}

/** joinChatsOf reads the chats of a join_required answer; anything that is not a list of {url, label} is none. */
function joinChatsOf(v: unknown): JoinChatLink[] {
  if (!Array.isArray(v)) return []
  return v.flatMap((c) => {
    if (typeof c !== 'object' || c === null) return []
    const { url, label } = c as Record<string, unknown>
    return [{ url: typeof url === 'string' ? url : '', label: typeof label === 'string' ? label : '' }]
  })
}

/** credit_limit is a reseller's: the balance may go down to minus it (0, or absent, for everyone else). */
export interface Wallet { currency: string; balance: number; scale: number; credit_limit?: number }
/** Features are what the sections of the app say about this customer (core's miniFeature.me); a section reads its own keys. */
export type Features = Record<string, boolean | number | string | undefined>

export interface Me {
  user: { id: string; username: string; language: string }
  wallets: Wallet[]
  brand: { name: string; color: string; support: string; support_url: string; bot_url: string }
  maintenance: boolean
  top_up: { currency: string; min: number; max: number }
  features: Features
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
  /** the plan group it is listed under (null or absent: none) */
  group?: string | null
}
/** A named group of plans with a text the customer reads before choosing one of its plans. */
export interface PlanGroup {
  id: string
  names: Record<string, string>
  texts: Record<string, string>
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
  /** what the payment shows: the USDT address of an automatic payment; for a manual one the card, the addresses, the link and the payment reference */
  details: {
    pay_address?: string
    network?: string
    expires_at?: string
    reference?: string
    card_number?: string
    card_holder?: string
    usdt_trc20?: string
    usdt_erc20?: string
    zarinpal_link?: string
  }
  /** Telegram Stars: the invoice to open, and its price in Stars */
  invoice_url?: string
  stars?: number
  /** manual USDT: what to send, in micro-USDT (6 decimals) */
  usdt_amount?: number
}
/**
 * PendingPayment is one of the customer's manual payments that still waits for its receipt (GET /payments/pending): what
 * the call that started it answered, so the same payment screen can be drawn again, and when it began (unix seconds).
 */
export interface PendingPayment extends Intent { created_at: number }
/** label names a new service a reseller buys for a customer (core ignores it for anyone else). */
export interface OrderRequest { plan_id: string; type?: string; subscription_id?: string; discount_code?: string; key?: string; label?: string }

let token = ''
let signingIn: Promise<Me> | null = null

async function raw<T>(method: string, path: string, body?: unknown, auth = true): Promise<T> {
  const headers: Record<string, string> = {}
  // A form (a receipt image) goes as it is: the browser writes its Content-Type, with the boundary.
  const form = typeof FormData !== 'undefined' && body instanceof FormData
  if (body !== undefined && !form) headers['Content-Type'] = 'application/json'
  if (auth && token) headers.Authorization = `Bearer ${token}`
  let res: Response
  try {
    res = await fetch(`/api/mini/v1${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : form ? (body as FormData) : JSON.stringify(body),
      cache: 'no-store',
    })
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
    throw new ApiError(res.status, String(data.error ?? 'error'), String(data.message ?? res.statusText), String(data.reason ?? ''), String(data.join_url ?? ''), joinChatsOf(data.join_chats))
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
  /** the plans with the plan groups (an older core sends none) */
  catalog: () =>
    call<{ plans: Plan[]; groups?: PlanGroup[] }>('GET', '/plans').then((r) => ({ plans: r.plans, groups: r.groups ?? [] })),
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
  /** pendingPayments lists the customer's own manual payments that still wait for their receipt, newest first. */
  pendingPayments: () => call<{ payments?: PendingPayment[] }>('GET', '/payments/pending').then((r) => r.payments ?? []),
  /** sendReceipt sends the receipt image of a manual payment, with the bank's tracking number; the payment then waits for staff. */
  sendReceipt: (id: string, receipt: Blob, filename: string, reference: string) => {
    const form = new FormData()
    if (reference) form.append('reference', reference)
    form.append('receipt', receipt, filename)
    return call<{ intent_id: string; status: string }>('POST', `/payments/${id}/proof`, form)
  },
  /** sendTxid sends a crypto payment's transaction hash instead of an image. */
  sendTxid: (id: string, txid: string, network: string) =>
    call<{ intent_id: string; status: string }>('POST', `/payments/${id}/proof`, { txid, network }),
  logout: () => raw<{ ok: boolean }>('POST', '/logout', {}).finally(() => (token = '')),
}

/** resetSession forgets the token (tests). */
export function resetSession(): void {
  token = ''
}
