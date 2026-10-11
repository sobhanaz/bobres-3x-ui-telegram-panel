import { describe, expect, it } from 'vitest'
import { sections, visibleSections } from '../sections'
import fa from '../locales/fa.json'
import en from '../locales/en.json'

describe('sections', () => {
  it('lists all 17 dashboard sections once', () => {
    expect(sections).toHaveLength(17)
    expect(new Set(sections.map((s) => s.key)).size).toBe(17)
  })
  it('shows only what the role may open', () => {
    const support = ['overview.read', 'users.read', 'services.read', 'services.extend', 'payments.read', 'tickets.write']
    expect(visibleSections(support).map((s) => s.key)).toEqual(['overview', 'users', 'services', 'support', 'payments'])
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
