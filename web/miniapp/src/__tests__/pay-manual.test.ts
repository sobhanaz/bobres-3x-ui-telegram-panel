import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Intent, Me } from '../api'
import ManualPay from '../components/ManualPay.vue'
import { setLang } from '../i18n'
import { state } from '../store'
import { chooseFile, clipboard, json, mount, noTelegram, pngBytes, settle, signedIn, telegram, type Mounted } from './helpers'

const HASH = 'ab12'.repeat(16)
const card = (over: Partial<Intent> = {}): Intent => ({
  intent_id: 'i1', status: 'pending', provider: 'manual_card', order_id: 'o1', amount: 150000, currency: 'IRT', scale: 0, pay_url: '',
  gateway_amount: 0, gateway_currency: '', scale_gateway: -1,
  details: { card_number: '6037-9911-2222-3333', card_holder: 'Ali Rezaei', reference: 'AB12CD34' }, ...over,
})
const crypto = (over: Partial<Intent> = {}): Intent =>
  card({
    provider: 'manual_crypto', usdt_amount: 2_500_000,
    details: { usdt_trc20: 'TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r', usdt_erc20: '0x0000000000000000000000000000000000000001', reference: 'AB12CD34' }, ...over,
  })
const link = (over: Partial<Intent> = {}): Intent =>
  card({ provider: 'manual_zarinpal', details: { zarinpal_link: 'https://zarinp.al/shop', reference: 'AB12CD34' }, ...over })
const props = (intent: Intent) => ({ intent, checking: false, message: '' })
const input = (m: Mounted, i: number) => m.el.querySelectorAll('input[type=text]')[i] as HTMLInputElement
async function type(m: Mounted, i: number, value: string) {
  const el = input(m, i)
  el.value = value
  el.dispatchEvent(new Event('input'))
  await settle(5)
}
const png = (name = 'IMG_2041 Ali Reza.png') => new File([pngBytes()], name, { type: 'image/png' })

beforeEach(() => {
  setLang('en')
  state.gate = ''
  state.me = null
})
afterEach(() => {
  vi.restoreAllMocks()
  noTelegram()
  document.body.innerHTML = ''
})

