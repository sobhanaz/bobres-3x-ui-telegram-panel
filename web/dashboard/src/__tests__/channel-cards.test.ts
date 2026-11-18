import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, ref } from 'vue'
import PrimeVue from 'primevue/config'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import ar from '../locales/ar.json'
import ru from '../locales/ru.json'
import tr from '../locales/tr.json'
import { i18n, setLang } from '../i18n'
import ChannelCardsEditor from '../components/settings/ChannelCardsEditor.vue'
import SettingField from '../components/settings/SettingField.vue'
import {
  canonical,
  changed,
  changes,
  formatChannelCards,
  fullWidthGroups,
  MAX_CARD_LABEL,
  MAX_CHANNEL_CARDS,
  normalize,
  normalizeChannelCards,
  parseChannelCards,
  parseTmePost,
  problems,
  tmeChannelLink,
  tmePostLink,
  validate,
  wideGroups,
  type ChannelCard,
} from '../settings'
import type { SettingItem } from '../types'

// The channel buttons of the bot's menu (setting bot.channel_cards): the t.me links and the list as core reads them (the cases are the ones core's own
// tests hold), the row editor, and the Channel buttons card of the Settings page, in English and Persian.

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

function item(key: string, kind: SettingItem['kind'], extra: Partial<SettingItem> = {}): SettingItem {
  return { key, group: key.split('.')[0], kind, value: '', default: '', options: [], min: null, max: null, editable: true, ...extra }
}
const cardsItem = (extra: Partial<SettingItem> = {}) => item('bot.channel_cards', 'list', { group: 'channels', ...extra })

const reviews = '[{"label":{"fa":"کانال نظرات","en":"Reviews"},"post":"https://t.me/heisen_reviews/2"}]'
// the same list as it may be typed: the other order, spaces, the web preview's link with a query, telegram.me, a channel the post already says
const messy = ` [ {"post":" https://t.me/s/heisen_reviews/2?single ","channel":"https://telegram.me/heisen_reviews","label":{"en":"  Reviews  ","fa":"کانال   نظرات"}} ] `
const privatePost = '[{"label":{"tr":"Yorumlar","fa":""},"channel":"https://t.me/+AbCdEfGh12","post":"https://t.me/c/1234567890/45"}]'
const privateNormal = '[{"label":{"tr":"Yorumlar"},"channel":"https://t.me/+AbCdEfGh12","post":"https://t.me/c/1234567890/45"}]'
/** channels is n buttons, each with its own post. */
const channels = (n: number) => JSON.stringify(Array.from({ length: n }, (_, i) => ({ label: { en: 'Button ' + (i + 1) }, post: `https://t.me/channel${'a'.repeat(i)}/1` })))
const zwnj = String.fromCharCode(0x200c)

describe('the links of Telegram posts and channels', () => {
  it('read a post link as core does, in every form Telegram gives', () => {
    const ok: [string, string][] = [
      ['https://t.me/heisen_reviews/2', 'https://t.me/heisen_reviews/2'],
      ['  https://t.me/heisen_reviews/2/  ', 'https://t.me/heisen_reviews/2'],
      ['https://t.me/heisen_reviews/2?single', 'https://t.me/heisen_reviews/2'],
      ['https://t.me/heisen_reviews/2#top', 'https://t.me/heisen_reviews/2'],
      ['https://t.me/s/heisen_reviews/17', 'https://t.me/heisen_reviews/17'],
      ['https://telegram.me/Heisen_Reviews/9', 'https://t.me/Heisen_Reviews/9'],
      ['https://T.ME/heisen_reviews/2', 'https://t.me/heisen_reviews/2'],
      ['HTTPS://t.me/heisen_reviews/2', 'https://t.me/heisen_reviews/2'],
      ['https://t.me/c/1234567890/45', 'https://t.me/c/1234567890/45'],
      ['https://t.me/abcde/2147483647', 'https://t.me/abcde/2147483647'],
      ['https://t.me/heisen_reviews/007', 'https://t.me/heisen_reviews/7'], // the number is written without leading zeros
      ['https://t.me/' + 'a'.repeat(32) + '/1', 'https://t.me/' + 'a'.repeat(32) + '/1'],
      // a name that only begins like one of Telegram's own paths is a name
      ['https://t.me/shared_files/5', 'https://t.me/shared_files/5'],
      ['https://t.me/boost_news/5', 'https://t.me/boost_news/5'],
      // white space is what core calls it: the NEL is trimmed; a query is not read, so its broken % escape does not matter, a fragment's does
      ['https://t.me/heisen_reviews/2\u0085', 'https://t.me/heisen_reviews/2'],
      ['https://t.me/heisen_reviews/2?x=%zz', 'https://t.me/heisen_reviews/2'],
      ['https://t.me/heisen_reviews/2#%20', 'https://t.me/heisen_reviews/2'],
    ]
    for (const [raw, want] of ok) expect(tmePostLink(raw), raw).toBe(want)
    expect(parseTmePost('https://t.me/heisen_reviews/2')).toEqual({ link: 'https://t.me/heisen_reviews/2', channel: 'https://t.me/heisen_reviews' })
    // a private channel's invite link is the owner's to give: the post does not say it
    expect(parseTmePost('https://t.me/c/1234567890/45')).toEqual({ link: 'https://t.me/c/1234567890/45', channel: '' })
  })

  it('refuse what is not the link of a post', () => {
    const bad = [
      '', '   ', 'heisen_reviews/2', 'http://t.me/heisen_reviews/2', 'https://example.com/heisen_reviews/2', 'https://t.me.evil.com/heisen_reviews/2',
      'https://t.me:8443/heisen_reviews/2', 'https://user@t.me/heisen_reviews/2', 'https://t.me/', 'https://t.me/heisen_reviews', 'https://t.me/heisen_reviews/0',
      'https://t.me/heisen_reviews/-3', 'https://t.me/heisen_reviews/abc', 'https://t.me/heisen_reviews/2147483648', 'https://t.me/heisen_reviews/12345678901',
      'https://t.me/abcd/2', 'https://t.me/1abcde/2', 'https://t.me/heisen-reviews/2', 'https://t.me/' + 'a'.repeat(33) + '/1', 'https://t.me/heisen_reviews/2/3',
      'https://t.me/c/123/4/5', 'https://t.me/c/abc/4', 'https://t.me/c/12345678901234/4', 'https://t.me/c/0/4x', 'https://t.me/x/heisen_reviews/2',
      'https://t.me/s/heisen_reviews', 'https://t.me/heisen_reviews/2 x', 'https://t.me/heisen reviews/2', 'https://t.me/+AbCdEfGh12/2',
      'tg://resolve?domain=heisen&post=2', 'https://t.me\\heisen_reviews\\2', 'https://t.me/heisen_reviews/\u00a02', 'https://t.me./heisen_reviews/2',
      'https://www.t.me/heisen_reviews/2',
      // Telegram's own paths are not the names of channels, in any case
      'https://t.me/joinchat/12', 'https://t.me/share/12', 'https://t.me/Boost/12', 'https://t.me/s/addstickers/3', 'https://t.me/addemoji/3',
      // the BOM is not white space for core, so it is a character of the path; a fragment with a broken % escape is refused
      'https://t.me/heisen_reviews/2\ufeff', 'https://t.me/heisen_reviews/2#%zz', 'https://t.me/heisen_reviews/2#a%',
    ]
    for (const raw of bad) expect(tmePostLink(raw), JSON.stringify(raw)).toBeNull()
  })

  it('read a channel link: a public one or an invite link', () => {
    const ok: [string, string][] = [
      ['https://t.me/heisen_reviews', 'https://t.me/heisen_reviews'],
      [' https://t.me/heisen_reviews/ ', 'https://t.me/heisen_reviews'],
      ['https://telegram.me/Heisen_Reviews', 'https://t.me/Heisen_Reviews'],
      ['https://t.me/heisen_reviews?start=abc', 'https://t.me/heisen_reviews'],
      ['https://t.me/+AbCdEfGh12_-', 'https://t.me/+AbCdEfGh12_-'],
      ['https://t.me/joinchat/AbCdEfGh12', 'https://t.me/joinchat/AbCdEfGh12'],
      ['https://t.me/shared_files', 'https://t.me/shared_files'],
    ]
    for (const [raw, want] of ok) expect(tmeChannelLink(raw), raw).toBe(want)
    const bad = [
      '', 'https://t.me/', 'https://t.me/heisen_reviews/2', 'https://t.me/abcd', 'http://t.me/heisen_reviews', 'https://example.com/heisen_reviews',
      'https://t.me/+abc', 'https://t.me/+abc$defghij', 'https://t.me/joinchat/ab', 'https://t.me/c/1234567890/4', 'https://t.me/s/heisen_reviews', '@heisen_reviews',
      // Telegram's own paths: the invite link's, the stickers' ...
      'https://t.me/joinchat', 'https://t.me/addstickers', 'https://t.me/Share', 'https://t.me/SetLanguage',
    ]
    for (const raw of bad) expect(tmeChannelLink(raw), raw).toBeNull()
  })
})

