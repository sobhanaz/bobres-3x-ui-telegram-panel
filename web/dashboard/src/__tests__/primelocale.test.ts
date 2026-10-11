import { describe, expect, it } from 'vitest'
import { primeLocale } from '../primelocale'

// Strings a PrimeVue upgrade adds come over in English; this lists them.
function untranslated(fa: Record<string, unknown>, en: Record<string, unknown>): string[] {
  return Object.keys(en).filter((k) => typeof en[k] === 'string' && fa[k] === en[k])
}

describe('primeLocale', () => {
  it('has every PrimeVue word in Persian', () => {
    const en = primeLocale('en') as unknown as Record<string, unknown>
    const fa = primeLocale('fa') as unknown as Record<string, unknown>
    expect(Object.keys(fa).sort()).toEqual(Object.keys(en).sort())
    expect(untranslated(fa, en)).toEqual(['dateFormat'])
    const enAria = en.aria as Record<string, unknown>
    const faAria = fa.aria as Record<string, unknown>
    expect(Object.keys(faAria).sort()).toEqual(Object.keys(enAria).sort())
    expect(untranslated(faAria, enAria)).toEqual(['slideNumber'])
    expect(fa.accept).toBe('بله')
    expect(en.accept).toBe('Yes')
  })
})
