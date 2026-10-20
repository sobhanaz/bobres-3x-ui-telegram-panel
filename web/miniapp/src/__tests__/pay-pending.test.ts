import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Me, PendingPayment } from '../api'
import { setLang } from '../i18n'
import { state } from '../store'
import WalletView from '../views/WalletView.vue'
import { chooseFile, json, mount, noTelegram, pngBytes, settle, signedIn, type Call, type Mounted } from './helpers'

// A manual payment started and left (Telegram reloaded the app between the bank and the receipt) is listed in the
// wallet, and taken up again from there: its own screen, ending in the proof call every receipt ends in.

const HASH = 'ab12'.repeat(16)
const ago = (seconds: number) => Math.floor(Date.now() / 1000) - seconds - 5
const wallet = { wallets: [{ currency: 'IRT', balance: 0, scale: 0 }], ledger: [] }

const card = (over: Partial<PendingPayment> = {}): PendingPayment => ({
  intent_id: 'i1', status: 'pending', provider: 'manual_card', order_id: 'o1', amount: 150000, currency: 'IRT', scale: 0, pay_url: '',
  gateway_amount: 0, gateway_currency: '', scale_gateway: -1,
  details: { card_number: '6037-9911-2222-3333', card_holder: 'Ali Rezaei', reference: 'AB12CD34' },
  created_at: ago(3 * 3600), ...over,
})
const crypto = (over: Partial<PendingPayment> = {}): PendingPayment =>
  card({
    intent_id: 'i2', provider: 'manual_crypto', order_id: '', amount: 200000, usdt_amount: 3_340_000, created_at: ago(2 * 86400),
    details: { usdt_trc20: 'TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r', usdt_erc20: '0x0000000000000000000000000000000000000001', reference: 'EF56AB78' }, ...over,
  })
const link = (over: Partial<PendingPayment> = {}): PendingPayment =>
  card({ intent_id: 'i3', provider: 'manual_zarinpal', created_at: ago(20), details: { zarinpal_link: 'https://zarinp.al/shop', reference: 'CD34EF56' }, ...over })

// the rows of the list as the customer reads them (the reference is wrapped in direction marks, which are not seen)
const rowsOf = (m: Mounted) =>
  Array.from(m.el.querySelectorAll('.pending')).map((li) => ({
    method: li.querySelectorAll('strong')[0]?.textContent?.trim(),
    amount: li.querySelectorAll('strong')[1]?.textContent?.trim(),
    what: li.querySelectorAll('p')[0]?.textContent?.trim(),
    reference: li.querySelector('.mono')?.textContent?.replace(/[\u2068\u2069]/g, ''),
  }))
const sendButtons = (m: Mounted) => m.buttons().filter((b) => b.textContent?.trim() === 'Send the receipt')
const input = (m: Mounted, i: number) => m.el.querySelectorAll('input[type=text]')[i] as HTMLInputElement
async function type(m: Mounted, i: number, value: string) {
  const el = input(m, i)
  el.value = value
  el.dispatchEvent(new Event('input'))
  await settle(5)
}
const png = () => new File([pngBytes()], 'IMG_2041.png', { type: 'image/png' })
const listCalls = (calls: Call[]) => calls.filter((c) => c.path === '/payments/pending')

beforeEach(() => {
  setLang('en')
  state.gate = ''
  state.me = { top_up: { currency: 'IRT', min: 10000, max: 50000000 }, brand: { bot_url: '' }, features: {} } as unknown as Me
  state.wallets = [{ currency: 'IRT', balance: 0, scale: 0 }]
  state.ledger = []
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  noTelegram()
  document.body.innerHTML = ''
})

