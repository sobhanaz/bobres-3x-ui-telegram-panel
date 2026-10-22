import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Me, Plan, Service } from '../../../api'
import main from '../../../main.ts?raw'
import loader from '../../index.ts?raw'
import { allSections, sectionsFor } from '../../../sections'
import { state } from '../../../store'
import '../index'
import ResellerSection from '../ResellerSection.vue'
import { resetUi, ui } from '../state'
import { counts, fakeCore, find, inDays, json, mount, need, settle, startAs, submit, svc, tap, text, type, unmountAll } from './helpers'

const panel = {
  tier: 'Gold',
  discount_percent: 20,
  commission_percent: 10,
  balances: [{ currency: 'IRT', balance: -120_000, credit_limit: 500_000, available: 380_000, scale: 0 }],
  customers: 12,
  commission_earned: [{ currency: 'IRT', amount: 45_000, scale: 0 }],
  ref_code: 'abcd2345',
  invite_link: 'https://t.me/acme_bot?start=r_abcd2345',
  counts: counts(),
  config_prefix: '',
  panel_group: '',
}

const page = (services: unknown[], over: Record<string, unknown> = {}) => ({ services, total: services.length, page: 1, size: 10, counts: counts(), ...over })

const plan = (over: Partial<Plan> = {}): Plan => ({
  id: '00000000-0000-0000-0000-0000000000a1', names: { en: 'Monthly', fa: 'ماهانه' }, kind: 'both', days: 30, traffic_bytes: 50 * 2 ** 30,
  price: 80_000, list_price: 100_000, currency: 'IRT', scale: 0, topup: false, ...over,
})

beforeEach(() => {
  startAs('en')
  resetUi()
  state.plans = null
  state.gate = ''
})
afterEach(() => {
  unmountAll()
  vi.restoreAllMocks()
  delete window.Telegram
})

describe('the section in the More tab', () => {
  const me = (features: Me['features']): Me => ({
    user: { id: 'u', username: '', language: 'en' }, wallets: [], brand: { name: '', color: '', support: '', support_url: '', bot_url: '' },
    maintenance: false, top_up: { currency: 'IRT', min: 1, max: 2 }, features,
  })

  it('is offered to a reseller core vouches for, and to nobody else', () => {
    expect(allSections().map((s) => s.id)).toContain('reseller')
    expect(sectionsFor(me({ reseller: true })).map((s) => s.id)).toContain('reseller')
    expect(sectionsFor(me({ reseller: false })).map((s) => s.id)).not.toContain('reseller')
    expect(sectionsFor(me({})).map((s) => s.id)).not.toContain('reseller')
    expect(sectionsFor(me({ reseller: 'true' })).map((s) => s.id)).not.toContain('reseller') // only a real true counts
  })

  it('is loaded by the app: main.ts starts the loader of the section folders, not the registry beside it', () => {
    // './sections' alone resolves to sections.ts (the registry); the folders are registered by './sections/index'
    expect(main).toMatch(/^import '\.\/sections\/index'$/m)
    expect(loader).toContain("import.meta.glob('./*/index.ts', { eager: true })")
  })

  it('has a title and a hint in both languages', () => {
    const s = allSections().find((x) => x.id === 'reseller')
    expect(s?.title).toBe('reseller.title')
    expect(s?.hint).toBe('reseller.hint')
  })
})

// the app wraps a name in direction isolates; a reader does not see them
const plain = (el: ParentNode) => text(el).replace(/[\u2066-\u2069]/g, '')

