import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Service } from '../api'
import { messages, translate } from '../i18n'
import { num } from '../format'
import { state } from '../store'
import ServicesView from '../views/ServicesView.vue'
import ServiceCard from '../sections/reseller/ServiceCard.vue'
import { fakeCore, inDays, mount, settle, startAs, svc, text, unmountAll } from '../sections/reseller/__tests__/helpers'

// Days from the first connection: a service still waiting for its config's first connection has no end date
// (expires_at null) and says how long it will run once it connects, "30 days · from the first connection", on My services
// and on the reseller panel's service cards, instead of nothing.

const service = (over: Partial<Service> = {}): Service => ({
  id: '00000000-0000-0000-0000-0000000000d1', label: 'Monthly', status: 'active', expires_at: null, first_use_days: 30, traffic_total: null,
  traffic_used: 0, link: 'https://sub.example/x', page_url: '', can_renew: true, can_topup: false, ...over,
})

afterEach(() => {
  unmountAll()
  vi.restoreAllMocks()
  state.services = null
  startAs('en')
})

describe('the waiting text', () => {
  it('is in every language, with the number', () => {
    for (const l of ['fa', 'en', 'ar', 'ru', 'tr'] as const) expect(messages[l]['services.first_use'], l).toMatch(/\{n[|}]/)
  })

  it('counts the days in the forms of the language', () => {
    expect(translate('en', 'services.first_use', { n: num(30, 'en') })).toBe('30 days · from the first connection')
    expect(translate('fa', 'services.first_use', { n: num(30, 'fa') })).toBe('۳۰ روز · از اولین اتصال')
    expect(translate('ru', 'services.first_use', { n: num(21, 'ru') })).toBe('21 день · с первого подключения')
    expect(translate('ru', 'services.first_use', { n: num(30, 'ru') })).toBe('30 дней · с первого подключения')
    expect(translate('ar', 'services.first_use', { n: num(1, 'ar') })).toBe('يوم واحد · من أول اتصال')
    expect(translate('tr', 'services.first_use', { n: num(30, 'tr') })).toBe('30 gün · ilk bağlantıdan itibaren')
  })
})

describe('My services', () => {
  it('shows a waiting service’s days from the first connection, and nothing of the kind for the others', async () => {
    startAs('en')
    fakeCore((c) =>
      c.path === '/services'
        ? {
            services: [
              service(),
              service({ id: '00000000-0000-0000-0000-0000000000d2', label: 'Dated', expires_at: inDays(10), first_use_days: null }),
              service({ id: '00000000-0000-0000-0000-0000000000d3', label: 'Forever', first_use_days: null }),
            ],
          }
        : {},
    )
    const { el } = mount(ServicesView)
    await settle()
    const cards = [...el.querySelectorAll('li.card')]
    expect(cards).toHaveLength(3)
    expect(text(cards[0])).toContain('30 days · from the first connection')
    expect(text(cards[1])).toMatch(/days left · Ends /)
    expect(text(cards[1])).not.toContain('first connection')
    expect(text(cards[2])).not.toContain('first connection')
    expect(text(cards[2])).not.toContain('Ends')
  })

  it('says it in Persian with Persian digits, right to left', async () => {
    startAs('fa')
    fakeCore((c) => (c.path === '/services' ? { services: [service({ first_use_days: 45 })] } : {}))
    const { el } = mount(ServicesView)
    await settle()
    expect(text(el)).toContain('۴۵ روز · از اولین اتصال')
    expect(document.documentElement.dir).toBe('rtl')
  })
})

describe('the reseller panel’s service card', () => {
  it('shows a waiting service’s days from the first connection instead of an end', async () => {
    startAs('en')
    fakeCore(() => ({}))
    const waiting = mount(ServiceCard, { service: svc({ expires_at: null, first_use_days: 30 }), open: false })
    await settle()
    expect(text(waiting.el)).toContain('30 days · from the first connection')
    expect(text(waiting.el)).not.toContain('Ends')

    const dated = mount(ServiceCard, { service: svc({ first_use_days: null }), open: false })
    await settle()
    expect(text(dated.el)).toMatch(/days left · Ends /)
    expect(text(dated.el)).not.toContain('first connection')

    // an older core leaves first_use_days out: a service with no end says nothing about it
    const endless = mount(ServiceCard, { service: svc({ expires_at: null }), open: false })
    await settle()
    expect(text(endless.el)).not.toContain('first connection')
  })

  it('says it in Persian', async () => {
    startAs('fa')
    fakeCore(() => ({}))
    const { el } = mount(ServiceCard, { service: svc({ expires_at: null, first_use_days: 30 }), open: false })
    await settle()
    expect(text(el)).toContain('۳۰ روز · از اولین اتصال')
  })
})
