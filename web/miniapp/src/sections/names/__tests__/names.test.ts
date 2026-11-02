import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Plan, Service } from '../../../api'
import { messages } from '../../../i18n'
import { state } from '../../../store'
import CheckoutView from '../../../views/CheckoutView.vue'
import ServicesView from '../../../views/ServicesView.vue'
import ServiceCard from '../../reseller/ServiceCard.vue'
import { ME, fakeCore, find, json, mount, need, settle, startAs, submit, svc, tap, text, type, unmountAll } from '../../reseller/__tests__/helpers'

// Config names: the checkout's name step for a new service (only while the store has it on for the buyer), a name
// checked before the order and sent with it, a refused name said in the buyer's language, and Rename on My services and
// on the reseller panel's services, only where core says the service can be renamed.

const plan: Plan = {
  id: '00000000-0000-0000-0000-0000000000a1', names: { en: 'Monthly' }, kind: 'both', days: 30, traffic_bytes: 50 * 2 ** 30,
  price: 100_000, list_price: 100_000, currency: 'IRT', scale: 0, topup: false,
}
const order = (over: Record<string, unknown> = {}) => ({
  id: '00000000-0000-0000-0000-0000000000b1', plan_id: plan.id, type: 'new', status: 'awaiting_payment', amount: 100_000, discount_amount: 0,
  currency: 'IRT', scale: 0, subscription_id: '', ...over,
})
const service = (over: Partial<Service> = {}): Service => ({
  id: '00000000-0000-0000-0000-0000000000c1', label: '', status: 'active', expires_at: null, traffic_total: null, traffic_used: 0,
  link: 'https://sub.example/x', page_url: '', can_renew: false, can_topup: false, config_name: 'u42-abcd1234', renamable: true, ...over,
})

beforeEach(() => {
  startAs('en')
  state.me = { ...ME, features: { config_names: true } }
  state.wallets = [{ currency: 'IRT', balance: 0, scale: 0 }]
})
afterEach(() => {
  unmountAll()
  vi.restoreAllMocks()
  state.services = null
})

describe('the texts of config names', () => {
  it('are in every language, with the same values', () => {
    const keys = Object.keys(messages.en).filter((k) => k.startsWith('names.'))
    expect(keys.length).toBeGreaterThan(20)
    for (const l of ['fa', 'ar', 'ru', 'tr'] as const) for (const k of keys) expect(messages[l][k], `${l}.${k}`).toBeTruthy()
  })
})

describe('the checkout name step', () => {
  it('is not shown while the store has config names off, nor for a renewal or a top-up', async () => {
    state.me = { ...ME, features: {} }
    fakeCore(() => ({}))
    let m = mount(CheckoutView, { plan, type: 'new' })
    await settle()
    expect(text(m.el)).not.toContain('Choose a name')
    m.unmount()

    state.me = { ...ME, features: { config_names: true } }
    for (const t of ['renew', 'traffic_topup']) {
      m = mount(CheckoutView, { plan, type: t, subscriptionId: '00000000-0000-0000-0000-0000000000c1' })
      await settle()
      expect(text(m.el)).not.toContain('Choose a name')
      m.unmount()
    }
  })

  it('orders with the automatic name unless a name is chosen', async () => {
    const calls = fakeCore((c) => (c.path === '/orders' ? order() : c.path.startsWith('/methods') ? { methods: [] } : {}))
    const { el } = mount(CheckoutView, { plan, type: 'new' })
    await settle()
    expect(text(el)).toContain('Automatic name')
    await tap(el, 'Continue')
    const created = calls.find((c) => c.path === '/orders')
    expect(created?.body).not.toHaveProperty('client_name')
    expect(calls.some((c) => c.path === '/config-name/check')).toBe(false)
  })

  it('says a taken name at once and orders nothing; a free one goes with the order', async () => {
    const calls = fakeCore((c) => {
      if (c.path === '/config-name/check') {
        const name = (c.body as { name: string }).name
        return name === 'taken123' ? { ok: false, reason: 'taken', name: '' } : { ok: true, reason: '', name: 'sob-' + name }
      }
      if (c.path === '/orders') return order()
      if (c.path.startsWith('/methods')) return { methods: [] }
      return {}
    })
    const { el } = mount(CheckoutView, { plan, type: 'new' })
    await settle()
    await tap(el, 'Choose a name')
    const input = need<HTMLInputElement>(el, 'input[dir=ltr][maxlength="40"]')
    await type(input, 'taken123')
    await tap(el, 'Continue')
    expect(text(el)).toContain('This name is taken. Try another one.')
    expect(calls.some((c) => c.path === '/orders')).toBe(false)

    await type(input, 'sara1234')
    await tap(el, /^\s*Check\s*$/)
    expect(text(el)).toContain('This name is free')
    expect(text(el)).toContain('sob-sara1234')
    await tap(el, 'Continue')
    const created = calls.find((c) => c.path === '/orders')
    expect(created?.body).toMatchObject({ type: 'new', client_name: 'sara1234' })
    expect(text(el)).toContain('Config name: sob-sara1234')
  })

  it('takes the order’s refusal of a name taken meanwhile, and the buyer can go automatic', async () => {
    let refuse = true
    const calls = fakeCore((c) => {
      if (c.path === '/config-name/check') return { ok: true, reason: '', name: 'sara1234' }
      if (c.path === '/orders') {
        if (refuse && (c.body as { client_name?: string }).client_name) return json(400, { error: 'config_name', reason: 'taken', message: 'config name: taken' })
        return order()
      }
      if (c.path.startsWith('/methods')) return { methods: [] }
      return {}
    })
    const { el } = mount(CheckoutView, { plan, type: 'new' })
    await settle()
    await tap(el, 'Choose a name')
    await type(need<HTMLInputElement>(el, 'input[dir=ltr][maxlength="40"]'), 'sara1234')
    await tap(el, 'Continue')
    expect(text(el)).toContain('This name is taken. Try another one.')
    expect(find(el, 'button', 'Pay from the wallet')).toBeNull()

    refuse = false
    await tap(el, 'Automatic name')
    await tap(el, 'Continue')
    const last = calls.filter((c) => c.path === '/orders').at(-1)
    expect(last?.body).not.toHaveProperty('client_name')
    expect(find(el, 'button', 'Pay from the wallet')).not.toBeNull()
  })

  it('explains the rules in Persian', async () => {
    startAs('fa')
    fakeCore((c) => (c.path === '/config-name/check' ? { ok: false, reason: 'mix', name: '' } : {}))
    const { el } = mount(CheckoutView, { plan, type: 'new' })
    await settle()
    await tap(el, 'انتخاب نام')
    await type(need<HTMLInputElement>(el, 'input[dir=ltr][maxlength="40"]'), 'sobhan')
    await tap(el, /^\s*بررسی\s*$/)
    expect(text(el)).toContain('نام باید دست‌کم یک حرف و یک عدد داشته باشد.')
  })
})

