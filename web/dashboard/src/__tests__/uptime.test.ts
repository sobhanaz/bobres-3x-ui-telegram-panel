import { describe, expect, it } from 'vitest'
import { availabilityLevel, availabilityText, cellState, observedShare, tidyUptime, UPTIME_WINDOWS, uptimeWindowOf } from '../monitor'

// The pure part of the Monitoring tab's Uptime section: how a cell of the bar is classed, how an availability is written
// and coloured, what is made of an answer that leaves things out. A moment nobody sampled is "no data" all the way down:
// a cell with nothing observed is neither up nor down, and a missing window is no data, never 100% and never 0%.

describe('the cells of the bar', () => {
  it('calls a cell with nothing observed no data, whatever else it holds', () => {
    expect(cellState([0, 0])).toBe('nodata')
    expect(cellState([0, 30])).toBe('nodata') // never happens, and would not be called down
    expect(cellState([-5, 0])).toBe('nodata')
  })
  it('calls a cell with no downtime up, some downtime partial and most of it down', () => {
    expect(cellState([1800, 0])).toBe('up')
    expect(cellState([1800, 1])).toBe('partial')
    expect(cellState([1800, 899])).toBe('partial')
    expect(cellState([1800, 900])).toBe('down') // half is the line
    expect(cellState([1800, 1800])).toBe('down')
  })
})

describe('the availability as a number', () => {
  it('writes up to three decimals, in the language of the page', () => {
    expect(availabilityText(100, 'en')).toBe('100%')
    expect(availabilityText(99.653, 'en')).toBe('99.653%')
    expect(availabilityText(99.9, 'en')).toBe('99.9%')
    expect(availabilityText(0, 'en')).toBe('0%')
    expect(availabilityText(99.999, 'en')).toBe('99.999%') // some downtime is never written as 100%
    expect(availabilityText(99.653, 'fa')).toBe('۹۹٫۶۵۳٪')
    expect(availabilityText(100, 'fa')).toBe('۱۰۰٪')
  })
  it('grades it by the usual lines, and says nothing about no data', () => {
    expect(availabilityLevel(null)).toBe('none')
    expect(availabilityLevel(100)).toBe('ok')
    expect(availabilityLevel(99.9)).toBe('ok')
    expect(availabilityLevel(99.899)).toBe('warn')
    expect(availabilityLevel(99)).toBe('warn')
    expect(availabilityLevel(98.9)).toBe('down')
    expect(availabilityLevel(0)).toBe('down') // all down is down; null is the only no data
  })
  it('measures how much of a period was observed', () => {
    expect(observedShare({ observed_s: 43_200, window_s: 86_400 })).toBe(50)
    expect(observedShare({ observed_s: 86_400, window_s: 86_400 })).toBe(100)
    expect(observedShare({ observed_s: 90_000, window_s: 86_400 })).toBe(100) // capped
    expect(observedShare({ observed_s: 0, window_s: 86_400 })).toBe(0)
    expect(observedShare({ observed_s: 10, window_s: 0 })).toBe(0)
  })
})

describe('the periods', () => {
  it('knows three, and falls back to the day for anything else', () => {
    expect(UPTIME_WINDOWS).toEqual(['24h', '7d', '30d'])
    for (const w of UPTIME_WINDOWS) expect(uptimeWindowOf(w)).toBe(w)
    for (const bad of [null, undefined, '', '1h', '30D', 'forever', 7]) expect(uptimeWindowOf(bad)).toBe('24h')
  })
})

describe('an answer that leaves things out', () => {
  it('becomes an empty reading, with every period there as no data', () => {
    const u = tidyUptime(null)
    expect(u).toEqual({ generated_at: 0, retention_days: 30, sample_every_s: 30, since: null, windows: [], services: [], panels: [], panels_unavailable: false, status_page: false })
    expect(tidyUptime({})).toEqual(u)
  })
  it('fills a component whose window is missing with no data of the right length of time', () => {
    const u = tidyUptime({ services: [{ id: 'core', name: 'core', windows: { '24h': { availability_pct: 99.5, observed_s: 100, window_s: 86_400, outages: 1, down_s: 5, longest_s: 5, bar: [[100, 5]] } } }] })
    const w = u.services[0].windows
    expect(w['24h']).toMatchObject({ availability_pct: 99.5, outages: 1, bar: [[100, 5]] })
    for (const [name, span] of [['7d', 604_800], ['30d', 2_592_000]] as const) {
      expect(w[name]).toEqual({ availability_pct: null, observed_s: 0, window_s: span, outages: 0, down_s: 0, longest_s: 0, bar: [] })
    }
  })
  it('keeps an unknown percentage null, never 0, and a bar that is missing an empty list', () => {
    const u = tidyUptime({
      panels: [{ id: 's1', name: 'DE-1', enabled: false, windows: { '24h': { availability_pct: undefined as unknown as null, observed_s: 0, window_s: 86_400, outages: 0, down_s: 0, longest_s: 0, bar: undefined as unknown as [number, number][] } } }],
      since: Number.NaN,
      panels_unavailable: true,
      status_page: true,
    })
    expect(u.panels[0].windows['24h'].availability_pct).toBeNull()
    expect(u.panels[0].windows['24h'].bar).toEqual([])
    expect(u.panels[0].enabled).toBe(false)
    expect(u.since).toBeNull()
    expect(u.panels_unavailable).toBe(true)
    expect(u.status_page).toBe(true)
  })
  it('takes the length of a missing window from the answer when it says', () => {
    const u = tidyUptime({ windows: [{ name: '7d', span_s: 100, bucket_s: 10 }], services: [{ id: 'bot', name: 'bot', windows: {} }] })
    expect(u.services[0].windows['7d'].window_s).toBe(100)
  })
})
