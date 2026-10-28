import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type Intent, type Me } from '../api'
import PayPanel from '../components/PayPanel.vue'
import { messages, setLang } from '../i18n'
import { state } from '../store'
import WalletView from '../views/WalletView.vue'
import { clipboard, json, mount, noTelegram, settle, signedIn, type Call, type Mounted } from './helpers'

// Topping the wallet up in USDT (Tether): the switch next to the Toman amounts, a whole number of USDT, what it adds to the
// wallet, the address with its QR code and the time left, and the wallet that fills a moment after the payment is seen.

const NOW = Date.UTC(2026, 9, 28, 12, 0, 0) // the phone's clock in these tests, to the second
const ADDRESS = 'TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r'
const QR = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAhJ0pbAAAAABJRU5ErkJggg=='
const USDT_BOUNDS = { currency: 'USDT', min: 1_000_000, max: 10_000_000_000, step: 1_000_000, scale: 6 }

const autoIntent = (over: Partial<Intent> = {}): Intent => ({
  intent_id: 'i1', status: 'pending', provider: 'usdt_trc20_auto', order_id: '', amount: 10_000_000, currency: 'USDT', scale: 6, pay_url: '',
  gateway_amount: 10_000_417, gateway_currency: 'USDT', scale_gateway: 6,
  details: { pay_address: ADDRESS, network: 'TRC20', expires_at: String(NOW / 1000 + 600), reference: 'AB12CD34' },
  qr: QR, ...over,
})
const manualIntent = (over: Partial<Intent> = {}): Intent => ({
  intent_id: 'i2', status: 'pending', provider: 'manual_crypto', order_id: '', amount: 10_000_000, currency: 'USDT', scale: 6, pay_url: '',
  gateway_amount: 0, gateway_currency: '', scale_gateway: -1, usdt_amount: 10_000_000,
  details: { usdt_trc20: ADDRESS, usdt_erc20: '0x0000000000000000000000000000000000000001', reference: 'EF56AB78' }, ...over,
})

// What the bank (core) says at the moment: the wallet in Toman with the live rate, what the store takes in USDT, how the payment is.
const world = {
  balance: 1_000_000,
  rate: 294_000 as number | undefined,
  usdtMethods: ['usdt_trc20_auto', 'manual_crypto'] as string[] | 'fail',
  status: 'pending',
  intent: (): Intent | Response => autoIntent(),
}
const wallet = () => ({
  wallets: [{
    currency: 'IRT', balance: world.balance, scale: 0,
    ...(world.rate ? { usdt_rate: world.rate, usdt_equivalent: Math.floor((world.balance * 1_000_000) / world.rate) } : {}),
  }],
  ledger: [],
})

function bank(): Call[] {
  return signedIn(({ method, path }) => {
    if (path === '/wallet') return wallet()
    if (path === '/payments/pending') return { payments: [] }
    if (path.startsWith('/methods')) {
      const currency = new URLSearchParams(path.split('?')[1]).get('currency')
      if (currency !== 'USDT') return { methods: ['manual_card', 'zarinpal'] }
      return world.usdtMethods === 'fail' ? json(500, { error: 'weird', message: 'x' }) : { methods: world.usdtMethods }
    }
    if (path === '/topup' && method === 'POST') return world.intent()
    const check = path.match(/^\/payments\/(\w+)\/check$/)
    if (check) return { intent_id: check[1], status: world.status, order_id: '', failure_reason: '' }
    return {}
  })
}

const me = (usdt: boolean): Me =>
  ({ top_up: { currency: 'IRT', min: 10000, max: 50000000, ...(usdt ? { usdt: USDT_BOUNDS } : {}) }, brand: { bot_url: '' }, features: {} }) as unknown as Me

const hits = (calls: Call[], method: string, path: string) => calls.filter((c) => c.method === method && c.path === path)
const bodyOf = (c: Call | undefined) => JSON.parse(String(c?.init.body))
const checks = (calls: Call[]) => calls.filter((c) => /^\/payments\/\w+\/check$/.test(c.path)).length
const walletReads = (calls: Call[]) => calls.filter((c) => c.path === '/wallet').length
const probes = (calls: Call[]) => calls.filter((c) => c.path === '/methods?currency=USDT&amount=1000000')
const topups = (calls: Call[]) => hits(calls, 'POST', '/topup')
const label = (b: Element) => b.textContent?.trim()
const group = (m: Mounted) => m.el.querySelector('[role=group]')
const switchLabels = (m: Mounted) => Array.from(group(m)?.querySelectorAll('button') ?? []).map(label)
const input = (m: Mounted) => m.el.querySelector('input[type=text]') as HTMLInputElement
async function type(m: Mounted, value: string) {
  input(m).value = value
  input(m).dispatchEvent(new Event('input'))
  await settle(5)
}
// the buttons that are whole USDT amounts (the presets), and one of them
const presetButtons = (m: Mounted) => m.buttons().map(label).filter((t) => /^[\d٠-٩۰-۹]+ (USDT|تتر)$/.test(t ?? ''))
const preset = (m: Mounted, text: string) => m.button(new RegExp(`^\\s*${text}\\s*$`))
const credit = (m: Mounted) => m.el.querySelector('.credit')
const creditRows = (m: Mounted) => Array.from(credit(m)?.querySelectorAll('.row') ?? []).map((r) => Array.from(r.children).map(label))

