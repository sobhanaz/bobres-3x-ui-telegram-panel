import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, ref } from 'vue'
import PrimeVue from 'primevue/config'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import ar from '../locales/ar.json'
import ru from '../locales/ru.json'
import tr from '../locales/tr.json'
import { isolate } from '../format'
import { i18n, setLang } from '../i18n'
import ChannelCardsEditor from '../components/settings/ChannelCardsEditor.vue'
import { describeAccess, tmePostChat } from '../settings'
import type { ChannelCheck, SettingItem } from '../types'

// The "Check the bot's access" button under the post link of each channel button: it asks the bot whether it can copy the posts of the post's channel (an
// administrator of a channel that does not restrict saving content), and the answer is shown under the row, belongs to the row and goes when the post changes.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

const rows3 = JSON.stringify([
  // a public post, with a channel link that is not the post's own: the bot copies from the post's channel
  { label: { en: 'A' }, channel: 'https://t.me/news_channel', post: 'https://t.me/aaaaaa/1' },
  // a private channel's post
  { label: { en: 'B' }, channel: 'https://t.me/+AbCdEfGh12', post: 'https://t.me/c/1234567890/45' },
  { label: { en: 'C' }, post: 'https://t.me/cccccc/1' },
])

describe('the chat of a post, and what the bot says of it', () => {
  it('names the chat the bot copies from: @name for a public channel, -100… for a private one, nothing for what is not a post', () => {
    expect(tmePostChat('https://t.me/heisen_reviews/2')).toBe('@heisen_reviews')
    expect(tmePostChat(' https://t.me/s/Heisen_Reviews/17?single ')).toBe('@Heisen_Reviews')
    expect(tmePostChat('https://telegram.me/heisen_reviews/9#top')).toBe('@heisen_reviews')
    expect(tmePostChat('https://t.me/c/1234567890/45')).toBe('-1001234567890')
    expect(tmePostChat('https://t.me/c/1/2')).toBe('-1001')
    expect(tmePostChat('https://t.me/c/0012/2')).toBe('-1000012') // the digits as the link has them, as core reads them
    for (const bad of ['', '  ', 'nope', 'https://t.me/heisen_reviews', 'http://t.me/heisen_reviews/2', 'https://t.me/share/2', 'https://t.me/c/abc/4', 'https://t.me/+AbCdEfGh12']) {
      expect(tmePostChat(bad), JSON.stringify(bad)).toBe('')
    }
  })

  it('is read as a message: success only for an administrator of a channel that lets its posts be copied', () => {
    const ok: ChannelCheck = { ok: true, title: 'News', bot_admin: true }
    expect(describeAccess(ok)).toEqual({ severity: 'success', key: 'settings.channel_cards.check_ok', title: 'News', detail: '' })
    expect(describeAccess({ ...ok, protected_content: true })).toEqual({ severity: 'error', key: 'settings.channel_cards.check_protected', title: 'News', detail: '' })
    expect(describeAccess({ ...ok, protected_content: false }).key).toBe('settings.channel_cards.check_ok')
    // core says the bot is an administrator with ok alone; an answer that says it is not one is read that way
    expect(describeAccess({ ok: true, title: 'News', bot_admin: false })).toEqual({ severity: 'warn', key: 'settings.channel_cards.check_not_admin', title: 'News', detail: '' })
    expect(describeAccess({ ok: true, title: 'News' }).key).toBe('settings.channel_cards.check_ok')
    // the problems: what to do about each, never Telegram's own words but for a check that went wrong
    expect(describeAccess({ ok: false, problem: 'not_admin', detail: 'Forbidden: the bot was kicked' })).toEqual({
      severity: 'warn',
      key: 'settings.channel_cards.check_not_admin',
      title: '',
      detail: '',
    })
    expect(describeAccess({ ok: false, problem: 'not_found', detail: 'Bad Request: chat not found' })).toMatchObject({ severity: 'error', key: 'settings.channel_cards.check_not_found', detail: '' })
    expect(describeAccess({ ok: false, problem: 'not_channel' })).toMatchObject({ severity: 'error', key: 'settings.channel_cards.check_not_channel', detail: '' })
    expect(describeAccess({ ok: false, problem: 'error', detail: 'Bad Gateway' })).toEqual({ severity: 'error', key: 'settings.channel_cards.check_error', title: '', detail: 'Bad Gateway' })
    expect(describeAccess({ ok: false })).toMatchObject({ severity: 'error', key: 'settings.channel_cards.check_error', detail: '' })
    expect(describeAccess({ ok: false, problem: 'weird' as 'error', detail: 'x' })).toMatchObject({ key: 'settings.channel_cards.check_error', detail: 'x' })
    // a channel that restricts saving content is only said so for a check that found the bot an administrator
    expect(describeAccess({ ok: false, problem: 'not_admin', protected_content: true }).key).toBe('settings.channel_cards.check_not_admin')
  })
})

