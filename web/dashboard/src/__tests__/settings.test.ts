import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api'
import fa from '../locales/fa.json'
import {
  canonical,
  changed,
  changes,
  describeCheck,
  effective,
  example,
  normalize,
  normalizeChannel,
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
  const pay = (key: string, kind: SettingItem['kind'], extra: Partial<SettingItem> = {}) => item(key, kind, { editable: false, ...extra })
  const start: SettingItem[] = [
    tz,
    item('maintenance.enabled', 'bool', { default: 'false' }),
    channel,
    link,
    item('notify.new_payment', 'bool', { default: 'true' }),
    item('notify.new_ticket', 'bool', { default: 'true' }),
    item('notify.provision_failed', 'bool', { default: 'true' }),
    sales,
    recipients,
    perTen,
    item('limits.tickets_per_day', 'int', { default: '10', min: 0, max: 100 }),
    pay('payments.card_number', 'text', { value: '6037 9911 2345 6789' }),
    pay('payments.card_holder', 'text'),
    pay('payments.usdt_trc20', 'text'),
    pay('payments.usdt_erc20', 'text'),
    pay('payments.usdt_rate', 'int', { min: 1, max: 1e12 }),
    pay('payments.stars_rate', 'int', { min: 1, max: 1e9 }),
    pay('payments.zarinpal_link', 'url'),
  ]
  const groups = ['general', 'maintenance', 'join', 'notify', 'limits', 'payments']
  const text = (path: string) => path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa) ?? path
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

  async function open(put: (values: Record<string, string>) => Response) {
    vi.resetModules()
    vi.spyOn(console, 'warn').mockImplementation(() => {}) // texts not merged into the catalogs yet
    window.history.replaceState(null, '', '/admin/settings')
    document.body.innerHTML = '<div id="app"></div>'
    // jsdom has no matchMedia; PrimeVue's Select asks it about orientation
    vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const puts: Record<string, string>[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        if (url === '/api/v1/me') return json(200, me)
        if (url === '/api/v1/settings' && method === 'GET') return json(200, { items: start, groups } satisfies SettingsReply)
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
    await vi.waitFor(() => expect(document.querySelectorAll('.settings-card')).toHaveLength(6))
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
    // payments are read-only for an admin: no Save, a lock, the value still shown
    expect(card('payments.card_number').querySelector('button[type=submit]')).toBeNull()
    expect(card('payments.card_number').querySelector('.pi-lock')).not.toBeNull()
    expect(input('payments.card_number').readOnly).toBe(true)
    expect(input('payments.card_number').value).toBe('6037 9911 2345 6789')
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
})
