import { describe, expect, it } from 'vitest'
import { openAmount, payoutProblem, percentValue, reversedState, wholeToman } from '../affiliates'
import { domainOk, looksLikeRegistration, newest, older, stateOf, stateSeverity, tokenOk } from '../hosted'
import fa from '../locales/fa.json'
import en from '../locales/en.json'

const toman = (amount: number) => ({ amount, currency: 'IRT' })

describe('the Affiliates page rules', () => {
  it('pays out at most what was earned and not paid, from the minimum, within the wallet', () => {
    const a = { earned: toman(80_000), paid: toman(30_000), balance: toman(60_000) }
    expect(openAmount(a)).toBe(50_000)
    expect(openAmount({ earned: toman(10), paid: toman(20) })).toBe(0)
    expect(payoutProblem(50_000, a, 20_000)).toBe('')
    expect(payoutProblem(10_000, a, 20_000)).toBe('min')
    expect(payoutProblem(50_001, a, 0)).toBe('open')
    expect(payoutProblem(0, a, 0)).toBe('amount')
    expect(payoutProblem(null, a, 0)).toBe('amount')
    expect(payoutProblem(50_000, { ...a, balance: toman(40_000) }, 0)).toBe('wallet')
  })
  it('reads whole Toman and percents as typed, Persian digits too', () => {
    expect(wholeToman('۵۰٬۰۰۰')).toBe(50_000)
    expect(wholeToman('1,000')).toBe(1000)
    expect(wholeToman('12.5')).toBeNull()
    expect(wholeToman('')).toBeNull()
    expect(percentValue('')).toBeNull()
    expect(percentValue('۲۵')).toBe(25)
    expect(percentValue('0')).toBe(0)
    expect(percentValue('101')).toBe('bad')
    expect(percentValue('x')).toBe('bad')
  })
  it('says what a refund took back', () => {
    expect(reversedState({ reversed: null, amount: toman(10) })).toBe('')
    expect(reversedState({ reversed: toman(10), amount: toman(10) })).toBe('all')
    expect(reversedState({ reversed: toman(4), amount: toman(10) })).toBe('part')
  })
})

describe('the Hosted stores page rules', () => {
  it('knows a registration code, a domain and a token as core does', () => {
    expect(looksLikeRegistration('  bobres-hosted:v1:\neyJ')).toBe(true)
    expect(looksLikeRegistration('shop.example.com')).toBe(false)
    expect(domainOk('Shop.Example.com')).toBe(true)
    for (const bad of ['localhost', '10.0.0.1', 'a.example.com:443', 'https://a.example.com', '']) expect(domainOk(bad), bad).toBe(false)
    expect(tokenOk('bm_' + 'a'.repeat(43))).toBe(true)
    expect(tokenOk('short')).toBe(false)
  })
  it('tells a store at a glance', () => {
    expect(stateOf({ up: null, checked_at: null, figures: {} })).toBe('unknown')
    expect(stateOf({ up: false, checked_at: 1, figures: {} })).toBe('down')
    expect(stateOf({ up: true, checked_at: 1, figures: { services_up: 5, services_total: 5 } })).toBe('up')
    expect(stateOf({ up: true, checked_at: 1, figures: { services_up: 4, services_total: 5 } })).toBe('warn')
    expect(stateOf({ up: true, checked_at: 1, figures: { services_up: 5, services_total: 5, alerts_critical: 1 } })).toBe('warn')
    expect(stateOf({ up: true, checked_at: 1, figures: { services_up: 5, services_total: 5, license_days_left: 3 } })).toBe('warn')
    expect(stateSeverity('down')).toBe('danger')
  })
  it('finds the stores that run an older version', () => {
    expect(older('0.19.2', '0.20.0')).toBe(true)
    expect(older('0.20.0', '0.20.0')).toBe(false)
    expect(older('v0.20.10', '0.20.9')).toBe(false)
    expect(newest([{ last_version: '0.19.0' }, { last_version: '' }, { last_version: '0.20.1' }])).toBe('0.20.1')
    expect(newest([])).toBe('')
  })
})

describe('the texts', () => {
  it('has the pages\' texts in both languages', () => {
    for (const cat of [fa, en] as Record<string, any>[]) {
      for (const k of ['intro', 'program', 'enabled', 'percent', 'min_payout', 'rules', 'pay', 'commissions', 'payouts']) expect(cat.affiliates[k], k).toBeTruthy()
      for (const p of ['amount', 'min', 'open', 'wallet']) expect(cat.affiliates.problem[p], p).toBeTruthy()
      for (const k of ['add', 'intro', 'custody', 'registration', 'token', 'make_license', 'key_once']) expect(cat.hosted[k], k).toBeTruthy()
      for (const s of ['up', 'warn', 'down', 'unknown']) expect(cat.hosted.states[s], s).toBeTruthy()
      for (const k of ['affiliate_commission', 'affiliate_reversal', 'affiliate_payout']) expect(cat.ledger.kind[k], k).toBeTruthy()
      expect(cat.licenses.refund && cat.licenses.refund_help && cat.licenses.refunded).toBeTruthy()
    }
  })
})
