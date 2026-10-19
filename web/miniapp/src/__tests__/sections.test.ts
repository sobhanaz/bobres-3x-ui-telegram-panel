import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import type { Me } from '../api'

const me = (features: Me['features'] = {}): Me => ({
  user: { id: 'u', username: '', language: 'en' },
  wallets: [],
  brand: { name: '', color: '', support: '', support_url: '', bot_url: '' },
  maintenance: false,
  top_up: { currency: 'IRT', min: 1, max: 2 },
  features,
})

const stub = defineComponent({ template: '<p />' })

describe('sections', () => {
  // each test starts with an empty registry
  beforeEach(() => vi.resetModules())

  it('shows a customer only the sections that are visible to them, in order', async () => {
    const { registerSection, sectionsFor } = await import('../sections')
    registerSection({ id: 'c', title: 'c', visible: () => true, component: stub })
    registerSection({ id: 'a', title: 'a', visible: (m) => m.features.a === true, component: stub, order: 10 })
    registerSection({ id: 'b', title: 'b', visible: (m) => m.features.b === true, component: stub, order: 10 })
    registerSection({ id: 'd', title: 'd', visible: () => true, component: stub, order: 200 })
    expect(sectionsFor(me()).map((s) => s.id)).toEqual(['c', 'd'])
    expect(sectionsFor(me({ b: true, a: true })).map((s) => s.id)).toEqual(['a', 'b', 'c', 'd']) // 10, 10 (registered order), 100, 200
    expect(sectionsFor(null)).toEqual([])
  })

  it('refuses two sections with the same id', async () => {
    const { registerSection } = await import('../sections')
    registerSection({ id: 'x', title: 'x', visible: () => true, component: stub })
    expect(() => registerSection({ id: 'x', title: 'x', visible: () => true, component: stub })).toThrow(/twice/)
  })
})

// Every text a view asks for by name exists in both languages, so a typo or a
// forgotten translation fails here and not on a customer's screen.
describe('texts used by the code', () => {
  const sources = import.meta.glob(['../**/*.vue', '../**/*.ts', '!../**/__tests__/**'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>

  it('are all defined in fa and en', async () => {
    const { messages } = await import('../i18n')
    const used = new Map<string, string>()
    for (const [file, text] of Object.entries(sources)) {
      for (const m of text.matchAll(/\btr\(\s*'([a-z0-9_]+(?:\.[a-z0-9_]+)+)'/g)) used.set(m[1], file)
      for (const m of text.matchAll(/\b(?:title|hint|label|labelKey):\s*'([a-z0-9_]+(?:\.[a-z0-9_]+)+)'/g)) used.set(m[1], file)
    }
    expect(used.size).toBeGreaterThan(20)
    for (const [key, file] of used) {
      expect(messages.fa[key], `fa "${key}" (used in ${file})`).toBeTruthy()
      expect(messages.en[key], `en "${key}" (used in ${file})`).toBeTruthy()
    }
  })
})
