import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp } from 'vue'
import { resetSession, type Me } from '../../../api'
import { setLang } from '../../../i18n'
import { allSections } from '../../../sections'
import { state } from '../../../store'
import '../index'
import NewsView from '../NewsView.vue'

// Product news: the section shows while the store has announcements on, and its switch saves the customer's own answer at once (and goes
// back when it could not be saved).
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const ME: Me = {
  user: { id: 'u', username: '', language: 'en' },
  wallets: [],
  brand: { name: 'Acme', color: '', support: '', support_url: '', bot_url: '' },
  maintenance: false,
  top_up: { currency: 'IRT', min: 1, max: 2 },
  features: { news: true, news_optin: false },
}

let unmount: (() => void) | undefined
function mount() {
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp(NewsView)
  app.mount(el)
  unmount = () => {
    app.unmount()
    el.remove()
  }
  return el
}
const text = (el: Element) => el.textContent?.replace(/\s+/g, ' ').trim() ?? ''

beforeEach(() => {
  resetSession()
  setLang('en')
  window.location.hash = '#tgWebAppData=' + encodeURIComponent('user=%7B%7D&hash=abc')
  state.me = { ...ME, features: { ...ME.features } }
})
afterEach(() => {
  unmount?.()
  vi.restoreAllMocks()
})

describe('product news section', () => {
  it('is listed only while the store has announcements on', () => {
    const s = allSections().find((x) => x.id === 'news')!
    expect(s.visible({ ...ME, features: { news: true } })).toBe(true)
    expect(s.visible({ ...ME, features: { news: false } })).toBe(false)
    expect(s.visible({ ...ME, features: {} })).toBe(false)
  })

  it('turns the news on and off with one switch', async () => {
    const bodies: unknown[] = []
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
      const key = `${init?.method ?? 'GET'} ${String(url).replace('/api/mini/v1', '')}`
      if (key === 'POST /auth') return json(200, { ...ME, token: 'tok' })
      if (key === 'PUT /news') {
        const body = JSON.parse(String(init!.body)) as { opt_in: boolean }
        bodies.push(body)
        return json(200, { opt_in: body.opt_in })
      }
      return json(404, { error: 'not_found', message: key })
    })
    const el = mount()
    const sw = el.querySelector<HTMLButtonElement>('[role="switch"]')!
    expect(sw.getAttribute('aria-checked')).toBe('false')
    expect(text(el)).toContain('you get no messages about new products')
    sw.click()
    await vi.waitFor(() => expect(sw.getAttribute('aria-checked')).toBe('true'))
    expect(text(el)).toContain('you get news of new products in the bot')
    expect(state.me?.features.news_optin).toBe(true)
    sw.click()
    await vi.waitFor(() => expect(sw.getAttribute('aria-checked')).toBe('false'))
    expect(bodies).toEqual([{ opt_in: true }, { opt_in: false }])
  })

  it('keeps the switch as it was when the answer cannot be saved', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
      const key = `${init?.method ?? 'GET'} ${String(url).replace('/api/mini/v1', '')}`
      if (key === 'POST /auth') return json(200, { ...ME, token: 'tok' })
      return json(409, { error: 'state', message: 'no' })
    })
    const el = mount()
    const sw = el.querySelector<HTMLButtonElement>('[role="switch"]')!
    sw.click()
    await vi.waitFor(() => expect(el.querySelector('[role="alert"]')).not.toBeNull())
    expect(sw.getAttribute('aria-checked')).toBe('false')
    expect(state.me?.features.news_optin).toBe(false)
  })
})