describe('ManualPay: card to card', () => {
  it('shows what the bot shows, with a copy button for each', async () => {
    const copied = clipboard()
    const m = mount(ManualPay, props(card()), ['check', 'leave'])
    await settle(5)
    const t = m.text()
    expect(t).toContain('Card to card')
    expect(t).toContain('150,000 Toman')
    expect(t).toContain('6037-9911-2222-3333')
    expect(t).toContain('Card holder: Ali Rezaei')
    expect(t).toContain('AB12CD34')
    const copy = m.buttons().filter((b) => b.textContent?.trim() === 'Copy')
    expect(copy).toHaveLength(3) // the amount, the card, the reference
    for (const b of copy) {
      b.click()
      await settle(5)
    }
    expect(copied).toEqual(['150000', '6037991122223333', 'AB12CD34']) // the digits a bank app takes
    m.unmount()
  })

  it('sends the receipt and the bank number, then waits for review', async () => {
    const calls = signedIn(() => ({ intent_id: 'i1', status: 'confirming' }))
    const m = mount(ManualPay, props(card()), ['check', 'leave'])
    await settle(5)
    const send = () => m.button('Send the receipt')
    expect(send().disabled).toBe(true)

    await chooseFile(m.el, png())
    const img = m.el.querySelector('img')
    expect(img?.getAttribute('src')?.startsWith('data:image/png;base64,')).toBe(true) // a data: URL: the page's policy allows no blob:
    expect(m.text()).toContain('receipt.png') // never the file's own name
    expect(m.text()).not.toContain('IMG_2041')
    expect(send().disabled).toBe(true) // a card transfer needs the bank's number

    await type(m, 0, '998877')
    expect(send().disabled).toBe(false)
    await m.click('Send the receipt')
    await settle(30)

    expect(calls).toHaveLength(1)
    expect(calls[0].path).toBe('/payments/i1/proof')
    const form = calls[0].init.body as FormData
    expect(form.get('reference')).toBe('998877')
    expect((form.get('receipt') as File).name).toBe('receipt.png')
    expect((form.get('receipt') as File).type).toBe('image/png')
    expect(m.text()).toContain('Receipt received')
    expect(m.text()).toContain('review your payment')
    expect(m.el.querySelector('input[type=file]')).toBeNull() // no form once it is sent

    await m.click('Check status')
    expect(m.events.check).toEqual([[]])
    await m.click('See my services')
    expect(m.events.leave).toHaveLength(1)
    m.unmount()
  })

  it('has no way out when the screen around it has its own', async () => {
    const m = mount(ManualPay, { ...props(card({ status: 'confirming' })), showLeave: false }, ['leave'])
    await settle(5)
    expect(m.text()).toContain('Receipt received')
    expect(m.buttons().map((b) => b.textContent?.trim())).toEqual(['Check status'])
    m.unmount()
  })

  it('says what is wrong with the file before sending anything', async () => {
    const calls = signedIn(() => ({}))
    const m = mount(ManualPay, props(card()), [])
    await settle(5)
    await chooseFile(m.el, new File(['<!doctype html><script>alert(1)</script>'], 'receipt.png', { type: 'image/png' }))
    expect(m.text()).toContain('Send a JPEG, PNG or WebP image.')
    expect(m.el.querySelector('img')).toBeNull()
    await chooseFile(m.el, new File([], 'empty.png', { type: 'image/png' }))
    expect(m.text()).toContain('The file is empty.')
    await chooseFile(m.el, new File(['%PDF-1.7 not an image'], 'receipt.pdf', { type: 'application/pdf' }))
    expect(m.text()).toContain('Send a JPEG, PNG or WebP image.')
    await type(m, 0, '998877')
    expect(m.button('Send the receipt').disabled).toBe(true)
    expect(calls).toHaveLength(0)
    // a good one after the bad ones clears the message
    await chooseFile(m.el, png())
    expect(m.text()).not.toContain('Send a JPEG')
    expect(m.button('Send the receipt').disabled).toBe(false)
    m.unmount()
  })

  it('shows the server\'s refusal and lets the customer fix it', async () => {
    let answer: Response = json(415, { error: 'bad_type', message: 'x' })
    const calls = signedIn(() => answer)
    const m = mount(ManualPay, props(card()), [])
    await settle(5)
    await chooseFile(m.el, png())
    await type(m, 0, '998877')
    await m.click('Send the receipt')
    await settle(30)
    expect(m.text()).toContain('Send a JPEG, PNG or WebP image.')
    expect(m.text()).not.toContain('Receipt received')
    expect(m.button('Send the receipt').disabled).toBe(false) // can try again
    answer = json(413, { error: 'too_large', message: 'x' })
    await m.click('Send the receipt')
    await settle(30)
    expect(m.text()).toContain('The image is too large (8 MB at most).')
    answer = json(429, { error: 'rate_limited', message: 'x' })
    await m.click('Send the receipt')
    await settle(30)
    expect(m.text()).toContain('Too many requests')
    answer = json(200, { intent_id: 'i1', status: 'confirming' })
    await m.click('Send the receipt')
    await settle(30)
    expect(m.text()).toContain('Receipt received')
    expect(calls).toHaveLength(4)
    m.unmount()
  })

  it('sends a customer who never started the bot to the bot', async () => {
    const open = vi.fn()
    telegram({ openTelegramLink: open })
    state.me = { brand: { name: '', color: '', support: '', support_url: '', bot_url: 'https://t.me/shop_bot' } } as Me
    signedIn(() => json(409, { error: 'chat_closed', message: 'x' }))
    const m = mount(ManualPay, props(card()), [])
    await settle(5)
    await chooseFile(m.el, png())
    await type(m, 0, '998877')
    await m.click('Send the receipt')
    await settle(30)
    expect(m.text()).toContain('Open the bot and tap Start')
    await m.click('Open the bot')
    expect(open).toHaveBeenCalledWith('https://t.me/shop_bot')
    m.unmount()
  })

  it('takes "already sent" as sent, and shows a full-screen gate for an ended session', async () => {
    signedIn(() => json(409, { error: 'already_submitted', message: 'x' }))
    let m = mount(ManualPay, props(card()), [])
    await settle(5)
    await chooseFile(m.el, png())
    await type(m, 0, '998877')
    await m.click('Send the receipt')
    await settle(30)
    expect(m.text()).toContain('Receipt received')
    m.unmount()

    signedIn(() => json(403, { error: 'banned', message: 'x' }))
    m = mount(ManualPay, props(card()), [])
    await settle(5)
    await chooseFile(m.el, png())
    await type(m, 0, '998877')
    await m.click('Send the receipt')
    await settle(30)
    expect(state.gate).toBe('banned')
    m.unmount()
  })

  it('keeps the form when the store is busy, and gives up the screen only when the session is over', async () => {
    // The receipt is chosen and the number typed in: a busy store, payments away or maintenance must not throw them away.
    let answer: Response = json(503, { error: 'unavailable', message: 'x' })
    signedIn(() => answer)
    const m = mount(ManualPay, props(card()), [])
    await settle(5)
    await chooseFile(m.el, png())
    await type(m, 0, '998877')
    await m.click('Send the receipt')
    await settle(30)
    expect(state.gate).toBe('')
    expect(m.text()).toContain('Busy right now. Try again in a moment.')
    expect(m.el.querySelector('img')).not.toBeNull() // the picture is still there
    expect(input(m, 0).value).toBe('998877')
    expect(m.button('Send the receipt').disabled).toBe(false)
    answer = json(503, { error: 'maintenance', message: 'x' })
    await m.click('Send the receipt')
    await settle(30)
    expect(state.gate).toBe('')
    expect(m.text()).toContain('The store is under maintenance.')
    expect(m.el.querySelector('img')).not.toBeNull()
    // Signed out: there is nothing to send with, and the screen says so.
    answer = json(401, { error: 'unauthorized', message: 'x' })
    await m.click('Send the receipt')
    await settle(30)
    expect(state.gate).toBe('expired')
    m.unmount()
  })

  it('starts where the payment is: waiting, approved, or closed', async () => {
    let m = mount(ManualPay, props(card({ status: 'confirming' })), [])
    await settle(5)
    expect(m.text()).toContain('Receipt received')
    expect(m.el.querySelector('input[type=file]')).toBeNull()
    m.unmount()
    m = mount(ManualPay, props(card({ status: 'succeeded' })), [])
    await settle(5)
    expect(m.text()).toContain('Your payment was approved.')
    m.unmount()
    for (const status of ['failed', 'expired']) {
      m = mount(ManualPay, props(card({ status })), [])
      await settle(5)
      expect(m.text()).toContain('This payment is closed.')
      expect(m.el.querySelector('input[type=file]')).toBeNull()
      m.unmount()
    }
  })

  it('shows a message from the screen around it', async () => {
    const m = mount(ManualPay, { ...props(card({ status: 'confirming' })), message: 'Still waiting for review.' }, [])
    await settle(5)
    expect(m.text()).toContain('Still waiting for review.')
    m.unmount()
  })

  it('is in Persian first', async () => {
    setLang('fa')
    const m = mount(ManualPay, props(card()), [])
    await settle(5)
    expect(m.text()).toContain('کارت به کارت')
    expect(m.text()).toContain('شماره کارت')
    expect(m.text()).toContain('۱۵۰٬۰۰۰ تومان')
    expect(m.text()).toContain('شماره پیگیری بانک')
    m.unmount()
  })
})