async function open(usdt = true): Promise<{ m: Mounted; calls: Call[] }> {
  state.me = me(usdt)
  const calls = bank()
  const m = mount(WalletView, {}, [])
  await settle(20)
  return { m, calls }
}
// to the screen with the address: USDT chosen, an amount, the automatic method
async function toAddress(amount = '10 USDT'): Promise<{ m: Mounted; calls: Call[] }> {
  const o = await open()
  await o.m.click('USDT')
  await preset(o.m, amount).click()
  await settle(20)
  await o.m.click('USDT (TRC20)')
  await settle(20)
  return o
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(NOW)
  setLang('en')
  state.gate = ''
  state.me = me(true)
  state.wallets = [{ currency: 'IRT', balance: 0, scale: 0 }]
  state.ledger = []
  world.balance = 1_000_000
  world.rate = 294_000
  world.usdtMethods = ['usdt_trc20_auto', 'manual_crypto']
  world.status = 'pending'
  world.intent = () => autoIntent()
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  noTelegram()
  document.body.innerHTML = ''
})

describe('the currency switch', () => {
  it('is there when core takes USDT and the store has a way to be paid in it, which is asked once', async () => {
    const { m, calls } = await open()
    expect(group(m)).not.toBeNull()
    expect(switchLabels(m)).toEqual(['Toman', 'USDT'])
    expect(probes(calls)).toHaveLength(1)
    await m.click('USDT')
    await settle(60_000)
    expect(probes(calls)).toHaveLength(1) // not again, however long the tab stays open
    m.unmount()
  })

  it('is not there, and asks nothing, when core sends no top-up in USDT', async () => {
    const { m, calls } = await open(false)
    expect(group(m)).toBeNull()
    expect(m.buttons().map(label)).toContain('100,000 Toman')
    expect(calls.filter((c) => c.path.startsWith('/methods'))).toHaveLength(0)
    m.unmount()
  })

  it('is not there when the store has no way to be paid in USDT', async () => {
    world.usdtMethods = []
    const { m, calls } = await open()
    expect(probes(calls)).toHaveLength(1)
    expect(group(m)).toBeNull()
    expect(m.text()).toContain('Add funds')
    m.unmount()
  })

  it('is not there, and nothing is said, when the question could not be answered', async () => {
    world.usdtMethods = 'fail'
    const { m } = await open()
    expect(group(m)).toBeNull()
    expect(m.el.querySelector('[role=alert]')).toBeNull()
    expect(m.text()).not.toContain('Something went wrong')
    expect(m.buttons().map(label)).toContain('100,000 Toman') // the Toman top-up is what the screen is for
    m.unmount()
  })

  it('is a labelled pair that says which one is on, starting with Toman', async () => {
    const { m } = await open()
    expect(group(m)?.getAttribute('aria-label')).toBe('Top-up currency')
    const [toman, usdt] = Array.from(group(m)?.querySelectorAll('button') ?? [])
    expect([toman.getAttribute('aria-pressed'), usdt.getAttribute('aria-pressed')]).toEqual(['true', 'false'])
    expect(m.buttons().map(label)).toContain('100,000 Toman')
    expect(presetButtons(m)).toEqual([])
    usdt.click()
    await settle(5)
    expect([toman.getAttribute('aria-pressed'), usdt.getAttribute('aria-pressed')]).toEqual(['false', 'true'])
    expect(m.buttons().map(label)).not.toContain('100,000 Toman')
    toman.click()
    await settle(5)
    expect(m.buttons().map(label)).toContain('100,000 Toman')
    expect(presetButtons(m)).toEqual([])
    m.unmount()
  })

  it('is written in Persian too', async () => {
    setLang('fa')
    const { m } = await open()
    expect(group(m)?.getAttribute('aria-label')).toBe('ارز افزایش موجودی')
    expect(switchLabels(m)).toEqual(['تومان', 'تتر'])
    m.unmount()
  })
})

