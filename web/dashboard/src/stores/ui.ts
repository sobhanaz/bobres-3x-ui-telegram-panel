import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { darkModeSelector } from '../theme'

export type ThemeChoice = 'system' | 'dark' | 'light'

const storageKey = 'bobres.theme'

function saved(): ThemeChoice {
  try {
    const v = localStorage.getItem(storageKey)
    if (v === 'dark' || v === 'light' || v === 'system') return v
  } catch {
    /* default */
  }
  return 'system'
}

/** The UI store: dark or light (following the system by default). */
export const useUi = defineStore('ui', () => {
  const theme = ref<ThemeChoice>(saved())
  const media = window.matchMedia?.('(prefers-color-scheme: dark)')

  function apply() {
    const dark = theme.value === 'dark' || (theme.value === 'system' && (media?.matches ?? false))
    document.documentElement.classList.toggle(darkModeSelector.slice(1), dark)
  }

  watch(theme, (t) => {
    try {
      localStorage.setItem(storageKey, t)
    } catch {
      /* not remembered */
    }
    apply()
  })
  media?.addEventListener?.('change', apply)
  apply()

  function cycleTheme() {
    theme.value = theme.value === 'system' ? 'dark' : theme.value === 'dark' ? 'light' : 'system'
  }

  return { theme, cycleTheme }
})
