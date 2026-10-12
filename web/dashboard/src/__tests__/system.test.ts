import { existsSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, expect, it, vi } from 'vitest'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import { bytes, date, dateTime } from '../format'
import {
  absent,
  ALERT_FALLBACK,
  alertArgs,
  alertLevel,
  alertsState,
  alertTextKey,
  backupSent,
  backupsState,
  channelOf,
  checkList,
  fills,
  INSTALL_COMMAND,
  issuedDay,
  logAttrs,
  logLevel,
  logsReason,
  mismatched,
  newestFirst,
  notSentReason,
  openAlerts,
  serviceState,
  sortedServices,
  unixOf,
  UPDATE_COMMAND,
  updateDetail,
  updateReason,
  updateState,
  worstLevel,
} from '../system'
import {
  bindingOf,
  bindings,
  canSave,
  entitlementRows,
  graceEndsAt,
  isValid,
  KEY_MAX,
  KEY_REASONS,
  keyOf,
  keyProblem,
  keyRefusal,
  licenseEnds,
  limitState,
  normalizeDomain,
  outsideLicense,
  poweredBy,
  reasonKey,
  renews,
  SOURCES,
  sourceKey,
  stateOf,
  timeLeft,
  TOKEN_MAX,
  tokenOf,
  tokenProblem,
  warnings,
} from '../license'
import type { BackupFile, LicenseEntitlements, LicenseInfo, LicenseToken, LogLine, ServiceHealth, SystemAlert, SystemBackups, SystemLogs, SystemReply, UpdateCheck } from '../types'

const DAY = 86_400
const iid = 'aaaaaaaa-1111-4aaa-8aaa-aaaaaaaaaaaa'
const svc = (name: string, over: Partial<ServiceHealth> = {}): ServiceHealth => ({
  name, status: 'ok', version: 'v1.4.0', checks: { db: 'ok' }, detail: '', latency_ms: 4, version_mismatch: false, ...over,
})
// what GET /license answers about a license's grants, with this install's use
const ents = (over: Partial<LicenseEntitlements> = {}): LicenseEntitlements => ({
  white_label: false, resellers: true, crypto_watcher: false,
  max_servers: { limit: 5, used: 3, near: false, full: false },
  max_active_services: { limit: 500, used: 460, near: true, full: false },
  dashboard_staff_seats: { limit: 2, used: 2, near: true, full: true },
  gateways: { licensed: ['zarinpal', 'manual_card'], enabled: ['zarinpal', 'stars'] },
  updates_until: '2027-09-30', ...over,
})
const token = (now: number, over: Partial<LicenseToken> = {}): LicenseToken => ({
  license_id: 'lic_7Q2', customer: 'cust_rose', tier: 'standard', kid: 'lic-2026a',
  issued_at: now - 300 * DAY, expires_at: now + 40 * DAY + 3600, grace_days: 14,
  domain: 'Shop.Example.com.', install_id: '', ...over,
})

describe('system health', () => {
  it('reads each service as working, not ready, down, not installed or not configured', () => {
    expect(serviceState(svc('core'))).toBe('ok')
    expect(serviceState(svc('payments', { status: 'not_ready' }))).toBe('not_ready')
    expect(serviceState(svc('bot', { status: 'down' }))).toBe('down')
    expect(serviceState(svc('backup', { status: 'not_installed' }))).toBe('not_installed')
    expect(serviceState(svc('backup', { status: 'not_configured' }))).toBe('not_configured')
    // a status this page does not know is not taken for working
    expect(serviceState(svc('bot', { status: 'odd' }))).toBe('down')
    expect(serviceState(svc('bot', { status: '' }))).toBe('down')
    expect(absent(svc('backup', { status: 'not_installed' }))).toBe(true)
    expect(absent(svc('backup', { status: 'not_configured' }))).toBe(true)
    expect(absent(svc('bot', { status: 'down' }))).toBe(false)
  })
  it('lists the known services first, in their usual order', () => {
    const list = [svc('backup'), svc('zeta'), svc('bot'), svc('provisioner'), svc('core'), svc('alpha'), svc('payments')]
    expect(sortedServices(list).map((s) => s.name)).toEqual(['core', 'bot', 'payments', 'provisioner', 'backup', 'alpha', 'zeta'])
  })
  it('lists the readiness checks by name', () => {
    expect(checkList(svc('core', { checks: { grpc: 'fail', db: 'ok' } }))).toEqual([{ name: 'db', ok: true }, { name: 'grpc', ok: false }])
    expect(checkList(svc('x', { checks: undefined }))).toEqual([])
    expect(checkList(svc('x', { checks: null }))).toEqual([])
  })
  it('names the services core found on another version, never core or an absent one', () => {
    const reply: SystemReply = {
      version: 'v1.4.0',
      services: [
        svc('payments', { version: '1.3.0', version_mismatch: true }),
        svc('core', { version_mismatch: true }),
        svc('bot', { version: 'v1.3.9', version_mismatch: true }),
        svc('provisioner', { version: '1.4.0' }),
        svc('backup', { status: 'not_installed', version_mismatch: true }),
      ],
    }
    expect(mismatched(reply).map((s) => s.name)).toEqual(['bot', 'payments'])
    expect(mismatched({ services: [svc('bot', { version_mismatch: undefined })] })).toEqual([])
  })
})

describe('open problems', () => {
  const alert = (key: string, over: Partial<SystemAlert> = {}): SystemAlert => ({ key, severity: 'warning', title_key: '', args: {}, detail: '', since: 1_790_000_000, ...over })
  it('reads the severity, never taking an unknown one for harmless', () => {
    expect(alertLevel(alert('a', { severity: 'critical' }))).toBe('critical')
    expect(alertLevel(alert('a', { severity: ' Critical ' }))).toBe('critical')
    expect(alertLevel(alert('a', { severity: 'warning' }))).toBe('warning')
    expect(alertLevel(alert('a', { severity: 'odd' }))).toBe('warning')
    expect(alertLevel(alert('a', { severity: '' }))).toBe('warning')
  })
  it('lists them critical first, then the oldest first, as core does', () => {
    const list = [
      alert('disk.low', { since: 300 }),
      alert('service.bot', { severity: 'critical', since: 500 }),
      alert('backup.stale', { since: '1970-01-01T00:03:20Z' }),
      alert('server.s2', { severity: 'critical', since: 400 }),
      alert('bot.lag', { since: 300 }),
    ]
    expect(openAlerts({ alerts: list })!.map((a) => a.key)).toEqual(['server.s2', 'service.bot', 'backup.stale', 'bot.lag', 'disk.low'])
    expect(worstLevel(list)).toBe('critical')
    expect(worstLevel([alert('license')])).toBe('warning')
    expect(worstLevel([])).toBeNull()
    // all clear, core could not read them, and an older core without a watchdog
    expect(alertsState({ alerts: [] })).toBe('ok')
    expect(alertsState({ alerts: null })).toBe('unreadable')
    expect(alertsState({})).toBe('unsupported')
    expect(alertsState(null)).toBe('unsupported')
    expect(openAlerts({ alerts: [] })).toEqual([])
    expect(openAlerts({})).toBeNull()
    expect(openAlerts({ alerts: null })).toBeNull()
    expect(openAlerts(null)).toBeNull()
  })
  it('names a problem as the bot’s message does, with its values written the same way', () => {
    expect(alertTextKey({ title_key: 'alert.backup_stale' })).toBe('system.alerts.kind.backup_stale')
    expect(alertTextKey({ title_key: '' })).toBe('')
    expect(alertTextKey({})).toBe('')
    expect(alertTextKey({ title_key: 'alert.<b>' })).toBe('')
    // every text the bot has for a problem is here too, in both languages
    const botTexts = ['service_down', 'service_not_ready', 'server_down', 'zarinpal_failing', 'zarinpal_stuck', 'backup_failed', 'backup_stale',
      'disk_low', 'provision_failed', 'bot_lag', 'license_expiring', 'license_grace', 'license_expired', 'check']
    expect(Object.keys(fa.system.alerts.kind).sort()).toEqual([...botTexts].sort())
    expect(Object.keys(en.system.alerts.kind).sort()).toEqual([...botTexts].sort())
    expect(ALERT_FALLBACK).toBe('system.alerts.kind.check')
    // and with the same values as the bot's own catalogs (in a full checkout)
    const holes = (m: string) => [...m.matchAll(/\{(\w+)\}/g)].map((x) => x[1]).sort()
    for (const [lang, page] of [['en', en], ['fa', fa]] as const) {
      const file = resolve(dirname(fileURLToPath(import.meta.url)), `../../../../internal/bot/i18n/locales/${lang}.json`)
      if (!existsSync(file)) continue
      const bot = JSON.parse(readFileSync(file, 'utf8')) as Record<string, string>
      const problems = Object.keys(bot)
        .filter((k) => k.startsWith('alert.') && !/^alert\.(critical|warning|repeat|resolved|duration_\w+)$/.test(k))
        .map((k) => k.slice('alert.'.length))
      expect(problems.sort(), lang).toEqual(Object.keys(page.system.alerts.kind).sort())
      for (const k of problems) expect(holes((page.system.alerts.kind as Record<string, string>)[k]), `${lang} ${k}`).toEqual(holes(bot['alert.' + k]))
    }
    // numbers in the language's digits, a size, a day; names kept whole; the key when core gives none
    const day = Date.UTC(2026, 9, 21, 12) / 1000
    const args = { count: '3', days: '9', hours: '30', minutes: '12', free: String(1.5 * 2 ** 30), date: String(day), server: 'nl-1', service: 'bot' }
    expect(alertArgs(alert('x.y', { args }), 'fa', (k, v) => (k === 'service' ? 'ربات' : v))).toEqual({
      key: '\u2068x.y\u2069', count: '۳', days: '۹', hours: '۳۰', minutes: '۱۲', free: bytes(1.5 * 2 ** 30, 'fa'), date: date(day, 'fa'),
      server: '\u2068nl-1\u2069', service: '\u2068ربات\u2069',
    })
    expect(alertArgs(alert('x.y', { args }), 'en').count).toBe('3')
    expect(alertArgs(alert('x.y', { args: { key: 'other' } }), 'en').key).toBe('\u2068other\u2069')
    expect(alertArgs(alert('x.y', { args: null }), 'en')).toEqual({ key: '\u2068x.y\u2069' })
    // a text is used only when every value it needs is there
    expect(fills('No new backup for {hours} hours', { hours: '30' })).toBe(true)
    expect(fills('No new backup for {hours} hours', { key: 'x' })).toBe(false)
    expect(fills('The last backup failed', {})).toBe(true)
    expect(fills(undefined, {})).toBe(true)
  })
})