describe('the panel', () => {
  it('shows the tier, the balance as a debt with its credit and what can be spent, the counts and the link', async () => {
    fakeCore(() => panel)
    const { el, unmount } = mount(ResellerSection)
    await settle()
    const t = text(el)
    for (const want of [
      'Reseller panel', 'Tier: Gold', '20% off store prices', 'Commission: 10%',
      'Debt 120,000 Toman', 'Credit limit 500,000 Toman', 'Available to spend 380,000 Toman',
      'All · 5', 'Active · 2', 'Ending soon · 1', 'Ended · 1', 'Paused · 1',
      'Commission earned 45,000 Toman', 'https://t.me/acme_bot?start=r_abcd2345',
    ]) {
      expect(t, want).toContain(want)
    }
    expect(t).not.toContain('-120,000') // never a bare minus on a debt
    expect(find(el, '[role=tab][aria-selected=true]')?.textContent?.trim()).toBe('Panel')
    unmount()
  })

  it('shares and copies the invite link', async () => {
    fakeCore(() => panel)
    const openTelegramLink = vi.fn()
    const writeText = vi.fn().mockResolvedValue(undefined)
    window.Telegram = { WebApp: { openTelegramLink } }
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { el, unmount } = mount(ResellerSection)
    await settle()
    await tap(el, 'Share the link')
    expect(openTelegramLink).toHaveBeenCalledWith(
      'https://t.me/share/url?url=' + encodeURIComponent(panel.invite_link) + '&text=' + encodeURIComponent('Join with this link'),
    )
    await tap(el, 'Copy')
    expect(writeText).toHaveBeenCalledWith(panel.invite_link)
    unmount()
  })

  it('lets the reseller choose their config prefix once, after asking, and shows it as locked afterwards', async () => {
    let prefix = ''
    const calls = fakeCore((c) => {
      if (c.method === 'POST' && c.path === '/reseller/prefix') {
        prefix = (c.body as { prefix: string }).prefix
      }
      return { ...panel, config_prefix: prefix, panel_group: 'SOBHANADMIN' }
    })
    const { el, unmount } = mount(ResellerSection)
    await settle()
    expect(text(el)).toContain('Config prefix')
    const set = need<HTMLButtonElement>(el, 'button', 'Set the prefix')
    expect(set.disabled).toBe(true) // nothing typed yet
    const input = need<HTMLInputElement>(el, '[data-prefix] input')
    await type(input, 'x')
    expect(need<HTMLButtonElement>(el, 'button', 'Set the prefix').disabled).toBe(true) // too short
    await type(input, 'has-dash')
    expect(need<HTMLButtonElement>(el, 'button', 'Set the prefix').disabled).toBe(true)
    await type(input, ' Sob ')
    await tap(el, 'Set the prefix')
    // asked first, and nothing is sent until it is confirmed
    expect(plain(el)).toContain('Use sob for all your configs? You cannot change it yourself afterwards.')
    expect(calls.some((c) => c.path === '/reseller/prefix')).toBe(false)
    await tap(el, 'Yes, use it')
    expect(calls.filter((c) => c.path === '/reseller/prefix')).toEqual([{ method: 'POST', path: '/reseller/prefix', body: { prefix: 'sob' } }])
    expect(plain(el)).toContain('sob-…')
    expect(text(el)).toContain('Only the owner can change it.')
    expect(find(el, '[data-prefix] input')).toBeNull() // locked
    unmount()
  })

  it('tells a prefix core refuses, and reads the panel again when it was set already', async () => {
    let state: 'bad' | 'set' = 'bad'
    fakeCore((c) => {
      if (c.method === 'POST' && c.path === '/reseller/prefix') {
        return state === 'bad'
          ? new Response(JSON.stringify({ error: 'invalid', message: 'another reseller has this prefix' }), { status: 400, headers: { 'Content-Type': 'application/json' } })
          : new Response(JSON.stringify({ error: 'state', message: 'the prefix was set already' }), { status: 409, headers: { 'Content-Type': 'application/json' } })
      }
      return { ...panel, config_prefix: state === 'set' ? 'abc' : '' }
    })
    const { el, unmount } = mount(ResellerSection)
    await settle()
    await type(need<HTMLInputElement>(el, '[data-prefix] input'), 'taken')
    await tap(el, 'Set the prefix')
    await tap(el, 'Yes, use it')
    expect(text(el)).toContain('That prefix is not possible')
    expect(find(el, '[data-prefix] input')).not.toBeNull() // asked again
    state = 'set'
    await tap(el, 'Set the prefix')
    await tap(el, 'Yes, use it')
    expect(plain(el)).toContain('abc-…')
    expect(find(el, '[data-prefix] input')).toBeNull()
    unmount()
  })

  it('says so when there is no link yet, and offers no share button', async () => {
    fakeCore(() => ({ ...panel, invite_link: '' }))
    const { el, unmount } = mount(ResellerSection)
    await settle()
    expect(text(el)).toContain('The invite link is not ready yet')
    expect(find(el, 'button', 'Share the link')).toBeNull()
    unmount()
  })

  it('goes to the services in a group, to the customers, and to the purchase', async () => {
    const calls = fakeCore((c) => (c.path.startsWith('/reseller/services') ? page([svc()]) : c.path.startsWith('/reseller/customers') ? { customers: [], total: 0, page: 1, size: 10 } : panel))
    const { el, unmount } = mount(ResellerSection)
    await settle()
    await tap(el, 'Ended · 1')
    expect(ui.tab).toBe('services')
    expect(ui.filter).toBe('expired')
    expect(calls.at(-1)).toMatchObject({ method: 'GET', path: '/reseller/services?status=expired&page=1' })
    ui.tab = 'panel'
    await settle()
    await tap(el, '12')
    expect(ui.tab).toBe('customers')
    ui.tab = 'panel'
    await settle()
    await tap(el, 'Buy for a customer')
    expect(ui.tab).toBe('buy')
    unmount()
  })

  it('tells a failure and tries again', async () => {
    let n = 0
    fakeCore(() => (++n === 1 ? json(500, { error: 'boom', message: 'x' }) : panel))
    const { el, unmount } = mount(ResellerSection)
    await settle()
    expect(text(el)).toContain('Something went wrong')
    await tap(el, 'Try again')
    expect(text(el)).toContain('Tier: Gold')
    unmount()
  })

  it('is replaced by the gate when core says the session is over', async () => {
    fakeCore(() => json(401, { error: 'unauthorized', message: 'x' }))
    const { unmount } = mount(ResellerSection)
    await settle()
    expect(state.gate).toBe('expired')
    unmount()
  })
})