describe('the list of channel buttons', () => {
  it('is read as core reads it: the order kept, the texts gathered, the links made canonical', () => {
    expect(parseChannelCards('')).toEqual({ cards: [] })
    expect(parseChannelCards('  ')).toEqual({ cards: [] })
    expect(parseChannelCards('null')).toEqual({ cards: [] })
    expect(parseChannelCards('[]')).toEqual({ cards: [] })
    expect(parseChannelCards(reviews)).toEqual({
      cards: [{ label: { fa: 'کانال نظرات', en: 'Reviews' }, channel: '', post: 'https://t.me/heisen_reviews/2' }],
    })
    expect(parseChannelCards(messy)).toEqual({
      cards: [{ label: { fa: 'کانال نظرات', en: 'Reviews' }, channel: 'https://t.me/heisen_reviews', post: 'https://t.me/heisen_reviews/2' }],
    })
    // a private channel with its invite link, a text in another language, an empty one left out
    expect(parseChannelCards(privatePost)).toEqual({
      cards: [{ label: { tr: 'Yorumlar' }, channel: 'https://t.me/+AbCdEfGh12', post: 'https://t.me/c/1234567890/45' }],
    })
    // the order of the buttons is the order of the list, not an order by anything
    const two = '[{"label":{"fa":"ب"},"post":"https://t.me/bbbbbb/1"},{"label":{"fa":"آ"},"post":"https://t.me/aaaaaa/1"}]'
    const p = parseChannelCards(two)
    expect('cards' in p && p.cards.map((c) => c.label.fa)).toEqual(['ب', 'آ'])
  })

  it('gathers a text the way core does, and counts its characters, not its bytes', () => {
    const one = (label: Record<string, string>) => JSON.stringify([{ label, post: 'https://t.me/heisen_reviews/2' }])
    const text = (label: Record<string, string>) => {
      const p = parseChannelCards(one(label))
      return 'cards' in p ? p.cards[0].label : p
    }
    expect(text({ en: '  A \u00a0 B\u3000C  ' })).toEqual({ en: 'A B C' })
    expect(text({ fa: 'کانال' + zwnj + 'ها' })).toEqual({ fa: 'کانال' + zwnj + 'ها' }) // the half-space is a character of the text
    expect(text({ en: 'x'.repeat(MAX_CARD_LABEL) })).toEqual({ en: 'x'.repeat(MAX_CARD_LABEL) })
    expect(text({ en: 'x'.repeat(MAX_CARD_LABEL + 1) })).toEqual({ code: 'cards_label' })
    // a face is one character though it takes two units of the string
    expect(text({ en: '😀'.repeat(MAX_CARD_LABEL) })).toEqual({ en: '😀'.repeat(MAX_CARD_LABEL) })
    expect(text({ en: '😀'.repeat(MAX_CARD_LABEL + 1) })).toEqual({ code: 'cards_label' })
    // the spaces that are gathered do not count; the text may be longer before it is
    expect(text({ en: ' '.repeat(10) + 'x'.repeat(MAX_CARD_LABEL) + ' '.repeat(10) })).toEqual({ en: 'x'.repeat(MAX_CARD_LABEL) })
    expect(text({ en: 'a\tb' })).toEqual({ code: 'cards_label' })
    expect(text({ en: 'a\nb' })).toEqual({ code: 'cards_label' })
    expect(text({ en: 'a\u0085b' })).toEqual({ code: 'cards_label' })
  })

  it('is refused for what is wrong, by a code that names it', () => {
    const post = 'https://t.me/heisen_reviews/2'
    const row = (over: Record<string, unknown>) => JSON.stringify([{ label: { fa: 'x' }, post, ...over }])
    const refused: [string, string, string][] = [
      // the shape of the list
      ['not json', '[{', 'cards'],
      ['an object', '{"label":{"fa":"x"},"post":"' + post + '"}', 'cards'],
      ['a string', '"x"', 'cards'],
      ['a number', '7', 'cards'],
      ['trailing text', reviews + ' x', 'cards'],
      ['two lists', reviews + reviews, 'cards'],
      ['a row that is not an object', '[1]', 'cards'],
      ['a row that is a list', '[[]]', 'cards'],
      ['a row that is a string', '["x"]', 'cards'],
      ['an unknown field', row({ extra: 1 }), 'cards'],
      ['a label that is not an object', row({ label: 'x' }), 'cards'],
      ['a label that is a list', row({ label: ['x'] }), 'cards'],
      ['a text that is not text', row({ label: { fa: 1 } }), 'cards'],
      ['a post that is not text', row({ post: 7 }), 'cards'],
      ['a channel that is not text', row({ channel: 7 }), 'cards'],
      ['an unknown language', row({ label: { xx: 'x' } }), 'cards'],
      // the texts
      ['no label', JSON.stringify([{ post }]), 'cards_label'],
      ['an empty row (null)', '[null]', 'cards_label'],
      ['an empty label', row({ label: {} }), 'cards_label'],
      ['a blank label', row({ label: { fa: '  ' } }), 'cards_label'],
      ['a label that is null', row({ label: null }), 'cards_label'],
      ['a label too long', row({ label: { fa: 'ا'.repeat(MAX_CARD_LABEL + 1) } }), 'cards_label'],
      ['a label in two lines', row({ label: { fa: 'a\nb' } }), 'cards_label'],
      // the post
      ['no post', JSON.stringify([{ label: { fa: 'x' } }]), 'cards_post'],
      ['a post that is null', row({ post: null }), 'cards_post'],
      ['a post that is a channel', row({ post: 'https://t.me/heisen_reviews' }), 'cards_post'],
      ['a post over http', row({ post: 'http://t.me/heisen_reviews/2' }), 'cards_post'],
      ['a post of a path Telegram keeps for itself', row({ post: 'https://t.me/share/2' }), 'cards_post'],
      // the channel
      ['a private channel without its invite link', row({ post: 'https://t.me/c/1234567890/45' }), 'cards_channel'],
      ['a private channel with a blank channel', row({ post: 'https://t.me/c/1234567890/45', channel: '   ' }), 'cards_channel'],
      ['a channel that is another site', row({ channel: 'https://example.com/x' }), 'cards_channel'],
      ['a channel that is a post', row({ channel: post }), 'cards_channel'],
      ['a channel that is a path Telegram keeps for itself', row({ channel: 'https://t.me/addstickers' }), 'cards_channel'],
      ['a channel that is only a BOM (not white space for core)', row({ channel: '\ufeff' }), 'cards_channel'],
      ['an invite link that is too short', row({ channel: 'https://t.me/+abc' }), 'cards_channel'],
      // the same post twice, however it is written
      ['the same post twice', '[{"label":{"fa":"x"},"post":"' + post + '"},{"label":{"fa":"y"},"post":"https://t.me/s/heisen_reviews/2?single"}]', 'cards_dup'],
    ]
    for (const [name, raw, code] of refused) expect(parseChannelCards(raw), name).toEqual({ code })
  })

  it('checks the row that is first wrong, and a text before a post before a channel', () => {
    const rows = (...r: Record<string, unknown>[]) => JSON.stringify(r)
    const good = { label: { fa: 'x' }, post: 'https://t.me/heisen_reviews/2' }
    expect(parseChannelCards(rows({ post: 'nope' }))).toEqual({ code: 'cards_label' }) // no text, and a post that is not one
    expect(parseChannelCards(rows({ label: { fa: 'x' }, post: 'nope', channel: 'nope' }))).toEqual({ code: 'cards_post' })
    expect(parseChannelCards(rows({ label: { fa: 'x' }, post: 'https://t.me/c/1234567890/45', channel: 'nope' }))).toEqual({ code: 'cards_channel' })
    // row by row: a post that is not one in the first row comes before a text that is missing in the second
    expect(parseChannelCards(rows({ label: { fa: 'x' }, post: 'nope' }, { post: 'https://t.me/other_chan/1' }))).toEqual({ code: 'cards_post' })
    expect(parseChannelCards(rows(good, { post: 'https://t.me/other_chan/1' }))).toEqual({ code: 'cards_label' })
  })

  it('holds at most six buttons, and says "many" only for a list that is otherwise readable', () => {
    expect(MAX_CHANNEL_CARDS).toBe(6)
    const six = parseChannelCards(channels(6))
    expect('cards' in six && six.cards).toHaveLength(6)
    expect(parseChannelCards(channels(7))).toEqual({ code: 'cards_many' })
    // the shape is read first, as core does: a row that is not a row is what is wrong
    const seven = JSON.parse(channels(7)) as Record<string, unknown>[]
    seven[3] = { ...seven[3], extra: true }
    expect(parseChannelCards(JSON.stringify(seven))).toEqual({ code: 'cards' })
    // the same post is not a "dup" for a list that is too long: the count comes before the rows are read
    const dup = JSON.parse(channels(7)) as Record<string, unknown>[]
    dup[6] = dup[0]
    expect(parseChannelCards(JSON.stringify(dup))).toEqual({ code: 'cards_many' })
  })

  it('is written the way core stores it: compact, label then channel then post, languages in order, a channel the post says left out', () => {
    expect(formatChannelCards([])).toBe('')
    const cards: ChannelCard[] = [
      { label: { tr: 'Yorumlar', en: 'Reviews', fa: 'نظرات', ar: 'آراء', ru: 'Отзывы' }, channel: '', post: 'https://t.me/heisen_reviews/2' },
      // the post says its channel: it is not stored
      { label: { en: 'News' }, channel: 'https://t.me/heisen_news', post: 'https://t.me/heisen_news/9' },
      // a channel other than the post's is kept
      { label: { en: 'Club' }, channel: 'https://t.me/+AbCdEfGh12', post: 'https://t.me/heisen_club/1' },
      { label: { en: 'Secret', fa: '' }, channel: 'https://t.me/joinchat/AbCdEfGh12', post: 'https://t.me/c/123/4' },
    ]
    expect(formatChannelCards(cards)).toBe(
      '[{"label":{"fa":"نظرات","en":"Reviews","ar":"آراء","ru":"Отзывы","tr":"Yorumlar"},"post":"https://t.me/heisen_reviews/2"},' +
        '{"label":{"en":"News"},"post":"https://t.me/heisen_news/9"},' +
        '{"label":{"en":"Club"},"channel":"https://t.me/+AbCdEfGh12","post":"https://t.me/heisen_club/1"},' +
        '{"label":{"en":"Secret"},"channel":"https://t.me/joinchat/AbCdEfGh12","post":"https://t.me/c/123/4"}]',
    )
    // read back, the same
    const back = parseChannelCards(formatChannelCards(cards))
    expect('cards' in back && back.cards.map((c) => c.post)).toEqual(cards.map((c) => c.post))
    expect('cards' in back && back.cards[1].channel).toBe('') // what the post says is not stored, so it is not read back as given
  })

  it('is made canonical the way core does, and stays as it is when it is not valid', () => {
    for (const [raw, want] of [
      ['', ''],
      ['  ', ''],
      ['null', ''],
      ['[]', ''],
      [messy, reviews],
      [privatePost, privateNormal],
      ['[{"label":{"en":"A & B <3"},"post":"https://t.me/heisen_reviews/2"}]', '[{"label":{"en":"A & B <3"},"post":"https://t.me/heisen_reviews/2"}]'],
      ['[{"label":{"fa":"x"},"post":"https://t.me/heisen_reviews/02"}]', '[{"label":{"fa":"x"},"post":"https://t.me/heisen_reviews/2"}]'],
    ]) {
      expect(normalizeChannelCards(raw), raw).toBe(want)
      expect(normalizeChannelCards(normalizeChannelCards(raw)), 'again: ' + raw).toBe(want) // stable
    }
    expect(normalizeChannelCards(' not json ')).toBe('not json')
    expect(normalizeChannelCards(' [{"label":{},"post":""}] ')).toBe('[{"label":{},"post":""}]') // left as typed, for validate to refuse
  })
})

