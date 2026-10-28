import { call } from '../../api'

/** One entry of the customer's own list: a discount (reward) code issued to them, or a gift code they applied. */
export interface Code {
  /** unique in the list: a discount code's own text, or the id of the gift redemption */
  id: string
  kind: 'reward' | 'gift'
  /** a discount code, whole (absent for a gift code: the store never keeps one) */
  code?: string
  /** a gift code: the last characters of the one the customer applied */
  tail?: string
  /** a gift code the customer applied is always used */
  state: 'unused' | 'used' | 'expired' | 'off'
  /** a discount code takes a percent off ... */
  percent?: number
  /** ... or an amount (minor units of its currency); for a gift code, what it added to the wallet */
  amount?: number
  currency?: string
  scale: number
  /** unix seconds: when a discount code ends (null: never) */
  expires_at: number | null
  /** when a discount code was used, or the gift code applied (null: not yet) */
  used_at: number | null
  /** when the customer received it */
  received_at: number
  /** the plan whose purchase gave a discount code, by language */
  plan?: Record<string, string>
  /** a code made before discount codes were for any plan works on this one plan only */
  only_plan?: Record<string, string>
}

export interface CodesPage {
  codes: Code[]
  total: number
  page: number
  size: number
}

export const codesApi = {
  page: (page: number, size: number) => call<CodesPage>('GET', `/codes?page=${page}&size=${size}`),
}
