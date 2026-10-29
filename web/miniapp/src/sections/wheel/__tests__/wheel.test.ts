import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, type Component } from 'vue'
import { resetSession, type Me } from '../../../api'
import { allSections } from '../../../sections'
import { setLang } from '../../../i18n'
import { state } from '../../../store'
import WheelView from '../WheelView.vue'
import messages from '../messages'
import { chanceOf, restAngle, sectorPath, segmentFor, segmentsOf, targetRotation } from '../wheel'
import '../../index'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const ME: Me = {
  user: { id: 'u', username: '', language: 'en' },
  wallets: [],
  brand: { name: 'Acme', color: '', support: '', support_url: '', bot_url: '' },
  maintenance: false,
  top_up: { currency: 'IRT', min: 1, max: 2 },
  features: { wheel: true },
}

// A wheel of three pieces: 1,000 (weight 3), 5,000 (weight 1) and no prize (weight 4), so 135, 45 and 180 degrees.
const WHEEL = {
  day: '2026-10-28', resets_at: 1_793_000_000, reason: '', orders: 2, min_orders: 1, spun: false, prize: 0, open: true,
  prizes: [{ amount: 1000, weight: 3 }, { amount: 5000, weight: 1 }], no_prize_weight: 4, total_weight: 8, currency: 'IRT', scale: 0,
}
const SPIN = { status: 'won', prize: 5000, index: 1, balance: 5000, day: '2026-10-28', resets_at: 1_793_000_000, orders: 2, min_orders: 1, currency: 'IRT', scale: 0 }

type Handler = (body: Record<string, unknown> | undefined) => Response
function mockApi(routes: Record<string, Handler>) {
  const calls: string[] = []
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
    const key = `${init?.method ?? 'GET'} ${String(url).replace('/api/mini/v1', '')}`
    calls.push(key)
    if (key === 'POST /auth') return json(200, { ...ME, token: 'tok' })
    if (key === 'GET /wallet') return json(200, { wallets: [{ currency: 'IRT', balance: 5000, credit_limit: 0, scale: 0 }], ledger: [] })
    return routes[key]?.(init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined) ?? json(404, { error: 'not_found', message: key })
  })
  return calls
}

const mounted: (() => void)[] = []
function mount(component: Component) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp(component)
  app.mount(el)
  mounted.push(() => {
    app.unmount()
    el.remove()
  })
  return el
}
const text = (el: Element) => el.textContent?.replace(/\s+/g, ' ').trim() ?? ''
const spinButton = (el: Element) => [...el.querySelectorAll('button')].find((b) => /Spin the wheel|Spinning|بچرخان|در حال/.test(text(b))) as HTMLButtonElement
const seen = (el: Element, s: string) => vi.waitFor(() => expect(text(el)).toContain(s))
const rotationOf = (el: Element) => Number(el.querySelector('[data-wheel-disc]')?.getAttribute('data-rotation'))
/** the angle of the wheel that is under the pointer once it has turned by rotation */
const underPointer = (rotation: number) => (((-rotation) % 360) + 360) % 360