describe('update check', () => {
  const upd = (over: Partial<UpdateCheck> = {}): UpdateCheck => ({
    current: '1.5.0', latest: '1.6.0', newer: true, verified: true, checked_at: 1_790_000_000, error: '', ...over,
  })
  it('reads a newer release, up to date, or not known', () => {
    expect(updateState(upd())).toBe('newer')
    expect(updateState(upd({ latest: '1.5.0', newer: false }))).toBe('current')
    expect(updateState(upd({ latest: '', newer: false, error: 'unreachable' }))).toBe('unknown')
    // not checked yet
    expect(updateState(upd({ latest: '', newer: false, checked_at: 0 }))).toBe('unknown')
    // a release read before a later check failed still counts; "up to date" does not
    expect(updateState(upd({ error: 'unreachable' }))).toBe('newer')
    expect(updateState(upd({ latest: '1.5.0', newer: false, error: 'unreachable' }))).toBe('unknown')
    // an older core says nothing
    expect(updateState(undefined)).toBe('unknown')
    expect(updateState(null)).toBe('unknown')
  })
  it('says why the latest release is not known', () => {
    expect(updateReason(upd())).toBeNull()
    expect(updateReason(upd({ latest: '', error: 'unreachable' }))).toBe('unreachable')
    expect(updateReason(upd({ latest: '', error: 'bot unreachable' }))).toBe('bot_unreachable')
    expect(updateReason(upd({ latest: '', error: 'checksums.txt: no version header' }))).toBe('other')
    expect(updateReason(upd({ latest: '', checked_at: 0 }))).toBe('not_checked')
    expect(updateReason(upd({ error: 'unreachable' }))).toBe('unreachable')
    expect(updateReason(undefined)).toBe('unsupported')
    // the bot's own codes, each explained (before its first check it says not_checked)
    expect(updateReason(upd({ latest: '', newer: false, checked_at: 0, error: 'not_checked' }))).toBe('not_checked')
    expect(updateReason(upd({ latest: '', newer: false, error: 'no_release' }))).toBe('no_release')
    expect(updateReason(upd({ newer: false, verified: false, error: 'bad_signature' }))).toBe('bad_signature')
    expect(updateReason(upd({ latest: '', newer: false, error: 'invalid' }))).toBe('invalid')
    for (const r of ['unreachable', 'bot_unreachable', 'not_checked', 'no_release', 'bad_signature', 'invalid', 'unsupported', 'other']) {
      expect((fa.system.updates.reason as Record<string, string>)[r]).toBeTruthy()
      expect((en.system.updates.reason as Record<string, string>)[r]).toBeTruthy()
    }
    // the bot's detail goes with a failed signature check or an unknown error, not a code the page explains
    const signed = upd({ newer: false, verified: false, error: 'bad_signature', detail: 'the release has no keys.txt' })
    expect(updateDetail(signed, updateReason(signed))).toBe('the release has no keys.txt')
    const odd = upd({ latest: '', error: 'rate_limited', detail: 'try later' })
    expect(updateDetail(odd, updateReason(odd))).toBe('rate_limited: try later')
    const plain = upd({ latest: '', error: 'checksums.txt: no version header' })
    expect(updateDetail(plain, updateReason(plain))).toBe('checksums.txt: no version header')
    const away = upd({ latest: '', error: 'unreachable', detail: 'checksums.txt: dial tcp: i/o timeout' })
    expect(updateDetail(away, updateReason(away))).toBe('')
    expect(updateDetail(null, 'unsupported')).toBe('')
    expect(UPDATE_COMMAND).toBe('sudo bobres update')
  })
  it('flags a newer release the license does not cover', () => {
    expect(updateState(upd({ covered: false }))).toBe('not_covered')
    // read before a later check failed: still not covered
    expect(updateState(upd({ covered: false, error: 'unreachable' }))).toBe('not_covered')
    expect(updateState(upd({ covered: true }))).toBe('newer')
    // no valid license to ask, or an older core: installable
    expect(updateState(upd({ covered: null }))).toBe('newer')
    expect(updateState(upd())).toBe('newer')
    // nothing newer: nothing to cover
    expect(updateState(upd({ latest: '1.5.0', newer: false, covered: false }))).toBe('current')
    for (const lang of [fa, en]) {
      expect(lang.system.updates.state.not_covered).toBeTruthy()
      expect(lang.system.updates.not_covered).toContain('{version}')
      expect(lang.system.updates.not_covered_hint).toBeTruthy()
    }
  })
  it('reads the day the latest release was signed', () => {
    expect(issuedDay(upd({ issued: '2027-03-01T10:04Z' }))).toBe('2027-03-01')
    expect(issuedDay(upd({ issued: ' 2027-03-01T23:59Z ' }))).toBe('2027-03-01')
    // not said, or not the header's form
    expect(issuedDay(upd())).toBe('')
    expect(issuedDay(upd({ issued: '' }))).toBe('')
    expect(issuedDay(upd({ issued: 'yesterday' }))).toBe('')
    expect(issuedDay(upd({ issued: '2027-03-01' }))).toBe('')
    expect(issuedDay(null)).toBe('')
  })
  it('reads the channel this install follows', () => {
    expect(channelOf(upd({ channel: 'stable' }))).toBe('stable')
    expect(channelOf(upd({ channel: ' Beta ' }))).toBe('beta')
    // the bot does not say, or says something else
    expect(channelOf(upd({ channel: '' }))).toBeNull()
    expect(channelOf(upd({ channel: 'nightly' }))).toBeNull()
    expect(channelOf(upd())).toBeNull()
    expect(channelOf(null)).toBeNull()
    expect(channelOf(undefined)).toBeNull()
    for (const lang of [fa, en]) {
      expect(lang.system.updates.channel_name.stable).toBeTruthy()
      expect(lang.system.updates.channel_name.beta).toBeTruthy()
    }
  })
})

describe('license', () => {
  const now = 1_790_000_000
  it('reads its state, and which states are a verified license', () => {
    expect(stateOf('active')).toBe('active')
    expect(stateOf('grace')).toBe('grace')
    expect(stateOf('expired')).toBe('expired')
    expect(stateOf('missing')).toBe('missing')
    expect(stateOf('invalid')).toBe('invalid')
    expect(stateOf('revoked')).toBe('invalid')
    expect(stateOf('')).toBe('missing')
    expect(stateOf(undefined)).toBe('missing')
    expect(['active', 'grace', 'expired'].every(isValid)).toBe(true)
    expect(['missing', 'invalid', 'odd', ''].some(isValid)).toBe(false)
  })
  it('shows "Powered by BOBRES" only under a valid license without white label', () => {
    // core says so
    expect(poweredBy({ state: 'active', white_label: false, powered_by: false })).toBe(false)
    expect(poweredBy({ state: 'missing', white_label: false, powered_by: true })).toBe(true)
    // read here when it does not
    expect(poweredBy({ state: 'active', white_label: false })).toBe(true)
    expect(poweredBy({ state: 'grace', white_label: false })).toBe(true)
    expect(poweredBy({ state: 'expired', white_label: false })).toBe(true)
    expect(poweredBy({ state: 'active', white_label: true })).toBe(false)
    // never on an install without a license of its own
    expect(poweredBy({ state: 'missing', white_label: false })).toBe(false)
    expect(poweredBy({ state: 'invalid', white_label: false })).toBe(false)
    expect(poweredBy(undefined)).toBe(false)
    expect(poweredBy(null)).toBe(false)
  })
  it('explains every reason core gives, and others plainly', () => {
    for (const r of ['malformed', 'bad_signature', 'unknown_key', 'version', 'domain', 'install_id', 'unbound', 'invalid', 'not_yet_valid', 'expired']) {
      expect(reasonKey(r)).toBe('license.reason.' + r)
      expect((fa.license.reason as Record<string, string>)[r]).toBeTruthy()
    }
    expect(reasonKey('')).toBe('license.reason.other')
    expect(reasonKey('weird')).toBe('license.reason.other')
  })
  it('compares the binding as core does', () => {
    expect(normalizeDomain(' Shop.Example.com. ')).toBe('shop.example.com')
    expect(normalizeDomain('shop.example.com:8443')).toBe('shop.example.com')
    expect(normalizeDomain('[::1]:443')).toBe('::1')
    expect(normalizeDomain('fe80::1')).toBe('fe80::1')
    expect(bindingOf('Shop.Example.com.', 'shop.example.com', true)).toBe('match')
    expect(bindingOf('other.example.com', 'shop.example.com', true)).toBe('mismatch')
    expect(bindingOf('', 'shop.example.com', true)).toBe('unbound')
    expect(bindingOf(iid.toUpperCase(), iid)).toBe('match')
    expect(bindingOf(iid, 'bbbbbbbb-1111-4aaa-8aaa-aaaaaaaaaaaa')).toBe('mismatch')
    expect(bindingOf('', iid)).toBe('unbound')
    // core's comparison wins; the page's own fills in what it leaves out
    const here = { domain: 'shop.example.com', install_id: iid, license: token(now, { install_id: iid }) }
    expect(bindings({ ...here, binding: { domain: 'mismatch', install_id: 'match' } })).toEqual({ domain: 'mismatch', install_id: 'match' })
    expect(bindings({ ...here, binding: null })).toEqual({ domain: 'match', install_id: 'match' })
    expect(bindings({ ...here, binding: { domain: 'odd', install_id: '' } })).toEqual({ domain: 'match', install_id: 'match' })
    expect(bindings({ ...here, license: null })).toBeNull()
  })
  it('counts down to the expiry, then the grace, then since', () => {
    const l = { expires_at: now + 10 * DAY + 60, grace_days: 14 }
    expect(graceEndsAt(l)).toBe(now + 24 * DAY + 60)
    expect(graceEndsAt({ expires_at: now, grace_days: -3 })).toBe(now)
    // core's own end wins
    expect(graceEndsAt({ ...l, grace_ends_at: now + 99 })).toBe(now + 99)
    expect(timeLeft(l, now * 1000)).toEqual({ kind: 'left', days: 10 })
    expect(timeLeft(l, (now + 11 * DAY) * 1000)).toEqual({ kind: 'grace', days: 13 })
    expect(timeLeft(l, (now + 30 * DAY) * 1000)).toEqual({ kind: 'over', days: 5 })
    // no grace: straight from left to over
    expect(timeLeft({ expires_at: now, grace_days: 0 }, (now + 1) * 1000)).toEqual({ kind: 'over', days: 0 })
  })
  it('warns from 90% of a limit', () => {
    expect(limitState(3, -1)).toBe('unlimited')
    expect(limitState(null, -1)).toBe('unlimited')
    expect(limitState(null, 5)).toBe('unknown')
    expect(limitState(undefined, 5)).toBe('unknown')
    // 0 grants none: nothing used is fine, anything used is over
    expect(limitState(0, 0)).toBe('none')
    expect(limitState(1, 0)).toBe('over')
    expect(limitState(3, 5)).toBe('ok')
    expect(limitState(449, 500)).toBe('ok')
    expect(limitState(450, 500)).toBe('near')
    expect(limitState(499, 500)).toBe('near')
    expect(limitState(500, 500)).toBe('full')
    expect(limitState(501, 500)).toBe('over')
    expect(limitState(1, 1)).toBe('full')
  })
  it('names the methods turned on outside the license, the wallet never', () => {
    expect(outsideLicense(['zarinpal', 'Manual_Card'], ['wallet', 'ZarinPal', 'manual_card', 'stars'])).toEqual(['stars'])
    expect(outsideLicense([], ['wallet'])).toEqual([])
    expect(outsideLicense([], ['manual_crypto'])).toEqual(['manual_crypto'])
  })
  it('lists what it grants with what this install uses', () => {
    const rows = entitlementRows({ entitlements: ents({ max_active_services: { limit: -1, used: 460 } }) })
    expect(rows).toEqual([
      { key: 'servers', kind: 'limit', used: 3, limit: 5, state: 'ok' },
      { key: 'active_services', kind: 'limit', used: 460, limit: -1, state: 'unlimited' },
      { key: 'staff_seats', kind: 'limit', used: 2, limit: 2, state: 'full' },
      { key: 'gateways', kind: 'gateways', allowed: ['zarinpal', 'manual_card'], outside: ['stars'] },
      { key: 'resellers', kind: 'feature', on: true },
      { key: 'white_label', kind: 'feature', on: false },
      { key: 'crypto_watcher', kind: 'feature', on: false },
      { key: 'updates_until', kind: 'date', value: '2027-09-30' },
    ])
    expect(warnings(rows)).toBe(2)
    // a count core could not read is unknown, never a warning
    const blind = entitlementRows({ entitlements: ents({ max_servers: { limit: 5, used: null }, updates_until: undefined, gateways: { licensed: [], enabled: [] } }) })
    expect(blind[0]).toEqual({ key: 'servers', kind: 'limit', used: null, limit: 5, state: 'unknown' })
    expect(blind.find((r) => r.key === 'updates_until')).toEqual({ key: 'updates_until', kind: 'date', value: '' })
    expect(warnings(blind)).toBe(2)
    expect(entitlementRows({ entitlements: null })).toEqual([])
    expect(entitlementRows({})).toEqual([])
  })
  it('saves only a token that verifies here and has not expired', () => {
    expect(canSave('active')).toBe(true)
    expect(canSave('grace')).toBe(true)
    expect(canSave('expired')).toBe(false)
    expect(canSave('invalid')).toBe(false)
    expect(canSave('missing')).toBe(false)
    expect(canSave(undefined)).toBe(false)
  })
  it('takes a pasted token without the breaks a chat adds', () => {
    expect(tokenOf('  eyJ2Ijox\n fQ.c2ln\r\n')).toBe('eyJ2IjoxfQ.c2ln')
    expect(tokenProblem('eyJ2IjoxfQ.c2ln-_')).toBe('')
    expect(tokenProblem('eyJ2Ijox\nfQ.c2ln')).toBe('')
    expect(tokenProblem('   ')).toBe('empty')
    expect(tokenProblem('no-dot-here')).toBe('format')
    expect(tokenProblem('a.b.c')).toBe('format')
    expect(tokenProblem('abc+/=.sig')).toBe('format')
    expect(tokenProblem('.sig')).toBe('format')
    expect(tokenProblem('a'.repeat(TOKEN_MAX) + '.b')).toBe('long')
  })
  it('takes a typed key without the breaks, digits and dashes a chat changes', () => {
    expect(keyOf('  BOBRES-7K2Q-\n 9XWM-۴TPA\r\n')).toBe('BOBRES-7K2Q-9XWM-4TPA')
    expect(keyOf('BOBRES\u20137K2Q\u20149XWM\u22124TPA')).toBe('BOBRES-7K2Q-9XWM-4TPA')
    expect(keyProblem('BOBRES-7K2Q-9XWM-4TPA')).toBe('')
    expect(keyProblem('bobres-7k2q-9xwm-4tpa')).toBe('')
    expect(keyProblem(' BOBRES-7K2Q\n-9XWM ')).toBe('')
    expect(keyProblem('')).toBe('empty')
    expect(keyProblem(' \n ')).toBe('empty')
    // a token belongs in the token box
    expect(keyProblem('eyJ2IjoxfQ.c2ln')).toBe('token')
    expect(keyProblem('BOBRES-' + 'A'.repeat(KEY_MAX))).toBe('long')
    // the license id, which shows on this page, is not the key
    expect(keyProblem('lic_7Q2')).toBe('format')
    expect(keyProblem('BOBRES')).toBe('format')
    expect(keyProblem('BOBRES-')).toBe('format')
    expect(keyProblem('BOBRES--7K2Q')).toBe('format')
    expect(keyProblem('LICENSE-7K2Q-9XWM')).toBe('format')
    expect(keyProblem('BOBRES-7K2Q_9XWM')).toBe('format')
    for (const lang of [fa, en]) {
      for (const p of ['token', 'long', 'format']) expect((lang.license.activate.problem as Record<string, string>)[p]).toBeTruthy()
    }
  })
  it('explains why a key was refused, and a token the license server answered', () => {
    for (const lang of [fa, en]) {
      for (const r of [...KEY_REASONS, 'other']) expect((lang.license.activate.reason as Record<string, string>)[r]).toBeTruthy()
    }
    const no = (status: number, data: Record<string, unknown>, message = 'license server: no') => keyRefusal({ status, data, message })
    // the license server's refusal, as core passes it on
    expect(no(400, { error: 'refused', field: 'key', code: 'invalid_key' })).toEqual({ reason: 'license.activate.reason.invalid_key', detail: '' })
    expect(no(400, { error: 'refused', field: 'key', code: 'revoked' })).toEqual({ reason: 'license.activate.reason.revoked', detail: '' })
    // with its words when they carry a date
    const moved = 'license server: this license key has moved to another install 3 times in the last year (the most allowed); the next move is possible on 2027-03-01, or ask the vendor'
    expect(no(400, { error: 'refused', field: 'key', code: 'reactivation_limit' }, moved)).toEqual({ reason: 'license.activate.reason.reactivation_limit', detail: moved })
    expect(no(400, { error: 'refused', field: 'key', code: 'expired' }, 'license server: this license ended on 2026-09-30; renew it with the vendor'))
      .toEqual({ reason: 'license.activate.reason.expired', detail: 'license server: this license ended on 2026-09-30; renew it with the vendor' })
    // a code the page has no sentence for, and core's own refusal of the typed key: core's words
    expect(no(400, { error: 'refused', field: 'key', code: 'invalid' }, 'license server: the license key is not in the BOBRES-XXXX-XXXX-... form'))
      .toEqual({ reason: 'license.activate.reason.other', detail: 'license server: the license key is not in the BOBRES-XXXX-XXXX-... form' })
    expect(no(400, { error: 'invalid', field: 'key' }, 'this is too long for a license key')).toEqual({ reason: 'license.activate.reason.other', detail: 'this is too long for a license key' })
    // the token it answered does not verify here: as for a pasted token (expired among them)
    expect(no(400, { error: 'invalid', field: 'token', code: 'domain' }, 'token: x')).toEqual({ reason: 'license.reason.domain', detail: '' })
    expect(no(400, { error: 'invalid', field: 'token', code: 'expired' }, 'token: x')).toEqual({ reason: 'license.reason.expired', detail: '' })
    expect(no(400, { error: 'invalid', field: 'token', code: 'odd' }, 'token: x')).toEqual({ reason: 'license.reason.other', detail: '' })
    // anything else is not about the key
    expect(no(400, { error: 'invalid', field: 'reason', message: 'a reason is required' })).toBeNull()
    expect(no(400, { error: 'invalid', field: 'token' })).toBeNull()
    expect(no(400, { error: 'invalid' })).toBeNull()
    expect(no(502, { error: 'unavailable', field: 'key', code: 'unreachable' })).toBeNull()
    expect(no(429, { error: 'rate_limited', code: 'rate_limited' })).toBeNull()
  })
  it('says how the license in use came, and when it renews online', () => {
    for (const src of SOURCES) {
      expect(sourceKey(src)).toBe('license.source.' + src)
      expect((fa.license.source as Record<string, string>)[src]).toBeTruthy()
      expect((en.license.source as Record<string, string>)[src]).toBeTruthy()
    }
    expect(sourceKey('')).toBe('')
    expect(sourceKey(null)).toBe('')
    expect(sourceKey(undefined)).toBe('')
    expect(sourceKey('import')).toBe('')
    // a short token the license server renews until the license ends
    const short = token(now, { expires_at: now + 30 * DAY, license_ends_at: now + 300 * DAY })
    expect(licenseEnds(short)).toBe(now + 300 * DAY)
    // a token that runs to the license's end, or does not say
    expect(licenseEnds({ expires_at: now, license_ends_at: now })).toBeNull()
    for (const l of [{ expires_at: now }, { expires_at: now, license_ends_at: 0 }, { expires_at: now, license_ends_at: null }]) expect(licenseEnds(l)).toBeNull()
    // core renews only a license activated with a key
    expect(renews({ license: short, source: 'activation' })).toBe(true)
    expect(renews({ license: token(now), source: 'refresh' })).toBe(true)
    expect(renews({ license: short, source: 'manual' })).toBe(false)
    expect(renews({ license: null, source: 'activation' })).toBe(false)
    // an older core that does not say: read from the token
    expect(renews({ license: short })).toBe(true)
    expect(renews({ license: token(now), source: null })).toBe(false)
  })
})

