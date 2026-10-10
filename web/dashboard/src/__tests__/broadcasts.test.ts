import { afterEach, describe, expect, it, vi } from 'vitest'
import fa from '../locales/fa.json'
import {
  actionsFor,
  blankDraft,
  changingSoon,
  bodyOf,
  defaultScheduleAt,
  draftOf,
  draftProblems,
  duration,
  editable,
  firstLine,
  fromLocalInput,
  labelProblem,
  live,
  localInput,
  neededLangs,
  percent,
  poller,
  previewOf,
  problemsFor,
  progress,
  sameDraft,
  scheduleProblem,
  splitProblems,
  testOutcome,
  urlProblem,
  utf16Length,
  type Draft,
} from '../broadcasts'
import type { BroadcastItem } from '../types'

const ID = '0b1c2d3e-0000-4000-8000-0000000000b1'

function broadcast(extra: Partial<BroadcastItem> = {}): BroadcastItem {
  return {
    id: ID,
    status: 'draft',
    segment: 'active',
    language: '',
    texts: { fa: 'سلام <b>دوستان</b>', en: 'Hello <b>friends</b>' },
    button: null,
    scheduled_at: null,
    started_at: null,
    finished_at: null,
    counts: { total: 0, pending: 0, sent: 0, blocked: 0, failed: 0, unknown: 0 },
    rate_per_sec: 0,
    eta_seconds: null,
    last_error: '',
    created_by: { id: 'u1', telegram_id: 1001, username: 'boss' },
    created_at: 1_790_000_000,
    version: 3,
    ...extra,
  }
}

describe('audience and draft', () => {
  it('needs a text in each language the audience reads', () => {
    expect(neededLangs('')).toEqual(['fa', 'en'])
    expect(neededLangs('fa')).toEqual(['fa'])
    expect(neededLangs('en')).toEqual(['en'])
  })

  it('sends only the needed languages, trimmed, and no button when it is off', () => {
    const d: Draft = {
      segment: 'expired',
      language: 'fa',
      texts: { fa: '  سلام \n', en: 'left over' },
      button: { on: false, url: 'https://x.example', labels: { fa: 'برو', en: 'Go' } },
    }
    expect(bodyOf(d)).toEqual({ segment: 'expired', language: 'fa', texts: { fa: 'سلام', en: '' }, button: null })
    d.button.on = true
    expect(bodyOf(d).button).toEqual({ url: 'https://x.example', labels: { fa: 'برو', en: '' } })
  })

  it('reads a broadcast back into a draft and sees what changed', () => {
    const b = broadcast({ button: { url: 'https://shop.example/plans', labels: { fa: 'خرید', en: 'Buy' } } })
    const d = draftOf(b)
    expect(d.button).toEqual({ on: true, url: 'https://shop.example/plans', labels: { fa: 'خرید', en: 'Buy' } })
    const copy = draftOf(b)
    copy.texts.en = 'Hello <b>friends</b>  ' // trailing spaces are not a change
    expect(sameDraft(d, copy)).toBe(true)
    copy.texts.en = 'Hi'
    expect(sameDraft(d, copy)).toBe(false)
    // a text the audience does not read is not a change either
    const faOnly = { ...draftOf(b), language: 'fa' as const }
    expect(sameDraft(faOnly, { ...faOnly, texts: { fa: faOnly.texts.fa, en: 'other' } })).toBe(true)
    expect(blankDraft()).toEqual({ segment: 'all', language: '', texts: { fa: '', en: '' }, button: { on: false, url: '', labels: { fa: '', en: '' } } })
  })

  it('checks button labels and links as Telegram and core do', () => {
    expect(labelProblem('  ')).toBe('empty')
    expect(labelProblem('Buy <now>')).toBe('chars')
    expect(labelProblem('Tom & Jerry')).toBe('chars')
    expect(labelProblem('a'.repeat(64))).toBe('')
    expect(labelProblem('a'.repeat(65))).toBe('too_long')
    expect(utf16Length('😀'.repeat(32))).toBe(64) // an emoji is two units
    expect(labelProblem('😀'.repeat(32) + 'a')).toBe('too_long')
    expect(urlProblem('')).toBe('empty')
    expect(urlProblem('http://shop.example')).toBe('bad')
    expect(urlProblem('https://user:pw@shop.example')).toBe('bad')
    expect(urlProblem('https://shop.example/a b')).toBe('bad')
    expect(urlProblem('https://shop.example/' + 'a'.repeat(1010))).toBe('too_long')
    expect(urlProblem(' https://shop.example/plans ')).toBe('')
  })

  it('lists what keeps a draft from going out, as the fields core names', () => {
    const d = blankDraft()
    expect(draftProblems(d, {})).toEqual(['texts.fa', 'texts.en'])
    d.texts = { fa: 'سلام', en: 'Hi' }
    expect(draftProblems(d, { fa: { checking: false, ok: true }, en: { checking: false, ok: false } })).toEqual(['texts.en'])
    expect(draftProblems(d, { en: { checking: true, ok: null } })).toEqual([])
    d.texts.en = 'x'.repeat(4097)
    expect(draftProblems(d, {})).toEqual(['texts.en'])
    d.texts.en = '<b>' + 'x'.repeat(4096) + '</b>' // tags do not count
    expect(draftProblems(d, {})).toEqual([])
    d.button = { on: true, url: 'ftp://x', labels: { fa: '', en: 'Go' } }
    expect(draftProblems(d, {})).toEqual(['button.url', 'button.labels.fa'])
    d.language = 'en'
    expect(draftProblems(d, {})).toEqual(['button.url'])
  })

  it('sorts core’s text problems by language', () => {
    const problems = [
      { code: 'tag_not_allowed', arg: 'script', lang: 'fa' },
      { code: 'too_long', arg: '4096', lang: 'en' },
      { code: 'empty', arg: '', lang: 'en' },
      { code: 'bad_entity', arg: '&' },
    ]
    expect(problemsFor(problems, 'fa').map((p) => p.code)).toEqual(['tag_not_allowed', 'bad_entity'])
    expect(problemsFor('nope', 'fa')).toEqual([])
    const split = splitProblems(problems, 'texts.en')
    expect(split.fa.map((p) => p.code)).toEqual(['tag_not_allowed'])
    expect(split.en.map((p) => p.code)).toEqual(['too_long', 'bad_entity'])
    expect(splitProblems(problems, 'button.labels.en')).toEqual({ fa: [], en: [] })
  })
})