describe('the payments waiting for a receipt, in the wallet', () => {
  it('lists each with what it is, the amount, how long ago it began and its reference', async () => {
    const calls = signedIn(({ path }) => {
      if (path === '/wallet') return wallet
      if (path === '/payments/pending') {
        return { payments: [link(), card(), crypto(), crypto({ intent_id: 'i4', currency: 'USDT', scale: 6, amount: 5_000_000, usdt_amount: 5_000_000, created_at: ago(86400), details: { usdt_trc20: 'T', reference: 'AA11BB22' } })] }
      }
      return {}
    })
    const m = mount(WalletView, {}, [])
    await settle(20)
    expect(listCalls(calls)).toHaveLength(1) // asked when the screen opens, and not again by itself
    const t = m.text()
    expect(t).toContain('Payments waiting for your receipt')
    expect(t).toContain('Paid but did not send the receipt? Send it from here.')
    // the payment link, a purchase, 20 seconds ago; the card, a purchase, 3 hours ago; a top-up by USDT, 2 days ago;
    // and one priced in USDT, shown whole as the payment's own screen shows it
    expect(rowsOf(m)).toEqual([
      { method: 'Zarinpal payment link', amount: '150,000 Toman', what: 'Purchase · now', reference: 'CD34EF56' },
      { method: 'Card to card', amount: '150,000 Toman', what: 'Purchase · 3 hours ago', reference: 'AB12CD34' },
      { method: 'USDT', amount: '200,000 Toman', what: 'Wallet top-up · 2 days ago', reference: 'EF56AB78' },
      { method: 'USDT', amount: '5 USDT', what: 'Wallet top-up · yesterday', reference: 'AA11BB22' },
    ])
    expect(m.text()).toContain('Payment reference:')
    expect(m.el.querySelectorAll('.pending .mono[dir=ltr]')).toHaveLength(4) // references read left to right
    expect(sendButtons(m)).toHaveLength(4)
    // each button is described by its own payment, for a screen reader
    for (const b of sendButtons(m)) {
      const described = m.el.querySelector('#' + b.getAttribute('aria-describedby'))
      expect(described?.textContent).toMatch(/\S/)
    }
    // the wallet is as it was around it
    expect(t).toContain('Add funds')
    m.unmount()
  })

  it('is not there when nothing waits, or when core has nothing to say', async () => {
    for (const answer of [{ payments: [] }, {}]) {
      signedIn(({ path }) => (path === '/wallet' ? wallet : path === '/payments/pending' ? answer : {}))
      const m = mount(WalletView, {}, [])
      await settle(20)
      expect(m.text()).not.toContain('Payments waiting for your receipt')
      expect(m.text()).not.toContain('Could not load')
      expect(m.text()).toContain('Add funds')
      m.unmount()
      vi.restoreAllMocks()
    }
  })

  it('is written in Persian, with Persian digits and the references still read left to right', async () => {
    setLang('fa')
    signedIn(({ path }) => (path === '/wallet' ? wallet : path === '/payments/pending' ? { payments: [card(), crypto({ created_at: ago(3 * 86400) })] } : {}))
    const m = mount(WalletView, {}, [])
    await settle(20)
    const t = m.text()
    expect(t).toContain('پرداخت‌های منتظر رسید')
    expect(t).toContain('اگر پرداخت کرده‌اید ولی رسیدش را نفرستاده‌اید')
    expect(t).toContain('کارت به کارت')
    expect(t).toContain('تتر (USDT)')
    expect(rowsOf(m)).toEqual([
      { method: 'کارت به کارت', amount: '۱۵۰٬۰۰۰ تومان', what: 'خرید · ۳ ساعت پیش', reference: 'AB12CD34' },
      { method: 'تتر (USDT)', amount: '۲۰۰٬۰۰۰ تومان', what: 'افزایش موجودی · ۳ روز پیش', reference: 'EF56AB78' },
    ])
    expect(m.el.querySelectorAll('.pending .mono[dir=ltr]')).toHaveLength(2)
    expect(sendButtons(m)).toHaveLength(0) // the buttons say it in Persian
    expect(m.buttons().filter((b) => b.textContent?.trim() === 'ارسال رسید')).toHaveLength(2)
    m.unmount()
  })
})

