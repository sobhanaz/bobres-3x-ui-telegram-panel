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
import type { SettingItem, SettingsReply } from '../types'

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

  async function open(
    put: (values: Record<string, string>) => Response,
    opts: { permissions?: string[]; items?: SettingItem[]; groups?: string[]; cards?: number; hash?: string } = {},
  ) {
    const items = opts.items ?? start
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
    expect(saveButton('join.channel').disabled).toBe(true)
    saveButton('limits.bot_per_10s').click()
    await vi.waitFor(() => expect(puts).toEqual([{ 'limits.bot_per_10s': '30' }]))
    await vi.waitFor(() => expect(saveButton('limits.bot_per_10s').disabled).toBe(true))
    expect(input('limits.bot_per_10s').value).toBe('30')
    // a setting the role may not change: a lock, read only, the value still shown, never sent
    expect(card('limits.tickets_per_day').querySelector('.pi-lock')).not.toBeNull()
    expect(input('limits.tickets_per_day').readOnly).toBe(true)
    expect(input('limits.tickets_per_day').value).toBe('5')
    expect(card('join.channel').querySelector('.pi-lock')).toBeNull()
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
    await open(() => json(400, { error: 'invalid', message: 'join.link: not an invite link', field: 'join.link' }))
    type('join.channel', '-1001234567890')
    type('join.link', 'https://t.me/+AbCdEf')
    await vi.waitFor(() => expect(saveButton('join.channel').disabled).toBe(false))
    saveButton('join.channel').click()
    await vi.waitFor(() => expect(input('join.link').closest('.field')?.textContent).toContain('join.link: not an invite link'))
    const check = card('join.channel').querySelector('.field button') as HTMLButtonElement
    expect(check.disabled).toBe(false)
    check.click()
    await vi.waitFor(() => expect(card('join.channel').textContent).toContain(text('settings.check.unreachable')))
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
