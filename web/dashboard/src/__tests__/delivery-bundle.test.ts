import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, ref } from 'vue'
import PrimeVue from 'primevue/config'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import ar from '../locales/ar.json'
import ru from '../locales/ru.json'
import tr from '../locales/tr.json'
import { DEFAULT_LIMITS } from '../help'
import { i18n, setLang } from '../i18n'
import SettingField from '../components/settings/SettingField.vue'
import TutorialPicker from '../components/settings/TutorialPicker.vue'
import { canonical, changed, changes, example, ltr, maxLength, normalize, problems, tmePostLink, validate } from '../settings'
import type { HelpItem, HelpPage, SettingItem } from '../types'

// What the bot sends after a service is delivered (the group "After a purchase"): the switches, the link of the tutorial video, which is the link of a
// post in the owner's channel, and the tutorial, which is picked from the Tutorials section; and the card of the Settings page that holds them.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

function item(key: string, kind: SettingItem['kind'], extra: Partial<SettingItem> = {}): SettingItem {
  return { key, group: 'delivery', kind, value: '', default: '', options: [], min: null, max: null, editable: true, ...extra }
}
const bundle = (extra: Partial<SettingItem> = {}) => item('delivery.bundle', 'bool', { default: 'true', ...extra })
const bundleTrial = (extra: Partial<SettingItem> = {}) => item('delivery.bundle_trial', 'bool', { default: 'true', ...extra })
const videoPost = (extra: Partial<SettingItem> = {}) => item('delivery.video_post', 'url', extra)
const tutorial = (extra: Partial<SettingItem> = {}) => item('delivery.tutorial', 'text', extra)
const deliveryItems = (): SettingItem[] => [bundle(), bundleTrial(), videoPost(), tutorial()]

function tut(id: string, titleFa: string, titleEn: string, over: Partial<HelpItem> = {}): HelpItem {
  return {
    id, kind: 'tutorial', title: { fa: titleFa, en: titleEn }, body: { fa: 'متن', en: 'Text' }, buttons: [], sort: 1, enabled: true, version: 1, updated_at: 1_790_000_000, ...over,
  }
}
const download = () => tut('11111111-1111-4111-8111-111111111111', 'دانلود برنامه', 'Download the app')
const connect = () => tut('22222222-2222-4222-8222-222222222222', '', 'Connect', { sort: 2 }) // written in English only
const hidden = () => tut('33333333-3333-4333-8333-333333333333', 'پنهان', 'Hidden', { sort: 3, enabled: false })
const helpPage = (tutorials: HelpItem[]): HelpPage => ({
  faq: [{ ...tut('q1', 'سوال', 'Question'), kind: 'faq' }], tutorials, faq_layout: { fa: 'message', en: 'message' }, limits: DEFAULT_LIMITS, languages: ['fa', 'en'],
})