describe('the Check the bot’s access button of a row', () => {
  beforeEach(async () => {
    await setLang('en')
  })
  afterEach(() => {
    document.body.innerHTML = ''
    vi.unstubAllGlobals()
    void setLang('fa')
  })

  interface Call {
    method: string
    url: string
    body: { chat?: string }
  }
  type Answer = (call: Call) => Response | Promise<Response>

  /** stubCore is a core that answers the calls the editor makes and keeps them. */
  function stubCore(answer: Answer) {
    const calls: Call[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const call = { method: init?.method ?? 'GET', url, body: typeof init?.body === 'string' ? JSON.parse(init.body) : {} }
        calls.push(call)
        return answer(call)
      }),
    )
    return calls
  }
  /** deferred is an answer that comes when the test says so. */
  function deferred() {
    let resolve: (r: Response) => void = () => {}
    const promise = new Promise<Response>((r) => (resolve = r))
    return { promise, resolve }
  }

  /** mountEditor shows the editor the way the card does: it takes what comes down and hands back what it is given. */
  async function mountEditor(initial: string, props: Record<string, unknown> = {}) {
    const value = ref(initial)
    const emitted: string[] = []
    const Host = defineComponent({
      render: () =>
        h(ChannelCardsEditor, {
          modelValue: value.value,
          inputId: 'cards',
          ...props,
          'onUpdate:modelValue': (v: string) => {
            emitted.push(v)
            value.value = v
          },
        }),
    })
    const root = document.createElement('div')
    document.body.appendChild(root)
    const app = createApp(Host)
    app.use(i18n).use(PrimeVue)
    app.mount(root)
    await nextTick()
    return { root, value, emitted, unmount: () => app.unmount() }
  }
  const rowEls = (root: HTMLElement) => [...root.querySelectorAll('.channel-card')] as HTMLElement[]
  const checkBtn = (root: HTMLElement, i: number) => rowEls(root)[i].querySelector('button.check-btn') as HTMLButtonElement
  const messageOf = (root: HTMLElement, i: number) => rowEls(root)[i].querySelector('.p-message') as HTMLElement | null
  const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
  const type = (input: HTMLInputElement, v: string) => {
    input.value = v
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }
  const inputOf = (root: HTMLElement, i: number, n: 0 | 1 | 2 | 3) => rowEls(root)[i].querySelectorAll('input')[n] as HTMLInputElement // text fa, text en, channel, post
  const isolated = (key: string, title: string) => (en.settings.channel_cards as Record<string, string>)[key].replace('{title}', isolate(title))
  const adminOf = (title = 'News', extra: Partial<ChannelCheck> = {}) => json(200, { ok: true, title, bot_admin: true, can_post: false, ...extra })
  const check = async (root: HTMLElement, i: number) => {
    checkBtn(root, i).click()
    await vi.waitFor(() => expect(messageOf(root, i)).not.toBeNull())
  }

  it('is under the post link of every row, named as the bot’s hint names it, and waits for a link of a post', async () => {
    stubCore(() => adminOf())
    const { root, unmount } = await mountEditor(rows3)
    expect(rowEls(root)).toHaveLength(3)
    for (let i = 0; i < 3; i++) {
      const btn = checkBtn(root, i)
      expect(text(btn)).toBe("Check the bot's access")
      expect(btn.type).toBe('button') // not the Save of the card
      expect(btn.querySelector('.pi-search')).not.toBeNull()
      expect(btn.disabled).toBe(false)
      expect(btn.hasAttribute('title')).toBe(false)
      // in the cell of the post link, after its hint
      expect(btn.closest('.cell')?.querySelector('input')).toBe(inputOf(root, i, 3))
    }
    // a new row has no link of a post: the button waits, and says why
    ;[...root.querySelectorAll('button')].find((b) => text(b) === 'Add a button')!.click()
    await nextTick()
    const blank = checkBtn(root, 3)
    expect(blank.disabled).toBe(true)
    expect(blank.title).toBe(en.settings.channel_cards.check_need_post)
    // a link that is not a post's is no better; a post's link opens it, in any form core reads
    type(inputOf(root, 3, 3), 'https://t.me/heisen_reviews')
    await nextTick()
    expect(blank.disabled).toBe(true)
    type(inputOf(root, 3, 3), 'https://t.me/s/heisen_reviews/2?single')
    await nextTick()
    expect(blank.disabled).toBe(false)
    expect(blank.hasAttribute('title')).toBe(false)
    type(inputOf(root, 3, 3), '')
    await nextTick()
    expect(blank.disabled).toBe(true)
    unmount()
  })

  it('is not there for a role that may not change the buttons', async () => {
    const calls = stubCore(() => adminOf())
    const { root, unmount } = await mountEditor(rows3, { locked: true })
    expect(root.querySelectorAll('button')).toHaveLength(0)
    expect(calls).toEqual([])
    unmount()
  })

  it('asks about the channel of the post, not of the View channel link: @name for a public post, -100… for a private one', async () => {
    const calls = stubCore(() => adminOf())
    const { root, emitted, unmount } = await mountEditor(rows3)
    await check(root, 0)
    expect(calls).toEqual([{ method: 'POST', url: '/api/v1/settings/channel-check', body: { chat: '@aaaaaa' } }]) // not @news_channel
    await check(root, 1)
    expect(calls.at(-1)).toEqual({ method: 'POST', url: '/api/v1/settings/channel-check', body: { chat: '-1001234567890' } }) // not the invite link
    // typed any way a post link is typed
    type(inputOf(root, 2, 3), ' https://telegram.me/Third_Channel/7?single ')
    await nextTick()
    await check(root, 2)
    expect(calls.at(-1)?.body).toEqual({ chat: '@Third_Channel' })
    expect(calls).toHaveLength(3)
    // asking changes nothing in the list: the card has nothing more to save for it
    expect(emitted.every((v) => !v.includes('check') && !v.includes('uid'))).toBe(true)
    unmount()
  })

  it('says what the bot found, in a small message that does not close, with the severity of what it means', async () => {
    const title = 'Heisen News'
    const detail = 'Bad Gateway: try later'
    const cases: [string, ChannelCheck | Record<string, unknown>, 'success' | 'warn' | 'error', string, string?][] = [
      ['an administrator of a channel that can be copied', { ok: true, title, bot_admin: true, can_post: false }, 'success', isolated('check_ok', title)],
      ['an administrator of a channel with Restrict saving content on', { ok: true, title, bot_admin: true, protected_content: true }, 'error', isolated('check_protected', title)],
      ['not an administrator', { ok: false, problem: 'not_admin', detail: 'Forbidden: nope' }, 'warn', en.settings.channel_cards.check_not_admin],
      ['a channel Telegram does not know', { ok: false, problem: 'not_found', detail: 'Bad Request: chat not found' }, 'error', en.settings.channel_cards.check_not_found],
      ['a chat that is not a channel or a group', { ok: false, problem: 'not_channel', detail: 'this chat is a private, not a channel or a group' }, 'error', en.settings.channel_cards.check_not_channel],
      ['a check that went wrong', { ok: false, problem: 'error', detail }, 'error', en.settings.channel_cards.check_error, detail],
      ['an answer with no reason', { ok: false }, 'error', en.settings.channel_cards.check_error],
      ['a reason it does not know', { ok: false, problem: 'weird', detail }, 'error', en.settings.channel_cards.check_error, detail],
      ['an administrator that is said not to be one', { ok: true, title, bot_admin: false }, 'warn', en.settings.channel_cards.check_not_admin],
    ]
    for (const [name, answer, severity, want, wantDetail] of cases) {
      document.body.innerHTML = ''
      stubCore(() => json(200, answer))
      const { root, emitted, unmount } = await mountEditor(rows3)
      await check(root, 0)
      const message = messageOf(root, 0)!
      expect(message.classList.contains(`p-message-${severity}`), name).toBe(true)
      expect(message.classList.contains('p-message-sm'), name).toBe(true)
      expect(message.querySelector('.p-message-close-button'), name).toBeNull() // not closable
      expect(message.getAttribute('aria-live'), name).toBe('polite')
      expect(text(message), name).toBe(wantDetail ? `${want} ${wantDetail}` : want)
      // Telegram's own words are in a left to right line of their own, and only where the check itself went wrong
      expect(message.querySelector('.app-ltr.detail')?.textContent ?? '', name).toBe(wantDetail ?? '')
      // it is under the row's fields, and only that row has one
      expect(rowEls(root)[0].lastElementChild, name).toBe(message)
      expect(messageOf(root, 1), name).toBeNull()
      expect(messageOf(root, 2), name).toBeNull()
      expect(emitted, name).toEqual([])
      unmount()
    }
  })

  it('isolates the title of the channel, which is often Latin in Persian text, and falls back to the chat when it has none', async () => {
    stubCore((call) => (call.body.chat === '@aaaaaa' ? adminOf('Heisen News') : adminOf('')))
    const { root, unmount } = await mountEditor(rows3)
    await check(root, 0)
    expect(messageOf(root, 0)?.textContent).toContain(isolate('Heisen News'))
    await check(root, 2)
    expect(text(messageOf(root, 2))).toBe(isolated('check_ok', '@cccccc'))
    unmount()
  })

  it('says why it could not ask: the bot not answering, core refusing the chat, the network, a failure', async () => {
    const failures: [string, Answer | (() => Promise<Response>), string, string][] = [
      ['the bot does not answer', () => json(503, { error: 'unavailable', message: 'the bot did not answer; try again in a minute' }), en.settings.check.unreachable, ''],
      [
        'core refuses the chat',
        () => json(400, { error: 'invalid', field: 'join.channel', message: "join.channel: a public channel's @name, or a private channel's id starting with -100" }),
        en.error.invalid,
        "join.channel: a public channel's @name, or a private channel's id starting with -100",
      ],
      ['no network', () => Promise.reject(new TypeError('offline')), en.error.network, ''],
      ['a failure of core', () => json(500, { error: 'internal', message: 'boom' }), en.error.generic, ''],
      ['no permission', () => json(403, { error: 'forbidden', message: 'no' }), en.error.forbidden, ''],
    ]
    for (const [name, answer, want, wantDetail] of failures) {
      document.body.innerHTML = ''
      stubCore(answer)
      const { root, unmount } = await mountEditor(rows3)
      await check(root, 1)
      const message = messageOf(root, 1)!
      expect(message.classList.contains('p-message-error'), name).toBe(true)
      expect(text(message), name).toBe(wantDetail ? `${want} ${wantDetail}` : want)
      expect(message.querySelector('.app-ltr.detail')?.textContent ?? '', name).toBe(wantDetail) // core's own words, left to right
      expect(checkBtn(root, 1).disabled, name).toBe(false) // it can be tried again
      unmount()
    }
  })

  it('shows that it is asking, and asks once however often the button is pressed', async () => {
    const answer = deferred()
    const calls = stubCore(() => answer.promise)
    const { root, unmount } = await mountEditor(rows3)
    const btn = checkBtn(root, 0)
    expect(rowEls(root)[0].hasAttribute('aria-busy')).toBe(false)
    btn.click()
    btn.click() // before the page has drawn the first one
    await nextTick()
    expect(calls).toHaveLength(1)
    expect(btn.disabled).toBe(true)
    expect(btn.classList.contains('p-button-loading')).toBe(true)
    expect(rowEls(root)[0].getAttribute('aria-busy')).toBe('true')
    expect(rowEls(root)[1].hasAttribute('aria-busy')).toBe(false) // the others are not waiting
    expect(messageOf(root, 0)).toBeNull()
    btn.click() // a disabled button does nothing
    await nextTick()
    expect(calls).toHaveLength(1)
    answer.resolve(adminOf())
    await vi.waitFor(() => expect(messageOf(root, 0)).not.toBeNull())
    expect(btn.disabled).toBe(false)
    expect(btn.classList.contains('p-button-loading')).toBe(false)
    expect(rowEls(root)[0].hasAttribute('aria-busy')).toBe(false)
    expect(calls).toHaveLength(1)
    unmount()
  })

  it('asks for two rows at once, each answer going to its own row', async () => {
    const first = deferred()
    const second = deferred()
    const calls = stubCore((call) => (call.body.chat === '@aaaaaa' ? first.promise : second.promise))
    const { root, unmount } = await mountEditor(rows3)
    checkBtn(root, 0).click()
    checkBtn(root, 1).click()
    await nextTick()
    expect(calls.map((c) => c.body.chat)).toEqual(['@aaaaaa', '-1001234567890'])
    expect(rowEls(root).map((r) => r.getAttribute('aria-busy'))).toEqual(['true', 'true', null])
    second.resolve(json(200, { ok: false, problem: 'not_admin' }))
    await vi.waitFor(() => expect(messageOf(root, 1)).not.toBeNull())
    expect(messageOf(root, 0)).toBeNull() // the first is still asking
    expect(rowEls(root)[0].getAttribute('aria-busy')).toBe('true')
    first.resolve(adminOf('A'))
    await vi.waitFor(() => expect(messageOf(root, 0)).not.toBeNull())
    expect(messageOf(root, 1)?.classList.contains('p-message-warn')).toBe(true)
    expect(messageOf(root, 0)?.classList.contains('p-message-success')).toBe(true)
    unmount()
  })

  it('takes the answer away when the post link changes, and keeps it when something else does', async () => {
    stubCore(() => adminOf())
    const { root, unmount } = await mountEditor(rows3)
    await check(root, 0)
    await check(root, 1)
    // the text, in both languages, and the View channel link are not what was checked
    type(inputOf(root, 0, 0), 'کانال')
    type(inputOf(root, 0, 1), 'Alpha')
    type(inputOf(root, 0, 2), 'https://t.me/other_news')
    await nextTick()
    expect(messageOf(root, 0)).not.toBeNull()
    // the post link is
    type(inputOf(root, 0, 3), 'https://t.me/aaaaaa/2')
    await nextTick()
    expect(messageOf(root, 0)).toBeNull()
    expect(messageOf(root, 1)).not.toBeNull() // the other rows are not touched
    expect(checkBtn(root, 0).disabled).toBe(false) // it can ask again, about what is typed now
    // and a link that is not one takes the answer away and the button too
    type(inputOf(root, 1, 3), 'https://t.me/c/1234567890/')
    await nextTick()
    expect(messageOf(root, 1)).toBeNull()
    expect(checkBtn(root, 1).disabled).toBe(true)
    unmount()
  })

  it('takes the answer away when a new check begins, and puts the new one in its place', async () => {
    let n = 0
    const second = deferred()
    stubCore(() => (++n === 1 ? json(200, { ok: false, problem: 'not_found' }) : second.promise))
    const { root, unmount } = await mountEditor(rows3)
    await check(root, 0)
    expect(messageOf(root, 0)?.classList.contains('p-message-error')).toBe(true)
    checkBtn(root, 0).click()
    await nextTick()
    expect(messageOf(root, 0)).toBeNull() // not the old answer while the new one is awaited
    expect(rowEls(root)[0].getAttribute('aria-busy')).toBe('true')
    second.resolve(adminOf('News'))
    await vi.waitFor(() => expect(messageOf(root, 0)?.classList.contains('p-message-success')).toBe(true))
    expect(messageOf(root, 0)?.classList.contains('p-message-error')).toBe(false)
    unmount()
  })

  it('belongs to its row: it goes when the row is removed, and goes with the row when it is moved', async () => {
    stubCore((call) => (call.body.chat === '@aaaaaa' ? adminOf('First') : call.body.chat === '@cccccc' ? json(200, { ok: false, problem: 'not_channel' }) : adminOf('Second')))
    const { root, unmount } = await mountEditor(rows3)
    await check(root, 0)
    await check(root, 2)
    const first = text(messageOf(root, 0))
    const third = text(messageOf(root, 2))
    expect(first).toContain('First')
    // the first row moves down: its answer goes with it
    root.querySelector<HTMLButtonElement>('button[aria-label="Move down: button 1"]')!.click()
    await nextTick()
    expect(inputOf(root, 1, 1).value).toBe('A')
    expect(messageOf(root, 0)).toBeNull()
    expect(text(messageOf(root, 1))).toBe(first)
    expect(text(messageOf(root, 2))).toBe(third)
    // and up again
    root.querySelector<HTMLButtonElement>('button[aria-label="Move up: button 2"]')!.click()
    await nextTick()
    expect(text(messageOf(root, 0))).toBe(first)
    expect(messageOf(root, 1)).toBeNull()
    // a row that is removed takes its answer with it; the rows after it keep theirs, now one place up
    root.querySelector<HTMLButtonElement>('button[aria-label="Remove button 1"]')!.click()
    await nextTick()
    expect(rowEls(root)).toHaveLength(2)
    expect(messageOf(root, 0)).toBeNull() // the row that was second never had one
    expect(text(messageOf(root, 1))).toBe(third)
    expect(inputOf(root, 1, 1).value).toBe('C')
    unmount()
  })

  it('drops an answer that comes after its row was removed or its post changed, and gives one to a row that moved meanwhile', async () => {
    const late = deferred()
    const calls = stubCore(() => late.promise)
    // the row removed while the bot answers: the one that takes its place has no answer it did not ask for
    let { root, unmount } = await mountEditor(rows3)
    checkBtn(root, 0).click()
    await nextTick()
    root.querySelector<HTMLButtonElement>('button[aria-label="Remove button 1"]')!.click()
    await nextTick()
    expect(rowEls(root)).toHaveLength(2)
    expect(rowEls(root)[0].hasAttribute('aria-busy')).toBe(false)
    late.resolve(adminOf())
    await new Promise((resolve) => setTimeout(resolve, 30))
    expect(messageOf(root, 0)).toBeNull()
    expect(messageOf(root, 1)).toBeNull()
    unmount()

    // the post changed while the bot answers: the answer is about a link that is not there any more
    document.body.innerHTML = ''
    const second = deferred()
    calls.length = 0
    stubCore(() => second.promise)
    ;({ root, unmount } = await mountEditor(rows3))
    checkBtn(root, 0).click()
    await nextTick()
    type(inputOf(root, 0, 3), 'https://t.me/aaaaaa/2')
    await nextTick()
    expect(rowEls(root)[0].hasAttribute('aria-busy')).toBe(false) // not waiting for an answer it will not use
    expect(checkBtn(root, 0).disabled).toBe(false)
    second.resolve(adminOf())
    await new Promise((resolve) => setTimeout(resolve, 30))
    expect(messageOf(root, 0)).toBeNull()
    unmount()

    // the row moved while the bot answers: the answer is for the row, wherever it is now
    document.body.innerHTML = ''
    const third = deferred()
    stubCore(() => third.promise)
    ;({ root, unmount } = await mountEditor(rows3))
    checkBtn(root, 0).click()
    await nextTick()
    root.querySelector<HTMLButtonElement>('button[aria-label="Move down: button 1"]')!.click()
    await nextTick()
    expect(rowEls(root)[1].getAttribute('aria-busy')).toBe('true')
    third.resolve(adminOf('Moved'))
    await vi.waitFor(() => expect(messageOf(root, 1)).not.toBeNull())
    expect(messageOf(root, 0)).toBeNull()
    expect(text(messageOf(root, 1))).toBe(isolated('check_ok', 'Moved'))
    unmount()
  })

  it('goes with a list that comes down from the card (an undo), but not with its own echo', async () => {
    stubCore(() => adminOf())
    const { root, value, unmount } = await mountEditor(rows3)
    await check(root, 0)
    // the card hands back what the editor gave it: nothing changes
    value.value = value.value
    await nextTick()
    expect(messageOf(root, 0)).not.toBeNull()
    // the list the card sets again (Undo changes) is another list of rows
    value.value = JSON.stringify([{ label: { en: 'Z' }, post: 'https://t.me/zzzzzz/1' }])
    await nextTick()
    expect(rowEls(root)).toHaveLength(1)
    expect(messageOf(root, 0)).toBeNull()
    unmount()
  })

  it('reads in Persian: the button is named as the bot’s hint names it, and the answer is in Persian with the title isolated', async () => {
    await setLang('fa')
    stubCore(() => adminOf('Heisen News'))
    const { root, unmount } = await mountEditor(rows3)
    expect(text(checkBtn(root, 0))).toBe('بررسی دسترسی ربات')
    await check(root, 0)
    expect(text(messageOf(root, 0))).toBe(fa.settings.channel_cards.check_ok.replace('{title}', isolate('Heisen News')))
    // the reason a blank row cannot be checked is in Persian too
    ;[...root.querySelectorAll('button')].find((b) => text(b) === fa.settings.channel_cards.add)!.click()
    await nextTick()
    expect(checkBtn(root, 3).title).toBe(fa.settings.channel_cards.check_need_post)
    unmount()
  })
})