describe('the customers services', () => {
  const open = async (handler: Parameters<typeof fakeCore>[0]) => {
    ui.tab = 'services'
    const calls = fakeCore(handler)
    const m = mount(ResellerSection)
    await settle()
    return { ...m, calls }
  }

  it('lists them with the groups, their counts, usage and note', async () => {
    const { el, unmount } = await open(() =>
      page([svc({ note: 'pays on the 5th' }), svc({ id: 'b', label: '', status: 'disabled', can_pause: false, can_resume: true, traffic_total: null, traffic_used: 3 * 2 ** 30 })]),
    )
    const t = text(el)
    for (const want of ['All · 5', 'Active · 2', 'Ending soon · 1', 'Ended · 1', 'Paused · 1', 'Ali', 'Active', '10 GB of 50 GB', '📝 pays on the 5th', 'Paused', '3 GB used']) {
      expect(t, want).toContain(want)
    }
    expect(find(el, '.rs-chip[aria-pressed=true]')?.textContent).toContain('All')
    expect(find(el, '[role=progressbar]')?.getAttribute('aria-valuenow')).toBe('20')
    // a service with no name is told by its number, and a paused one is "Paused", not "Disabled"
    expect(t).toContain('Service #')
    expect(t).not.toContain('Disabled')
    unmount()
  })

  it('shows a group, a search and a page by asking core, and keeps them for the next visit', async () => {
    const { el, calls, unmount } = await open(() => page([svc()], { total: 25, counts: counts({ total: 25 }) }))
    await tap(el, 'Ended · 1')
    expect(calls.at(-1)?.path).toBe('/reseller/services?status=expired&page=1')
    await type(need<HTMLInputElement>(el, 'input[type=search]'), '  ali ')
    await submit(need<HTMLFormElement>(el, 'form[role=search]'))
    expect(calls.at(-1)?.path).toBe('/reseller/services?status=expired&q=ali&page=1')
    expect(text(el)).toContain('“ali”')
    await tap(el, 'Next')
    expect(calls.at(-1)?.path).toBe('/reseller/services?status=expired&q=ali&page=2')
    expect(text(el)).toContain('Page 1 of 3') // the answer says page 1 (the fake does not follow the page)
    expect([ui.filter, ui.query, ui.page]).toEqual(['expired', 'ali', 2])
    // another group starts again from the first page, and the search stays
    await tap(el, 'Paused · 1')
    expect(calls.at(-1)?.path).toBe('/reseller/services?status=paused&q=ali&page=1')
    expect(ui.page).toBe(1)
    await tap(el, 'Clear the search')
    expect(calls.at(-1)?.path).toBe('/reseller/services?status=paused&page=1')
    unmount()
    // a visit later the list is as it was left
    const again = mount(ResellerSection)
    await settle()
    expect(calls.at(-1)?.path).toBe('/reseller/services?status=paused&page=1')
    again.unmount()
  })

  it('says when there is nothing, and when the search finds nothing', async () => {
    const empty = await open(() => page([], { counts: counts({ total: 0 }) }))
    expect(text(empty.el)).toContain('You have not bought any services for customers yet')
    empty.unmount()
    resetUi()
    const none = await open(() => page([], { counts: counts() }))
    expect(text(none.el)).toContain('No service found')
    none.unmount()
  })

  it('opens a service for its actions, and hands back the link', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { el, unmount } = await open(() => page([svc()]))
    expect(find(el, 'button', 'Subscription link')).toBeNull() // closed until opened
    await tap(el, 'Ali')
    for (const want of ['Subscription link', 'Service page', /^Renew$/, 'Add traffic', /^Pause$/, /^Rename$/, /^Note$/, /^Delete$/]) {
      expect(find(el, 'button', want), String(want)).not.toBeNull()
    }
    expect(find(el, 'button', /^Resume$/)).toBeNull()
    await tap(el, 'Subscription link')
    expect(writeText).toHaveBeenCalledWith('https://sub.example/x')
    await tap(el, 'Ali') // opening it again closes it
    expect(find(el, 'button', 'Subscription link')).toBeNull()
    unmount()
  })

  it('offers only what the service allows', async () => {
    const { el, unmount } = await open(() =>
      page([svc({ status: 'pending', link: '', page_url: '', can_renew: false, can_topup: false, can_pause: false, can_delete: false })]),
    )
    await tap(el, 'Ali')
    expect(text(el)).toContain('Being prepared')
    for (const none of ['Subscription link', 'Service page', /^Renew$/, 'Add traffic', /^Pause$/, /^Resume$/, /^Delete$/]) expect(find(el, 'button', none), String(none)).toBeNull()
    expect(find(el, 'button', /^Rename$/)).not.toBeNull()
    expect(find(el, 'button', /^Note$/)).not.toBeNull()
    unmount()
  })

  it('asks the app to renew or add traffic, with the service', async () => {
    const renew = vi.fn()
    const topup = vi.fn()
    ui.tab = 'services'
    fakeCore(() => page([svc()]))
    const { el, unmount } = mount(ResellerSection, { onRenew: renew, onTopup: topup })
    await settle()
    await tap(el, 'Ali')
    await tap(el, /^Renew$/)
    await tap(el, 'Add traffic')
    expect(renew).toHaveBeenCalledTimes(1)
    expect((renew.mock.calls[0][0] as Service).id).toBe(svc().id)
    expect((topup.mock.calls[0][0] as Service).id).toBe(svc().id)
    unmount()
  })

  it('pauses and resumes, then reads the list again', async () => {
    let paused = false
    const { el, calls, unmount } = await open((c) => {
      if (c.path.endsWith('/pause')) paused = true
      if (c.path.endsWith('/resume')) paused = false
      if (c.method === 'POST') return { service: svc() }
      return page([paused ? svc({ status: 'disabled', can_pause: false, can_resume: true }) : svc()])
    })
    await tap(el, 'Ali')
    await tap(el, /^Pause$/)
    expect(calls.map((c) => c.method + ' ' + c.path).slice(-2)).toEqual([`POST /reseller/services/${svc().id}/pause`, 'GET /reseller/services?page=1'])
    expect(text(el)).toContain('Service paused. Its link stops working until you resume it.')
    expect(find(el, 'button', /^Pause$/)).toBeNull()
    await tap(el, /^Resume$/)
    expect(calls.map((c) => c.path).slice(-2)).toEqual([`/reseller/services/${svc().id}/resume`, '/reseller/services?page=1'])
    expect(text(el)).toContain('Service resumed.')
    unmount()
  })

  it('says why a service support paused is not resumed', async () => {
    const { el, unmount } = await open((c) =>
      c.method === 'POST' ? json(409, { error: 'paused_by_staff', message: 'support paused this service' }) : page([svc({ status: 'disabled', can_pause: false, can_resume: true })]),
    )
    await tap(el, 'Ali')
    await tap(el, /^Resume$/)
    expect(text(el)).toContain('Support paused this service. Contact support to turn it back on.')
    unmount()
  })

  it('does not delete without the confirmation, and deletes with it', async () => {
    const { el, calls, unmount } = await open((c) => (c.method === 'POST' ? { ok: true } : page([svc()])))
    await tap(el, 'Ali')
    await tap(el, /^Delete$/)
    expect(text(el)).toContain('Delete this service?')
    expect(text(el)).toContain('nothing is refunded')
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(0) // asking is not deleting
    await tap(el, 'No, keep it')
    expect(text(el)).not.toContain('Delete this service?')
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(0)
    await tap(el, /^Delete$/)
    await tap(el, 'Yes, delete it')
    const posts = calls.filter((c) => c.method === 'POST')
    expect(posts).toHaveLength(1)
    expect(posts[0]).toMatchObject({ path: `/reseller/services/${svc().id}/delete`, body: { confirm: true } })
    expect(text(el)).toContain('Service deleted.')
    expect(calls.at(-1)?.method).toBe('GET') // the list was read again
    unmount()
  })

  it('renames, notes and clears, one line each, and refuses a text that is too long before asking core', async () => {
    const { el, calls, unmount } = await open((c) => (c.method === 'POST' ? { service: svc() } : page([svc({ note: 'old' })])))
    await tap(el, 'Ali')
    await tap(el, /^Rename$/)
    const name = need<HTMLInputElement>(el, 'input[maxlength="64"]')
    expect(name.value).toBe('Ali') // the current name to change
    await type(name, '  Reza’s \n phone ')
    await submit(need<HTMLFormElement>(el, 'form.rs-grid'))
    expect(calls.filter((c) => c.method === 'POST').at(-1)).toMatchObject({ path: `/reseller/services/${svc().id}/label`, body: { label: 'Reza’s phone' } })
    expect(text(el)).toContain('Saved.')

    await tap(el, /^Note$/)
    const note = need<HTMLInputElement>(el, 'input[maxlength="200"]')
    expect(note.value).toBe('old')
    await type(note, '')
    await submit(need<HTMLFormElement>(el, 'form.rs-grid'))
    expect(calls.filter((c) => c.method === 'POST').at(-1)).toMatchObject({ path: `/reseller/services/${svc().id}/note`, body: { note: '' } }) // empty clears it

    await tap(el, /^Note$/)
    const before = calls.length
    const long = need<HTMLInputElement>(el, 'input[maxlength="200"]')
    long.removeAttribute('maxlength') // the field stops typing at 200; a paste can still get past a script
    await type(long, 'x'.repeat(201))
    await submit(need<HTMLFormElement>(el, 'form.rs-grid'))
    expect(calls.length).toBe(before)
    expect(text(el)).toContain('Write it as one short line.')
    unmount()
  })

  it('tells a service that is gone, and reads the list again', async () => {
    let gone = false
    const { el, calls, unmount } = await open((c) => {
      if (c.method === 'POST') {
        gone = true
        return json(404, { error: 'not_found', message: 'no such item' })
      }
      return page(gone ? [] : [svc()], gone ? { counts: counts({ total: 0 }) } : {})
    })
    await tap(el, 'Ali')
    await tap(el, /^Pause$/)
    expect(calls.at(-1)?.method).toBe('GET')
    expect(text(el)).toContain('You have not bought any services')
    unmount()
  })

  it('tells a refusal for the license without a word about it', async () => {
    const { el, unmount } = await open((c) => (c.method === 'POST' ? json(403, { error: 'forbidden', message: 'this is not yours' }) : page([svc()])))
    await tap(el, 'Ali')
    await tap(el, /^Pause$/)
    expect(text(el)).toContain('This section is not available right now.')
    expect(text(el).toLowerCase()).not.toContain('licen')
    unmount()
  })
})

