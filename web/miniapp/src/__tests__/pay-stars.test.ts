import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { Intent } from '../api'
import StarsPay from '../components/StarsPay.vue'
import { setLang } from '../i18n'
import { mount, noTelegram, telegram } from './helpers'

const stars = (over: Partial<Intent> = {}): Intent => ({
  intent_id: 'i1', status: 'pending', provider: 'stars', order_id: 'o1', amount: 150000, currency: 'IRT', scale: 0, pay_url: '',
  gateway_amount: 100, gateway_currency: 'XTR', scale_gateway: -1, details: { reference: 'AB12CD34' },
  invoice_url: 'https://t.me/$fakeInvoice1', stars: 100, ...over,
})
const props = (intent: Intent) => ({ intent, checking: false, message: '' })

beforeEach(() => setLang('en'))
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  noTelegram()
  document.body.innerHTML = ''
})

describe('StarsPay', () => {
  it('opens the invoice by itself and says what is paid', async () => {
    const open = vi.fn()
    telegram({ openInvoice: open, isVersionAtLeast: () => true })
    const m = mount(StarsPay, props(stars()), ['check'])
    await nextTick()
    expect(open).toHaveBeenCalledTimes(1)
    expect(open.mock.calls[0][0]).toBe('https://t.me/$fakeInvoice1')
    expect(m.text()).toContain('Pay with Telegram Stars')
    expect(m.text()).toContain('100 Stars (150,000 Toman)')
    expect(m.text()).toContain('Telegram') // the sheet is open: come back after paying
    m.unmount()
  })

  it('asks whether the payment landed, quietly, when Telegram says it was paid', async () => {
    vi.useFakeTimers()
    let ended: (s: string) => void = () => {}
    telegram({ openInvoice: (_u: string, cb: (s: string) => void) => (ended = cb), isVersionAtLeast: () => true })
    const m = mount(StarsPay, props(stars()), ['check'])
    await nextTick()
    ended('paid')
    await nextTick()
    expect(m.text()).toContain('Payment sent; confirming it')
    expect(m.events.check).toEqual([[true]]) // quietly: nobody pressed anything
    expect(m.button('Pay 100 Stars').disabled).toBe(true)
    await vi.advanceTimersByTimeAsync(3000)
    expect(m.events.check).toEqual([[true], [true]])
    await vi.advanceTimersByTimeAsync(60_000)
    expect(m.events.check).toHaveLength(2) // it does not keep asking: the screen around it does
    m.unmount()
  })

  it('does not ask after it is gone', async () => {
    vi.useFakeTimers()
    let ended: (s: string) => void = () => {}
    telegram({ openInvoice: (_u: string, cb: (s: string) => void) => (ended = cb), isVersionAtLeast: () => true })
    const m = mount(StarsPay, props(stars()), ['check'])
    await nextTick()
    ended('pending')
    m.unmount()
    await vi.advanceTimersByTimeAsync(10_000)
    expect(m.events.check).toHaveLength(1)
  })

  it('lets the customer try again after cancelling or a failure', async () => {
    const open = vi.fn()
    let ended: (s: string) => void = () => {}
    telegram({ openInvoice: (u: string, cb: (s: string) => void) => (open(u), (ended = cb)), isVersionAtLeast: () => true })
    const m = mount(StarsPay, props(stars()), ['check'])
    await nextTick()
    ended('cancelled')
    await nextTick()
    expect(m.text()).toContain('The payment was cancelled')
    expect(m.events.check).toEqual([])
    await m.click('Pay 100 Stars')
    expect(open).toHaveBeenCalledTimes(2)
    ended('failed')
    await nextTick()
    expect(m.text()).toContain('The payment did not go through')
    await m.click('Pay 100 Stars')
    expect(open).toHaveBeenCalledTimes(3)
    expect(m.events.check).toEqual([]) // nothing was paid, nothing is asked
    m.unmount()
  })

  it('opens the link in the chat where Telegram has no payment sheet, and waits to be told', async () => {
    const link = vi.fn()
    telegram({ openInvoice: vi.fn(), isVersionAtLeast: () => false, openTelegramLink: link })
    const m = mount(StarsPay, props(stars()), ['check'])
    await nextTick()
    expect(link).toHaveBeenCalledWith('https://t.me/$fakeInvoice1')
    await m.click('I have paid')
    expect(m.events.check).toEqual([[]]) // the customer asked: not quietly
    m.unmount()
  })

  it('opens nothing for a payment that is paid, over, or has no invoice', async () => {
    const open = vi.fn()
    telegram({ openInvoice: open, isVersionAtLeast: () => true })
    let m = mount(StarsPay, props(stars({ status: 'succeeded', invoice_url: undefined })), ['check'])
    await nextTick()
    expect(m.text()).toContain('Payment received.')
    m.unmount()
    m = mount(StarsPay, props(stars({ status: 'failed' })), ['check'])
    await nextTick()
    expect(m.text()).toContain('The payment did not go through.')
    expect(m.buttons().map((b) => b.textContent?.trim())).not.toContain('Pay 100 Stars')
    m.unmount()
    m = mount(StarsPay, props(stars({ invoice_url: undefined })), ['check'])
    await nextTick()
    expect(m.text()).toContain('The payment did not go through.')
    m.unmount()
    expect(open).not.toHaveBeenCalled()
  })

  it('opens only a Telegram link', async () => {
    const open = vi.fn()
    const link = vi.fn()
    telegram({ openInvoice: open, openTelegramLink: link, isVersionAtLeast: () => true })
    for (const bad of ['https://evil.example/pay', 'http://t.me/$abc', 'javascript:alert(1)', 'https://t.me.evil.example/$abc']) {
      const m = mount(StarsPay, props(stars({ invoice_url: bad })), ['check'])
      await nextTick()
      expect(m.text(), bad).toContain('The payment did not go through.')
      m.unmount()
    }
    expect(open).not.toHaveBeenCalled()
    expect(link).not.toHaveBeenCalled()
  })

  it('ignores how the sheet ended when the customer has left', async () => {
    let ended: (s: string) => void = () => {}
    telegram({ openInvoice: (_u: string, cb: (s: string) => void) => (ended = cb), isVersionAtLeast: () => true })
    const m = mount(StarsPay, props(stars()), ['check'])
    await nextTick()
    m.unmount()
    ended('paid')
    await nextTick()
    expect(m.events.check).toEqual([])
  })

  it('is in Persian first, with Persian digits', async () => {
    setLang('fa')
    telegram({ openInvoice: vi.fn(), isVersionAtLeast: () => true })
    const m = mount(StarsPay, props(stars()), ['check'])
    await nextTick()
    expect(m.text()).toContain('پرداخت با استارز تلگرام')
    expect(m.text()).toContain('۱۰۰ استارز')
    expect(m.text()).toContain('۱۵۰٬۰۰۰ تومان')
    expect(document.documentElement.dir).toBe('rtl')
    m.unmount()
  })
})