describe('the amount in USDT', () => {
  it('offers 5, 10, 25, 50 and 100, a field of its own, and the range', async () => {
    const { m } = await open()
    await m.click('USDT')
    expect(presetButtons(m)).toEqual(['5 USDT', '10 USDT', '25 USDT', '50 USDT', '100 USDT'])
    expect(m.text()).toContain('Other amount (a whole number of USDT)')
    expect(m.text()).toContain('From 1 to 10,000 USDT')
    expect(m.text()).not.toContain('100,000 Toman')
    m.unmount()
  })

  it.each([['1', '1 USDT'], ['25', '25 USDT'], ['۲۵', '25 USDT'], ['٢٥', '25 USDT'], ['10000', '10,000 USDT'], ['10,000', '10,000 USDT'], ['۱۰٬۰۰۰', '10,000 USDT'], [' 7 ', '7 USDT'], ['007', '7 USDT']])(
    'takes %s as a whole number of USDT',
    async (typed, shown) => {
      const { m, calls } = await open()
      await m.click('USDT')
      await type(m, typed)
      await m.click('Continue')
      expect(m.el.querySelector('[role=alert]')).toBeNull()
      expect(m.text()).toContain(shown)
      expect(m.text()).toContain('USDT (TRC20)')
      expect(m.text()).not.toContain('From 1 to 10,000 USDT') // on to the methods
      expect(topups(calls)).toHaveLength(0) // choosing an amount starts no payment
      m.unmount()
    },
  )

  it.each(['0', '0.5', '0,5', '1.5', '5.0', '10001', '10 001', '99999999', '', '  ', 'abc', '-5', '1e3', '٠٫٥', '۰', '12x'])(
    'refuses %j and says so',
    async (typed) => {
      const { m, calls } = await open()
      await m.click('USDT')
      await type(m, typed)
      await m.click('Continue')
      expect(m.el.querySelector('[role=alert]')?.textContent).toBe('That amount is not valid.')
      expect(m.text()).toContain('From 1 to 10,000 USDT') // still choosing the amount
      expect(m.text()).not.toContain('USDT (TRC20)')
      expect(calls.filter((c) => c.path.startsWith('/methods?currency=USDT&amount=') && !c.path.endsWith('amount=1000000'))).toHaveLength(0)
      expect(topups(calls)).toHaveLength(0)
      m.unmount()
    },
  )

  it('takes the Enter key as Continue, and goes on once the amount is right', async () => {
    const { m } = await open()
    await m.click('USDT')
    await type(m, '0.5')
    input(m).dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }))
    await settle(5)
    expect(m.el.querySelector('[role=alert]')?.textContent).toBe('That amount is not valid.')
    await type(m, '5')
    input(m).dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }))
    await settle(20)
    expect(m.el.querySelector('[role=alert]')).toBeNull()
    expect(m.text()).toContain('5 USDT')
    m.unmount()
  })

  it('starts the amount again when the currency is switched', async () => {
    const { m } = await open()
    await m.click('USDT')
    await type(m, '0.5')
    await m.click('Continue')
    expect(m.el.querySelector('[role=alert]')).not.toBeNull()
    await m.click('Toman')
    expect(m.el.querySelector('[role=alert]')).toBeNull()
    expect(input(m).value).toBe('')
    await m.click('USDT')
    expect(input(m).value).toBe('')
    m.unmount()
  })

  it('uses Persian digits and the word Tether in Persian', async () => {
    setLang('fa')
    const { m } = await open()
    await m.click('تتر')
    expect(presetButtons(m)).toEqual(['۵ تتر', '۱۰ تتر', '۲۵ تتر', '۵۰ تتر', '۱۰۰ تتر'])
    expect(m.text()).toContain('مبلغ دلخواه (عدد صحیح، به تتر)')
    expect(m.text()).toContain('از ۱ تا ۱۰٬۰۰۰ تتر')
    await type(m, '۰٫۵')
    await m.click('ادامه')
    expect(m.el.querySelector('[role=alert]')?.textContent).toBe('مبلغ معتبر نیست.')
    await type(m, '۲۵')
    await m.click('ادامه')
    expect(m.text()).toContain('۲۵ تتر')
    m.unmount()
  })
})

describe('what the wallet gets for it', () => {
  const ESTIMATE = (toman: string) => `About ${toman} will be added to your wallet at today’s rate of 294,000 Toman per USDT.`
  const NO_RATE = 'The rate is not known right now; your USDT will be added to the wallet as soon as it is.'
  // (the wallet card says "About 3.4 USDT" itself, so it is the estimate's own words that tell it from that)
  const ADDED = 'will be added to your wallet at'

  it('says it under the amount that is chosen, at the wallet\'s rate without anything added', async () => {
    const { m } = await open()
    await m.click('USDT')
    await preset(m, '10 USDT').click()
    await settle(20)
    expect(m.text()).toContain(ESTIMATE('2,940,000 Toman'))
    expect(m.text()).not.toContain(NO_RATE)
    m.unmount()
  })

  it('follows what is typed, once it is an amount the top-up takes', async () => {
    const { m } = await open()
    await m.click('USDT')
    expect(m.text()).not.toContain(ADDED) // nothing typed, nothing to say (the rate is known)
    expect(m.text()).not.toContain(NO_RATE)
    await type(m, '7')
    expect(m.text()).toContain(ESTIMATE('2,058,000 Toman'))
    await type(m, '1,000')
    expect(m.text()).toContain(ESTIMATE('294,000,000 Toman'))
    await type(m, '0.5')
    expect(m.text()).not.toContain(ADDED)
    await type(m, '10001')
    expect(m.text()).not.toContain(ADDED)
    m.unmount()
  })

  it('says the rate is not known, before and after the amount, when it is not', async () => {
    world.rate = undefined // the wallet carries no usdt_rate
    const { m } = await open()
    await m.click('USDT')
    expect(m.text()).toContain(NO_RATE)
    expect(m.text()).not.toContain(ADDED)
    await type(m, '10')
    expect(m.text()).toContain(NO_RATE)
    await m.click('Continue')
    await settle(20)
    expect(m.text()).toContain('10 USDT')
    expect(m.text()).toContain(NO_RATE)
    expect(m.text()).not.toContain(ADDED)
    m.unmount()
  })

  it('takes a rate of 0 for no rate', async () => {
    world.rate = 0
    const { m } = await open()
    await m.click('USDT')
    await preset(m, '5 USDT').click()
    await settle(20)
    expect(m.text()).toContain(NO_RATE)
    m.unmount()
  })

  it('is in Persian digits and Toman in Persian', async () => {
    setLang('fa')
    const { m } = await open()
    await m.click('تتر')
    await preset(m, '۱۰ تتر').click()
    await settle(20)
    expect(m.text()).toContain('با نرخ امروز (هر تتر ۲۹۴٬۰۰۰ تومان) حدود ۲٬۹۴۰٬۰۰۰ تومان به کیف پول شما اضافه می‌شود.')
    m.unmount()
    world.rate = undefined
    vi.restoreAllMocks()
    const again = await open()
    await again.m.click('تتر')
    expect(again.m.text()).toContain('نرخ تتر الان مشخص نیست؛ به‌محض مشخص شدن نرخ، مبلغ به کیف پول شما اضافه می‌شود.')
    again.m.unmount()
  })
})

