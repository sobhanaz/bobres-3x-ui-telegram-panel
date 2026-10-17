// The Telegram Mini App bridge. Everything works without it too (a plain
// browser shows "open the store from Telegram"), and init data is also read
// from the URL hash Telegram opens the page with, in case its script could
// not be loaded (telegram.org can be blocked).

interface BackButton {
  show(): void
  hide(): void
  onClick(cb: () => void): void
  offClick(cb: () => void): void
}

interface WebApp {
  initData?: string
  initDataUnsafe?: { user?: { language_code?: string } }
  ready?(): void
  expand?(): void
  close?(): void
  openLink?(url: string): void
  openTelegramLink?(url: string): void
  setHeaderColor?(color: string): void
  BackButton?: BackButton
  HapticFeedback?: { notificationOccurred(type: 'success' | 'error' | 'warning'): void }
  colorScheme?: 'light' | 'dark'
}

declare global {
  interface Window {
    Telegram?: { WebApp?: WebApp }
  }
}

const app = (): WebApp | undefined => window.Telegram?.WebApp

/** initData is the signed string Telegram hands the Mini App ("" outside Telegram). */
export function initData(): string {
  const fromScript = app()?.initData
  if (fromScript) return fromScript
  const hash = window.location.hash.replace(/^#/, '')
  return new URLSearchParams(hash).get('tgWebAppData') ?? ''
}

export function telegramLanguage(): string {
  return app()?.initDataUnsafe?.user?.language_code ?? navigator.language ?? ''
}

/** start tells Telegram the app is ready and uses the full height. */
export function start(): void {
  const a = app()
  a?.ready?.()
  a?.expand?.()
}

/** openLink opens a payment page or other site outside the app. */
export function openLink(url: string): void {
  const a = app()
  if (a?.openLink) a.openLink(url)
  else window.open(url, '_blank', 'noopener')
}

/** openTelegram opens a t.me link inside Telegram. */
export function openTelegram(url: string): void {
  const a = app()
  if (a?.openTelegramLink) a.openTelegramLink(url)
  else openLink(url)
}

export function closeApp(): void {
  const a = app()
  if (a?.close) a.close()
}

export function haptic(kind: 'success' | 'error' | 'warning'): void {
  app()?.HapticFeedback?.notificationOccurred(kind)
}

/** onBack shows Telegram's back button while handler is set; returns a function that removes it. */
export function onBack(handler: () => void): () => void {
  const b = app()?.BackButton
  if (!b) return () => {}
  b.onClick(handler)
  b.show()
  return () => {
    b.offClick(handler)
    b.hide()
  }
}

export function copyText(text: string): Promise<void> {
  return navigator.clipboard?.writeText(text) ?? Promise.reject(new Error('no clipboard'))
}
