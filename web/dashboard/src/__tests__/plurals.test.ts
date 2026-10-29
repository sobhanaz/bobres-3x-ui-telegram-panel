import { afterEach, describe, expect, it, vi } from 'vitest'
import { i18n } from '../i18n'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { date, num, type Lang } from '../format'
import { rateCount } from '../broadcasts'
import { alertArgs, alertCount, alertTextKey } from '../system'
import type { SystemAlert } from '../types'

// English puts a noun after the number, so "1 day" and "2 days" are two
// texts: a message that counts something is written "{n} day | {n} days"
// (vue-i18n's pipe form) and asked for with the raw number as the third
// argument, t(key, { n: num(x, lang) }, x), because n itself is the number as
// shown ("1,200"). With two forms vue-i18n picks the first for exactly 1 and
// the second for anything else, 0 included. Persian has no such split: its
// texts have no pipe and read the same for every count.
//
// Three layers: the catalogs say the right thing for 0, 1 and 2; a scan keeps
// a counting text from being written without its plural form again (and a
// plural text from being asked for without the count); and the real pages,
// booted in English, pass the right number.

const say = (lang: Lang, key: string, count: number, params: Record<string, string> = {}) => {
  i18n.global.locale.value = lang
  return i18n.global.t(key, { n: num(count, lang), ...params }, count)
}

afterEach(() => {
  i18n.global.locale.value = 'fa'
})