describe('ManualPay: USDT', () => {
  it('shows both addresses and the exact amount in USDT', async () => {
    const copied = clipboard()
    const m = mount(ManualPay, props(crypto()), [])
    await settle(5)
    const t = m.text()
    expect(t).toContain('Send exactly 2.5 USDT (about 150,000 Toman)')
    expect(t).toContain('Address on the TRC20 network')
    expect(t).toContain('TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r')
    expect(t).toContain('Address on the ERC20 network')
    expect(t).toContain('0x0000000000000000000000000000000000000001')
    for (const b of m.buttons().filter((b) => b.textContent?.trim() === 'Copy')) {
      b.click()
      await settle(5)
    }
    expect(copied).toEqual(['2.5', 'TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r', '0x0000000000000000000000000000000000000001', 'AB12CD34'])
    m.unmount()
  })

  it('says the price as it is for an order priced in USDT', async () => {
    const m = mount(ManualPay, props(crypto({ currency: 'USDT', scale: 6, amount: 5_000_000, usdt_amount: 5_000_000 })), [])
    await settle(5)
    expect(m.text()).toContain('Send exactly 5 USDT to one of these addresses')
    expect(m.text()).not.toContain('about')
    m.unmount()
  })

  it('shows an amount with fractions of a cent whole, as it is copied', async () => {
    // a price in USDT can have any of the 6 decimals; "4.12" on the screen over "4.123456" in the clipboard would be a payment short by 0.003456
    const copied = clipboard()
    const m = mount(ManualPay, props(crypto({ currency: 'USDT', scale: 6, amount: 4_123_456, usdt_amount: 4_123_456 })), [])
    await settle(5)
    expect(m.text()).toContain('Send exactly 4.123456 USDT to one of these addresses')
    expect(m.text()).not.toContain('4.12 USDT')
    m.buttons().filter((b) => b.textContent?.trim() === 'Copy')[0].click()
    await settle(5)
    expect(copied).toEqual(['4.123456'])
    m.unmount()
  })

  it('takes a screenshot with no bank number, or a transaction hash', async () => {
    const calls = signedIn(() => ({ intent_id: 'i1', status: 'confirming' }))
    const m = mount(ManualPay, props(crypto()), [])
    await settle(5)
    expect(m.text()).toContain('(optional)')
    await chooseFile(m.el, png())
    expect(m.button('Send the receipt').disabled).toBe(false) // no number needed
    await m.click('Transaction hash (TXID)')
    expect(m.el.querySelector('input[type=file]')).toBeNull()
    const send = () => m.button('Send the hash')
    expect(send().disabled).toBe(true)
    await type(m, 0, HASH.slice(1))
    expect(send().disabled).toBe(true)
    await type(m, 0, ' ' + HASH.slice(0, 32) + ' ' + HASH.slice(32))
    expect(send().disabled).toBe(false) // spaces from pasting do not matter
    const select = m.el.querySelector('select') as HTMLSelectElement
    expect(select.value).toBe('TRC20')
    await m.click('Send the hash')
    await settle(30)
    expect(calls).toHaveLength(1)
    expect(calls[0].path).toBe('/payments/i1/proof')
    expect(JSON.parse(String(calls[0].init.body))).toEqual({ txid: HASH, network: 'TRC20' })
    expect(m.text()).toContain('Receipt received')
    m.unmount()
  })

  it('knows an Ethereum hash by its 0x, lets the customer choose the network, and takes only the networks the store has', async () => {
    const calls = signedIn(() => ({ intent_id: 'i1', status: 'confirming' }))
    let m = mount(ManualPay, props(crypto()), [])
    await settle(5)
    await m.click('Transaction hash (TXID)')
    await type(m, 0, '0x' + HASH)
    let select = m.el.querySelector('select') as HTMLSelectElement
    expect(select.value).toBe('ERC20')
    select.value = 'TRC20' // the customer knows better
    select.dispatchEvent(new Event('change'))
    await settle(5)
    await m.click('Send the hash')
    await settle(30)
    expect(JSON.parse(String(calls[0].init.body))).toEqual({ txid: '0x' + HASH, network: 'TRC20' })
    m.unmount()

    // a store with an ERC20 address only: nothing to choose
    m = mount(ManualPay, props(crypto({ details: { usdt_erc20: '0x0000000000000000000000000000000000000001', reference: 'AB12CD34' } })), [])
    await settle(5)
    await m.click('Transaction hash (TXID)')
    await type(m, 0, HASH)
    select = m.el.querySelector('select') as HTMLSelectElement
    expect(select).toBeNull()
    await m.click('Send the hash')
    await settle(30)
    expect(JSON.parse(String(calls[1].init.body))).toEqual({ txid: HASH, network: 'ERC20' })
    m.unmount()
  })

  it('says a transaction was used before', async () => {
    signedIn(() => json(409, { error: 'duplicate', message: 'x' }))
    const m = mount(ManualPay, props(crypto()), [])
    await settle(5)
    await m.click('Transaction hash (TXID)')
    await type(m, 0, HASH)
    await m.click('Send the hash')
    await settle(30)
    expect(m.text()).toContain('This transaction was already submitted.')
    m.unmount()
  })
})

