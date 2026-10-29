import { call } from '../../api'

export interface WheelPrize {
  /** Toman */
  amount: number
  /** its share of the chances, out of total_weight */
  weight: number
}

/** The wheel as core shows it to this customer (GET /wheel). */
export interface Wheel {
  /** the store-local day, "2026-10-28" */
  day: string
  /** unix seconds: when the next spin is possible */
  resets_at: number
  /** why this customer may not spin: "orders" (not enough paid orders), "role"; empty when they may */
  reason: '' | 'orders' | 'role'
  orders: number
  min_orders: number
  /** the customer has spun today, and won this much (0: no prize) */
  spun: boolean
  prize: number
  /** what is left of the day's budget still covers the smallest prize */
  open: boolean
  /** smallest first: the order of the pieces */
  prizes: WheelPrize[]
  /** the weight of the piece that pays nothing, after the prizes */
  no_prize_weight: number
  total_weight: number
  currency: string
  scale: number
}

/**
 * Where a spin ended (POST /wheel/spin). won and lost used the day's spin; already is the earlier spin of the day; closed is a prize the day's budget
 * cannot cover, which used no spin; orders and role are refusals.
 */
export type SpinStatus = 'won' | 'lost' | 'already' | 'closed' | 'orders' | 'role'

export interface Spin {
  status: SpinStatus
  prize: number
  /** the place of the prize on the wheel; -1 for no prize */
  index: number
  /** the wallet now, in Toman */
  balance: number
  day: string
  resets_at: number
  orders: number
  min_orders: number
  currency: string
  scale: number
}

export const wheelApi = {
  get: () => call<Wheel>('GET', '/wheel'),
  spin: () => call<Spin>('POST', '/wheel/spin'),
}