type Forms = [zero: string, one: string, two: string]
// key, the values besides n, and what English says for 0, 1 and 2
const CASES: [string, Record<string, string>, Forms][] = [
  ['service.days_left', {}, ['0 days left', '1 day left', '2 days left']],
  ['service.days_ago', {}, ['ended 0 days ago', 'ended 1 day ago', 'ended 2 days ago']],
  ['plan.days', {}, ['0 days', '1 day', '2 days']],
  ['plan.servers_count', {}, ['0 servers', '1 server', '2 servers']],
  ['order.attempts', {}, ['0 attempts', '1 attempt', '2 attempts']],
  ['overview.resellers_count', {}, ['0 resellers', '1 reseller', '2 resellers']],
  ['overview.services_n', {}, ['0 services', '1 service', '2 services']],
  ['overview.servers_n', {}, ['0 servers', '1 server', '2 servers']],
  ['support.badge', {}, ['0 tickets waiting', '1 ticket waiting', '2 tickets waiting']],
  ['license.left.left', {}, ['0 days left', '1 day left', '2 days left']],
  ['license.left.grace', {}, ['0 days of grace left', '1 day of grace left', '2 days of grace left']],
  ['license.left.over', {}, ['Ended 0 days ago', 'Ended 1 day ago', 'Ended 2 days ago']],
  ['license.chip.trial', {}, ['Trial: 0 days left', 'Trial: 1 day left', 'Trial: 2 days left']],
  ['license.plan.trial', {}, ['Trial: 0 days left', 'Trial: 1 day left', 'Trial: 2 days left']],
  [
    'license.plan.trial_ends',
    { date: '1 Jan 2027' },
    ['The trial ends on 1 Jan 2027 (0 days left).', 'The trial ends on 1 Jan 2027 (1 day left).', 'The trial ends on 1 Jan 2027 (2 days left).'],
  ],
  ['system.https.addresses.days_left', {}, ['0 days left', '1 day left', '2 days left']],
  ['texts.import_done', {}, ['0 texts changed', '1 text changed', '2 texts changed']],
  ['texts.import_file', { file: 'x.json' }, ['0 texts in x.json', '1 text in x.json', '2 texts in x.json']],
  ['texts.limit', {}, ['up to 0 characters', 'up to 1 character', 'up to 2 characters']],
  ['broadcasts.button.label_long', {}, ['At most 0 characters.', 'At most 1 character.', 'At most 2 characters.']],
  ['broadcasts.detail.rate_value', {}, ['0 messages a second', '1 message a second', '2 messages a second']],
  [
    'broadcasts.send.confirm_now',
    {},
    [
      'This message goes to 0 customers now. Messages already sent cannot be taken back.',
      'This message goes to 1 customer now. Messages already sent cannot be taken back.',
      'This message goes to 2 customers now. Messages already sent cannot be taken back.',
    ],
  ],
  [
    'broadcasts.send.confirm_later',
    { time: 'Sat 1 Jan' },
    [
      'This message goes to 0 customers on Sat 1 Jan. Until then you can still edit or cancel it.',
      'This message goes to 1 customer on Sat 1 Jan. Until then you can still edit or cancel it.',
      'This message goes to 2 customers on Sat 1 Jan. Until then you can still edit or cancel it.',
    ],
  ],
  [
    'server.test_ok',
    { version: '3x-ui v3.9.0' },
    ['Connected: 3x-ui v3.9.0, 0 inbounds.', 'Connected: 3x-ui v3.9.0, 1 inbound.', 'Connected: 3x-ui v3.9.0, 2 inbounds.'],
  ],
  [
    'server.address_changed',
    {},
    [
      'This server has 0 services. Change the address only when the same panel moved; pointing it to another panel leaves those services behind.',
      'This server has 1 service. Change the address only when the same panel moved; pointing it to another panel leaves that service behind.',
      'This server has 2 services. Change the address only when the same panel moved; pointing it to another panel leaves those services behind.',
    ],
  ],
  [
    'gateway.open_payments',
    {},
    [
      '0 Zarinpal payments are still open. The merchant id and the sandbox cannot change until they finish, or those payments could not be confirmed.',
      '1 Zarinpal payment is still open. The merchant id and the sandbox cannot change until it finishes, or that payment could not be confirmed.',
      '2 Zarinpal payments are still open. The merchant id and the sandbox cannot change until they finish, or those payments could not be confirmed.',
    ],
  ],
  [
    'gateway.usdt.open_payments',
    {},
    [
      '0 automatic USDT payments are still open. The address cannot change until they finish, or USDT sent to the old address would not be found.',
      '1 automatic USDT payment is still open. The address cannot change until it finishes, or USDT sent to the old address would not be found.',
      '2 automatic USDT payments are still open. The address cannot change until they finish, or USDT sent to the old address would not be found.',
    ],
  ],
  [
    'gateway.ton.open_payments',
    {},
    [
      '0 automatic TON payments are still open. The address cannot change until they finish, or TON sent to the old address would not be found.',
      '1 automatic TON payment is still open. The address cannot change until it finishes, or TON sent to the old address would not be found.',
      '2 automatic TON payments are still open. The address cannot change until they finish, or TON sent to the old address would not be found.',
    ],
  ],
  [
    'gateway.keyed.pin.open_payments',
    { name: 'Aqayepardakht' },
    [
      '0 Aqayepardakht payments are still open. The PIN cannot change until they finish, or those payments could not be confirmed.',
      '1 Aqayepardakht payment is still open. The PIN cannot change until it finishes, or that payment could not be confirmed.',
      '2 Aqayepardakht payments are still open. The PIN cannot change until they finish, or those payments could not be confirmed.',
    ],
  ],
  ['reports.kpi.orders', {}, ['0 paid orders', '1 paid order', '2 paid orders']],
  ['reports.kpi.topups_n', {}, ['0 top-ups', '1 top-up', '2 top-ups']],
  ['reports.kpi.refunds_n', {}, ['0 refunds', '1 refund', '2 refunds']],
  ['reports.kpi.rejected_n', {}, ['0 payments', '1 payment', '2 payments']],
  ['reports.kpi.cancelled_n', {}, ['0 orders', '1 order', '2 orders']],
  [
    'reports.churn.hint',
    {},
    [
      'Services that ended in this period and were not renewed within 0 days',
      'Services that ended in this period and were not renewed within 1 day',
      'Services that ended in this period and were not renewed within 2 days',
    ],
  ],
  ['gateway.test_unverified', {}, ['0 paid payments are waiting to be confirmed.', '1 paid payment is waiting to be confirmed.', '2 paid payments are waiting to be confirmed.']],
  ['settings.tiers.max', {}, ['The list holds at most 0 tiers.', 'The list holds at most 1 tier.', 'The list holds at most 2 tiers.']],
  ['settings.prizes.max', {}, ['The wheel holds at most 0 prizes.', 'The wheel holds at most 1 prize.', 'The wheel holds at most 2 prizes.']],
  ['reseller.targets.tier', { bonus: '50,000 T' }, ['0 services: 50,000 T bonus', '1 service: 50,000 T bonus', '2 services: 50,000 T bonus']],
  [
    'reseller.targets.next',
    { bonus: '50,000 T' },
    ['0 more services for the next bonus of 50,000 T.', '1 more service for the next bonus of 50,000 T.', '2 more services for the next bonus of 50,000 T.'],
  ],
]

