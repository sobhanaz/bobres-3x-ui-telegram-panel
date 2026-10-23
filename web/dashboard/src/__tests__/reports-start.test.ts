import { describe, expect, it } from 'vitest'
import fa from '../locales/fa.json'
import en from '../locales/en.json'

// Starting the reports from zero (the owner's button above the Reports page): its texts are in both catalogs, and its audit entries have names.
describe('reports start texts', () => {
  it('exist in Persian and English', () => {
    for (const cat of [fa, en] as Record<string, any>[]) {
      for (const k of ['all', 'since', 'action', 'restart', 'clear', 'confirm_title', 'confirm', 'clear_confirm', 'done', 'cleared']) {
        expect(typeof cat.reports.start[k], k).toBe('string')
      }
      expect(cat.reports.start.since).toContain('{when}')
      expect(cat.audit.action.reports.reset).toBeTruthy()
      expect(cat.audit.action.reports.reset_clear).toBeTruthy()
      // the licenses and digital accounts the store sold: a line on the cards and a series in the charts
      expect(cat.reports.kpi.licenses).toContain('{n}')
      expect(cat.reports.kpi.digital).toContain('{amount}')
      expect(cat.reports.chart.type.license).toBeTruthy()
      expect(cat.reports.chart.type.digital).toBeTruthy()
    }
  })

  it('says that nothing is deleted before the owner confirms', () => {
    expect(en.reports.start.confirm).toContain('Nothing is deleted')
    expect(fa.reports.start.confirm).toContain('چیزی پاک نمی‌شود')
  })
})