describe('taking a payment up again', () => {
  it('shows the screen the payment had and sends the receipt through the proof call; back at the wallet it is gone', async () => {
    let sent = false
    const calls = signedIn(({ path }) => {
      if (path === '/wallet') return wallet
      if (path === '/payments/pending') return { payments: sent ? [crypto()] : [card(), crypto()] }
      if (path === '/payments/i1/proof') {
        sent = true
        return { intent_id: 'i1', status: 'confirming' }
      }
      return {}
    })
    const m = mount(WalletView, {}, [])
    await settle(20)
    expect(sendButtons(m)).toHaveLength(2)
    sendButtons(m)[0].click() // the card payment
    await settle(20)

    // One screen: the payment's own, with what to pay and the form for the receipt; no wallet around it.
    const t = m.text()
    expect(t).toContain('This payment is waiting for your receipt. If you have already paid, send it here.')
    expect(t).toContain('Card to card')
    expect(t).toContain('150,000 Toman')
    expect(t).toContain('6037-9911-2222-3333')
    expect(t).toContain('Card holder: Ali Rezaei')
    expect(t).toContain('AB12CD34')
    expect(t).toContain('After paying, send the receipt')
    expect(t).not.toContain('Add funds')
    expect(t).not.toContain('Payments waiting for your receipt')
    expect(m.el.querySelector('input[type=file]')).not.toBeNull()

    await chooseFile(m.el, png())
    await type(m, 0, '998877')
    await m.click('Send the receipt')
    await settle(30)
    const proof = calls.filter((c) => c.path === '/payments/i1/proof')
    expect(proof).toHaveLength(1)
    expect(proof[0].method).toBe('POST')
    const form = proof[0].init.body as FormData
    expect(form.get('reference')).toBe('998877')
    expect((form.get('receipt') as File).name).toBe('receipt.png')
    expect(m.text()).toContain('Receipt received')
    expect(m.text()).not.toContain('waiting for your receipt') // sent: no longer waiting for it

    // Back to the wallet: the list is read again, and the payment whose receipt went is not in it.
    const before = listCalls(calls).length
    await m.click('Back')
    await settle(20)
    expect(listCalls(calls)).toHaveLength(before + 1)
    expect(m.text()).toContain('Add funds')
    expect(m.text()).toContain('Payments waiting for your receipt')
    expect(m.text()).not.toContain('Card to card')
    expect(sendButtons(m)).toHaveLength(1)
    m.unmount()
  })

  it('takes a crypto payment up again with its transaction hash, and a payment link with a receipt', async () => {
    let done = ''
    const calls = signedIn(({ path }) => {
      if (path === '/wallet') return wallet
      if (path === '/payments/pending') return { payments: [crypto(), link()].filter((p) => p.intent_id !== done) }
      if (path.endsWith('/proof')) {
        done = path.split('/')[2]
        return { intent_id: done, status: 'confirming' }
      }
      return {}
    })
    const m = mount(WalletView, {}, [])
    await settle(20)
    sendButtons(m)[0].click() // the crypto payment, the older but listed first here
    await settle(20)
    const t = m.text()
    expect(t).toContain('USDT, then send a receipt') // the screen's own full name
    expect(t).toContain('Send exactly 3.34 USDT (about 200,000 Toman) to one of these addresses:')
    expect(t).toContain('TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r')
    await m.click('Transaction hash (TXID)')
    await type(m, 0, HASH)
    await m.click('Send the hash')
    await settle(30)
    const proof = calls.find((c) => c.path === '/payments/i2/proof')
    expect(proof?.method).toBe('POST')
    expect(JSON.parse(String(proof?.init.body))).toEqual({ txid: HASH, network: 'TRC20' })
    expect(m.text()).toContain('Receipt received')
    await m.click('Back')
    await settle(20)

    sendButtons(m)[0].click() // the payment link
    await settle(20)
    expect(m.text()).toContain('Open the payment page')
    await chooseFile(m.el, png())
    await type(m, 0, '445566') // the app asks for the bank's number for a payment link too
    await m.click('Send the receipt')
    await settle(30)
    expect(calls.filter((c) => c.path === '/payments/i3/proof')).toHaveLength(1)
    expect(m.text()).toContain('Receipt received')
    m.unmount()
  })

  it('shows what the server says about a proof it will not take, and lets the customer try again', async () => {
    let answer = json(409, { error: 'chat_closed', message: 'x' })
    signedIn(({ path }) => {
      if (path === '/wallet') return wallet
      if (path === '/payments/pending') return { payments: [card()] }
      if (path === '/payments/i1/proof') return answer
      return {}
    })
    const m = mount(WalletView, {}, [])
    await settle(20)
    sendButtons(m)[0].click()
    await settle(20)
    await chooseFile(m.el, png())
    await type(m, 0, '998877')
    await m.click('Send the receipt')
    await settle(30)
    expect(m.text()).toContain('Open the bot and tap Start, then send the receipt again.')
    expect(m.text()).not.toContain('Receipt received')
    answer = json(409, { error: 'already_submitted', message: 'x' }) // it did arrive after all
    await m.click('Send the receipt')
    await settle(30)
    expect(m.text()).toContain('Receipt received')
    m.unmount()
  })

  it('asks about the payment while its screen is open, and tells an order payment from a top-up when it is approved', async () => {
    vi.useFakeTimers()
    for (const [row, said, listed] of [
      [card(), 'Your payment was approved.', 1], // an order's payment: the wallet is not what went up
      [crypto(), 'The wallet balance went up.', 1],
    ] as const) {
      let approved = false
      const calls = signedIn(({ path }) => {
        if (path === '/wallet') return { wallets: [{ currency: 'IRT', balance: approved && !row.order_id ? 200000 : 0, scale: 0 }], ledger: [] }
        if (path === '/payments/pending') return { payments: approved ? [] : [row] }
        if (path === `/payments/${row.intent_id}/check`) return { intent_id: row.intent_id, status: approved ? 'succeeded' : 'confirming', order_id: row.order_id, failure_reason: '' }
        return {}
      })
      const m = mount(WalletView, {}, [])
      await settle(20)
      const checks = () => calls.filter((c) => c.path.endsWith('/check')).length
      await settle(5 * 60_000)
      expect(checks()).toBe(0) // a list is not a payment on screen: nothing is asked until one is opened
      sendButtons(m)[0].click()
      await settle(20)
      await settle(30_500)
      expect(checks()).toBe(1) // every 30 seconds, as for a manual payment made here
      expect(m.text()).not.toContain(said)
      approved = true
      await settle(30_000)
      await settle(20)
      expect(checks()).toBe(2)
      expect(m.text()).toContain(said)
      expect(m.text()).toContain('Add funds') // back in the wallet, the payment's screen is gone
      expect(m.text()).not.toContain('Payments waiting for your receipt') // and it is not waiting any more
      expect(listCalls(calls)).toHaveLength(listed + 1) // the list was read again
      expect(state.wallets[0].balance).toBe(!row.order_id ? 200000 : 0)
      await settle(10 * 60_000)
      expect(checks()).toBe(2) // and nothing more to ask
      m.unmount()
      vi.restoreAllMocks()
    }
  })

  it('learns that the staff turned the payment down, and stops asking', async () => {
    vi.useFakeTimers()
    const calls = signedIn(({ path }) => {
      if (path === '/wallet') return wallet
      if (path === '/payments/pending') return { payments: [card()] }
      if (path === '/payments/i1/check') return { intent_id: 'i1', status: 'rejected', order_id: 'o1', failure_reason: '' }
      return {}
    })
    const m = mount(WalletView, {}, [])
    await settle(20)
    sendButtons(m)[0].click()
    await settle(30_500)
    expect(m.text()).toContain('The payment did not go through.')
    await settle(10 * 60_000)
    expect(calls.filter((c) => c.path === '/payments/i1/check')).toHaveLength(1)
    m.unmount()
  })

  it('stops asking when the customer goes back to the wallet', async () => {
    vi.useFakeTimers()
    const calls = signedIn(({ path }) => {
      if (path === '/wallet') return wallet
      if (path === '/payments/pending') return { payments: [card()] }
      if (path === '/payments/i1/check') return { intent_id: 'i1', status: 'pending', order_id: 'o1', failure_reason: '' }
      return {}
    })
    const m = mount(WalletView, {}, [])
    await settle(20)
    sendButtons(m)[0].click()
    await settle(20)
    await m.click('Back')
    await settle(10 * 60_000)
    expect(calls.filter((c) => c.path === '/payments/i1/check')).toHaveLength(0)
    m.unmount()
  })

  it('lists a payment started here and left, once the customer is back at the wallet', async () => {
    let left = false
    const calls = signedIn(({ method, path }) => {
      if (path === '/wallet') return wallet
      if (path.startsWith('/methods')) return { methods: ['manual_card'] }
      if (path === '/topup' && method === 'POST') {
        left = true
        return card({ order_id: '', amount: 100000 })
      }
      if (path === '/payments/pending') return { payments: left ? [card({ order_id: '', amount: 100000, created_at: ago(0) })] : [] }
      return {}
    })
    const m = mount(WalletView, {}, [])
    await settle(20)
    expect(m.text()).not.toContain('Payments waiting for your receipt')
    await m.click('100,000 Toman')
    await settle(20)
    await m.click('Card to card')
    await settle(20)
    expect(m.text()).toContain('6037-9911-2222-3333')
    await m.click('Back') // left without sending the receipt
    await settle(20)
    expect(listCalls(calls)).toHaveLength(2)
    expect(m.text()).toContain('Payments waiting for your receipt')
    expect(m.text()).toContain('Wallet top-up · now')
    m.unmount()
  })
})

