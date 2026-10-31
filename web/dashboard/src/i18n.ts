import { createI18n } from 'vue-i18n'
import fa from './locales/fa.json'
import en from './locales/en.json'
import { isLang, numberLocale, rtl, type Lang } from './format'

// The beta languages are fetched when first chosen, so the page every staff
// member loads stays the size of Persian and English.
const betaCatalogs: Record<'ar' | 'ru' | 'tr', () => Promise<{ default: Record<string, unknown> }>> = {
  ar: () => import('./locales/ar.json'),
  ru: () => import('./locales/ru.json'),
  tr: () => import('./locales/tr.json'),
}

const storageKey = 'bobres.lang'

function savedLang(): Lang {
  try {
    const v = localStorage.getItem(storageKey)
    if (isLang(v)) return v
  } catch {
    /* storage blocked: use the default */
  }
  return 'fa'
}

/** pluralIndex picks a plural form by the language's own categories (CLDR):
 * Russian texts have three forms (one | few | many), Arabic ones six (zero |
 * one | two | few | many | other), Turkish ones a single form. A text with
 * fewer forms than its language has (one not translated yet) falls back to
 * vue-i18n's English rule. */
function pluralIndex(lang: Lang, forms: Record<string, number>) {
  const rules = new Intl.PluralRules(numberLocale(lang))
  const count = new Set(Object.values(forms)).size
  return (choice: number, choicesLength: number): number => {
    if (choicesLength === count) return forms[rules.select(Math.abs(choice))] ?? choicesLength - 1
    if (choicesLength === 1) return 0
    return Math.abs(choice) === 1 ? 0 : 1
  }
}

/** pluralRules are the plural rules of the beta languages (the others keep
 * vue-i18n's: English "one | other", Persian a single form). */
export const pluralRules = {
  ru: pluralIndex('ru', { one: 0, few: 1, many: 2, other: 1 }), // a fraction ("other") takes the "few" form: 1,5 дня
  ar: pluralIndex('ar', { zero: 0, one: 1, two: 2, few: 3, many: 4, other: 5 }),
  tr: pluralIndex('tr', { one: 0, other: 0 }),
}

export const i18n = createI18n({
  legacy: false,
  locale: savedLang(),
  fallbackLocale: 'en',
  messages: { fa, en } as Record<string, typeof en>,
  pluralRules,
})

const loaded = (lang: Lang) => (i18n.global.availableLocales as string[]).includes(lang)

/** loadLang fetches a beta language's texts the first time it is needed. */
export async function loadLang(lang: Lang): Promise<void> {
  if (loaded(lang) || lang === 'fa' || lang === 'en') return
  const m = await betaCatalogs[lang]()
  i18n.global.setLocaleMessage(lang, m.default as typeof en)
}

/** setLang switches the language and the page direction, and remembers it:
 * at once for a language whose texts are here, after fetching them for a beta
 * language chosen the first time. */
export function setLang(lang: Lang): Promise<void> {
  if (!loaded(lang)) return loadLang(lang).then(() => setLang(lang))
  i18n.global.locale.value = lang
  applyLang(lang)
  try {
    localStorage.setItem(storageKey, lang)
  } catch {
    /* not remembered: fine */
  }
  return Promise.resolve()
}

export function applyLang(lang: Lang): void {
  const html = document.documentElement
  html.lang = lang
  html.dir = rtl(lang) ? 'rtl' : 'ltr'
}

export function currentLang(): Lang {
  return i18n.global.locale.value as Lang
}
