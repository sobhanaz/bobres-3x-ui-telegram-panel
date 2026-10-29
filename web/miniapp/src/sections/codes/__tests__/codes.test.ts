import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Me } from '../../../api'
import { messages, setLang } from '../../../i18n'
import { allSections } from '../../../sections'
import { state } from '../../../store'
import { clipboard, json, mount, noTelegram, settle, signedIn, type Call, type Mounted } from '../../../__tests__/helpers'
import CodesView from '../CodesView.vue'
import '../../index'

// My codes: the customer's own discount (reward) codes and the gift codes they applied. The app asks core for a page of the signed-in
// customer's list and draws what comes; a discount code is whole and can be copied, a gift code is only its last characters.

const DAY = 86_400
const at = (days: number) => 1_790_000_000 + days * DAY

const reward = (over: Record<string, unknown> = {}) => ({
  id: 'THX-AB23CD45', kind: 'reward', code: 'THX-AB23CD45', state: 'unused', percent: 20, scale: 0, expires_at: at(5), used_at: null, received_at: at(-1),
  plan: { en: 'Monthly 50GB', fa: 'ماهانه ۵۰ گیگ' }, ...over,
})
const gift = (over: Record<string, unknown> = {}) => ({
  id: 'red-1', kind: 'gift', tail: 'JKMN', state: 'used', amount: 25_000, currency: 'IRT', scale: 0, expires_at: null, used_at: at(-3), received_at: at(-3), ...over,
})
const page = (codes: unknown[], total = codes.length, p = 1) => ({ codes, total, page: p, size: 10 })

const cards = (m: Mounted) => Array.from(m.el.querySelectorAll('li.code')) as HTMLElement[]
const words = (el: Element) => (el.textContent ?? '').replace(/[⁦-⁩]/g, '').replace(/\s+/g, ' ').trim()
const list = (calls: Call[]) => calls.filter((c) => c.path.startsWith('/codes'))

beforeEach(() => {
  setLang('en')
  state.gate = ''
  state.me = { top_up: { currency: 'IRT', min: 10000, max: 50000000 }, brand: { name: 'Acme', bot_url: '' }, features: { codes: true } } as unknown as Me
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  noTelegram()
  document.body.innerHTML = ''
})