describe('the texts of the check', () => {
  const get = (o: unknown, path: string): unknown => path.split('.').reduce<unknown>((a, k) => (a as Record<string, unknown> | undefined)?.[k], o)
  const keys = ['check', 'check_need_post', 'check_ok', 'check_protected', 'check_not_admin', 'check_not_found', 'check_not_channel', 'check_error'].map((k) => `settings.channel_cards.${k}`)
  const names = (s: string) => [...new Set([...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))].sort()
  // The bot's hint to staff names the button, so its words are fixed.
  const labels = { en: "Check the bot's access", fa: 'بررسی دسترسی ربات', ar: 'فحص صلاحيات البوت', ru: 'Проверить доступ бота', tr: 'Botun erişimini denetle' } as const
  const catalogs = { en, fa, ar, ru, tr } as const

  it('are in all five languages, with the same values to fill in, and the button has the words the bot’s hint names it by', () => {
    for (const [lang, cat] of Object.entries(catalogs)) {
      for (const k of keys) {
        const a = get(en, k)
        const b = get(cat, k)
        expect(typeof a, `en ${k}`).toBe('string')
        expect(typeof b === 'string' && b.trim() !== '', `${lang} ${k}`).toBe(true)
        expect(names(b as string), `${lang} ${k}`).toEqual(names(a as string))
      }
      expect(cat.settings.channel_cards.check, lang).toBe(labels[lang as keyof typeof labels])
    }
    // only the two that name the channel have a title to fill in
    expect(names(en.settings.channel_cards.check_ok)).toEqual(['title'])
    expect(names(en.settings.channel_cards.check_protected)).toEqual(['title'])
    expect(names(en.settings.channel_cards.check_not_admin)).toEqual([])
  })

  it('are pointed at by the help of the channel buttons, in each language, by the words of the button', () => {
    for (const [lang, cat] of Object.entries(catalogs)) {
      const label = labels[lang as keyof typeof labels]
      expect(cat.settings.group.channels.note, `${lang} note`).toContain(label)
      expect(cat.settings.field.bot.channel_cards.help, `${lang} help`).toContain(label)
    }
  })

  it('say the honest things: the success does not vouch for the post, the failure says what to do', () => {
    expect(en.settings.channel_cards.check_ok).toContain('does not check that this post exists')
    expect(en.settings.channel_cards.check_protected).toContain('Restrict saving content')
    expect(en.settings.channel_cards.check_not_admin).toContain('no special rights')
    expect(en.settings.channel_cards.check_not_found).toContain('link of the post')
  })
})

