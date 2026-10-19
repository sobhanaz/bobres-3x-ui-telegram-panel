import { call, type Order } from '../../api'

export interface TrialOffer {
  /** the customer can take it now */
  available: boolean
  /** they took it already (here or in the bot) */
  used: boolean
  plan?: { names: Record<string, string>; kind: 'traffic' | 'time' | 'both'; days: number | null; traffic_bytes: number | null; currency: string; scale: number }
}

export const trialApi = {
  get: () => call<TrialOffer>('GET', '/trial'),
  start: () => call<Order>('POST', '/trial', {}),
}