describe('English plurals', () => {
  for (const [key, params, forms] of CASES) {
    it(`${key}: 0 and 2 are plural, 1 is singular`, () => {
      expect([0, 1, 2].map((n) => say('en', key, n, params))).toEqual(forms)
    })
  }

  it('picks the form by the number, not by the text shown for it', () => {
    i18n.global.locale.value = 'en'
    const { t } = i18n.global
    expect(t('service.days_left', { n: '1,200' }, 1200)).toBe('1,200 days left')
    expect(t('service.days_left', { n: '1' }, 1)).toBe('1 day left')
    // a text pluralized by the total, not by the part ("3 of 1" cannot happen; "1 of 40 texts" does)
    expect(t('texts.count', { n: '1', total: '40' }, 40)).toBe('1 of 40 texts')
    expect(t('texts.count', { n: '1', total: '1' }, 1)).toBe('1 of 1 text')
    expect(t('broadcasts.detail.done_of', { done: '0', total: '1' }, 1)).toBe('0 of 1 recipient done')
    expect(t('broadcasts.detail.done_of', { done: '1', total: '2' }, 2)).toBe('1 of 2 recipients done')
  })

  it('says a fraction of a message a second in the plural, and exactly 1 in the singular', () => {
    expect(rateCount(1)).toBe(1)
    expect(rateCount(1.04)).toBe(1) // shown as "1"
    expect(rateCount(0.5)).toBe(2)
    expect(rateCount(1.5)).toBe(2)
    expect(rateCount(25)).toBe(2)
    expect(rateCount(0)).toBe(2)
    i18n.global.locale.value = 'en'
    expect(i18n.global.t('broadcasts.detail.rate_value', { n: '0.5' }, rateCount(0.5))).toBe('0.5 messages a second')
    expect(i18n.global.t('broadcasts.detail.rate_value', { n: '1' }, rateCount(1))).toBe('1 message a second')
  })

  it('writes "1 service on 1 server" from two separately pluralized pieces', () => {
    const line = (services: number, servers: number) =>
      say('en', 'overview.servers_clients', 0, { services: say('en', 'overview.services_n', services), servers: say('en', 'overview.servers_n', servers) })
    expect(line(1, 1)).toBe('1 service on 1 server')
    expect(line(0, 1)).toBe('0 services on 1 server')
    expect(line(1, 2)).toBe('1 service on 2 servers')
    expect(line(19, 3)).toBe('19 services on 3 servers')
  })

  it('names a license limit by the limit: "1 active service", "25 active services"', () => {
    i18n.global.locale.value = 'en'
    expect(i18n.global.t('license.limit_name.active_services', 1)).toBe('active service')
    expect(i18n.global.t('license.limit_name.active_services', 25)).toBe('active services')
  })
})

