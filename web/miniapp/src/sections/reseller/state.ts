// Where the reseller is in the section. It lives here, not in the views,
// because buying, renewing and adding traffic leave the section for the
// shop's checkout and come back: the reseller finds the screen they left.
import { reactive } from 'vue'
import type { Filter } from './api'

export type Tab = 'panel' | 'buy' | 'services' | 'customers' | 'statement'
export const tabs: Tab[] = ['panel', 'buy', 'services', 'customers', 'statement']

export const ui = reactive({
  tab: 'panel' as Tab,
  filter: '' as Filter,
  query: '',
  page: 1,
  /** the service whose actions are open */
  open: '',
})

/** resetUi starts the section over (tests, and a reseller who signs out). */
export function resetUi(): void {
  ui.tab = 'panel'
  ui.filter = ''
  ui.query = ''
  ui.page = 1
  ui.open = ''
}

/** showServices opens the services tab on a group. */
export function showServices(filter: Filter = ''): void {
  ui.tab = 'services'
  ui.filter = filter
  ui.query = ''
  ui.page = 1
  ui.open = ''
}