describe('buying for a customer', () => {
  it('shows the plans at the reseller price, takes a name and asks the app for the purchase', async () => {
    state.plans = [plan(), plan({ id: '00000000-0000-0000-0000-0000000000a2', names: { en: 'Extra 10 GB' }, topup: true, price: 30_000, list_price: 30_000 })]
    const buy = vi.fn()
    ui.tab = 'buy'
    const calls = fakeCore(() => ({}))
    const { el, unmount } = mount(ResellerSection, { onBuy: buy })
    await settle()
    expect(calls).toHaveLength(0) // the plans were loaded already
    const t = text(el)
    expect(t).toContain('Monthly')
    expect(t).toContain('80,000 Toman')
    expect(t).toContain('List price 100,000 Toman')
    expect(t).not.toContain('Extra 10 GB') // a traffic package is bought for a service, not as a service
    expect(find(el, 'input')).toBeNull() // no name field until a plan is picked
    await tap(el, '80,000 Toman')
    const name = need<HTMLInputElement>(el, 'input[maxlength="64"]')
    await type(name, '  Ali’s   phone  ')
    await submit(need<HTMLFormElement>(el, 'form'))
    expect(buy).toHaveBeenCalledTimes(1)
    expect(buy.mock.calls[0][0]).toMatchObject({ type: 'new', label: 'Ali’s phone', plan: { id: plan().id, price: 80_000 } })
    unmount()
  })

  it('goes on without a name, and refuses one that is too long', async () => {
    state.plans = [plan()]
    const buy = vi.fn()
    ui.tab = 'buy'
    fakeCore(() => ({}))
    const { el, unmount } = mount(ResellerSection, { onBuy: buy })
    await settle()
    await tap(el, '80,000 Toman')
    const name = need<HTMLInputElement>(el, 'input[maxlength="64"]')
    name.removeAttribute('maxlength')
    await type(name, 'x'.repeat(65))
    await submit(need<HTMLFormElement>(el, 'form'))
    expect(buy).not.toHaveBeenCalled()
    expect(text(el)).toContain('Write it as one short line.')
    await type(name, '')
    await submit(need<HTMLFormElement>(el, 'form'))
    expect(buy.mock.calls[0][0]).toMatchObject({ type: 'new', label: '' })
    unmount()
  })

  it('loads the plans when the app has none yet', async () => {
    ui.tab = 'buy'
    const calls = fakeCore(() => ({ plans: [plan()] }))
    const { el, unmount } = mount(ResellerSection)
    await settle()
    expect(calls.map((c) => c.path)).toEqual(['/plans'])
    expect(text(el)).toContain('Monthly')
    unmount()
  })
})