describe('the setting bot.channel_cards on a card', () => {
  const cards = cardsItem()

  it('is made canonical and checked as the card sends it', () => {
    expect(normalize(cards, messy)).toBe(reviews)
    expect(normalize(cards, '[]')).toBe('')
    expect(validate(cards, reviews)).toBeNull()
    expect(validate(cards, '')).toBeNull() // empty is "back to the default": no buttons
    expect(validate(cards, '[{"label":{},"post":""}]')).toEqual({ code: 'cards_label' })
    expect(validate(cards, '[{"label":{"fa":"x"},"post":""}]')).toEqual({ code: 'cards_post' })
    expect(validate(cards, '[{"label":{"fa":"x"},"post":"https://t.me/c/1234567890/45"}]')).toEqual({ code: 'cards_channel' })
    expect(validate(cards, channels(7))).toEqual({ code: 'cards_many' })
    expect(validate(cards, '{}')).toEqual({ code: 'cards' })
    expect(canonical(cards, '[]')).toBe('')
  })

  it('leaves the lists of the wheel and of the targets to their own rules', () => {
    const prizes = item('wheel.prizes', 'list')
    const tiers = item('targets.tiers', 'list')
    expect(validate(prizes, '[{"amount":1000,"weight":3}]')).toBeNull()
    expect(validate(prizes, reviews)).toEqual({ code: 'prizes' })
    expect(validate(tiers, '[{"threshold":3,"bonus":10000}]')).toBeNull()
    expect(validate(tiers, reviews)).toEqual({ code: 'tiers' })
    expect(validate(cards, '[{"threshold":3,"bonus":10000}]')).toEqual({ code: 'cards' })
    expect(normalize(prizes, '[{"weight":3,"amount":1000}]')).toBe('[{"amount":1000,"weight":3}]')
    expect(normalize(tiers, '[{"bonus":10000,"threshold":3}]')).toBe('[{"threshold":3,"bonus":10000}]')
  })

  it('sends only a list that changed, as it will be stored, and flags the one that cannot be', () => {
    const stored = cardsItem({ value: reviews })
    // typed another way, it is the list the bot already has: nothing to save
    expect(changed(stored, messy)).toBe(false)
    expect(changes([stored], { 'bot.channel_cards': messy })).toEqual({})
    expect(changes([cardsItem()], { 'bot.channel_cards': messy })).toEqual({ 'bot.channel_cards': reviews })
    expect(changes([stored], { 'bot.channel_cards': '[]' })).toEqual({ 'bot.channel_cards': '' })
    expect(problems([cardsItem()], { 'bot.channel_cards': '[{"label":{"fa":"x"},"post":"nope"}]' })).toEqual({ 'bot.channel_cards': { code: 'cards_post' } })
    expect(problems([cardsItem()], { 'bot.channel_cards': messy })).toEqual({})
  })

  it('gets a card of the full width of the page', () => {
    expect(fullWidthGroups).toContain('channels')
    // a group that is always wide ends the run before it, so the card before it is not left beside an empty column
    expect(wideGroups([{ group: 'channels', count: 1 }])).toEqual(['channels'])
    expect(wideGroups([{ group: 'general', count: 1 }, { group: 'channels', count: 1 }, { group: 'limits', count: 2 }])).toEqual(['general', 'channels', 'limits'])
    expect(wideGroups([{ group: 'general', count: 1 }, { group: 'maintenance', count: 1 }, { group: 'channels', count: 1 }, { group: 'limits', count: 1 }])).toEqual([
      'channels',
      'limits',
    ])
    // the groups that were there before decide as they did
    expect(wideGroups([{ group: 'payments', count: 7 }, { group: 'general', count: 1 }])).toEqual(['payments', 'general'])
  })
})