describe('starting the payment', () => {
  it('asks the store for the methods at that amount in USDT, and starts the payment with micro-USDT, the currency, the method and a key', async () => {
    const { m, calls } = await open()
    await m.click('USDT')
    await preset(m, '10 USDT').click()
    await settle(20)
    expect(calls.some((c) => c.path === '/methods?currency=USDT&amount=10000000')).toBe(true)
    expect(m.buttons().map(label)).toEqual(expect.arrayContaining(['USDT (TRC20)', 'USDT, then send a receipt', 'Back']))
    await m.click('USDT (TRC20)')
    await settle(20)
    expect(topups(calls)).toHaveLength(1)
    const body = bodyOf(topups(calls)[0])
    expect(body).toEqual({ amount: 10_000_000, currency: 'USDT', method: 'usdt_trc20_auto', key: expect.stringMatching(/^[0-9a-f]{24}$/) })
    m.unmount()
  })

  it('makes a new key for a new amount, and sends the amount that was chosen', async () => {
    const { m, calls } = await open()
    await m.click('USDT')
    await preset(m, '10 USDT').click()
    await settle(20)
    await m.click('USDT (TRC20)')
    await settle(20)
    await m.click('Back')
    await settle(20)
    await m.click('USDT')
    await type(m, '۲۵')
    await m.click('Continue')
    await settle(20)
    await m.click('USDT (TRC20)')
    await settle(20)
    const bodies = topups(calls).map(bodyOf)
    expect(bodies.map((b) => b.amount)).toEqual([10_000_000, 25_000_000])
    expect(bodies[0].key).not.toBe(bodies[1].key)
    expect(bodies.every((b) => b.currency === 'USDT')).toBe(true)
    m.unmount()
  })

  it('goes on in Toman as it always did: the request has no currency in it', async () => {
    const { m, calls } = await open() // the switch is there and is not used
    await m.click('100,000 Toman')
    await settle(20)
    expect(m.text()).toContain('100,000 Toman')
    await m.click('Card to card')
    await settle(20)
    expect(Object.keys(bodyOf(topups(calls)[0])).sort()).toEqual(['amount', 'key', 'method'])
    expect(bodyOf(topups(calls)[0])).toMatchObject({ amount: 100000, method: 'manual_card' })
    m.unmount()
  })

  it('goes on in Toman when the store has no USDT, and in Toman after the switch is put back', async () => {
    const { m, calls } = await open(false)
    await m.click('200,000 Toman')
    await settle(20)
    await m.click('Card to card')
    await settle(20)
    expect(bodyOf(topups(calls)[0])).not.toHaveProperty('currency')
    m.unmount()

    vi.restoreAllMocks()
    const back = await open()
    await back.m.click('USDT')
    await back.m.click('Toman')
    await back.m.click('500,000 Toman')
    await settle(20)
    await back.m.click('Card to card')
    await settle(20)
    expect(bodyOf(topups(back.calls)[0])).toMatchObject({ amount: 500000, method: 'manual_card' })
    expect(bodyOf(topups(back.calls)[0])).not.toHaveProperty('currency')
    back.m.unmount()
  })

  it('words a refusal of the amount or the method, and stays where it is', async () => {
    world.intent = () => json(400, { error: 'method_off', message: 'x' })
    const { m } = await open()
    await m.click('USDT')
    await preset(m, '5 USDT').click()
    await settle(20)
    await m.click('USDT (TRC20)')
    await settle(20)
    expect(m.el.querySelector('[role=alert]')?.textContent).toBe('This payment method is not available right now. Pick another one.')
    expect(m.text()).toContain('USDT (TRC20)') // still at the methods, to pick another
    expect(m.text()).not.toContain('Send exactly')
    world.intent = () => json(400, { error: 'invalid', message: 'top-up amount out of range' })
    await m.click('USDT (TRC20)')
    await settle(20)
    expect(m.el.querySelector('[role=alert]')?.textContent).toBe('The input is not valid.')
    m.unmount()
  })

  it('does not start from a payment of this method that already ran out', async () => {
    world.intent = () => autoIntent({ status: 'expired' })
    const { m } = await open()
    await m.click('USDT')
    await preset(m, '10 USDT').click()
    await settle(20)
    await m.click('USDT (TRC20)')
    await settle(20)
    expect(m.text()).toContain('The payment did not go through.')
    expect(m.text()).not.toContain('Send exactly')
    m.unmount()
  })
})

