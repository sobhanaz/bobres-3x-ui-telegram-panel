// What the app shows, loaded from core and kept here: one small reactive
// object (no state library).
import { reactive } from 'vue'
import { api, ApiError, type LedgerEntry, type Me, type Plan, type Service, type Wallet } from './api'
import { errorKey, gateFor, type Gate } from './errors'
import { signIn } from './api'
import { initData, telegramLanguage } from './telegram'
import { pickLang, setLang, type Key } from './i18n'

export const state = reactive({
  booted: false,
  gate: '' as Gate,
  /** the gate join_required: where to join the store's channel ("" when the store gave no link) */
  joinUrl: '',
  me: null as Me | null,
  plans: null as Plan[] | null,
  services: null as Service[] | null,
  wallets: [] as Wallet[],
  ledger: [] as LedgerEntry[],
})

/** fail turns a failure into a full-screen gate when it is one; returns whether it was. */
export function fail(e: unknown): boolean {
  const g = gateFor(e)
  if (g) state.gate = g
  if (g === 'join_required') state.joinUrl = e instanceof ApiError ? e.joinUrl : ''
  return g !== ''
}

/** applyMe takes in who the customer is, as a sign-in answers. */
function applyMe(me: Me): void {
  state.me = me
  state.wallets = me.wallets
  setLang(pickLang(me.user.language))
  if (me.brand.color) document.documentElement.style.setProperty('--accent', me.brand.color)
  if (me.maintenance) state.gate = 'maintenance'
}

/** boot signs in and loads who the customer is. */
export async function boot(): Promise<void> {
  setLang(pickLang(telegramLanguage()))
  if (!initData()) {
    state.gate = 'not_telegram'
    state.booted = true
    return
  }
  try {
    applyMe(await signIn())
  } catch (e) {
    if (!fail(e)) state.gate = 'unavailable'
  }
  state.booted = true
}

/**
 * recheckJoin is the "I've joined" button of the channel gate: a new sign-in, which asks the bot at once (core never
 * remembers a customer who is outside the channel). It clears the gate and returns "" when the customer is in; else the
 * text to show under the button: not yet, or why the check could not be made. Another gate (maintenance, banned) takes
 * the screen over by itself.
 */
export async function recheckJoin(): Promise<Key | ''> {
  try {
    const me = await signIn()
    state.gate = ''
    state.joinUrl = ''
    applyMe(me)
    return ''
  } catch (e) {
    if (gateFor(e) === 'join_required') {
      fail(e)
      return 'join.not_yet'
    }
    return fail(e) ? '' : errorKey(e)
  }
}

export async function loadPlans(force = false): Promise<void> {
  if (state.plans && !force) return
  state.plans = await api.plans()
}

export async function loadServices(): Promise<void> {
  state.services = await api.services()
}

export async function loadWallet(): Promise<void> {
  const w = await api.wallet()
  state.wallets = w.wallets
  state.ledger = w.ledger
}

export function irtBalance(): number {
  return state.wallets.find((w) => w.currency === 'IRT')?.balance ?? 0
}

/** irtSpendable is what the wallet can pay: the balance plus a reseller's credit (0 for everyone else). */
export function irtSpendable(): number {
  const w = state.wallets.find((x) => x.currency === 'IRT')
  return (w?.balance ?? 0) + (w?.credit_limit ?? 0)
}