beforeEach(() => {
  resetSession()
  setLang('en')
  window.location.hash = '#tgWebAppData=' + encodeURIComponent('user=%7B%7D&hash=abc')
  state.me = { ...ME, features: { ...ME.features } }
})
afterEach(() => {
  mounted.splice(0).forEach((f) => f())
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('the geometry of the wheel', () => {
  it('gives each piece the width of its weight, the piece that pays nothing last, and covers the circle', () => {
    const segs = segmentsOf(WHEEL.prizes, 4)
    expect(segs.map((s) => [s.index, s.start, s.end])).toEqual([[0, 0, 135], [1, 135, 180], [-1, 180, 360]])
    expect(segmentsOf(WHEEL.prizes, 0).map((s) => s.index)).toEqual([0, 1])
    expect(segmentsOf(WHEEL.prizes, 0).at(-1)?.end).toBe(360)
    expect(segmentsOf([], 0)).toEqual([])
    // thirds do not leave a gap at the top
    const thirds = segmentsOf([{ amount: 1, weight: 1 }, { amount: 2, weight: 1 }, { amount: 3, weight: 1 }], 0)
    expect(thirds.at(-1)?.end).toBe(360)
    expect(thirds[1].start).toBeCloseTo(120)
    expect(segmentFor(segs, 1)?.amount).toBe(5000)
    expect(segmentFor(segs, -1)?.weight).toBe(4)
    expect(segmentFor(segs, 7)).toBeUndefined()
    expect(chanceOf(3, 8)).toBe(0.375)
    expect(chanceOf(1, 0)).toBe(0)
  })

  it('turns forward to a piece, always whole turns and always ending with the piece under the pointer', () => {
    const segs = segmentsOf(WHEEL.prizes, 4)
    for (const current of [0, 725, 1234.5, 3600]) {
      for (const seg of segs) {
        for (const frac of [0, 0.3, 0.5, 1]) {
          const to = targetRotation(current, restAngle(seg, frac))
          expect(to).toBeGreaterThanOrEqual(current + 5 * 360 - 1e-9)
          const under = underPointer(to)
          expect(under).toBeGreaterThan(seg.start)
          expect(under).toBeLessThan(seg.end)
        }
      }
    }
    // a rest angle stays off the edges of its piece
    const seg = segs[0]
    expect(restAngle(seg, 0)).toBeGreaterThan(seg.start)
    expect(restAngle(seg, 1)).toBeLessThan(seg.end)
    expect(restAngle(seg, -3)).toBe(restAngle(seg, 0))
    expect(restAngle(seg, 9)).toBe(restAngle(seg, 1))
  })

  it('draws a piece as an arc from the centre', () => {
    expect(sectorPath(100, 100, 50, 0, 90)).toBe('M100 100 L100 50 A50 50 0 0 1 150 100 Z')
    expect(sectorPath(100, 100, 50, 0, 270)).toContain(' 0 1 1 ')
  })
})

describe('the wheel section', () => {
  it('is registered and visible only when core says the wheel is on for this customer', () => {
    const s = allSections().find((x) => x.id === 'wheel')
    expect(s).toBeDefined()
    expect(s!.visible({ ...ME, features: { wheel: true } })).toBe(true)
    expect(s!.visible({ ...ME, features: {} })).toBe(false)
    expect(s!.visible({ ...ME, features: { wheel: false } })).toBe(false)
  })

  it('has its texts in both languages with the same values to fill in', () => {
    expect(Object.keys(messages.fa).sort()).toEqual(Object.keys(messages.en).sort())
    const names = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()
    for (const k of Object.keys(messages.en)) expect(names(messages.fa[k]), k).toEqual(names(messages.en[k]))
  })

  it('shows the pieces with their chances, and spins to the piece core names', async () => {
    vi.useFakeTimers()
    const calls = mockApi({ 'GET /wheel': () => json(200, WHEEL), 'POST /wheel/spin': () => json(200, SPIN) })
    const el = mount(WheelView)
    await seen(el, 'What you can win')
    expect(text(el)).toContain('1,000 Toman37.5%')
    expect(text(el)).toContain('5,000 Toman12.5%')
    expect(text(el)).toContain('No prize50%')
    expect(el.querySelectorAll('[data-segment]')).toHaveLength(3)
    expect(spinButton(el).disabled).toBe(false)
    expect(rotationOf(el)).toBe(0)

    spinButton(el).click()
    await vi.advanceTimersByTimeAsync(10)
    // under way: the wheel is turning towards the 5,000 piece (135 to 180 degrees), the button is busy, nothing is announced yet
    const target = rotationOf(el)
    expect(target).toBeGreaterThanOrEqual(5 * 360)
    expect(underPointer(target)).toBeGreaterThan(135)
    expect(underPointer(target)).toBeLessThan(180)
    expect(el.querySelector<HTMLElement>('[data-wheel-disc]')!.style.transition).toContain('transform')
    expect(spinButton(el).disabled).toBe(true)
    expect(text(el)).not.toContain('You won')

    await vi.advanceTimersByTimeAsync(4300)
    await seen(el, 'You won 5,000 Toman!')
    expect(text(el)).toContain('Balance: 5,000 Toman')
    expect(text(el)).toContain('Next spin:')
    expect(spinButton(el).disabled).toBe(true) // one spin a day
    expect(calls.filter((c) => c === 'POST /wheel/spin')).toHaveLength(1)
    expect(calls).toContain('GET /wallet') // the balance elsewhere in the app follows
  })

  it('lands on the piece that pays nothing for a spin that won nothing', async () => {
    vi.useFakeTimers()
    mockApi({ 'GET /wheel': () => json(200, WHEEL), 'POST /wheel/spin': () => json(200, { ...SPIN, status: 'lost', prize: 0, index: -1, balance: 0 }) })
    const el = mount(WheelView)
    await seen(el, 'What you can win')
    spinButton(el).click()
    await vi.advanceTimersByTimeAsync(10)
    expect(underPointer(rotationOf(el))).toBeGreaterThan(180)
    await vi.advanceTimersByTimeAsync(4300)
    await seen(el, 'No prize this time.')
    expect(text(el)).not.toContain('Balance:')
  })

  it('does not draw twice for a double tap', async () => {
    vi.useFakeTimers()
    const calls = mockApi({ 'GET /wheel': () => json(200, WHEEL), 'POST /wheel/spin': () => json(200, SPIN) })
    const el = mount(WheelView)
    await seen(el, 'What you can win')
    const b = spinButton(el)
    b.click()
    b.click()
    b.click()
    await vi.advanceTimersByTimeAsync(5000)
    await seen(el, 'You won 5,000 Toman!')
    expect(calls.filter((c) => c === 'POST /wheel/spin')).toHaveLength(1)
  })

  it('shows the spin made earlier today, when the app was behind, without turning', async () => {
    mockApi({ 'GET /wheel': () => json(200, WHEEL), 'POST /wheel/spin': () => json(200, { ...SPIN, status: 'already', prize: 1000, index: 0, balance: 1000 }) })
    const el = mount(WheelView)
    await seen(el, 'What you can win')
    spinButton(el).click()
    await seen(el, 'You have spun today. Your prize today: 1,000 Toman.')
    expect(rotationOf(el)).toBe(0)
    expect(spinButton(el).disabled).toBe(true)
  })

  it('says the day’s prizes are gone and closes the button, when the budget cannot cover the prize', async () => {
    mockApi({ 'GET /wheel': () => json(200, WHEEL), 'POST /wheel/spin': () => json(200, { ...SPIN, status: 'closed', prize: 0, index: -1, balance: 0 }) })
    const el = mount(WheelView)
    await seen(el, 'What you can win')
    spinButton(el).click()
    await seen(el, 'Today’s prizes have all been given out. Your spin was not used: come back tomorrow.')
    expect(rotationOf(el)).toBe(0)
    expect(spinButton(el).disabled).toBe(true)
  })

  it('tells a customer without enough paid orders what it takes, and never calls spin', async () => {
    const calls = mockApi({ 'GET /wheel': () => json(200, { ...WHEEL, reason: 'orders', orders: 1, min_orders: 3 }) })
    const el = mount(WheelView)
    await seen(el, 'Paid orders needed to spin: 3. Yours so far: 1.')
    expect(spinButton(el).disabled).toBe(true)
    spinButton(el).click()
    await nextTick()
    expect(calls.filter((c) => c === 'POST /wheel/spin')).toHaveLength(0)
  })

  it('shows today’s spin when the customer has made it, and when the next one is', async () => {
    mockApi({ 'GET /wheel': () => json(200, { ...WHEEL, spun: true, prize: 1000 }) })
    const el = mount(WheelView)
    await seen(el, 'You have spun today. Your prize today: 1,000 Toman.')
    expect(text(el)).toContain('Next spin:')
    expect(spinButton(el).disabled).toBe(true)
  })

  it('says so when the day’s budget is gone before the customer spins', async () => {
    mockApi({ 'GET /wheel': () => json(200, { ...WHEEL, open: false }) })
    const el = mount(WheelView)
    await seen(el, 'Today’s prizes have all been given out.')
    expect(spinButton(el).disabled).toBe(true)
  })

  it('says the wheel is not available when core says there is none, and offers a retry on a failure', async () => {
    mockApi({ 'GET /wheel': () => json(404, { error: 'not_found', message: 'the wheel is not available' }) })
    const gone = mount(WheelView)
    await seen(gone, 'The wheel is not available right now.')

    let fails = true
    mockApi({ 'GET /wheel': () => (fails ? json(500, { error: 'internal', message: 'x' }) : json(200, WHEEL)) })
    const el = mount(WheelView)
    await vi.waitFor(() => expect(el.querySelector('[role="alert"]')).not.toBeNull())
    fails = false
    ;[...el.querySelectorAll('button')].find((b) => /Try again|Retry/i.test(text(b)))?.click()
    await seen(el, 'What you can win')
  })

  it('is in Persian too, with Persian digits', async () => {
    setLang('fa')
    mockApi({ 'GET /wheel': () => json(200, WHEEL), 'POST /wheel/spin': () => json(200, SPIN) })
    const el = mount(WheelView)
    await seen(el, 'جایزه‌های ممکن')
    expect(text(el)).toContain('گردونه شانس')
    expect(text(el)).toContain('۱٬۰۰۰ تومان')
    expect(text(el)).toContain('۳۷٫۵٪')
    expect(text(el)).toContain('بدون جایزه')
    expect(spinButton(el).disabled).toBe(false)
  })
})
