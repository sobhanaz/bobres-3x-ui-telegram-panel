import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import PrimeVue from 'primevue/config'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import ar from '../locales/ar.json'
import ru from '../locales/ru.json'
import tr from '../locales/tr.json'
import { i18n, loadLang, setLang } from '../i18n'
import { date, money, num, percent, type Lang } from '../format'
import { formatDate } from '../calendar'
import { formatLangs, parseLangs, validate } from '../settings'
import type { SettingItem } from '../types'

// Arabic, Russian and Turkish (beta, machine-translated): every text of the
// English catalog is in each, with the same values and literal escapes; the
// plural texts have the forms of their language; numbers and dates follow
// the language; the language menu and the Settings editor offer them.

type Cat = Record<string, unknown>
function flat(o: Cat, prefix = ''): [string, string][] {
  return Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v as Cat, prefix + k + '.') : [[prefix + k, String(v)] as [string, string]]))
}
const enTexts = flat(en)
const BETA = { ar, ru, tr } as const
const FORMS = { ar: 6, ru: 3, tr: 1 } as const
const holes = (m: string) => [...new Set([...m.matchAll(/\{(\w+)\}/g)].map((x) => x[1]))].sort()
// {'%'} lets Turkish write the percent before its number ("%{n}" would read as a placeholder).
const literals = (m: string) => [...m.matchAll(/\{'[^']*'\}/g)].map((x) => x[0]).filter((x) => x !== "{'%'}").sort()
const forms = (m: string) => m.replace(/\{'\|'\}/g, '').split('|').length

// The beta catalogs are fetched when first chosen: here, before the tests.
beforeAll(async () => {
  await Promise.all([loadLang('ar'), loadLang('ru'), loadLang('tr')])
})
afterEach(() => void setLang('fa'))

describe('the beta catalogs', () => {
  for (const [lang, cat] of Object.entries(BETA) as [keyof typeof BETA, Cat][]) {
    const texts = new Map(flat(cat))
    it(`${lang}: every text, with the values, escapes and plural forms of the English one`, () => {
      const bad: string[] = []
      for (const [key, m] of enTexts) {
        const t = texts.get(key)
        if (t === undefined || t.trim() === '') {
          bad.push(`${key}: missing`)
          continue
        }
        if (holes(t).join() !== holes(m).join()) bad.push(`${key}: values ${holes(t)} vs ${holes(m)}`)
        if (literals(t).join() !== literals(m).join()) bad.push(`${key}: escapes ${literals(t)} vs ${literals(m)}`)
        const want = m.includes('|') ? FORMS[lang] : 1
        if (forms(t) !== want) bad.push(`${key}: ${forms(t)} plural forms, ${lang} takes ${want}`)
        if (/%\{/.test(t)) bad.push(`${key}: "%{" reads as a placeholder`)
      }
      expect(bad).toEqual([])
      expect(texts.size).toBe(enTexts.length)
    })
  }
  it('keeps Persian with no plural forms', () => {
    expect(flat(fa).filter(([, m]) => m.replace(/\{'\|'\}/g, '').includes('|'))).toEqual([])
  })
})

describe('plural forms in the beta languages', () => {
  const say = (lang: Lang, key: string, count: number) => {
    i18n.global.locale.value = lang
    return i18n.global.t(key, { n: String(count) }, count)
  }
  const form = (cat: Cat, key: string, i: number, n: number) => {
    const m = new Map(flat(cat)).get(key)!
    return m.split('|')[i].trim().replace(/\{n\}/g, String(n))
  }
  const piped = enTexts.filter(([, m]) => m.includes('|') && holes(m).join() === 'n').map(([k]) => k)

  it('reads Russian one | few | many by the number', () => {
    expect(piped.length).toBeGreaterThan(20)
    for (const key of piped) {
      for (const [n, i] of [[1, 0], [21, 0], [2, 1], [4, 1], [22, 1], [5, 2], [11, 2], [12, 2], [0, 2], [100, 2]]) {
        expect(say('ru', key, n), `${key} ${n}`).toBe(form(ru, key, i, n))
      }
    }
  })
  it('reads Arabic zero | one | two | few | many | other by the number', () => {
    for (const key of piped) {
      for (const [n, i] of [[0, 0], [1, 1], [2, 2], [3, 3], [10, 3], [11, 4], [99, 4], [100, 5], [102, 5]]) {
        expect(say('ar', key, n), `${key} ${n}`).toBe(form(ar, key, i, n))
      }
    }
  })
  it('reads Turkish the same for every number', () => {
    for (const key of piped) expect(say('tr', key, 1).replace('1', '5'), key).toBe(say('tr', key, 5))
  })
})

describe('numbers and dates in the beta languages', () => {
  it('writes Arabic-Indic digits in Arabic, and Gregorian dates in all three', () => {
    expect(num(1234567, 'ar')).toBe('١٬٢٣٤٬٥٦٧')
    expect(percent(50, 'ar')).toMatch(/٥٠/)
    const nov2 = Date.UTC(2026, 10, 2, 12) / 1000
    expect(date(nov2, 'ar')).toMatch(/٢٠٢٦/)
    expect(date(nov2, 'ru')).toMatch(/2026/)
    expect(date(nov2, 'tr')).toMatch(/Kas/)
    expect(formatDate('2026-11-02', 'ar', 'gregorian')).toMatch(/٢/)
    expect(num(1234567, 'ru')).toBe('1\u00a0234\u00a0567')
    expect(num(1234567, 'tr')).toBe('1.234.567')
  })
  it('writes the Toman in the plural form its amount takes in Russian', () => {
    expect(money({ amount: 1, currency: 'IRT' }, 'ru')).toBe('1 томан')
    expect(money({ amount: 3, currency: 'IRT' }, 'ru')).toBe('3 томана')
    expect(money({ amount: 150000, currency: 'IRT' }, 'ru')).toBe('150\u00a0000 томанов')
    expect(money({ amount: 150000, currency: 'IRT' }, 'ar')).toBe('١٥٠٬٠٠٠ تومان')
  })
  it('turns the page right to left for Arabic as for Persian', () => {
    setLang('ar')
    expect(document.documentElement.dir).toBe('rtl')
    expect(document.documentElement.lang).toBe('ar')
    setLang('ru')
    expect(document.documentElement.dir).toBe('ltr')
    setLang('fa')
    expect(document.documentElement.dir).toBe('rtl')
  })
})

describe('the customer languages setting', () => {
  const item: SettingItem = { key: 'languages.enabled', group: 'general', kind: 'langs', value: '', default: 'fa,en', options: [], min: null, max: null, editable: true } as unknown as SettingItem
  it('reads and writes the list with the default first', () => {
    expect(parseLangs('ru, fa ,en')).toEqual({ langs: ['ru', 'fa', 'en'] })
    expect(parseLangs('fa,fa')).toEqual({ code: 'langs' })
    expect(parseLangs('de')).toEqual({ code: 'langs' })
    expect(parseLangs(' ')).toEqual({ code: 'langs_empty' })
    expect(formatLangs(['tr', 'fa', 'ru'], 'ru')).toBe('ru,fa,tr')
    expect(validate(item, 'fa,en')).toBeNull()
    expect(validate(item, 'fa,xx')).toEqual({ code: 'langs' })
  })

  const mount = async (component: Component, props: Record<string, unknown>) => {
    const root = document.createElement('div')
    document.body.appendChild(root)
    const app = createApp({ render: () => h(component, props) })
    app.use(i18n).use(PrimeVue)
    app.mount(root)
    await nextTick()
    return { root, unmount: () => app.unmount() }
  }

  it('offers the three beta languages with a tag and a note, and keeps the default first', async () => {
    setLang('en')
    const { default: LanguagesEditor } = await import('../components/settings/LanguagesEditor.vue')
    const values: string[] = []
    let value = 'fa,en'
    const { root, unmount } = await mount(LanguagesEditor as Component, {
      modelValue: value, inputId: 'langs', 'onUpdate:modelValue': (v: string) => values.push((value = v)),
    })
    const rows = [...root.querySelectorAll('.row')]
    expect(rows.map((r) => r.getAttribute('data-lang'))).toEqual(['fa', 'en', 'ar', 'ru', 'tr'])
    expect(rows.map((r) => r.querySelector('.p-tag')?.textContent ?? '')).toEqual(['', '', 'beta', 'beta', 'beta'])
    expect(root.textContent).not.toContain('machine-translated')
    ;(rows[3].querySelector('input[type=checkbox]') as HTMLInputElement).click()
    await nextTick()
    expect(values.at(-1)).toBe('fa,en,ru')
    unmount()

    const second = await mount(LanguagesEditor as Component, { modelValue: 'fa,en,ru', inputId: 'langs2', 'onUpdate:modelValue': (v: string) => values.push(v) })
    expect(second.root.textContent).toContain('machine-translated')
    const radios = second.root.querySelectorAll('input[type=radio]')
    expect(radios).toHaveLength(3)
    ;(radios[2] as HTMLInputElement).click()
    await nextTick()
    expect(values.at(-1)).toBe('ru,fa,en')
    second.unmount()
  })

  it('lets staff switch the dashboard to a beta language from the menu', async () => {
    setLang('en')
    const { default: LangMenu } = await import('../components/LangMenu.vue')
    const { root, unmount } = await mount(LangMenu as Component, {})
    ;(root.querySelector('[data-test=lang-menu]') as HTMLButtonElement).click()
    await nextTick()
    const items = [...document.querySelectorAll('.lang-item')]
    expect(items.map((i) => i.getAttribute('data-lang'))).toEqual(['fa', 'en', 'ar', 'ru', 'tr'])
    expect(items.filter((i) => i.querySelector('.p-tag')).map((i) => i.getAttribute('data-lang'))).toEqual(['ar', 'ru', 'tr'])
    ;(items[2] as HTMLElement).click()
    await nextTick()
    expect(i18n.global.locale.value).toBe('ar')
    expect(document.documentElement.dir).toBe('rtl')
    unmount()
  })
})
