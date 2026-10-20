import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { localDate, money, moneyCompact, percent } from '../format'
import {
  bars,
  currenciesOf,
  defaultCalendar,
  exportParams,
  initialCalendar,
  jalaliMonth,
  hasData,
  periodLabel,
  rememberCalendar,
  reportParams,
  savedCalendar,
  share,
  validCustom,
  type ReportBucket,
  type ReportCell,
  type ReportMethod,
  type ReportPlan,
  type ReportSummary,
} from '../reports'

// The numbers: dates the store's zone names stay on their day, amounts keep
// their unit, a chart is scaled to one currency, and a request names a range.
const churn0 = { churned_services: 0, churned_customers: 0, at_risk: 0 }

describe('report dates and numbers', () => {
  it('writes a store-local date as the same day in both calendars', () => {
    // 14 Oct 2026 is 22 Mehr 1405; the browser's own zone cannot move it
    expect(localDate('2026-10-14', 'en')).toBe('14 Oct 2026')
    expect(localDate('2026-10-14', 'en', 'short')).toBe('14 Oct')
    const fa = localDate('2026-10-14', 'fa')
    expect(fa).toContain('۱۴۰۵')
    expect(fa).toContain('۲۲')
    expect(fa).toContain('مهر')
    // the first day of a Gregorian month is not the first of a Jalali month
    expect(localDate('2026-10-01', 'fa')).toContain('۹')
    expect(localDate('2026-03-21', 'fa')).toContain('فروردین')
  })

  it('shortens amounts for an axis in the currency unit', () => {
    expect(moneyCompact({ amount: 1_200_000, currency: 'IRT' }, 'en')).toBe('1.2M Toman')
    expect(moneyCompact({ amount: 2_500_000, currency: 'USDT' }, 'en')).toBe('2.5 USDT') // micro-USDT: 2.5
    expect(moneyCompact({ amount: 0, currency: 'IRT' }, 'en')).toBe('0 Toman')
    expect(moneyCompact({ amount: 1_200_000, currency: 'IRT' }, 'fa')).toContain('تومان')
  })

  it('writes percent in the language', () => {
    expect(percent(50, 'en')).toBe('50%')
    expect(percent(50, 'fa')).toBe('۵۰٪')
    expect(percent(0, 'en')).toBe('0%')
  })

  it('asks for a named range as it is and a custom one as dates, always with the group', () => {
    expect(reportParams({ range: 'last_7_days', group: 'week', calendar: 'gregorian', from: '2026-01-01', to: '2026-01-02' })).toEqual({ range: 'last_7_days', group: 'week' })
    expect(reportParams({ range: 'custom', group: 'day', calendar: 'gregorian', from: '2026-10-01', to: '2026-10-14' })).toEqual({ from: '2026-10-01', to: '2026-10-14', group: 'day' })
  })

  it('pins the dates and zone core resolved for the CSV links', () => {
    expect(exportParams({ from: '2026-10-01', to: '2026-10-14', group: 'month', tz: 'Asia/Tehran' })).toEqual({
      from: '2026-10-01',
      to: '2026-10-14',
      group: 'month',
      tz: 'Asia/Tehran',
    })
  })

  it('accepts a custom range of two ordered dates, at most 400 days', () => {
    expect(validCustom('2026-10-01', '2026-10-14')).toBe(true)
    expect(validCustom('2026-10-14', '2026-10-14')).toBe(true)
    expect(validCustom('2026-10-14', '2026-10-13')).toBe(false)
    expect(validCustom('', '2026-10-13')).toBe(false)
    expect(validCustom('2026-1-1', '2026-10-13')).toBe(false)
    expect(validCustom('2026-01-01', '2027-02-04')).toBe(true) // 400 days
    expect(validCustom('2026-01-01', '2027-02-05')).toBe(false) // 401
  })

  it('scales each currency alone and keeps empty periods as zero-height bars', () => {
    const cell = (revenue: number): ReportCell => ({
      revenue, refunds: { count: 0, amount: 0 }, net: revenue, orders: revenue ? 1 : 0, new_orders: 0, renewals: 0, traffic_topups: 0,
      topups: { count: 0, amount: 0 }, rejected: { count: 0, amount: 0 }, gateway_failed: { count: 0, amount: 0 }, cancelled: { count: 0, amount: 0 },
    })
    const bucket = (date: string, irt: number, usdt: number, partial = false): ReportBucket => ({
      date, partial, new_customers: 0, trials: 0, ...churn0, currencies: { IRT: cell(irt), USDT: cell(usdt) },
    })
    const s = {
      from: '2026-10-01', to: '2026-10-03', group: 'day', calendar: 'gregorian', tz: 'Asia/Tehran', churn_grace_days: 7, currencies: ['IRT', 'USDT'],
      buckets: [bucket('2026-10-01', 100, 0), bucket('2026-10-02', 0, 5_000_000), bucket('2026-10-03', 50, 0, true)],
      totals: { new_customers: 0, trials: 0, ...churn0, currencies: { IRT: cell(150), USDT: cell(5_000_000) } },
    } as ReportSummary
    expect(bars(s, 'IRT').map((b) => b.pct)).toEqual([100, 0, 50])
    expect(bars(s, 'USDT').map((b) => b.pct)).toEqual([0, 100, 0]) // 5,000,000 micro-USDT never lifts the Toman bars
    expect(bars(s, 'IRT')[2].partial).toBe(true)
    expect(bars(s, 'EUR').map((b) => b.pct)).toEqual([0, 0, 0]) // no revenue: a flat chart, not NaN
    expect(hasData(s)).toBe(true)
    expect(hasData({ ...s, currencies: [], totals: { new_customers: 0, trials: 0, ...churn0, currencies: {} } })).toBe(false)
    expect(hasData({ ...s, currencies: [], totals: { new_customers: 2, trials: 0, ...churn0, currencies: {} } })).toBe(true)
    // a period that only lost services still has something to show
    expect(hasData({ ...s, currencies: [], totals: { new_customers: 0, trials: 0, ...churn0, churned_services: 1, currencies: {} } })).toBe(true)
    expect(hasData({ ...s, currencies: [], totals: { new_customers: 0, trials: 0, ...churn0, at_risk: 3, currencies: {} } })).toBe(true)
    // a currency only a table names is still listed, once
    const plans = [{ currency: 'XTR' }, { currency: 'IRT' }] as ReportPlan[]
    expect(currenciesOf(s, plans, [{ currency: 'XTR' }] as ReportMethod[])).toEqual(['IRT', 'USDT', 'XTR'])
  })

  it('splits a total into whole percent', () => {
    expect(share(1, 3)).toBe(33)
    expect(share(2, 3)).toBe(67)
    expect(share(5, 0)).toBe(0)
  })

  it('sends the calendar only when it is Jalali, and pins it for the CSV links', () => {
    const sel = { range: 'last_30_days', group: 'month', from: '', to: '' } as const
    expect(reportParams({ ...sel, calendar: 'jalali' })).toEqual({ range: 'last_30_days', group: 'month', calendar: 'jalali' })
    expect(reportParams({ ...sel, calendar: 'gregorian' })).toEqual({ range: 'last_30_days', group: 'month' })
    expect(reportParams({ range: 'custom', group: 'week', calendar: 'jalali', from: '2026-10-01', to: '2026-10-14' })).toEqual({
      from: '2026-10-01', to: '2026-10-14', group: 'week', calendar: 'jalali',
    })
    const pin = { from: '2026-10-01', to: '2026-10-14', group: 'month', tz: 'Asia/Tehran' } as const
    expect(exportParams({ ...pin, calendar: 'jalali' })).toEqual({ ...pin, calendar: 'jalali' })
    expect(exportParams({ ...pin, calendar: 'gregorian' })).toEqual(pin)
  })

  it('reads Persian in the Jalali calendar and English in the Gregorian one, by default', () => {
    expect(defaultCalendar('fa')).toBe('jalali')
    expect(defaultCalendar('en')).toBe('gregorian')
  })

  it('names a Jalali month by its name and year, and a Gregorian one as before', () => {
    const t = (key: string, p?: Record<string, unknown>) => key + ':' + (p?.date ?? '')
    expect(periodLabel('2026-10-23', 'month', 'en', t, 'jalali')).toBe('Aban 1405')
    expect(periodLabel('2026-10-23', 'month', 'fa', t, 'jalali')).toBe('آبان ۱۴۰۵')
    expect(periodLabel('2026-03-21', 'month', 'en', t, 'jalali')).toBe('Farvardin 1405')
    // weeks and days are named by their first day, written in the calendar of the report
    expect(periodLabel('2026-10-17', 'week', 'en', t, 'jalali')).toBe('reports.period.week_of:25 Mehr 1405')
    expect(periodLabel('2026-10-17', 'day', 'en', t, 'jalali')).toBe('25 Mehr 1405')
    expect(periodLabel('2026-10-17', 'day', 'fa', t, 'jalali')).toBe('۲۵ مهر ۱۴۰۵')
    expect(periodLabel('2026-10-17', 'day', 'fa', t, 'gregorian')).toBe('۱۷ اکتبر ۲۰۲۶') // Persian, but the Gregorian calendar was asked for
    expect(periodLabel('2026-10-01', 'month', 'en', t, 'gregorian')).toBe('reports.period.month_of:1 Oct 2026')
    expect(periodLabel('2026-10-01', 'month', 'fa', t, 'gregorian')).toBe('reports.period.month_of:۱ اکتبر ۲۰۲۶')
  })

  it('writes the dates in the calendar the page is set to, even before a report cut that way arrives', () => {
    const t = (key: string, p?: Record<string, unknown>) => key + ':' + (p?.date ?? '')
    // the periods on screen are Gregorian months, the page was just switched to Jalali: a true date, not a month's name
    expect(periodLabel('2026-10-01', 'month', 'en', t, 'gregorian', 'jalali')).toBe('reports.period.month_of:9 Mehr 1405')
    // and the other way round: a Jalali month starting on 23 October is not "October"
    expect(periodLabel('2026-10-23', 'month', 'en', t, 'jalali', 'gregorian')).toBe('reports.period.month_of:23 Oct 2026')
    expect(periodLabel('2026-10-23', 'month', 'en', t, 'jalali', 'jalali')).toBe('Aban 1405')
  })

  it('remembers the calendar per browser, and ignores what storage holds that is no calendar', () => {
    localStorage.removeItem('bobres.reports.calendar')
    expect(savedCalendar()).toBeNull()
    expect(initialCalendar('fa')).toBe('jalali')
    expect(initialCalendar('en')).toBe('gregorian')
    rememberCalendar('gregorian')
    expect(localStorage.getItem('bobres.reports.calendar')).toBe('gregorian')
    expect(initialCalendar('fa')).toBe('gregorian') // a choice wins over the language
    rememberCalendar('jalali')
    expect(initialCalendar('en')).toBe('jalali')
    localStorage.setItem('bobres.reports.calendar', 'persian')
    expect(savedCalendar()).toBeNull()
    expect(initialCalendar('fa')).toBe('jalali')
    localStorage.removeItem('bobres.reports.calendar')
    // blocked storage: the default, and no error
    const get = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    expect(initialCalendar('fa')).toBe('jalali')
    expect(() => rememberCalendar('gregorian')).not.toThrow()
    get.mockRestore()
    set.mockRestore()
  })

  it('agrees with the Jalali calendar on the first day of each month (the same anchors as core)', () => {
    // 1 Farvardin 1399..1479 start on these days: the Nowruz anchors core tests against
    const nowruz: Record<number, string> = {
      1399: '2020-03-20', 1403: '2024-03-20', 1404: '2025-03-21', 1405: '2026-03-21', 1408: '2029-03-20', 1412: '2033-03-20', 1416: '2037-03-20', 1420: '2041-03-20', 1479: '2100-03-21',
    }
    const parts = (date: string) => {
      const [y, m, d] = date.split('-').map(Number)
      const f = new Intl.DateTimeFormat('en-US-u-ca-persian', { year: 'numeric', month: 'numeric', day: 'numeric', timeZone: 'UTC' }).formatToParts(new Date(Date.UTC(y, m - 1, d)))
      return Object.fromEntries(f.filter((p) => p.type !== 'literal' && p.type !== 'era').map((p) => [p.type, Number(p.value)]))
    }
    for (const [year, date] of Object.entries(nowruz)) {
      expect(parts(date), date).toMatchObject({ year: Number(year), month: 1, day: 1 })
      // the day before is the last of the year before, so a Jalali month never starts one day off
      const prev = new Date(Date.parse(date) - 86_400_000).toISOString().slice(0, 10)
      expect(parts(prev), prev).toMatchObject({ year: Number(year) - 1, month: 12 })
      // and the label of a bucket that starts there is Farvardin of that year
      expect(jalaliMonth(date, 'en')).toBe('Farvardin ' + year)
    }
    // every Jalali month of 1405 starts a bucket named after it
    const mehr = parts('2026-09-23')
    expect(mehr).toMatchObject({ year: 1405, month: 7, day: 1 })
    expect(jalaliMonth('2026-09-23', 'fa')).toBe('مهر ۱۴۰۵')
  })

  it('names a period by its first day', () => {
    const t = (key: string, p?: Record<string, unknown>) => key + ':' + (p?.date ?? '')
    expect(periodLabel('2026-10-14', 'day', 'en', t)).toBe('14 Oct 2026')
    expect(periodLabel('2026-10-12', 'week', 'en', t)).toBe('reports.period.week_of:12 Oct 2026')
    expect(periodLabel('2026-10-01', 'month', 'en', t)).toBe('reports.period.month_of:1 Oct 2026')
  })
})

