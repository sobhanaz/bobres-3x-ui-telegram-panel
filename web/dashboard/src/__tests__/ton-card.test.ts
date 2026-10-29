import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import PrimeVue from 'primevue/config'
import type { GatewayConfig } from '../types'

// The TON card on its own, with a clock: the price grows older by itself, and the settings are asked for again every minute.
// (A file of its own: faking the clock must not reach the page tests, which run in the same window otherwise.)
const t0 = 1_760_000_000
const tonConfig = (over: Partial<GatewayConfig> = {}): GatewayConfig => ({
  provider: 'ton_auto',
  enabled: true,
  configured: true,
  offered: true,
  problem: '',
  sandbox: false,
  merchant_hint: '',
  proxy_set: false,
  address: 'UQDtFpEwcFAEcRe5mLVh2N6C0x-_hJEM7W61_JLnSF74p9dz',
  key_set: true,
  key_missing: false,
  health: { healthy: true, open: 1, oldest_open_at: t0 - 600, last_paid_at: t0 - 300, last_error_at: null, last_error: '' },
  price: { usdt_per_ton: '3.2145', source: 'binance', at: t0 - 10, fresh: true },
  ...over,
})

describe('automatic TON card clock', () => {
  const unmounts: (() => void)[] = []
  afterEach(() => {
    unmounts.splice(0).forEach((f) => f())
    vi.useRealTimers()
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
  })
  async function mountCard(config: GatewayConfig) {
    const { default: TonAutoCard } = await import('../components/gateways/TonAutoCard.vue')
    const { i18n, setLang } = await import('../i18n')
    const { default: ToastService } = await import('primevue/toastservice')
    setLang('en')
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify({ provider: 'ton_auto', totals: [], healthy: true, open: 0, oldest_open_at: null, last_paid_at: null, last_error_at: null, last_error: '' }), { status: 200, headers: { 'Content-Type': 'application/json' } })),
    )
    const refreshed = vi.fn()
    const root = document.createElement('div')
    document.body.appendChild(root)
    const app = createApp({ render: () => h(TonAutoCard as Component, { config, onRefresh: refreshed }) })
    app.use(i18n).use(PrimeVue).use(ToastService)
    app.mount(root)
    let mounted = true
    const unmount = () => {
      if (!mounted) return
      mounted = false
      app.unmount()
      setLang('fa')
    }
    unmounts.push(unmount)
    await nextTick()
    return { root, refreshed, unmount }
  }

  it('asks for the settings again every minute while it is on the screen, and not after', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const { refreshed, unmount } = await mountCard(tonConfig())
    vi.advanceTimersByTime(59_999)
    expect(refreshed).toHaveBeenCalledTimes(0)
    vi.advanceTimersByTime(1)
    expect(refreshed).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(120_000)
    expect(refreshed).toHaveBeenCalledTimes(3)
    unmount()
    vi.advanceTimersByTime(300_000)
    expect(refreshed).toHaveBeenCalledTimes(3)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('lets the price grow older on the screen without asking for it', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] })
    vi.setSystemTime(new Date(t0 * 1000))
    const { root } = await mountCard(tonConfig())
    const age = () => root.querySelector('.price-age')?.textContent
    expect(age()).toBe('10 seconds ago')
    expect(root.querySelector('.price-source')?.textContent).toBe('Binance')
    vi.advanceTimersByTime(15_000)
    await nextTick()
    expect(age()).toBe('25 seconds ago')
    vi.advanceTimersByTime(75_000)
    await nextTick()
    expect(age()).toBe('2 minutes ago')
  })
})
