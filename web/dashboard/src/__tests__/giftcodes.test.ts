import { afterEach, describe, expect, it, vi } from 'vitest'
import fa from '../locales/fa.json'
import en from '../locales/en.json'
import { BOM, csvCell, csvFile, safeText } from '../csv'
import {
  blankForm,
  canonicalCode,
  codeProblem,
  codesFile,
  formProblems,
  howMany,
  limits,
  mostPaid,
  requestBody,
  severity,
  valid,
  type GiftForm,
} from '../giftcodes'
import { auditLink } from '../staff'
import type { GiftCodeItem, GiftRedemptionItem } from '../types'

// Built from code points, so the invisible characters these tests are about do not sit in the file.
const runes = (...cps: number[]) => String.fromCodePoint(...cps)
const persianDigits = (s: string) => s.replace(/\d/g, (d) => runes(0x06f0 + Number(d)))

describe('CSV cells', () => {
  it('quotes a cell that a spreadsheet would run as a formula', () => {
    for (const s of ['=1+1', '+1', '-1', '@SUM(A1)', '  =1+1', '\t=1', '\r=1', '\n=1', ' \t @x', "=HYPERLINK(\"http://x\")"]) {
      expect(safeText(s), JSON.stringify(s)).toBe("'" + s)
    }
    for (const s of ['plain', 'a=b', '1-2', 'x@y', 'ABCD-EFGH', '', ' leading space', 'کد هدیه']) {
      expect(safeText(s), JSON.stringify(s)).toBe(s)
    }
  })
  it('writes numbers as they are, and quotes what needs it', () => {
    expect(csvCell(-5)).toBe('-5')
    expect(csvCell(0)).toBe('0')
    expect(csvCell('a,b')).toBe('"a,b"')
    expect(csvCell('say "hi"')).toBe('"say ""hi"""')
    expect(csvCell('line\nbreak')).toBe('"line\nbreak"')
    expect(csvCell('=1,2')).toBe('"\'=1,2"')
  })
  it('starts a file with the byte-order mark and ends its lines with CRLF', () => {
    const f = csvFile([['a', 'b'], ['x', 1], [persianDigits('12'), 'é']])
    expect(f.startsWith(BOM)).toBe(true)
    expect(BOM.charCodeAt(0)).toBe(0xfeff)
    expect(f.slice(1)).toBe(`a,b\r\nx,1\r\n${persianDigits('12')},é\r\n`)
    expect(f.split('\r\n').filter(Boolean)).toHaveLength(3)
  })
})

describe('typed gift codes', () => {
  it('reads a code the way core does: capitals and digits, no separators', () => {
    expect(canonicalCode('abcd-efgh_jkmn')).toBe('ABCDEFGHJKMN')
    expect(canonicalCode(' ABCD . EFGH ')).toBe('ABCDEFGH')
    expect(canonicalCode('nowruz-' + persianDigits('1405'))).toBe('NOWRUZ1405')
    expect(canonicalCode(runes(0x0661, 0x0662, 0x0663) + 'a')).toBe('123A')
    expect(canonicalCode('AB' + runes(0x200c) + 'CD' + runes(0x200f) + runes(0xfeff))).toBe('ABCD')
    expect(canonicalCode('ab' + runes(0x2013) + 'cd')).toBe('ABCD')
    expect(canonicalCode('AB!CD')).toBeNull()
    expect(canonicalCode('AB' + runes(0x0645))).toBeNull()
    expect(canonicalCode('')).toBe('')
  })
  it('refuses a code that is short, easy to guess or not made of letters and digits', () => {
    expect(codeProblem('NOWRUZ-1405')).toBeNull()
    expect(codeProblem('ABCDEABCDE')).toBeNull() // five different characters are enough
    expect(codeProblem('SHORT1')).toBe('code_length')
    expect(codeProblem('A'.repeat(33))).toBe('code_length')
    expect(codeProblem('AAAAAAAAAA')).toBe('code_easy')
    expect(codeProblem('ABABABABAB')).toBe('code_easy')
    expect(codeProblem('ABCDABCDABCD')).toBe('code_easy')
    expect(codeProblem('GIFT*2026')).toBe('code_chars')
    expect(codeProblem('')).toBe('code_length')
  })
})