describe('the link of the tutorial video', () => {
  const video = videoPost()
  const check = (raw: string) => validate(video, normalize(video, raw))?.code ?? null

  it('is a link to a post in a Telegram channel, made canonical as it is typed', () => {
    expect(normalize(video, ' https://t.me/mychannel/12 ')).toBe('https://t.me/mychannel/12')
    expect(normalize(video, 'https://t.me/s/mychannel/12?single')).toBe('https://t.me/mychannel/12')
    expect(normalize(video, 'https://telegram.me/MyChannel/12#top')).toBe('https://t.me/MyChannel/12')
    expect(normalize(video, 'https://t.me/c/1234567890/45/')).toBe('https://t.me/c/1234567890/45')
    expect(normalize(video, 'https://t.me/mychannel/0012')).toBe('https://t.me/mychannel/12')
    expect(normalize(video, ' not a link ')).toBe('not a link') // left as typed, for validate to refuse
    for (const ok of ['https://t.me/mychannel/12', 'https://t.me/s/mychannel/12?single', 'https://telegram.me/mychannel/12', 'https://t.me/c/1234567890/45']) {
      expect(check(ok), ok).toBeNull()
    }
    expect(check('')).toBeNull() // empty: no video
  })

  it('is refused with a code of its own for anything else, a plain https link included', () => {
    for (const bad of [
      'https://example.com/video.mp4', 'http://t.me/mychannel/12', 'https://t.me/mychannel', 'https://t.me/mychannel/0', 'https://t.me/mychannel/12/3', 'https://t.me/abcd/1',
      'https://t.me/+AbCdEfGh12', 'https://t.me/joinchat/AbCdEfGh12', 'https://user@t.me/mychannel/12', 'https://t.me:8443/mychannel/12', 'https://t.me/mychannel/12 x',
      'mychannel/12', 'tg://resolve?domain=mychannel&post=12', 'https://t.me/joinchat/12', 'https://t.me/share/12',
    ]) {
      expect(check(bad), bad).toBe('tme_post')
    }
    // the check is by the key: other links keep their rules
    expect(validate(item('payments.zarinpal_link', 'url'), 'https://example.com/pay')).toBeNull()
    expect(validate(item('join.link', 'url'), 'https://t.me/+AbCdEfGh12')).toBeNull()
    expect(validate(item('join.link', 'url'), 'https://t.me/mychannel/12')).toBeNull() // a join link takes any t.me address
    expect(validate(item('join.link', 'url'), 'https://example.com/x')).toEqual({ code: 'tme_link' })
    expect(validate(item('payments.zarinpal_link', 'url'), 'https://t.me/mychannel/12')).toBeNull()
  })

  it('is shared with the post of a channel button', () => {
    expect(tmePostLink('https://t.me/s/mychannel/12?single')).toBe('https://t.me/mychannel/12')
    expect(tmePostLink('https://t.me/mychannel')).toBeNull()
  })

  it('is sent only when it changed, as it will be stored', () => {
    const stored = videoPost({ value: 'https://t.me/mychannel/12' })
    expect(changed(stored, 'https://t.me/s/mychannel/12?single')).toBe(false) // the post it already is
    expect(changes([stored], { 'delivery.video_post': 'https://telegram.me/mychannel/13' })).toEqual({ 'delivery.video_post': 'https://t.me/mychannel/13' })
    expect(changes([videoPost()], { 'delivery.video_post': 'https://t.me/s/mychannel/12' })).toEqual({ 'delivery.video_post': 'https://t.me/mychannel/12' })
    expect(changes([stored], { 'delivery.video_post': '' })).toEqual({ 'delivery.video_post': '' }) // no video any more
    expect(canonical(video, '')).toBe('')
    expect(problems([videoPost()], { 'delivery.video_post': 'https://example.com/video.mp4' })).toEqual({ 'delivery.video_post': { code: 'tme_post' } })
    expect(problems([videoPost()], { 'delivery.video_post': 'https://t.me/s/mychannel/12' })).toEqual({})
  })

  it('shows an example, reads left to right and has room for a link', () => {
    expect(example(video)).toBe('https://t.me/yourchannel/12')
    expect(ltr(video)).toBe(true)
    expect(maxLength(video)).toBeGreaterThanOrEqual(300)
  })

  it('has the two switches on until they are turned off, and a tutorial that is none until one is picked', () => {
    expect(canonical(bundle(), '')).toBe('true')
    expect(changes([bundle(), bundleTrial(), tutorial()], { 'delivery.bundle': 'true', 'delivery.bundle_trial': 'false', 'delivery.tutorial': '' })).toEqual({
      'delivery.bundle_trial': 'false',
    })
    expect(validate(bundle(), 'maybe')).toEqual({ code: 'option' })
    expect(validate(tutorial(), '11111111-1111-4111-8111-111111111111')).toBeNull()
    expect(validate(tutorial(), '')).toBeNull()
  })
})