describe('the address, the QR code and the time left', () => {
  it('shows the exact amount whole, the address to copy, the network and the time left', async () => {
    const copied = clipboard()
    const { m } = await toAddress()
    const t = m.text()
    expect(t).toContain('Waiting for the payment')
    expect(t).toContain('Send exactly 10.000417 USDT to this address:')
    expect(m.el.querySelector('.mono')?.getAttribute('dir')).toBe('ltr')
    expect(m.el.querySelector('.mono')?.textContent?.replace(/[⁨⁩]/g, '')).toBe(ADDRESS)
    expect(t).toContain('Network TRC20')
    expect(t).toContain('Time left 10:00')
    expect(t).not.toContain('Valid until')
    expect(t).toContain('I have paid')
    await m.click('Copy')
    expect(copied).toEqual([ADDRESS])
    m.unmount()
  })

  it('draws the QR code core made, a square of about 176 pixels with a text for a screen reader', async () => {
    const { m } = await toAddress()
    const img = m.el.querySelectorAll('img')
    expect(img).toHaveLength(1)
    expect(img[0].getAttribute('src')).toBe(QR)
    expect(img[0].getAttribute('alt')).toBe('QR code of the USDT address')
    expect(img[0].getAttribute('width')).toBe('176')
    expect(img[0].getAttribute('height')).toBe('176')
    m.unmount()
  })

  it('draws nothing where core sent no QR code, and the rest is as it was', async () => {
    world.intent = () => autoIntent({ qr: undefined })
    const { m } = await toAddress()
    expect(m.el.querySelector('img')).toBeNull()
    expect(m.text()).toContain('Send exactly 10.000417 USDT to this address:')
    expect(m.text()).toContain(ADDRESS)
    m.unmount()
  })

  it.each([['an empty one', ''], ['a web address', 'https://evil.example/qr.png'], ['a script', 'javascript:alert(1)'], ['another type', 'data:text/html;base64,PHNjcmlwdD4=']])(
    'draws nothing for %s',
    async (_name, qr) => {
      world.intent = () => autoIntent({ qr })
      const { m } = await toAddress()
      expect(m.el.querySelector('img')).toBeNull()
      m.unmount()
    },
  )

  it('counts the time left down every second, in minutes and seconds', async () => {
    const { m } = await toAddress()
    expect(m.text()).toContain('Time left 10:00')
    await settle(1000)
    expect(m.text()).toContain('Time left 09:59')
    await settle(59_000)
    expect(m.text()).toContain('Time left 09:00')
    expect(m.el.querySelector('[role=timer]')?.getAttribute('dir')).toBe('ltr')
    m.unmount()
  })

  it('says when the time is over, takes the address away, and stops asking', async () => {
    world.intent = () => autoIntent({ details: { pay_address: ADDRESS, network: 'TRC20', expires_at: String(NOW / 1000 + 90) } })
    const { m, calls } = await toAddress()
    expect(m.text()).toContain('Time left 01:30')
    await settle(89_000)
    expect(m.text()).toContain('Time left 00:01')
    expect(m.text()).toContain(ADDRESS)
    expect(checks(calls)).toBeGreaterThan(10) // asked every 5 seconds meanwhile
    await settle(1_000)
    expect(m.text()).toContain('The time for this payment is over. Do not send anything to this address; go back and start again. If you have already sent it, tap “I have paid”.')
    expect(m.el.querySelector('[role=alert]')?.textContent).toContain('The time for this payment is over')
    expect(m.text()).not.toContain(ADDRESS)
    expect(m.text()).not.toContain('Send exactly')
    expect(m.text()).not.toContain('Time left')
    expect(m.text()).not.toContain('Waiting for the payment')
    expect(m.el.querySelector('img')).toBeNull() // and not the code to scan either
    expect(m.el.querySelector('[role=timer]')).toBeNull()
    const asked = checks(calls)
    await settle(10 * 60_000)
    expect(checks(calls)).toBe(asked) // not one more
    expect(vi.getTimerCount()).toBe(0)
    // Back starts again
    expect(m.buttons().map(label)).toEqual(['I have paid', 'Back'])
    await m.click('Back')
    expect(m.text()).toContain('From 1 to 10,000 USDT')
    m.unmount()
  })

  it('still lets the customer say they paid after the time is over, which asks once', async () => {
    world.intent = () => autoIntent({ details: { pay_address: ADDRESS, network: 'TRC20', expires_at: String(NOW / 1000 + 30) } })
    const { m, calls } = await toAddress()
    await settle(31_000)
    const asked = checks(calls)
    await m.click('I have paid')
    await settle(20)
    expect(checks(calls)).toBe(asked + 1)
    expect(m.text()).toContain('No payment seen yet.')
    await settle(60_000)
    expect(checks(calls)).toBe(asked + 1)
    m.unmount()
  })

  it('takes a moment already behind it for a wrong clock on the phone: the address stays, with when it is valid until', async () => {
    world.intent = () => autoIntent({ details: { pay_address: ADDRESS, network: 'TRC20', expires_at: String(NOW / 1000 - 3600) } })
    const { m, calls } = await toAddress()
    expect(m.text()).toContain(ADDRESS)
    expect(m.text()).toContain('Valid until')
    expect(m.text()).not.toContain('Time left')
    expect(m.text()).not.toContain('The time for this payment is over')
    await settle(30_000)
    expect(m.text()).toContain(ADDRESS)
    expect(checks(calls)).toBeGreaterThan(5) // and the server decides, as before
    m.unmount()
  })

  it('has no countdown for a payment that gave no time', async () => {
    world.intent = () => autoIntent({ details: { pay_address: ADDRESS, network: 'TRC20' } })
    const { m } = await toAddress()
    expect(m.text()).toContain(ADDRESS)
    expect(m.text()).not.toContain('Time left')
    expect(m.text()).not.toContain('Valid until')
    expect(vi.getTimerCount()).toBe(1) // only the 5-second asking
    m.unmount()
  })

  it('reads left to right where it should in Persian: the amount, the address, the time', async () => {
    setLang('fa')
    const { m } = await open()
    await m.click('تتر')
    await preset(m, '۱۰ تتر').click()
    await settle(20)
    await m.click('تتر (TRC20)')
    await settle(20)
    const t = m.text()
    expect(t).toContain('دقیقاً 10.000417 تتر را به این آدرس بفرستید:') // the amount to type keeps its Latin digits
    expect(t).toContain('شبکه TRC20')
    expect(t).toContain('زمان باقی‌مانده ۱۰:۰۰')
    expect(m.el.querySelector('.mono')?.getAttribute('dir')).toBe('ltr')
    expect(m.el.querySelector('.mono')?.textContent).toContain(ADDRESS)
    expect(m.el.querySelector('[role=timer]')?.getAttribute('dir')).toBe('ltr')
    expect(m.el.querySelector('img')?.getAttribute('alt')).toBe('کد QR آدرس تتر')
    await settle(600_000)
    expect(m.text()).toContain('زمان این پرداخت تمام شد.')
    m.unmount()
  })
})