describe('the customers who joined through the link', () => {
  const customer = (over: Record<string, unknown> = {}) => ({
    telegram_id: 777001, username: 'ali_shop', joined_at: inDays(-5), paid_orders: 2,
    spent: [{ currency: 'IRT', amount: 300_000, scale: 0 }], commission: [{ currency: 'IRT', amount: 30_000, scale: 0 }], ...over,
  })

  it('lists who, when, how many orders, what they spent and what it earned', async () => {
    ui.tab = 'customers'
    fakeCore(() => ({ customers: [customer(), customer({ telegram_id: 777002, username: '', paid_orders: 0, spent: [], commission: [] })], total: 2, page: 1, size: 10 }))
    const { el, unmount } = mount(ResellerSection)
    await settle()
    const t = text(el)
    for (const want of ['Customers from your link · 2', 'ali_shop', 'Joined ', 'Orders: 2', 'Spent: 300,000 Toman', 'Your commission: 30,000 Toman', 'id 777002', 'Orders: 0', 'Spent: 0 Toman']) {
      expect(t, want).toContain(want)
    }
    unmount()
  })

  it('pages, and says when nobody has joined', async () => {
    ui.tab = 'customers'
    const calls = fakeCore((c) => (c.path.endsWith('page=1') ? { customers: [customer()], total: 12, page: 1, size: 10 } : { customers: [customer({ username: 'p2' })], total: 12, page: 2, size: 10 }))
    const { el, unmount } = mount(ResellerSection)
    await settle()
    expect(text(el)).toContain('Page 1 of 2')
    await tap(el, 'Next')
    expect(calls.at(-1)?.path).toBe('/reseller/customers?page=2')
    expect(text(el)).toContain('p2')
    unmount()

    const empty = fakeCore(() => ({ customers: [], total: 0, page: 1, size: 10 }))
    const again = mount(ResellerSection)
    await settle()
    expect(text(again.el)).toContain('Nobody has joined with your link yet.')
    expect(empty).toHaveLength(1)
    again.unmount()
  })
})