describe('logs', () => {
  const line = (time: number, level: string, msg: string, attrs = ''): LogLine => ({ time, level, msg, attrs })
  it('lists the lines newest first, keeping the order within a second', () => {
    const got = newestFirst([line(10, 'WARN', 'a'), line(20, 'ERROR', 'b'), line(20, 'WARN', 'c'), line(15, 'WARN', 'd')])
    expect(got.map((l) => l.msg)).toEqual(['c', 'b', 'd', 'a'])
    expect(newestFirst(null)).toEqual([])
  })
  it('reads the level, the other fields, and why logs could not be read', () => {
    expect(logLevel({ level: 'ERROR' })).toBe('error')
    expect(logLevel({ level: 'error' })).toBe('error')
    expect(logLevel({ level: 'WARN' })).toBe('warn')
    expect(logLevel({ level: 'WARN+2' })).toBe('warn')
    expect(logAttrs({ attrs: '{"user":"[redacted]"}' })).toBe('{"user":"[redacted]"}')
    expect(logAttrs({ attrs: '{}' })).toBe('')
    expect(logAttrs({})).toBe('')
    expect(logsReason('')).toBeNull()
    expect(logsReason(undefined)).toBeNull()
    expect(logsReason('unreachable')).toBe('unreachable')
    expect(logsReason('not configured')).toBe('not_configured')
    expect(logsReason('rpc error: code = Unimplemented')).toBe('other')
  })
})

describe('backups', () => {
  it('reads core’s answer: not installed, unreachable, or the list', () => {
    expect(backupsState({ installed: false })).toBe('not_installed')
    expect(backupsState({ installed: true, error: 'unreachable' })).toBe('unreachable')
    expect(backupsState({ installed: true })).toBe('ok')
    // the service's own list has no installed field: still a list
    expect(backupsState({})).toBe('ok')
    expect(backupsState({ error: 'unreachable' })).toBe('unreachable')
    expect(INSTALL_COMMAND).toBe('bobres install')
  })
  it('reads times as unix seconds, from core or from the service', () => {
    expect(unixOf(1_790_000_000)).toBe(1_790_000_000)
    expect(unixOf('2026-10-11T03:30:00Z')).toBe(Date.UTC(2026, 9, 11, 3, 30) / 1000)
    expect(unixOf(0)).toBeNull()
    expect(unixOf(null)).toBeNull()
    expect(unixOf(undefined)).toBeNull()
    expect(unixOf('')).toBeNull()
    expect(unixOf('soon')).toBeNull()
  })
  it('says whether each backup was sent, kept on the server only, or failed', () => {
    const b = (over: Partial<BackupFile>): BackupFile => ({ file: 'f', size: 1, sha256: '', created_at: 1, sent_at: null, ...over })
    expect(backupSent(b({ sent_at: 1_790_000_000 }))).toBe('sent')
    expect(backupSent(b({ sent_at: '2026-10-11T03:31:00Z' }))).toBe('sent')
    expect(backupSent(b({ telegram_error: 'too_big' }))).toBe('not_sent')
    expect(backupSent(b({}))).toBe('not_sent')
    expect(backupSent(b({ error: 'pg_dump: connection refused', sent_at: 5 }))).toBe('failed')
  })
  it('explains why a backup was not sent', () => {
    expect(notSentReason('')).toBeNull()
    expect(notSentReason(undefined)).toBeNull()
    expect(notSentReason('too_big')).toEqual({ reason: 'too_big', detail: '' })
    expect(notSentReason('no_owner')).toEqual({ reason: 'no_owner', detail: '' })
    expect(notSentReason('telegram_error: Bad Request: file is too big')).toEqual({ reason: 'telegram_error', detail: 'Bad Request: file is too big' })
    expect(notSentReason('the bot is unreachable: dial tcp: refused')).toEqual({ reason: 'unreachable', detail: 'dial tcp: refused' })
    expect(notSentReason('the bot answered HTTP 502')).toEqual({ reason: 'other', detail: 'the bot answered HTTP 502' })
    for (const r of ['too_big', 'no_owner', 'busy', 'blocked', 'telegram_error', 'unreachable', 'other']) {
      expect((fa.system.backups.reason as Record<string, string>)[r]).toBeTruthy()
    }
  })
})

