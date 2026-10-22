import { describe, expect, it } from 'vitest'
import { configPrefix, PANEL_GROUP_PREFIX, panelGroupName, resellerOf } from '../resellers'
import type { ResellerDetail } from '../types'
import fa from '../locales/fa.json'
import en from '../locales/en.json'

// What a reseller's configs are called and grouped as in the 3x-ui panel: the rules the owner's forms check the way core does.
describe('reseller naming', () => {
  it('reads a group name as core stores it', () => {
    expect(PANEL_GROUP_PREFIX).toBe('BOBRES-')
    expect(panelGroupName('sobhanadmin')).toBe('SOBHANADMIN')
    expect(panelGroupName(' bobres-Sobhan_1 ')).toBe('SOBHAN_1')
    expect(panelGroupName('AB')).toBe('AB')
    for (const bad of ['', 'a', 'A-B', 'has space', 'x'.repeat(25), 'BOBRES-', 'نماینده']) expect(panelGroupName(bad), bad).toBe('')
  })
  it('reads a config prefix as core stores it', () => {
    expect(configPrefix('Sob')).toBe('sob')
    expect(configPrefix(' a1b2c3d4e5 ')).toBe('a1b2c3d4e5')
    for (const bad of ['', 'a', 'abcdefghijk', 'a-b', 'u', 'u12', 'U99', 'a_b']) expect(configPrefix(bad), bad).toBe('')
  })
  it('takes the names from a reseller answer, and none from an older one', () => {
    const item = {
      user: { id: 'u1', telegram_id: 1, username: 'r', status: 'active' },
      tier: { id: 't', name: 'Gold', discount_percent: 10, commission_percent: 5 },
      commission_percent: 5, commission_override: null, balances: [], customers: 0, commission_earned: [], created_at: 1,
      note: '', version: 1, can_remove: true,
      config_prefix: 'sob', panel_group: 'SOBHANADMIN', panel_group_full: 'BOBRES-SOBHANADMIN', groups_synced: true,
    } as unknown as ResellerDetail
    const r = resellerOf(item)
    expect([r.configPrefix, r.panelGroup, r.panelGroupFull, r.groupsSynced]).toEqual(['sob', 'SOBHANADMIN', 'BOBRES-SOBHANADMIN', true])
    const older = resellerOf({ ...item, config_prefix: undefined, panel_group: undefined, panel_group_full: undefined, groups_synced: undefined })
    expect([older.configPrefix, older.panelGroup, older.panelGroupFull, older.groupsSynced]).toEqual(['', '', '', false])
  })
  it('has its texts in both languages', () => {
    const sources = import.meta.glob(['../components/resellers/NamingDialog.vue', '../components/resellers/MakeResellerDialog.vue', '../pages/ResellerPage.vue'], {
      query: '?raw', import: 'default', eager: true,
    }) as Record<string, string>
    const has = (cat: Record<string, unknown>, key: string) =>
      typeof key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], cat) === 'string'
    const keys = new Set<string>()
    for (const src of Object.values(sources)) for (const m of src.matchAll(/\bt\('([a-z_.0-9]+)'\s*[,)]/g)) keys.add(m[1])
    for (const k of ['reseller.naming_title', 'reseller.panel_group', 'reseller.config_prefix', 'reseller.naming_move']) expect(keys.has(k), k).toBe(true)
    for (const k of keys) {
      expect(has(fa, k), 'fa: ' + k).toBe(true)
      expect(has(en, k), 'en: ' + k).toBe(true)
    }
    for (const cat of [fa, en] as Record<string, any>[]) {
      for (const a of ['panel_group', 'config_prefix', 'group_sync']) expect(cat.audit.action.reseller[a], a).toBeTruthy()
    }
  })
})
