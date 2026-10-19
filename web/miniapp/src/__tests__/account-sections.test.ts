import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, type Component } from 'vue'
import { resetSession, type Me } from '../api'
import { setLang } from '../i18n'
import { state } from '../store'
import InviteView from '../sections/invite/InviteView.vue'
import SupportView from '../sections/support/SupportView.vue'
import TrialView from '../sections/trial/TrialView.vue'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const ME: Me = {
  user: { id: 'u', username: '', language: 'en' },
  wallets: [],
  brand: { name: 'Acme', color: '', support: '', support_url: '', bot_url: '' },
  maintenance: false,
  top_up: { currency: 'IRT', min: 1, max: 2 },
  features: { trial: true, invite: true, support: true },
}

type Handler = (body: Record<string, unknown> | undefined) => Response
/** mockApi answers the Mini App's calls by "METHOD /path" and records them. */
function mockApi(routes: Record<string, Handler>) {
  const calls: { key: string; body?: Record<string, unknown> }[] = []
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
    const key = `${init?.method ?? 'GET'} ${String(url).replace('/api/mini/v1', '')}`
    const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined
    calls.push({ key, body })
    if (key === 'POST /auth') return json(200, { ...ME, token: 'tok' })
    return routes[key]?.(body) ?? json(404, { error: 'not_found', message: key })
  })
  return calls
}

const mounted: (() => void)[] = []
function mount(component: Component, props: Record<string, unknown> = {}) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp(component, props)
  app.mount(el)
  mounted.push(() => {
    app.unmount()
    el.remove()
  })
  return el
}
const text = (el: Element) => el.textContent?.replace(/\s+/g, ' ').trim() ?? ''
const button = (el: Element, label: string) => [...el.querySelectorAll('button')].find((b) => text(b).includes(label))
const seen = (el: Element, s: string) => vi.waitFor(() => expect(text(el)).toContain(s))
const type = (input: HTMLTextAreaElement | HTMLSelectElement, value: string) => {
  input.value = value
  input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? 'change' : 'input'))
}

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

describe('trial section', () => {
  const offer = { available: true, used: false, plan: { names: { en: 'Free trial' }, kind: 'both', days: 1, traffic_bytes: 2 ** 30, currency: 'IRT', scale: 0 } }

  it('shows what the trial gives, starts it and follows the order until the service is ready', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    let polled = 0
    const calls = mockApi({
      'GET /trial': () => json(200, offer),
      'POST /trial': () => json(200, { id: 'o1', status: 'paid', amount: 0 }),
      'GET /orders/o1': () => json(200, { id: 'o1', status: ++polled > 1 ? 'active' : 'provisioning' }),
      'GET /services': () => json(200, { services: [] }),
    })
    const onServices = vi.fn()
    const el = mount(TrialView, { onServices })

    await seen(el, 'You get: 1 days · 1 GB')
    expect(text(el)).toContain('Free trial')
    button(el, 'Start the free trial')!.click()

    await seen(el, 'Setting up your trial service')
    expect(calls.map((c) => c.key)).toContain('POST /trial')
    expect(state.me?.features.trial).toBe(false) // gone from the More tab at once
    expect(button(el, 'See my services')).toBeUndefined()

    vi.advanceTimersByTime(3000)
    await seen(el, 'Setting up your trial service') // still being delivered
    vi.advanceTimersByTime(3000)
    await seen(el, 'Your trial service is ready.')
    button(el, 'See my services')!.click()
    expect(onServices).toHaveBeenCalledOnce()
  })

  it('says so when the trial was used already, with no way to start another', async () => {
    mockApi({ 'GET /trial': () => json(200, { available: false, used: true }) })
    const el = mount(TrialView)
    await seen(el, 'You have used your free trial.')
    expect(button(el, 'Start the free trial')).toBeUndefined()
  })

  it('does not start a trial that is not offered', async () => {
    mockApi({ 'GET /trial': () => json(200, { available: false, used: false }) })
    const el = mount(TrialView)
    await seen(el, 'The free trial is not available now.')
    expect(button(el, 'Start the free trial')).toBeUndefined()
  })

  it('takes a trial used meanwhile (the bot, another device) as used, not as an error', async () => {
    mockApi({
      'GET /trial': () => json(200, offer),
      'POST /trial': () => json(409, { error: 'trial_used', message: 'used' }),
    })
    const el = mount(TrialView)
    await vi.waitFor(() => expect(button(el, 'Start the free trial')).toBeDefined())
    button(el, 'Start the free trial')!.click()
    await seen(el, 'You have used your free trial.')
    expect(el.querySelector('[role="alert"]')).toBeNull()
    expect(state.me?.features.trial).toBe(false)
  })
})