// A problem's text is picked by a key the bot names (a dynamic key), and its
// numbers come from core as text: alertCount reads the raw one, the
// pluralized part of the message follows it.
describe('English plurals in the open problems', () => {
  const alert = (title_key: string, args: Record<string, string>): SystemAlert => ({ key: 'x.y', severity: 'warning', title_key, args, detail: '', since: 1_790_000_000 })
  const day = Date.UTC(2026, 9, 21, 12) / 1000
  const title = (lang: Lang, a: SystemAlert) => {
    i18n.global.locale.value = lang
    const count = alertCount(a)
    const args = alertArgs(a, lang)
    return count === undefined ? i18n.global.t(alertTextKey(a), args) : i18n.global.t(alertTextKey(a), args, count)
  }

  it('reads the number a problem counts', () => {
    expect(alertCount(alert('alert.backup_stale', { hours: '1' }))).toBe(1)
    expect(alertCount(alert('alert.bot_lag', { minutes: '12' }))).toBe(12)
    expect(alertCount(alert('alert.license_expiring', { days: '0', date: '1' }))).toBe(0)
    expect(alertCount(alert('alert.license_limit', { limit: '25', name: 'active_services' }))).toBe(25)
    expect(alertCount(alert('alert.provision_failed', { count: '3' }))).toBe(3)
    expect(alertCount(alert('alert.disk_low', { free: '1024' }))).toBeUndefined()
    expect(alertCount({ args: null })).toBeUndefined()
    expect(alertCount({})).toBeUndefined()
  })

  it('says "1 hour", "1 minute", "1 day" and "1 payment" in the singular', () => {
    expect(title('en', alert('alert.backup_stale', { hours: '1' }))).toBe('No new backup for 1 hour')
    expect(title('en', alert('alert.backup_stale', { hours: '30' }))).toBe('No new backup for 30 hours')
    expect(title('en', alert('alert.bot_lag', { minutes: '1' }))).toBe('The bot is 1 minute behind on its messages (deliveries, receipts, alerts)')
    expect(title('en', alert('alert.bot_lag', { minutes: '12' }))).toBe('The bot is 12 minutes behind on its messages (deliveries, receipts, alerts)')
    expect(title('en', alert('alert.license_expiring', { days: '1', date: String(day) }))).toBe(`The license expires in 1 day, on ${date(day, 'en')}`)
    expect(title('en', alert('alert.license_expiring', { days: '9', date: String(day) }))).toBe(`The license expires in 9 days, on ${date(day, 'en')}`)
    expect(title('en', alert('alert.tls_expiring', { days: '1', date: String(day), address: 'a.example.com' }))).toBe(
      `The HTTPS certificate of ⁨a.example.com⁩ expires in 1 day, on ${date(day, 'en')}, and has not been renewed`,
    )
    expect(title('en', alert('alert.provision_failed', { count: '1' }))).toBe('1 paid order could not be delivered in the last hour')
    expect(title('en', alert('alert.provision_failed', { count: '4' }))).toBe('4 paid orders could not be delivered in the last hour')
    expect(title('en', alert('alert.zarinpal_stuck', { count: '1' }))).toBe('1 Zarinpal payment is still open; it started over 3 hours ago')
    expect(title('en', alert('alert.zarinpal_stuck', { count: '2' }))).toBe('2 Zarinpal payments are still open; the oldest started over 3 hours ago')
    expect(title('en', alert('alert.aqayepardakht_stuck', { count: '1' }))).toBe('1 Aqayepardakht payment is still open; it started over 3 hours ago')
    expect(title('en', alert('alert.aqayepardakht_stuck', { count: '2' }))).toBe('2 Aqayepardakht payments are still open; the oldest started over 3 hours ago')
    expect(title('en', alert('alert.usdt_stuck', { count: '1' }))).toBe('1 automatic USDT payment is still open; it started over 2 hours ago')
    expect(title('en', alert('alert.usdt_stuck', { count: '5' }))).toBe('5 automatic USDT payments are still open; the oldest started over 2 hours ago')
    expect(title('en', alert('alert.ton_stuck', { count: '1' }))).toBe('1 automatic TON payment is still open; it started over 2 hours ago')
    expect(title('en', alert('alert.ton_stuck', { count: '5' }))).toBe('5 automatic TON payments are still open; the oldest started over 2 hours ago')
    expect(title('en', alert('alert.ton_failing', {}))).toBe('Automatic TON payments are not being checked: the store cannot read its TON address from TON Center')
    expect(title('en', alert('alert.trial_ending', { days: '1', date: String(day) }))).toMatch(/^The free trial ends in 1 day, on /)
    expect(title('en', alert('alert.trial_ending', { days: '2', date: String(day) }))).toMatch(/^The free trial ends in 2 days, on /)
  })

  it('reads the same in Persian for 0, 1 and 2', () => {
    i18n.global.locale.value = 'fa'
    for (const [kind, name] of [['backup_stale', 'hours'], ['bot_lag', 'minutes'], ['license_expiring', 'days'], ['provision_failed', 'count'], ['usdt_stuck', 'count'], ['ton_stuck', 'count']] as const) {
      const a = alert('alert.' + kind, { [name]: '5', date: String(day) })
      const key = alertTextKey(a)
      const args = alertArgs(a, 'fa')
      const text = i18n.global.t(key, args, 1)
      expect(text, kind).toContain('۵')
      expect(text, kind).not.toContain('system.alerts')
      expect(i18n.global.t(key, args, 2), kind).toBe(text)
      expect(i18n.global.t(key, args, 0), kind).toBe(text)
    }
  })
})

