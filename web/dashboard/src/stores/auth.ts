import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, setCsrf } from '../api'
import { setCurrencyNames } from '../format'
import { applyBrandColor } from '../theme'

export interface Me {
  user: { id: string; telegram_id: number; username: string; role: string; language: string }
  permissions: string[]
  csrf: string
  session: { method: 'link' | 'password'; expires_at: number }
  /** reauth: replacing or removing the password needs the current code. */
  password: { enabled: boolean; username?: string; reauth?: boolean }
  password_available: boolean
  brand: string
  /** The store's look; older servers (and test fixtures) may leave it out. */
  branding?: { color: string; logo: string | null; currency: { fa: string; en: string } }
}

/** The store's public look (GET /brand, no session): for the login page. */
export interface PublicBrand {
  name: string
  color: string
  logo: string | null
}

export const useAuth = defineStore('auth', () => {
  const me = ref<Me | null>(null)
  const loaded = ref(false)
  const publicBrand = ref<PublicBrand | null>(null)

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
      publicBrand.value = { name: b.name || '', color: b.color || '', logo: b.logo || null }
      if (!me.value) applyBrandColor(publicBrand.value.color)
    } catch {
      /* defaults */
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

  async function loginWithLink(token: string) {
    set(await api<Me>('POST', '/auth/link', { token }))
  }

  async function loginWithPassword(username: string, password: string, code: string) {
    set(await api<Me>('POST', '/auth/login', { username, password, code }))
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

  return { me, loaded, publicBrand, load, set, loadPublicBrand, loginWithLink, loginWithPassword, logout, can, brand }
})
