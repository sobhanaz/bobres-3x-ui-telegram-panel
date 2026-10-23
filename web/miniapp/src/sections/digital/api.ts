import { call, type Wallet } from '../../api'

export interface Money { amount: number; currency: string; scale: number }

export interface DigitalItem {
  /** the short id the shop uses for the product */
  sid: string
  names: Record<string, string>
  descriptions: Record<string, string>
  /** the supplier's category ("" when it names none) */
  category: string
  price: number
  sold_out: boolean
  /** how many the supplier has (0 and stock_known false: it does not say) */
  stock: number
  stock_known: boolean
}

export interface DigitalShelf {
  items: DigitalItem[]
  wallet: Wallet | null
  currency: string
  scale: number
  /** the most one purchase takes */
  max_quantity: number
}

/** paid: the wallet was charged and the supplier has not delivered yet; done: delivered; refunded: cancelled and paid back. */
export type DigitalStatus = 'paid' | 'done' | 'refunded'

export interface DigitalOrder {
  id: string
  names: Record<string, string>
  quantity: number
  total: Money
  status: DigitalStatus
  at: number
  /** the account(s) bought; only in the answer for one order or a purchase just made, "" until delivered */
  delivery?: string
  replayed?: boolean
}

export const digitalApi = {
  shelf: () => call<DigitalShelf>('GET', '/digital'),
  /** buy charges the wallet once per key; sending the same key again returns the first result. */
  buy: (req: { sid: string; quantity: number; expected_total: number; key: string }) => call<DigitalOrder>('POST', '/digital/buy', req),
  orders: () => call<{ orders: DigitalOrder[] }>('GET', '/digital/orders').then((r) => r.orders),
  order: (id: string) => call<DigitalOrder>('GET', `/digital/orders/${encodeURIComponent(id)}`),
}
