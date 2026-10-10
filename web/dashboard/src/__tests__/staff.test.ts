import { describe, expect, it } from 'vitest'
import { addable, auditLink, deviceKind, deviceOf, entityIdLabel, isLocked, loginState, roleOptions, staffActions } from '../staff'
import type { StaffItem } from '../types'

const now = 1_760_000_000

function staff(over: Partial<StaffItem> = {}, password: Partial<StaffItem['password']> = {}): StaffItem {
  return {
    id: '0b6f0f3e-6a39-4c55-9a1e-2b9a1c0d4e5f',
    telegram_id: 42,
    username: 'ali',
    language: 'fa',
    role: 'admin',
    status: 'active',
    configured_owner: false,
    me: false,
    created_at: now - 86_400,
    password: { state: 'off', username: '', locked_until: null, failed_attempts: 0, ...password },
    last_login: null,
    sessions: 0,
    ...over,
  }
}

describe('deviceOf', () => {
  const cases: [string, string, string][] = [
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36', 'Chrome', 'Windows'],
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Edg/129.0.2792.52', 'Edge', 'Windows'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15', 'Safari', 'macOS'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 14.6; rv:130.0) Gecko/20100101 Firefox/130.0', 'Firefox', 'macOS'],
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Mobile/15E148 Safari/604.1', 'Safari', 'iOS'],
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/129.0.6668.46 Mobile/15E148 Safari/604.1', 'Chrome', 'iOS'],
    ['Mozilla/5.0 (iPad; CPU OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/130.0 Mobile/15E148 Safari/605.1.15', 'Firefox', 'iPadOS'],
    ['Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/25.0 Chrome/121.0.0.0 Mobile Safari/537.36', 'Samsung Internet', 'Android'],
    ['Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36', 'Chrome', 'Android'],
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 OPR/114.0.0.0', 'Opera', 'Windows'],
    ['Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0', 'Firefox', 'Linux'],
    ['Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36', 'Chrome', 'ChromeOS'],
  ]
  it.each(cases)('reads %s', (ua, browser, os) => {
    expect(deviceOf(ua)).toEqual({ browser, os })
  })
  it('leaves unknown agents blank', () => {
    expect(deviceOf('')).toEqual({ browser: '', os: '' })
    expect(deviceOf('curl/8.7.1')).toEqual({ browser: '', os: '' })
  })
  it('picks an icon kind', () => {
    expect(deviceKind('iOS')).toBe('mobile')
    expect(deviceKind('Android')).toBe('mobile')
    expect(deviceKind('iPadOS')).toBe('tablet')
    expect(deviceKind('Windows')).toBe('desktop')
    expect(deviceKind('')).toBe('desktop')
  })
})

describe('login state', () => {
  it('is locked only while the lock runs', () => {
    expect(isLocked({ state: 'on', username: 'a', locked_until: now + 60, failed_attempts: 5 }, now)).toBe(true)
    expect(isLocked({ state: 'on', username: 'a', locked_until: now - 60, failed_attempts: 5 }, now)).toBe(false)
    expect(isLocked({ state: 'on', username: 'a', locked_until: null, failed_attempts: 0 }, now)).toBe(false)
  })
  it('shows the password state, or locked', () => {
    expect(loginState(staff(), now)).toBe('off')
    expect(loginState(staff({}, { state: 'pending' }), now)).toBe('pending')
    expect(loginState(staff({}, { state: 'on' }), now)).toBe('on')
    expect(loginState(staff({}, { state: 'on', locked_until: now + 900 }), now)).toBe('locked')
    expect(loginState(staff({}, { state: 'on', locked_until: now - 1 }), now)).toBe('on')
  })
})

