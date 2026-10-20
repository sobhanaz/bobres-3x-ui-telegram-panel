import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import {
  canonical,
  changed,
  changes,
  describeCheck,
  effective,
  example,
  ltr,
  maxLength,
  normalize,
  normalizeChannel,
  normalizeTime,
  problems,
  rebase,
  serverField,
  timeZones,
  unreachable,
  validate,
  wideGroups,
  zoneNow,
  zoneOptions,
} from '../settings'
import type { JoinChatRow, JoinChatsReply, SettingItem, SettingsReply } from '../types'

function item(key: string, kind: SettingItem['kind'], extra: Partial<SettingItem> = {}): SettingItem {
  return { key, group: key.split('.')[0], kind, value: '', default: '', options: [], min: null, max: null, editable: true, ...extra }
}

const channel = item('join.channel', 'channel')
const link = item('join.link', 'url')
const zarinpal = item('payments.zarinpal_link', 'url')
const perTen = item('limits.bot_per_10s', 'int', { default: '20', min: 5, max: 200 })
const usdtRate = item('payments.usdt_rate', 'int', { min: 1, max: 1e12 })
const card = item('payments.card_number', 'text')
const holder = item('payments.card_holder', 'text')
const trc = item('payments.usdt_trc20', 'text')
const erc = item('payments.usdt_erc20', 'text')
const tz = item('general.timezone', 'tz')
const recipients = item('notify.recipients', 'enum', { default: 'owner', options: ['owner', 'staff'] })
const sales = item('notify.sales', 'bool', { default: 'false' })
const summaryOn = item('reports.daily_summary', 'bool', { default: 'false' })
const summaryTime = item('reports.daily_summary_time', 'time', { default: '09:00' })

const check = (i: SettingItem, raw: string, others: Record<string, string> = {}) => validate(i, normalize(i, raw), others)?.code ?? null

describe('channel input', () => {
  it('accepts what staff paste', () => {
    expect(normalizeChannel('mychannel')).toBe('@mychannel')
    expect(normalizeChannel(' @mychannel ')).toBe('@mychannel')
    expect(normalizeChannel('https://t.me/mychannel')).toBe('@mychannel')
    expect(normalizeChannel('t.me/mychannel/')).toBe('@mychannel')
    expect(normalizeChannel('-۱۰۰۱۲۳۴۵۶۷۸۹۰')).toBe('-1001234567890')
    expect(normalizeChannel('https://t.me/+AbCdEf')).toBe('https://t.me/+AbCdEf')
  })
  it('checks the @name and -100 id rules', () => {
    expect(check(channel, '@abcde')).toBeNull()
    expect(check(channel, '-1001234567890')).toBeNull()
    expect(check(channel, '@abcd')).toBe('channel')
    expect(check(channel, '@1abcde')).toBe('channel')
    expect(check(channel, '-123456')).toBe('channel')
    expect(check(channel, 'https://t.me/+AbCdEf')).toBe('channel')
    expect(check(channel, '')).toBeNull()
  })
})

describe('links', () => {
  it('wants a t.me link for the join button, required for a private channel', () => {
    expect(check(link, 'https://t.me/+AbCdEf')).toBeNull()
    expect(check(link, 'http://t.me/mychannel')).toBe('tme_link')
    expect(check(link, 'https://example.com/x')).toBe('tme_link')
    expect(check(link, '', { 'join.channel': '-1001234567890' })).toBe('link_required')
    expect(check(link, '', { 'join.channel': '@mychannel' })).toBeNull()
  })
  it('wants https with a host and no user for Zarinpal', () => {
    expect(check(zarinpal, 'https://zarinp.al/shop')).toBeNull()
    expect(check(zarinpal, 'http://zarinp.al/shop')).toBe('https')
    expect(check(zarinpal, 'https://me:pw@zarinp.al/shop')).toBe('https')
    expect(check(zarinpal, 'zarinp.al/shop')).toBe('https')
    expect(check(zarinpal, 'https://zarinp.al/a b')).toBe('https')
  })
  it('suggests the public channel link', () => {
    expect(example(link, '@mychannel')).toBe('https://t.me/mychannel')
    expect(example(link, '-1001234567890')).toBe('https://t.me/+…')
    expect(example(perTen)).toBe('20')
  })
})

