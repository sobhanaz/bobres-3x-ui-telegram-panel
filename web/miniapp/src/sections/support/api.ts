import { call } from '../../api'

export interface Ticket {
  id: string
  number: number
  category: string
  subject: string
  /** open: waiting for staff; answered: staff wrote last; closed */
  status: 'open' | 'answered' | 'closed'
  created_at: number
  updated_at: number
  last_staff_at?: number
}

export interface TicketMessage {
  id: string
  kind: 'customer' | 'staff'
  body: string
  /** a photo or file was sent (it is shown in the bot) */
  has_file: boolean
  at: number
}

export interface Overview {
  open: Ticket | null
  tickets: Ticket[]
  categories: string[]
}

export const supportApi = {
  overview: () => call<Overview>('GET', '/support'),
  thread: (id: string) => call<{ ticket: Ticket; messages: TicketMessage[] }>('GET', `/support/tickets/${id}`),
  send: (text: string, category: string) =>
    call<{ ticket: Ticket; message_id: string; new_ticket: boolean }>('POST', '/support/messages', { text, category }),
  close: (id: string) => call<{ ticket: Ticket }>('POST', `/support/tickets/${id}/close`, {}),
}