describe('the statement', () => {
  it('names a reseller’s movements and pages by whether another page follows', async () => {
    ui.tab = 'statement'
    const entries = [
      { kind: 'commission', amount: 15_000, currency: 'IRT', balance_after: -105_000, at: inDays(-1), scale: 0 },
      { kind: 'settlement', amount: 50_000, currency: 'IRT', balance_after: -120_000, at: inDays(-2), scale: 0 },
      { kind: 'purchase', amount: -120_000, currency: 'IRT', balance_after: -170_000, at: inDays(-3), scale: 0 },
    ]
    const calls = fakeCore((c) => ({ entries: c.path.endsWith('page=1') ? entries : [entries[0]], page: c.path.endsWith('page=1') ? 1 : 2, size: 10, has_more: c.path.endsWith('page=1') }))
    const { el, unmount } = mount(ResellerSection)
    await settle()
    const t = text(el)
    for (const want of ['Commission', 'Payment to the store', 'Purchase', '+15,000 Toman', '+50,000 Toman', '-120,000 Toman', 'Balance after: Debt 105,000 Toman']) {
      expect(t, want).toContain(want)
    }
    await tap(el, 'Next')
    expect(calls.at(-1)?.path).toBe('/reseller/statement?page=2')
    expect(find(el, 'button', 'Next')?.hasAttribute('disabled')).toBe(true) // no page after the last
    unmount()
  })

  it('says when there is nothing yet', async () => {
    ui.tab = 'statement'
    fakeCore(() => ({ entries: [], page: 1, size: 10, has_more: false }))
    const { el, unmount } = mount(ResellerSection)
    await settle()
    expect(text(el)).toContain('No movements yet.')
    unmount()
  })
})

