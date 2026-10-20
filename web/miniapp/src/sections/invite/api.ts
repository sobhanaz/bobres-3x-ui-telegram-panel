import { call } from '../../api'

export interface Invite {
  /** the customer's own link to the bot */
  link: string
  /** the share of an invited friend's first purchase credited to the customer */
  percent: number
  invited: number
  /** how many of them bought */
  rewarded: number
  earned: { currency: string; amount: number; scale: number }[]
}

export const inviteApi = {
  get: () => call<Invite>('GET', '/invite'),
}