describe('asking for the list more than once', () => {
  it('shows the answer of the last ask, whatever the order the answers come in', async () => {
    // the first ask (opening the screen) is slow and sees the payment; the second (back from a payment screen) is fast and does not
    let release: () => void = () => {}
    const held = new Promise<void>((r) => (release = r))
    let asks = 0
    signedIn(async ({ method, path }) => {
      if (path === '/wallet') return wallet
      if (path.startsWith('/methods')) return { methods: ['manual_card'] }
      if (path === '/topup' && method === 'POST') return card({ order_id: '', amount: 100000 })
      if (path === '/payments/pending') {
        if (++asks === 1) {
          await held
          return { payments: [card()] }
        }
        return { payments: [] }
      }
      return {}
    })
    const m = mount(WalletView, {}, [])
    await settle(20)
    await m.click('100,000 Toman')
    await settle(20)
    await m.click('Card to card')
    await settle(20)
    await m.click('Back') // the second ask
    await settle(20)
    release() // and only now the first answers
    await settle(20)
    expect(asks).toBe(2)
    expect(m.text()).not.toContain('Payments waiting for your receipt')
    m.unmount()
  })
})

describe('a list that cannot be had', () => {
  it('leaves the wallet as it is, says so, and can be asked again', async () => {
    let fail = true
    signedIn(({ path }) => {
      if (path === '/wallet') return wallet
      if (path === '/payments/pending') return fail ? json(500, { error: 'internal', message: 'x' }) : { payments: [card()] }
      return {}
    })
    const m = mount(WalletView, {}, [])
    await settle(20)
    expect(state.gate).toBe('')
    expect(m.text()).toContain('Add funds')
    expect(m.text()).toContain('Could not load the payments waiting for a receipt.')
    expect(m.el.querySelector('[role=alert]')).toBeNull() // the wallet itself is fine
    fail = false
    await m.click('Try again')
    await settle(20)
    expect(m.text()).not.toContain('Could not load')
    expect(m.text()).toContain('Payments waiting for your receipt')
    m.unmount()
  })

  it('is no reason to say the store is busy or under maintenance', async () => {
    for (const body of [{ error: 'unavailable', message: 'x' }, { error: 'maintenance', message: 'x' }]) {
      signedIn(({ path }) => (path === '/wallet' ? wallet : path === '/payments/pending' ? json(503, body) : {}))
      const m = mount(WalletView, {}, [])
      await settle(20)
      expect(state.gate).toBe('')
      expect(m.text()).toContain('Add funds')
      expect(m.text()).toContain('Could not load the payments waiting for a receipt.')
      m.unmount()
      vi.restoreAllMocks()
    }
  })

  it('does replace the screen when the customer is banned or the store is off', async () => {
    for (const [status, error, gate] of [[403, 'banned', 'banned'], [404, 'disabled', 'off']] as const) {
      signedIn(({ path }) => (path === '/wallet' ? wallet : path === '/payments/pending' ? json(status, { error, message: 'x' }) : {}))
      state.gate = ''
      const m = mount(WalletView, {}, [])
      await settle(20)
      expect(state.gate).toBe(gate)
      m.unmount()
      vi.restoreAllMocks()
    }
  })
})