// The page, booted like the real app with a fake core.
describe('the Reports page', () => {
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  type Answer = (url: string) => Response | undefined
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()

  async function boot(path: string, perms: string[], answer: Answer, lang: 'en' | 'fa' = 'en') {
    unmount()
    try {
      localStorage.setItem('bobres.lang', lang)
    } catch {
      /* the page falls back to Persian and the test says so */
    }
    vi.resetModules()
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const fetchMock = vi.fn(async (url: string) => {
      const own = answer(url)
      if (own) return own
      if (url === '/api/v1/me') {
        return json(200, {
          user: { id: 'u1', telegram_id: 1, username: 'boss', role: 'admin', language: lang }, permissions: perms, csrf: 'c',
          session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
        })
      }
      if (url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null })
      if (url === '/api/v1/overview') return json(503, { error: 'unavailable', message: 'not in this test' })
      return json(503, { error: 'unavailable', message: 'not in this test' })
    })
    vi.stubGlobal('fetch', fetchMock)
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { fetchMock, router }
  }

  afterEach(() => {
    unmount()
    try {
      localStorage.removeItem('bobres.lang')
      localStorage.removeItem('bobres.reports.calendar')
    } catch {
      /* nothing was stored */
    }
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  const found = async <T extends Element>(selector: string) =>
    vi.waitFor(() => {
      const el = document.querySelector<T>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })

  const flow = (count = 0, amount = 0) => ({ count, amount })
  const cell = (c: Partial<ReportCell>): ReportCell => ({
    revenue: 0, refunds: flow(), net: 0, orders: 0, new_orders: 0, renewals: 0, traffic_topups: 0, topups: flow(), rejected: flow(), gateway_failed: flow(), cancelled: flow(), ...c,
  })
  const summary = (): ReportSummary => ({
    from: '2026-10-15', to: '2026-10-17', group: 'day', calendar: 'gregorian', tz: 'Asia/Tehran', churn_grace_days: 7, currencies: ['IRT', 'USDT'],
    buckets: [
      { date: '2026-10-15', partial: false, new_customers: 1, trials: 0, churned_services: 2, churned_customers: 1, at_risk: 0, currencies: { IRT: cell({ revenue: 100_000, net: 100_000, orders: 1, new_orders: 1 }), USDT: cell({}) } },
      { date: '2026-10-16', partial: false, new_customers: 0, trials: 1, churned_services: 0, churned_customers: 0, at_risk: 1, currencies: { IRT: cell({}), USDT: cell({ revenue: 2_500_000, net: 2_500_000, orders: 1, renewals: 1 }) } },
      {
        date: '2026-10-17', partial: false, new_customers: 1, trials: 0, churned_services: 1, churned_customers: 1, at_risk: 2,
        currencies: { IRT: cell({ revenue: 250_000, refunds: flow(1, 50_000), net: 200_000, orders: 2, new_orders: 1, traffic_topups: 1, topups: flow(1, 70_000), rejected: flow(1, 90_000), gateway_failed: flow(3, 40_000), cancelled: flow(2, 300_000) }), USDT: cell({}) },
      },
    ],
    totals: {
      new_customers: 2, trials: 1, churned_services: 3, churned_customers: 2, at_risk: 3,
      currencies: {
        IRT: cell({ revenue: 350_000, refunds: flow(1, 50_000), net: 300_000, orders: 3, new_orders: 2, traffic_topups: 1, topups: flow(1, 70_000), rejected: flow(1, 90_000), gateway_failed: flow(3, 40_000), cancelled: flow(2, 300_000) }),
        USDT: cell({ revenue: 2_500_000, net: 2_500_000, orders: 1, renewals: 1 }),
      },
    },
  })
  const plans = (): { plans: ReportPlan[] } => ({
    plans: [
      { plan_id: 'p1', name: { fa: 'پایه', en: 'Basic' }, currency: 'IRT', orders: 3, revenue: 350_000 },
      { plan_id: 'p2', name: { fa: 'تتر', en: 'Pro USDT' }, currency: 'USDT', orders: 1, revenue: 2_500_000 },
    ],
  })
  const methods = (): { methods: ReportMethod[] } => ({
    methods: [
      { method: 'wallet', provider: '', currency: 'IRT', orders: 2, revenue: 250_000 },
      { method: 'manual', provider: 'manual_card', currency: 'IRT', orders: 1, revenue: 100_000 },
      { method: 'gateway', provider: 'usdt_trc20_auto', currency: 'USDT', orders: 1, revenue: 2_500_000 },
      { method: 'gateway', provider: '', currency: 'USDT', orders: 1, revenue: 400_000 },
      { method: 'unknown', provider: '', currency: 'USDT', orders: 1, revenue: 100_000 },
    ],
  })
  const params = (url: string) => new URL(url, 'https://x.example').searchParams
  // What core answers: the calendar and the group it was asked for, echoed.
  const answered = (url: string): ReportSummary => {
    const q = params(url)
    return { ...summary(), calendar: q.get('calendar') === 'jalali' ? 'jalali' : 'gregorian', group: (q.get('group') || 'day') as ReportSummary['group'] }
  }
  const core =
    (over: { summary?: (url: string) => Response; daily?: () => Response }): Answer =>
    (url) => {
      if (url === '/api/v1/reports/daily-summary' && over.daily) return over.daily()
      if (url.startsWith('/api/v1/reports/summary')) return over.summary ? over.summary(url) : json(200, answered(url))
      if (url.startsWith('/api/v1/reports/plans')) return json(200, plans())
      if (url.startsWith('/api/v1/reports/methods')) return json(200, methods())
      return undefined
    }
  const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
  // Typing into a field: the text, and the input event a keystroke makes.
  const typeInto = (el: HTMLInputElement, value: string) => {
    el.value = value
    el.dispatchEvent(new Event('input', { bubbles: true }))
  }

  it('shows each currency on its own: a card, a chart and a table per currency, never a sum', async () => {
    const { fetchMock } = await boot('/admin/reports', ['overview.read', 'reports.read'], core({}))
    await found('.currency-card')
    const cards = [...document.querySelectorAll<HTMLElement>('.currency-card')]
    expect(cards.map((c) => c.dataset.currency)).toEqual(['IRT', 'USDT'])
    const irt = text(cards[0])
    expect(irt).toContain('350,000 Toman')
    expect(irt).toContain('After refunds: 300,000 Toman')
    expect(irt).toContain('3 paid orders')
    expect(irt).toContain('New 2 · Renewals 0 · Traffic 1')
    // staff rejections and failed gateway payments are two lines, never one
    expect([...cards[0].querySelectorAll('.flows dt')].map(text)).toEqual([
      'Wallet top-ups',
      'Refunds',
      'Rejected by staff',
      'Failed or expired gateway payments',
      'Cancelled orders',
    ])
    expect([...cards[0].querySelectorAll('.flows dd')].map(text)).toEqual([
      '70,000 Toman · 1 top-up',
      '50,000 Toman · 1 refund',
      '90,000 Toman · 1 payment',
      '40,000 Toman · 3 payments',
      '300,000 Toman · 2 orders',
    ])
    expect(irt).not.toContain('USDT')
    const usdt = text(cards[1])
    expect(usdt).toContain('2.5 USDT')
    expect(usdt).toContain('1 paid order') // singular
    expect(usdt).not.toContain('1 paid orders')
    expect(usdt).not.toContain('Toman')
    // the customers card counts people, once
    expect(text(document.querySelector('.customers-card'))).toContain('2')
    expect(text(document.querySelector('.customers-card'))).toContain('Free trials started: 1')
    // the churn card: services lost, people lost, and the ones still inside the grace period
    const churn = text(document.querySelector('.churn-card'))
    expect(text(document.querySelector('.churned-services'))).toBe('3')
    expect(churn).toContain('Services lost')
    expect(churn).toContain('not renewed within 7 days')
    expect(churn).toContain('Customers lost: 2')
    expect(churn).toContain('Ended, still inside the grace period: 3')
    const headers = [...document.querySelectorAll('.period-table th')].map((h) => text(h))
    expect(headers.slice(-2)).toEqual(['Services lost', 'At risk'])
    const firstRow = [...document.querySelectorAll('.period-table tbody tr:first-child td')].map((c) => text(c))
    expect(firstRow.slice(-2)).toEqual(['2', '0'])
    // one chart per currency, a bar per period
    const charts = [...document.querySelectorAll<HTMLElement>('.chart')]
    expect(charts.map((c) => c.dataset.currency)).toEqual(['IRT', 'USDT'])
    expect(charts[0].querySelectorAll('.bar')).toHaveLength(3)
    const heights = [...charts[0].querySelectorAll<HTMLElement>('.bar')].map((b) => b.style.height)
    expect(heights).toEqual(['40%', '0%', '100%'])
    expect(charts[1].querySelector('.plot')?.getAttribute('aria-label')).toBe('USDT (Tether): highest 2.5 USDT, in 16 Oct 2026')
    expect(charts[0].querySelector('.slot')?.getAttribute('title')).toBe('15 Oct 2026: 100,000 Toman')
    // by period, top plans and by method
    expect(document.querySelectorAll('.period-table tbody tr')).toHaveLength(3)
    expect(text(document.querySelector('.plans-card [data-currency="IRT"]'))).toContain('Basic')
    expect(text(document.querySelector('.plans-card [data-currency="USDT"]'))).toContain('Pro USDT')
    const irtMethods = text(document.querySelector('.methods-card [data-currency="IRT"]'))
    expect(irtMethods).toContain('Wallet balance')
    expect(irtMethods).toContain('Approved by staff')
    expect(irtMethods).toContain('Card to card') // the provider that took the payment
    expect(irtMethods).not.toContain('not recorded') // wallet has no provider to record
    expect(irtMethods).toContain('71%') // 250,000 of 350,000
    expect(irtMethods).not.toContain('Payment gateway')
    // a sale with no record of how it was paid is listed, as Unknown, with its share
    const usdtMethods = text(document.querySelector('.methods-card [data-currency="USDT"]'))
    expect(usdtMethods).toContain('Payment gateway')
    expect(usdtMethods).toContain('Unknown')
    expect(usdtMethods).toContain('A sale with no record of how it was paid.')
    // one gateway row names its provider, another (an old event) says it was not recorded
    expect(usdtMethods).toContain('USDT TRC20 (automatic)')
    expect(usdtMethods).toContain('Gateway (provider not recorded)')
    expect(document.querySelectorAll('.methods-card [data-currency="USDT"] tbody tr')).toHaveLength(3)
    expect(usdtMethods).toContain('3%') // 100,000 of 3,000,000
    expect(usdtMethods).toContain('13%') // 400,000
    expect(usdtMethods).toContain('83%')
    // the covered days, in the store's zone
    expect(text(document.querySelector('.covers'))).toBe('15 Oct 2026 to 17 Oct 2026 · time zone ⁨Asia/Tehran⁩')

    // the plans and methods asked for the very days and zone the summary named
    const urls = fetchMock.mock.calls.map((c) => String(c[0]))
    expect(urls).toContain('/api/v1/reports/summary?range=last_30_days&group=day')
    expect(urls).toContain('/api/v1/reports/plans?from=2026-10-15&to=2026-10-17&tz=Asia%2FTehran')
    expect(urls).toContain('/api/v1/reports/methods?from=2026-10-15&to=2026-10-17&tz=Asia%2FTehran')
  })

  it('links the CSV files for the range on screen', async () => {
    await boot('/admin/reports', ['reports.read'], core({}))
    const summaryLink = await found<HTMLAnchorElement>('.export-summary')
    const ordersLink = await found<HTMLAnchorElement>('.export-orders')
    const q = new URLSearchParams('from=2026-10-15&to=2026-10-17&group=day&tz=Asia%2FTehran')
    for (const [el, path] of [[summaryLink, '/api/v1/reports/summary.csv'], [ordersLink, '/api/v1/reports/orders.csv']] as const) {
      // an ordinary same-origin download, no blob: the session cookie authorizes it
      const u = new URL(el.getAttribute('href')!, 'https://x.example')
      expect(u.pathname).toBe(path)
      expect(Object.fromEntries(u.searchParams)).toEqual(Object.fromEntries(q))
      expect(el.hasAttribute('download')).toBe(true)
      expect(el.getAttribute('href')).not.toMatch(/^blob:|^data:/)
    }
    expect(text(summaryLink)).toBe('Summary CSV')
    expect(text(ordersLink)).toBe('Paid orders CSV')
  })

  it('says so when nothing was sold, with no cards or charts', async () => {
    const empty: ReportSummary = {
      ...summary(), currencies: [], buckets: summary().buckets.map((b) => ({ ...b, new_customers: 0, trials: 0, ...churn0, currencies: {} })),
      totals: { new_customers: 0, trials: 0, ...churn0, currencies: {} },
    }
    await boot('/admin/reports', ['reports.read'], core({ summary: () => json(200, empty) }))
    await found('.empty')
    expect(text(document.querySelector('.empty'))).toBe('No sales in this period.')
    expect(document.querySelector('.currency-card')).toBeNull()
    expect(document.querySelector('.chart')).toBeNull()
    expect(document.querySelector('.report-error')).toBeNull()
  })

  it('shows an error with a retry, and the report once it works', async () => {
    let calls = 0
    await boot(
      '/admin/reports',
      ['reports.read'],
      core({
        summary: () => (++calls === 1 ? json(500, { error: 'internal', message: 'boom' }) : json(200, summary())),
      }),
    )
    const err = await found('.report-error')
    expect(text(err)).toContain('Something went wrong')
    expect(document.querySelector('.currency-card')).toBeNull()
    expect(document.querySelector('.export-summary')).toBeNull() // no file for a report that did not load
    const retry = err.querySelector<HTMLButtonElement>('.retry')!
    retry.click()
    await found('.currency-card')
    expect(document.querySelector('.report-error')).toBeNull()
    expect(calls).toBe(2)
  })

  it('shows what core refused as the reason, for a range it will not read', async () => {
    await boot(
      '/admin/reports',
      ['reports.read'],
      core({ summary: () => json(400, { error: 'invalid', message: 'a report covers at most 400 days (this one covers 401)' }) }),
    )
    const err = await found('.report-error')
    expect(text(err)).toContain('at most 400 days')
  })

  it('asks again when the range or the group changes', async () => {
    const { fetchMock } = await boot('/admin/reports', ['reports.read'], core({}))
    await found('.currency-card')
    const click = (label: string) => {
      const b = [...document.querySelectorAll<HTMLElement>('.range [role="button"], .range button')].find((e) => text(e) === label)
      expect(b, label).toBeDefined()
      b!.click()
    }
    click('Last 7 days')
    await vi.waitFor(() => expect(fetchMock.mock.calls.map((c) => String(c[0]))).toContain('/api/v1/reports/summary?range=last_7_days&group=day'))
    click('Custom')
    const from = await found<HTMLInputElement>('.date-from')
    const to = await found<HTMLInputElement>('.date-to')
    // it starts from the dates on screen, written in the calendar shown (English, Gregorian: year-month-day)
    expect(from.value).toBe('2026-10-15')
    expect(to.value).toBe('2026-10-17')
    typeInto(from, '2026-10-01')
    await vi.waitFor(() => expect(fetchMock.mock.calls.map((c) => String(c[0]))).toContain('/api/v1/reports/summary?from=2026-10-01&to=2026-10-17&group=day'))
    // a range core would refuse is not sent, and the page says why
    const before = fetchMock.mock.calls.length
    typeInto(to, '2026-09-01')
    await new Promise((r) => setTimeout(r, 450))
    expect(fetchMock.mock.calls.length).toBe(before)
    expect(to.getAttribute('aria-invalid')).toBe('true')
    expect(text(document.querySelector('.custom-problem'))).toBe('The end date is before the start date.')
  })

  it('reads in Persian: Persian digits, Jalali dates, right to left', async () => {
    await boot('/admin/reports', ['reports.read'], core({}), 'fa')
    await found('.currency-card')
    expect(document.documentElement.dir).toBe('rtl')
    expect(text(document.querySelector('.currency-card[data-currency="IRT"]'))).toContain('۳۵۰٬۰۰۰ تومان')
    expect(text(document.querySelector('.covers'))).toContain('۱۴۰۵') // 15 Oct 2026 is 23 Mehr 1405
    expect(text(document.querySelector('.covers'))).toContain('مهر')
    expect(text(document.querySelector('.methods-card [data-currency="IRT"]'))).toContain('۷۱٪')
    expect(text(document.querySelector('.methods-card [data-currency="IRT"]'))).toContain('کارت به کارت')
    expect(text(document.querySelector('.methods-card [data-currency="USDT"]'))).toContain('درگاه (ثبت نشده)')
    expect(text(document.querySelector('.export-orders'))).toBe('خروجی CSV سفارش‌های پرداخت‌شده')
    // no text of the page is left as a missing key
    expect(document.body.textContent).not.toMatch(/reports\.[a-z_.]+/)
  })

  it('asks for Jalali weeks and months in Persian by default, and when the calendar is switched in English', async () => {
    const fa = await boot('/admin/reports', ['reports.read'], core({ summary: () => json(200, { ...summary(), calendar: 'jalali' }) }), 'fa')
    await found('.currency-card')
    expect(fa.fetchMock.mock.calls.map((c) => String(c[0]))).toContain('/api/v1/reports/summary?range=last_30_days&group=day&calendar=jalali')
    expect(text(document.querySelector('.charts-card .small:last-child'))).toContain('از شنبه شروع')
    // the CSV links name the calendar core answered with
    const u = new URL(document.querySelector<HTMLAnchorElement>('.export-summary')!.getAttribute('href')!, 'https://x.example')
    expect(u.searchParams.get('calendar')).toBe('jalali')
    const { fetchMock } = await boot('/admin/reports', ['reports.read'], core({}))
    await found('.currency-card')
    expect(fetchMock.mock.calls.map((c) => String(c[0]))).not.toContain('/api/v1/reports/summary?range=last_30_days&group=day&calendar=jalali')
    expect(text(document.querySelector('.charts-card .small:last-child'))).toContain('start on Monday')
    const jalali = [...document.querySelectorAll<HTMLElement>('.calendar [role="button"], .calendar button')].find((e) => text(e) === 'Jalali')
    expect(jalali).toBeDefined()
    jalali!.click()
    await vi.waitFor(() => expect(fetchMock.mock.calls.map((c) => String(c[0]))).toContain('/api/v1/reports/summary?range=last_30_days&group=day&calendar=jalali'))
  })

  // The calendar switch: Jalali or Gregorian decides every date the page shows or takes. The API keeps asking in
  // Gregorian ISO days; what changes is how a person reads, types and picks them.
  describe('the calendar', () => {
    const urls = (fetchMock: { mock: { calls: unknown[][] } }) => fetchMock.mock.calls.map((c) => String(c[0]))
    const pressed = (name: string) =>
      [...document.querySelectorAll<HTMLElement>('.calendar [role="button"], .calendar button')].find((e) => text(e) === name)
    const choose = (name: string) => {
      const b = pressed(name)
      expect(b, name).toBeDefined()
      b!.click()
    }
    const range = (label: string) => {
      const b = [...document.querySelectorAll<HTMLElement>('.range [role="button"], .range button')].find((e) => text(e) === label)
      expect(b, label).toBeDefined()
      b!.click()
    }
    // Every place a date is written: the covered days, the chart axes, tooltips and summaries, the table's periods.
    const dates = () => ({
      covers: text(document.querySelector('.covers')),
      axes: [...document.querySelectorAll('.chart .axis')].map((a) => [...a.querySelectorAll('span')].map(text)),
      tooltips: [...document.querySelectorAll('.chart[data-currency="IRT"] .slot')].map((e) => e.getAttribute('title')),
      peak: document.querySelector('.chart[data-currency="USDT"] .plot')?.getAttribute('aria-label'),
      rows: [...document.querySelectorAll('.period-table tbody tr td:first-child')].map(text),
    })
    const zone = '⁨Asia/Tehran⁩'

    it('switches every date of the page to the calendar chosen, and back, in English', async () => {
      const { fetchMock } = await boot('/admin/reports', ['reports.read'], core({}))
      await found('.currency-card')
      const gregorian = {
        covers: `15 Oct 2026 to 17 Oct 2026 · time zone ${zone}`,
        axes: [['15 Oct', '17 Oct'], ['15 Oct', '17 Oct']],
        tooltips: ['15 Oct 2026: 100,000 Toman', '16 Oct 2026: 0 Toman', '17 Oct 2026: 250,000 Toman'],
        peak: 'USDT (Tether): highest 2.5 USDT, in 16 Oct 2026',
        rows: ['15 Oct 2026', '16 Oct 2026', '17 Oct 2026'],
      }
      expect(dates()).toEqual(gregorian)
      expect(pressed('Gregorian')?.getAttribute('aria-pressed')).toBe('true')

      choose('Jalali') // 15 Oct 2026 is 23 Mehr 1405
      await vi.waitFor(() => expect(urls(fetchMock)).toContain('/api/v1/reports/summary?range=last_30_days&group=day&calendar=jalali'))
      await vi.waitFor(() => expect(dates().covers).toContain('Mehr 1405'))
      const jalali = {
        covers: `23 Mehr 1405 to 25 Mehr 1405 · time zone ${zone}`,
        axes: [['23 Mehr', '25 Mehr'], ['23 Mehr', '25 Mehr']],
        tooltips: ['23 Mehr 1405: 100,000 Toman', '24 Mehr 1405: 0 Toman', '25 Mehr 1405: 250,000 Toman'],
        peak: 'USDT (Tether): highest 2.5 USDT, in 24 Mehr 1405',
        rows: ['23 Mehr 1405', '24 Mehr 1405', '25 Mehr 1405'],
      }
      expect(dates()).toEqual(jalali)
      expect(pressed('Jalali')?.getAttribute('aria-pressed')).toBe('true')
      expect(localStorage.getItem('bobres.reports.calendar')).toBe('jalali')
      // the files are asked for in the calendar on screen
      const link = new URL(document.querySelector<HTMLAnchorElement>('.export-summary')!.getAttribute('href')!, 'https://x.example')
      expect(link.searchParams.get('calendar')).toBe('jalali')

      choose('Gregorian')
      await vi.waitFor(() => expect(dates().covers).toContain('Oct 2026'))
      expect(dates()).toEqual(gregorian)
      expect(localStorage.getItem('bobres.reports.calendar')).toBe('gregorian')
      expect(new URL(document.querySelector<HTMLAnchorElement>('.export-summary')!.getAttribute('href')!, 'https://x.example').searchParams.has('calendar')).toBe(false)
    })

    it('switches every date of the page to the calendar chosen, and back, in Persian (digits stay Persian)', async () => {
      const { fetchMock } = await boot('/admin/reports', ['reports.read'], core({}), 'fa')
      await found('.currency-card')
      const jalali = {
        covers: `۲۳ مهر ۱۴۰۵ تا ۲۵ مهر ۱۴۰۵ · منطقهٔ زمانی ${zone}`,
        axes: [['۲۳ مهر', '۲۵ مهر'], ['۲۳ مهر', '۲۵ مهر']],
        tooltips: ['۲۳ مهر ۱۴۰۵: ۱۰۰٬۰۰۰ تومان', '۲۴ مهر ۱۴۰۵: ۰ تومان', '۲۵ مهر ۱۴۰۵: ۲۵۰٬۰۰۰ تومان'],
        rows: ['۲۳ مهر ۱۴۰۵', '۲۴ مهر ۱۴۰۵', '۲۵ مهر ۱۴۰۵'],
      }
      // the peak line names the currency by its own catalog entry; the rest is compared exactly
      const { peak, ...rest } = dates()
      expect(rest).toEqual(jalali)
      expect(peak).toContain('در ۲۴ مهر ۱۴۰۵')

      choose('میلادی')
      await vi.waitFor(() => expect(urls(fetchMock)).toContain('/api/v1/reports/summary?range=last_30_days&group=day'))
      await vi.waitFor(() => expect(dates().covers).toContain('اکتبر'))
      const after = dates()
      expect(after.covers).toBe(`۱۵ اکتبر ۲۰۲۶ تا ۱۷ اکتبر ۲۰۲۶ · منطقهٔ زمانی ${zone}`)
      expect(after.axes).toEqual([['۱۵ اکتبر', '۱۷ اکتبر'], ['۱۵ اکتبر', '۱۷ اکتبر']])
      expect(after.tooltips[0]).toBe('۱۵ اکتبر ۲۰۲۶: ۱۰۰٬۰۰۰ تومان')
      expect(after.peak).toContain('در ۱۶ اکتبر ۲۰۲۶')
      expect(after.rows).toEqual(['۱۵ اکتبر ۲۰۲۶', '۱۶ اکتبر ۲۰۲۶', '۱۷ اکتبر ۲۰۲۶'])
      expect(localStorage.getItem('bobres.reports.calendar')).toBe('gregorian')
      // and back: the same days, Jalali again
      choose('شمسی')
      await vi.waitFor(() => expect(dates().covers).toContain('مهر'))
      expect(dates().rows).toEqual(jalali.rows)
      expect(dates().covers).toBe(jalali.covers)
    })

    it('names the weeks by their first day and a Jalali month by its name, whichever calendar cut them', async () => {
      const bucket = (date: string) => ({ ...summary().buckets[0], date, partial: false })
      const starts: Record<string, string[]> = {
        'week|gregorian': ['2026-10-05', '2026-10-12'], // Mondays
        'week|jalali': ['2026-10-03', '2026-10-10'], // Saturdays
        'month|gregorian': ['2026-10-01', '2026-11-01'],
        'month|jalali': ['2026-09-23', '2026-10-23'], // 1 Mehr and 1 Aban 1405
      }
      const answer = (url: string) => {
        const q = params(url)
        const key = `${q.get('group') || 'day'}|${q.get('calendar') === 'jalali' ? 'jalali' : 'gregorian'}`
        const base = answered(url)
        return json(200, { ...base, buckets: key.startsWith('day') ? base.buckets : starts[key].map(bucket) })
      }
      await boot('/admin/reports', ['reports.read'], core({ summary: answer }))
      await found('.currency-card')
      const periods = () => [...document.querySelectorAll('.period-table tbody tr td:first-child')].map(text)
      const group = async (label: string) => {
        document.querySelector<HTMLElement>('.p-select.group')!.click()
        const option = await vi.waitFor(() => {
          const o = [...document.querySelectorAll<HTMLElement>('[role="option"]')].find((e) => text(e) === label)
          expect(o, label).toBeDefined()
          return o!
        })
        option.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) // PrimeVue picks on mousedown
      }
      await group('Week')
      await vi.waitFor(() => expect(periods()).toEqual(['Week of 5 Oct 2026', 'Week of 12 Oct 2026']))
      choose('Jalali')
      await vi.waitFor(() => expect(periods()).toEqual(['Week of 11 Mehr 1405', 'Week of 18 Mehr 1405']))
      await group('Month')
      await vi.waitFor(() => expect(periods()).toEqual(['Mehr 1405', 'Aban 1405']))
      expect(document.querySelector('.chart[data-currency="IRT"] .slot')?.getAttribute('title')).toBe('Mehr 1405: 100,000 Toman')
      choose('Gregorian')
      await vi.waitFor(() => expect(periods()).toEqual(['Month of 1 Oct 2026', 'Month of 1 Nov 2026']))
    })

    it("starts in the calendar chosen last time, else the language's own, and survives blocked storage", async () => {
      localStorage.setItem('bobres.reports.calendar', 'gregorian')
      const fa = await boot('/admin/reports', ['reports.read'], core({}), 'fa')
      await found('.currency-card')
      // Persian page, Gregorian calendar: remembered, so no Jalali weeks are asked for
      expect(urls(fa.fetchMock)).toContain('/api/v1/reports/summary?range=last_30_days&group=day')
      expect(urls(fa.fetchMock).some((u) => u.includes('calendar=jalali'))).toBe(false)
      expect(pressed('میلادی')?.getAttribute('aria-pressed')).toBe('true')
      expect(text(document.querySelector('.covers'))).toContain('اکتبر ۲۰۲۶')
      localStorage.setItem('bobres.reports.calendar', 'jalali')
      const en = await boot('/admin/reports', ['reports.read'], core({}))
      await found('.currency-card')
      expect(urls(en.fetchMock)).toContain('/api/v1/reports/summary?range=last_30_days&group=day&calendar=jalali')
      expect(text(document.querySelector('.covers'))).toContain('Mehr 1405')
      // something that is no calendar is ignored
      localStorage.setItem('bobres.reports.calendar', 'hijri')
      const other = await boot('/admin/reports', ['reports.read'], core({}))
      await found('.currency-card')
      expect(urls(other.fetchMock).some((u) => u.includes('calendar='))).toBe(false)
    })

    it('opens with storage blocked, in the language\'s calendar, and still switches', async () => {
      const real = Storage.prototype.getItem
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(function (this: Storage, k: string) {
        if (k === 'bobres.lang') return real.call(this, k)
        throw new Error('blocked')
      })
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation((k: string) => {
        if (k !== 'bobres.lang') throw new Error('blocked')
      })
      const { fetchMock } = await boot('/admin/reports', ['reports.read'], core({}), 'fa')
      await found('.currency-card')
      expect(urls(fetchMock)).toContain('/api/v1/reports/summary?range=last_30_days&group=day&calendar=jalali')
      choose('میلادی') // not remembered, but it works
      await vi.waitFor(() => expect(text(document.querySelector('.covers'))).toContain('اکتبر'))
    })

    it('takes a custom range typed in the Jalali calendar as the exact Gregorian days, and shows it in the other calendar on a switch', async () => {
      const { fetchMock } = await boot('/admin/reports', ['reports.read'], core({}), 'fa')
      await found('.currency-card')
      range('دلخواه')
      const from = await found<HTMLInputElement>('.date-from')
      const to = await found<HTMLInputElement>('.date-to')
      // the days on screen, in the Jalali calendar and Persian digits
      expect(from.value).toBe('۱۴۰۵/۰۷/۲۳')
      expect(to.value).toBe('۱۴۰۵/۰۷/۲۵')
      expect(from.placeholder).toMatch(/^[۰-۹]{4}\/[۰-۹]{2}\/[۰-۹]{2}$/)
      expect(from.closest('.date-field')?.querySelector('label')?.getAttribute('for')).toBe(from.id)
      // Latin digits typed in the Persian page are read; the first and the last day of a month of 30 days
      typeInto(from, '1405/07/01')
      typeInto(to, '۱۴۰۵/۰۷/۳۰')
      await vi.waitFor(() => expect(urls(fetchMock)).toContain('/api/v1/reports/summary?from=2026-09-23&to=2026-10-22&group=day&calendar=jalali'))
      expect(from.getAttribute('aria-invalid')).not.toBe('true')
      expect(document.querySelector('.custom-problem')).toBeNull()
      // the year edge: 30 Esfand exists in the leap year 1403 and the new year 1404 starts the day after it
      typeInto(from, '1403/12/30')
      typeInto(to, '1404/01/01')
      await vi.waitFor(() => expect(urls(fetchMock)).toContain('/api/v1/reports/summary?from=2025-03-20&to=2025-03-21&group=day&calendar=jalali'))
      // ... and does not in 1404, nor does a 31st of Mehr: nothing is sent, and the page says what a date looks like
      let before = fetchMock.mock.calls.length
      typeInto(to, '1404/12/30')
      await new Promise((r) => setTimeout(r, 450))
      expect(fetchMock.mock.calls.length).toBe(before)
      expect(to.getAttribute('aria-invalid')).toBe('true')
      expect(text(document.querySelector('.custom-problem'))).toMatch(/^هر تاریخ را به صورت سال، ماه و روز در تقویم شمسی بنویسید، مثلاً .+\.$/)
      typeInto(to, '1405/07/31')
      await new Promise((r) => setTimeout(r, 450))
      expect(fetchMock.mock.calls.length).toBe(before)
      expect(to.getAttribute('aria-invalid')).toBe('true')
      // a date outside the years core reports on is refused here too, with the range in the calendar shown
      typeInto(from, '1300/01/01')
      typeInto(to, '1300/01/05')
      await vi.waitFor(() => expect(text(document.querySelector('.custom-problem'))).toContain('۱۳۷۸/۱۰/۱۱'))
      expect(text(document.querySelector('.custom-problem'))).toContain('۱۴۷۹/۱۰/۱۰')
      expect(fetchMock.mock.calls.length).toBe(before)
      // mid-typing is not an error that clears the text
      typeInto(from, '1405/07/')
      await new Promise((r) => setTimeout(r, 50))
      expect(from.value).toBe('1405/07/')
      // a good range again, then the switch: the same two days, written the other way, with the same request minus the calendar
      typeInto(from, '1405/07/01')
      typeInto(to, '1405/07/30')
      await vi.waitFor(() => expect(document.querySelector('.custom-problem')).toBeNull())
      await vi.waitFor(() => expect(urls(fetchMock).filter((u) => u === '/api/v1/reports/summary?from=2026-09-23&to=2026-10-22&group=day&calendar=jalali').length).toBeGreaterThan(1))
      before = fetchMock.mock.calls.length
      choose('میلادی')
      await vi.waitFor(() => expect(urls(fetchMock)).toContain('/api/v1/reports/summary?from=2026-09-23&to=2026-10-22&group=day'))
      expect(from.value).toBe('۲۰۲۶/۰۹/۲۳')
      expect(to.value).toBe('۲۰۲۶/۱۰/۲۲')
      choose('شمسی')
      await vi.waitFor(() => expect(from.value).toBe('۱۴۰۵/۰۷/۰۱'))
      expect(to.value).toBe('۱۴۰۵/۰۷/۳۰')
      expect(fetchMock.mock.calls.length).toBeGreaterThan(before)
    })

    it('takes a custom range typed in English the same way, with Gregorian days as the default', async () => {
      const { fetchMock } = await boot('/admin/reports', ['reports.read'], core({}))
      await found('.currency-card')
      range('Custom')
      const from = await found<HTMLInputElement>('.date-from')
      const to = await found<HTMLInputElement>('.date-to')
      // a Jalali date typed into the Gregorian field is a different (and here unsupported) day, not silently converted
      typeInto(from, '1405-07-01')
      typeInto(to, '1405-07-30')
      await vi.waitFor(() => expect(text(document.querySelector('.custom-problem'))).toContain('Reports cover the dates from ⁨2000-01-01⁩ to ⁨2100-12-31⁩.'))
      // 29 February exists in 2028 only
      typeInto(from, '2028-02-29')
      typeInto(to, '2028-03-01')
      await vi.waitFor(() => expect(urls(fetchMock)).toContain('/api/v1/reports/summary?from=2028-02-29&to=2028-03-01&group=day'))
      typeInto(from, '2027-02-29')
      await vi.waitFor(() => expect(text(document.querySelector('.custom-problem'))).toContain('Type each date as year, month and day in the Gregorian calendar, for example'))
      // switched to Jalali, the same two days are Jalali dates (and the same request, plus the calendar)
      typeInto(from, '2028-02-29')
      choose('Jalali')
      await vi.waitFor(() => expect(from.value).toBe('1406-12-10'))
      expect(to.value).toBe('1406-12-11')
      await vi.waitFor(() => expect(urls(fetchMock)).toContain('/api/v1/reports/summary?from=2028-02-29&to=2028-03-01&group=day&calendar=jalali'))
    })

    it("picks a day from a month grid in the calendar shown, with the week starting where the calendar's does", async () => {
      const { fetchMock } = await boot('/admin/reports', ['reports.read'], core({}), 'fa')
      await found('.currency-card')
      range('دلخواه')
      const from = await found<HTMLInputElement>('.date-from')
      const open = (field: HTMLInputElement) => field.closest('.date-input')!.querySelector<HTMLButtonElement>('button.open')!
      open(from).click()
      const picker = await found<HTMLElement>('.picker')
      expect(picker.getAttribute('data-calendar')).toBe('jalali')
      expect(text(picker.querySelector('.title'))).toBe('مهر ۱۴۰۵')
      expect([...picker.querySelectorAll('th')].map(text)).toEqual(['ش', 'ی', 'د', 'س', 'چ', 'پ', 'ج']) // Saturday first
      // Mehr starts on a Wednesday: four empty cells, then 1 2 3
      expect([...picker.querySelectorAll('tbody tr:first-child td')].map(text)).toEqual(['', '', '', '', '۱', '۲', '۳'])
      expect(picker.querySelectorAll('.day')).toHaveLength(30)
      // the day on screen is marked
      expect(picker.querySelector('.day.selected')?.getAttribute('data-date')).toBe('2026-10-15')
      expect(text(picker.querySelector('.day.selected'))).toBe('۲۳')
      expect(picker.querySelector('.day[data-date="2026-10-15"]')?.getAttribute('aria-pressed')).toBe('true')
      expect(picker.querySelector('.day[data-date="2026-10-15"]')?.getAttribute('aria-label')).toBe('پنجشنبه ۲۳ مهر ۱۴۰۵')
      // 15 Mehr is 7 October
      picker.querySelector<HTMLButtonElement>('.day[data-date="2026-10-07"]')!.click()
      await vi.waitFor(() => expect(from.value).toBe('۱۴۰۵/۰۷/۱۵'))
      await vi.waitFor(() => expect(urls(fetchMock)).toContain('/api/v1/reports/summary?from=2026-10-07&to=2026-10-17&group=day&calendar=jalali'))
      await vi.waitFor(() => expect(document.querySelector('.picker')).toBeNull()) // a pick closes it
      expect(document.activeElement).toBe(open(from)) // and the focus goes back to the button that opened it

      // months and years: the end of a Jalali year is Esfand, whose 30th a common year does not have
      open(from).click()
      const again = await found<HTMLElement>('.picker')
      const click = (selector: string) => again.querySelector<HTMLButtonElement>(selector)!.click()
      click('.next-month')
      await vi.waitFor(() => expect(text(again.querySelector('.title'))).toBe('آبان ۱۴۰۵'))
      expect(again.querySelectorAll('.day')).toHaveLength(30)
      click('.prev-year')
      click('.prev-year')
      await vi.waitFor(() => expect(text(again.querySelector('.title'))).toBe('آبان ۱۴۰۳'))
      for (let i = 0; i < 4; i++) click('.next-month')
      await vi.waitFor(() => expect(text(again.querySelector('.title'))).toBe('اسفند ۱۴۰۳'))
      expect(again.querySelectorAll('.day')).toHaveLength(30) // 1403 is a leap year
      click('.next-year')
      await vi.waitFor(() => expect(text(again.querySelector('.title'))).toBe('اسفند ۱۴۰۴'))
      expect(again.querySelectorAll('.day')).toHaveLength(29)
      click('.pick-today')
      await vi.waitFor(() => expect(document.querySelector('.picker')).toBeNull())
      // the Gregorian grid starts on Monday
      choose('میلادی')
      await vi.waitFor(() => expect(from.value).toMatch(/^[۰-۹]{4}\/[۰-۹]{2}\/[۰-۹]{2}$/))
      open(from).click()
      const greg = await found<HTMLElement>('.picker')
      expect(greg.getAttribute('data-calendar')).toBe('gregorian')
      expect([...greg.querySelectorAll('th')].map(text)).toEqual(['د', 'س', 'چ', 'پ', 'ج', 'ش', 'ی'])
      expect(text(greg.querySelector('.title'))).toMatch(/^[^\d۰-۹]+ [۰-۹]{4}$/)
    })

    it('asks the ledger and audit files for their Jalali date column when the calendar is Jalali', async () => {
      const lists: Answer = (url) => (url.startsWith('/api/v1/ledger') || url.startsWith('/api/v1/audit') ? json(200, { items: [], total: 0, page: 1, size: 25 }) : undefined)
      const link = async (path: string, lang: 'en' | 'fa') => {
        await boot(path, ['ledger.read', 'audit.read'], lists, lang)
        const a = await found<HTMLAnchorElement>('a[download]')
        return new URL(a.getAttribute('href')!, 'https://x.example')
      }
      // nothing chosen: the language's calendar
      let u = await link('/admin/ledger', 'fa')
      expect(u.pathname).toBe('/api/v1/ledger.csv')
      expect(u.searchParams.get('calendar')).toBe('jalali')
      u = await link('/admin/audit', 'fa')
      expect(u.pathname).toBe('/api/v1/audit.csv')
      expect(u.searchParams.get('calendar')).toBe('jalali')
      u = await link('/admin/ledger', 'en')
      expect(u.searchParams.has('calendar')).toBe(false)
      u = await link('/admin/audit', 'en')
      expect(u.searchParams.has('calendar')).toBe(false)
      // the calendar the Reports page was set to wins, either way
      localStorage.setItem('bobres.reports.calendar', 'jalali')
      u = await link('/admin/ledger', 'en')
      expect(u.searchParams.get('calendar')).toBe('jalali')
      localStorage.setItem('bobres.reports.calendar', 'gregorian')
      u = await link('/admin/audit', 'fa')
      expect(u.searchParams.has('calendar')).toBe(false)
    })

    it('moves around the month grid with the arrow keys the way the page reads, and stops at the supported years', async () => {
      await boot('/admin/reports', ['reports.read'], core({}))
      await found('.currency-card')
      range('Custom')
      const from = await found<HTMLInputElement>('.date-from')
      from.closest('.date-input')!.querySelector<HTMLButtonElement>('button.open')!.click()
      const picker = await found<HTMLElement>('.picker')
      const day = (iso: string) => picker.querySelector<HTMLButtonElement>(`.day[data-date="${iso}"]`)
      const press = async (key: string, shiftKey = false) => {
        const target = (document.activeElement as HTMLElement | null)?.closest('[data-date]') ?? picker.querySelector('[data-date][tabindex="0"]')!
        target.dispatchEvent(new KeyboardEvent('keydown', { key, shiftKey, bubbles: true, cancelable: true }))
        await nextTick() // the grid follows, and the new day takes the focus
      }
      const focused = () => (document.activeElement as HTMLElement | null)?.dataset.date
      // only the focused day is in the tab order
      expect(picker.querySelectorAll('.day[tabindex="0"]')).toHaveLength(1)
      expect(day('2026-10-15')?.getAttribute('tabindex')).toBe('0')
      day('2026-10-15')!.focus()
      await press('ArrowRight')
      expect(focused()).toBe('2026-10-16') // English reads left to right: right is the next day
      await press('ArrowDown')
      expect(focused()).toBe('2026-10-23')
      await press('ArrowLeft')
      expect(focused()).toBe('2026-10-22')
      await press('ArrowUp')
      expect(focused()).toBe('2026-10-15')
      await press('Home') // the week starts on Monday: 12 Oct
      expect(focused()).toBe('2026-10-12')
      await press('End')
      expect(focused()).toBe('2026-10-18')
      // into the next month and back; the grid follows
      await press('ArrowDown')
      await press('ArrowDown')
      await press('ArrowDown')
      expect(focused()).toBe('2026-11-08')
      await vi.waitFor(() => expect(text(picker.querySelector('.title'))).toBe('November 2026'))
      await press('PageUp')
      expect(focused()).toBe('2026-10-08')
      await press('PageDown', true) // a year ahead
      expect(focused()).toBe('2027-10-08')
      // the supported years end at 2000 and 2100
      for (let i = 0; i < 30; i++) await press('PageUp', true)
      expect(focused()).toBe('2000-10-08')
      await press('PageUp', true)
      expect(focused()).toBe('2000-10-08') // stays: no month before 2000 is offered
      expect(picker.querySelector<HTMLButtonElement>('.prev-year')!.disabled).toBe(true) // a year back is before 2000
      day('2000-10-08')!.click()
      await vi.waitFor(() => expect(from.value).toBe('2000-10-08'))
    })
  })

  it('draws a long daily range as a dense chart that shrinks to its card (no gaps between hairline bars)', async () => {
    const long = summary()
    long.buckets = Array.from({ length: 130 }, (_, i) => ({ ...summary().buckets[i % 3], date: new Date(Date.UTC(2025, 0, 1 + i)).toISOString().slice(0, 10) }))
    await boot('/admin/reports', ['reports.read'], core({ summary: () => json(200, long) }))
    await found('.chart')
    const plot = document.querySelector('.chart .plot')!
    expect(plot.querySelectorAll('.slot')).toHaveLength(130)
    expect(plot.classList.contains('dense')).toBe(true)
    // a short range keeps its gaps
    await boot('/admin/reports', ['reports.read'], core({}))
    await found('.chart')
    expect(document.querySelector('.chart .plot')?.classList.contains('dense')).toBe(false)
  }, 20_000)

  it('tells a report that ran out of time to choose a shorter range, in the language', async () => {
    await boot('/admin/reports', ['reports.read'], core({ summary: () => json(503, { error: 'timeout', message: 'the report took too long: choose a shorter range' }) }))
    const err = await found('.report-error')
    expect(text(err)).toContain('The report took too long. Choose a shorter range.')
    expect(text(err)).not.toContain('Something went wrong')
    await boot('/admin/reports', ['reports.read'], core({ summary: () => json(503, { error: 'timeout', message: 'x' }) }), 'fa')
    expect(text(await found('.report-error'))).toContain('بازهٔ کوتاه‌تری انتخاب کنید')
  })

  // The line under the page's hint: is the daily summary in Telegram on, when, in which zone, and
  // where to change it (for staff who may change settings).
  describe('the daily summary line', () => {
    const line = () => document.querySelector<HTMLElement>('.daily-summary')
    const status = (over: object) => () => json(200, { enabled: true, time: '07:30', tz: 'America/New_York', ...over })

    it('says it is on, when and in which zone, and links to the switch for staff who may change settings', async () => {
      await boot('/admin/reports', ['reports.read', 'settings.write'], core({ daily: status({}) }))
      await found('.daily-summary')
      expect(text(line())).toBe("Daily summary in Telegram: on. Yesterday's sales go to every owner and admin at ⁨07:30⁩ (⁨America/New_York⁩). Daily summary settings")
      expect(line()!.classList.contains('is-on')).toBe(true)
      const link = line()!.querySelector<HTMLAnchorElement>('a.daily-link')!
      expect(link.getAttribute('href')).toBe('/admin/settings#setting-reports-daily-summary')
      expect(text(link)).toBe('Daily summary settings')
      await found('.currency-card') // the report is there too
    })

    it('says it is off', async () => {
      await boot('/admin/reports', ['reports.read', 'settings.write'], core({ daily: status({ enabled: false, time: '09:00', tz: 'Asia/Tehran' }) }))
      await found('.daily-summary')
      expect(text(line())).toBe("Daily summary in Telegram: off. Turn it on to get yesterday's sales in a message every day. Daily summary settings")
      expect(line()!.classList.contains('is-off')).toBe(true)
    })

    it('has no link for staff who may not read the settings', async () => {
      await boot('/admin/reports', ['reports.read'], core({ daily: status({}) }))
      await found('.daily-summary')
      expect(text(line())).toBe("Daily summary in Telegram: on. Yesterday's sales go to every owner and admin at ⁨07:30⁩ (⁨America/New_York⁩).")
      expect(line()!.querySelector('a')).toBeNull()
    })

    it('is left out when core cannot say, and the report still shows', async () => {
      await boot('/admin/reports', ['reports.read', 'settings.write'], core({ daily: () => json(503, { error: 'unavailable', message: 'down' }) }))
      await found('.currency-card')
      expect(line()).toBeNull()
      expect(document.querySelector('.report-error')).toBeNull()
    })

    it('reads in Persian, with the time and zone kept whole', async () => {
      await boot('/admin/reports', ['reports.read', 'settings.write'], core({ daily: status({}) }), 'fa')
      await found('.daily-summary')
      expect(text(line())).toBe('خلاصهٔ روزانه در تلگرام: روشن. فروش دیروز هر روز ساعت ⁨07:30⁩ (⁨America/New_York⁩) برای همهٔ مالک‌ها و مدیرها فرستاده می‌شود. تنظیمات خلاصهٔ روزانه')
    })
  })

  it('has the menu entry only for staff with reports.read, and guards the route', async () => {
    const admin = await boot('/admin/reports', ['overview.read', 'reports.read'], core({}))
    await found('.currency-card')
    const links = (root: ParentNode) => [...root.querySelectorAll<HTMLAnchorElement>('nav a.item')].map((a) => a.getAttribute('href'))
    expect(links(document)).toContain('/admin/reports')
    expect(admin.router.currentRoute.value.name).toBe('reports')

    const support = await boot('/admin/reports', ['overview.read', 'users.read', 'payments.read'], core({}))
    await vi.waitFor(() => expect(support.router.currentRoute.value.name).toBe('overview'))
    await found('nav a.item')
    expect(links(document)).not.toContain('/admin/reports')
    expect(support.fetchMock.mock.calls.map((c) => String(c[0])).filter((u) => u.includes('/reports/'))).toEqual([])
  })

  // The money rule, as the whole page: a total that adds Toman to USDT would show up as one of these numbers.
  it('never shows the sum of two currencies', async () => {
    await boot('/admin/reports', ['reports.read'], core({}))
    await found('.currency-card')
    const body = text(document.body)
    expect(body).not.toContain(money({ amount: 350_000 + 2_500_000, currency: 'IRT' }, 'en'))
    expect(body).not.toContain('2,850,000')
  })
})

