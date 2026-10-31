import { describe, expect, it } from 'vitest'
import { sections, visibleSections } from '../sections'
import fa from '../locales/fa.json'
import en from '../locales/en.json'
import ar from '../locales/ar.json'
import ru from '../locales/ru.json'
import tr from '../locales/tr.json'
import { pluralRules } from '../i18n'

describe('sections', () => {
  it('lists all 21 dashboard sections once', () => {
    expect(sections).toHaveLength(21)
    expect(new Set(sections.map((s) => s.key)).size).toBe(21)
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
  it('shows the vendor\'s own sections only to an owner whose license grants them', () => {
    const owner = ['overview.read', 'license.write', 'gateways.write', 'discounts.write']
    expect(visibleSections(owner).map((s) => s.key)).not.toContain('licenses')
    expect(visibleSections(owner).map((s) => s.key)).not.toContain('supplier')
    expect(visibleSections([...owner, 'private.features']).map((s) => s.key)).toEqual(expect.arrayContaining(['licenses', 'supplier']))
    // the feature without the permission opens nothing
    expect(visibleSections(['overview.read', 'private.features']).map((s) => s.key)).not.toContain('licenses')
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
  it('have the same keys, in all five languages', () => {
    for (const cat of [fa, ar, ru, tr]) expect(keys(cat).sort()).toEqual(keys(en).sort())
  })
})

// Every text compiles in vue-i18n's message syntax ("@", "|", "{", "}" and
// "$" are syntax there): a broken one throws while rendering and freezes
// the page in the language it had.
describe('catalog syntax', () => {
  it('compiles every message in all five languages', async () => {
    const { createI18n } = await import('vue-i18n')
    for (const [lang, cat] of [['fa', fa], ['en', en], ['ar', ar], ['ru', ru], ['tr', tr]] as const) {
      const i18n = createI18n({ legacy: false, locale: lang, messages: { [lang]: cat }, pluralRules, missingWarn: false, fallbackWarn: false })
      for (const k of keys(cat)) {
        expect(() => i18n.global.t(k, { n: 1, amount: 'x', balance: 'x', who: 'x', used: 'x', total: 'x', max: 'x', date: 'x', time: 'x', day: 1, week: 1, expiring: 1, ended: 1, brand: 'x', username: 'x', id: 'x', fallback: 'x', ratio: 'x', size: 'x', lang: 'x', key: 'x', file: 'x', name: 'x', arg: 'x', code: 'x', entity: 'x', tags: 'x', chars: 'x', entities: 'x', role: 'x', browser: 'x', os: 'x', device: 'x', group: 'x', def: 'x', min: 'x', title: 'x', sent: 'x', blocked: 'x', failed: 'x', done: 'x', zone: 'x', fa: 'x', en: 'x' }), `${lang}: ${k}`).not.toThrow()
      }
    }
  })
})