describe('Rename on My services', () => {
  it('shows only where core says the service can be renamed, and renames it keeping the link', async () => {
    const calls = fakeCore((c) => {
      if (c.path === '/services') return { services: [service(), service({ id: '00000000-0000-0000-0000-0000000000c2', config_name: 'u42-zzzz9999', renamable: false })] }
      if (c.path.endsWith('/rename')) {
        const name = (c.body as { name: string }).name
        if (name === 'ab') return { ok: false, reason: 'length', name: '' }
        return { ok: true, reason: '', name, service: service({ config_name: name }) }
      }
      return {}
    })
    const { el } = mount(ServicesView)
    await settle()
    expect(text(el)).toContain('Config name: u42-abcd1234')
    expect(text(el)).toContain('Config name: u42-zzzz9999')
    expect(el.querySelectorAll('button').length).toBeGreaterThan(0)
    const renames = [...el.querySelectorAll('button')].filter((b) => (b.textContent ?? '').includes('Change config name'))
    expect(renames).toHaveLength(1) // the second service's server cannot keep the link

    await tap(el, 'Change config name')
    expect(text(el)).toContain('The subscription link stays the same.')
    const form = need<HTMLFormElement>(el, 'form')
    await type(need<HTMLInputElement>(form, 'input'), 'ab')
    await submit(form)
    expect(text(el)).toContain('The name must be 4 to 20 characters.')

    await type(need<HTMLInputElement>(form, 'input'), 'mine5678')
    await submit(form)
    const call = calls.find((c) => c.path === '/services/00000000-0000-0000-0000-0000000000c1/rename' && (c.body as { name: string }).name === 'mine5678')
    expect(call?.method).toBe('POST')
    expect(text(el)).toContain('Config name: mine5678')
    expect(text(el)).toContain('The config is now called mine5678. The subscription link is the same.')
    expect(find(el, 'form')).toBeNull()
  })
})

describe('Rename on the reseller panel', () => {
  it('renames a customer’s config where it can be, and the list loads again', async () => {
    state.me = { ...ME, features: { reseller: true, config_names: true } }
    const calls = fakeCore((c) => (c.path.endsWith('/rename') ? { ok: true, reason: '', name: 'sob-ali1234', service: svc({ config_name: 'sob-ali1234' }) } : {}))
    const changed: string[] = []
    const { el } = mount(ServiceCard, { service: svc({ config_name: 'sob-u42-abcd1234', renamable: true }), open: true, onChanged: (d: string) => changed.push(d) })
    await settle()
    expect(text(el)).toContain('Config name: sob-u42-abcd1234')
    await tap(el, 'Change config name')
    const form = need<HTMLFormElement>(el, 'form')
    await type(need<HTMLInputElement>(form, 'input'), 'ali1234')
    await submit(form)
    expect(calls.find((c) => c.path.endsWith('/rename'))?.body).toEqual({ name: 'ali1234' })
    expect(changed).toEqual(['reseller.done.saved'])

    const other = mount(ServiceCard, { service: svc({ config_name: 'sob-u1-x', renamable: false }), open: true })
    await settle()
    expect(find(other.el, 'button', 'Change config name')).toBeNull()
  })
})
