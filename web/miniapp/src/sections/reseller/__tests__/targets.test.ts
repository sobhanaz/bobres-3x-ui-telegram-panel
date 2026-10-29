import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { messages } from '../../../i18n'
import '../index'
import ResellerSection from '../ResellerSection.vue'
import { resetUi } from '../state'
import { counts, fakeCore, find, mount, settle, startAs, text, unmountAll } from './helpers'

// The reseller's panel carries this month against the owner's targets: the count, each tier with its bonus (ticked once paid), what is left to the
// next one and the days left; nothing while the owner has them off.

const panel = (targets?: unknown) => ({
  tier: 'Gold', discount_percent: 20, commission_percent: 10,
  balances: [{ currency: 'IRT', balance: -120_000, credit_limit: 500_000, available: 380_000, scale: 0 }],
  customers: 12, commission_earned: [{ currency: 'IRT', amount: 45_000, scale: 0 }], ref_code: 'abcd2345',
  invite_link: 'https://t.me/acme_bot?start=r_abcd2345', counts: counts(), config_prefix: '', panel_group: '', targets,
})

const month = (over: Record<string, unknown> = {}) => ({
  period: '2026-10', calendar: 'gregorian', from: '2026-10-01', to: '2026-10-31', ends_at: Math.floor(Date.now() / 1000) + 5 * 86_400 + 3600,
  sold: 2, remaining: 2, bonus_paid: 10_000, currency: 'IRT', scale: 0,
  tiers: [
    { threshold: 2, bonus: 10_000, reached: true, paid: true },
    { threshold: 4, bonus: 50_000, reached: false, paid: false },
  ],
  ...over,
})

beforeEach(() => {
  startAs('en')
  resetUi()
})
afterEach(() => {
  unmountAll()
  vi.restoreAllMocks()
})

describe('the monthly target on the reseller panel', () => {
  it('shows the count, the tiers with their bonus, what is left and the days left', async () => {
    fakeCore(() => panel(month()))
    const { el, unmount } = mount(ResellerSection)
    await settle()
    const card = find(el, '[data-targets]')
    if (!card) throw new Error('no target card: ' + text(el))
    const t = text(card)
    for (const want of ['Monthly target', 'From ', ' to ', 'Services sold this month', '2', '✅', '2 sold: bonus 10,000 Toman', '⬜', '4 sold: bonus 50,000 Toman',
      'Next bonus: 50,000 Toman · still to sell: 2', 'Bonus earned this month: 10,000 Toman', 'Days left in this month: 6']) {
      expect(t, want).toContain(want)
    }
    const bar = find<HTMLProgressElement>(card, 'progress')
    expect(bar?.max).toBe(4)
    expect(bar?.value).toBe(2)
    unmount()
  })

  it('says every tier is reached, and keeps the bar full', async () => {
    fakeCore(() =>
      panel(month({ sold: 5, remaining: 0, bonus_paid: 60_000, tiers: [{ threshold: 2, bonus: 10_000, reached: true, paid: true }, { threshold: 4, bonus: 50_000, reached: true, paid: true }] })),
    )
    const { el, unmount } = mount(ResellerSection)
    await settle()
    const card = find(el, '[data-targets]')
    if (!card) throw new Error('no target card')
    expect(text(card)).toContain('You have reached every tier this month. 🎉')
    expect(text(card)).not.toContain('Next bonus')
    expect(find<HTMLProgressElement>(card, 'progress')?.value).toBe(4) // no further than the top tier
    unmount()
  })

  it('has no card while the owner has targets off', async () => {
    fakeCore(() => panel())
    const { el, unmount } = mount(ResellerSection)
    await settle()
    expect(text(el)).toContain('Tier: Gold')
    expect(find(el, '[data-targets]')).toBeNull()
    unmount()
  })

  it('reads in Persian, with Persian digits', async () => {
    startAs('fa')
    fakeCore(() => panel(month()))
    const { el, unmount } = mount(ResellerSection)
    await settle()
    const card = find(el, '[data-targets]')
    if (!card) throw new Error('no target card')
    const t = text(card)
    for (const want of ['هدف ماهانه', 'سرویس فروخته‌شده در این ماه', '۲ فروش: پاداش ۱۰٬۰۰۰ تومان', '۴ فروش: پاداش ۵۰٬۰۰۰ تومان',
      'پاداش بعدی: ۵۰٬۰۰۰ تومان · فروش باقی‌مانده: ۲', 'پاداش دریافتی این ماه: ۱۰٬۰۰۰ تومان', 'روزهای باقی‌ماندهٔ این ماه: ۶']) {
      expect(t, want).toContain(want)
    }
    unmount()
  })

  it('has every text in both languages', () => {
    const keys = Object.keys(messages.en).filter((k) => k.startsWith('targets.'))
    expect(keys.length).toBeGreaterThanOrEqual(10)
    for (const k of keys) {
      expect(messages.fa[k], k).toBeTruthy()
      expect(messages.en[k], k).toBeTruthy()
    }
  })
})