describe('the My codes section', () => {
  it('is registered, and shown when core says so', () => {
    const s = allSections().find((x) => x.id === 'codes')
    expect(s).toBeDefined()
    expect(s!.title).toBe('codes.title')
    expect(s!.visible({ features: { codes: true } } as unknown as Me)).toBe(true)
    expect(s!.visible({ features: {} } as unknown as Me)).toBe(false)
  })

  it('asks for a page of the signed-in customer’s list and nothing else', async () => {
    const calls = signedIn(({ path }) => (path.startsWith('/codes') ? page([reward()]) : {}))
    const m = mount(CodesView)
    await settle(10)
    expect(list(calls).map((c) => `${c.method} ${c.path}`)).toEqual(['GET /codes?page=1&size=10'])
    expect(list(calls)[0].init.body).toBeUndefined()
    m.unmount()
  })

  it('shows a discount code whole with what it takes off, its state, its date and the purchase that gave it', async () => {
    signedIn(() => page([reward()]))
    const m = mount(CodesView)
    await settle(10)
    const [card] = cards(m)
    expect(m.text()).toContain('Your discount and gift codes, newest first.')
    expect(card.dataset.kind).toBe('reward')
    expect(words(card.querySelector('.mono')!)).toBe('THX-AB23CD45')
    expect(card.querySelector('.mono')!.getAttribute('dir')).toBe('ltr')
    const t = words(card)
    expect(t).toContain('Discount code')
    expect(t).toContain('20% off any plan')
    expect(t).toContain('Not used yet')
    expect(t).toMatch(/Valid until \d{1,2} \w+ 2026/)
    expect(t).toContain('Reward for buying Monthly 50GB')
    expect(card.querySelector('.badge')!.classList.contains('active')).toBe(true)
    expect(m.text()).not.toContain('{') // no placeholder is left in a text
    m.unmount()
  })

  it('copies the whole code', async () => {
    const copied = clipboard()
    signedIn(() => page([reward(), reward({ id: 'THX-ZZ99ZZ99', code: 'THX-ZZ99ZZ99' })]))
    const m = mount(CodesView)
    await settle(10)
    const copyButtons = m.buttons().filter((b) => b.textContent?.trim() === 'Copy')
    expect(copyButtons).toHaveLength(2)
    copyButtons[1].click()
    await settle(5)
    expect(copied).toEqual(['THX-ZZ99ZZ99'])
    expect(copyButtons[1].textContent?.trim()).toBe('Copied')
    m.unmount()
  })

  it('says each state: used with the day, expired, switched off, an amount, and a code for one plan', async () => {
    signedIn(() =>
      page([
        reward({ id: 'A', code: 'THX-USED0002', state: 'used', used_at: at(0) }),
        reward({ id: 'B', code: 'THX-OLD00002', state: 'expired', expires_at: at(-2), percent: 0, amount: 40_000, currency: 'IRT' }),
        reward({ id: 'C', code: 'THX-OFF00002', state: 'off' }),
        reward({ id: 'D', code: 'THX-LEG00002', only_plan: { en: 'Weekly 20GB' }, expires_at: null, plan: undefined }),
      ]),
    )
    const m = mount(CodesView)
    await settle(10)
    const [used, expired, off, legacy] = cards(m).map(words)
    expect(used).toContain('Used')
    expect(used).toMatch(/Used on \d{1,2} \w+ 2026/)
    expect(used).not.toContain('Not used yet')
    expect(expired).toContain('40,000 Toman off any plan')
    expect(expired).toContain('Expired')
    expect(expired).toMatch(/Expired on \d{1,2} \w+ 2026/)
    expect(off).toContain('Switched off')
    expect(off).toContain('Switched off by the store')
    expect(legacy).toContain('20% off Weekly 20GB only')
    expect(legacy).toContain('No expiry date')
    expect(legacy).toContain('Reward code ·') // no purchase behind it
    const badge = (i: number) => cards(m)[i].querySelector('.badge')!.className
    expect(badge(1)).toContain('expired')
    expect(badge(2)).toContain('disabled')
    m.unmount()
  })

  it('shows a gift code as its last characters, used by the customer, with no way to copy a code', async () => {
    const copied = clipboard()
    signedIn(() => page([gift(), reward()]))
    const m = mount(CodesView)
    await settle(10)
    const [g, r] = cards(m)
    expect(g.dataset.kind).toBe('gift')
    const t = words(g)
    expect(t).toContain('Gift code ending in JKMN')
    expect(t).toContain('Used by you')
    expect(t).toContain('25,000 Toman added to your wallet')
    expect(t).toMatch(/Applied on \d{1,2} \w+ 2026/)
    expect(g.querySelector('button')).toBeNull()
    expect(g.querySelector('.mono')).toBeNull()
    expect(r.querySelectorAll('button')).toHaveLength(1) // only the discount code can be copied
    expect(copied).toEqual([])
    m.unmount()
  })

  it('says how codes come when there are none', async () => {
    signedIn(() => page([]))
    const m = mount(CodesView)
    await settle(10)
    expect(m.text()).toContain('You have no codes yet.')
    expect(cards(m)).toHaveLength(0)
    expect(m.el.querySelector('nav')).toBeNull()
    m.unmount()
  })

  it('pages: ten to a page, Previous and Next, the page count', async () => {
    const calls = signedIn(({ path }) => {
      if (path === '/codes?page=2&size=10') return page([reward({ id: 'X11', code: 'THX-PAGE0011' }), reward({ id: 'X12', code: 'THX-PAGE0012' })], 12, 2)
      if (path.startsWith('/codes')) return page(Array.from({ length: 10 }, (_, i) => reward({ id: `X${i}`, code: `THX-PAGE00${String(i).padStart(2, '0')}` })), 12)
      return {}
    })
    const m = mount(CodesView)
    await settle(10)
    expect(cards(m)).toHaveLength(10)
    expect(m.text()).toContain('Page 1 of 2')
    expect(m.button('Previous').disabled).toBe(true)
    expect(m.button('Next').disabled).toBe(false)

    await m.click('Next')
    await settle(10)
    expect(list(calls).map((c) => c.path)).toEqual(['/codes?page=1&size=10', '/codes?page=2&size=10'])
    expect(cards(m).map((c) => words(c.querySelector('.mono')!))).toEqual(['THX-PAGE0011', 'THX-PAGE0012'])
    expect(m.text()).toContain('Page 2 of 2')
    expect(m.button('Previous').disabled).toBe(false)
    expect(m.button('Next').disabled).toBe(true)

    await m.click('Previous')
    await settle(10)
    expect(m.text()).toContain('Page 1 of 2')
    expect(cards(m)).toHaveLength(10)
    m.unmount()
  })

  it('says what went wrong and tries again', async () => {
    let fails = true
    const calls = signedIn(({ path }) => {
      if (!path.startsWith('/codes')) return {}
      return fails ? json(500, { error: 'internal', message: 'x' }) : page([reward()])
    })
    const m = mount(CodesView)
    await settle(10)
    expect(m.el.querySelector('[role=alert]')).not.toBeNull()
    expect(cards(m)).toHaveLength(0)
    fails = false
    await m.click('Try again')
    await settle(10)
    expect(cards(m)).toHaveLength(1)
    expect(list(calls)).toHaveLength(2)
    m.unmount()
  })

  it('is written in Persian, with Persian digits and the Jalali calendar', async () => {
    setLang('fa')
    signedIn(() => page([reward(), gift()]))
    const m = mount(CodesView)
    await settle(10)
    const [r, g] = cards(m).map(words)
    expect(m.text()).toContain('کدهای تخفیف و هدیهٔ شما، از تازه‌ترین.')
    expect(r).toContain('کد تخفیف')
    expect(r).toContain('۲۰٪ تخفیف برای هر پلن')
    expect(r).toContain('استفاده‌نشده')
    expect(r).toMatch(/معتبر تا [۰-۹]+ \S+ ۱۴۰۵/)
    expect(r).toContain('پاداش خرید ماهانه ۵۰ گیگ')
    expect(g).toContain('کد هدیه · آخر کد: JKMN')
    expect(g).toContain('استفاده‌شده توسط شما')
    expect(g).toContain('۲۵٬۰۰۰ تومان به کیف پول شما اضافه شد')
    expect(g).toMatch(/اعمال‌شده در [۰-۹]+ \S+ ۱۴۰۵/)
    expect(m.text()).not.toMatch(/[A-Za-z]{4,}\s+(off|added|Used)/) // no English sentence
    expect(words(m.el.querySelector('.mono')!)).toBe('THX-AB23CD45') // the code itself stays Latin and left to right
    m.unmount()
  })
})

// Every text of the section is in both languages, with the same placeholders: nothing the screen asks for is missing in either.
describe('the My codes texts', () => {
  const keys = (l: 'fa' | 'en') => Object.keys(messages[l]).filter((k) => k.startsWith('codes.'))
  const holes = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()

  it('are the same set in Persian and English', () => {
    expect(keys('fa').length).toBeGreaterThan(20)
    expect(keys('fa').sort()).toEqual(keys('en').sort())
  })

  it('fill the same placeholders in both languages', () => {
    for (const k of keys('en')) expect(holes(messages.fa[k]), k).toEqual(holes(messages.en[k]))
  })

  it('name every state a code can be in', () => {
    for (const s of ['unused', 'used', 'expired', 'off', 'gift_used']) {
      expect(messages.fa[`codes.state.${s}`], s).toBeTruthy()
      expect(messages.en[`codes.state.${s}`], s).toBeTruthy()
    }
  })
})