describe('the texts of the delivery group', () => {
  const get = (o: unknown, path: string): unknown => path.split('.').reduce<unknown>((a, k) => (a as Record<string, unknown> | undefined)?.[k], o)
  const keys = [
    'settings.invalid.tme_post',
    ...['title', 'desc', 'note'].map((k) => `settings.group.delivery.${k}`),
    ...['bundle', 'bundle_trial', 'video_post', 'tutorial'].flatMap((k) => [`settings.field.delivery.${k}.label`, `settings.field.delivery.${k}.help`]),
    ...['none', 'unavailable', 'saved', 'load_failed'].map((k) => `settings.tutorial.${k}`),
  ]
  const names = (s: string) => [...new Set([...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))].sort()

  it('are in all five languages, with the same values to fill in', () => {
    for (const [lang, cat] of [['en', en], ['fa', fa], ['ar', ar], ['ru', ru], ['tr', tr]] as const) {
      for (const k of keys) {
        const a = get(en, k)
        const b = get(cat, k)
        expect(typeof a, `en ${k}`).toBe('string')
        expect(typeof b === 'string' && b.trim() !== '', `${lang} ${k}`).toBe(true)
        expect(names(b as string), `${lang} ${k}`).toEqual(names(a as string))
      }
    }
  })

  it('tell the owner how to get a post link, and that the bot must be an administrator of the channel', () => {
    expect(en.settings.field.delivery.video_post.help).toContain('Copy Link')
    expect(en.settings.field.delivery.video_post.help).toContain('administrator')
    expect(en.settings.invalid.tme_post).toContain('https://t.me/yourchannel/12')
    expect(fa.settings.invalid.tme_post).toContain('https://t.me/yourchannel/12')
    expect(en.settings.group.delivery.title).toBe('After a purchase')
  })
})

