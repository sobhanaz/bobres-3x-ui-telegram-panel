// Shapes of core's /api/v1 answers (see internal/core/web).
import type { Money } from './format'

export interface UserBrief {
  id: string
  telegram_id: number
  username: string
}

export interface Page<T> {
  items: T[]
  total: number
  page: number
  size: number
}

export type Names = Record<string, string>

export interface PlanRef {
  id: string
  name: Names
}

export interface UserItem {
  id: string
  telegram_id: number
  username: string
  language: string
  role: string
  status: string
  created_at: number
  ref_code?: string
  wallets: Money[]
  subscriptions?: number
  orders?: number
}

export interface UserDetail {
  user: UserItem
  counts: { subscriptions: number; orders: number }
  referral: { referred_by: UserBrief | null; invited: number; rewarded: number; earned: Money[] }
  can_ban: boolean
}

export interface ServiceItem {
  id: string
  user: UserBrief
  plan: PlanRef
  status: string
  client_email: string
  sub_link: string
  expires_at: number | null
  traffic_total: number | null
  traffic_used: number
  last_synced_at: number | null
  created_at: number
}

export interface OrderItem {
  id: string
  user: UserBrief
  plan: PlanRef
  type: string
  status: string
  amount: Money
  discount: { code: string; amount: Money } | null
  subscription_id: string | null
  created_at: number
  updated_at: number
  attempts: number
  last_error: string
  next_attempt_at: number | null
  staff: string
  extend: { days: number | null; bytes: number | null } | null
  refund: { amount: Money; at: number } | null
}

export interface Proof {
  has_file: boolean
  reference: string
  network: string
  txid: string
  submitted_at: number
  decision: string
  reason: string
  reviewed_by: UserBrief | null
  reviewed_at: number | null
}

export interface PaymentItem {
  id: string
  order_id: string
  user: UserBrief | null
  provider: string
  status: string
  amount: Money
  gateway_amount: Money | null
  provider_ref: string
  failure_reason: string
  created_at: number
  proof: Proof | null
}

export interface PendingItem {
  id: string
  order_id: string
  user: UserBrief | null
  provider: string
  amount: Money
  reference: string
  network: string
  txid: string
  has_file: boolean
  submitted_at: number
  possible_duplicate: boolean
}

export interface LedgerItem {
  id: string
  user: UserBrief
  amount: Money
  balance_after: Money
  kind: string
  ref_type: string
  ref_id: string
  created_at: number
}

export interface PlanItem {
  id: string
  name: Names
  kind: string
  duration_days: number | null
  traffic_bytes: number | null
  price: Money
  enabled: boolean
  is_trial: boolean
  is_topup: boolean
  sort: number
  sales: number
}

export interface DiscountItem {
  code: string
  percent: number | null
  amount: Money | null
  max_uses: number | null
  used: number
  expires_at: number | null
  enabled: boolean
}

export interface ReferrerItem {
  user: UserBrief
  invited: number
  rewarded: number
  earned: Money[]
}