describe('status and progress', () => {
  it('offers what each status allows', () => {
    expect(actionsFor('draft')).toEqual(['edit', 'send', 'delete'])
    expect(actionsFor('scheduled')).toEqual(['edit', 'cancel'])
    expect(actionsFor('running')).toEqual(['pause', 'cancel'])
    expect(actionsFor('paused')).toEqual(['resume', 'cancel'])
    expect(actionsFor('done')).toEqual([])
    expect(actionsFor('cancelled')).toEqual([])
    expect(['draft', 'scheduled', 'running'].map((s) => editable(s as BroadcastItem['status']))).toEqual([true, true, false])
    expect(['scheduled', 'running', 'paused', 'done', 'cancelled', 'draft'].map((s) => live(s as BroadcastItem['status']))).toEqual([true, true, true, false, false, false])
    const now = 1_800_000_000
    expect(changingSoon({ status: 'running', scheduled_at: null }, now)).toBe(true)
    expect(changingSoon({ status: 'paused', scheduled_at: null }, now)).toBe(false)
    expect(changingSoon({ status: 'scheduled', scheduled_at: now + 600 }, now)).toBe(true)
    expect(changingSoon({ status: 'scheduled', scheduled_at: now + 601 }, now)).toBe(false)
  })

  it('counts every handled recipient as progress', () => {
    expect(progress({ total: 200, pending: 50, sent: 120, blocked: 20, failed: 6, unknown: 4 })).toEqual({ done: 150, total: 200, percent: 75 })
    expect(progress({ total: 0, pending: 0, sent: 0, blocked: 0, failed: 0, unknown: 0 }).percent).toBe(0)
    expect(progress({ total: 3, pending: 1, sent: 2, blocked: 0, failed: 0, unknown: 0 }).percent).toBe(66) // never rounds up to done
    expect(percent(75, 'fa')).toBe('۷۵٪')
    expect(percent(75, 'en')).toBe('75%')
  })

  it('previews a text in the viewer’s language, else the other one', () => {
    expect(firstLine('\n  First line  \nsecond')).toBe('First line')
    expect(firstLine('x'.repeat(100), 10)).toBe('x'.repeat(9) + '…')
    expect(previewOf({ fa: 'سلام\nدوم', en: 'Hi' }, 'fa')).toEqual({ text: 'سلام', lang: 'fa' })
    expect(previewOf({ fa: '', en: 'Hi <b>all</b>' }, 'fa')).toEqual({ text: 'Hi <b>all</b>', lang: 'en' })
  })

  it('writes durations in the language’s words and digits', () => {
    expect(duration(42, 'en')).toBe('42 seconds')
    expect(duration(61, 'en')).toBe('2 minutes')
    expect(duration(3900, 'en')).toBe('1 hour, 5 minutes')
    expect(duration(3600, 'en')).toBe('1 hour')
    expect(duration(90_000, 'en')).toBe('1 day, 1 hour')
    expect(duration(480, 'fa')).toBe('۸ دقیقه')
    expect(duration(3900, 'fa')).toBe('۱ ساعت و ۵ دقیقه')
  })

  it('sorts the answers of Send to me', () => {
    expect(testOutcome({ ok: true })).toBe('ok')
    expect(testOutcome({ ok: false, problem: 'blocked' })).toBe('blocked')
    expect(testOutcome({ ok: false, problem: 'bad_request' })).toBe('bad_request')
    expect(testOutcome({ ok: false, problem: 'error' })).toBe('error')
    expect(testOutcome(null, { status: 503, code: 'unavailable' })).toBe('unreachable')
    expect(testOutcome(null, { status: 429, code: 'rate_limited' })).toBe('rate_limited')
    expect(testOutcome(null, { status: 400, code: 'invalid' })).toBe('invalid')
    expect(testOutcome(null, { status: 500, code: 'internal' })).toBe('failed')
  })
})