describe('the texts of the channel buttons', () => {
  const get = (o: unknown, path: string): unknown => path.split('.').reduce<unknown>((a, k) => (a as Record<string, unknown> | undefined)?.[k], o)
  const keys = [
    ...['cards', 'cards_many', 'cards_label', 'cards_post', 'cards_channel', 'cards_dup'].map((c) => `settings.invalid.${c}`),
    ...['title', 'desc', 'note'].map((k) => `settings.group.channels.${k}`),
    'settings.field.bot.channel_cards.label', 'settings.field.bot.channel_cards.help',
    ...['empty', 'row', 'label_fa', 'label_en', 'channel', 'post', 'post_hint', 'move_up', 'move_down', 'remove', 'add', 'max', 'order'].map((k) => `settings.channel_cards.${k}`),
  ]
  const names = (s: string) => [...new Set([...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))].sort() // plural forms repeat a name

  it('are in all five languages, with the same values to fill in', () => {
    for (const [lang, cat] of [['fa', fa], ['ar', ar], ['ru', ru], ['tr', tr]] as const) {
      for (const k of keys) {
        const a = get(en, k)
        const b = get(cat, k)
        expect(typeof a, `en ${k}`).toBe('string')
        expect(typeof b === 'string' && b.trim() !== '', `${lang} ${k}`).toBe(true)
        expect(names(b as string), `${lang} ${k}`).toEqual(names(a as string))
      }
    }
  })

  it('say what is wrong in plain words, with the example of a link', () => {
    expect(en.settings.invalid.cards_post).toContain('https://t.me/yourchannel/2')
    expect(en.settings.invalid.cards_label).toContain('40')
    expect(en.settings.invalid.cards_many).toContain('6')
    // the bot has to be able to read the posts it copies
    expect(en.settings.group.channels.note).toContain('administrator')
    expect(fa.settings.invalid.cards_post).toContain('https://t.me/yourchannel/2')
    // the half-space is where Persian needs it
    expect(fa.settings.group.channels.title).toBe('دکمه' + zwnj + 'های کانال')
    expect(fa.settings.invalid.cards_label).toContain('یک' + zwnj + 'خطی')
  })
})

