// Small helpers of the payment screens (Stars and the manual methods).
import type { Key } from './i18n'

/** isManual says whether a payment method is one the customer pays by hand and then sends a proof for. */
export function isManual(provider: string): boolean {
  return provider === 'manual_card' || provider === 'manual_crypto' || provider === 'manual_zarinpal'
}

/** shortMethodKey is the short name of a manual method, for a row of a list (the full names run on: "USDT, then send a receipt"). */
export function shortMethodKey(provider: string): Key {
  switch (provider) {
    case 'manual_card':
      return 'wallet.pending_card'
    case 'manual_crypto':
      return 'wallet.pending_crypto'
    default:
      return 'wallet.pending_link'
  }
}

/**
 * notYetKey is what is said when the customer asks about a payment and it is not there yet: a Stars payment may be
 * on its way from Telegram (so: do not pay twice), a manual one waits for the staff.
 */
export function notYetKey(provider: string): Key {
  if (provider === 'stars') return 'stars.not_yet'
  return isManual(provider) ? 'manual.still_waiting' : 'pay.not_yet'
}

/** cardDigits is a card number as a bank app takes it: digits only (an IR sheba keeps its letters). */
export function cardDigits(card: string): string {
  return card.replace(/[\s-]/g, '')
}

/** plainAmount writes an amount in minor units as a plain decimal a wallet or bank app takes: 2500000 (scale 6) is "2.5". */
export function plainAmount(minor: number, scale: number): string {
  if (scale <= 0) return String(minor)
  const unit = 10 ** scale
  const whole = Math.trunc(minor / unit)
  const frac = String(minor % unit).padStart(scale, '0').replace(/0+$/, '')
  return frac ? `${whole}.${frac}` : String(whole)
}

/** txidPattern is a transaction hash as the server takes it: 64 hexadecimal characters, 0x in front for Ethereum. */
export const txidPattern = /^(0x)?[0-9a-fA-F]{64}$/

/** cleanTxid removes the spaces a pasted hash picks up. */
export function cleanTxid(s: string): string {
  return s.replace(/\s+/g, '')
}

/** Networks a manual USDT payment can be sent on, in the order they are shown. */
export type Network = 'TRC20' | 'ERC20'

/** networksOf lists the networks the store has an address for. */
export function networksOf(details: { usdt_trc20?: string; usdt_erc20?: string }): Network[] {
  const out: Network[] = []
  if (details.usdt_trc20) out.push('TRC20')
  if (details.usdt_erc20) out.push('ERC20')
  return out
}

/** networkFor is the network a hash is for, when the customer has not said: a hash with 0x is Ethereum's, else TRON's. */
export function networkFor(txid: string, available: Network[]): Network | '' {
  const preferred: Network = txid.startsWith('0x') ? 'ERC20' : 'TRC20'
  return available.includes(preferred) ? preferred : (available[0] ?? '')
}