describe('system page', () => {
  const adminPerms = ['overview.read', 'users.read', 'plans.write', 'settings.write', 'servers.write', 'audit.read', 'system.read']
  const ownerPerms = [...adminPerms, 'gateways.write', 'staff.write', 'system.write', 'license.write']
  const supportPerms = ['overview.read', 'users.read', 'services.read', 'tickets.write']
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const text = (path: string) => (path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa) as string | undefined) ?? path
  const fill = (s: string, params: Record<string, string>) => Object.entries(params).reduce((out, [k, v]) => out.replace('{' + k + '}', v), s)
  const nowSec = () => Math.floor(Date.now() / 1000)

  const systemReply = (): SystemReply => ({
    version: 'v1.4.0',
    version_mismatch: true,
    checked_at: nowSec(),
    services: [
      svc('backup', { status: 'not_installed', version: '', checks: null }),
      svc('provisioner', { status: 'down', version: '', checks: null, detail: 'dial tcp 172.18.0.5:8080: connect: connection refused' }),
      svc('payments', { status: 'not_ready', checks: { db: 'ok', grpc: 'fail' }, version: '1.4.0', detail: 'grpc failed' }),
      svc('bot', { version: 'v1.3.9', checks: { redis: 'ok' }, version_mismatch: true }),
      svc('core', { checks: { db: 'ok', grpc: 'ok' } }),
    ],
    servers: [
      { id: 's1', name: 'de-1', healthy: true, detail: '', clients: 12, enabled: true },
      { id: 's2', name: 'nl-1', healthy: false, detail: 'i/o timeout', clients: 3, enabled: true },
      { id: 's3', name: 'old', healthy: false, detail: 'refused', clients: 0, enabled: false },
    ],
    gateways: [
      { provider: 'zarinpal', enabled: true, healthy: true, ready: true },
      { provider: 'manual_card', enabled: false, healthy: true },
    ],
    updates: { current: '1.4.0', latest: '1.5.0', newer: true, verified: false, checked_at: nowSec() - 3600, error: '' },
  })
  // as core's licenseView answers
  const missing = (): LicenseInfo => ({
    state: 'missing', reason: '', install_id: iid, domain: 'shop.example.com', keys: 1, license: null,
    binding: null, entitlements: null, updated_at: null, updated_by: null,
  })
  const active = (): LicenseInfo => ({
    state: 'active', reason: '', install_id: iid, domain: 'shop.example.com', keys: 1, license: { ...token(nowSec()), grace_ends_at: nowSec() + 54 * DAY },
    binding: { domain: 'match', install_id: 'unbound' }, entitlements: ents(), updated_at: nowSec() - DAY, updated_by: { id: 'u1', username: 'boss' },
  })

  type Answer = (url: string, init: RequestInit | undefined, body: Record<string, unknown>) => Response | Promise<Response> | undefined
  interface Boot {
    perms?: string[]
    role?: string
    license?: { state: string; white_label: boolean; powered_by?: boolean } | null
    loggedIn?: boolean
    brand?: Record<string, unknown>
    answer?: Answer
  }
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
  async function boot(path: string, o: Boot = {}) {
    unmount()
    vi.resetModules()
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    const me = () => ({
      user: { id: 'u1', telegram_id: 1, username: 'boss', role: o.role ?? 'admin', language: 'fa' }, permissions: o.perms ?? adminPerms, csrf: 'c',
      session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
      ...(o.license ? { license: o.license } : {}),
    })
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const raw = typeof init?.body === 'string' ? init.body : ''
        calls.push(`${init?.method ?? 'GET'} ${url} ${raw}`.trim())
        const own = o.answer?.(url, init, raw ? (JSON.parse(raw) as Record<string, unknown>) : {})
        if (own) return own
        const get = (init?.method ?? 'GET') === 'GET'
        if (url === '/api/v1/me') return o.loggedIn === false ? json(401, { error: 'unauthorized' }) : json(200, me())
        if (get && url === '/api/v1/brand') return json(200, { name: 'Shop', color: '', logo: null, ...o.brand })
        if (get && url === '/api/v1/system') return json(200, systemReply())
        if (get && url === '/api/v1/license') return json(200, missing())
        return json(503, { error: 'unavailable', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { calls, router }
  }

  afterEach(() => {
    unmount()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  function type(el: Element | null, value: string) {
    const input = el as HTMLInputElement
    input.value = value
    input.dispatchEvent(new Event('input'))
  }
  const sent = (calls: string[], method: string, path: string) =>
    calls.filter((c) => c.startsWith(`${method} /api/v1${path} `)).map((c) => JSON.parse(c.slice(`${method} /api/v1${path} `.length)) as Record<string, unknown>)
  const found = async <T extends Element>(selector: string) =>
    vi.waitFor(() => {
      const el = document.querySelector<T>(selector)
      expect(el, selector).not.toBeNull()
      return el!
    })
  const rows = () => [...document.querySelectorAll('.services-health .p-datatable-tbody tr')] as HTMLElement[]
  const btn = (selector: string) => document.querySelector(selector) as HTMLButtonElement

  it('shows every service with its version, health and checks', async () => {
    const { calls } = await boot('/admin/system')
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    expect(calls).toContain('GET /api/v1/system')
    // the license loads only when its tab opens
    expect(calls.some((c) => c.includes('/api/v1/license'))).toBe(false)
    const [core, bot, payments, prov, backup] = rows()
    expect(core.querySelector('.service-name')?.textContent).toBe(text('system.service.core'))
    expect(core.querySelector('.service-state')?.textContent).toBe(text('system.state.ok'))
    expect(core.querySelector('.version')?.textContent).toBe('v1.4.0')
    expect([...core.querySelectorAll('.check.pass')].map((c) => c.textContent)).toEqual([text('system.check.db'), text('system.check.grpc')])
    // the bot runs another release: marked, and named in the warning
    expect(bot.querySelector('.mismatch')?.textContent).toBe(text('system.mismatch'))
    expect(core.querySelector('.mismatch')).toBeNull()
    expect(payments.querySelector('.mismatch')).toBeNull()
    const banner = document.querySelector('.mismatch-banner')?.textContent ?? ''
    expect(banner).toContain(fill(text('system.version_mismatch'), { version: '⁨v1.4.0⁩', list: text('system.service.bot') + ' ⁨v1.3.9⁩' }))
    // answering but not ready: the failed check says which
    expect(payments.querySelector('.service-state')?.textContent).toBe(text('system.state.not_ready'))
    expect(payments.querySelector('.check.fail')?.textContent).toBe(text('system.check.grpc'))
    expect(payments.querySelector('.check.fail')?.getAttribute('aria-label')).toBe(fill(text('system.check_fail'), { name: text('system.check.grpc') }))
    // not answering, with core's reason
    expect(prov.querySelector('.service-state')?.textContent).toBe(text('system.state.down'))
    expect(prov.querySelector('.error')?.textContent).toContain('connection refused')
    expect(prov.querySelector('.version')?.textContent).toBe('—')
    // the backup service is not installed yet
    expect(backup.querySelector('.service-state')?.textContent).toBe(text('system.state.not_installed'))
    expect(backup.textContent).toContain(text('system.not_installed'))
    expect(backup.querySelector('.checks')).toBeNull()

    // the servers and payment methods, as on the overview
    const servers = [...document.querySelectorAll('.servers-summary .server')] as HTMLElement[]
    expect(servers.map((s) => s.querySelector('.server-health')?.textContent)).toEqual([
      text('server.health_state.ok'), text('server.health_state.down'), text('server.health_state.off'),
    ])
    expect(servers[0].querySelector('.clients')?.textContent).toContain('۱۲')
    expect(servers[1].textContent).toContain('i/o timeout')
    // a switched-off server's old failure is not shown
    expect(servers[2].textContent).not.toContain('refused')
    const gateways = [...document.querySelectorAll('.gateways-summary .gateway')] as HTMLElement[]
    expect(gateways.map((g) => g.textContent)).toEqual([
      text('payment.provider.zarinpal') + text('overview.gateway_state.ok'),
      text('payment.provider.manual_card') + text('overview.gateway_state.off'),
    ])
    // admins manage servers, never gateways
    expect(document.querySelector('.servers-link')).not.toBeNull()
    expect(document.querySelector('.gateways-link')).toBeNull()
    expect(document.querySelector('.sidebar')?.textContent).toContain(text('section.system.title'))

    // checks again on demand
    btn('button.refresh').click()
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/system')).toHaveLength(2))
  })

  it('says when the status cannot be read, and tries again', async () => {
    let fail = true
    const { calls } = await boot('/admin/system', {
      answer: (url) => {
        if (url === '/api/v1/system' && fail) {
          fail = false
          return json(503, { error: 'unavailable', message: 'down' })
        }
      },
    })
    const err = await found('.load-error')
    expect(err.textContent).toContain(text('system.load_error'))
    expect(err.textContent).toContain(text('error.unavailable'))
    btn('button.refresh').click()
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    expect(document.querySelector('.load-error')).toBeNull()
    expect(calls.filter((c) => c === 'GET /api/v1/system')).toHaveLength(2)
  })

  it('shows the open problems, critical first, with when each began', async () => {
    const t0 = nowSec()
    const alerts: SystemAlert[] = [
      { key: 'backup.stale', severity: 'warning', title_key: 'alert.backup_stale', args: { hours: '30' }, detail: '', since: t0 - 7200 },
      { key: 'mystery.check', severity: 'odd', title_key: 'alert.mystery', args: {}, detail: '', since: t0 - 60 },
      { key: 'service.bot', severity: 'critical', title_key: 'alert.service_down', args: { service: 'bot' }, detail: 'dial tcp 172.18.0.4:8080: connect: connection refused', since: t0 - 600 },
      // a text whose value core did not give: core's words instead of a hole
      { key: 'server.s2', severity: 'warning', title_key: 'alert.server_down', args: {}, title: 'nl-1 does not answer', detail: '', since: t0 - 30 },
    ]
    const { calls } = await boot('/admin/system', { answer: (url) => (url === '/api/v1/system' ? json(200, { ...systemReply(), alerts }) : undefined) })
    await vi.waitFor(() => expect(document.querySelectorAll('.alerts .alert')).toHaveLength(4))
    // above the services
    const cards = [...document.querySelectorAll('.health > .p-card')]
    expect(cards.indexOf(document.querySelector('.alerts-card')!)).toBeLessThan(cards.indexOf(document.querySelector('.services-card')!))
    const count = document.querySelector('.alerts-count')!
    expect(count.textContent).toBe('۴')
    expect(count.classList.contains('p-tag-danger')).toBe(true)
    expect(count.getAttribute('aria-label')).toBe(fill(text('system.alerts.count'), { n: '۴' }))
    const [bot, backup, odd, server] = [...document.querySelectorAll('.alerts .alert')] as HTMLElement[]
    // critical first, named as the bot names it, with the service as this page names it
    expect(bot.querySelector('.alert-severity')?.textContent).toBe(text('system.alerts.severity.critical'))
    expect(bot.querySelector('.alert-severity')?.classList.contains('p-tag-danger')).toBe(true)
    expect(bot.querySelector('.alert-title')?.textContent).toBe(fill(text('system.alerts.kind.service_down'), { service: '\u2068' + text('system.service.bot') + '\u2069' }))
    expect(bot.querySelector('.alert-since')?.textContent).toBe(fill(text('system.alerts.since'), { time: dateTime(t0 - 600, 'fa') }))
    expect(bot.querySelector('.alert-detail')?.textContent).toBe('dial tcp 172.18.0.4:8080: connect: connection refused')
    // then the warnings, oldest first, numbers in Persian digits
    expect(backup.querySelector('.alert-severity')?.textContent).toBe(text('system.alerts.severity.warning'))
    expect(backup.querySelector('.alert-severity')?.classList.contains('p-tag-warn')).toBe(true)
    expect(backup.querySelector('.alert-title')?.textContent).toBe(fill(text('system.alerts.kind.backup_stale'), { hours: '۳۰' }))
    expect(backup.querySelector('.alert-since')?.textContent).toBe(fill(text('system.alerts.since'), { time: dateTime(t0 - 7200, 'fa') }))
    expect(backup.querySelector('.alert-detail')).toBeNull()
    // a check this page has no text for, and an unknown severity: a warning, named by its key
    expect(odd.querySelector('.alert-severity')?.textContent).toBe(text('system.alerts.severity.warning'))
    expect(odd.querySelector('.alert-title')?.textContent).toBe(fill(text('system.alerts.kind.check'), { key: '\u2068mystery.check\u2069' }))
    expect(server.querySelector('.alert-title')?.textContent).toBe('nl-1 does not answer')
    expect(document.querySelector('.all-clear')).toBeNull()
    // where the Telegram alerts are turned on or off
    expect(document.querySelector('.alerts-note')?.textContent).toContain(text('system.alerts.note'))
    expect(document.querySelector('a.settings-link')?.getAttribute('href')).toBe('/admin/settings')
    // read only: nothing is sent from here
    expect(calls.some((c) => !c.startsWith('GET '))).toBe(false)
  })

  it('says all is clear when nothing is open, when they cannot be read, and nothing on an older core', async () => {
    await boot('/admin/system', {
      perms: adminPerms.filter((p) => p !== 'settings.write'),
      answer: (url) => (url === '/api/v1/system' ? json(200, { ...systemReply(), alerts: [] }) : undefined),
    })
    const clear = await found('.all-clear')
    expect(clear.textContent?.trim()).toBe(text('system.alerts.none'))
    expect(document.querySelector('.alerts .alert')).toBeNull()
    expect(document.querySelector('.alerts-count')).toBeNull()
    // no settings link for staff who cannot change settings
    expect(document.querySelector('.alerts-note')).not.toBeNull()
    expect(document.querySelector('a.settings-link')).toBeNull()
    // core could not read them: said, never "all clear"
    await boot('/admin/system', { answer: (url) => (url === '/api/v1/system' ? json(200, { ...systemReply(), alerts: null }) : undefined) })
    const unreadable = await found('.alerts-unreadable')
    expect(unreadable.textContent?.trim()).toBe(text('system.alerts.unreadable'))
    expect(document.querySelector('.all-clear')).toBeNull()
    expect(document.querySelector('.alerts-count')).toBeNull()
    // a core without the watchdog: no card, rather than a false all clear
    await boot('/admin/system')
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    expect(document.querySelector('.alerts-card')).toBeNull()
    expect(document.querySelector('.all-clear')).toBeNull()
  })

  it('keeps the page from support staff', async () => {
    const { router, calls } = await boot('/admin/system', { perms: supportPerms, role: 'support', license: { state: 'missing', white_label: false } })
    expect(router.currentRoute.value.name).toBe('overview')
    expect(document.querySelector('.sidebar')?.textContent).not.toContain(text('section.system.title'))
    expect(calls.some((c) => c.includes('/api/v1/system') || c.includes('/api/v1/license'))).toBe(false)
    // and no license chip for them
    expect(document.querySelector('.license-chip')).toBeNull()
  })

  it('shows a newer release with the command to install it', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { calls, router } = await boot('/admin/system')
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    btn('.tab-updates').click()
    const card = await found('.update-available')
    expect(router.currentRoute.value.query.tab).toBe('updates')
    expect(document.querySelector('.update-state')?.textContent).toBe(text('system.updates.state.newer'))
    expect(document.querySelector('.current-version')?.textContent).toBe('1.4.0')
    expect(document.querySelector('.latest-version')?.textContent).toBe('1.5.0')
    expect(card.querySelector('.available-title')?.textContent).toBe(fill(text('system.updates.available'), { version: '\u20681.5.0\u2069' }))
    expect(card.querySelector('.command')?.textContent).toBe('sudo bobres update')
    expect(document.querySelector('.updates .checked')?.textContent).toContain(text('system.updates.checked_at').split('{')[0])
    // the bot could not check the signature: said, and that the CLI will
    expect(document.querySelector('.unverified')?.textContent?.trim()).toBe(text('system.updates.unverified'))
    expect(document.querySelector('.up-to-date')).toBeNull()
    expect(document.querySelector('.not-checked')).toBeNull()
    // a bot that does not say which channel it follows: no channel shown
    expect(document.querySelector('.channel')).toBeNull()
    expect(document.querySelector('.channel-hint')).toBeNull()
    expect(document.querySelector('.not-covered')).toBeNull()
    btn('button.copy-command').click()
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith('sudo bobres update'))
    // nothing is applied from the page
    expect(calls.some((c) => !c.startsWith('GET '))).toBe(false)
    // checks again on demand
    btn('.updates button.refresh').click()
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/system')).toHaveLength(3))
  })

  it('shows the channel, and a newer release the license does not cover', async () => {
    const reply = (updates: Partial<UpdateCheck>) => (url: string) => (url === '/api/v1/system'
      ? json(200, { version: 'v1.5.0', services: [], updates: { current: '1.5.0', latest: '1.6.0-rc.2', newer: true, verified: true, checked_at: nowSec(), error: '', ...updates } })
      : undefined)
    const { calls, router } = await boot('/admin/system?tab=updates', { answer: reply({ channel: 'beta', covered: false, issued: '2027-03-01T10:04Z' }) })
    const box = await found('.not-covered')
    // the day to compare with the license's "updates until"
    expect(document.querySelector('.released')?.textContent).toBe('2027-03-01')
    expect(document.querySelector('.update-state')?.textContent).toBe(text('system.updates.state.not_covered'))
    expect(document.querySelector('.update-state')?.classList.contains('p-tag-info')).toBe(true)
    expect(box.querySelector('.available-title')?.textContent).toBe(fill(text('system.updates.not_covered'), { version: '⁨1.6.0-rc.2⁩' }))
    expect(box.querySelector('.available-hint')?.textContent).toBe(text('system.updates.not_covered_hint'))
    // bobres update would refuse it: no command to run
    expect(document.querySelector('.update-available')).toBeNull()
    expect(document.querySelector('.command')).toBeNull()
    // the beta channel, and what going back to stable means
    expect(document.querySelector('.channel-name')?.textContent).toBe(text('system.updates.channel_name.beta'))
    expect(document.querySelector('.channel-hint')?.textContent).toBe(text('system.updates.channel_hint'))
    expect(document.querySelector('.beta-note')?.textContent?.trim()).toBe(text('system.updates.beta_note'))
    // the license's updates date is on its tab
    expect(calls.some((c) => c.includes('/api/v1/license'))).toBe(false)
    ;(box.querySelector('a.license-link') as HTMLAnchorElement).click()
    await found('.license-state')
    expect(router.currentRoute.value.query.tab).toBe('license')
    expect(calls).toContain('GET /api/v1/license')

    // stable, and no valid license to ask: installable as before
    await boot('/admin/system?tab=updates', { answer: reply({ channel: 'stable', covered: null }) })
    await found('.update-available')
    expect(document.querySelector('.update-state')?.textContent).toBe(text('system.updates.state.newer'))
    expect(document.querySelector('.channel-name')?.textContent).toBe(text('system.updates.channel_name.stable'))
    expect(document.querySelector('.beta-note')).toBeNull()
    expect(document.querySelector('.not-covered')).toBeNull()
    // the bot did not say when it was signed
    expect(document.querySelector('.released')).toBeNull()
    // covered by the license
    await boot('/admin/system?tab=updates', { answer: reply({ channel: 'stable', covered: true }) })
    await found('.update-available')
    expect(document.querySelector('.not-covered')).toBeNull()
    expect(document.querySelector('.command')?.textContent).toBe('sudo bobres update')
  })

  it('says when this install is up to date', async () => {
    const { calls } = await boot('/admin/system?tab=updates', {
      answer: (url) => (url === '/api/v1/system'
        ? json(200, { version: 'v1.6.0', services: [], updates: { current: '1.6.0', latest: '1.6.0', newer: false, verified: true, checked_at: nowSec(), error: '' } })
        : undefined),
    })
    await found('.up-to-date')
    expect(document.querySelector('.update-state')?.textContent).toBe(text('system.updates.state.current'))
    expect(document.querySelector('.up-to-date')?.textContent).toBe(text('system.updates.up_to_date'))
    expect(document.querySelector('.latest-version')?.textContent).toBe('1.6.0')
    expect(document.querySelector('.update-available')).toBeNull()
    expect(document.querySelector('.unverified')).toBeNull()
    expect(document.querySelector('.not-checked')).toBeNull()
    // the health tab was never opened
    expect(document.querySelector('.services-health')).toBeNull()
    expect(calls.filter((c) => c === 'GET /api/v1/system')).toHaveLength(1)
  })

  it('says why updates could not be checked', async () => {
    const reply = (updates: Partial<UpdateCheck> | null) => (url: string) => (url === '/api/v1/system'
      ? json(200, { version: 'v1.5.0', services: [], ...(updates ? { updates: { current: '1.5.0', latest: '', newer: false, verified: false, checked_at: 0, error: '', ...updates } } : {}) })
      : undefined)
    const reason = () => document.querySelector('.not-checked .reason')?.textContent
    // GitHub unreachable
    await boot('/admin/system?tab=updates', { answer: reply({ error: 'unreachable', checked_at: nowSec() }) })
    await found('.not-checked')
    expect(document.querySelector('.update-state')?.textContent).toBe(text('system.updates.state.unknown'))
    expect(document.querySelector('.reason-title')?.textContent).toBe(text('system.updates.could_not_check'))
    expect(reason()).toBe(text('system.updates.reason.unreachable'))
    expect(document.querySelector('.current-version')?.textContent).toBe('1.5.0')
    expect(document.querySelector('.latest-version')?.textContent).toBe('—')
    expect(document.querySelector('.update-available')).toBeNull()
    expect(document.querySelector('.unverified')).toBeNull()
    // core could not ask the bot
    await boot('/admin/system?tab=updates', { answer: reply({ error: 'bot unreachable' }) })
    await vi.waitFor(() => expect(reason()).toBe(text('system.updates.reason.bot_unreachable')))
    expect(document.querySelector('.updates .checked')).toBeNull()
    // the bot's own words for anything else
    await boot('/admin/system?tab=updates', { answer: reply({ error: 'checksums.txt: no version header' }) })
    await vi.waitFor(() => expect(reason()).toBe(text('system.updates.reason.other')))
    expect(document.querySelector('.not-checked .error')?.textContent).toBe('checksums.txt: no version header')
    // not checked yet: what the bot says before its first check, not a raw code
    await boot('/admin/system?tab=updates', { answer: reply({ error: 'not_checked' }) })
    await vi.waitFor(() => expect(reason()).toBe(text('system.updates.reason.not_checked')))
    expect(document.querySelector('.not-checked .error')).toBeNull()
    await boot('/admin/system?tab=updates', { answer: reply({}) })
    await vi.waitFor(() => expect(reason()).toBe(text('system.updates.reason.not_checked')))
    // no release yet, and a release that failed the signature check (with the bot's detail)
    await boot('/admin/system?tab=updates', { answer: reply({ error: 'no_release', checked_at: nowSec(), detail: 'checksums.txt: not found' }) })
    await vi.waitFor(() => expect(reason()).toBe(text('system.updates.reason.no_release')))
    expect(document.querySelector('.not-checked .error')).toBeNull()
    await boot('/admin/system?tab=updates', {
      answer: reply({ latest: '1.6.0', error: 'bad_signature', checked_at: nowSec(), detail: 'the release is not signed (no checksums.txt.sig)' }),
    })
    await vi.waitFor(() => expect(reason()).toBe(text('system.updates.reason.bad_signature')))
    expect(document.querySelector('.not-checked .error')?.textContent).toBe('the release is not signed (no checksums.txt.sig)')
    expect(document.querySelector('.latest-version')?.textContent).toBe('1.6.0')
    expect(document.querySelector('.update-available')).toBeNull()
    expect(document.querySelector('.unverified')).toBeNull()
    // an older core: its own version, and no check
    await boot('/admin/system?tab=updates', { answer: reply(null) })
    await vi.waitFor(() => expect(reason()).toBe(text('system.updates.reason.unsupported')))
    expect(document.querySelector('.current-version')?.textContent).toBe('v1.5.0')
    // a newer release read before the last check failed: still shown, with the failure
    await boot('/admin/system?tab=updates', { answer: reply({ latest: '1.6.0', newer: true, verified: true, error: 'unreachable' }) })
    await found('.update-available')
    expect(document.querySelector('.reason-title')?.textContent).toBe(text('system.updates.last_check_failed'))
    expect(reason()).toBe(text('system.updates.reason.unreachable'))
    expect(document.querySelector('.unverified')).toBeNull()
  })

  // One moment for every reply, so the times a test expects match the ones
  // the page shows even when the clock passes a minute meanwhile.
  const backupsAt = nowSec()
  const backupsReply = (over: Partial<SystemBackups> = {}): SystemBackups => {
    const t0 = backupsAt - 3600
    return {
      installed: true,
      backups: [
        { file: 'bobres-20261011T033000Z.bkp', size: 3 * 2 ** 20, sha256: 'ab', created_at: t0, sent_at: t0 + 20 },
        { file: 'bobres-20261010T033000Z.bkp', size: 80 * 2 ** 20, sha256: 'cd', created_at: t0 - DAY, sent_at: null, telegram_error: 'too_big' },
        { file: '', size: 0, sha256: '', created_at: t0 - 2 * DAY, sent_at: null, error: 'pg_dump: connection refused' },
        { file: 'bobres-20261008T033000Z.bkp', size: 2 ** 20, sha256: 'ef', created_at: t0 - 3 * DAY, sent_at: null, telegram_error: 'telegram_error: Forbidden: bot was blocked' },
      ],
      running: false, last_error: '', next_run: backupsAt + 20 * 3600, disk_free: 12 * 2 ** 30, dir: '/backups', ...over,
    }
  }
  // the backup service's own list, which core passed on as it was (no installed field)
  const asTheServiceSays = (r: SystemBackups): SystemBackups => {
    const { installed: _installed, ...rest } = r
    return rest
  }
  // the tab reads the list again quickly while a backup is being made
  const quickPoll = async () => {
    const { backupPoll } = await import('../system')
    backupPoll.ms = 5
  }

  it('lists the backups, and lets the owner back up now until it is done', async () => {
    let state = backupsReply()
    let reads = 0
    let done = false
    const { calls, router } = await boot('/admin/system', {
      perms: ownerPerms, role: 'owner', license: { state: 'active', white_label: false },
      answer: (url, init) => {
        if (url === '/api/v1/system/backups/run' && init?.method === 'POST') {
          state = { ...state, running: true }
          return json(202, { started: true })
        }
        if (url === '/api/v1/system/backups') {
          if (state.running) reads++
          if (done) state = { ...state, running: false }
          return json(200, state)
        }
      },
    })
    await quickPoll()
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    expect(calls.some((c) => c.includes('/system/backups'))).toBe(false)
    btn('.tab-backups').click()
    await found('.backup')
    expect(router.currentRoute.value.query.tab).toBe('backups')
    const items = [...document.querySelectorAll('.backup')] as HTMLElement[]
    expect(items).toHaveLength(4)
    const r = backupsReply()
    // newest first, in the user's calendar, with the size and whether it was sent
    expect(items[0].querySelector('.time')?.textContent).toBe(dateTime(r.backups![0].created_at as number, 'fa'))
    expect(items[0].querySelector('.size')?.textContent).toBe(bytes(3 * 2 ** 20, 'fa'))
    expect(items[0].querySelector('.sent-state')?.textContent).toBe(text('system.backups.sent.sent'))
    expect(items[0].querySelector('.sent-at')?.textContent).toBe(fill(text('system.backups.sent_at'), { time: dateTime(r.backups![0].sent_at as number, 'fa') }))
    expect(items[0].querySelector('.file')?.textContent).toBe('bobres-20261011T033000Z.bkp')
    // too big for Telegram: kept on the server
    expect(items[1].querySelector('.sent-state')?.textContent).toBe(text('system.backups.sent.not_sent'))
    expect(items[1].querySelector('.reason')?.textContent).toBe(text('system.backups.reason.too_big'))
    // a failed run, with why
    expect(items[2].querySelector('.sent-state')?.textContent).toBe(text('system.backups.sent.failed'))
    expect(items[2].querySelector('.sent-state')?.classList.contains('p-tag-danger')).toBe(true)
    expect(items[2].querySelector('.error')?.textContent).toBe('pg_dump: connection refused')
    expect(items[2].querySelector('.size')).toBeNull()
    // Telegram's own words
    expect(items[3].querySelector('.reason')?.textContent).toBe(text('system.backups.reason.telegram_error'))
    expect(items[3].querySelector('.error')?.textContent).toBe('Forbidden: bot was blocked')
    // the next run, free space, and how to restore
    expect(document.querySelector('.next-run')?.textContent).toBe(dateTime(r.next_run!, 'fa'))
    expect(document.querySelector('.disk-free')?.textContent).toBe(bytes(12 * 2 ** 30, 'fa'))
    expect(document.querySelector('.dir')?.textContent).toBe('/backups')
    expect(document.querySelector('.restore-note')?.textContent).toContain(text('system.backups.restore_later'))
    expect(document.querySelector('.backup-running')).toBeNull()

    // back up now: shown as running, read again until done
    const run = btn('button.run-backup')
    expect(run.disabled).toBe(false)
    run.click()
    await vi.waitFor(() => expect(calls).toContain('POST /api/v1/system/backups/run'))
    await found('.backup-running')
    expect(btn('button.run-backup').disabled).toBe(true)
    await vi.waitFor(() => expect(reads).toBeGreaterThanOrEqual(3))
    expect(document.querySelector('.backup-running')).not.toBeNull()
    done = true
    await vi.waitFor(() => expect(document.querySelector('.backup-running')).toBeNull())
    expect(btn('button.run-backup').disabled).toBe(false)
    // and stops reading once it is done
    const n = calls.length
    await new Promise((res) => setTimeout(res, 40))
    expect(calls.length).toBe(n)
  })

  it('waits for a backup already running, and says when one cannot start', async () => {
    let running = false
    let reads = 0
    let post = 0
    let done = false
    const { calls } = await boot('/admin/system?tab=backups', {
      perms: ownerPerms, role: 'owner', license: { state: 'active', white_label: false },
      answer: (url) => {
        if (url === '/api/v1/system/backups/run') {
          if (++post === 1) return json(503, { error: 'unavailable', message: 'backup service unreachable' })
          running = true
          return json(409, { started: false, reason: 'running' })
        }
        if (url === '/api/v1/system/backups') {
          if (running) reads++
          if (done) running = false
          return json(200, asTheServiceSays(backupsReply({ backups: [], running })))
        }
      },
    })
    await quickPoll()
    const empty = await found('.backups .empty')
    expect(empty.textContent).toBe(text('system.backups.empty'))
    btn('button.run-backup').click()
    const err = await found('.run-error')
    expect(err.textContent).toContain(text('system.backups.run_error'))
    expect(document.querySelector('.backup-running')).toBeNull()
    btn('button.run-backup').click()
    await found('.backup-running')
    expect(document.querySelector('.run-error')).toBeNull()
    await vi.waitFor(() => expect(reads).toBeGreaterThanOrEqual(2))
    done = true
    await vi.waitFor(() => expect(document.querySelector('.backup-running')).toBeNull())
    expect(calls.filter((c) => c.startsWith('POST ')).length).toBe(2)
  })

  it('says how to add the backup service, and when core cannot reach it', async () => {
    let reply: SystemBackups = { installed: false }
    const { calls } = await boot('/admin/system?tab=backups', {
      answer: (url) => (url === '/api/v1/system/backups' ? json(200, reply) : undefined),
    })
    const box = await found('.backups-not-installed')
    expect(box.querySelector('.title')?.textContent).toBe(text('system.backups.not_installed_title'))
    expect(box.querySelector('.hint')?.textContent).toBe(text('system.backups.not_installed'))
    expect(box.querySelector('.command')?.textContent).toBe('bobres install')
    expect(document.querySelector('.backup-list')).toBeNull()
    expect(document.querySelector('.restore-note')).toBeNull()
    // admins see the backups, but only the owner backs up
    expect(document.querySelector('button.run-backup')).toBeNull()
    reply = { installed: true, error: 'unreachable' }
    btn('.backups button.refresh').click()
    const warn = await found('.backups-unreachable')
    expect(warn.textContent?.trim()).toBe(text('system.backups.unreachable'))
    expect(document.querySelector('.backups-not-installed')).toBeNull()
    // the service's list as it answers it, without installed: shown as a list
    reply = asTheServiceSays(backupsReply())
    btn('.backups button.refresh').click()
    await found('.backup')
    expect(document.querySelectorAll('.backup')).toHaveLength(4)
    expect(document.querySelector('.backups-not-installed')).toBeNull()
    expect(document.querySelector('.next-run')).not.toBeNull()
    expect(document.querySelector('button.run-backup')).toBeNull()
    expect(calls.some((c) => !c.startsWith('GET '))).toBe(false)
  })

  it('shows the owner each service’s recent warnings and errors, newest first', async () => {
    const t0 = nowSec() - 600
    const logs: Record<string, SystemLogs> = {
      core: {
        service: 'core',
        lines: [
          { time: t0, level: 'WARN', msg: 'slow query', attrs: '{"ms":1200}' },
          { time: t0 + 60, level: 'ERROR', msg: 'payment webhook failed', attrs: '{}' },
        ],
        error: '',
      },
      bot: { service: 'bot', lines: [], error: '' },
      payments: { service: 'payments', lines: null, error: 'unreachable' },
      provisioner: { service: 'provisioner', lines: [], error: 'rpc error: code = Unimplemented' },
    }
    const { calls, router } = await boot('/admin/system', {
      perms: ownerPerms, role: 'owner', license: { state: 'active', white_label: false },
      answer: (url) => {
        const m = /^\/api\/v1\/system\/logs\?service=(\w+)$/.exec(url)
        return m ? json(200, logs[m[1]]) : undefined
      },
    })
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    // nothing read before the tab opens
    expect(calls.some((c) => c.includes('/system/logs'))).toBe(false)
    btn('.tab-logs').click()
    await found('.log-line')
    expect(router.currentRoute.value.query.tab).toBe('logs')
    expect(calls).toContain('GET /api/v1/system/logs?service=core')
    expect(document.querySelector('.logs .note')?.textContent?.trim()).toBe(text('system.logs.note'))
    // English, left to right inside the right-to-left page, newest first
    const list = document.querySelector('.log-lines') as HTMLElement
    expect(document.documentElement.getAttribute('dir')).toBe('rtl')
    expect(list.getAttribute('dir')).toBe('ltr')
    const lines = [...list.querySelectorAll('.log-line')] as HTMLElement[]
    expect(lines.map((l) => l.querySelector('.msg')?.textContent)).toEqual(['payment webhook failed', 'slow query'])
    expect(lines.map((l) => l.querySelector('.level')?.textContent)).toEqual(['ERROR', 'WARN'])
    expect(lines[0].querySelector('.level')?.classList.contains('p-tag-danger')).toBe(true)
    expect(lines[0].querySelector('.attrs')).toBeNull()
    expect(lines[1].querySelector('.attrs')?.textContent).toBe('{"ms":1200}')
    expect(lines[1].querySelector('.msg')?.classList.contains('app-mono')).toBe(true)
    expect(document.querySelector('.logs .empty')).toBeNull()

    const pick = (name: string) => {
      const label = text('system.service.' + name)
      const b = [...document.querySelectorAll('.log-service button')].find((x) => x.textContent?.trim() === label) as HTMLButtonElement
      b.click()
    }
    // nothing went wrong since the bot started
    pick('bot')
    const empty = await found('.logs .empty')
    expect(empty.textContent?.trim()).toBe(text('system.logs.empty'))
    expect(calls).toContain('GET /api/v1/system/logs?service=bot')
    expect(document.querySelector('.log-line')).toBeNull()
    // core could not ask the service
    pick('payments')
    const unread = await found('.logs-unread')
    expect(unread.querySelector('.reason')?.textContent).toBe(fill(text('system.logs.reason.unreachable'), { service: text('system.service.payments') }))
    expect(document.querySelector('.logs .empty')).toBeNull()
    // the service's own words for anything else
    pick('provisioner')
    await vi.waitFor(() => expect(document.querySelector('.logs-unread .error')?.textContent).toBe('rpc error: code = Unimplemented'))
    expect(document.querySelector('.logs-unread .reason')?.textContent).toBe(fill(text('system.logs.reason.other'), { service: text('system.service.provisioner') }))
    // read again on demand; nothing is ever changed from here
    btn('.logs button.refresh').click()
    await vi.waitFor(() => expect(calls.filter((c) => c === 'GET /api/v1/system/logs?service=provisioner')).toHaveLength(2))
    expect(calls.some((c) => !c.startsWith('GET '))).toBe(false)
  })

  it('says when the logs cannot be read, and tries again', async () => {
    let fail = true
    await boot('/admin/system?tab=logs', {
      perms: ownerPerms, role: 'owner', license: { state: 'active', white_label: false },
      answer: (url) => {
        if (!url.startsWith('/api/v1/system/logs')) return undefined
        if (fail) {
          fail = false
          return json(503, { error: 'unavailable', message: 'down' })
        }
        return json(200, { service: 'core', lines: [], error: '' })
      },
    })
    const err = await found('.logs .load-error')
    expect(err.textContent).toContain(text('system.logs.load_error'))
    btn('.logs button.refresh').click()
    await found('.logs .empty')
    expect(document.querySelector('.logs .load-error')).toBeNull()
  })

  it('keeps the logs from admins', async () => {
    // even from a link to the tab: the health tab instead
    const { calls } = await boot('/admin/system?tab=logs')
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    expect(document.querySelector('.tab-health')?.getAttribute('aria-selected')).toBe('true')
    expect(document.querySelector('.tab-logs')).toBeNull()
    expect(document.querySelector('.logs')).toBeNull()
    expect(calls.some((c) => c.includes('/system/logs'))).toBe(false)
  })

  it('shows admins the license read only, with this install’s id to copy', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { calls, router } = await boot('/admin/system', { license: { state: 'missing', white_label: false } })
    await vi.waitFor(() => expect(rows()).toHaveLength(5))
    // the tab says the license needs a look
    expect(document.querySelector('.tab-license .tab-state')?.textContent).toBe(text('license.state.missing'))
    btn('.tab-license').click()
    await found('.license-state')
    expect(router.currentRoute.value.query.tab).toBe('license')
    expect(calls).toContain('GET /api/v1/license')
    expect(document.querySelector('.license-state')?.textContent).toBe(text('license.state.missing'))
    expect(document.querySelector('.state-hint')?.textContent).toBe(text('license.state_hint.missing'))
    expect(document.querySelector('.install-id')?.textContent).toBe(iid)
    expect(document.querySelector('.this-domain')?.textContent).toBe('shop.example.com')
    // nothing granted, no binding to compare
    expect(document.querySelector('.entitlements')).toBeNull()
    expect(document.querySelector('.binding-domain')).toBeNull()
    // only the owner adds a license, online or pasted
    expect(document.querySelector('.license-import')).toBeNull()
    expect(document.querySelector('.license-activate')).toBeNull()
    expect(document.querySelector('.owner-only')?.textContent).toBe(text('license.owner_only'))
    btn('button.copy-install').click()
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(iid))
    // the health tab stays loaded: no second request going back
    btn('.tab-health').click()
    await vi.waitFor(() => expect(router.currentRoute.value.query.tab).toBe('health'))
    expect(calls.filter((c) => c === 'GET /api/v1/system')).toHaveLength(1)
  })

  it('explains a license that was not accepted', async () => {
    await boot('/admin/system?tab=license', {
      answer: (url) => (url === '/api/v1/license' ? json(200, { ...missing(), state: 'invalid', reason: 'unknown_key', keys: 0, updated_at: nowSec(), updated_by: { id: 'u1', username: '' } }) : undefined),
    })
    await found('.license-state')
    expect(document.querySelector('.license-state')?.textContent).toBe(text('license.state.invalid'))
    expect(document.querySelector('.reason')?.textContent).toBe(text('license.reason.unknown_key'))
    // this build has no vendor key yet: said plainly
    expect(document.querySelector('.no-keys')?.textContent).toBe(text('license.no_keys'))
    // saved by a staff member without a username: their id
    expect(document.querySelector('.updated')?.textContent).toContain('\u2068u1\u2069')
    // the health tab was never opened
    expect(document.querySelector('.services-health')).toBeNull()
  })

  it('shows what a valid license grants against what is used', async () => {
    await boot('/admin/system?tab=license', {
      perms: ownerPerms, role: 'owner', license: { state: 'active', white_label: false },
      answer: (url) => (url === '/api/v1/license' ? json(200, active()) : undefined),
    })
    await found('.entitlements')
    expect(document.querySelector('.license-state')?.textContent).toBe(text('license.state.active'))
    expect(document.querySelector('.tab-license .tab-state')).toBeNull()
    expect(document.querySelector('.tier')?.textContent).toBe(text('license.tier.standard'))
    expect(document.querySelector('.license-id')?.textContent).toBe('lic_7Q2')
    expect(document.querySelector('.kid')?.textContent).toBe('lic-2026a')
    expect(document.querySelector('.days-left')?.textContent).toContain(fill(text('license.left.left'), { n: '۴۰' }))
    expect(document.querySelector('.grace-ends')).not.toBeNull()
    expect(document.querySelector('.updated')?.textContent).toContain('\u2068@boss\u2069')
    expect(document.querySelector('.no-keys')).toBeNull()
    // a core that does not say how it came or when it renewed, and a token to the license's end
    expect(document.querySelector('.source')).toBeNull()
    expect(document.querySelector('.refreshed')).toBeNull()
    expect(document.querySelector('.license-ends')).toBeNull()
    expect(document.querySelector('.renews-hint')).toBeNull()
    expect(document.querySelector('.expires')?.previousElementSibling?.textContent).toBe(text('license.expires'))
    // bound to this domain (as core compares it), not to an install
    expect(document.querySelector('.binding-domain')?.textContent).toContain(text('license.binding_state.match'))
    expect(document.querySelector('.binding-domain')?.textContent).toContain('Shop.Example.com.')
    expect(document.querySelector('.binding-install')?.textContent).toContain(text('license.binding_state.unbound'))
    const ent = (key: string) => document.querySelector('.ent-' + key) as HTMLElement
    expect(ent('servers').querySelector('.use')?.textContent).toBe(fill(text('license.usage'), { used: '۳', limit: '۵' }))
    expect(ent('servers').querySelector('.limit-state')).toBeNull()
    expect(ent('active_services').querySelector('.limit-state')?.textContent).toBe(text('license.limit_state.near'))
    expect(ent('staff_seats').querySelector('.limit-state')?.textContent).toBe(text('license.limit_state.full'))
    expect(ent('gateways').querySelector('.outside')?.textContent).toContain(text('payment.provider.stars'))
    expect(ent('gateways').querySelector('.allowed')?.textContent).toBe(text('payment.provider.zarinpal') + '، ' + text('payment.provider.manual_card'))
    expect(ent('resellers').querySelector('.feature')?.textContent).toBe(text('license.feature.on'))
    expect(ent('white_label').querySelector('.feature')?.textContent).toBe(text('license.feature.off'))
    expect(ent('updates_until').textContent).toContain('2027-09-30')
    expect(document.querySelector('.limits-warn')?.textContent).toBe(text('license.warnings'))
    // the owner may replace it, online or with a token
    expect(document.querySelector('.license-activate')?.textContent).toContain(text('license.activate.title'))
    expect(document.querySelector('.license-import')?.textContent).toContain(text('license.import.replace'))
    // a valid license without white label shows the attribution (with the
    // little beaver), and no chip
    expect(document.querySelector('.powered-by')?.textContent).toBe(text('license.powered_by'))
    expect(document.querySelector('.powered-by img')?.getAttribute('src')).toBe(import.meta.env.BASE_URL + 'favicon.svg')
    expect(document.querySelector('.license-chip')).toBeNull()
  })

  it('lets the owner check a token, then save it with a reason', async () => {
    const good = 'eyJ2IjoxLCJsaWNlbnNlX2lkIjoibGljXzdRMiJ9.c2lnbmF0dXJl'
    const bad = 'eyJ2IjoxfQ.YmFk'
    let saved = false
    const { calls } = await boot('/admin/system?tab=license', {
      perms: ownerPerms, role: 'owner', license: { state: 'missing', white_label: false },
      answer: (url, init, b) => {
        if (url === '/api/v1/license/check') {
          if (b.token === bad) return json(200, { ...missing(), state: 'invalid', reason: 'bad_signature' })
          return json(200, { ...active(), updated_at: undefined })
        }
        if (url === '/api/v1/license' && init?.method === 'PUT') {
          saved = true
          return json(200, active())
        }
        if (url === '/api/v1/license' && saved) return json(200, active())
        if (url === '/api/v1/me' && saved) {
          return json(200, {
            user: { id: 'u1', telegram_id: 1, username: 'boss', role: 'owner', language: 'fa' }, permissions: ownerPerms, csrf: 'c',
            session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
            license: { state: 'active', white_label: false },
          })
        }
      },
    })
    const card = await found('.license-import')
    expect(card.textContent).toContain(text('license.import.add'))
    // the menu shows the install is unlicensed, and there is no attribution
    expect(document.querySelector('.license-chip')?.textContent).toBe(text('license.chip.missing'))
    expect(document.querySelector('.license-chip')?.getAttribute('href')).toBe('/admin/system?tab=license')
    expect(document.querySelector('.powered-by')).toBeNull()

    const area = card.querySelector('textarea.token-input')
    const check = () => btn('.license-import button.check')
    expect(check().disabled).toBe(true)
    type(area, 'not a token')
    await vi.waitFor(() => expect(card.querySelector('.token-problem')?.textContent).toBe(text('license.import.problem.format')))
    expect(check().disabled).toBe(true)

    // a refused token: why, and no way to save it
    type(area, bad)
    await vi.waitFor(() => expect(check().disabled).toBe(false))
    check().click()
    await found('.check-result')
    expect(sent(calls, 'POST', '/license/check')).toEqual([{ token: bad }])
    expect(card.querySelector('.result-state')?.textContent).toBe(text('license.state.invalid'))
    expect(card.querySelector('.result-reason')?.textContent).toBe(text('license.reason.bad_signature'))
    expect(card.querySelector('button.save')).toBeNull()

    // a good one, pasted with line breaks: checked as one token
    type(area, good.slice(0, 20) + '\n  ' + good.slice(20) + '\n')
    // the old result belongs to the old token
    await vi.waitFor(() => expect(card.querySelector('.check-result')).toBeNull())
    check().click()
    await vi.waitFor(() => expect(card.querySelector('.result-state')?.textContent).toBe(text('license.state.active')))
    expect(sent(calls, 'POST', '/license/check')[1]).toEqual({ token: good })
    expect(card.querySelector('.result-tier')?.textContent).toBe(text('license.tier.standard'))
    // nothing is saved by a check
    expect(calls.some((c) => c.startsWith('PUT '))).toBe(false)

    btn('.license-import button.save').click()
    const dialog = await found('.p-dialog')
    expect(dialog.textContent).toContain(text('license.import.confirm_message'))
    const confirm = () => [...dialog.querySelectorAll('.p-dialog-footer button')].pop() as HTMLButtonElement
    expect(confirm().disabled).toBe(true)
    type(dialog.querySelector('textarea'), 'bought the standard tier')
    await vi.waitFor(() => expect(confirm().disabled).toBe(false))
    confirm().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/license')).toEqual([{ token: good, reason: 'bought the standard tier' }]))
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())
    // the page, the chip and the footer follow the new license
    await vi.waitFor(() => expect(document.querySelector('.license-state')?.textContent).toBe(text('license.state.active')))
    expect(calls.filter((c) => c === 'GET /api/v1/me').length).toBeGreaterThanOrEqual(2)
    await vi.waitFor(() => expect(document.querySelector('.powered-by')?.textContent).toBe(text('license.powered_by')))
    expect(document.querySelector('.license-chip')).toBeNull()
    expect((document.querySelector('textarea.token-input') as HTMLTextAreaElement).value).toBe('')
    expect(document.querySelector('.check-result')).toBeNull()
  })

  it('shows why core refused to save a token', async () => {
    const tok = 'eyJ2IjoxfQ.c2ln'
    const { calls } = await boot('/admin/system?tab=license', {
      perms: ownerPerms, role: 'owner',
      answer: (url, init) => {
        if (url === '/api/v1/license/check') return json(200, { ...active(), state: 'grace' })
        if (url === '/api/v1/license' && init?.method === 'PUT') return json(400, { error: 'invalid', field: 'token', code: 'domain', message: 'token: the license is for another domain' })
      },
    })
    const card = await found('.license-import')
    type(card.querySelector('textarea.token-input'), tok)
    await vi.waitFor(() => expect(btn('.license-import button.check').disabled).toBe(false))
    btn('.license-import button.check').click()
    await vi.waitFor(() => expect(card.querySelector('.result-state')?.textContent).toBe(text('license.state.grace')))
    btn('.license-import button.save').click()
    const dialog = await found('.p-dialog')
    type(dialog.querySelector('textarea'), 'renewal')
    const confirm = () => [...dialog.querySelectorAll('.p-dialog-footer button')].pop() as HTMLButtonElement
    await vi.waitFor(() => expect(confirm().disabled).toBe(false))
    confirm().click()
    await vi.waitFor(() => expect(card.querySelector('.result-reason')?.textContent).toBe(text('license.reason.domain')))
    expect(sent(calls, 'PUT', '/license')).toHaveLength(1)
    expect(card.querySelector('button.save')).toBeNull()
    // nothing was saved: the page still shows no license
    expect(document.querySelector('.license-state')?.textContent).toBe(text('license.state.missing'))
  })

  it('lets the owner activate with a license key and a reason', async () => {
    const key = 'BOBRES-7K2Q-9XWM-4TPA'
    let activated = false
    const t0 = nowSec()
    const renewed = (): LicenseInfo => {
      const a = active()
      return { ...a, license: { ...a.license!, expires_at: t0 + 30 * DAY, license_ends_at: t0 + 300 * DAY }, updated_at: t0, refreshed_at: null, source: 'activation' }
    }
    const { calls } = await boot('/admin/system?tab=license', {
      perms: ownerPerms, role: 'owner', license: { state: 'missing', white_label: false },
      answer: (url, init) => {
        if (url === '/api/v1/license/activate' && init?.method === 'POST') {
          activated = true
          return json(200, renewed())
        }
        if (url === '/api/v1/license' && activated) return json(200, renewed())
        if (url === '/api/v1/me' && activated) {
          return json(200, {
            user: { id: 'u1', telegram_id: 1, username: 'boss', role: 'owner', language: 'fa' }, permissions: ownerPerms, csrf: 'c',
            session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop',
            license: { state: 'active', white_label: false },
          })
        }
      },
    })
    const card = await found('.license-activate')
    expect(card.textContent).toContain(text('license.activate.title'))
    // above the token box, which is for an install that cannot reach the server
    const cards = [...document.querySelectorAll('.license > .p-card')]
    expect(cards.indexOf(card)).toBeLessThan(cards.indexOf(document.querySelector('.license-import')!))
    const input = card.querySelector('input.key-input') as HTMLInputElement
    expect(input.getAttribute('autocomplete')).toBe('off')
    const activate = () => btn('.license-activate button.activate')
    expect(activate().disabled).toBe(true)
    // a token pasted here, and the license id, are not the key
    type(input, 'eyJ2IjoxfQ.c2ln')
    await vi.waitFor(() => expect(card.querySelector('.key-problem')?.textContent).toBe(text('license.activate.problem.token')))
    expect(activate().disabled).toBe(true)
    type(input, 'lic_7Q2')
    await vi.waitFor(() => expect(card.querySelector('.key-problem')?.textContent).toBe(text('license.activate.problem.format')))
    // typed with a break and a Persian digit: sent as one key
    type(input, ' BOBRES-7K2Q-9XWM-\n۴TPA ')
    await vi.waitFor(() => expect(activate().disabled).toBe(false))
    expect(card.querySelector('.key-problem')).toBeNull()
    activate().click()
    const dialog = await found('.p-dialog')
    expect(dialog.textContent).toContain(text('license.activate.confirm_message'))
    const confirm = () => [...dialog.querySelectorAll('.p-dialog-footer button')].pop() as HTMLButtonElement
    expect(confirm().disabled).toBe(true)
    type(dialog.querySelector('textarea'), 'bought the standard tier')
    await vi.waitFor(() => expect(confirm().disabled).toBe(false))
    confirm().click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/license/activate')).toEqual([{ key, reason: 'bought the standard tier' }]))
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())
    // the page, the chip and the footer follow the new license
    await vi.waitFor(() => expect(document.querySelector('.license-state')?.textContent).toBe(text('license.state.active')))
    await vi.waitFor(() => expect(document.querySelector('.license-chip')).toBeNull())
    expect(document.querySelector('.powered-by')?.textContent).toBe(text('license.powered_by'))
    // the key is gone from the page, and was never in an address
    expect((document.querySelector('input.key-input') as HTMLInputElement).value).toBe('')
    expect(calls.filter((c) => c.includes('7K2Q')).every((c) => c.startsWith('POST /api/v1/license/activate {'))).toBe(true)
    expect(calls.some((c) => c.startsWith('PUT ') || c.includes('/license/check'))).toBe(false)
    // a short token core renews online: when the license itself ends, and how it came
    expect(document.querySelector('.expires')?.previousElementSibling?.textContent).toBe(text('license.token_expires'))
    expect(document.querySelector('.license-ends')?.textContent).toBe(date(t0 + 300 * DAY, 'fa'))
    expect(document.querySelector('.renews-hint')?.textContent).toBe(text('license.renews_hint'))
    expect(document.querySelector('.source')?.textContent).toBe('· ' + text('license.source.activation'))
    // not renewed yet
    expect(document.querySelector('.refreshed')).toBeNull()
  })

  it('says when the license server last renewed the license', async () => {
    const t0 = nowSec()
    const a = active()
    await boot('/admin/system?tab=license', {
      answer: (url) => (url === '/api/v1/license'
        ? json(200, { ...a, license: { ...a.license!, license_ends_at: null }, updated_at: t0 - 3 * DAY, source: 'refresh', refreshed_at: t0 - 3600 })
        : undefined),
    })
    await found('.refreshed')
    expect(document.querySelector('.refreshed')?.textContent).toBe(fill(text('license.refreshed_at'), { time: dateTime(t0 - 3600, 'fa') }))
    expect(document.querySelector('.source')?.textContent).toBe('· ' + text('license.source.refresh'))
    // who installed it is kept
    expect(document.querySelector('.updated')?.textContent).toContain('\u2068@boss\u2069')
    // renewed daily, though the token does not say when the license ends
    expect(document.querySelector('.renews-hint')).not.toBeNull()
    expect(document.querySelector('.license-ends')).toBeNull()
    expect(document.querySelector('.expires')?.previousElementSibling?.textContent).toBe(text('license.token_expires'))
    // a token added by hand is not renewed
    await boot('/admin/system?tab=license', {
      answer: (url) => (url === '/api/v1/license' ? json(200, { ...a, source: 'manual', refreshed_at: null }) : undefined),
    })
    await found('.source')
    expect(document.querySelector('.source')?.textContent).toBe('· ' + text('license.source.manual'))
    expect(document.querySelector('.renews-hint')).toBeNull()
    expect(document.querySelector('.refreshed')).toBeNull()
    expect(document.querySelector('.expires')?.previousElementSibling?.textContent).toBe(text('license.expires'))
  })

  it('shows why a key was refused, and when the license server cannot be reached', async () => {
    const moved = 'license server: this license key has moved to another install 3 times in the last year (the most allowed); the next move is possible on 2027-03-01, or ask the vendor'
    // as core answers them
    const answers = [
      json(400, { error: 'refused', field: 'key', code: 'reactivation_limit', message: moved }),
      json(400, { error: 'invalid', field: 'token', code: 'domain', message: 'token: the license is for another domain' }),
      json(400, { error: 'refused', field: 'key', code: 'suspended', message: 'license server: the license is suspended' }),
      json(502, { error: 'unavailable', code: 'unreachable', message: 'the bot did not answer' }),
      json(503, { error: 'unavailable', code: 'not_configured', message: 'no license server is set (BOBRES_LICENSE_SERVER on the bot)' }),
      json(409, { error: 'state', message: 'this install has no domain (BOBRES_DOMAIN), and a license is bound to one' }),
    ]
    const { calls } = await boot('/admin/system?tab=license', {
      perms: ownerPerms, role: 'owner',
      answer: (url) => (url === '/api/v1/license/activate' ? answers.shift() : undefined),
    })
    const card = await found('.license-activate')
    const input = card.querySelector('input.key-input')
    const reason = () => card.querySelector('.key-refused .key-reason')?.textContent
    async function tryKey(key: string) {
      type(input, key)
      await vi.waitFor(() => expect(btn('.license-activate button.activate').disabled).toBe(false))
      btn('.license-activate button.activate').click()
      const dialog = await found('.p-dialog')
      type(dialog.querySelector('textarea'), 'moved to a new server')
      const confirm = [...dialog.querySelectorAll('.p-dialog-footer button')].pop() as HTMLButtonElement
      await vi.waitFor(() => expect(confirm.disabled).toBe(false))
      confirm.click()
      return dialog
    }
    const cancel = async (dialog: Element) => {
      ;([...dialog.querySelectorAll('.p-dialog-footer button')][0] as HTMLButtonElement).click()
      await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())
    }
    // refused by the license server: said next to the key, with its date, the dialog closed
    await tryKey('BOBRES-AAAA-BBBB')
    await vi.waitFor(() => expect(reason()).toBe(text('license.activate.reason.reactivation_limit')))
    await vi.waitFor(() => expect(document.querySelector('.p-dialog')).toBeNull())
    expect(card.querySelector('.key-refused .detail')?.textContent).toBe(moved)
    expect(card.querySelector('input.key-input')?.getAttribute('aria-describedby')).toBe('license-key-refused')
    // typing again hides it: it belongs to the key it refused
    type(input, 'BOBRES-AAAA-BBBC')
    await vi.waitFor(() => expect(card.querySelector('.key-refused')).toBeNull())
    // the license it answered does not verify here: as for a pasted token
    await tryKey('BOBRES-CCCC-DDDD')
    await vi.waitFor(() => expect(reason()).toBe(text('license.reason.domain')))
    expect(card.querySelector('.key-refused .detail')).toBeNull()
    // a code the page does not know: core's own words
    await tryKey('BOBRES-EEEE-FFFF')
    await vi.waitFor(() => expect(reason()).toBe(text('license.activate.reason.other')))
    expect(card.querySelector('.key-refused .detail')?.textContent).toBe('license server: the license is suspended')
    // the bot or the license server did not answer: said in the dialog, with core's words
    let dialog = await tryKey('BOBRES-GGGG-HHHH')
    await vi.waitFor(() => expect(dialog.querySelector('.msg')?.textContent).toContain(text('license.activate.unreachable')))
    expect(dialog.querySelector('.msg .detail')?.textContent).toBe('the bot did not answer')
    expect(card.querySelector('.key-refused')).toBeNull()
    await cancel(dialog)
    // no license server set up on this install
    dialog = await tryKey('BOBRES-GGGG-HHHH')
    await vi.waitFor(() => expect(dialog.querySelector('.msg')?.textContent).toContain(text('license.activate.not_configured')))
    expect(dialog.querySelector('.msg .detail')?.textContent).toBe('no license server is set (BOBRES_LICENSE_SERVER on the bot)')
    await cancel(dialog)
    // no domain to bind it to: core's words
    dialog = await tryKey('BOBRES-GGGG-HHHH')
    await vi.waitFor(() => expect(dialog.querySelector('.msg')?.textContent).toContain(text('error.state')))
    expect(dialog.querySelector('.msg .detail')?.textContent).toBe('this install has no domain (BOBRES_DOMAIN), and a license is bound to one')
    expect(sent(calls, 'POST', '/license/activate').map((b) => b.key)).toEqual([
      'BOBRES-AAAA-BBBB', 'BOBRES-CCCC-DDDD', 'BOBRES-EEEE-FFFF', 'BOBRES-GGGG-HHHH', 'BOBRES-GGGG-HHHH', 'BOBRES-GGGG-HHHH',
    ])
    // nothing was installed: the page still shows no license
    expect(document.querySelector('.license-state')?.textContent).toBe(text('license.state.missing'))
    expect(calls.filter((c) => c === 'GET /api/v1/license')).toHaveLength(1)
  })

  it('says an expired license cannot be saved, and reads a token file', async () => {
    const tok = 'eyJ2IjoxfQ.ZXhwaXJlZA'
    await boot('/admin/system?tab=license', {
      perms: ownerPerms, role: 'owner',
      answer: (url) => (url === '/api/v1/license/check' ? json(200, { ...active(), state: 'expired' }) : undefined),
    })
    const card = await found('.license-import')
    const input = card.querySelector('input[type="file"]') as HTMLInputElement
    // too big to be a license: refused without reading it
    Object.defineProperty(input, 'files', { value: [new File([new Uint8Array(64 * 1024 + 1)], 'big.lic')], configurable: true })
    input.dispatchEvent(new Event('change'))
    await vi.waitFor(() => expect(card.querySelector('.file-error')?.textContent).toBe(text('license.import.file_too_big')))
    Object.defineProperty(input, 'files', { value: [new File([tok + '\n'], 'shop.lic', { type: 'text/plain' })], configurable: true })
    input.dispatchEvent(new Event('change'))
    await vi.waitFor(() => expect((card.querySelector('textarea.token-input') as HTMLTextAreaElement).value).toBe(tok))
    expect(card.querySelector('.file-error')).toBeNull()
    await vi.waitFor(() => expect(btn('.license-import button.check').disabled).toBe(false))
    btn('.license-import button.check').click()
    await found('.expired-warn')
    expect(card.querySelector('.expired-warn')?.textContent).toBe(text('license.import.expired_warn'))
    // core installs no expired license: nothing to save
    expect(card.querySelector('button.save')).toBeNull()
  })

  it('hides the attribution under white label, and on older cores', async () => {
    await boot('/admin/system?tab=license', { license: { state: 'active', white_label: true } })
    await found('.license-state')
    expect(document.querySelector('.powered-by')).toBeNull()
    expect(document.querySelector('.license-chip')).toBeNull()
    // a license in grace needs a look: the chip says so
    await boot('/admin/', { license: { state: 'grace', white_label: false } })
    await found('.license-chip')
    expect(document.querySelector('.license-chip')?.textContent).toBe(text('license.chip.grace'))
    expect(document.querySelector('.powered-by')?.textContent).toBe(text('license.powered_by'))
    // a core without the license field: neither
    await boot('/admin/', { license: null })
    await found('.sidebar')
    expect(document.querySelector('.license-chip')).toBeNull()
    expect(document.querySelector('.powered-by')).toBeNull()
  })

  it('shows the attribution on the login page only when core says so', async () => {
    await boot('/admin/login', { loggedIn: false, brand: { powered_by: true } })
    await vi.waitFor(() => expect(document.querySelector('.login .powered-by')?.textContent).toBe(text('license.powered_by')))
    expect(document.querySelector('.login .powered-by img')?.getAttribute('src')).toBe(import.meta.env.BASE_URL + 'favicon.svg')
    await boot('/admin/login', { loggedIn: false })
    await found('.login')
    await new Promise((r) => setTimeout(r, 20))
    expect(document.querySelector('.powered-by')).toBeNull()
  })
})