describe('Persian counts the same way for every number', () => {
  for (const [key, params] of CASES) {
    it(`${key}: no plural forms, the same words for 0, 1 and 2`, () => {
      const faText = key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown>)[k], fa) as string
      expect(faText, key).toBeTruthy()
      expect(faText, key).not.toContain('|')
      // a third argument to a text without plural forms changes nothing
      i18n.global.locale.value = 'fa'
      const withN = (count?: number) => {
        const named = { n: '۵', ...params }
        return count === undefined ? i18n.global.t(key, named) : i18n.global.t(key, named, count)
      }
      expect(withN(1)).toBe(withN())
      expect(withN(2)).toBe(withN())
      expect(withN(0)).toBe(withN())
    })
  }

  it('keeps the Persian line for services on servers as it was', () => {
    const line = (services: number, servers: number) =>
      say('fa', 'overview.servers_clients', 0, { services: say('fa', 'overview.services_n', services), servers: say('fa', 'overview.servers_n', servers) })
    expect(line(1, 1)).toBe('۱ سرویس روی ۱ سرور')
    expect(line(19, 3)).toBe('۱۹ سرویس روی ۳ سرور')
  })
})

// The guard: how the bug comes back is a new text written as "{n} days", or a
// plural text asked for without the count.
function flat(o: Record<string, unknown>, prefix = ''): [string, string][] {
  return Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v as Record<string, unknown>, prefix + k + '.') : [[prefix + k, String(v)] as [string, string]]))
}
const enTexts = flat(en)
const faTexts = new Map(flat(fa))
const piped = enTexts.filter(([, m]) => m.includes('|'))

