// What the app shows, loaded from core and kept here: one small reactive
// object (no state library).
import { reactive } from 'vue'
import { api, type LedgerEntry, type Me, type Plan, type Service, type Wallet } from './api'
import { gateFor, type Gate } from './errors'
import { signIn } from './api'
import { initData, telegramLanguage } from './telegram'
import { pickLang, setLang } from './i18n'

export const state = reactive({
  booted: false,
  gate: '' as Gate,
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
  return g !== ''
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
    const me = await signIn()
    state.me = me
    state.wallets = me.wallets
    setLang(pickLang(me.user.language))
    if (me.brand.color) document.documentElement.style.setProperty('--accent', me.brand.color)
    if (me.maintenance) state.gate = 'maintenance'
  } catch (e) {
    if (!fail(e)) state.gate = 'unavailable'
  }
  state.booted = true
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
