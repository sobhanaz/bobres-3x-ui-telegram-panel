import { describe, expect, it } from 'vitest'
import { addressOk, keyOk, requestId, statusSeverity, grantMonths } from '../vendor'
import fa from '../locales/fa.json'
import en from '../locales/en.json'

describe('the Licenses page rules', () => {
  it('takes only an https address', () => {
    expect(addressOk('https://license.example.com')).toBe(true)
    expect(addressOk(' https://license.example.com/ ')).toBe(true)
    for (const bad of ['', 'http://license.example.com', 'license.example.com', 'https://', 'https://a b', 'ftp://x.example'])
      expect(addressOk(bad), bad).toBe(false)
    expect(addressOk('https://' + 'a'.repeat(200))).toBe(false)
  })
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
    const keys = ['intro', 'no_key', 'server', 'url', 'key', 'save', 'turn_on', 'turn_off', 'none', 'buyer', 'give', 'revoke', 'reason']
    for (const cat of [fa, en] as Record<string, any>[]) {
      for (const k of keys) expect(cat.licenses[k], k).toBeTruthy()
      expect(cat.licenses.tier.standard && cat.licenses.tier.pro).toBeTruthy()
      expect(cat.licenses.status.active && cat.licenses.status.revoked).toBeTruthy()
    }
  })
})