describe('invite section', () => {
  const invite = { link: 'https://t.me/acme_bot?start=r_abcd2345', percent: 10, invited: 3, rewarded: 1, earned: [{ currency: 'IRT', amount: 25000, scale: 0 }] }

  it('shows the link and what it earned, copies it and shares it', async () => {
    mockApi({ 'GET /invite': () => json(200, invite) })
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const el = mount(InviteView)

    await seen(el, invite.link)
    expect(text(el)).toContain('you get 10% of it in your wallet')
    expect(text(el)).toMatch(/Invited\s*3/)
    expect(text(el)).toMatch(/Bought\s*1/)
    expect(text(el)).toMatch(/You earned.*25,000 Toman/)

    button(el, 'Copy the link')!.click()
    await seen(el, 'Copied')
    expect(writeText).toHaveBeenCalledWith(invite.link)

    button(el, 'Send to friends')!.click()
    const shared = String(open.mock.calls[0][0])
    expect(shared.startsWith('https://t.me/share/url?url=' + encodeURIComponent(invite.link))).toBe(true)
    expect(decodeURIComponent(shared)).toContain('Join Acme with my link')
  })

  it('says when nothing was earned yet', async () => {
    mockApi({ 'GET /invite': () => json(200, { ...invite, invited: 0, rewarded: 0, earned: [] }) })
    const el = mount(InviteView)
    await seen(el, 'Nothing earned yet.')
  })

  it('does not show a link when invitations are off', async () => {
    mockApi({ 'GET /invite': () => json(404, { error: 'not_found', message: 'off' }) })
    const el = mount(InviteView)
    await vi.waitFor(() => expect(el.querySelector('[role="alert"]')).not.toBeNull())
    expect(text(el)).not.toContain('https://t.me/')
  })
})