describe('English texts that count something', () => {
  // A number next to a plain word, in a text with no plural forms. These
  // placeholders are the ones that hold a number of things.
  const COUNTED = /\{(?:n|count|days|hours|minutes|seconds|limit|every|done|total|max)\}\s+[A-Za-z]/

  // Texts where the word after the number is not a noun that can be singular,
  // or the number is never 1.
  const NOT_A_COUNT: Record<string, string> = {
    'placeholder.coming': '"milestone {n} of Phase 4": the number names a milestone, it counts nothing',
    'overview.servers_down': '"{n} of {total} not answering": "not" is not a counted noun',
    'service.bulk.progress': '"Working: {done} of {total}": a position in the run, "of" is not a counted noun',
    'texts.changed_count': '"{n} changed": a verb, there is no noun to pluralize',
    'reseller.discount_short': '"{n} off": a percentage, "off" is not a counted noun',
    'reseller.commission_short': '"{n} commission": a percentage, "commission" is not counted',
    'reseller.discount_off': '"{n} off store prices": a percentage',
    'usdtrate.per_usdt': '"{n} Toman per USDT": Toman is the currency, not a counted noun',
    'usdtrate.live_line': '"{n} Toman": the currency, not a counted noun',
    'gateway.open_count': '"{n} open": an adjective, the noun is left out',
    'gateway.check_errors.more': '"The newest {shown} of {total} are shown": "are" is a verb, the noun is left out',
    'gateway.usdt.unmatched_more': '"The newest {shown} of {n} are listed": "are" is a verb, the noun is left out',
    'system.https.wait.detail': 'seconds: the page reads every 3 s for up to 90 s (httpsPoll in https.ts), constants that are never 1',
  }

  it('has a plural form for every number next to a noun', () => {
    const bad = enTexts.filter(([k, m]) => !m.includes('|') && COUNTED.test(m) && !(k in NOT_A_COUNT)).map(([k, m]) => `${k}: ${m}`)
    expect(
      bad,
      'English texts that put a number next to a word without plural forms. Write them "{n} day | {n} days" and pass the count as the third argument of t(): t(key, { n: num(x, lang) }, x). A text where the word is not a counted noun goes in NOT_A_COUNT with the reason.',
    ).toEqual([])
  })

  it('keeps NOT_A_COUNT to texts that still need it', () => {
    const all = new Map(enTexts)
    for (const key of Object.keys(NOT_A_COUNT)) {
      const m = all.get(key)
      expect(m, `${key} is not in en.json: remove it from NOT_A_COUNT`).toBeDefined()
      expect(m, `${key} has plural forms now: remove it from NOT_A_COUNT`).not.toContain('|')
      expect(COUNTED.test(m!), `${key} no longer has a number next to a word: remove it from NOT_A_COUNT`).toBe(true)
    }
  })

  it('writes every plural text with two forms that name the same values', () => {
    expect(piped.length).toBeGreaterThan(35)
    const holes = (m: string) => [...new Set([...m.matchAll(/\{(\w+)\}/g)].map((x) => x[1]))].sort()
    for (const [key, message] of piped) {
      const forms = message.split('|').map((f) => f.trim())
      expect(forms, key).toHaveLength(2)
      expect(forms[0], key).not.toBe(forms[1])
      expect(holes(forms[0]), key).toEqual(holes(forms[1]))
      // and Persian has the key, with no forms of its own
      expect(faTexts.has(key), `${key} is missing from fa.json`).toBe(true)
      expect(faTexts.get(key), `${key} must not have plural forms in fa.json`).not.toContain('|')
    }
  })

  // The pages ask for a plural text with the count as the third argument:
  // without it vue-i18n takes the singular for every number ("5 day left").
  // This reads the calls whose key is written out; the keys built from a
  // variable (license.chip.*, license.left.*, the problems' texts) are read by
  // the pages' own tests below.
  const sources = import.meta.glob('../**/*.{vue,ts}', { query: '?raw', import: 'default', eager: true }) as Record<string, string>

  /** argCount counts the arguments of the call whose "(" is at open. */
  function argCount(src: string, open: number): number {
    let depth = 0
    let args = 0
    let content = false
    for (let i = open + 1; i < src.length; i++) {
      const c = src[i]
      if (c === "'" || c === '"' || c === '`') {
        for (i++; i < src.length && src[i] !== c; i++) if (src[i] === '\\') i++
        content = true
      } else if ('([{'.includes(c)) {
        depth++
        content = true
      } else if (')]}'.includes(c)) {
        if (depth === 0) return content ? args + 1 : args
        depth--
      } else if (c === ',' && depth === 0) {
        if (content) args++
        content = false
      } else if (!/\s/.test(c)) {
        content = true
      }
    }
    return args
  }

  /** callsWithout lists the calls of the given keys that have fewer than three arguments. */
  function callsWithout(files: Record<string, string>, keys: Set<string>): string[] {
    const out: string[] = []
    for (const [file, src] of Object.entries(files)) {
      if (file.includes('/__tests__/') || file.endsWith('.d.ts')) continue
      for (const m of src.matchAll(/(?<![\w$.])(?:\$t|t)\(\s*(['"`])([\w.-]+)\1\s*[,)]/g)) {
        if (keys.has(m[2]) && argCount(src, m.index + m[0].indexOf('(')) < 3) out.push(`${m[2]} (${file})`)
      }
    }
    return out
  }

  it('is asked for with the count as the third argument', () => {
    const keys = new Set(piped.map(([k]) => k))
    // the scan reads what it claims to (the sample calls are put together so that the
    // key scan of the sources does not take them for calls)
    const call = (rest: string) => ['t', rest].join('(')
    expect(argCount(call('"a.b", { n: f(1, 2) }, 3)'), 1)).toBe(3)
    expect(argCount(call("'a.b', { n: x })"), 1)).toBe(2)
    expect(argCount(call("'a.b', { n: x }, y,)"), 1)).toBe(3)
    expect(argCount(call("`a.${b}`, { n: 'a,b' })"), 1)).toBe(2)
    expect(callsWithout({ '../x.vue': '{{ ' + call("'a.b', { n }) }} {{ ") + call("'a.c', { n }, n) }}") }, new Set(['a.b', 'a.c']))).toEqual(['a.b (../x.vue)'])
    // and finds the calls of the real plural texts
    const seen = Object.entries(sources).reduce(
      (n, [file, src]) => (file.includes('/__tests__/') ? n : n + [...src.matchAll(/(?<![\w$.])(?:\$t|t)\(\s*(['"`])([\w.-]+)\1\s*[,)]/g)].filter((m) => keys.has(m[2])).length),
      0,
    )
    expect(seen).toBeGreaterThan(25)
    expect(callsWithout(sources, keys)).toEqual([])
  })
})

// The real pages, booted in English with a fake core, give the number to the
// texts they build from a variable.
describe('English pages', () => {
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const nowSec = () => Math.floor(Date.now() / 1000)
  type Answer = (url: string) => Response | undefined
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()

  async function boot(path: string, perms: string[], answer: Answer, license?: Record<string, unknown>) {
    unmount()
    try {
      localStorage.setItem('bobres.lang', 'en')
    } catch {
      /* the page falls back to Persian and the test says so */
    }
    vi.resetModules()
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        const own = answer(url)
        if (own) return own
        if (url === '/api/v1/me') {
          return json(200, {
            user: { id: 'u1', telegram_id: 1, username: 'boss', role: 'admin', language: 'en' }, permissions: perms, csrf: 'c',
            session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
            ...(license ? { license } : {}),
          })
        }
        if (url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null })
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
  }

  afterEach(() => {
    unmount()
    try {
      localStorage.removeItem('bobres.lang')
    } catch {
      /* nothing was stored */
    }
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  const found = async <T extends Element>(selector: string) =>
    vi.waitFor(() => {
      const el = document.querySelector<T>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })

  const overview = (extra: Record<string, unknown>) => ({
    users: { total: 10, new_24h: 1, new_7d: 2 }, services: { active: 5, expiring: 1, ended: 0 }, orders: { paid_24h: 1, provision_failed: 0 },
    revenue: { day: [], month: [] }, pending_payments: 0, panel: { healthy: true, detail: '' }, recent_orders: [],
    tickets: { waiting: 0, oldest_waiting_at: null }, ...extra,
  })
  const server = (id: string, clients: number) => ({ id, name: 'srv-' + id, healthy: true, detail: '', clients, enabled: true })

  it('says "1 service on 1 server" and "1 reseller" on the overview, then the plurals', async () => {
    const perms = ['overview.read', 'servers.write', 'resellers.write']
    await boot('/admin/', perms, (url) =>
      url === '/api/v1/overview' ? json(200, overview({ servers: [server('a', 1)], resellers: { count: 1, debt: [] } })) : undefined,
    )
    const card = await found<HTMLElement>('.servers-card')
    expect(document.documentElement.lang).toBe('en')
    expect(card.textContent).toContain('1 service on 1 server')
    expect(card.textContent).not.toMatch(/1 services|1 servers/)
    expect(document.querySelector('.resellers-card')?.textContent).toContain('1 reseller')
    expect(document.querySelector('.resellers-card')?.textContent).not.toContain('1 resellers')

    await boot('/admin/', perms, (url) =>
      url === '/api/v1/overview' ? json(200, overview({ servers: [server('a', 5), server('b', 7)], resellers: { count: 2, debt: [] } })) : undefined,
    )
    await vi.waitFor(() => expect(document.querySelector('.servers-card')?.textContent).toContain('12 services on 2 servers'))
    expect(document.querySelector('.resellers-card')?.textContent).toContain('2 resellers')
  })

  it('says "1 day left", "1 hour", "1 minute" and "1 active service" on the system page', async () => {
    const day = nowSec() + 7 * 86_400
    const alerts: SystemAlert[] = [
      { key: 'backup.stale', severity: 'warning', title_key: 'alert.backup_stale', args: { hours: '1' }, detail: '', since: 10 },
      { key: 'bot.lag', severity: 'warning', title_key: 'alert.bot_lag', args: { minutes: '1' }, detail: '', since: 20 },
      { key: 'provision.failed', severity: 'critical', title_key: 'alert.provision_failed', args: { count: '2' }, detail: '', since: 30 },
      { key: 'license.expiring', severity: 'warning', title_key: 'alert.license_expiring', args: { days: '1', date: String(day) }, detail: '', since: 40 },
      { key: 'license.limit', severity: 'warning', title_key: 'alert.license_limit', args: { limit: '1', name: 'active_services' }, detail: '', since: 50 },
      { key: 'license.limit2', severity: 'warning', title_key: 'alert.license_limit', args: { limit: '25', name: 'active_services' }, detail: '', since: 60 },
      { key: 'usdt.stuck', severity: 'warning', title_key: 'alert.usdt_stuck', args: { count: '1' }, detail: '', since: 70 },
    ]
    const reply = {
      version: 'v1.4.0', version_mismatch: false, checked_at: nowSec(), services: [], servers: [], gateways: [], alerts,
      updates: { current: '1.4.0', latest: '1.4.0', newer: false, verified: true, checked_at: nowSec(), error: '' },
    }
    await boot(
      '/admin/system',
      ['overview.read', 'system.read'],
      (url) => (url === '/api/v1/system' ? json(200, reply) : undefined),
      { state: 'missing', white_label: false, powered_by: true, basis: 'trial', tier: 'trial', trial_days_left: 1 },
    )
    await vi.waitFor(() => expect(document.querySelectorAll('.alerts .alert')).toHaveLength(7))
    const titles = [...document.querySelectorAll('.alerts .alert-title')].map((e) => e.textContent).sort()
    expect(titles).toEqual(
      [
        'No new backup for 1 hour',
        'The bot is 1 minute behind on its messages (deliveries, receipts, alerts)',
        '2 paid orders could not be delivered in the last hour',
        `The license expires in 1 day, on ${date(day, 'en')}`,
        'A new purchase was refused: this install is at its limit of 1 ⁨active service⁩. Buy or renew a license to sell more',
        'A new purchase was refused: this install is at its limit of 25 ⁨active services⁩. Buy or renew a license to sell more',
        '1 automatic USDT payment is still open; it started over 2 hours ago',
      ].sort(),
    )
    // the problems' count in the card's title, and the menu's trial chip
    expect(document.querySelector('.alerts-count')?.getAttribute('aria-label')).toBe('Open problems: 7')
    expect(document.querySelector('.license-chip')?.textContent).toBe('Trial: 1 day left')
    expect(document.querySelector('.license-chip')?.getAttribute('aria-label')).toBe('License: Trial: 1 day left')
  })
})
