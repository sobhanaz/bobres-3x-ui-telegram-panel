import { describe, expect, it } from 'vitest'
import { blankLicense, dateOk, daysLeft, editBody, keyOk, newLicenseBody, newLicenseProblem, requestId, statusSeverity, grantMonths } from '../vendor'
import fa from '../locales/fa.json'
import en from '../locales/en.json'

describe('the Licenses page rules', () => {
  it('takes 32 bytes of base64 as the admin key', () => {
    expect(keyOk('A'.repeat(43))).toBe(true)
    expect(keyOk('abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ-_')).toBe(false) // 45 characters
    expect(keyOk('A'.repeat(42))).toBe(false)
    expect(keyOk('')).toBe(false)
    expect(keyOk('A'.repeat(43) + '!')).toBe(false)
  })
  it('makes a fresh request id per click, in the form core takes (8 to 40 letters and digits)', () => {
    const a = requestId()
    expect(a).toMatch(/^[0-9a-f]{24}$/)
    expect(requestId()).not.toBe(a)
  })
  it('colors a status and offers the months core accepts', () => {
    expect(statusSeverity('active')).toBe('success')
    expect(statusSeverity('revoked')).toBe('danger')
    for (const m of grantMonths) expect(m >= 1 && m <= 36).toBe(true)
  })
  it('has every text in both languages', () => {
    const keys = ['intro', 'no_key', 'server', 'key', 'save', 'turn_on', 'turn_off', 'none', 'buyer', 'give', 'revoke', 'reason']
    for (const cat of [fa, en] as Record<string, any>[]) {
      for (const k of keys) expect(cat.licenses[k], k).toBeTruthy()
      expect(cat.licenses.tier.standard && cat.licenses.tier.pro).toBeTruthy()
      expect(cat.licenses.status.active && cat.licenses.status.revoked).toBeTruthy()
    }
  })
})

describe('making and changing every license by hand', () => {
  const now = new Date('2026-10-21T09:00:00Z')
  it('takes a real Gregorian date after today and within ten years', () => {
    for (const ok of ['2026-10-22', '2027-02-28', '2036-10-21']) expect(dateOk(ok, now), ok).toBe(true)
    for (const bad of ['', '2026-10-21', '2020-01-01', '2036-10-22', '2027-02-30', '2027-13-01', '27-01-01', 'next year', '2027/01/01'])
      expect(dateOk(bad, now), bad).toBe(false)
  })
  it('names what is wrong with the new-license form', () => {
    const f = { ...blankLicense(), customer: 'Acme' }
    expect(newLicenseProblem(f, now)).toBe('')
    expect(newLicenseProblem({ ...f, customer: '  ' }, now)).toBe('customer')
    expect(newLicenseProblem({ ...f, customer: 'a\nb' }, now)).toBe('customer')
    expect(newLicenseProblem({ ...f, customer: 'x'.repeat(201) }, now)).toBe('customer')
    expect(newLicenseProblem({ ...f, mode: 'date', date: '2020-01-01' }, now)).toBe('date')
    expect(newLicenseProblem({ ...f, mode: 'default' }, now)).toBe('default') // only a trial has days of its own
    expect(newLicenseProblem({ ...f, tier: 'trial', mode: 'default' }, now)).toBe('')
  })
  it('sends exactly one way to say when a license ends', () => {
    const f = { ...blankLicense(), customer: ' Acme ' }
    expect(newLicenseBody(f, 'rid-0001')).toEqual({ customer: 'Acme', tier: 'standard', months: 12, request_id: 'rid-0001' })
    expect(newLicenseBody({ ...f, mode: 'date', date: ' 2027-05-01 ' }, 'rid-0001')).toEqual({ customer: 'Acme', tier: 'standard', ends_at: '2027-05-01', request_id: 'rid-0001' })
    expect(newLicenseBody({ ...f, mode: 'none' }, 'rid-0001')).toEqual({ customer: 'Acme', tier: 'standard', no_end: true, request_id: 'rid-0001' })
    expect(newLicenseBody({ ...f, tier: 'trial', mode: 'default' }, 'rid-0001')).toEqual({ customer: 'Acme', tier: 'trial', request_id: 'rid-0001' })
  })
  it('sends only what an edit changed, and nothing for a click that changes nothing', () => {
    const orig = { customer: 'Acme', tier: 'standard' as const }
    const same = { customer: 'Acme', tier: 'standard' as const, mode: 'keep' as const, date: '' }
    expect(editBody(orig, same, 'rid-0002')).toBeNull()
    expect(editBody(orig, { ...same, customer: ' New ' }, 'rid-0002')).toEqual({ request_id: 'rid-0002', customer: 'New' })
    expect(editBody(orig, { ...same, tier: 'pro', mode: 'none' }, 'rid-0002')).toEqual({ request_id: 'rid-0002', tier: 'pro', no_end: true })
    expect(editBody(orig, { ...same, mode: 'date', date: '2027-05-01' }, 'rid-0002')).toEqual({ request_id: 'rid-0002', ends_at: '2027-05-01' })
  })
  it('counts the days left to an end, and none for no end', () => {
    const t0 = Date.parse('2026-10-21T09:00:00Z')
    expect(daysLeft(null, t0)).toBeNull()
    expect(daysLeft(t0 / 1000 + 10 * 86400 + 60, t0)).toBe(10)
    expect(daysLeft(t0 / 1000 - 3 * 86400 - 60, t0)).toBe(-4)
  })
  it('has the new texts in both languages', () => {
    const keys = ['tab_all', 'tab_sold', 'all_intro', 'new', 'none_all', 'customer', 'runs_on', 'details', 'make', 'key_once', 'key_once_help', 'rekey', 'history', 'grants']
    for (const cat of [fa, en] as Record<string, any>[]) {
      for (const k of keys) expect(cat.licenses[k], k).toBeTruthy()
      for (const k of ['months', 'date', 'none', 'default']) expect(cat.licenses.mode[k], 'mode.' + k).toBeTruthy()
      for (const k of ['title', 'keep', 'tier_help', 'extend', 'extend_go']) expect(cat.licenses.edit[k], 'edit.' + k).toBeTruthy()
    }
  })
})

