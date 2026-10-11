import { createI18n } from 'vue-i18n'
import fa from './locales/fa.json'
import en from './locales/en.json'
import type { Lang } from './format'

const storageKey = 'bobres.lang'

function savedLang(): Lang {
  try {
    const v = localStorage.getItem(storageKey)
    if (v === 'fa' || v === 'en') return v
  } catch {
    /* storage blocked: use the default */
  }
  return 'fa'
}

export const i18n = createI18n({
  legacy: false,
  locale: savedLang(),
  fallbackLocale: 'en',
  messages: { fa, en },
})

/** setLang switches the language and the page direction, and remembers it. */
export function setLang(lang: Lang): void {
  i18n.global.locale.value = lang
  applyLang(lang)
  try {
    localStorage.setItem(storageKey, lang)
  } catch {
    /* not remembered: fine */
  }
}

export function applyLang(lang: Lang): void {
  const html = document.documentElement
  html.lang = lang
  html.dir = lang === 'fa' ? 'rtl' : 'ltr'
}

export function currentLang(): Lang {
  return i18n.global.locale.value as Lang
}