describe('the wallet that fills when the payment is seen', () => {
  it('stops asking, says it is adding, and shows the new balance and what was added when the balance goes up', async () => {
    const { m, calls } = await toAddress()
    expect(m.buttons().map(label)).toEqual(['Copy', 'I have paid', 'Back'])
    await settle(10_000) // pending...
    const before = walletReads(calls)
    world.status = 'succeeded'
    await settle(5000) // ...seen
    expect(m.text()).toContain('Your payment of 10 USDT is confirmed.')
    expect(m.text()).toContain('Adding it to your wallet…')
    expect(m.text()).not.toContain('Send exactly') // the payment screen is gone
    expect(walletReads(calls)).toBe(before + 1) // at once
    const asked = checks(calls)
    // the credit is a moment late: two more looks at the old balance...
    await settle(2000)
    expect(walletReads(calls)).toBe(before + 2)
    expect(m.text()).toContain('Adding it to your wallet…')
    await settle(2000)
    expect(walletReads(calls)).toBe(before + 3)
    expect(m.text()).toContain('Adding it to your wallet…')
    expect(m.text()).not.toContain('Added to your wallet')
    // ...and then the balance has gone up
    world.balance = 3_940_000
    await settle(2000)
    expect(walletReads(calls)).toBe(before + 4)
    expect(m.text()).not.toContain('Adding it to your wallet…')
    expect(creditRows(m)).toEqual([['Added to your wallet', '+2,940,000 Toman'], ['The wallet now', '3,940,000 Toman']])
    expect(credit(m)?.textContent).toContain('Your payment of 10 USDT is confirmed.')
    expect(credit(m)?.textContent).toContain('About 13.4 USDT · USDT rate now: 294,000 Toman')
    expect(credit(m)?.querySelector('.pos')?.getAttribute('dir')).toBe('ltr')
    expect(state.wallets[0].balance).toBe(3_940_000)
    expect(m.el.querySelector('[role=alert]')).toBeNull()
    // no more looking, at the payment or at the wallet
    await settle(5 * 60_000)
    expect(checks(calls)).toBe(asked)
    expect(walletReads(calls)).toBe(before + 4)
    expect(vi.getTimerCount()).toBe(0)
    // the screen is ready for the next top-up, and the generic text of a Toman top-up is not used
    expect(m.text()).toContain('From 1 to 10,000 USDT')
    expect(m.text()).not.toContain('The wallet balance went up.')
    m.unmount()
  })

  it('leaves the amount field empty for the next top-up, not holding the amount just paid', async () => {
    const o = await open()
    const { m } = o
    await m.click('USDT')
    await type(m, '10')
    await m.click('Continue')
    await settle(20)
    await m.click('USDT (TRC20)')
    await settle(20)
    world.status = 'succeeded'
    world.balance = 3_940_000
    await settle(5000)
    expect(credit(m)).not.toBeNull()
    expect(input(m).value).toBe('')
    expect(m.text()).not.toContain('will be added to your wallet at') // and no estimate for an amount that is not there
    m.unmount()
  })

  it('shows it at once when the balance has already gone up by the first look', async () => {
    const { m } = await toAddress()
    world.status = 'succeeded'
    world.balance = 3_940_000
    await settle(5000)
    expect(creditRows(m)).toEqual([['Added to your wallet', '+2,940,000 Toman'], ['The wallet now', '3,940,000 Toman']])
    m.unmount()
  })

  it('says the same when the customer taps I have paid and the payment is there', async () => {
    const { m, calls } = await toAddress()
    world.status = 'succeeded'
    world.balance = 3_940_000
    await m.click('I have paid')
    await settle(20)
    expect(checks(calls)).toBe(1)
    expect(creditRows(m)).toEqual([['Added to your wallet', '+2,940,000 Toman'], ['The wallet now', '3,940,000 Toman']])
    await settle(60_000)
    expect(checks(calls)).toBe(1)
    m.unmount()
  })

  it('looks eight more times, two seconds apart, and then says it can take a minute', async () => {
    const { m, calls } = await toAddress()
    const before = walletReads(calls)
    world.status = 'succeeded'
    await settle(5000)
    expect(walletReads(calls)).toBe(before + 1)
    for (let i = 2; i <= 9; i++) {
      await settle(2000)
      expect(walletReads(calls), `look ${i}`).toBe(before + i)
    }
    expect(m.text()).not.toContain('Adding it to your wallet…')
    expect(m.text()).toContain('Your payment of 10 USDT is confirmed.')
    expect(m.text()).toContain('It can take a minute to reach your wallet. The balance will be up to date whenever you open the wallet again; or tap “Try again” to check now.')
    expect(m.text()).not.toContain('Added to your wallet')
    await settle(5 * 60_000)
    expect(walletReads(calls)).toBe(before + 9) // and no more by itself
    expect(vi.getTimerCount()).toBe(0)
    // Try again looks the same way, and finds it when it has come
    world.balance = 3_940_000
    await m.click('Try again')
    await settle(20)
    expect(walletReads(calls)).toBe(before + 10)
    expect(creditRows(m)).toEqual([['Added to your wallet', '+2,940,000 Toman'], ['The wallet now', '3,940,000 Toman']])
    m.unmount()
  })

  it('counts a rise from what the balance was when the payment began, not from zero', async () => {
    world.balance = 5_000_000
    const { m } = await toAddress()
    world.status = 'succeeded'
    world.balance = 5_000_000 + 2_940_000
    await settle(5000)
    expect(creditRows(m)).toEqual([['Added to your wallet', '+2,940,000 Toman'], ['The wallet now', '7,940,000 Toman']])
    m.unmount()
  })

  it('tells a wallet with no rate known the balance without the USDT line', async () => {
    const { m } = await toAddress()
    world.status = 'succeeded'
    world.balance = 3_940_000
    world.rate = undefined
    await settle(5000)
    expect(creditRows(m)).toEqual([['Added to your wallet', '+2,940,000 Toman'], ['The wallet now', '3,940,000 Toman']])
    expect(credit(m)?.textContent).not.toContain('USDT rate now')
    m.unmount()
  })

  it('stops looking when the customer leaves the tab', async () => {
    const { m, calls } = await toAddress()
    world.status = 'succeeded'
    await settle(5000)
    const looked = walletReads(calls)
    m.unmount()
    await settle(60_000)
    expect(walletReads(calls)).toBe(looked)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not mix a second top-up with the first one\'s loop', async () => {
    const { m, calls } = await toAddress()
    world.status = 'succeeded'
    await settle(5000)
    expect(m.text()).toContain('Adding it to your wallet…')
    // a new top-up begins before the first has landed: its screen takes over, the old loop is dropped
    world.status = 'pending'
    world.intent = () => autoIntent({ intent_id: 'i3' })
    await preset(m, '5 USDT').click()
    await settle(20)
    await m.click('USDT (TRC20)')
    await settle(20)
    expect(credit(m)).toBeNull()
    const looked = walletReads(calls)
    await settle(60_000)
    expect(walletReads(calls)).toBe(looked)
    m.unmount()
  })

  it('writes the result in Persian, with Persian digits and the amount added reading left to right', async () => {
    setLang('fa')
    const { m } = await open()
    await m.click('تتر')
    await preset(m, '۱۰ تتر').click()
    await settle(20)
    await m.click('تتر (TRC20)')
    await settle(20)
    world.status = 'succeeded'
    world.balance = 3_940_000
    await settle(5000)
    expect(credit(m)?.textContent).toContain('پرداخت ۱۰ تتر شما تأیید شد.')
    expect(creditRows(m)).toEqual([['اضافه شده به کیف پول', '+۲٬۹۴۰٬۰۰۰ تومان'], ['موجودی کیف پول اکنون', '۳٬۹۴۰٬۰۰۰ تومان']])
    expect(credit(m)?.querySelector('.pos')?.getAttribute('dir')).toBe('ltr')
    expect(credit(m)?.textContent).toContain('معادل ۱۳٫۴ تتر · نرخ لحظه‌ای تتر: ۲۹۴٬۰۰۰ تومان')
    m.unmount()
  })

  it.each(['failed', 'expired', 'rejected'])('(%s) says it did not go through, stops asking and loads no wallet', async (status) => {
    const { m, calls } = await toAddress()
    const before = walletReads(calls)
    world.status = status
    await settle(5000)
    expect(m.text()).toContain('The payment did not go through.')
    expect(credit(m)).toBeNull()
    expect(m.text()).not.toContain('is confirmed')
    const asked = checks(calls)
    await settle(60_000)
    expect(checks(calls)).toBe(asked)
    expect(walletReads(calls)).toBe(before)
    m.unmount()
  })

  it('leaves a Toman top-up as it was: the generic text, and no result card', async () => {
    const { m } = await open()
    world.intent = () => autoIntent({ intent_id: 'i4', provider: 'manual_card', amount: 100000, currency: 'IRT', scale: 0, qr: undefined, details: { card_number: '6037-9911-2222-3333', reference: 'AB12CD34' } })
    await m.click('100,000 Toman')
    await settle(20)
    await m.click('Card to card')
    await settle(20)
    world.status = 'succeeded'
    world.balance = 1_100_000
    await settle(30_000)
    expect(m.text()).toContain('The wallet balance went up.')
    expect(credit(m)).toBeNull()
    expect(state.wallets[0].balance).toBe(1_100_000)
    m.unmount()
  })
})

describe('a USDT top-up paid by hand', () => {
  it('shows the ordinary screen of a payment by hand, with the amount in USDT', async () => {
    world.intent = () => manualIntent()
    const copied = clipboard()
    const { m } = await open()
    await m.click('USDT')
    await preset(m, '10 USDT').click()
    await settle(20)
    await m.click('USDT, then send a receipt')
    await settle(20)
    const t = m.text()
    expect(t).toContain('USDT, then send a receipt')
    expect(t).toContain('Send exactly 10 USDT to one of these addresses:')
    const card = Array.from(m.el.querySelectorAll('section.card')).find((c) => c.querySelector('h2')?.textContent === 'Add funds')
    expect(card?.textContent).not.toContain('Toman') // no price in Toman, no rate: the amount is the price
    expect(card?.textContent).not.toContain('Rate now')
    expect(t).toContain('Address on the TRC20 network')
    expect(t).toContain('Address on the ERC20 network')
    expect(t).toContain('EF56AB78')
    expect(t).toContain('After paying, send the receipt')
    expect(m.el.querySelector('img')).toBeNull()
    const copyButtons = m.buttons().filter((b) => label(b) === 'Copy')
    expect(copyButtons).toHaveLength(4) // the amount, the two addresses, the reference
    copyButtons[0].click()
    await settle(5)
    expect(copied).toEqual(['10'])
    m.unmount()
  })

  it('shows a USDT price that is not a whole number of USDT whole, as it is to be sent', async () => {
    const m = mount(PayPanel, { intent: manualIntent({ amount: 2_500_731, usdt_amount: 2_500_731 }), checking: false, message: '' }, [])
    await settle(5)
    expect(m.text()).toContain('Send exactly 2.500731 USDT to one of these addresses:')
    m.unmount()
  })

  it('is asked about every 30 seconds, and the wallet fills when the staff have approved it', async () => {
    world.intent = () => manualIntent({ status: 'confirming' })
    const { m, calls } = await open()
    await m.click('USDT')
    await preset(m, '10 USDT').click()
    await settle(20)
    await m.click('USDT, then send a receipt')
    await settle(20)
    expect(m.text()).toContain('Receipt received')
    await settle(29_000)
    expect(checks(calls)).toBe(0)
    world.status = 'succeeded'
    world.balance = 3_940_000
    await settle(2_000)
    expect(checks(calls)).toBe(1)
    expect(creditRows(m)).toEqual([['Added to your wallet', '+2,940,000 Toman'], ['The wallet now', '3,940,000 Toman']])
    expect(credit(m)?.textContent).toContain('Your payment of 10 USDT is confirmed.')
    await settle(10 * 60_000)
    expect(checks(calls)).toBe(1)
    m.unmount()
  })
})

describe('the calls', () => {
  it('sends the currency only when it is asked to', async () => {
    const calls = signedIn(() => autoIntent())
    await api.topup(100000, 'zarinpal', 'k1')
    await api.topup(100000, 'zarinpal', 'k2', 'IRT')
    await api.topup(10_000_000, 'usdt_trc20_auto', 'k3', 'USDT')
    expect(calls.map((c) => [c.method, c.path, c.init.body])).toEqual([
      ['POST', '/topup', '{"amount":100000,"method":"zarinpal","key":"k1"}'],
      ['POST', '/topup', '{"amount":100000,"method":"zarinpal","key":"k2"}'],
      ['POST', '/topup', '{"amount":10000000,"currency":"USDT","method":"usdt_trc20_auto","key":"k3"}'],
    ])
  })

  it('has every text of the USDT top-up in both languages, with the same placeholders', () => {
    const keys = [
      'wallet.currency', 'wallet.cur_irt', 'wallet.cur_usdt', 'wallet.custom_usdt', 'wallet.est_usdt', 'wallet.est_none', 'wallet.usdt_paid',
      'wallet.usdt_adding', 'wallet.usdt_added', 'wallet.usdt_now', 'wallet.usdt_late', 'pay.usdt_qr', 'pay.usdt_left', 'pay.usdt_over',
    ]
    const names = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()
    for (const key of keys) {
      expect(messages.fa[key], `fa ${key}`).toBeTruthy()
      expect(messages.en[key], `en ${key}`).toBeTruthy()
      expect(names(messages.fa[key]), key).toEqual(names(messages.en[key]))
    }
    expect(names(messages.en['wallet.est_usdt'])).toEqual(['rate', 'toman'])
    expect(names(messages.en['wallet.usdt_paid'])).toEqual(['amount'])
  })
})
