// Helpers for the tests of components and calls (no test utility library: Vue's own createApp in jsdom).
import { createApp, h, nextTick, reactive, type Component } from 'vue'
import { vi } from 'vitest'
import { resetSession } from '../api'

/** json is a fetch answer with a JSON body. */
export const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

/** settle lets promises, timers of up to ms and Vue's rendering run (moving a fake clock on when the test has one). */
export async function settle(ms = 0): Promise<void> {
  if (vi.isFakeTimers()) await vi.advanceTimersByTimeAsync(ms)
  else await new Promise((r) => setTimeout(r, ms))
  await nextTick()
}

export interface Mounted {
  el: HTMLElement
  /** the component's props, changeable after mounting */
  props: Record<string, unknown>
  /** the events it emitted: name to the list of their arguments */
  events: Record<string, unknown[][]>
  text(): string
  buttons(): HTMLButtonElement[]
  /** the first button whose text matches */
  button(match: string | RegExp): HTMLButtonElement
  click(match: string | RegExp): Promise<void>
  unmount(): void
}

/** mount renders a component into the page and records the events it emits. */
export function mount(component: Component, initial: Record<string, unknown> = {}, emits: string[] = []): Mounted {
  const el = document.createElement('div')
  document.body.appendChild(el)
  const props = reactive({ ...initial })
  const events: Record<string, unknown[][]> = {}
  const on: Record<string, (...args: unknown[]) => void> = {}
  for (const name of emits) {
    events[name] = []
    on['on' + name[0].toUpperCase() + name.slice(1)] = (...args) => events[name].push(args)
  }
  const app = createApp({ render: () => h(component, { ...props, ...on }) })
  app.mount(el)
  const buttons = () => Array.from(el.querySelectorAll('button'))
  const button = (match: string | RegExp) => {
    const found = buttons().find((b) => (typeof match === 'string' ? (b.textContent ?? '').includes(match) : match.test(b.textContent ?? '')))
    if (!found) throw new Error(`no button ${String(match)} among: ${buttons().map((b) => JSON.stringify((b.textContent ?? '').trim())).join(', ')}`)
    return found
  }
  return {
    el,
    props,
    events,
    text: () => (el.textContent ?? '').replace(/\s+/g, ' ').trim(),
    buttons,
    button,
    click: async (match) => {
      button(match).click()
      await settle()
    },
    unmount: () => {
      app.unmount()
      el.remove()
    },
  }
}

const ME = { user: { id: 'u', username: '', language: 'en' }, wallets: [], brand: { name: '', color: '', support: '', support_url: '', bot_url: '' }, maintenance: false, top_up: { currency: 'IRT', min: 1, max: 2 }, features: {}, token: 'tok1', expires_in: 3600 }

export interface Call {
  method: string
  path: string
  init: RequestInit
}

/**
 * signedIn gives the page Telegram's init data and starts a fresh session, and answers every fetch of the API: the
 * sign-in itself, and the rest by handler (a Response, or a JSON body for a 200). It returns the calls made after the sign-in.
 */
export function signedIn(handler: (c: Call) => Response | unknown | Promise<Response | unknown>): Call[] {
  resetSession()
  window.location.hash = '#tgWebAppData=' + encodeURIComponent('user=%7B%7D&hash=abc')
  const calls: Call[] = []
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
    const path = String(url).replace('/api/mini/v1', '')
    if (path === '/auth') return json(200, ME)
    const call = { method: init?.method ?? 'GET', path, init: init ?? {} }
    calls.push(call)
    const out = await handler(call)
    return out instanceof Response ? out : json(200, out)
  })
  return calls
}

/** telegram installs a stand-in for Telegram's WebApp object. */
export function telegram(webApp: Record<string, unknown>): void {
  ;(window as unknown as { Telegram?: unknown }).Telegram = { WebApp: webApp }
}

export function noTelegram(): void {
  delete (window as unknown as { Telegram?: unknown }).Telegram
}

/** clipboard installs a clipboard that records what is copied. */
export function clipboard(): string[] {
  const copied: string[] = []
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText: async (s: string) => void copied.push(s) },
    configurable: true,
  })
  return copied
}

/** pngBytes starts like a PNG file. */
export const pngBytes = (extra = 32) => new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, ...new Array(extra).fill(7)])

/** chooseFile does what choosing a file in a file input does. */
export async function chooseFile(el: HTMLElement, file: File): Promise<void> {
  const input = el.querySelector('input[type=file]') as HTMLInputElement
  Object.defineProperty(input, 'files', { value: [file], configurable: true })
  input.dispatchEvent(new Event('change'))
  await settle(30)
}
