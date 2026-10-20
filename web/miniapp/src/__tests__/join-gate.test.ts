import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App.vue'
import { ApiError, resetSession, signIn } from '../api'
import { gateFor } from '../errors'
import { setLang } from '../i18n'
import { fail, state } from '../store'
import { json, mount, noTelegram, settle, telegram, type Mounted } from './helpers'

// The store's required channel (docs/17-mini-app.md): core answers a customer outside it with 403 join_required and the
// channel's public link, at sign-in and at any later call; the app shows a full-screen gate with a Join button and "I've
// joined", which signs in again.

const ME = { user: { id: 'u', username: '', language: 'en' }, wallets: [], brand: { name: '', color: '', support: '', support_url: '', bot_url: 'https://t.me/shop_bot' }, maintenance: false, top_up: { currency: 'IRT', min: 1, max: 2 }, features: {}, token: 'tok1', expires_in: 3600 }
const joined = () => json(200, ME)
const outside = (url = 'https://t.me/mynews') => () => json(403, { error: 'join_required', message: 'join the store’s channel to continue', join_url: url })

type Answer = Response | (() => Response | Promise<Response>)

/** serve answers the sign-ins in turn (the last one goes on answering) and the screens' calls by routes (an empty store otherwise). */
function serve(auth: Answer[], routes: Record<string, () => Response> = {}) {
  const calls: string[] = []
  let signIns = 0
  const empty: Record<string, () => Response> = {
    '/plans': () => json(200, { plans: [] }),
    '/services': () => json(200, { services: [] }),
    '/wallet': () => json(200, { wallets: [], ledger: [] }),
  }
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) => {
    const path = String(url).replace('/api/mini/v1', '')
    calls.push(path)
    if (path === '/auth') {
      const a = auth[Math.min(signIns++, auth.length - 1)]
      return typeof a === 'function' ? a() : a.clone()
    }
    return (routes[path] ?? empty[path] ?? (() => json(200, {})))()
  })
  return { calls, signIns: () => signIns }
}

const labels = (m: Mounted) => m.buttons().map((b) => (b.textContent ?? '').trim())

beforeEach(() => {
  resetSession()
  window.location.hash = '#tgWebAppData=' + encodeURIComponent('user=%7B%7D&hash=abc')
  Object.assign(state, { booted: false, gate: '', joinUrl: '', me: null, plans: null, services: null, wallets: [], ledger: [] })
  setLang('en')
})
afterEach(() => {
  vi.restoreAllMocks()
  noTelegram()
  document.body.innerHTML = ''
  window.location.hash = ''
})