describe('ManualPay: the Zarinpal payment link', () => {
  it('opens only a web address', async () => {
    const open = vi.fn()
    telegram({ openLink: open })
    for (const bad of ['javascript:alert(1)', 'data:text/html,<script>1</script>', 'tg://resolve?domain=x', '']) {
      const m = mount(ManualPay, props(link({ details: { zarinpal_link: bad, reference: 'AB12CD34' } })), [])
      await settle(5)
      expect(m.buttons().map((b) => b.textContent?.trim()), bad).not.toContain('Open the payment page')
      m.unmount()
    }
    expect(open).not.toHaveBeenCalled()
  })

  it('opens the link, and wants the receipt with the tracking number', async () => {
    const open = vi.fn()
    telegram({ openLink: open })
    const calls = signedIn(() => ({ intent_id: 'i1', status: 'confirming' }))
    const m = mount(ManualPay, props(link()), [])
    await settle(5)
    expect(m.text()).toContain("Pay 150,000 Toman on the store's Zarinpal payment page")
    expect(m.text()).toContain('AB12CD34')
    await m.click('Open the payment page')
    expect(open).toHaveBeenCalledWith('https://zarinp.al/shop')
    await chooseFile(m.el, png())
    expect(m.button('Send the receipt').disabled).toBe(true)
    await type(m, 0, '123456789')
    await m.click('Send the receipt')
    await settle(30)
    expect((calls[0].init.body as FormData).get('reference')).toBe('123456789')
    expect(m.text()).toContain('Receipt received')
    m.unmount()
  })
})
