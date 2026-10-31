import { call } from '../../api'

export const newsApi = {
  /** set stores the customer's answer: a private message when the store announces a new product, or none */
  set: (optIn: boolean) => call<{ opt_in: boolean }>('PUT', '/news', { opt_in: optIn }),
}
