import { describe, expect, it } from 'vitest'
import fa from '../locales/fa.json'
import en from '../locales/en.json'

// Plan groups (the plan editor's Group field and the groups table): every text the screens ask for is in both catalogs.
describe('plan groups texts', () => {
  const sources = import.meta.glob(['../pages/PlansPage.vue', '../components/plans/*.vue'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>
  const has = (cat: Record<string, unknown>, key: string) =>
    typeof key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], cat) === 'string'

  it('exist in Persian and English', () => {
    const keys = new Set<string>()
    for (const src of Object.values(sources)) for (const m of src.matchAll(/\bt\('([a-z_.0-9]+)'\s*[,)]/g)) keys.add(m[1])
    for (const k of ['plan.groups_title', 'plan.group_new', 'plan.group_delete_confirm', 'plan.group', 'plan.group_none']) expect(keys.has(k), k).toBe(true)
    for (const k of keys) {
      expect(has(fa, k), 'fa: ' + k).toBe(true)
      expect(has(en, k), 'en: ' + k).toBe(true)
    }
  })

  it('names the audit entries of a group', () => {
    for (const cat of [fa, en] as Record<string, any>[]) {
      expect(cat.audit.action.plan_group.upsert).toBeTruthy()
      expect(cat.audit.action.plan_group.delete).toBeTruthy()
    }
  })
})