describe('the channel gate', () => {
  it('stops a customer outside the channel at sign-in and sends them to it', async () => {
    const open = vi.fn()
    telegram({ openTelegramLink: open })
    serve([outside()])
    const m = mount(App)
    await settle(10)
    expect(m.text()).toContain('To use the store, join our channel first, then tap “I’ve joined”.')
    // the app's own "Open the bot" button has no place here: the customer is in the bot already
    expect(labels(m)).toEqual(['Join the channel', 'I’ve joined'])
    expect(m.el.querySelector('nav')).toBeNull() // nothing of the store behind it
    await m.click('Join the channel')
    expect(open).toHaveBeenCalledWith('https://t.me/mynews')
    m.unmount()
  })

  it('has no Join button when the store gave no link, only the check', async () => {
    serve([outside('')])
    const m = mount(App)
    await settle(10)
    expect(labels(m)).toEqual(['I’ve joined'])
    m.unmount()
  })

  it('says "not yet" when the customer is still outside, and lets them try again', async () => {
    const s = serve([outside()])
    const m = mount(App)
    await settle(10)
    await m.click('I’ve joined')
    await settle(10)
    expect(s.signIns()).toBe(2) // asked again at once
    expect(m.text()).toContain('You haven’t joined the channel yet. Join it first, then tap “I’ve joined” again.')
    expect(m.button('I’ve joined').disabled).toBe(false)
    expect(state.gate).toBe('join_required')
    m.unmount()
  })

  it('lets the customer in when they have joined', async () => {
    const s = serve([outside(), joined])
    const m = mount(App)
    await settle(10)
    await m.click('I’ve joined')
    await settle(20)
    expect(state.gate).toBe('')
    expect(state.joinUrl).toBe('')
    expect(m.text()).not.toContain('join our channel')
    expect(m.text()).toContain('No plans for sale right now.') // the store itself
    expect(m.el.querySelector('nav')).not.toBeNull()
    expect(s.signIns()).toBe(2)
    m.unmount()
  })

  it('cannot be pressed twice while it checks', async () => {
    let finish: (r: Response) => void = () => {}
    const slow = () => new Promise<Response>((resolve) => (finish = resolve))
    const s = serve([outside(), slow])
    const m = mount(App)
    await settle(10)
    await m.click('I’ve joined')
    expect(m.button('Checking…').disabled).toBe(true)
    m.button('Checking…').click()
    await settle(5)
    expect(s.signIns()).toBe(2)
    finish(joined())
    await settle(20)
    expect(state.gate).toBe('')
    m.unmount()
  })

  it('stops a customer who leaves the channel in the middle of a visit, wherever they are', async () => {
    let member = true
    const s = serve([joined], { '/services': () => (member ? json(200, { services: [] }) : outside('https://t.me/+AbCd')()) })
    const m = mount(App)
    await settle(20)
    expect(m.text()).toContain('No plans for sale right now.')
    // the next screen's first call is refused
    member = false
    await m.click('My services')
    await settle(10)
    expect(state.gate).toBe('join_required')
    expect(state.joinUrl).toBe('https://t.me/+AbCd')
    expect(labels(m)).toEqual(['Join the channel', 'I’ve joined'])
    // they join and press the button: the same screen comes back
    member = true
    await m.click('I’ve joined')
    await settle(20)
    expect(state.gate).toBe('')
    expect(m.text()).toContain('You have no services yet.')
    expect(s.signIns()).toBe(2)
    m.unmount()
  })

  it('says why when the check cannot be made, and keeps the gate', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) => {
      if (!String(url).endsWith('/auth')) return json(200, {})
      if (!state.booted) return outside()()
      throw new TypeError('offline')
    })
    const m = mount(App)
    await settle(10)
    await m.click('I’ve joined')
    await settle(10)
    expect(state.gate).toBe('join_required')
    expect(m.text()).toContain('Could not connect. Check your internet.')
    expect(m.button('I’ve joined').disabled).toBe(false)
    m.unmount()
  })

  it('gives way to another gate that comes up at the check', async () => {
    serve([outside(), () => json(403, { error: 'banned', message: 'no' })])
    const m = mount(App)
    await settle(10)
    await m.click('I’ve joined')
    await settle(10)
    expect(state.gate).toBe('banned')
    expect(m.text()).toContain('This account cannot use the store.')
    expect(labels(m)).toEqual([])
    m.unmount()
  })

  it('is in Persian, right to left, for a Persian customer', async () => {
    telegram({ initDataUnsafe: { user: { language_code: 'fa' } } })
    serve([outside()])
    const m = mount(App)
    await settle(10)
    expect(m.text()).toContain('برای استفاده از فروشگاه، ابتدا در کانال ما عضو شوید و سپس دکمه «عضو شدم» را بزنید.')
    expect(labels(m)).toEqual(['عضویت در کانال', 'عضو شدم'])
    expect(document.documentElement.dir).toBe('rtl')
    await m.click('عضو شدم')
    await settle(10)
    expect(m.text()).toContain('هنوز عضو کانال نشده‌اید.')
    m.unmount()
  })
})

describe('what core says', () => {
  it('is a gate of its own, whatever else a 403 is', () => {
    expect(gateFor(new ApiError(403, 'join_required', ''))).toBe('join_required')
    expect(gateFor(new ApiError(403, 'banned', ''))).toBe('banned')
    expect(gateFor(new ApiError(403, 'forbidden', ''))).toBe('')
  })

  it('carries the channel’s link from the answer, and is empty without one', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(json(403, { error: 'join_required', message: 'x', join_url: 'https://t.me/mynews' }))
    await expect(signIn()).rejects.toMatchObject({ status: 403, code: 'join_required', joinUrl: 'https://t.me/mynews' })
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(json(403, { error: 'banned', message: 'x' }))
    await expect(signIn()).rejects.toMatchObject({ code: 'banned', joinUrl: '' })
  })

  it('keeps the link of the failure that raised the gate', () => {
    expect(fail(new ApiError(403, 'join_required', 'x', '', 'https://t.me/mynews'))).toBe(true)
    expect(state.gate).toBe('join_required')
    expect(state.joinUrl).toBe('https://t.me/mynews')
    expect(fail(new ApiError(403, 'join_required', 'x'))).toBe(true)
    expect(state.joinUrl).toBe('')
  })
})
