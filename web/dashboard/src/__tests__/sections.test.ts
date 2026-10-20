import { describe, expect, it } from 'vitest'
import { sections, visibleSections } from '../sections'
import fa from '../locales/fa.json'
import en from '../locales/en.json'

describe('sections', () => {
  it('lists all 19 dashboard sections once', () => {
    expect(sections).toHaveLength(19)
    expect(new Set(sections.map((s) => s.key)).size).toBe(19)
  })
  it('puts Gift codes beside Discounts, for the staff who hold discounts.write', () => {
    expect(sections.find((s) => s.key === 'giftcodes')).toMatchObject({ path: 'gift-codes', perm: 'discounts.write', group: 'sales' })
    const keys = sections.map((s) => s.key)
    expect(keys.indexOf('giftcodes')).toBe(keys.indexOf('discounts') + 1)
    const support = ['overview.read', 'users.read', 'services.read', 'services.extend', 'payments.read', 'tickets.write']
    expect(visibleSections(support).map((s) => s.key)).not.toContain('giftcodes')
    expect(visibleSections([...support, 'discounts.write']).map((s) => s.key)).toEqual(expect.arrayContaining(['discounts', 'giftcodes']))
  })
  it('shows only what the role may open', () => {
    const support = ['overview.read', 'users.read', 'services.read', 'services.extend', 'payments.read', 'tickets.write']
    expect(visibleSections(support).map((s) => s.key)).toEqual(['overview', 'users', 'services', 'support', 'payments'])
  })
  it('opens Reports only to staff with reports.read (revenue is not for support)', () => {
    const support = ['overview.read', 'users.read', 'services.read', 'services.extend', 'payments.read', 'tickets.write']
    expect(visibleSections(support).map((s) => s.key)).not.toContain('reports')
    expect(visibleSections([...support, 'reports.read']).map((s) => s.key)).toContain('reports')
    expect(sections.find((s) => s.key === 'reports')?.perm).toBe('reports.read')
  })
  it('has a title and description in both languages', () => {
    for (const s of sections) {
      for (const cat of [fa, en] as Record<string, any>[]) {
        expect(cat.section[s.key]?.title, s.key).toBeTruthy()
        expect(cat.section[s.key]?.desc, s.key).toBeTruthy()
      }
    }
  })
})

// Every text key exists in both catalogs.
function keys(o: Record<string, unknown>, prefix = ''): string[] {
  return Object.entries(o).flatMap(([k, v]) =>
    v && typeof v === 'object' ? keys(v as Record<string, unknown>, prefix + k + '.') : [prefix + k],
  )
}
describe('catalogs', () => {
  it('have the same keys', () => {
    expect(keys(fa).sort()).toEqual(keys(en).sort())
  })
})

// Every text compiles in vue-i18n's message syntax ("@", "|", "{", "}" and
// "$" are syntax there): a broken one throws while rendering and freezes
// the page in the language it had.
describe('catalog syntax', () => {
  it('compiles every message in both languages', async () => {
    const { createI18n } = await import('vue-i18n')
    for (const [lang, cat] of [['fa', fa], ['en', en]] as const) {
      const i18n = createI18n({ legacy: false, locale: lang, messages: { [lang]: cat }, missingWarn: false, fallbackWarn: false })
      for (const k of keys(cat)) {
        expect(() => i18n.global.t(k, { n: 1, amount: 'x', balance: 'x', who: 'x', used: 'x', total: 'x', max: 'x', date: 'x', time: 'x', day: 1, week: 1, expiring: 1, ended: 1, brand: 'x', username: 'x', id: 'x', fallback: 'x', ratio: 'x', size: 'x', lang: 'x', key: 'x', file: 'x', name: 'x', arg: 'x', code: 'x', entity: 'x', tags: 'x', chars: 'x', entities: 'x', role: 'x', browser: 'x', os: 'x', device: 'x', group: 'x', def: 'x', min: 'x', title: 'x', sent: 'x', blocked: 'x', failed: 'x', done: 'x', zone: 'x', fa: 'x', en: 'x' }), `${lang}: ${k}`).not.toThrow()
      }
    }
  })
})
