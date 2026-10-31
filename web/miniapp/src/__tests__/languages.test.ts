import { afterEach, describe, expect, it } from 'vitest'
import PlansView from '../views/PlansView.vue'
import { countOf, dir, messages, pickLang, setLang, translate } from '../i18n'
import { bytes, money, num } from '../format'
import { state } from '../store'
import type { Plan } from '../api'
import { mount, settle } from './helpers'

// Arabic, Russian and Turkish (beta, machine-translated): every text of the
// app in each, with the same values; the counting texts in the forms of the
// language; only the languages the store enabled; Arabic right to left.

const values = (s: string) => [...new Set([...s.matchAll(/\{(\w+)(?:\|[^{}]*)?\}/g)].map((m) => m[1]))].sort()
const selectors = (s: string) => [...s.matchAll(/\{(\w+)((?:\|[^{}|]*)+)\}/g)].map((m) => m[2].split('|').length - 1)

afterEach(() => setLang('en'))

describe('the beta languages', () => {
  it('have every text of the app, with the values of the English one', () => {
    const en = messages.en
    for (const l of ['ar', 'ru', 'tr'] as const) {
      expect(Object.keys(messages[l]).sort(), l).toEqual(Object.keys(en).sort())
      for (const [k, v] of Object.entries(messages[l])) {
        expect(v.trim(), `${l}.${k}`).not.toBe('')
        expect(values(v), `${l}.${k}`).toEqual(values(en[k]))
      }
    }
  })

  it('count in the forms of the language: three in Russian, six in Arabic, none in Turkish', () => {
    let ru = 0
    let ar = 0
    for (const v of Object.values(messages.ru)) for (const n of selectors(v)) (ru++, expect(n).toBe(3))
    for (const v of Object.values(messages.ar)) for (const n of selectors(v)) (ar++, expect(n).toBe(6))
    for (const v of Object.values(messages.tr)) expect(selectors(v)).toEqual([])
    expect(ru).toBeGreaterThan(1)
    expect(ar).toBeGreaterThan(1)
  })

  it('pick the form the number takes', () => {
    const days = (l: 'ru' | 'ar', n: number) => translate(l, 'plans.days', { n: num(n, l) })
    const ruForms = messages.ru['plans.days'].match(/\{n\|([^}]*)\}/)![1].split('|')
    for (const [n, i] of [[1, 0], [21, 0], [3, 1], [24, 1], [5, 2], [11, 2], [1000, 2]] as const) {
      expect(days('ru', n)).toBe(messages.ru['plans.days'].replace(/\{n\|[^}]*\}/, ruForms[i].replaceAll('#', num(n, 'ru'))))
    }
    const arForms = messages.ar['plans.days'].match(/\{n\|([^}]*)\}/)![1].split('|')
    for (const [n, i] of [[0, 0], [1, 1], [2, 2], [3, 3], [10, 3], [11, 4], [99, 4], [100, 5], [1003, 3]] as const) {
      expect(days('ar', n)).toBe(messages.ar['plans.days'].replace(/\{n\|[^}]*\}/, arForms[i].replaceAll('#', num(n, 'ar'))))
    }
    expect(countOf('١٬٠٠٣', 'ar')).toBe(1003)
    expect(countOf('1\u00a0001', 'ru')).toBe(1001)
    expect(countOf('1,5', 'ru')).toBe(1.5)
    expect(countOf('1.500', 'tr')).toBe(1500)
    expect(countOf('5 GB', 'en')).toBeNaN()
  })

  it('write numbers and amounts as the language does', () => {
    expect(num(150000, 'ar')).toBe('١٥٠٬٠٠٠')
    expect(money(150000, 'IRT', 0, 'ar')).toBe('١٥٠٬٠٠٠ تومان')
    expect(money(1, 'IRT', 0, 'ru')).toBe('1 томан')
    expect(money(150000, 'IRT', 0, 'ru')).toBe('150\u00a0000 томанов')
    expect(money(2, 'IRT', 0, 'ru')).toBe('2 томана')
    expect(bytes(50 * 2 ** 30, 'tr')).toBe('50 GB')
    expect(bytes(50 * 2 ** 30, 'ru')).toBe('50 ГБ')
  })
})

describe('the languages the store enabled', () => {
  it('takes the customer’s language only when the store has it, else the store’s default', () => {
    expect(pickLang('ru', ['fa', 'en'])).toBe('fa')
    expect(pickLang('ru-RU', ['en', 'ru'])).toBe('ru')
    expect(pickLang('ar', ['en', 'fa', 'ar'])).toBe('ar')
    expect(pickLang('tr', ['en', 'fa'])).toBe('en')
    expect(pickLang('de', ['tr', 'xx'])).toBe('tr')
    expect(pickLang('en')).toBe('en')
    expect(pickLang(undefined)).toBe('fa')
  })

  it('reads Arabic right to left, as Persian', () => {
    setLang('ar')
    expect(document.documentElement.dir).toBe('rtl')
    expect(dir.value).toBe('rtl')
    setLang('ru')
    expect(document.documentElement.dir).toBe('ltr')
  })
})

describe('a page in a beta language', () => {
  const plan: Plan = {
    id: 'p1', names: { en: 'Monthly', fa: 'ماهانه' }, kind: 'both', days: 30, traffic_bytes: 50 * 1024 ** 3, price: 150000, list_price: 150000,
    currency: 'IRT', scale: 0, topup: false, group: null,
  }
  afterEach(() => {
    state.plans = null
    state.groups = []
  })

  it('shows the plans in Arabic with Arabic-Indic digits and the English plan name', async () => {
    setLang('ar')
    state.plans = [plan]
    state.groups = []
    const v = mount(PlansView, { mode: 'new' }, ['pick'])
    await settle()
    expect(v.text()).toContain('Monthly')
    expect(v.text()).toContain('١٥٠٬٠٠٠ تومان')
    expect(v.text()).toContain('٣٠')
    v.unmount()
  })

  it('shows them in Russian with the plural form of the days', async () => {
    setLang('ru')
    state.plans = [plan]
    const v = mount(PlansView, { mode: 'new' }, ['pick'])
    await settle()
    expect(v.text()).toContain('30 дней')
    expect(v.text()).toContain('150 000 томанов') // text() reads the no-break space as a plain space
    v.unmount()
  })
})