describe('the row editor', () => {
  beforeEach(async () => {
    await setLang('en')
  })
  afterEach(() => {
    document.body.innerHTML = ''
    void setLang('fa')
  })

  /** mountEditor shows the editor the way the card does: it takes what comes down and hands back what it is given. */
  async function mountEditor(initial: string, props: Record<string, unknown> = {}) {
    const value = ref(initial)
    const emitted: string[] = []
    let touches = 0
    const Host = defineComponent({
      render: () =>
        h(ChannelCardsEditor, {
          modelValue: value.value,
          inputId: 'cards',
          describedBy: 'cards-help',
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
    return { root, value, emitted, touches: () => touches, unmount: () => app.unmount() }
  }
  const rowsOf = (root: HTMLElement) => [...root.querySelectorAll('.channel-card')] as HTMLElement[]
  const inputsOf = (row: HTMLElement) => [...row.querySelectorAll('input')] as HTMLInputElement[]
  const button = (root: HTMLElement, label: string) => root.querySelector(`button[aria-label="${label}"]`) as HTMLButtonElement
  const addButton = (root: HTMLElement) => [...root.querySelectorAll('button')].find((b) => (b.textContent ?? '').includes('Add a button')) as HTMLButtonElement
  const type = (input: HTMLInputElement, v: string) => {
    input.value = v
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }
  const texts = (root: HTMLElement) => rowsOf(root).map((r) => inputsOf(r).map((i) => i.value))
  const two = '[{"label":{"fa":"آ","en":"A"},"post":"https://t.me/aaaaaa/1"},{"label":{"fa":"ب","en":"B"},"post":"https://t.me/bbbbbb/1"}]'
  const three = JSON.stringify([...JSON.parse(two), { label: { en: 'C' }, post: 'https://t.me/cccccc/1' }])

  it('shows the buttons that are saved, each with four inputs that a screen reader can name', async () => {
    const { root, unmount } = await mountEditor(reviews)
    const rows = rowsOf(root)
    expect(rows).toHaveLength(1)
    expect(rows[0].getAttribute('aria-label')).toBe('Button 1')
    expect(texts(root)).toEqual([['کانال نظرات', 'Reviews', '', 'https://t.me/heisen_reviews/2']])
    const [fa1, en1, channel, post] = inputsOf(rows[0])
    // each input has its own label, and the text ones read in their own direction, the links left to right
    const nameOf = (i: HTMLInputElement) => root.querySelector(`label[for="${i.id}"]`)?.textContent
    expect([fa1, en1, channel, post].map(nameOf)).toEqual(['Button text (Persian)', 'Button text (English)', 'Channel link', "Link of the channel's first post"])
    expect([fa1, en1, channel, post].map((i) => i.dir)).toEqual(['auto', 'auto', 'ltr', 'ltr'])
    // the field's id is on the first input, the others have their own; the post input points at its hint
    expect(fa1.id).toBe('cards')
    expect(new Set([fa1, en1, channel, post].map((i) => i.id)).size).toBe(4)
    expect(post.getAttribute('aria-describedby')).toBe(`${post.id}-hint`)
    expect(root.querySelector(`#${post.id}-hint`)?.textContent).toContain('Copy Link')
    expect(root.querySelector('[role=group]')?.getAttribute('aria-describedby')).toBe('cards-help')
    expect(post.getAttribute('placeholder')).toBe('https://t.me/yourchannel/2')
    expect(root.textContent).toContain('Buttons come in the menu in this order.')
    unmount()
  })

  it('says so when there is no button, and lets the first one be added', async () => {
    const { root, value, emitted, touches, unmount } = await mountEditor('')
    expect(root.textContent).toContain('No channel button yet.')
    expect(rowsOf(root)).toHaveLength(0)
    expect(addButton(root).id).toBe('cards') // the card focuses the field on Save: with no row, it is the Add button
    addButton(root).click()
    await nextTick()
    expect(rowsOf(root)).toHaveLength(1)
    expect(inputsOf(rowsOf(root)[0])[0].id).toBe('cards')
    await vi.waitFor(() => expect(document.activeElement).toBe(inputsOf(rowsOf(root)[0])[0])) // the new button is what is typed next
    expect(root.textContent).not.toContain('No channel button yet.')
    // an empty row is what it is: a list the card refuses, not one that is hidden
    expect(emitted).toEqual(['[{"label":{},"post":""}]'])
    expect(validate(cardsItem(), value.value)).toEqual({ code: 'cards_label' })
    expect(touches()).toBe(1)
    unmount()
  })

  it('hands up the rows as compact JSON as they are typed, without losing a row that is half typed', async () => {
    const { root, value, emitted, touches, unmount } = await mountEditor('')
    addButton(root).click()
    await nextTick()
    const [fa1, en1, channel, post] = inputsOf(rowsOf(root)[0])
    type(en1, 'Reviews ')
    type(fa1, 'کانال نظرات')
    type(post, 'https://t.me/heisen_re') // not a link yet
    await nextTick()
    expect(emitted.at(-1)).toBe('[{"label":{"fa":"کانال نظرات","en":"Reviews "},"post":"https://t.me/heisen_re"}]')
    // what comes back down is the echo of the edit: the rows keep what was typed, space and all
    expect(texts(root)).toEqual([['کانال نظرات', 'Reviews ', '', 'https://t.me/heisen_re']])
    expect(validate(cardsItem(), normalize(cardsItem(), value.value))).toEqual({ code: 'cards_post' })
    // typed another way: the card still sends it as the bot stores it
    type(post, 'https://telegram.me/heisen_reviews/2?single')
    type(channel, ' https://t.me/heisen_reviews ')
    await nextTick()
    expect(emitted.at(-1)).toBe(
      '[{"label":{"fa":"کانال نظرات","en":"Reviews "},"channel":"https://t.me/heisen_reviews","post":"https://telegram.me/heisen_reviews/2?single"}]',
    )
    expect(normalize(cardsItem(), value.value)).toBe(reviews)
    expect(texts(root)[0][3]).toBe('https://telegram.me/heisen_reviews/2?single')
    // a blank text is not sent, a blank channel is not sent
    type(fa1, '   ')
    type(channel, '')
    await nextTick()
    expect(emitted.at(-1)).toBe('[{"label":{"en":"Reviews "},"post":"https://telegram.me/heisen_reviews/2?single"}]')
    // leaving an input tells the card
    const before = touches()
    en1.dispatchEvent(new Event('blur'))
    expect(touches()).toBe(before + 1)
    unmount()
  })

  it('takes the list the card brings down (a save, an undo) in place of its rows, but not its own echo', async () => {
    const { root, value, unmount } = await mountEditor(two)
    expect(texts(root).map((r) => r[1])).toEqual(['A', 'B'])
    type(inputsOf(rowsOf(root)[0])[1], 'A!')
    await nextTick()
    expect(texts(root).map((r) => r[1])).toEqual(['A!', 'B'])
    // Undo changes: the card sets the saved list again
    value.value = two
    await nextTick()
    expect(texts(root).map((r) => r[1])).toEqual(['A', 'B'])
    // a save that came back as the bot stores it (the same list, written as core writes it) leaves what is typed
    type(inputsOf(rowsOf(root)[1])[3], 'https://t.me/s/bbbbbb/1?single')
    await nextTick()
    value.value = two
    await nextTick()
    expect(texts(root).map((r) => r[3])).toEqual(['https://t.me/aaaaaa/1', 'https://t.me/s/bbbbbb/1?single'])
    // and a list with nothing in it empties the editor
    value.value = ''
    await nextTick()
    expect(rowsOf(root)).toHaveLength(0)
    unmount()
  })

  it('keeps the texts in the other languages with their row, whatever else is edited, moved or removed', async () => {
    const saved =
      '[{"label":{"fa":"آ","en":"A","ar":"أ","ru":"А","tr":"Ay"},"post":"https://t.me/aaaaaa/1"},' +
      '{"label":{"en":"B","tr":"Be"},"channel":"https://t.me/+AbCdEfGh12","post":"https://t.me/c/1234567890/2"}]'
    const { root, emitted, unmount } = await mountEditor(saved)
    expect(inputsOf(rowsOf(root)[0]).length).toBe(4) // no input for them
    type(inputsOf(rowsOf(root)[0])[1], 'Alpha')
    await nextTick()
    expect(emitted.at(-1)).toBe(
      '[{"label":{"fa":"آ","en":"Alpha","ar":"أ","ru":"А","tr":"Ay"},"post":"https://t.me/aaaaaa/1"},' +
        '{"label":{"en":"B","tr":"Be"},"channel":"https://t.me/+AbCdEfGh12","post":"https://t.me/c/1234567890/2"}]',
    )
    // the channel and the post of the second row, edited
    type(inputsOf(rowsOf(root)[1])[3], 'https://t.me/c/1234567890/3')
    await nextTick()
    expect(emitted.at(-1)).toContain('"post":"https://t.me/c/1234567890/3"')
    // the first row moves down: its other texts go with it
    button(root, 'Move down: button 1').click()
    await nextTick()
    expect(emitted.at(-1)).toBe(
      '[{"label":{"en":"B","tr":"Be"},"channel":"https://t.me/+AbCdEfGh12","post":"https://t.me/c/1234567890/3"},' +
        '{"label":{"fa":"آ","en":"Alpha","ar":"أ","ru":"А","tr":"Ay"},"post":"https://t.me/aaaaaa/1"}]',
    )
    // the text of the first row cleared in both languages it has an input for: the others still make a label
    type(inputsOf(rowsOf(root)[1])[0], '')
    type(inputsOf(rowsOf(root)[1])[1], '')
    await nextTick()
    expect(emitted.at(-1)).toContain('"label":{"ar":"أ","ru":"А","tr":"Ay"},"post":"https://t.me/aaaaaa/1"')
    expect(validate(cardsItem(), normalize(cardsItem(), emitted.at(-1) as string))).toBeNull()
    // removing the first row leaves the other one whole
    button(root, 'Remove button 1').click()
    await nextTick()
    expect(emitted.at(-1)).toBe('[{"label":{"ar":"أ","ru":"А","tr":"Ay"},"post":"https://t.me/aaaaaa/1"}]')
    unmount()
  })

  it('moves a button up or down, the first one with no way up and the last with no way down, and keeps the focus on it', async () => {
    const { root, emitted, touches, unmount } = await mountEditor(three)
    const labels = () => texts(root).map((r) => r[1])
    expect(labels()).toEqual(['A', 'B', 'C'])
    expect(button(root, 'Move up: button 1').disabled).toBe(true)
    expect(button(root, 'Move down: button 3').disabled).toBe(true)
    expect(button(root, 'Move up: button 2').disabled).toBe(false)
    expect(button(root, 'Move down: button 2').disabled).toBe(false)

    button(root, 'Move up: button 3').click()
    await vi.waitFor(() => expect(labels()).toEqual(['A', 'C', 'B']))
    // the focus went with the button: it is on the same arrow of its new place
    await vi.waitFor(() => expect(document.activeElement).toBe(button(root, 'Move up: button 2')))
    expect(emitted.at(-1)).toBe(
      '[{"label":{"fa":"آ","en":"A"},"post":"https://t.me/aaaaaa/1"},{"label":{"en":"C"},"post":"https://t.me/cccccc/1"},{"label":{"fa":"ب","en":"B"},"post":"https://t.me/bbbbbb/1"}]',
    )
    // to the top: that arrow is off there, so the focus is on the other one
    ;(document.activeElement as HTMLButtonElement).click()
    await vi.waitFor(() => expect(labels()).toEqual(['C', 'A', 'B']))
    await vi.waitFor(() => expect(document.activeElement).toBe(button(root, 'Move down: button 1')))
    // and down again
    ;(document.activeElement as HTMLButtonElement).click()
    await vi.waitFor(() => expect(labels()).toEqual(['A', 'C', 'B']))
    expect(touches()).toBe(3)
    unmount()
  })

  it('removes a button', async () => {
    const { root, emitted, unmount } = await mountEditor(three)
    button(root, 'Remove button 2').click()
    await nextTick()
    expect(texts(root).map((r) => r[1])).toEqual(['A', 'C'])
    expect(emitted.at(-1)).toBe('[{"label":{"fa":"آ","en":"A"},"post":"https://t.me/aaaaaa/1"},{"label":{"en":"C"},"post":"https://t.me/cccccc/1"}]')
    button(root, 'Remove button 1').click()
    button(root, 'Remove button 1').click()
    await nextTick()
    expect(emitted.at(-1)).toBe('') // no button at all: nothing is stored
    expect(root.textContent).toContain('No channel button yet.')
    unmount()
  })

  it('stops at six buttons: Add is off and the note says why', async () => {
    const { root, emitted, unmount } = await mountEditor(channels(5))
    expect(addButton(root).disabled).toBe(false)
    expect(root.textContent).toContain('Buttons come in the menu in this order.')
    addButton(root).click()
    await nextTick()
    expect(rowsOf(root)).toHaveLength(6)
    expect(addButton(root).disabled).toBe(true)
    expect(root.textContent).toContain('The menu holds at most 6 channel buttons.')
    expect(root.textContent).not.toContain('Buttons come in the menu in this order.')
    expect(emitted).toHaveLength(1)
    // a list that is already full
    const full = await mountEditor(channels(6), { inputId: 'full' })
    expect(addButton(full.root).disabled).toBe(true)
    // taking one away gives the button back
    button(full.root, 'Remove button 6').click()
    await nextTick()
    expect(addButton(full.root).disabled).toBe(false)
    full.unmount()
    unmount()
  })

  it('shows the buttons and nothing to change when the role may not change them', async () => {
    const { root, emitted, unmount } = await mountEditor(three, { locked: true })
    expect(rowsOf(root)).toHaveLength(3)
    expect(root.querySelectorAll('button')).toHaveLength(0) // no Add, no arrows, no remove
    const all = inputsOf(root)
    expect(all).toHaveLength(12)
    expect(all.every((i) => i.readOnly)).toBe(true)
    expect(emitted).toEqual([])
    unmount()
    const none = await mountEditor('', { locked: true })
    expect(none.root.textContent).toContain('No channel button yet.')
    expect(none.root.querySelectorAll('button')).toHaveLength(0)
    none.unmount()
  })

  it('marks every input when the card says the list is wrong', async () => {
    const { root, unmount } = await mountEditor(reviews, { invalid: true })
    expect(inputsOf(root).every((i) => i.getAttribute('aria-invalid') === 'true' || i.classList.contains('p-invalid'))).toBe(true)
    unmount()
  })

  it('reads in Persian, with its numbers in Persian digits', async () => {
    await setLang('fa')
    const { root, unmount } = await mountEditor(two)
    const row = fa.settings.channel_cards.row
    expect(rowsOf(root).map((r) => r.getAttribute('aria-label'))).toEqual([row.replace('{n}', '۱'), row.replace('{n}', '۲')])
    expect(root.textContent).toContain(fa.settings.channel_cards.order)
    expect(root.querySelector(`button[aria-label="${fa.settings.channel_cards.remove.replace('{n}', '۲')}"]`)).not.toBeNull()
    expect(root.querySelector(`label[for="${inputsOf(rowsOf(root)[0])[0].id}"]`)?.textContent).toBe(fa.settings.channel_cards.label_fa)
    unmount()
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

  /** boot starts the app on the Settings page against a fake core that keeps the settings it is given. */
  async function boot(lang: 'en' | 'fa', state: { items: SettingItem[] }, calls: string[] = []) {
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
        const reply = () => json(200, { items: state.items, groups: ['channels'] })
        if (url === '/api/v1/me') return json(200, me)
        if (url === '/api/v1/settings' && method === 'GET') return reply()
        if (url === '/api/v1/settings' && method === 'PUT') {
          const values = JSON.parse(String(init?.body)).values as Record<string, string>
          state.items = state.items.map((i) => (i.key in values ? { ...i, value: values[i.key] } : i))
          return reply()
        }
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
  const inputs = (card: HTMLElement) => [...card.querySelectorAll('.channel-card input')] as HTMLInputElement[]
  const buttonLabelled = (card: HTMLElement, label: string) =>
    [...card.querySelectorAll('button')].find((b) => text(b) === label || b.getAttribute('aria-label') === label) as HTMLButtonElement
  const saveButton = (card: HTMLElement) => card.querySelector('button[type=submit]') as HTMLButtonElement
  const sent = (calls: string[]) => calls.filter((c) => c.startsWith('PUT /api/v1/settings')).map((c) => JSON.parse(c.slice(c.indexOf('{'))).values as Record<string, string>)

  it('has a Channel buttons card where a button is added, typed in any form and saved the way the bot stores it', async () => {
    const state = { items: [cardsItem()] }
    const calls: string[] = []
    const card = await boot('en', state, calls)
    expect(text(card.querySelector('h2'))).toBe('Channel buttons')
    expect(text(card)).toContain('No channel button yet.')
    expect(text(card)).toContain('under Buy and Free trial')
    expect(text(card)).toContain('Write each button')
    expect(card.classList.contains('wide')).toBe(true) // a card of rows has the page's full width
    expect(saveButton(card).disabled).toBe(true)

    buttonLabelled(card, 'Add a button').click()
    await nextTick()
    // the empty row is refused where the card says so, and nothing is sent
    expect(text(card)).toContain(en.settings.invalid.cards_label)
    saveButton(card).click()
    await nextTick()
    expect(sent(calls)).toEqual([])
    const [fa1, en1, channel, post] = inputs(card)
    type(fa1, 'کانال نظرات')
    type(en1, '  Reviews ')
    type(post, 'https://t.me/heisen_re')
    post.dispatchEvent(new Event('blur'))
    await nextTick()
    expect(text(card)).toContain(en.settings.invalid.cards_post)
    expect(text(card)).not.toContain(en.settings.invalid.cards_label)
    // the post as Telegram's web page gives it, with a channel the post already says
    type(post, 'https://t.me/s/heisen_reviews/2?single')
    type(channel, 'https://telegram.me/heisen_reviews')
    await nextTick()
    expect(text(card)).not.toContain(en.settings.invalid.cards_post)
    expect(saveButton(card).disabled).toBe(false)
    saveButton(card).click()
    await vi.waitFor(() => expect(sent(calls)).toHaveLength(1))
    expect(sent(calls)[0]).toEqual({ 'bot.channel_cards': reviews })
    // saved: nothing more to save, and what was typed is still there
    await vi.waitFor(() => expect(saveButton(card).disabled).toBe(true))
    expect(inputs(card).map((i) => i.value)).toEqual(['کانال نظرات', '  Reviews ', 'https://telegram.me/heisen_reviews', 'https://t.me/s/heisen_reviews/2?single'])
  })

  it('asks for the invite link of a private channel, refuses the same post twice and stops at six buttons', async () => {
    const state = { items: [cardsItem({ value: channels(2) })] }
    const calls: string[] = []
    const card = await boot('en', state, calls)
    expect(inputs(card)).toHaveLength(8)
    // a second button on a private channel's post, with no invite link
    buttonLabelled(card, 'Add a button').click()
    await nextTick()
    const row = [...card.querySelectorAll('.channel-card')][2] as HTMLElement
    const [fa3, , channel3, post3] = [...row.querySelectorAll('input')] as HTMLInputElement[]
    type(fa3, 'Private')
    type(post3, 'https://t.me/c/1234567890/45')
    saveButton(card).click()
    await nextTick()
    expect(text(card)).toContain(en.settings.invalid.cards_channel)
    expect(text(card)).toContain(en.settings.fix_errors)
    expect(sent(calls)).toEqual([])
    await vi.waitFor(() => expect(document.activeElement).toBe(inputs(card)[0])) // the card goes to the field, which is its first input
    type(channel3, 'https://t.me/+AbCdEfGh12')
    await nextTick()
    expect(text(card)).not.toContain(en.settings.invalid.cards_channel)
    // the same post as the first button, however it is written
    type(post3, 'https://t.me/s/channel/1?single')
    await nextTick()
    expect(text(card)).toContain(en.settings.invalid.cards_dup)
    type(post3, 'https://t.me/c/1234567890/45')
    await nextTick()
    saveButton(card).click()
    await vi.waitFor(() => expect(sent(calls)).toHaveLength(1))
    expect(JSON.parse(sent(calls)[0]['bot.channel_cards'])[2]).toEqual({
      label: { fa: 'Private' },
      channel: 'https://t.me/+AbCdEfGh12',
      post: 'https://t.me/c/1234567890/45',
    })
    // three buttons are in (once the save is done); the Add button is on until there are six
    await vi.waitFor(() => expect(saveButton(card).disabled).toBe(true))
    for (let i = 0; i < 3; i++) {
      expect(buttonLabelled(card, 'Add a button').disabled).toBe(false)
      buttonLabelled(card, 'Add a button').click()
      await nextTick()
    }
    expect(card.querySelectorAll('.channel-card')).toHaveLength(6)
    expect(buttonLabelled(card, 'Add a button').disabled).toBe(true)
    expect(text(card)).toContain('The menu holds at most 6 channel buttons.')
  })

  it('shows the buttons that are saved, in their order, and saves a new order', async () => {
    const state = { items: [cardsItem({ value: channels(3) })] }
    const calls: string[] = []
    const card = await boot('en', state, calls)
    const names = () => [...card.querySelectorAll('.channel-card')].map((r) => (r.querySelectorAll('input')[1] as HTMLInputElement).value)
    expect(names()).toEqual(['Button 1', 'Button 2', 'Button 3'])
    expect(saveButton(card).disabled).toBe(true)
    buttonLabelled(card, 'Move up: button 3').click()
    await vi.waitFor(() => expect(names()).toEqual(['Button 1', 'Button 3', 'Button 2']))
    await vi.waitFor(() => expect(saveButton(card).disabled).toBe(false))
    saveButton(card).click()
    await vi.waitFor(() => expect(sent(calls)).toHaveLength(1))
    const saved = JSON.parse(sent(calls)[0]['bot.channel_cards']) as { label: { en: string } }[]
    expect(saved.map((c) => c.label.en)).toEqual(['Button 1', 'Button 3', 'Button 2'])
    await vi.waitFor(() => expect(saveButton(card).disabled).toBe(true))
    // taking every button away stores nothing
    for (let i = 0; i < 3; i++) buttonLabelled(card, 'Remove button 1').click()
    await nextTick()
    await vi.waitFor(() => expect(saveButton(card).disabled).toBe(false))
    saveButton(card).click()
    await vi.waitFor(() => expect(sent(calls)).toHaveLength(2))
    expect(sent(calls)[1]).toEqual({ 'bot.channel_cards': '' })
  })

  it('keeps the texts of the other languages when the list is saved again', async () => {
    const saved = '[{"label":{"fa":"آ","en":"A","ru":"А"},"post":"https://t.me/aaaaaa/1"}]'
    const state = { items: [cardsItem({ value: saved })] }
    const calls: string[] = []
    const card = await boot('en', state, calls)
    type(inputs(card)[1], 'Alpha')
    await vi.waitFor(() => expect(saveButton(card).disabled).toBe(false))
    saveButton(card).click()
    await vi.waitFor(() => expect(sent(calls)).toHaveLength(1))
    expect(sent(calls)[0]).toEqual({ 'bot.channel_cards': '[{"label":{"fa":"آ","en":"Alpha","ru":"А"},"post":"https://t.me/aaaaaa/1"}]' })
  })

  it('shows the buttons to a role that may not change them, read only', async () => {
    const state = { items: [cardsItem({ value: channels(2), editable: false })] }
    const card = await boot('en', state)
    expect(inputs(card)).toHaveLength(8)
    expect(inputs(card).every((i) => i.readOnly)).toBe(true)
    expect(card.querySelector('.channel-cards button')).toBeNull()
    expect(card.querySelector('.pi-lock')).not.toBeNull()
  })

  it('reads in Persian', async () => {
    const state = { items: [cardsItem({ value: reviews })] }
    const card = await boot('fa', state)
    expect(text(card.querySelector('h2'))).toBe(fa.settings.group.channels.title)
    expect(text(card)).toContain(fa.settings.group.channels.desc)
    expect(text(card)).toContain(fa.settings.field.bot.channel_cards.help)
    expect(text(card)).toContain(fa.settings.channel_cards.row.replace('{n}', '۱'))
    expect(text(card)).toContain(fa.settings.channel_cards.label_fa)
    expect(inputs(card).map((i) => i.value)).toEqual(['کانال نظرات', 'Reviews', '', 'https://t.me/heisen_reviews/2'])
  })
})

describe('the field of the card', () => {
  const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim()
  beforeEach(async () => {
    await setLang('en')
  })
  afterEach(() => {
    document.body.innerHTML = ''
    void setLang('fa')
  })

  function mountField(it: SettingItem, value: string) {
    const root = document.createElement('div')
    document.body.appendChild(root)
    const app = createApp({ render: () => h(SettingField, { item: it, modelValue: value }) })
    app.use(i18n).use(PrimeVue)
    app.mount(root)
    return { root, unmount: () => app.unmount() }
  }

  it('draws the row editor for the channel buttons, with its label and help, and plain inputs for the other keys', async () => {
    const { root, unmount } = mountField(cardsItem({ value: reviews }), reviews)
    await nextTick()
    expect(root.querySelector('.channel-cards')).not.toBeNull()
    expect(root.querySelectorAll('.channel-card input')).toHaveLength(4)
    expect(root.querySelector('.field')?.classList.contains('is-rows')).toBe(true)
    expect(text(root.querySelector('.label'))).toBe('Buttons')
    expect(text(root.querySelector('.help'))).toContain('Copy Link')
    // the label points at the first input
    const label = root.querySelector('label.label') as HTMLLabelElement
    expect(label.htmlFor).toBe('setting-bot-channel-cards')
    expect(document.getElementById(label.htmlFor)?.closest('.channel-card')).not.toBeNull()
    unmount()

    const other = mountField(item('bot.plan_layout', 'text'), 'x')
    await nextTick()
    expect(other.root.querySelector('.channel-cards')).toBeNull()
    expect(other.root.querySelector('.field')?.classList.contains('is-rows')).toBe(false)
    expect(other.root.querySelector('input#setting-bot-plan-layout')).not.toBeNull()
    other.unmount()
    // a list that is not this one keeps its own editor
    const prizes = mountField(item('wheel.prizes', 'list'), '')
    await nextTick()
    expect(prizes.root.querySelector('.prizes')).not.toBeNull()
    expect(prizes.root.querySelector('.channel-cards')).toBeNull()
    prizes.unmount()
  })
})
