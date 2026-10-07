import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, setCsrf } from '../api'

export interface Me {
  user: { id: string; telegram_id: number; username: string; role: string; language: string }
  permissions: string[]
  csrf: string
  session: { method: 'link' | 'password'; expires_at: number }
  password: { enabled: boolean; username?: string }
  password_available: boolean
  brand: string
}

export const useAuth = defineStore('auth', () => {
  const me = ref<Me | null>(null)
  const loaded = ref(false)

  function set(m: Me | null) {
    me.value = m
    setCsrf(m?.csrf ?? '')
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

  return { me, loaded, load, set, loginWithLink, loginWithPassword, logout, can, brand }
})