describe('the new codes form', () => {
  const batch = (over: Partial<GiftForm> = {}): GiftForm => ({ ...blankForm(), amount: '25000', maxRedemptions: '5', count: '3', ...over })
  it('needs an amount, the uses in all and a count', () => {
    expect(valid(blankForm())).toBe(false)
    expect(formProblems(blankForm())).toMatchObject({ amount: 'required', maxRedemptions: 'required' })
    expect(valid(batch())).toBe(true)
    expect(formProblems(batch({ count: '' }))).toHaveProperty('count')
  })
  it('reads the amount in the currency, in either digit set', () => {
    expect(valid(batch({ amount: persianDigits('25') + runes(0x066c) + persianDigits('000') }))).toBe(true)
    expect(formProblems(batch({ amount: '0' }))).toHaveProperty('amount')
    expect(formProblems(batch({ amount: '-5' }))).toHaveProperty('amount')
    expect(formProblems(batch({ amount: '1.5' }))).toHaveProperty('amount') // Toman has no decimals
    expect(valid(batch({ amount: '1.5', currency: 'USDT' }))).toBe(true)
    expect(formProblems(batch({ amount: '1.1234567', currency: 'USDT' }))).toHaveProperty('amount') // six decimals at most
    expect(valid(batch({ amount: String(limits.amount) }))).toBe(true)
    expect(formProblems(batch({ amount: String(limits.amount + 1) }))).toHaveProperty('amount')
    expect(formProblems(batch({ amount: '9223372036854775807' }))).toHaveProperty('amount')
    expect(formProblems(batch({ amount: 'lots' }))).toHaveProperty('amount')
    expect(formProblems(batch({ amount: '1', currency: 'EUR' }))).toHaveProperty('amount')
  })
  it('keeps the limits core keeps', () => {
    expect(valid(batch({ maxRedemptions: String(limits.uses) }))).toBe(true)
    for (const bad of ['0', '-1', '1000001', '1.5', 'many', '99999999999999999999']) {
      expect(formProblems(batch({ maxRedemptions: bad })), bad).toHaveProperty('maxRedemptions')
    }
    expect(valid(batch({ perCustomer: '100', maxRedemptions: '100' }))).toBe(true)
    expect(formProblems(batch({ perCustomer: '101', maxRedemptions: '500' }))).toHaveProperty('perCustomer')
    expect(formProblems(batch({ perCustomer: '3', maxRedemptions: '2' }))).toHaveProperty('perCustomer')
    expect(formProblems(batch({ perCustomer: '0' }))).toHaveProperty('perCustomer')
    expect(valid(batch({ count: '500' }))).toBe(true)
    expect(formProblems(batch({ count: '501' }))).toHaveProperty('count')
    expect(formProblems(batch({ count: '0' }))).toHaveProperty('count')
    expect(valid(batch({ days: '3650' }))).toBe(true)
    expect(valid(batch({ days: '' }))).toBe(true)
    expect(formProblems(batch({ days: '3651' }))).toHaveProperty('days')
    expect(formProblems(batch({ days: '0' }))).toHaveProperty('days')
    expect(valid(batch({ prefix: 'NOWRUZ' }))).toBe(true)
    expect(valid(batch({ prefix: 'no-wruz' }))).toBe(true)
    expect(formProblems(batch({ prefix: 'NOWRUZ1405' }))).toHaveProperty('prefix')
    expect(formProblems(batch({ prefix: 'A$' }))).toHaveProperty('prefix')
    expect(valid(batch({ note: 'x'.repeat(200) }))).toBe(true)
    expect(formProblems(batch({ note: 'x'.repeat(201) }))).toHaveProperty('note')
    expect(formProblems(batch({ note: 'a\nb' }))).toHaveProperty('note')
  })
  it('checks a code of your own', () => {
    const own = (code: string) => batch({ mode: 'own', code })
    expect(valid(own('NOWRUZ-1405'))).toBe(true)
    expect(formProblems(own(''))).toMatchObject({ code: 'required' })
    expect(formProblems(own('SHORT1'))).toMatchObject({ code: 'code_length' })
    expect(formProblems(own('AAAAAAAAAAAA'))).toMatchObject({ code: 'code_easy' })
    expect(formProblems(own('GIFT*2026'))).toMatchObject({ code: 'code_chars' })
    // a batch's count and prefix do not matter for a code of your own
    expect(valid({ ...own('NOWRUZ-1405'), count: '', prefix: '!' })).toBe(true)
  })
  it('sends what core takes', () => {
    const now = 1_760_000_000_000
    expect(requestBody(batch({ amount: persianDigits('25') + runes(0x066c) + persianDigits('000'), days: '30', note: ' spring ', prefix: ' spring ' }), now)).toEqual({
      amount: '25000', currency: 'IRT', max_redemptions: 5, per_customer_limit: 1, expires_at: 1_760_000_000 + 30 * 86_400, note: 'spring', count: 3, prefix: 'spring',
    })
    expect(requestBody(batch({ mode: 'own', code: ' nowruz-1405 ', perCustomer: '2', amount: '2.5', currency: 'USDT' }), now)).toEqual({
      amount: '2.5', currency: 'USDT', max_redemptions: 5, per_customer_limit: 2, expires_at: null, note: '', code: 'nowruz-1405',
    })
  })
  it('says how many codes there are and the most they can pay out', () => {
    expect(howMany(batch())).toBe(3)
    expect(howMany(batch({ mode: 'own' }))).toBe(1)
    expect(howMany(batch({ count: 'x' }))).toBe(0)
    expect(mostPaid(batch())).toEqual({ amount: 25000 * 5 * 3, currency: 'IRT' })
    expect(mostPaid(batch({ mode: 'own' }))).toEqual({ amount: 25000 * 5, currency: 'IRT' })
    expect(mostPaid(batch({ amount: '1.5', currency: 'USDT' }))).toEqual({ amount: 1_500_000 * 5 * 3, currency: 'USDT' })
    expect(mostPaid(blankForm())).toBeNull()
    // never a number that is not exact
    expect(mostPaid(batch({ amount: String(limits.amount), maxRedemptions: String(limits.uses), count: '500' }))?.amount).toBe(Number.MAX_SAFE_INTEGER)
  })
  it('colours the status', () => {
    expect([severity('active'), severity('disabled'), severity('used_up'), severity('expired'), severity('?')]).toEqual(['success', 'danger', 'warn', 'secondary', 'secondary'])
  })
})