describe('the Settings page', () => {
  const me = {
    user: { id: 'u0', telegram_id: 1, username: 'boss', role: 'owner', language: 'fa' },
    permissions: ['overview.read', 'settings.write'],
    csrf: 'csrf-1',
    session: { method: 'link', expires_at: 2_000_000_000 },
    password: { enabled: false },
    password_available: true,
    brand: 'Shop',
    branding: { color: '#0f766e', logo: null, currency: { fa: '', en: 'T' } },
  }
  const item = (extra: Partial<SettingItem> = {}): SettingItem => ({
    key: 'bot.channel_cards', group: 'channels', kind: 'list', value: '', default: '', options: [], min: null, max: null, editable: true, ...extra,
  })

  afterEach(() => {
    ;(document.getElementById('app') as (HTMLElement & { __vue_app__?: { unmount(): void } }) | null)?.__vue_app__?.unmount()
    vi.unstubAllGlobals()
    try {
      localStorage.removeItem('bobres.lang')
    } catch {
      /* no storage in this environment */
    }
    document.body.innerHTML = ''
  })

  it('has the button in its card: it asks the bot through core, and what it says is not a change to save', async () => {
    vi.resetModules()
    localStorage.setItem('bobres.lang', 'en')
    window.history.replaceState(null, '', '/admin/settings')
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        calls.push(`${method} ${url} ${typeof init?.body === 'string' ? init.body : ''}`.trim())
        if (url === '/api/v1/me') return json(200, me)
        if (url === '/api/v1/settings' && method === 'GET') {
          return json(200, { items: [item({ value: '[{"label":{"en":"Reviews"},"post":"https://t.me/heisen_reviews/2"}]' })], groups: ['channels'] })
        }
        if (url === '/api/v1/settings/channel-check') return json(200, { ok: true, title: 'Heisen Reviews', bot_admin: true, protected_content: true })
        return json(404, { error: 'not_found', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    await vi.waitFor(() => expect(document.querySelector('.settings-card .channel-card')).not.toBeNull())
    const card = document.querySelector('.settings-card') as HTMLElement
    const btn = card.querySelector('button.check-btn') as HTMLButtonElement
    expect(btn.textContent?.trim()).toBe("Check the bot's access")
    btn.click()
    await vi.waitFor(() => expect(card.querySelector('.channel-card .p-message-error')).not.toBeNull())
    expect(calls).toContain('POST /api/v1/settings/channel-check {"chat":"@heisen_reviews"}')
    expect(card.querySelector('.channel-card .p-message')?.textContent).toContain(isolate('Heisen Reviews'))
    expect(card.querySelector('.channel-card .p-message')?.textContent).toContain('Restrict saving content')
    // the owner has changed nothing: Save is still off, and no PUT went out
    expect((card.querySelector('button[type=submit]') as HTMLButtonElement).disabled).toBe(true)
    expect(calls.some((c) => c.startsWith('PUT '))).toBe(false)
  })
})