describe('support section', () => {
  const ticket = (status: string, over: Record<string, unknown> = {}) => ({
    id: 't1', number: 7, category: 'payment', subject: 'My payment is missing', status, created_at: 1, updated_at: 2, ...over,
  })
  const empty = { open: null, tickets: [], categories: ['payment', 'service', 'other'] }

  it('starts a conversation in the chosen topic and shows it', async () => {
    let thread = [] as unknown[]
    const calls = mockApi({
      'GET /support': () => json(200, empty),
      'POST /support/messages': () => {
        thread = [{ id: 'm1', kind: 'customer', body: 'My payment is missing', has_file: false, at: 100 }]
        return json(200, { ticket: ticket('open'), message_id: 'm1', new_ticket: true })
      },
      'GET /support/tickets/t1': () => json(200, { ticket: ticket('open'), messages: thread }),
    })
    const el = mount(SupportView)
    await seen(el, 'You have not written to support yet.')

    const send = button(el, 'Send')!
    expect(send.disabled).toBe(true) // nothing written
    type(el.querySelector('select')!, 'payment')
    type(el.querySelector('textarea')!, '  My payment is missing  ')
    await vi.waitFor(() => expect(send.disabled).toBe(false))
    send.click()

    await seen(el, '#7')
    expect(calls.find((c) => c.key === 'POST /support/messages')!.body).toEqual({ text: 'My payment is missing', category: 'payment' })
    expect(text(el)).toContain('My payment is missing')
    expect(text(el)).toContain('Waiting for an answer')
    expect(text(el)).toContain('Your message arrived')
  })

  it('shows staff answers apart from the customer\'s own words, and allows a reply', async () => {
    const calls = mockApi({
      'GET /support': () => json(200, { open: ticket('answered'), tickets: [ticket('answered')], categories: ['payment', 'service', 'other'] }),
      'GET /support/tickets/t1': () =>
        json(200, {
          ticket: ticket('answered'),
          messages: [
            { id: 'm1', kind: 'customer', body: 'Help', has_file: false, at: 100 },
            { id: 'm2', kind: 'staff', body: 'Checking it now', has_file: false, at: 200 },
            { id: 'm3', kind: 'customer', body: '', has_file: true, at: 300 },
          ],
        }),
      'POST /support/messages': () => json(200, { ticket: ticket('open'), message_id: 'm4', new_ticket: false }),
    })
    const el = mount(SupportView)
    await seen(el, 'Answered')
    button(el, 'Open the conversation')!.click()
    await seen(el, 'Checking it now')
    const bubbles = [...el.querySelectorAll('.bubble')]
    expect(bubbles.map((b) => (b.classList.contains('staff') ? 'staff' : 'customer'))).toEqual(['customer', 'staff', 'customer'])
    expect(text(bubbles[2])).toContain('Attachment')

    type(el.querySelector('textarea')!, 'Thanks')
    await vi.waitFor(() => expect(button(el, 'Send')!.disabled).toBe(false))
    button(el, 'Send')!.click()
    await vi.waitFor(() => expect(calls.some((c) => c.key === 'POST /support/messages')).toBe(true))
    // the conversation is reloaded, and a reply needs no topic
    expect(calls.find((c) => c.key === 'POST /support/messages')!.body).toMatchObject({ text: 'Thanks' })
    expect(el.querySelector('select')).toBeNull()
  })

  it('words the daily limit', async () => {
    mockApi({
      'GET /support': () => json(200, empty),
      'POST /support/messages': () => json(429, { error: 'daily_limit', message: 'x' }),
    })
    const el = mount(SupportView)
    await seen(el, 'You have not written')
    type(el.querySelector('textarea')!, 'Hello')
    await vi.waitFor(() => expect(button(el, 'Send')!.disabled).toBe(false))
    button(el, 'Send')!.click()
    await seen(el, 'You reached the limit of messages for today')
  })

  it('closes a conversation and then offers a new message', async () => {
    let status = 'answered'
    const calls = mockApi({
      'GET /support': () => json(200, { open: status === 'closed' ? null : ticket(status), tickets: [ticket(status)], categories: ['other'] }),
      'GET /support/tickets/t1': () => json(200, { ticket: ticket(status), messages: [{ id: 'm1', kind: 'customer', body: 'Hi', has_file: false, at: 1 }] }),
      'POST /support/tickets/t1/close': () => {
        status = 'closed'
        return json(200, { ticket: ticket('closed') })
      },
    })
    const el = mount(SupportView)
    await seen(el, 'Open the conversation')
    button(el, 'Open the conversation')!.click()
    await seen(el, 'Close the conversation')
    button(el, 'Close the conversation')!.click()
    await seen(el, 'This conversation is closed.')
    expect(calls.some((c) => c.key === 'POST /support/tickets/t1/close')).toBe(true)
    expect(el.querySelector('textarea')).toBeNull() // a closed conversation takes no reply
    button(el, 'New message')!.click()
    await vi.waitFor(() => expect(el.querySelector('textarea')).not.toBeNull())
  })

  it('lists earlier conversations apart from the open one', async () => {
    mockApi({
      'GET /support': () =>
        json(200, {
          open: null,
          tickets: [ticket('closed', { id: 't9', number: 9, subject: 'Old question' })],
          categories: ['payment', 'service', 'other'],
        }),
    })
    const el = mount(SupportView)
    await seen(el, 'Earlier conversations')
    expect(text(el)).toContain('Old question')
    expect(text(el)).toContain('Closed')
    expect(el.querySelector('textarea')).not.toBeNull() // no open one: a new message can be written
  })
})