// The overview shows revenue to those who may read the reports. Core leaves it out
// of support's answer, and the page then shows no card for it (not an empty one,
// and no "NaN").
describe('revenue on the overview', () => {
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  const base = {
    users: { total: 10, new_24h: 1, new_7d: 2 }, services: { active: 5, expiring: 1, ended: 0 }, orders: { paid_24h: 1, provision_failed: 0 },
    pending_payments: 0, panel: { healthy: true, detail: '' }, recent_orders: [],
  }

  async function boot(perms: string[], overview: Record<string, unknown>) {
    unmount()
    localStorage.setItem('bobres.lang', 'en')
    vi.resetModules()
    window.history.replaceState(null, '', '/admin/')
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url === '/api/v1/me') {
          return json(200, {
            user: { id: 'u1', telegram_id: 1, username: 'boss', role: 'admin', language: 'en' }, permissions: perms, csrf: 'c',
            session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
          })
        }
        if (url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null })
        if (url === '/api/v1/overview') return json(200, overview)
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
  }

  afterEach(() => {
    unmount()
    localStorage.removeItem('bobres.lang')
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  it('shows both revenue cards to staff who read reports, per currency', async () => {
    await boot(['overview.read', 'reports.read'], {
      ...base,
      revenue: { day: [{ amount: 120_000, currency: 'IRT' }], month: [{ amount: 3_500_000, currency: 'IRT' }, { amount: 2_500_000, currency: 'USDT' }] },
    })
    await vi.waitFor(() => expect(document.querySelector('.revenue-month .mid')?.textContent).toContain('2.5 USDT'))
    expect(document.querySelector('.revenue-day .mid')?.textContent).toBe('120,000 Toman')
    expect(document.querySelector('.revenue-month .mid')?.textContent).toBe('3,500,000 Toman · 2.5 USDT')
  })

  it('shows no revenue card when core leaves revenue out (support), and no empty or NaN card', async () => {
    await boot(['overview.read'], { ...base })
    await vi.waitFor(() => expect(document.querySelector('.stat .big')?.textContent).toBe('10')) // the overview has loaded
    expect(document.querySelector('.revenue-day')).toBeNull()
    expect(document.querySelector('.revenue-month')).toBeNull()
    expect(document.body.textContent).not.toMatch(/NaN|undefined/)
    expect(document.body.textContent).not.toContain('Revenue')
  })

  it('shows none either when the answer has no revenue though the role could read it (an older core)', async () => {
    await boot(['overview.read', 'reports.read'], { ...base })
    await vi.waitFor(() => expect(document.querySelector('.stat .big')?.textContent).toBe('10')) // the overview has loaded
    expect(document.querySelector('.revenue-day')).toBeNull()
    expect(document.body.textContent).not.toMatch(/NaN|undefined/)
  })
})