describe('the file of new codes', () => {
  const terms = { amount: { amount: 25000, currency: 'IRT' }, max_redemptions: 5, per_customer_limit: 1, expires_at: 1_760_000_000, note: '=cmd|"x"' }
  it('has the codes with their terms, a byte-order mark and a quoted note', () => {
    const f = codesFile(['SPRING-ABCD-EFGH-JKMN', 'SPRING-QRST-UVWX-YZ23'], terms)
    expect(f.startsWith(BOM)).toBe(true)
    const lines = f.slice(1).split('\r\n')
    expect(lines[0]).toBe('code,amount,currency,max_redemptions,per_customer_limit,expires_utc,note')
    expect(lines[1]).toBe('SPRING-ABCD-EFGH-JKMN,25000,IRT,5,1,2025-10-09T08:53:20.000Z,"\'=cmd|""x"""')
    expect(lines[2].startsWith('SPRING-QRST-UVWX-YZ23,')).toBe(true)
    expect(lines).toHaveLength(4) // header, two codes, and the empty piece after the last CRLF
  })
  it('writes USDT in major units and no end as empty', () => {
    const f = codesFile(['ABCD-EFGH-JKMN'], { ...terms, amount: { amount: 2_500_000, currency: 'USDT' }, expires_at: null, note: '' })
    expect(f.slice(1).split('\r\n')[1]).toBe('ABCD-EFGH-JKMN,2.500000,USDT,5,1,,')
  })
})

describe('the audit log', () => {
  it('links a gift code entry to the Gift codes page, and a switch setting too', () => {
    const id = 'aaaaaaaa-1111-4222-8333-444444444444'
    expect(auditLink({ entity: 'gift_code', entity_id: id, after: null, actor: null })).toEqual({ name: 'giftcodes' })
    expect(auditLink({ entity: 'settings', entity_id: 'gift_codes.enabled', after: null, actor: null })).toEqual({ name: 'giftcodes' })
  })
})

describe('the texts', () => {
  const at = (o: unknown, path: string) => path.split('.').reduce<unknown>((x, k) => (x as Record<string, unknown> | undefined)?.[k], o)
  it('say the new ledger kind, the setting and the audit entries in both languages', () => {
    for (const cat of [fa, en]) {
      for (const key of [
        'ledger.kind.gift_code', 'settings.field.gift_codes.enabled.label', 'settings.field.gift_codes.enabled.help', 'audit.entity.gift_code', 'audit.group.gift_code',
        'audit.action.gift_code.create', 'audit.action.gift_code.enable', 'audit.action.gift_code.disable', 'audit.action.gift_code.redeem', 'audit.action.gift_code.lock',
        'audit.action.export.gift_codes', 'audit.action.export.gift_redemptions', 'texts.group.gift', 'section.giftcodes.title', 'section.giftcodes.desc',
      ]) {
        expect(at(cat, key), key).toBeTruthy()
      }
    }
  })
  it('have a text for every problem the form can find', () => {
    const problems = new Set<string>()
    for (const f of [
      { ...blankForm(), amount: 'x', maxRedemptions: 'x', perCustomer: 'x', days: 'x', note: 'a\nb', count: 'x', prefix: '!' },
      { ...blankForm(), mode: 'own' as const, code: 'A' },
      { ...blankForm(), mode: 'own' as const, code: 'AAAAAAAAAA' },
      { ...blankForm(), mode: 'own' as const, code: 'GIFT*2026' },
    ]) {
      for (const p of Object.values(formProblems(f))) problems.add(p)
    }
    expect([...problems].sort()).toEqual(['amount', 'code_chars', 'code_easy', 'code_length', 'count', 'days', 'note', 'per_customer', 'prefix', 'required', 'uses'])
    // an empty required field has no text: the form only keeps its button off
    for (const p of problems) if (p !== 'required') for (const cat of [fa, en]) expect(at(cat, 'giftcodes.invalid.' + p), p).toBeTruthy()
  })
})