describe('in Persian', () => {
  it('reads right to left with Persian digits and names', async () => {
    startAs('fa')
    expect(document.documentElement.dir).toBe('rtl')
    fakeCore(() => panel)
    const { el, unmount } = mount(ResellerSection)
    await settle()
    const t = text(el)
    for (const want of ['پنل نمایندگی', 'سطح: Gold', '۲۰٪ تخفیف از قیمت فروشگاه', 'بدهی ۱۲۰٬۰۰۰ تومان', 'قابل خرید', 'همه · ۵', 'فعال · ۲', 'متوقف · ۱', 'خرید برای مشتری']) {
      expect(t, want).toContain(want)
    }
    // the link reads left to right inside the right-to-left page
    expect(find(el, '.mono')?.getAttribute('dir')).toBe('ltr')
    unmount()
  })

  it('words the services and the delete warning in Persian', async () => {
    startAs('fa')
    ui.tab = 'services'
    fakeCore(() => page([svc({ status: 'disabled', can_pause: false, can_resume: true })]))
    const { el, unmount } = mount(ResellerSection)
    await settle()
    expect(text(el)).toContain('متوقف')
    await tap(el, 'Ali')
    await tap(el, 'حذف')
    expect(text(el)).toContain('این سرویس حذف شود؟')
    expect(text(el)).toContain('پولی بازگردانده نمی‌شود')
    unmount()
  })
})