describe('schedule', () => {
  it('reads and writes the datetime-local value in this device’s time', () => {
    const at = new Date(2026, 9, 12, 18, 30).getTime() / 1000
    expect(localInput(at)).toBe('2026-10-12T18:30')
    expect(fromLocalInput('2026-10-12T18:30')).toBe(at)
    expect(fromLocalInput('2026-02-30T10:00')).toBeNull()
    expect(fromLocalInput('۲۰۲۶-۱۰-۱۲T18:30')).toBeNull()
    expect(fromLocalInput('')).toBeNull()
  })

  it('takes a time in the future, at most 30 days ahead', () => {
    const now = 1_800_000_000
    expect(scheduleProblem(null, now)).toBe('invalid')
    expect(scheduleProblem(now, now)).toBe('past')
    expect(scheduleProblem(now + 60, now)).toBe('')
    expect(scheduleProblem(now + 30 * 86_400, now)).toBe('')
    expect(scheduleProblem(now + 30 * 86_400 + 1, now)).toBe('too_far')
    const start = defaultScheduleAt(now)
    expect(start % 900).toBe(0)
    expect(start - now).toBeGreaterThanOrEqual(3600)
    expect(start - now).toBeLessThan(3600 + 900)
  })

  it('polls one call at a time and stops', async () => {
    let calls = 0
    let inFlight = 0
    let overlap = false
    const p = poller(async () => {
      calls++
      inFlight++
      if (inFlight > 1) overlap = true
      await new Promise((r) => setTimeout(r, 15))
      inFlight--
    }, () => 5)
    p.start()
    p.start() // a second start changes nothing
    await vi.waitFor(() => expect(calls).toBeGreaterThanOrEqual(3))
    p.stop()
    expect(p.running).toBe(false)
    const stopped = calls
    await new Promise((r) => setTimeout(r, 60))
    expect(calls).toBeLessThanOrEqual(stopped + 1)
    expect(overlap).toBe(false)
  })
})

