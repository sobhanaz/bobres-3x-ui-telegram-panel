import { describe, expect, it } from 'vitest'
import { ledgerKey } from '../fmt'
import { messages } from '../i18n'

describe('ledgerKey', () => {
  it('names a gift code credit in both languages, not as a plain movement', () => {
    expect(ledgerKey('gift_code')).toBe('ledger.gift_code')
    expect(messages.fa['ledger.gift_code']).toBeTruthy()
    expect(messages.en['ledger.gift_code']).toBe('Gift code')
  })

  it('keeps the kinds it already named', () => {
    expect(ledgerKey('topup')).toBe('ledger.topup')
    expect(ledgerKey('purchase')).toBe('ledger.purchase')
    expect(ledgerKey('refund')).toBe('ledger.refund')
    expect(ledgerKey('adjust')).toBe('ledger.adjustment')
    expect(ledgerKey('referral_reward')).toBe('ledger.other')
    // the sales of licenses and digital accounts and the conversion of USDT to Toman have names of their own
    for (const [kind, key] of [['license_purchase', 'ledger.license'], ['license_refund', 'ledger.license'], ['supplier_purchase', 'ledger.digital'],
      ['supplier_refund', 'ledger.digital'], ['wallet_convert', 'ledger.convert']] as const) {
      expect(ledgerKey(kind)).toBe(key)
      expect(messages.fa[key]).toBeTruthy()
      expect(messages.en[key]).toBeTruthy()
    }
  })
})