// The page, booted with a fake core.
describe('the Gift codes page', () => {
  const t0 = 1_760_000_000
  const boss = { id: 'cccccccc-1111-4222-8333-444444444444', telegram_id: 1, username: 'boss' }
  const gid = (n: number) => `0000000${n}-1111-4222-8333-444444444444`
  const code = (over: Partial<GiftCodeItem> = {}): GiftCodeItem => ({
    id: gid(1), tail: 'JKMN', generated: true, amount: { amount: 25000, currency: 'IRT' }, max_redemptions: 5, per_customer_limit: 1, redeemed: 2, expires_at: null,
    enabled: true, status: 'active', note: 'Spring giveaway', created_at: t0 - 86_400, created_by: boss, last_redeemed_at: t0 - 3600, ...over,
  })
  const codes = (): GiftCodeItem[] => [
    code(),
    code({ id: gid(2), tail: '1405', generated: false, amount: { amount: 2_500_000, currency: 'USDT' }, max_redemptions: 10, per_customer_limit: 2, redeemed: 0, enabled: false, status: 'disabled', note: '', last_redeemed_at: null, expires_at: t0 + 86_400 }),
  ]
  const ownerPerms = ['overview.read', 'users.read', 'discounts.write', 'settings.write', 'audit.read']
  const supportPerms = ['overview.read', 'users.read', 'services.read', 'tickets.write']
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const listAnswer = (items: GiftCodeItem[], over: Record<string, unknown> = {}) => ({ items, total: items.length, page: 1, size: 25, enabled: true, available: true, ...over })
  const text = (path: string) => (path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa) as string | undefined) ?? path
  const unmount = () => (document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()

  type Answer = (url: string, init: RequestInit | undefined, body: Record<string, unknown>) => Response | Promise<Response> | undefined
  async function boot(path: string, permissions: string[], answer: Answer = () => undefined, lang = 'fa') {
    unmount()
    try {
      localStorage.setItem('bobres.lang', lang)
    } catch {
      /* the page falls back to Persian */
    }
    vi.resetModules()
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const raw = typeof init?.body === 'string' ? init.body : ''
        calls.push(`${init?.method ?? 'GET'} ${url} ${raw}`.trim())
        const own = answer(url, init, raw ? (JSON.parse(raw) as Record<string, unknown>) : {})
        if (own) return own
        const get = (init?.method ?? 'GET') === 'GET'
        if (url === '/api/v1/me') {
          return json(200, { user: { ...boss, role: permissions.includes('discounts.write') ? 'owner' : 'support', language: lang }, permissions, csrf: 'c', session: { method: 'link', expires_at: 2e9 }, password: { enabled: false }, password_available: true, brand: 'Shop' })
        }
        if (get && url.startsWith('/api/v1/gift-codes?')) return json(200, listAnswer(codes()))
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
    const input = el as HTMLInputElement | HTMLTextAreaElement
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
  async function pick(select: string, label: string) {
    ;(await found<HTMLElement>(select)).click()
    const option = await vi.waitFor(() => {
      const el = [...document.querySelectorAll('.p-select-option')].find((o) => o.textContent?.includes(label)) as HTMLElement | undefined
      expect(el, label).toBeDefined()
      return el!
    })
    option.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) // PrimeVue picks on mousedown
  }

  it('lists the codes with their last four characters, never a code, and keeps its menu entry', async () => {
    const { calls } = await boot('/admin/gift-codes', ownerPerms)
    const body = await found('.codes .p-datatable-tbody')
    await vi.waitFor(() => expect(body.querySelectorAll('tr')).toHaveLength(2))
    expect(calls).toContain('GET /api/v1/gift-codes?page=1&size=25')
    const [a, b] = [...body.querySelectorAll('tr')] as HTMLElement[]
    expect(a.textContent).toContain('…JKMN')
    expect(a.textContent).toContain('۲۵٬۰۰۰ تومان')
    expect(a.textContent).toContain('۲ از ۵')
    expect(a.textContent).toContain(text('giftcodes.status.active'))
    expect(a.textContent).toContain(text('giftcodes.kind_generated'))
    expect(a.textContent).toContain('Spring giveaway')
    expect(a.textContent).toContain('@boss')
    expect(b.textContent).toContain('…1405')
    expect(b.textContent).toContain('۲٫۵ تتر')
    expect(b.textContent).toContain(text('giftcodes.status.disabled'))
    expect(b.textContent).toContain(text('giftcodes.kind_own'))
    expect(b.textContent).toContain(text('giftcodes.per_customer_short').replace('{n}', '۲'))
    expect((b.querySelector('.toggle input') as HTMLInputElement).checked).toBe(false)
    expect((a.querySelector('.toggle input') as HTMLInputElement).checked).toBe(true)
    expect(document.querySelector('.sidebar')?.textContent).toContain(text('section.giftcodes.title'))
    expect(document.querySelector('a.export')?.getAttribute('href')).toBe('/api/v1/gift-codes.csv?calendar=jalali')
    expect(document.querySelector('.no-key')).toBeNull()
    expect(document.querySelector('.is-off')).toBeNull()
  })

  it('filters by status and searches, asking core each time', async () => {
    const { calls } = await boot('/admin/gift-codes', ownerPerms)
    await found('.codes .p-datatable-tbody tr')
    await pick('.status-filter', text('giftcodes.status.used_up'))
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/gift-codes?status=used_up&page=1&size=25'))
    type(document.querySelector('input.search'), ' ' + persianDigits('1405') + ' ')
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/gift-codes?status=used_up&q=1405&page=1&size=25'))
    expect(document.querySelector('a.export')?.getAttribute('href')).toBe('/api/v1/gift-codes.csv?status=used_up&q=1405&calendar=jalali')
  })

  it('asks for the files without the Jalali column when the calendar is Gregorian', async () => {
    localStorage.setItem('bobres.reports.calendar', 'gregorian')
    try {
      await boot('/admin/gift-codes', ownerPerms)
      await found('.codes .p-datatable-tbody tr')
      expect(document.querySelector('a.export')?.getAttribute('href')).toBe('/api/v1/gift-codes.csv')
    } finally {
      localStorage.removeItem('bobres.reports.calendar')
    }
  })

  it('switches a code off and on from its row', async () => {
    let enabled = true
    const { calls } = await boot('/admin/gift-codes', ownerPerms, (url, init, b) => {
      if (url === `/api/v1/gift-codes/${gid(1)}/enabled` && init?.method === 'PUT') {
        enabled = b.enabled as boolean
        return json(200, { item: code({ enabled, status: enabled ? 'active' : 'disabled' }) })
      }
    })
    const body = await found('.codes .p-datatable-tbody')
    await vi.waitFor(() => expect(body.querySelectorAll('tr')).toHaveLength(2))
    const toggle = () => body.querySelector('tr .toggle input') as HTMLInputElement
    toggle().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', `/gift-codes/${gid(1)}/enabled`)).toEqual([{ enabled: false }]))
    await vi.waitFor(() => expect(body.querySelector('tr')?.textContent).toContain(text('giftcodes.status.disabled')))
    expect(toggle().checked).toBe(false)
    toggle().click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', `/gift-codes/${gid(1)}/enabled`)).toEqual([{ enabled: false }, { enabled: true }]))
    await vi.waitFor(() => expect(body.querySelector('tr')?.textContent).toContain(text('giftcodes.status.active')))
  })

  it('tells staff when gift codes are off, and turns them on for those who may', async () => {
    const { calls } = await boot('/admin/gift-codes', ownerPerms, (url, init) => {
      if (url.startsWith('/api/v1/gift-codes?')) return json(200, listAnswer(codes(), { enabled: false }))
      if (url === '/api/v1/settings' && init?.method === 'PUT') return json(200, { items: [], groups: [] })
    })
    const note = await found('.is-off')
    expect(note.textContent).toContain(text('giftcodes.off'))
    ;(note.querySelector('button.turn-on') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(sent(calls, 'PUT', '/settings')).toEqual([{ values: { 'gift_codes.enabled': 'true' } }]))
    await vi.waitFor(() => expect(document.querySelector('.p-toast')?.textContent).toContain(text('giftcodes.turned_on')))
  })

  it('says so, and makes nothing, when core has no master key', async () => {
    await boot('/admin/gift-codes', ownerPerms, (url) => {
      if (url.startsWith('/api/v1/gift-codes?')) return json(200, listAnswer(codes(), { available: false, enabled: false }))
    })
    const warn = await found('.no-key')
    expect(warn.textContent).toContain('BOBRES_CORE_MASTER_KEY')
    expect(document.querySelector('.is-off')).toBeNull()
    expect((document.querySelector('button.new') as HTMLButtonElement).disabled).toBe(true)
  })

  it('makes a batch, shows its codes once and asks before they are lost', async () => {
    const made = ['SPRING-ABCD-EFGH-JKMN', 'SPRING-QRST-UVWX-YZ23', 'SPRING-4567-89AB-CDEF']
    const { calls } = await boot('/admin/gift-codes', ownerPerms, (url, init) => {
      if (url === '/api/v1/gift-codes' && init?.method === 'POST') {
        return json(200, { codes: made, items: made.map((c, i) => code({ id: gid(3 + i), tail: c.slice(-4), redeemed: 0, note: 'Spring', max_redemptions: 5 })) })
      }
    })
    await found('.codes .p-datatable-tbody tr')
    ;(document.querySelector('button.new') as HTMLButtonElement).click()
    const d = await found('.new-dialog')
    const create = () => d.querySelector('button.create') as HTMLButtonElement
    expect(create().disabled).toBe(true)
    // Persian digits and separators are taken as the digits they are
    type(d.querySelector('input.amount'), persianDigits('25') + '٬' + persianDigits('000'))
    type(d.querySelector('input.uses'), persianDigits('5'))
    type(d.querySelector('input.count'), '3')
    type(d.querySelector('input.prefix'), 'spring')
    type(d.querySelector('input.days'), '30')
    type(d.querySelector('input.note'), ' Spring ')
    await vi.waitFor(() => expect(create().disabled).toBe(false))
    expect(d.querySelector('.sum-codes')?.textContent).toBe('۳')
    expect(d.querySelector('.sum-value')?.textContent).toBe('۲۵٬۰۰۰ تومان')
    expect(d.querySelector('.sum-most')?.textContent).toBe('۳۷۵٬۰۰۰ تومان')
    create().click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/gift-codes')).toHaveLength(1))
    const body = sent(calls, 'POST', '/gift-codes')[0]
    expect(body).toMatchObject({ amount: '25000', currency: 'IRT', max_redemptions: 5, per_customer_limit: 1, note: 'Spring', count: 3, prefix: 'spring' })
    expect(Math.abs((body.expires_at as number) - (Date.now() / 1000 + 30 * 86_400))).toBeLessThan(60)
    expect(body).not.toHaveProperty('code')

    // The codes, once, with the warning; the list is read again (it holds no code).
    const shown = await found('.shown-dialog')
    expect((shown.querySelector('textarea') as HTMLTextAreaElement).value).toBe(made.join('\n'))
    expect(shown.textContent).toContain(text('giftcodes.shown_warning'))
    expect(shown.querySelector('.p-dialog-close-button')).toBeNull() // no X: closing asks first
    await vi.waitFor(() => expect(calls.filter((c) => c.startsWith('GET /api/v1/gift-codes?'))).toHaveLength(2))
    expect(document.querySelector('.codes')?.textContent).not.toContain('ABCD')

    // Done before copying or downloading asks; keeping it open keeps the codes.
    ;(shown.querySelector('button.done') as HTMLButtonElement).click()
    const ask = await found('.p-confirmdialog')
    expect(ask.textContent).toContain(text('giftcodes.unsaved_message'))
    const buttons = [...ask.querySelectorAll('.p-confirmdialog-reject-button, .p-confirmdialog-accept-button, button')] as HTMLButtonElement[]
    const keep = buttons.find((b) => b.textContent?.includes(text('giftcodes.keep_open')))!
    keep.click()
    await vi.waitFor(() => expect(document.querySelector('.p-confirmdialog')).toBeNull())
    expect(document.querySelector('.shown-dialog textarea')).not.toBeNull()
    // Closing anyway loses them from the page.
    ;(document.querySelector('.shown-dialog button.done') as HTMLButtonElement).click()
    const ask2 = await found('.p-confirmdialog')
    ;([...ask2.querySelectorAll('button')] as HTMLButtonElement[]).find((b) => b.textContent?.includes(text('giftcodes.close_anyway')))!.click()
    await vi.waitFor(() => expect(document.querySelector('.shown-dialog')).toBeNull())
    expect(document.body.innerHTML).not.toContain('SPRING-ABCD')
  })

  it('copies the codes, or downloads them as a file with their terms, and then closes without asking', async () => {
    const made = ['SPRING-ABCD-EFGH-JKMN', 'SPRING-QRST-UVWX-YZ23']
    const written: string[] = []
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn(async (s: string) => void written.push(s)) }, configurable: true })
    const blobs: Blob[] = []
    ;(URL as unknown as Record<string, unknown>).createObjectURL = vi.fn((b: Blob) => (blobs.push(b), 'blob:codes'))
    ;(URL as unknown as Record<string, unknown>).revokeObjectURL = vi.fn()
    const clicked = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    const { calls } = await boot('/admin/gift-codes', ownerPerms, (url, init) => {
      if (url === '/api/v1/gift-codes' && init?.method === 'POST') {
        return json(200, { codes: made, items: made.map((c, i) => code({ id: gid(3 + i), tail: c.slice(-4), redeemed: 0, note: '=cmd', max_redemptions: 5 })) })
      }
    })
    await found('.codes .p-datatable-tbody tr')
    const make = async () => {
      ;(document.querySelector('button.new') as HTMLButtonElement).click()
      const d = await found('.new-dialog')
      type(d.querySelector('input.amount'), '25000')
      type(d.querySelector('input.uses'), '5')
      type(d.querySelector('input.count'), '2')
      await vi.waitFor(() => expect((d.querySelector('button.create') as HTMLButtonElement).disabled).toBe(false))
      ;(d.querySelector('button.create') as HTMLButtonElement).click()
      return found<HTMLElement>('.shown-dialog')
    }
    let shown = await make()
    ;(shown.querySelector('button.copy') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(written).toEqual([made.join('\n')]))
    await vi.waitFor(() => expect(document.querySelector('.p-toast')?.textContent).toContain(text('giftcodes.copied')))
    ;(shown.querySelector('button.done') as HTMLButtonElement).click() // copied: closes at once
    await vi.waitFor(() => expect(document.querySelector('.shown-dialog')).toBeNull())
    expect(document.querySelector('.p-confirmdialog')).toBeNull()

    shown = await make()
    ;(shown.querySelector('button.download') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(blobs).toHaveLength(1))
    expect(clicked).toHaveBeenCalled()
    const file = await new Promise<ArrayBuffer>((resolve, reject) => {
      const r = new FileReader()
      r.onload = () => resolve(r.result as ArrayBuffer)
      r.onerror = () => reject(r.error)
      r.readAsArrayBuffer(blobs[0])
    })
    const bytes = new Uint8Array(file)
    expect([...bytes.slice(0, 3)]).toEqual([0xef, 0xbb, 0xbf]) // UTF-8 byte-order mark
    const csv = new TextDecoder().decode(bytes.slice(3))
    expect(csv.split('\r\n')[0]).toBe('code,amount,currency,max_redemptions,per_customer_limit,expires_utc,note')
    expect(csv).toContain('SPRING-ABCD-EFGH-JKMN,25000,IRT,5,1,,\'=cmd')
    expect(csv).toContain('SPRING-QRST-UVWX-YZ23,')
    ;(shown.querySelector('button.done') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(document.querySelector('.shown-dialog')).toBeNull())
    expect(sent(calls, 'POST', '/gift-codes')).toHaveLength(2)
  })

  it('makes a code of your own words, and shows the refusal of core under the field', async () => {
    let taken = true
    const { calls } = await boot('/admin/gift-codes', ownerPerms, (url, init) => {
      if (url === '/api/v1/gift-codes' && init?.method === 'POST') {
        if (taken) {
          taken = false
          return json(400, { error: 'invalid', field: 'code', message: 'code: this code exists already: choose another' })
        }
        return json(200, { codes: ['NOWRUZ-1405'], items: [code({ id: gid(9), tail: '1405', generated: false, redeemed: 0 })] })
      }
    })
    await found('.codes .p-datatable-tbody tr')
    ;(document.querySelector('button.new') as HTMLButtonElement).click()
    const d = await found('.new-dialog')
    ;([...d.querySelectorAll('.mode .p-togglebutton')][1] as HTMLElement).click()
    await vi.waitFor(() => expect(d.querySelector('input.own-code')).not.toBeNull())
    expect(d.querySelector('input.count')).toBeNull()
    expect(d.querySelector('input.own-code')?.getAttribute('dir')).toBe('ltr')
    type(d.querySelector('input.amount'), '1000')
    type(d.querySelector('input.uses'), '3')
    // an easy code is refused on the spot, with the reason
    type(d.querySelector('input.own-code'), 'AAAAAAAAAA')
    await vi.waitFor(() => expect(d.querySelector('.own-problem')?.textContent).toBe(text('giftcodes.invalid.code_easy')))
    expect((d.querySelector('button.create') as HTMLButtonElement).disabled).toBe(true)
    type(d.querySelector('input.own-code'), 'nowruz-' + persianDigits('1405'))
    await vi.waitFor(() => expect((d.querySelector('button.create') as HTMLButtonElement).disabled).toBe(false))
    expect(d.querySelector('.sum-codes')?.textContent).toBe('۱')
    ;(d.querySelector('button.create') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(d.querySelector('.own-problem')?.textContent).toBe('code: this code exists already: choose another'))
    expect(d.querySelector('input.own-code')?.classList.contains('p-invalid')).toBe(true)
    type(d.querySelector('input.own-code'), 'nowruz-1406')
    await vi.waitFor(() => expect((d.querySelector('button.create') as HTMLButtonElement).disabled).toBe(false))
    ;(d.querySelector('button.create') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(sent(calls, 'POST', '/gift-codes')).toHaveLength(2))
    expect(sent(calls, 'POST', '/gift-codes')[0]).toMatchObject({ code: 'nowruz-1405', amount: '1000', max_redemptions: 3 })
    expect(sent(calls, 'POST', '/gift-codes')[0]).not.toHaveProperty('count')
    expect(sent(calls, 'POST', '/gift-codes')[1]).toMatchObject({ code: 'nowruz-1406' })
    const shown = await found('.shown-dialog')
    expect((shown.querySelector('textarea') as HTMLTextAreaElement).value).toBe('NOWRUZ-1405')
  })

  it('shows who used a code, with a file of them', async () => {
    const used: GiftRedemptionItem[] = [
      { id: 'r2', user: { id: 'u2', telegram_id: 202, username: 'sara' }, seq: 1, amount: { amount: 25000, currency: 'IRT' }, balance_after: { amount: 40000, currency: 'IRT' }, created_at: t0 },
      { id: 'r1', user: { id: 'u1', telegram_id: 101, username: 'dara' }, seq: 2, amount: { amount: 25000, currency: 'IRT' }, balance_after: { amount: 25000, currency: 'IRT' }, created_at: t0 - 3600 },
    ]
    const { calls } = await boot('/admin/gift-codes', ownerPerms, (url) => {
      if (url.startsWith(`/api/v1/gift-codes/${gid(1)}/redemptions?`)) return json(200, { code: code(), items: used, total: 2, page: 1, size: 25 })
    })
    const body = await found('.codes .p-datatable-tbody')
    await vi.waitFor(() => expect(body.querySelectorAll('tr')).toHaveLength(2))
    ;(body.querySelector('tr button.who') as HTMLButtonElement).click()
    const d = await found('.redemptions-dialog')
    await vi.waitFor(() => expect(calls).toContain(`GET /api/v1/gift-codes/${gid(1)}/redemptions?page=1&size=25`))
    const rows = await vi.waitFor(() => {
      const r = d.querySelectorAll('.redemptions .p-datatable-tbody tr')
      expect(r).toHaveLength(2)
      return [...r] as HTMLElement[]
    })
    expect(rows[0].textContent).toContain('@sara')
    expect(rows[0].textContent).toContain('۲۵٬۰۰۰ تومان')
    expect(rows[0].textContent).toContain('۴۰٬۰۰۰ تومان')
    expect(rows[1].textContent).toContain('@dara')
    expect(d.textContent).toContain('…JKMN')
    expect(d.querySelector('a.export')?.getAttribute('href')).toBe(`/api/v1/gift-codes/${gid(1)}/redemptions.csv?calendar=jalali`)
  })

  it('stays closed to staff without discounts.write', async () => {
    const { router, calls } = await boot('/admin/gift-codes', supportPerms)
    expect(router.currentRoute.value.name).toBe('overview')
    expect(document.querySelector('.sidebar')?.textContent).not.toContain(text('section.giftcodes.title'))
    expect(calls.some((c) => c.includes('/api/v1/gift-codes'))).toBe(false)
  })

  it('reads the same page in English, with plural texts that take the count', async () => {
    await boot('/admin/gift-codes', ownerPerms, undefined, 'en')
    const body = await found('.codes .p-datatable-tbody')
    await vi.waitFor(() => expect(body.querySelectorAll('tr')).toHaveLength(2))
    expect(body.textContent).toContain('2 of 5')
    expect(body.textContent).toContain('Per customer: 2')
    expect(body.textContent).toContain('2.5 USDT')
    expect(document.querySelector('h1')?.textContent).toBe('Gift codes')
    expect(en.giftcodes.ends_in).toContain('|')
  })
})