// The pages, booted like a browser against a fake core.
describe('Broadcast pages', () => {
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  const me = {
    user: { id: 'u1', telegram_id: 1001, username: 'boss', role: 'admin', language: 'fa' },
    permissions: ['overview.read', 'broadcasts.write', 'audit.read'],
    csrf: 'csrf-1',
    session: { method: 'link', expires_at: 2_000_000_000 },
    password: { enabled: false },
    password_available: true,
    brand: 'Shop',
  }
  const text = (path: string) => path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], fa) ?? path
  // Messages with values, fixed for the test (the real ones are merged into
  // the catalogs separately).
  const stubs = {
    broadcasts: {
      audience: { count: 'COUNT {n}', by_lang: 'FA {fa} EN {en}' },
      send: { confirm_now: 'NOW TO {n}', confirm_later: 'AT {time} TO {n}', chosen: 'CHOSEN {time}' },
      detail: { done_of: 'DONE {done} OF {total}', rate_value: 'RATE {n}', eta_value: 'ETA {time}' },
    },
  }

  afterEach(() => {
    // stop this test's app: Vue and PrimeVue are shared between tests
    ;(document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  type Answer = (url: string, init: RequestInit | undefined, body: Record<string, unknown>) => Response | undefined

  /** boot starts the app at path with a fake core; answer may override replies. */
  async function boot(path: string, answer: Answer = () => undefined) {
    vi.resetModules()
    vi.spyOn(console, 'warn').mockImplementation(() => {}) // texts not merged into the catalogs yet
    window.history.replaceState(null, '', path)
    document.body.innerHTML = '<div id="app"></div>'
    const calls: string[] = []
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const body = typeof init?.body === 'string' ? init.body : ''
        calls.push(`${init?.method ?? 'GET'} ${url} ${body}`.trim())
        const own = answer(url, init, body ? (JSON.parse(body) as Record<string, unknown>) : {})
        if (own) return own
        if (url === '/api/v1/me') return json(200, me)
        if (url === '/api/v1/broadcasts/audience') return json(200, { total: 1234, languages: { fa: 1000, en: 234 } })
        if (url === '/api/v1/broadcasts/check') {
          const { lang, text: value } = JSON.parse(body) as { lang: string; text: string }
          const bad = value.includes('<script>')
          return json(200, { ok: !bad, value, problems: bad ? [{ code: 'tag_not_allowed', arg: 'script', lang }] : [], length: value.length, max: 4096 })
        }
        return json(404, { error: 'not_found', message: 'not in this test' })
      }),
    )
    // The app's own copies after resetModules: shorter waits, fixed messages.
    const b = await import('../broadcasts')
    Object.assign(b.timing, { poll: 30, listPoll: 30, check: 10, audience: 10 })
    const { i18n } = await import('../i18n')
    i18n.global.mergeLocaleMessage('fa', stubs)
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    return { calls, router }
  }
  const posted = (calls: string[], path: string) => calls.filter((c) => c.startsWith('POST /api/v1' + path + ' ') || c === 'POST /api/v1' + path)
  const bodyOfCall = (c: string) => JSON.parse(c.slice(c.indexOf('{'))) as Record<string, unknown>
  function type(el: HTMLTextAreaElement | HTMLInputElement, value: string) {
    el.value = value
    el.dispatchEvent(new Event('input'))
  }
  const button = (cls: string) => document.querySelector(`button.${cls}`) as HTMLButtonElement | null

  it('lists broadcasts and opens drafts in the composer, others on their page', async () => {
    const items = [
      broadcast({ id: ID, status: 'draft' }),
      broadcast({
        id: '0b1c2d3e-0000-4000-8000-0000000000b2',
        status: 'running',
        language: 'en',
        texts: { fa: '', en: 'Big sale\nsecond line' },
        counts: { total: 3000, pending: 1700, sent: 1240, blocked: 50, failed: 10, unknown: 0 },
        started_at: 1_790_000_100,
      }),
    ]
    const { calls, router } = await boot('/admin/broadcasts', (url) => {
      if (url.startsWith('/api/v1/broadcasts?')) {
        const status = new URL(url, 'http://x').searchParams.get('status')
        const list = status ? items.filter((b) => b.status === status) : items
        return json(200, { items: list, total: list.length, page: 1, size: 25 })
      }
    })
    await vi.waitFor(() => expect(document.querySelectorAll('.p-datatable-tbody tr')).toHaveLength(2))
    expect(calls).toContain('GET /api/v1/broadcasts?page=1&size=25')
    const rows = document.querySelectorAll('.p-datatable-tbody tr')
    // raw text, the viewer's language first, else the other one; Persian digits
    expect(rows[0].textContent).toContain('سلام <b>دوستان</b>')
    expect(rows[1].textContent).toContain('Big sale')
    expect(rows[1].textContent).not.toContain('second line')
    expect(rows[1].textContent).toContain('۱٬۲۴۰ / ۳٬۰۰۰')
    expect(rows[1].querySelector('.p-progressbar')).not.toBeNull()
    expect(rows[0].querySelector('.p-progressbar')).toBeNull()
    // the status label falls back to the raw status until the catalogs have it
    const draftLabel = text('broadcasts.status.draft')
    expect(rows[0].querySelector('.p-tag')?.textContent).toBe(draftLabel === 'broadcasts.status.draft' ? 'draft' : draftLabel)

    // A running broadcast refreshes quietly.
    await vi.waitFor(() => expect(calls.filter((c) => c.startsWith('GET /api/v1/broadcasts?')).length).toBeGreaterThan(2))

    ;(rows[1].querySelector('td') as HTMLElement).click()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('broadcast'))
    expect(router.currentRoute.value.params.id).toBe('0b1c2d3e-0000-4000-8000-0000000000b2')
    await router.push('/broadcasts')
    await vi.waitFor(() => expect(document.querySelectorAll('.p-datatable-tbody tr')).toHaveLength(2))
    ;(document.querySelector('.p-datatable-tbody tr td') as HTMLElement).click()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('broadcast-edit'))
  })

  it('composes with live checks and counts, then sends only after the confirm', async () => {
    const created = broadcast({ segment: 'all' })
    const { calls, router } = await boot('/admin/broadcasts/new', (url, init, body) => {
      if (url === '/api/v1/broadcasts' && init?.method === 'POST') return json(200, { item: { ...created, ...body, button: body.button ?? null } })
      if (url === `/api/v1/broadcasts/${ID}/send`) return json(200, { item: { ...created, status: 'running' } })
      if (url === `/api/v1/broadcasts/${ID}`) return json(200, { item: { ...created, status: 'running', counts: { total: 1234, pending: 1234, sent: 0, blocked: 0, failed: 0, unknown: 0 } } })
    })
    // The audience is counted at once, in Persian digits, with both languages.
    await vi.waitFor(() => expect(document.body.textContent).toContain('COUNT ۱٬۲۳۴'))
    expect(document.body.textContent).toContain('FA ۱٬۰۰۰ EN ۲۳۴')
    expect(posted(calls, '/broadcasts/audience').map(bodyOfCall)).toEqual([{ segment: 'all', language: '' }])
    // "All languages" shows as chosen (PrimeVue never checks a radio whose value is '').
    const langRadios = [...document.querySelectorAll('input[type=radio][name$=-language]')] as HTMLInputElement[]
    expect(langRadios.map((r) => r.checked)).toEqual([true, false, false])

    // Both languages are needed: two tabs. The Persian one is open.
    expect(document.querySelectorAll('[role=tab]')).toHaveLength(2)
    const faArea = document.querySelector('textarea[lang="fa"]') as HTMLTextAreaElement
    expect(faArea.dir).toBe('rtl')
    type(faArea, 'سلام <b>همه</b> <script>alert(1)</script>')
    const preview = () => document.querySelector('.preview[lang="fa"]') as HTMLElement
    await vi.waitFor(() => expect(preview().textContent).toContain('<script>alert(1)</script>'))
    expect(preview().querySelector('script')).toBeNull()
    expect(preview().querySelector('b')?.textContent).toBe('همه')
    await vi.waitFor(() => expect(posted(calls, '/broadcasts/check').length).toBe(1))
    expect(bodyOfCall(posted(calls, '/broadcasts/check')[0])).toEqual({ lang: 'fa', text: 'سلام <b>همه</b> <script>alert(1)</script>' })
    await vi.waitFor(() => expect(document.querySelectorAll('.problems li')).toHaveLength(1))

    // A refused text keeps it from going out; nothing is posted.
    button('send-now')!.click()
    await vi.waitFor(() => expect(document.body.textContent).toContain(text('broadcasts.save.fix_first')))
    expect(document.querySelector('.send-dialog')).toBeNull()
    type(faArea, 'سلام <b>همه</b>')
    await vi.waitFor(() => expect(document.querySelectorAll('.problems li')).toHaveLength(0))

    // The English tab.
    ;(document.querySelectorAll('[role=tab]')[1] as HTMLElement).click()
    const enArea = await vi.waitFor(() => {
      const el = document.querySelector('textarea[lang="en"]') as HTMLTextAreaElement | null
      expect(el).not.toBeNull()
      return el!
    })
    expect(enArea.dir).toBe('ltr')
    type(enArea, 'Hello <i>everyone</i>')
    await vi.waitFor(() => expect(posted(calls, '/broadcasts/check').some((c) => c.includes('"lang":"en"'))).toBe(true))

    // Send now: the dialog counts again and names the count; nothing is sent before the confirm.
    await vi.waitFor(() => expect(document.querySelector('.p-tabpanels .pi-spinner')).toBeNull())
    button('send-now')!.click()
    const confirm = await vi.waitFor(() => {
      const b = document.querySelector('.send-dialog button.send-confirm') as HTMLButtonElement | null
      expect(b).not.toBeNull()
      expect(b!.disabled).toBe(false)
      return b!
    })
    expect(document.querySelector('.send-dialog')?.textContent).toContain('NOW TO ۱٬۲۳۴')
    expect(posted(calls, '/broadcasts')).toEqual([])
    confirm.click()
    await vi.waitFor(() => expect(posted(calls, `/broadcasts/${ID}/send`)).toHaveLength(1))
    expect(bodyOfCall(posted(calls, '/broadcasts')[0])).toEqual({
      segment: 'all',
      language: '',
      texts: { fa: 'سلام <b>همه</b>', en: 'Hello <i>everyone</i>' },
      button: null,
    })
    expect(bodyOfCall(posted(calls, `/broadcasts/${ID}/send`)[0])).toEqual({ at: null })
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('broadcast'))
    await vi.waitFor(() => expect(document.body.textContent).toContain('DONE ۰ OF ۱٬۲۳۴'))
  })

  it('schedules at the chosen local time, saving the edits first with their version', async () => {
    const draft = broadcast({ language: 'fa', texts: { fa: 'سلام', en: '' } })
    const { calls, router } = await boot(`/admin/broadcasts/${ID}/edit`, (url, init, body) => {
      if (url === `/api/v1/broadcasts/${ID}` && init?.method === 'PUT') return json(200, { item: { ...draft, ...body, version: 4 } })
      if (url === `/api/v1/broadcasts/${ID}/send`) return json(200, { item: { ...draft, status: 'scheduled', scheduled_at: body.at } })
      if (url === `/api/v1/broadcasts/${ID}`) return json(200, { item: draft })
    })
    const faArea = await vi.waitFor(() => {
      const el = document.querySelector('textarea[lang="fa"]') as HTMLTextAreaElement | null
      expect(el?.value).toBe('سلام')
      return el!
    })
    // Only Persian speakers: one text, no tabs, no English field.
    expect(document.querySelectorAll('[role=tab]')).toHaveLength(0)
    expect(document.querySelector('textarea[lang="en"]')).toBeNull()
    expect(button('save')!.disabled).toBe(true)
    type(faArea, 'سلام دوباره')
    await vi.waitFor(() => expect(button('save')!.disabled).toBe(false))
    await vi.waitFor(() => expect(document.querySelector('.p-card .pi-spinner')).toBeNull())

    button('schedule')!.click()
    const input = await vi.waitFor(() => {
      const el = document.querySelector('.send-dialog input[type="datetime-local"]') as HTMLInputElement | null
      expect(el).not.toBeNull()
      return el!
    })
    const when = new Date()
    when.setDate(when.getDate() + 2)
    when.setHours(9, 30, 0, 0)
    const at = Math.floor(when.getTime() / 1000)
    type(input, localInput(at))
    // The chosen time in the viewer's calendar (Jalali in Persian).
    const fmt = await import('../format')
    await vi.waitFor(() => expect(document.querySelector('.send-dialog')?.textContent).toContain('CHOSEN ' + fmt.dateTime(at, 'fa')))
    expect(document.querySelector('.send-dialog')?.textContent).toContain(`AT ${fmt.dateTime(at, 'fa')} TO ۱٬۲۳۴`)
    const confirm = document.querySelector('.send-dialog button.send-confirm') as HTMLButtonElement
    await vi.waitFor(() => expect(confirm.disabled).toBe(false))
    confirm.click()
    await vi.waitFor(() => expect(posted(calls, `/broadcasts/${ID}/send`)).toHaveLength(1))
    const put = calls.find((c) => c.startsWith(`PUT /api/v1/broadcasts/${ID}`))!
    expect(bodyOfCall(put)).toEqual({ segment: 'active', language: 'fa', texts: { fa: 'سلام دوباره', en: '' }, button: null, version: 3 })
    expect(bodyOfCall(posted(calls, `/broadcasts/${ID}/send`)[0])).toEqual({ at })
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('broadcast'))
  })

  it('tells why Send to me did not arrive, and shows field errors and stale edits', async () => {
    let testReply: Response = json(200, { ok: false, problem: 'blocked' })
    let putReply: Response = json(400, { error: 'invalid', message: 'button.url: must be https', field: 'button.url' })
    const draft = broadcast({ language: 'en', texts: { fa: '', en: 'Hi' } })
    const { calls } = await boot(`/admin/broadcasts/${ID}/edit`, (url, init) => {
      if (url === '/api/v1/broadcasts/test') return testReply
      if (url === `/api/v1/broadcasts/${ID}` && init?.method === 'PUT') return putReply
      if (url === `/api/v1/broadcasts/${ID}`) return json(200, { item: draft })
    })
    await vi.waitFor(() => expect((document.querySelector('textarea[lang="en"]') as HTMLTextAreaElement | null)?.value).toBe('Hi'))

    button('test')!.click()
    await vi.waitFor(() => expect(document.querySelector('.test-result')?.textContent).toContain(text('broadcasts.test.blocked')))
    expect(bodyOfCall(posted(calls, '/broadcasts/test')[0])).toEqual({ lang: 'en', text: 'Hi', button: null })
    testReply = json(503, { error: 'unavailable', message: 'bot down' })
    button('test')!.click()
    await vi.waitFor(() => expect(document.querySelector('.test-result')?.textContent).toContain(text('broadcasts.test.unreachable')))
    testReply = json(200, { ok: false, problem: 'bad_request', detail: 'Bad Request: BUTTON_URL_INVALID' })
    button('test')!.click()
    await vi.waitFor(() => expect(document.querySelector('.test-result')?.textContent).toContain('BUTTON_URL_INVALID'))

    // A button with a link core refuses: the reason sits under the link.
    const toggle = document.querySelector('.button-field input[type="checkbox"]') as HTMLInputElement
    toggle.click()
    const url = await vi.waitFor(() => {
      const el = document.querySelector('.button-field input[inputmode="url"]') as HTMLInputElement | null
      expect(el).not.toBeNull()
      return el!
    })
    type(url, 'https://shop.example')
    type(document.querySelector('.button-field input[lang="en"]') as HTMLInputElement, 'Open')
    await vi.waitFor(() => expect(button('save')!.disabled).toBe(false))
    button('save')!.click()
    await vi.waitFor(() => expect(url.closest('.field')?.textContent).toContain('button.url: must be https'))
    expect(bodyOfCall(calls.find((c) => c.startsWith('PUT '))!).button).toEqual({ url: 'https://shop.example', labels: { fa: '', en: 'Open' } })

    // Someone else saved first: reload instead of overwriting.
    putReply = json(409, { error: 'conflict', message: 'stale version' })
    type(url, 'https://shop.example/new')
    await vi.waitFor(() => expect(button('save')!.disabled).toBe(false))
    button('save')!.click()
    await vi.waitFor(() => expect(document.body.textContent).toContain(text('broadcasts.save.stale')))
    expect(document.querySelector('.reload')).not.toBeNull()
  })

  it('follows a running broadcast, pauses and cancels it, and lists who it missed', async () => {
    let current = broadcast({
      status: 'running',
      started_at: 1_790_000_100,
      counts: { total: 200, pending: 50, sent: 120, blocked: 20, failed: 6, unknown: 4 },
      rate_per_sec: 18.5,
      eta_seconds: 480,
      button: { url: 'https://shop.example/plans', labels: { fa: 'خرید', en: 'Buy' } },
    })
    let gets = 0
    const { calls } = await boot(`/admin/broadcasts/${ID}`, (url, init) => {
      if (url === `/api/v1/broadcasts/${ID}` && (init?.method ?? 'GET') === 'GET') {
        gets++
        return json(200, { item: current })
      }
      if (url === `/api/v1/broadcasts/${ID}/pause`) {
        current = { ...current, status: 'paused', last_error: '' }
        return json(200, { item: current })
      }
      if (url === `/api/v1/broadcasts/${ID}/cancel`) {
        current = { ...current, status: 'cancelled', finished_at: 1_790_000_900 }
        return json(200, { item: current })
      }
      if (url.startsWith(`/api/v1/broadcasts/${ID}/recipients?`)) {
        return json(200, {
          items: [{ user: { id: 'c1', telegram_id: 555, username: 'cust' }, status: 'failed', error: 'Bad Request: chat not found', done_at: 1_790_000_200 }],
          total: 1, page: 1, size: 25,
        })
      }
    })
    await vi.waitFor(() => expect(document.body.textContent).toContain('DONE ۱۵۰ OF ۲۰۰'))
    expect(document.body.textContent).toContain('۷۵٪')
    expect(document.body.textContent).toContain('RATE ۱۸٫۵')
    expect(document.body.textContent).toContain('ETA ۸ دقیقه')
    expect(document.querySelector('.counts')?.textContent).toContain('۱۲۰')
    // texts raw and previewed; the history link names this broadcast
    expect(document.querySelector('pre.raw[lang="en"]')?.textContent).toBe('Hello <b>friends</b>')
    expect(document.querySelector('.preview[lang="en"] b')?.textContent).toBe('friends')
    expect(document.querySelector(`a[href="/admin/audit?entity_id=${ID}"]`)).not.toBeNull()
    // polled every couple of seconds while it runs
    await vi.waitFor(() => expect(gets).toBeGreaterThanOrEqual(3))

    button('pause')!.click()
    await vi.waitFor(() => expect(posted(calls, `/broadcasts/${ID}/pause`)).toHaveLength(1))
    await vi.waitFor(() => expect(button('resume')).not.toBeNull())
    // paused by the bot: its reason as a warning
    current = { ...current, last_error: 'telegram unreachable' }
    await vi.waitFor(() => expect(document.querySelector('.halted')?.textContent).toContain('telegram unreachable'))

    button('cancel')!.click()
    const accept = await vi.waitFor(() => {
      const b = document.querySelector('.p-confirmdialog-accept-button') as HTMLButtonElement | null
      expect(b).not.toBeNull()
      return b!
    })
    expect(posted(calls, `/broadcasts/${ID}/cancel`)).toHaveLength(0)
    accept.click()
    await vi.waitFor(() => expect(posted(calls, `/broadcasts/${ID}/cancel`)).toHaveLength(1))
    await vi.waitFor(() => expect(button('cancel')).toBeNull())
    // over: no more polling
    const after = gets
    await new Promise((r) => setTimeout(r, 120))
    expect(gets).toBe(after)

    // The failed and blocked tab loads when opened.
    expect(calls.some((c) => c.includes('/recipients'))).toBe(false)
    ;(document.querySelectorAll('[role=tab]')[1] as HTMLElement).click()
    await vi.waitFor(() => expect(document.querySelector('.recipients')?.textContent).toContain('chat not found'))
    expect(calls).toContain(`GET /api/v1/broadcasts/${ID}/recipients?status=failed&page=1&size=25`)
  })
})