describe('staffActions', () => {
  it('offers nothing on yourself or the configured owner', () => {
    expect(staffActions(staff({ me: true }), true, now)).toEqual([])
    expect(staffActions(staff({ configured_owner: true, role: 'owner' }), true, now)).toEqual([])
  })
  it('offers reset only with a password, and unlock only while locked', () => {
    expect(staffActions(staff(), false, now)).toEqual(['role', 'sessions', 'remove'])
    expect(staffActions(staff({}, { state: 'on' }), false, now)).toEqual(['role', 'sessions', 'reset', 'remove'])
    expect(staffActions(staff({}, { state: 'on', locked_until: now + 60 }), false, now)).toEqual(['role', 'sessions', 'reset', 'unlock', 'remove'])
  })
  it('keeps owners out of reach without the right to grant owner', () => {
    expect(staffActions(staff({ role: 'owner' }), false, now)).toEqual(['sessions'])
    expect(staffActions(staff({ role: 'owner' }), true, now)).toEqual(['role', 'sessions', 'remove'])
  })
  it('lists owner as a role only when it can be given', () => {
    expect(roleOptions(false)).toEqual(['support', 'admin'])
    expect(roleOptions(true)).toEqual(['support', 'admin', 'owner'])
  })
  it('adds only active customers', () => {
    expect(addable({ role: 'user', status: 'active' })).toBe(true)
    expect(addable({ role: 'user', status: 'banned' })).toBe(false)
    expect(addable({ role: 'support', status: 'active' })).toBe(false)
    expect(addable({ role: 'reseller', status: 'active' })).toBe(false)
  })
})

describe('audit links', () => {
  const uuid = '7d1c6f0a-2b3e-4f5a-8b9c-0d1e2f3a4b5c'
  const entry = (entity: string, entity_id = '', after: unknown = null) => ({ entity, entity_id, after, actor: { id: uuid, telegram_id: 1, username: 'boss' } })
  it('opens item pages by id', () => {
    expect(auditLink(entry('user', uuid))).toEqual({ name: 'user', params: { id: uuid } })
    expect(auditLink(entry('staff', uuid))).toEqual({ name: 'user', params: { id: uuid } })
    expect(auditLink(entry('subscription', uuid))).toEqual({ name: 'service', params: { id: uuid } })
    expect(auditLink(entry('order', uuid))).toEqual({ name: 'order', params: { id: uuid } })
    expect(auditLink(entry('user', ''))).toBeNull()
  })
  it('opens a payment through its order, when known', () => {
    expect(auditLink(entry('payment_intent', uuid, { order_id: uuid, decision: 'approved' }))).toEqual({ name: 'order', params: { id: uuid } })
    expect(auditLink(entry('payment_intent', uuid, { decision: 'approved' }))).toBeNull()
    expect(auditLink(entry('payment_intent', uuid, { order_id: 'nope' }))).toBeNull()
  })
  it('opens list pages for items without one', () => {
    expect(auditLink(entry('plan', uuid))).toEqual({ name: 'plans' })
    expect(auditLink(entry('discount', 'NOWRUZ'))).toEqual({ name: 'discounts' })
    expect(auditLink(entry('settings', 'maintenance.enabled'))).toEqual({ name: 'settings' })
    expect(auditLink(entry('text', 'fa.welcome'))).toEqual({ name: 'branding', query: { tab: 'texts' } })
    expect(auditLink(entry('branding', 'logo'))).toEqual({ name: 'branding' })
  })
  it('filters dashboard entries by the staff member', () => {
    expect(auditLink(entry('dashboard'))).toEqual({ name: 'audit', query: { actor: uuid } })
    expect(auditLink({ entity: 'dashboard', entity_id: '', after: null, actor: null })).toBeNull()
    expect(auditLink(entry('something_new', uuid))).toBeNull()
  })
  it('shortens ids but keeps keys and codes readable', () => {
    expect(entityIdLabel(uuid)).toBe('3a4b5c')
    expect(entityIdLabel('payments.card_number')).toBe('payments.card_number')
    expect(entityIdLabel('fa.welcome')).toBe('fa.welcome')
    expect(entityIdLabel('x'.repeat(40))).toBe('x'.repeat(31) + '…')
  })
})