describe('the tutorial picker', () => {
  beforeEach(async () => {
    await setLang('en')
    // jsdom has no matchMedia; PrimeVue's Select asks it about orientation
    vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
  })
  afterEach(() => {
    document.body.innerHTML = ''
    vi.unstubAllGlobals()
    void setLang('fa')
  })

  /** mountPicker shows the picker the way the card does, against a fake core that answers GET /help. */
  async function mountPicker(initial: string, answer: () => Response | Promise<Response>, props: Record<string, unknown> = {}) {
    const fetched: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        fetched.push(`${init?.method ?? 'GET'} ${url}`)
        return answer()
      }),
    )
    const value = ref(initial)
    const emitted: string[] = []
    let touches = 0
    const Host = defineComponent({
      render: () =>
        h(TutorialPicker, {
          modelValue: value.value,
          inputId: 'tutorial',
          describedBy: 'tutorial-help',
          ...props,
          'onUpdate:modelValue': (v: string) => {
            emitted.push(v)
            value.value = v
          },
          onTouch: () => touches++,
        }),
    })
    const root = document.createElement('div')
    document.body.appendChild(root)
    const app = createApp(Host)
    app.use(i18n).use(PrimeVue)
    app.mount(root)
    await nextTick()
    return { root, value, emitted, fetched, touches: () => touches, unmount: () => app.unmount() }
  }
  const label = (root: HTMLElement) => (root.querySelector('.p-select-label')?.textContent ?? '').trim()
  const loaded = (root: HTMLElement, text: string) => vi.waitFor(() => expect(label(root)).toBe(text))
  // the list has come (or failed): until then the box does not open
  const ready = (root: HTMLElement) => vi.waitFor(() => expect(root.querySelector('.picker')?.getAttribute('aria-busy')).toBe('false'))
  const open = async (root: HTMLElement) => {
    // the list of an earlier pick takes a moment to leave
    await vi.waitFor(() => expect(document.querySelectorAll('.p-select-option')).toHaveLength(0))
    root.querySelector<HTMLElement>('.p-select')!.click()
    await vi.waitFor(() => expect(document.querySelectorAll('.p-select-option').length).toBeGreaterThan(0))
    return [...document.querySelectorAll('.p-select-option')] as HTMLElement[]
  }
  const optionTexts = (options: HTMLElement[]) => options.map((o) => (o.textContent ?? '').trim())
  const pick = (options: HTMLElement[], text: string) => {
    const o = options.find((x) => (x.textContent ?? '').trim() === text)
    expect(o, text).toBeDefined()
    o!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) // PrimeVue picks on mousedown
  }
  const all = () => json(200, helpPage([download(), connect(), hidden()]))

  it('lists the tutorials that are switched on by their titles, None first, and reads the list once', async () => {
    const { root, fetched, unmount } = await mountPicker('', all)
    await ready(root)
    expect(label(root)).toBe('None')
    expect(fetched).toEqual(['GET /api/v1/help'])
    const options = await open(root)
    expect(optionTexts(options)).toEqual(['None', 'Download the app', 'Connect']) // the one switched off is not there
    expect(root.querySelector('[role=status]')).toBeNull()
    unmount()
  })

  it('shows the saved tutorial by its title', async () => {
    const { root, unmount } = await mountPicker(download().id, all)
    await loaded(root, 'Download the app')
    expect(root.querySelector('.picker')?.classList.contains('is-gone')).toBe(false)
    unmount()
  })

  it('writes the titles in the language of the page, and in the other where there is none', async () => {
    await setLang('fa')
    const { root, unmount } = await mountPicker(connect().id, all)
    await loaded(root, 'Connect') // written in English only
    const options = await open(root)
    expect(optionTexts(options)).toEqual([fa.settings.tutorial.none, 'دانلود برنامه', 'Connect'])
    unmount()
    // a language that is neither reads the English titles
    document.body.innerHTML = ''
    await setLang('tr')
    const turkish = await mountPicker(download().id, all)
    await loaded(turkish.root, 'Download the app')
    expect(optionTexts(await open(turkish.root))[0]).toBe(tr.settings.tutorial.none)
    turkish.unmount()
  })

  it('hands up the id of the tutorial that is picked, or nothing for None, and tells the card', async () => {
    const { root, value, emitted, touches, unmount } = await mountPicker('', all)
    await ready(root)
    pick(await open(root), 'Connect')
    await vi.waitFor(() => expect(emitted).toEqual([connect().id]))
    expect(touches()).toBe(1)
    await loaded(root, 'Connect')
    pick(await open(root), 'None')
    await vi.waitFor(() => expect(emitted).toEqual([connect().id, '']))
    expect(value.value).toBe('')
    expect(touches()).toBe(2)
    await loaded(root, 'None')
    unmount()
  })

  it('shows a saved tutorial that was deleted, or switched off, as not available, in the list and as the value', async () => {
    for (const saved of ['99999999-9999-4999-8999-999999999999', hidden().id]) {
      document.body.innerHTML = ''
      const { root, emitted, unmount } = await mountPicker(saved, all)
      await loaded(root, 'Tutorial not available')
      expect(root.querySelector('.picker')?.classList.contains('is-gone')).toBe(true)
      const options = await open(root)
      expect(optionTexts(options)).toEqual(['None', 'Download the app', 'Connect', 'Tutorial not available'])
      expect(options[3].querySelector('.gone')).not.toBeNull() // it looks switched off
      expect(options[1].querySelector('.gone')).toBeNull()
      // another tutorial can be picked, and then the one that is gone is not offered any more
      pick(options, 'Download the app')
      await vi.waitFor(() => expect(emitted).toEqual([download().id]))
      await loaded(root, 'Download the app')
      expect(optionTexts(await open(root))).toEqual(['None', 'Download the app', 'Connect'])
      unmount()
    }
  })

  it('keeps the saved choice and says so when the list cannot be loaded', async () => {
    const saved = download().id
    const { root, emitted, unmount } = await mountPicker(saved, () => json(500, { error: 'internal', message: 'boom' }))
    await ready(root)
    expect(label(root)).toBe('Saved tutorial')
    expect(root.querySelector('[role=status]')?.textContent).toBe(en.settings.tutorial.load_failed)
    expect(root.querySelector('.picker')?.classList.contains('is-gone')).toBe(false) // nothing is known about it
    const options = await open(root)
    expect(optionTexts(options)).toEqual(['None', 'Saved tutorial'])
    expect(emitted).toEqual([]) // nothing was changed by the failure
    pick(options, 'None')
    await vi.waitFor(() => expect(emitted).toEqual(['']))
    unmount()
  })

  it('does not throw for a request that fails in any way, with nothing saved', async () => {
    const unhandled = vi.fn()
    process.on('unhandledRejection', unhandled)
    try {
      for (const answer of [
        () => json(403, { error: 'forbidden', message: 'no' }),
        () => json(200, {}), // an answer that is not a help page
        () => json(200, { tutorials: 'x' }),
        () => new Response('not json', { status: 200 }),
        () => Promise.reject(new TypeError('network down')),
      ]) {
        document.body.innerHTML = ''
        const { root, unmount } = await mountPicker('', answer)
        await vi.waitFor(() => expect(root.querySelector('[role=status]')).not.toBeNull())
        expect(label(root)).toBe('None')
        expect(optionTexts(await open(root))).toEqual(['None'])
        unmount()
      }
      await new Promise((resolve) => setTimeout(resolve, 20))
      expect(unhandled).not.toHaveBeenCalled()
    } finally {
      process.off('unhandledRejection', unhandled)
    }
  })

  it('shows the saved choice while the list is on its way, without calling it gone', async () => {
    let release: (r: Response) => void = () => {}
    const waiting = new Promise<Response>((resolve) => (release = resolve))
    const { root, unmount } = await mountPicker(download().id, () => waiting)
    expect(label(root)).toBe('Saved tutorial')
    expect(root.querySelector('.picker')?.getAttribute('aria-busy')).toBe('true')
    expect(root.querySelector('.picker')?.classList.contains('is-gone')).toBe(false)
    expect(root.querySelector('[role=status]')).toBeNull()
    release(all())
    await loaded(root, 'Download the app')
    unmount()
  })

  it('is named by the field label and described by its help, and is read only for a role that may not change it', async () => {
    const { root, unmount } = await mountPicker('', all)
    await loaded(root, 'None')
    const combo = document.getElementById('tutorial') as HTMLElement // the card focuses this one on Save
    expect(combo.classList.contains('p-select-label')).toBe(true)
    expect(combo.getAttribute('aria-describedby')).toBe('tutorial-help')
    expect(combo.getAttribute('aria-labelledby')).toBe('tutorial-label') // SettingField's label has this id
    unmount()
    document.body.innerHTML = ''
    const locked = await mountPicker(download().id, all, { locked: true })
    await loaded(locked.root, 'Download the app')
    expect(locked.root.querySelector('.p-select')?.classList.contains('p-disabled')).toBe(true)
    locked.root.querySelector<HTMLElement>('.p-select')!.click()
    await nextTick()
    expect(document.querySelectorAll('.p-select-option')).toHaveLength(0)
    locked.unmount()
  })

  it('marks the box invalid when the card says so', async () => {
    const { root, unmount } = await mountPicker('', all, { invalid: true })
    await loaded(root, 'None')
    expect(root.querySelector('.p-select')?.classList.contains('p-invalid')).toBe(true)
    unmount()
  })
})