describe('payment details', () => {
  it('takes a card number or sheba in any digits, with spaces and dashes', () => {
    expect(check(card, '6037-9911 2345 6789')).toBeNull()
    expect(check(card, '۶۰۳۷۹۹۱۱۲۳۴۵۶۷۸۹')).toBeNull()
    expect(normalize(card, '۶۰۳۷ ۹۹۱۱')).toBe('6037 9911')
    expect(check(card, 'ir' + '1'.repeat(24))).toBeNull()
    expect(normalize(card, 'ir' + '1'.repeat(24))).toBe('IR' + '1'.repeat(24))
    expect(check(card, '6037 9911 2345 678')).toBe('card')
    expect(check(card, '6037.9911.2345.6789')).toBe('card')
  })
  it('checks USDT addresses', () => {
    expect(check(trc, 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t')).toBeNull()
    expect(check(trc, 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6')).toBe('trc20')
    expect(check(trc, 'T07NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t')).toBe('trc20')
    expect(check(erc, '0x' + 'aB3'.repeat(13) + 'f')).toBeNull()
    expect(check(erc, '0x' + 'g'.repeat(40))).toBe('erc20')
  })
  it('limits the card holder to one line of 64 characters', () => {
    expect(check(holder, 'علی رضایی')).toBeNull()
    expect(check(holder, 'ع'.repeat(64))).toBeNull()
    expect(validate(holder, 'ع'.repeat(65))).toEqual({ code: 'too_long', params: { max: 64 } })
    expect(check(holder, 'a\tb')).toBe('one_line')
  })
})

describe('numbers and choices', () => {
  it('reads Persian digits and checks the range', () => {
    expect(normalize(perTen, '۳۰')).toBe('30')
    expect(check(perTen, '۳۰')).toBeNull()
    expect(validate(perTen, '4')).toEqual({ code: 'range', params: { min: 5, max: 200 } })
    expect(check(perTen, '1.5')).toBe('int')
    expect(check(perTen, '-5')).toBe('int')
    expect(check(usdtRate, '0')).toBe('range')
    expect(check(usdtRate, '95,000')).toBeNull()
    expect(validate(item('x.y', 'int', { min: 1 }), '0')).toEqual({ code: 'min', params: { min: 1 } })
  })
  it('checks time zones, options and switches', () => {
    expect(check(tz, 'Asia/Tehran')).toBeNull()
    expect(check(tz, 'America/Argentina/Buenos_Aires')).toBeNull()
    expect(check(tz, 'Etc/GMT+5')).toBeNull()
    expect(check(tz, 'Local')).toBe('timezone')
    expect(check(tz, '')).toBeNull()
    expect(check(recipients, 'staff')).toBeNull()
    expect(check(recipients, 'everyone')).toBe('option')
    expect(check(sales, 'true')).toBeNull()
    expect(check(sales, 'yes')).toBe('option')
  })
})

describe('the daily summary settings', () => {
  it('reads a time of day in any digits and writes it as HH:MM', () => {
    expect(normalizeTime('9:30')).toBe('09:30')
    expect(normalizeTime(' 09:30 ')).toBe('09:30')
    expect(normalizeTime('۹:۳۰')).toBe('09:30')
    expect(normalizeTime('٢١:٠٥')).toBe('21:05')
    expect(normalizeTime('soon')).toBe('soon') // left for validate to refuse
    expect(normalize(summaryTime, '7:05')).toBe('07:05')
  })
  it('accepts only a 24-hour time of day', () => {
    for (const ok of ['00:00', '09:00', '9:00', '12:30', '23:59', '۰۹:۳۰']) expect(check(summaryTime, ok), ok).toBeNull()
    for (const bad of ['24:00', '9', '09:60', '9:5', '09-00', '0900', '09:00:00', '12:30 pm', 'abc', '-1:00']) {
      expect(check(summaryTime, bad), bad).toBe('time')
    }
    expect(check(summaryTime, '')).toBeNull() // back to the default, 09:00
  })
  it('is off and at 09:00 until changed, and sends only what changed', () => {
    expect(effective(summaryOn)).toBe('false')
    expect(effective(summaryTime)).toBe('09:00')
    expect(changed(summaryTime, '9:00')).toBe(false)
    expect(changed(summaryTime, '09:01')).toBe(true)
    expect(changes([summaryOn, summaryTime], { 'reports.daily_summary': 'true', 'reports.daily_summary_time': '۹:۳۰' })).toEqual({
      'reports.daily_summary': 'true',
      'reports.daily_summary_time': '09:30',
    })
    expect(problems([summaryTime], { 'reports.daily_summary_time': '25:00' })).toEqual({ 'reports.daily_summary_time': { code: 'time' } })
    expect(example(summaryTime)).toBe('09:00')
  })
  it('shows a time left to right, in a box that fits it', () => {
    expect(ltr(summaryTime)).toBe(true)
    expect(maxLength(summaryTime)).toBe(5)
  })
  it('has its texts in both languages', () => {
    const get = (o: unknown, path: string) => path.split('.').reduce<unknown>((x, k) => (x as Record<string, unknown> | undefined)?.[k], o)
    const keys = [
      'settings.group.reports.title',
      'settings.group.reports.desc',
      'settings.field.reports.daily_summary.label',
      'settings.field.reports.daily_summary.help',
      'settings.field.reports.daily_summary_time.label',
      'settings.field.reports.daily_summary_time.help',
      'settings.invalid.time',
      'reports.daily.on',
      'reports.daily.off',
      'reports.daily.link',
    ]
    for (const [lang, catalog] of [['en', en], ['fa', fa]] as const) {
      for (const k of keys) {
        const v = get(catalog, k)
        expect(typeof v === 'string' && v.trim() !== '', `${lang} ${k}`).toBe(true)
      }
      // the one sentence with values names them in both languages
      expect(get(catalog, 'reports.daily.on') as string).toContain('{time}')
      expect(get(catalog, 'reports.daily.on') as string).toContain('{tz}')
    }
  })
})

describe('drafts', () => {
  it('compares what the bot would use', () => {
    expect(effective(perTen)).toBe('20')
    expect(changed(perTen, '20')).toBe(false)
    expect(changed(perTen, '۲۰')).toBe(false)
    expect(changed(perTen, '')).toBe(false)
    expect(changed(perTen, '25')).toBe(true)
    const stored = { ...perTen, value: '30' }
    expect(canonical(stored, '')).toBe('20')
    expect(changed(stored, '')).toBe(true)
    expect(changes([stored], { [stored.key]: '' })).toEqual({ 'limits.bot_per_10s': '' })
  })
  it('sends only changed, editable keys, normalized', () => {
    const locked = { ...card, editable: false }
    const items = [perTen, channel, locked, sales]
    const drafts = { 'limits.bot_per_10s': '۳۰', 'join.channel': 'https://t.me/mychannel', 'payments.card_number': '1', 'notify.sales': 'false' }
    expect(changes(items, drafts)).toEqual({ 'limits.bot_per_10s': '30', 'join.channel': '@mychannel' })
  })
  it('checks only what a save sends, and keys that depend on it', () => {
    const legacy = { ...card, value: 'Mellat 6037' }
    expect(problems([legacy, holder], { 'payments.card_number': 'Mellat 6037', 'payments.card_holder': 'Ali' })).toEqual({})
    expect(problems([legacy], { 'payments.card_number': 'Mellat 60379' })).toEqual({ 'payments.card_number': { code: 'card' } })
    expect(problems([channel, link], { 'join.channel': '-1001234567890', 'join.link': '' })).toEqual({ 'join.link': { code: 'link_required' } })
    expect(problems([channel, link], { 'join.channel': '@mychannel', 'join.link': '' })).toEqual({})
  })
  it('keeps edits when fresh settings arrive, unless just saved', () => {
    const prev = [perTen, sales]
    const next = [{ ...perTen, value: '40' }, { ...sales, value: 'true' }]
    expect(rebase({}, [], prev)).toEqual({ 'limits.bot_per_10s': '20', 'notify.sales': 'false' })
    // an untouched draft follows the new value; an edit is kept
    expect(rebase({ 'limits.bot_per_10s': '20', 'notify.sales': 'true' }, prev, [perTen, { ...sales, value: 'true' }])).toEqual({
      'limits.bot_per_10s': '20',
      'notify.sales': 'true',
    })
    expect(rebase({ 'limits.bot_per_10s': '۴۰', 'notify.sales': 'false' }, prev, next, ['limits.bot_per_10s'])).toEqual({
      'limits.bot_per_10s': '40',
      'notify.sales': 'true',
    })
    expect(rebase({ 'limits.bot_per_10s': '50' }, prev, next)).toEqual({ 'limits.bot_per_10s': '50', 'notify.sales': 'true' })
  })
})

describe('answers from core', () => {
  it('finds the key a 400 names', () => {
    expect(serverField(new ApiError(400, 'invalid', 'join.link is required', { field: 'join.link' }))).toBe('join.link')
    expect(serverField(new ApiError(400, 'invalid', 'bad'))).toBe('')
    expect(serverField(new ApiError(403, 'forbidden', 'no', { field: 'payments.card_number' }))).toBe('')
    expect(serverField(new Error('x'))).toBe('')
  })
  it('knows when the bot could not be reached', () => {
    expect(unreachable(new ApiError(503, 'unavailable', 'bot'))).toBe(true)
    expect(unreachable(new ApiError(400, 'invalid', 'x'))).toBe(false)
  })
  it('reads the channel check', () => {
    expect(describeCheck({ ok: true, title: 'News', bot_admin: true })).toEqual({ severity: 'success', key: 'settings.check.ok', title: 'News', detail: '' })
    expect(describeCheck({ ok: true, title: 'News', bot_admin: false }).key).toBe('settings.check.ok_not_admin')
    expect(describeCheck({ ok: false, problem: 'not_found' })).toEqual({
      severity: 'error',
      key: 'settings.check.problem.not_found',
      title: '',
      detail: '',
    })
    expect(describeCheck({ ok: false, problem: 'not_admin' }).severity).toBe('warn')
    expect(describeCheck({ ok: false, problem: 'error', detail: 'Bad Request: chat not found' }).detail).toBe('Bad Request: chat not found')
    expect(describeCheck({ ok: false, problem: 'weird' as 'error' }).key).toBe('settings.check.problem.error')
  })
})

describe('page layout and time zones', () => {
  it('widens big cards and the odd one out', () => {
    const groups = [
      { group: 'general', count: 1 },
      { group: 'maintenance', count: 1 },
      { group: 'join', count: 2 },
      { group: 'notify', count: 5 },
      { group: 'limits', count: 2 },
      { group: 'payments', count: 7 },
    ]
    expect(wideGroups(groups)).toEqual(['limits', 'payments'])
    expect(wideGroups(groups.slice(0, 4))).toEqual([])
    expect(wideGroups([{ group: 'payments', count: 7 }, { group: 'general', count: 1 }])).toEqual(['payments', 'general'])
  })
  it('lists zones and shows the time in one', () => {
    expect(timeZones()).toContain('UTC')
    expect(timeZones()).toContain('Asia/Tehran')
    expect(zoneOptions('Mars/Olympus')[0]).toBe('Mars/Olympus')
    expect(zoneOptions('Asia/Tehran')).toBe(timeZones())
    expect(zoneNow('Asia/Tehran', new Date(Date.UTC(2026, 0, 1, 10, 0)))).toBe('13:30 · GMT+3:30')
    expect(zoneNow('')).toBe('')
    expect(zoneNow('Not/AZone')).toBe('')
  })
})

// The page itself, started the way a browser does, against a fake core.
describe('settings page', () => {
  const me = {
    user: { id: 'u1', telegram_id: 1001, username: 'admin', role: 'admin', language: 'fa' },
    permissions: ['overview.read', 'settings.write'],
    csrf: 'csrf-1',
    session: { method: 'link', expires_at: 2_000_000_000 },
    password: { enabled: false },
    password_available: true,
    brand: 'BOBRES',
  }
  // The payment details moved to the Payment gateways page (M6): core no
  // longer lists them here; an older core still may.
  const pay = (key: string, kind: SettingItem['kind'], extra: Partial<SettingItem> = {}) => item(key, kind, { editable: false, ...extra })
  const payments: SettingItem[] = [
    pay('payments.card_number', 'text', { value: '6037 9911 2345 6789' }),
    pay('payments.card_holder', 'text'),
    pay('payments.usdt_rate', 'int', { min: 1, max: 1e12 }),
    pay('payments.zarinpal_link', 'url'),
  ]
  const start: SettingItem[] = [
    tz,
    item('maintenance.enabled', 'bool', { default: 'false' }),
    channel,
    link,
    item('notify.new_payment', 'bool', { default: 'true' }),
    item('notify.new_ticket', 'bool', { default: 'true' }),
    item('notify.provision_failed', 'bool', { default: 'true' }),
    item('notify.system_alerts', 'bool', { default: 'true' }),
    sales,
    recipients,
    perTen,
    // a setting this role may not change
    item('limits.tickets_per_day', 'int', { default: '10', min: 0, max: 100, value: '5', editable: false }),
  ]
  const groups = ['general', 'maintenance', 'join', 'notify', 'limits']
  const text = (path: string) => path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa) ?? path
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

  // The required channels and groups have a card of their own (JoinChatsCard), loaded from /join-chats.
  const joinNone: JoinChatsReply = { chats: [], on: false, limit: -1, used: 0, max: 50, basis: 'trial', tier: 'trial', editable: true }
  const joinRow = (chat: string, extra: Partial<JoinChatRow> = {}): JoinChatRow => ({ chat, link: '', label: '', primary: false, active: true, ...extra })
  let joinPuts: { chats: { chat: string; link: string; label: string }[] }[] = []
  let joinPut: (body: { chats: { chat: string; link: string; label: string }[] }) => Response = () => json(500, { error: 'internal', message: 'no' })

  async function open(
    put: (values: Record<string, string>) => Response,
    opts: { permissions?: string[]; items?: SettingItem[]; groups?: string[]; cards?: number; hash?: string; join?: Partial<JoinChatsReply> } = {},
  ) {
    const items = opts.items ?? start
    joinPuts = []
    const join: JoinChatsReply = { ...joinNone, ...opts.join }
    const listed = opts.groups ?? groups
    vi.resetModules()
    vi.spyOn(console, 'warn').mockImplementation(() => {}) // texts not merged into the catalogs yet
    window.history.replaceState(null, '', '/admin/settings' + (opts.hash ?? ''))
    document.body.innerHTML = '<div id="app"></div>'
    // jsdom has no matchMedia; PrimeVue's Select asks it about orientation
    vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const puts: Record<string, string>[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        if (url === '/api/v1/me') return json(200, opts.permissions ? { ...me, permissions: opts.permissions } : me)
        if (url === '/api/v1/settings' && method === 'GET') return json(200, { items, groups: listed } satisfies SettingsReply)
        if (url === '/api/v1/settings' && method === 'PUT') {
          const { values } = JSON.parse(String(init?.body)) as { values: Record<string, string> }
          puts.push(values)
          return put(values)
        }
        if (url === '/api/v1/join-chats' && method === 'GET') return json(200, join)
        if (url === '/api/v1/join-chats' && method === 'PUT') {
          const body = JSON.parse(String(init?.body)) as (typeof joinPuts)[number]
          joinPuts.push(body)
          return joinPut(body)
        }
        if (url === '/api/v1/settings/channel-check') return json(503, { error: 'unavailable', message: 'bot down' })
        return json(404, { error: 'not_found', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    await vi.waitFor(() => expect(document.querySelectorAll('.settings-card')).toHaveLength(opts.cards ?? 5))
    return puts
  }
  const saved = (values: Record<string, string>) =>
    json(200, { items: start.map((i) => (i.key in values ? { ...i, value: values[i.key] } : i)), groups } satisfies SettingsReply)
  const input = (key: string) => document.getElementById('setting-' + key.replace(/[^A-Za-z0-9]/g, '-')) as HTMLInputElement
  const card = (key: string) => input(key).closest('.settings-card') as HTMLElement
  const saveButton = (key: string) => card(key).querySelector('button[type=submit]') as HTMLButtonElement
  function type(key: string, value: string) {
    input(key).value = value
    input(key).dispatchEvent(new Event('input'))
  }
  // The required channels and groups card: its rows, their inputs and its buttons.
  const joinCard = () => document.querySelector('.join-card') as HTMLElement
  const joinSave = () => joinCard().querySelector('button[type=submit]') as HTMLButtonElement
  const joinRows = () => [...joinCard().querySelectorAll('li.row')] as HTMLElement[]
  const joinInput = (row: number, field: 'chat' | 'link' | 'label') => joinRows()[row].querySelector(`input[id$="-${field}"]`) as HTMLInputElement
  const joinType = (row: number, field: 'chat' | 'link' | 'label', value: string) => {
    joinInput(row, field).value = value
    joinInput(row, field).dispatchEvent(new Event('input'))
  }
  const joinButton = (label: string) => [...joinCard().querySelectorAll('button')].find((b) => (b.textContent ?? '').includes(label) || b.getAttribute('aria-label') === label) as HTMLButtonElement
  const joinLoaded = (rows: number) => vi.waitFor(() => expect(joinRows()).toHaveLength(rows))

  afterEach(() => {
    // stop this test's app: Vue and PrimeVue are shared between tests
    ;(document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  it('saves only what changed in one card, and locks what the role may not change', async () => {
    const puts = await open(saved)
    expect(input('limits.bot_per_10s').value).toBe('20')
    expect(saveButton('limits.bot_per_10s').disabled).toBe(true)
    type('limits.bot_per_10s', '۳۰')
    await vi.waitFor(() => expect(saveButton('limits.bot_per_10s').disabled).toBe(false))
    expect(joinSave().disabled).toBe(true)
    saveButton('limits.bot_per_10s').click()
    await vi.waitFor(() => expect(puts).toEqual([{ 'limits.bot_per_10s': '30' }]))
    await vi.waitFor(() => expect(saveButton('limits.bot_per_10s').disabled).toBe(true))
    expect(input('limits.bot_per_10s').value).toBe('30')
    // a setting the role may not change: a lock, read only, the value still shown, never sent
    expect(card('limits.tickets_per_day').querySelector('.pi-lock')).not.toBeNull()
    expect(input('limits.tickets_per_day').readOnly).toBe(true)
    expect(input('limits.tickets_per_day').value).toBe('5')
    expect(joinCard().querySelector('.pi-lock')).toBeNull()
    // an admin is not sent to the Gateways page
    expect(document.querySelector('a.payments-moved')).toBeNull()
  })

  it('leaves the payment details to the Payment gateways page', async () => {
    // An older core still lists the payments group: it is not shown here.
    await open(saved, { items: [...start, ...payments], groups: [...groups, 'payments'], permissions: ['overview.read', 'settings.write', 'gateways.write'] })
    expect(input('payments.card_number')).toBeNull()
    expect(document.body.textContent).not.toContain(text('settings.group.payments.title'))
    // the owner gets a link there
    const moved = document.querySelector('a.payments-moved') as HTMLAnchorElement
    expect(moved.getAttribute('href')).toBe('/admin/gateways')
    expect(document.querySelector('.intro')?.textContent).toContain(text('settings.payments_moved'))
  })

  it('shows core’s reason under the field it names, and a bot that cannot be reached', async () => {
    await open(saved, { join: { chats: [joinRow('@first_news', { primary: true })], on: true, used: 1 } })
    await joinLoaded(1)
    joinPut = () => json(400, { error: 'invalid', message: 'chats.0.link: not an invite link', field: 'chats.0.link' })
    joinType(0, 'chat', '-1001234567890')
    joinType(0, 'link', 'https://t.me/+AbCdEf')
    await vi.waitFor(() => expect(joinSave().disabled).toBe(false))
    joinSave().click()
    await vi.waitFor(() => expect(joinInput(0, 'link').closest('.field')?.textContent).toContain('chats.0.link: not an invite link'))
    const check = joinRows()[0].querySelector('.control button') as HTMLButtonElement
    expect(check.disabled).toBe(false)
    check.click()
    await vi.waitFor(() => expect(joinRows()[0].textContent).toContain(text('settings.check.unreachable')))
  })

  it('lists the required chats, counts them against the limit and marks the ones the plan does not count', async () => {
    await open(saved, {
      join: { chats: [joinRow('@first_news', { primary: true, label: 'News' }), joinRow('-1001234567890', { link: 'https://t.me/+AbC', active: false }), joinRow('@third_group', { active: false })], on: true, limit: 1, used: 3, basis: 'free', tier: 'free' },
    })
    await joinLoaded(3)
    expect(joinInput(0, 'chat').value).toBe('@first_news')
    expect(joinInput(0, 'label').value).toBe('News')
    expect(joinInput(1, 'link').value).toBe('https://t.me/+AbC')
    expect(joinInput(0, 'chat').dir).toBe('ltr')
    // "3 of 1": more are saved than the plan counts
    expect(joinCard().querySelector('.count')?.textContent).toBe(String(text('settings.join.count')).replace('{used}', '۳').replace('{limit}', '۱'))
    expect(joinCard().querySelector('.count')?.classList.contains('is-over')).toBe(true)
    expect(joinCard().textContent).toContain(text('settings.join.inactive'))
    expect(joinRows().map((r) => r.classList.contains('inactive'))).toEqual([false, true, true])
    // nothing can be added past the limit, and the plan to upgrade to is named
    const add = joinButton(String(text('settings.join.add')))
    expect(add.disabled).toBe(true)
    expect(joinCard().querySelector('.limit-note a')?.getAttribute('href')).toContain('license')
    // the saved ones can still be removed
    joinRows()[2].querySelector<HTMLButtonElement>('.pi-trash')?.closest('button')?.click()
    await joinLoaded(2)
    expect(joinSave().disabled).toBe(false)
  })

  it('adds, reorders and removes rows and saves the whole list, the first row staying first', async () => {
    await open(saved, { join: { chats: [joinRow('@first_news', { primary: true }), joinRow('@second_chan'), joinRow('@third_chan')], on: true, used: 3 } })
    await joinLoaded(3)
    expect(joinSave().disabled).toBe(true)
    // the first row has no way to move; the second cannot go up above it
    const arrows = (i: number) => [...joinRows()[i].querySelectorAll('.pi-arrow-up, .pi-arrow-down')].map((e) => e.className.includes('up') ? 'up' : 'down')
    expect(arrows(0)).toEqual([])
    expect(arrows(1)).toEqual(['down'])
    expect(arrows(2)).toEqual(['up'])
    joinRows()[2].querySelector<HTMLButtonElement>('.pi-arrow-up')?.closest('button')?.click()
    await vi.waitFor(() => expect(joinInput(1, 'chat').value).toBe('@third_chan'))
    expect(joinInput(2, 'chat').value).toBe('@second_chan')
    // add one, in Persian digits and as a t.me link, with a name
    joinButton(String(text('settings.join.add'))).click()
    await joinLoaded(4)
    joinType(3, 'chat', 'https://t.me/fourth_chan')
    joinType(3, 'label', '  دوستان   ما ')
    // remove the middle one
    joinRows()[2].querySelector<HTMLButtonElement>('.pi-trash')?.closest('button')?.click()
    await joinLoaded(3)
    await vi.waitFor(() => expect(joinSave().disabled).toBe(false))
    joinPut = (body) =>
      json(200, { ...joinNone, chats: body.chats.map((c, i) => joinRow(c.chat, { ...c, primary: i === 0 })), on: true, used: body.chats.length })
    joinSave().click()
    await vi.waitFor(() =>
      expect(joinPuts).toEqual([
        {
          chats: [
            { chat: '@first_news', link: '', label: '' },
            { chat: '@third_chan', link: '', label: '' },
            { chat: '@fourth_chan', link: '', label: 'دوستان ما' },
          ],
        },
      ]),
    )
    await vi.waitFor(() => expect(joinSave().disabled).toBe(true))
    expect(joinInput(2, 'label').value).toBe('دوستان ما')
  })

  it('checks every row where it is typed, as core does, and sends nothing while a row is wrong', async () => {
    await open(saved, { join: { chats: [joinRow('@first_news', { primary: true })], on: true, used: 1 } })
    await joinLoaded(1)
    joinButton(String(text('settings.join.add'))).click()
    await joinLoaded(2)
    joinType(1, 'chat', '-1009876543210') // a private channel needs its invite link
    joinType(1, 'label', 'x'.repeat(40))
    joinSave().click()
    await vi.waitFor(() => expect(joinRows()[1].textContent).toContain(text('settings.invalid.link_required')))
    expect(joinRows()[1].textContent).toContain(String(text('settings.invalid.too_long')).replace('{max}', '۳۲'))
    expect(joinPuts).toEqual([])
    joinType(1, 'link', 'https://example.com/x')
    joinSave().click()
    await vi.waitFor(() => expect(joinRows()[1].textContent).toContain(text('settings.invalid.tme_link')))
    // the same chat twice, the case of an @name aside
    joinType(1, 'chat', '@First_News')
    joinType(1, 'link', '')
    joinType(1, 'label', '')
    joinSave().click()
    await vi.waitFor(() => expect(joinRows()[1].textContent).toContain(text('settings.invalid.duplicate')))
    // a row left empty
    joinType(1, 'chat', '')
    joinSave().click()
    await vi.waitFor(() => expect(joinRows()[1].textContent).toContain(text('settings.invalid.required')))
    expect(joinPuts).toEqual([])
  })

  it('says what the license refuses when core will not add a chat', async () => {
    await open(saved, { join: { chats: [joinRow('@first_news', { primary: true })], on: true, used: 1, limit: 2 } })
    await joinLoaded(1)
    joinButton(String(text('settings.join.add'))).click()
    await joinLoaded(2)
    joinType(1, 'chat', '@second_chan')
    joinPut = () =>
      json(409, { error: 'state', license: 'max_join_chats', message: 'the license allows 1 required chat (channels and groups customers must join): buy or renew a license to add more' })
    await vi.waitFor(() => expect(joinSave().disabled).toBe(false))
    joinSave().click()
    await vi.waitFor(() => expect(joinCard().textContent).toContain(text('license.refused.max_join_chats')))
    expect(joinCard().textContent).toContain('the license allows 1 required chat')
    expect(joinRows()).toHaveLength(2) // what was typed stays
  })

  it('shows the list read only to a role that may not change it, and the requirement being off', async () => {
    await open(saved, { join: { chats: [joinRow('@first_news')], on: false, used: 1, editable: false } })
    await joinLoaded(1)
    expect(joinCard().querySelector('.pi-lock')).not.toBeNull()
    expect(joinInput(0, 'chat').readOnly).toBe(true)
    expect(joinCard().textContent).toContain(text('settings.join.locked'))
    expect(joinCard().textContent).toContain(text('settings.join.dormant'))
    expect(joinCard().querySelector('button[type=submit]')).toBeNull()
    expect(joinCard().querySelector('.pi-trash')).toBeNull()
    expect(joinButton(String(text('settings.join.add')))).toBeUndefined()
  })

  it('turns the system problem alerts off and on in the notifications card', async () => {
    const puts = await open(saved)
    const toggle = input('notify.system_alerts')
    // on by default, in the staff notifications card, with what it sends
    expect(toggle.checked).toBe(true)
    expect(card('notify.system_alerts')).toBe(card('notify.recipients'))
    const field = toggle.closest('.field') as HTMLElement
    expect(field.querySelector('.label')?.textContent?.trim()).toBe(text('settings.field.notify.system_alerts.label'))
    expect(field.querySelector('.help')?.textContent).toBe(text('settings.field.notify.system_alerts.help'))
    toggle.click()
    await vi.waitFor(() => expect(saveButton('notify.system_alerts').disabled).toBe(false))
    saveButton('notify.system_alerts').click()
    await vi.waitFor(() => expect(puts).toEqual([{ 'notify.system_alerts': 'false' }]))
    // saved: nothing left to save, and the switch stays off
    await vi.waitFor(() => expect(saveButton('notify.system_alerts').disabled).toBe(true))
    expect(input('notify.system_alerts').checked).toBe(false)
  })

  it('asks before turning maintenance on, then warns at the top', async () => {
    const puts = await open(saved)
    expect(document.body.textContent).not.toContain(text('settings.maintenance_on'))
    input('maintenance.enabled').click()
    await vi.waitFor(() => expect(saveButton('maintenance.enabled').disabled).toBe(false))
    saveButton('maintenance.enabled').click()
    const accept = await vi.waitFor(() => {
      const b = document.querySelector('.p-confirmdialog-accept-button') as HTMLButtonElement | null
      expect(b).not.toBeNull()
      return b!
    })
    expect(puts).toEqual([])
    accept.click()
    await vi.waitFor(() => expect(puts).toEqual([{ 'maintenance.enabled': 'true' }]))
    await vi.waitFor(() => expect(document.body.textContent).toContain(text('settings.maintenance_on')))
  })
  // The daily sales summary: its own card, off and at 09:00 by default, the time
  // checked and saved as HH:MM, the texts in the language of the page.
  const summaryItems = [...start, summaryOn, summaryTime]
  const summaryGroups = [...groups, 'reports']
  const summarySaved = (values: Record<string, string>) =>
    json(200, { items: summaryItems.map((i) => (i.key in values ? { ...i, value: values[i.key] } : i)), groups: summaryGroups } satisfies SettingsReply)

  it('has the daily sales summary in a card of its own, off at 09:00', async () => {
    const puts = await open(summarySaved, { items: summaryItems, groups: summaryGroups, cards: 6 })
    const on = input('reports.daily_summary')
    const at = input('reports.daily_summary_time')
    expect(card('reports.daily_summary')).toBe(card('reports.daily_summary_time'))
    expect(card('reports.daily_summary')).not.toBe(card('notify.sales'))
    expect(card('reports.daily_summary').querySelector('.card-title')?.textContent).toBe(text('settings.group.reports.title'))
    expect(on.checked).toBe(false)
    expect(at.value).toBe('09:00')
    expect(at.dir).toBe('ltr')
    expect(at.maxLength).toBe(5)
    for (const key of ['reports.daily_summary', 'reports.daily_summary_time']) {
      const field = input(key).closest('.field') as HTMLElement
      expect(field.querySelector('.label')?.textContent?.trim()).toBe(text(`settings.field.${key}.label`))
      expect(field.querySelector('.help')?.textContent).toBe(text(`settings.field.${key}.help`))
    }
    expect(saveButton('reports.daily_summary').disabled).toBe(true)

    // A time that is not a time of day is refused where it is typed, and not sent.
    type('reports.daily_summary_time', '25:00')
    await vi.waitFor(() => expect(saveButton('reports.daily_summary').disabled).toBe(false))
    saveButton('reports.daily_summary').click()
    await vi.waitFor(() => expect(at.closest('.field')?.textContent).toContain(text('settings.invalid.time')))
    expect(puts).toEqual([])

    // Typed in Persian digits without the leading zero, saved as HH:MM with the switch.
    type('reports.daily_summary_time', '۹:۳۰')
    on.click()
    await vi.waitFor(() => expect(at.closest('.field')?.textContent).not.toContain(text('settings.invalid.time')))
    saveButton('reports.daily_summary').click()
    await vi.waitFor(() => expect(puts).toEqual([{ 'reports.daily_summary': 'true', 'reports.daily_summary_time': '09:30' }]))
    await vi.waitFor(() => expect(saveButton('reports.daily_summary').disabled).toBe(true))
    expect(input('reports.daily_summary').checked).toBe(true)
    expect(input('reports.daily_summary_time').value).toBe('09:30')
  })

  // The Top users board's switch: a card of its own (Growth), on by default, with its texts in
  // the language of the page, saved as "false" when turned off.
  const growthSwitch = item('growth.leaderboard.enabled', 'bool', { default: 'true' })
  const growthItems = [...start, growthSwitch]
  const growthGroups = [...groups, 'growth']
  const growthSaved = (values: Record<string, string>) =>
    json(200, { items: growthItems.map((i) => (i.key in values ? { ...i, value: values[i.key] } : i)), groups: growthGroups } satisfies SettingsReply)

  it('has the Top users switch in a card of its own, on by default', async () => {
    const puts = await open(growthSaved, { items: growthItems, groups: growthGroups, cards: 6 })
    const on = input('growth.leaderboard.enabled')
    expect(card('growth.leaderboard.enabled').querySelector('.card-title')?.textContent).toBe(text('settings.group.growth.title'))
    expect(card('growth.leaderboard.enabled')).not.toBe(card('notify.sales'))
    expect(on.checked).toBe(true)
    const field = on.closest('.field') as HTMLElement
    expect(field.querySelector('.label')?.textContent?.trim()).toBe(text('settings.field.growth.leaderboard.enabled.label'))
    expect(field.querySelector('.help')?.textContent).toBe(text('settings.field.growth.leaderboard.enabled.help'))
    expect(saveButton('growth.leaderboard.enabled').disabled).toBe(true)

    on.click()
    await vi.waitFor(() => expect(saveButton('growth.leaderboard.enabled').disabled).toBe(false))
    saveButton('growth.leaderboard.enabled').click()
    await vi.waitFor(() => expect(puts).toEqual([{ 'growth.leaderboard.enabled': 'false' }]))
    await vi.waitFor(() => expect(saveButton('growth.leaderboard.enabled').disabled).toBe(true))
    expect(input('growth.leaderboard.enabled').checked).toBe(false)
  })

  it('has the Top users texts in both languages', () => {
    const get = (o: unknown, path: string) => path.split('.').reduce<unknown>((x, k) => (x as Record<string, unknown> | undefined)?.[k], o)
    for (const [lang, catalog] of [['en', en], ['fa', fa]] as const) {
      for (const k of [
        'settings.group.growth.title',
        'settings.group.growth.desc',
        'settings.field.growth.leaderboard.enabled.label',
        'settings.field.growth.leaderboard.enabled.help',
        'support.category.reseller',
      ]) {
        const v = get(catalog, k)
        expect(typeof v === 'string' && v.trim() !== '', `${lang} ${k}`).toBe(true)
      }
    }
  })

  // The plan list's layout and the bulk purchase: one card, Table by default, the
  // switch on, the most at 20; each saved alone, the number checked where it is typed
  // (2 to 100), the texts in the language of the page.
  const planLayout = item('bot.plan_layout', 'enum', { group: 'shop', default: 'table', options: ['table', 'list'] })
  const bulkOn = item('bulk.enabled', 'bool', { group: 'shop', default: 'true' })
  const bulkMax = item('bulk.max_quantity', 'int', { group: 'shop', default: '20', min: 2, max: 100 })
  const shopItems = [...start, planLayout, bulkOn, bulkMax]
  const shopGroups = [...groups, 'shop']
  const shopSaved = (values: Record<string, string>) =>
    json(200, { items: shopItems.map((i) => (i.key in values ? { ...i, value: values[i.key] } : i)), groups: shopGroups } satisfies SettingsReply)

  it('has the plan layout and bulk purchase in a card of their own', async () => {
    const puts = await open(shopSaved, { items: shopItems, groups: shopGroups, cards: 6 })
    expect(card('bot.plan_layout')).toBe(card('bulk.enabled'))
    expect(card('bulk.enabled')).toBe(card('bulk.max_quantity'))
    expect(card('bot.plan_layout')).not.toBe(card('notify.sales'))
    expect(card('bot.plan_layout').querySelector('.card-title')?.textContent).toBe(text('settings.group.shop.title'))
    for (const key of ['bot.plan_layout', 'bulk.enabled', 'bulk.max_quantity']) {
      const field = (document.getElementById('setting-' + key.replace(/[^A-Za-z0-9]/g, '-')) as HTMLElement).closest('.field') as HTMLElement
      expect(field.querySelector('.label')?.textContent?.trim(), key).toBe(text(`settings.field.${key}.label`))
      expect(field.querySelector('.help')?.textContent, key).toBe(text(`settings.field.${key}.help`))
    }
    // what the store has now: the table, the switch on, 20
    expect(card('bot.plan_layout').querySelector('.p-select-label')?.textContent).toBe(text('settings.option.bot.plan_layout.table'))
    expect(input('bulk.enabled').checked).toBe(true)
    expect(input('bulk.max_quantity').value).toBe('20')
    expect(input('bulk.max_quantity').dir).toBe('ltr')
    expect(saveButton('bulk.max_quantity').disabled).toBe(true)

    // a number out of range is refused where it is typed, and nothing is sent
    for (const bad of ['1', '101', 'many']) {
      type('bulk.max_quantity', bad)
      await vi.waitFor(() => expect(saveButton('bulk.max_quantity').disabled).toBe(false))
      saveButton('bulk.max_quantity').click()
      await vi.waitFor(() => expect(input('bulk.max_quantity').closest('.field')?.querySelector('[role=alert]')).not.toBeNull())
      expect(puts, bad).toEqual([])
    }

    // the layout, the switch and the limit together: only what changed, the number in ASCII digits
    type('bulk.max_quantity', '۵۰')
    input('bulk.enabled').click()
    card('bot.plan_layout').querySelector<HTMLElement>('.p-select')!.click()
    const list = await vi.waitFor(() => {
      const el = [...document.querySelectorAll('.p-select-option')].find((o) => o.textContent?.includes(String(text('settings.option.bot.plan_layout.list')))) as HTMLElement | undefined
      expect(el).toBeDefined()
      return el!
    })
    list.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) // PrimeVue picks on mousedown
    await vi.waitFor(() => expect(saveButton('bot.plan_layout').disabled).toBe(false))
    saveButton('bot.plan_layout').click()
    await vi.waitFor(() => expect(puts).toEqual([{ 'bot.plan_layout': 'list', 'bulk.enabled': 'false', 'bulk.max_quantity': '50' }]))
    await vi.waitFor(() => expect(saveButton('bot.plan_layout').disabled).toBe(true))
    expect(input('bulk.enabled').checked).toBe(false)
    expect(input('bulk.max_quantity').value).toBe('50')
    expect(card('bot.plan_layout').querySelector('.p-select-label')?.textContent).toBe(text('settings.option.bot.plan_layout.list'))
  })

  it('brings the entry a link names into view', async () => {
    const scrolled = vi.fn()
    const proto = Element.prototype as { scrollIntoView?: unknown }
    proto.scrollIntoView = scrolled // jsdom has none
    try {
      await open(summarySaved, { items: summaryItems, groups: summaryGroups, cards: 6, hash: '#setting-reports-daily-summary' })
      await vi.waitFor(() => expect(scrolled).toHaveBeenCalledTimes(1))
      expect(scrolled.mock.contexts[0]).toBe(input('reports.daily_summary'))
    } finally {
      delete proto.scrollIntoView
    }
  })
})
