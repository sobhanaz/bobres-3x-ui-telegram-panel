import { describe, expect, it } from 'vitest'
import { messages, pickLang, planName, translate } from '../i18n'

describe('i18n', () => {
  it('has the same keys in both languages and no empty text', () => {
    expect(Object.keys(messages.en).sort()).toEqual(Object.keys(messages.fa).sort())
    for (const l of ['fa', 'en'] as const) {
      for (const [k, v] of Object.entries(messages[l])) expect(v, `${l}.${k}`).not.toBe('')
    }
  })

  it('uses the same placeholders in both languages', () => {
    const names = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()
    for (const k of Object.keys(messages.en) as (keyof typeof messages.en)[]) {
      expect(names(messages.fa[k]), k).toEqual(names(messages.en[k]))
    }
  })

  it('fills placeholders', () => {
    expect(translate('en', 'plans.days', { n: 30 })).toBe('30 days')
    expect(translate('fa', 'plans.days', { n: '۳۰' })).toBe('۳۰ روز')
  })

  it('picks Persian unless English is asked for', () => {
    expect(pickLang('en')).toBe('en')
    expect(pickLang('en-US')).toBe('en')
    expect(pickLang('fa')).toBe('fa')
    expect(pickLang('ru')).toBe('fa')
    expect(pickLang(undefined)).toBe('fa')
  })

  it('names a plan in the language with fallbacks', () => {
    expect(planName({ fa: 'ماهانه', en: 'Monthly' }, 'fa')).toBe('ماهانه')
    expect(planName({ en: 'Monthly' }, 'fa')).toBe('Monthly')
    expect(planName({ de: 'Monat' }, 'en')).toBe('Monat')
  })
})