describe('the field of the card', () => {
  beforeEach(async () => {
    await setLang('en')
    vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    vi.stubGlobal('fetch', vi.fn(async () => json(200, helpPage([download()]))))
  })
  afterEach(() => {
    document.body.innerHTML = ''
    vi.unstubAllGlobals()
    void setLang('fa')
  })

  it('draws the picker for the tutorial, a switch for each of the two switches, and a link box for the video', async () => {
    const root = document.createElement('div')
    document.body.appendChild(root)
    const mount = (it: SettingItem, value: string, placeholder = '') => {
      const el = document.createElement('div')
      root.appendChild(el)
      const app = createApp({ render: () => h(SettingField, { item: it, modelValue: value, placeholder }) })
      app.use(i18n).use(PrimeVue)
      app.mount(el)
      return el
    }
    const picker = mount(tutorial(), download().id)
    await vi.waitFor(() => expect(picker.querySelector('.p-select-label')?.textContent?.trim()).toBe('Download the app'))
    expect(picker.querySelector('input.p-inputtext')).toBeNull()
    expect(picker.querySelector('label.label')?.textContent?.trim()).toBe('Tutorial')
    expect(picker.querySelector('label.label')?.getAttribute('for')).toBe('setting-delivery-tutorial')
    expect(document.getElementById('setting-delivery-tutorial')).not.toBeNull()
    expect(picker.querySelector('.help')?.textContent).toContain('Branding & texts')

    const switches = [mount(bundle(), 'true'), mount(bundleTrial(), 'false')]
    expect(switches.map((s) => s.querySelector('.p-toggleswitch') !== null)).toEqual([true, true])
    expect(switches.map((s) => (s.querySelector('input[type=checkbox]') as HTMLInputElement).checked)).toEqual([true, false])
    expect(switches[0].querySelector('.label')?.textContent?.trim()).toBe('Send a direct config, a video and a tutorial')

    const video = mount(videoPost(), '', example(videoPost()))
    const input = video.querySelector('input') as HTMLInputElement
    expect(input.id).toBe('setting-delivery-video-post')
    expect(input.dir).toBe('ltr')
    expect(input.placeholder).toBe('https://t.me/yourchannel/12')
    expect(video.querySelector('.picker')).toBeNull()
    expect(video.querySelector('.help')?.textContent).toContain('Copy Link')
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

  /** boot starts the app on the Settings page against a fake core: it keeps the settings it is given and answers GET /help. */
  async function boot(lang: 'en' | 'fa', state: { items: SettingItem[]; help: () => Response }, calls: string[] = []) {
    vi.resetModules()
    localStorage.setItem('bobres.lang', lang)
    window.history.replaceState(null, '', '/admin/settings')
    document.body.innerHTML = '<div id="app"></div>'
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        calls.push(`${method} ${url} ${typeof init?.body === 'string' ? init.body : ''}`.trim())
        const reply = () => json(200, { items: state.items, groups: ['delivery'] })
        if (url === '/api/v1/me') return json(200, me)
        if (url === '/api/v1/settings' && method === 'GET') return reply()
        if (url === '/api/v1/settings' && method === 'PUT') {
          const values = JSON.parse(String(init?.body)).values as Record<string, string>
          state.items = state.items.map((i) => (i.key in values ? { ...i, value: values[i.key] } : i))
          return reply()
        }
        if (url === '/api/v1/help') return state.help()
        return json(404, { error: 'not_found', message: 'not in this test' })
      }),
    )
    await import('../main')
    const { router } = await import('../router')
    await router.isReady()
    await vi.waitFor(() => expect(document.querySelector('.settings-card')).not.toBeNull())
    return document.querySelector('.settings-card') as HTMLElement
  }
  const text = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
  const type = (input: HTMLInputElement, v: string) => {
    input.value = v
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }
  const input = (key: string) => document.getElementById('setting-' + key.replace(/[^A-Za-z0-9]/g, '-')) as HTMLInputElement
  const saveButton = (card: HTMLElement) => card.querySelector('button[type=submit]') as HTMLButtonElement
  const sent = (calls: string[]) => calls.filter((c) => c.startsWith('PUT /api/v1/settings')).map((c) => JSON.parse(c.slice(c.indexOf('{'))).values as Record<string, string>)
  const helpAll = () => json(200, helpPage([download(), connect(), hidden()]))

  it('has an After a purchase card: both switches on, a video link checked as a post link, a tutorial picked by its title', async () => {
    const state = { items: deliveryItems(), help: helpAll }
    const calls: string[] = []
    const card = await boot('en', state, calls)
    expect(text(card.querySelector('h2'))).toBe('After a purchase')
    expect(text(card)).toContain(en.settings.group.delivery.desc)
    expect(text(card)).toContain('Renewals and services bought by resellers get none of it.')
    for (const key of ['bundle', 'bundle_trial', 'video_post', 'tutorial']) {
      const field = (document.getElementById('setting-delivery-' + key.replace('_', '-')) as HTMLElement).closest('.field') as HTMLElement
      expect(text(field.querySelector('.label')), key).toBe(en.settings.field.delivery[key as 'bundle'].label)
      expect(text(field.querySelector('.help')), key).toBe(en.settings.field.delivery[key as 'bundle'].help)
    }
    expect(input('delivery.bundle').checked).toBe(true)
    expect(input('delivery.bundle_trial').checked).toBe(true)
    expect(input('delivery.video_post').value).toBe('')
    expect(input('delivery.video_post').placeholder).toBe('https://t.me/yourchannel/12')
    expect(saveButton(card).disabled).toBe(true)
    await vi.waitFor(() => expect(calls).toContain('GET /api/v1/help'))
    expect(card.querySelector('.p-select-label')?.textContent?.trim()).toBe('None')

    // the free trial off, a link that is not a post's refused where it is typed, nothing sent
    input('delivery.bundle_trial').click()
    type(input('delivery.video_post'), 'https://example.com/video.mp4')
    input('delivery.video_post').dispatchEvent(new Event('blur'))
    await vi.waitFor(() => expect(text(card)).toContain(en.settings.invalid.tme_post))
    await vi.waitFor(() => expect(saveButton(card).disabled).toBe(false))
    saveButton(card).click()
    await nextTick()
    expect(sent(calls)).toEqual([])
    // the post as Telegram's web page gives it; the tutorial from the list
    type(input('delivery.video_post'), ' https://t.me/s/mychannel/12?single ')
    await vi.waitFor(() => expect(text(card)).not.toContain(en.settings.invalid.tme_post))
    card.querySelector<HTMLElement>('.p-select')!.click()
    const option = await vi.waitFor(() => {
      const o = [...document.querySelectorAll('.p-select-option')].find((x) => text(x) === 'Connect') as HTMLElement | undefined
      expect(o).toBeDefined()
      return o!
    })
    option.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await vi.waitFor(() => expect(card.querySelector('.p-select-label')?.textContent?.trim()).toBe('Connect'))
    saveButton(card).click()
    await vi.waitFor(() => expect(sent(calls)).toHaveLength(1))
    expect(sent(calls)[0]).toEqual({
      'delivery.bundle_trial': 'false',
      'delivery.video_post': 'https://t.me/mychannel/12',
      'delivery.tutorial': connect().id,
    })
    await vi.waitFor(() => expect(saveButton(card).disabled).toBe(true))
    expect(input('delivery.bundle_trial').checked).toBe(false)
    expect(card.querySelector('.p-select-label')?.textContent?.trim()).toBe('Connect')
  })

  it('says a saved tutorial is not available when it is gone, and sends other changes without touching it', async () => {
    const gone = '99999999-9999-4999-8999-999999999999'
    const state = { items: [bundle(), bundleTrial(), videoPost({ value: 'https://t.me/mychannel/12' }), tutorial({ value: gone })], help: helpAll }
    const calls: string[] = []
    const card = await boot('en', state, calls)
    await vi.waitFor(() => expect(card.querySelector('.p-select-label')?.textContent?.trim()).toBe('Tutorial not available'))
    expect(input('delivery.video_post').value).toBe('https://t.me/mychannel/12')
    expect(saveButton(card).disabled).toBe(true)
    input('delivery.bundle').click()
    await vi.waitFor(() => expect(saveButton(card).disabled).toBe(false))
    saveButton(card).click()
    await vi.waitFor(() => expect(sent(calls)).toHaveLength(1))
    expect(sent(calls)[0]).toEqual({ 'delivery.bundle': 'false' })
    await vi.waitFor(() => expect(saveButton(card).disabled).toBe(true))
    expect(card.querySelector('.p-select-label')?.textContent?.trim()).toBe('Tutorial not available')
  })

  it('keeps the page whole when the tutorials cannot be read', async () => {
    const saved = download().id
    const state = { items: [bundle(), bundleTrial(), videoPost(), tutorial({ value: saved })], help: () => json(403, { error: 'forbidden', message: 'no' }) }
    const calls: string[] = []
    const card = await boot('en', state, calls)
    await vi.waitFor(() => expect(text(card)).toContain(en.settings.tutorial.load_failed))
    expect(card.querySelector('.p-select-label')?.textContent?.trim()).toBe('Saved tutorial')
    // the rest of the card still saves
    input('delivery.bundle').click()
    await vi.waitFor(() => expect(saveButton(card).disabled).toBe(false))
    saveButton(card).click()
    await vi.waitFor(() => expect(sent(calls)).toEqual([{ 'delivery.bundle': 'false' }]))
  })

  it('reads in Persian', async () => {
    const state = { items: [bundle(), bundleTrial(), videoPost(), tutorial({ value: download().id })], help: helpAll }
    const card = await boot('fa', state)
    expect(text(card.querySelector('h2'))).toBe(fa.settings.group.delivery.title)
    expect(text(card)).toContain(fa.settings.group.delivery.note)
    expect(text(card)).toContain(fa.settings.field.delivery.video_post.help)
    await vi.waitFor(() => expect(card.querySelector('.p-select-label')?.textContent?.trim()).toBe('دانلود برنامه'))
    // a link that is not a post's is refused in Persian
    type(input('delivery.video_post'), 'https://example.com/x')
    input('delivery.video_post').dispatchEvent(new Event('blur'))
    await vi.waitFor(() => expect(text(card)).toContain(fa.settings.invalid.tme_post))
  })
})
