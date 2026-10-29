import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, setCsrf } from '../api'
import { setCurrencyNames } from '../format'
import { isPending, type PendingEnd, type PendingLogin } from '../security'
import { applyBrandColor } from '../theme'

export interface Me {
  user: { id: string; telegram_id: number; username: string; role: string; language: string }
  permissions: string[]
  csrf: string
  session: { method: 'link' | 'password' | 'password_initial'; expires_at: number }
  /** The session came from the initial password (bobres admin password, the
   * installer): until a new password and an authenticator are set, only the
   * account page works. Older servers leave it out. */
  must_change?: boolean
  /** reauth: replacing or removing the password needs the current code.
   * initial: the login is the initial one, still to be changed. */
  password: { enabled: boolean; username?: string; reauth?: boolean; initial?: boolean }
  password_available: boolean
  brand: string
  /** The store's look; older servers (and test fixtures) may leave it out. */
  branding?: { color: string; logo: string | null; currency: { fa: string; en: string } }
  /** The license state, what applies now (basis: license | grace | trial |
   * free; tier; the trial's days left) and whether it hides "Powered by
   * BOBRES" (M6). */
  license?: { state: string; white_label: boolean; powered_by?: boolean; basis?: string; tier?: string; trial_days_left?: number }
}

/** The store's public look (GET /brand, no session): for the login page. */
export interface PublicBrand {
  name: string
  color: string
  logo: string | null
  /** show "Powered by BOBRES": always, except under a license with white
   * label (an answer that does not say counts as true) */
  powered_by?: boolean
}

export const useAuth = defineStore('auth', () => {
  const me = ref<Me | null>(null)
  const loaded = ref(false)
  const publicBrand = ref<PublicBrand | null>(null)
  /** The public brand was asked for (it answered or failed): until then the
   * login page does not know whether the store hides "Powered by BOBRES". */
  const publicBrandSettled = ref(false)

  // Logging in or out also applies (or drops) the store's colour and Toman name.
  function set(m: Me | null) {
    me.value = m
    setCsrf(m?.csrf ?? '')
    applyBrandColor(m?.branding?.color)
    setCurrencyNames(m?.branding?.currency ?? {})
  }

  /** loadPublicBrand reads the name, colour and logo shown before login; a
   * failure keeps the defaults. */
  async function loadPublicBrand() {
    try {
      const b = await api<PublicBrand>('GET', '/brand')
      // The mark is shown unless core says false: that is its default.
      publicBrand.value = { name: b.name || '', color: b.color || '', logo: b.logo || null, powered_by: b.powered_by !== false }
      if (!me.value) applyBrandColor(publicBrand.value.color)
    } catch {
      /* defaults */
    } finally {
      publicBrandSettled.value = true
    }
  }

  async function load() {
    try {
      set(await api<Me>('GET', '/me'))
    } catch {
      set(null)
    } finally {
      loaded.value = true
    }
  }

  /** refresh re-reads /me after a change (new brand, password state). A
   * failed read keeps what is shown: an ended session is handled by the 401
   * hook, a network blip must not log the page out. */
  async function refresh() {
    try {
      set(await api<Me>('GET', '/me'))
    } catch {
      /* keep the current state */
    }
  }

  /** pending is the sign-in that waits for an approval in Telegram (the owner switched that on), else null. */
  const pending = ref<PendingLogin | null>(null)

  // A login answers a session, or, while sign-ins need an approval, a wait: 'pending' says which.
  function taken(res: Me | PendingLogin): 'session' | 'pending' {
    if (isPending(res)) {
      pending.value = res
      return 'pending'
    }
    pending.value = null
    set(res)
    return 'session'
  }

  async function loginWithLink(token: string): Promise<'session' | 'pending'> {
    return taken(await api<Me | PendingLogin>('POST', '/auth/link', { token }))
  }

  async function loginWithPassword(username: string, password: string, code: string): Promise<'session' | 'pending'> {
    return taken(await api<Me | PendingLogin>('POST', '/auth/login', { username, password, code }))
  }

  /** pollPending asks how the waiting sign-in stands: 'session' once it was approved (the session is made), 'pending' while it waits,
   * or how it ended. A page reloaded while waiting finds its wait again this way. */
  async function pollPending(): Promise<'session' | 'pending' | PendingEnd> {
    const res = await api<(Me & { status: 'approved' }) | PendingLogin | { status: PendingEnd }>('POST', '/auth/pending')
    if (isPending(res)) {
      pending.value = res
      return 'pending'
    }
    pending.value = null
    if (res.status === 'approved') {
      set(res as Me)
      return 'session'
    }
    return res.status as PendingEnd
  }

  /** cancelPending gives up the wait (the request ends in Telegram too). */
  async function cancelPending() {
    try {
      await api('DELETE', '/auth/pending')
    } finally {
      pending.value = null
    }
  }

  async function logout() {
    try {
      await api('POST', '/auth/logout')
    } finally {
      set(null)
    }
  }

  const can = (perm: string) => me.value?.permissions.includes(perm) ?? false
  const brand = computed(() => me.value?.brand ?? 'BOBRES')

  return {
    me, loaded, publicBrand, publicBrandSettled, pending, load, refresh, set, loadPublicBrand,
    loginWithLink, loginWithPassword, pollPending, cancelPending, logout, can, brand,
  }
})
