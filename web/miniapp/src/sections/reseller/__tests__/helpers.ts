// What the reseller section's tests share: mounting a component in jsdom (no
// test library: Vue's own createApp), a fake core that answers /api/mini/v1
// calls, and finders that read the page the way a reseller does (by text).
import { createApp, type Component } from 'vue'
import { vi } from 'vitest'
import { resetSession } from '../../../api'
import { setLang } from '../../../i18n'
import type { Me } from '../../../api'

export const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

export const ME: Me & { token: string; expires_in: number } = {
  user: { id: 'u1', username: 'rsl', language: 'en' },
  wallets: [{ currency: 'IRT', balance: 0, scale: 0 }],
  brand: { name: 'Shop', color: '', support: '', support_url: '', bot_url: '' },
  maintenance: false,
  top_up: { currency: 'IRT', min: 10_000, max: 50_000_000 },
  features: { reseller: true },
  token: 'tok1',
  expires_in: 3600,
}

export interface Call { method: string; path: string; body: unknown }

/** fakeCore answers every call of the app but sign-in with what the handler returns (an object is a 200 answer, a Response is sent as it is). */
export function fakeCore(handler: (c: Call) => unknown): Call[] {
  resetSession()
  window.location.hash = '#tgWebAppData=' + encodeURIComponent('user=%7B%7D&hash=abc')
  const calls: Call[] = []
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
    const path = String(url).replace('/api/mini/v1', '')
    if (path === '/auth') return json(200, ME)
    const call: Call = { method: init?.method ?? 'GET', path, body: init?.body ? JSON.parse(String(init.body)) : undefined }
    calls.push(call)
    const out = handler(call)
    return out instanceof Response ? out : json(200, out ?? {})
  })
  return calls
}

const mounted: (() => void)[] = []

export function mount(component: Component, props: Record<string, unknown> = {}) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  const app = createApp(component, props)
  app.mount(el)
  let done = false
  const unmount = () => {
    if (done) return
    done = true
    app.unmount()
    el.remove()
  }
  mounted.push(unmount)
  return { el, unmount }
}

/** unmountAll takes down what a test left mounted (a failed test must not leave a screen listening to the next one). */
export function unmountAll(): void {
  for (const u of mounted.splice(0)) u()
}

/** settle lets pending promises and renders finish. */
export async function settle(): Promise<void> {
  for (let i = 0; i < 6; i++) await new Promise((r) => setTimeout(r, 0))
}

/** text is what a reader sees of an element, on one line: its pieces of text with a space between (a layout puts them apart). */
export function text(root: ParentNode): string {
  const parts: string[] = []
  const walk = (n: Node) => {
    if (n.nodeType === Node.TEXT_NODE) {
      const t = (n.nodeValue ?? '').replace(/\s+/g, ' ').trim()
      if (t) parts.push(t)
    } else n.childNodes.forEach(walk)
  }
  walk(root as Node)
  return parts.join(' ')
}

/** find returns the first element matching selector whose text contains (or matches) what is asked for. */
export function find<T extends Element = HTMLElement>(root: ParentNode, selector: string, what?: string | RegExp): T | null {
  const all = [...root.querySelectorAll<T>(selector)]
  if (what === undefined) return all[0] ?? null
  return all.find((e) => (typeof what === 'string' ? text(e).includes(what) : what.test(text(e)))) ?? null
}

export function need<T extends Element = HTMLElement>(root: ParentNode, selector: string, what?: string | RegExp): T {
  const el = find<T>(root, selector, what)
  if (!el) throw new Error(`no ${selector} ${what ?? ''} in: ${text(root).slice(0, 400)}`)
  return el
}

/** tap clicks the button with this text and lets the page settle. */
export async function tap(root: ParentNode, what: string | RegExp): Promise<void> {
  need<HTMLButtonElement>(root, 'button', what).click()
  await settle()
}

/** type fills an input the way typing does. */
export async function type(input: HTMLInputElement, value: string): Promise<void> {
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await settle()
}

export async function submit(form: HTMLFormElement): Promise<void> {
  form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  await settle()
}

/** startAs puts the app in a language with nothing left over from the last test. */
export function startAs(lang: 'en' | 'fa'): void {
  setLang(lang)
  document.body.innerHTML = ''
}

const day = 86_400
export const inDays = (n: number): number => Math.floor(Date.now() / 1000) + n * day

export function svc(over: Record<string, unknown> = {}) {
  return {
    id: '01a156a6-0fa9-7182-959e-98b79df62d88',
    label: 'Ali',
    note: '',
    status: 'active',
    expires_at: inDays(20),
    traffic_total: 50 * 2 ** 30,
    traffic_used: 10 * 2 ** 30,
    created_at: inDays(-3),
    link: 'https://sub.example/x',
    page_url: 'https://panel.example/sub/abc',
    can_renew: true,
    can_topup: true,
    can_pause: true,
    can_resume: false,
    can_delete: true,
    ...over,
  }
}

export const counts = (over: Record<string, number> = {}) => ({ total: 5, active: 2, expiring_soon: 1, expired: 1, paused: 1, pending: 0, ...over })
