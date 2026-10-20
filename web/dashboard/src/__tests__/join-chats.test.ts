import { describe, expect, it } from 'vitest'
import { ApiError } from '../api'
import {
  LABEL_MAX,
  canAdd,
  changed,
  draftsOf,
  firstProblem,
  hasProblems,
  inactiveFrom,
  isLimit,
  moveRow,
  normalizeRow,
  over,
  payload,
  rowProblems,
  serverField,
  type DraftRow,
} from '../joinChats'
import en from '../locales/en.json'
import fa from '../locales/fa.json'
import type { JoinChatRow } from '../types'

// The Settings page's list of required channels and groups: the rules of a row,
// which rows the license counts, and how core's refusals are read.

const row = (chat: string, link = '', label = ''): DraftRow => draftsOf([{ chat, link, label, primary: false, active: true }])[0]
const saved = (chat: string, link = '', label = ''): JoinChatRow => ({ chat, link, label, primary: false, active: true })
const codes = (rows: DraftRow[]) => rowProblems(rows).map((p) => Object.fromEntries(Object.entries(p).map(([k, v]) => [k, v.code])))

describe('a row', () => {
  it('reads what is pasted: a t.me link, Persian digits, an @-less name, spaces', () => {
    expect(normalizeRow(row('https://t.me/my_channel'))).toMatchObject({ chat: '@my_channel' })
    expect(normalizeRow(row('my_channel'))).toMatchObject({ chat: '@my_channel' })
    expect(normalizeRow(row('−۱۰۰۱۲۳۴۵۶۷۸۹۰'))).toMatchObject({ chat: '-1001234567890' })
    expect(normalizeRow(row(' @a_channel ', ' https://t.me/+AbC ', '  Our   news '))).toEqual({ chat: '@a_channel', link: 'https://t.me/+AbC', label: 'Our news' })
  })

  it('is checked as core checks the settings', () => {
    expect(codes([row('@first_news'), row('-1001234567890', 'https://t.me/+AbC', 'VIP'), row('@team_group', '', 'Team')])).toEqual([{}, {}, {}])
    expect(codes([row('')])).toEqual([{ chat: 'required' }])
    expect(codes([row('abc')])).toEqual([{ chat: 'channel' }])
    expect(codes([row('@abc')])).toEqual([{ chat: 'channel' }])
    expect(codes([row('-1001234567890')])).toEqual([{ link: 'link_required' }])
    expect(codes([row('@news_chan', 'http://t.me/x')])).toEqual([{ link: 'tme_link' }])
    expect(codes([row('@news_chan', 'https://example.com/x')])).toEqual([{ link: 'tme_link' }])
    expect(codes([row('@news_chan', '', 'x'.repeat(LABEL_MAX))])).toEqual([{}])
    expect(rowProblems([row('@news_chan', '', 'x'.repeat(LABEL_MAX + 1))])[0].label).toEqual({ code: 'too_long', params: { max: LABEL_MAX } })
    expect(codes([row('@news_chan', '', 'a\u0007b')])).toEqual([{ label: 'one_line' }])
  })

  it('may be in the list once, the case of an @name aside', () => {
    expect(codes([row('@news_chan'), row('@other_chan'), row('@News_Chan')])).toEqual([{}, {}, { chat: 'duplicate' }])
    expect(codes([row('https://t.me/news_chan'), row('news_chan')])).toEqual([{}, { chat: 'duplicate' }])
    const p = rowProblems([row('@news_chan'), row('')])
    expect(hasProblems(p)).toBe(true)
    expect(firstProblem(p)).toEqual([1, 'chat'])
    expect(firstProblem(rowProblems([row('@news_chan')]))).toBeNull()
  })
})

describe('what is sent and what changed', () => {
  it('sends every row normalized, in order', () => {
    expect(payload([row('t.me/first_news'), row('@second_chan', '', ' Two ')])).toEqual({
      chats: [
        { chat: '@first_news', link: '', label: '' },
        { chat: '@second_chan', link: '', label: 'Two' },
      ],
    })
  })

  it('compares the rows as they would be sent with what is saved', () => {
    const list = [saved('@first_news'), saved('@second_chan', '', 'Two')]
    expect(changed(draftsOf(list), list)).toBe(false)
    expect(changed([row('@first_news'), row('@second_chan', '', ' Two ')], list)).toBe(false)
    expect(changed([row('@first_news')], list)).toBe(true)
    expect(changed([row('@second_chan', '', 'Two'), row('@first_news')], list)).toBe(true)
    expect(changed([...draftsOf(list), row('')], list)).toBe(true)
  })
})

describe('the license', () => {
  it('counts the first N rows, the first one always', () => {
    expect(inactiveFrom(-1, 5)).toBe(5)
    expect(inactiveFrom(1, 3)).toBe(1)
    expect(inactiveFrom(2, 1)).toBe(1)
    expect(inactiveFrom(0, 3)).toBe(1)
    expect(inactiveFrom(1, 0)).toBe(0)
  })

  it('lets a row be added up to the limit and the most the list holds', () => {
    expect(canAdd(1, 0, 50)).toBe(true)
    expect(canAdd(1, 1, 50)).toBe(false)
    expect(canAdd(-1, 49, 50)).toBe(true)
    expect(canAdd(-1, 50, 50)).toBe(false)
    expect(canAdd(3, 2, 50)).toBe(true)
  })

  it('says when more are saved than the plan counts', () => {
    expect(over(1, 2)).toBe(true)
    expect(over(1, 1)).toBe(false)
    expect(over(-1, 30)).toBe(false)
  })

  it('reads core’s refusals', () => {
    const field = new ApiError(400, 'invalid', 'chats.2.link: bad', { field: 'chats.2.link' })
    expect(serverField(field)).toEqual({ index: 2, field: 'link' })
    expect(serverField(new ApiError(400, 'invalid', 'x', { field: 'chats' }))).toBeNull()
    expect(serverField(new ApiError(400, 'invalid', 'x', { field: 'join.channel' }))).toBeNull()
    expect(serverField(new Error('x'))).toBeNull()
    const limit = new ApiError(409, 'state', 'the free tier allows 1 required chat', { license: 'max_join_chats' })
    expect(isLimit(limit)).toBe(true)
    expect(isLimit(new ApiError(409, 'state', 'x', { license: 'max_servers' }))).toBe(false)
    expect(isLimit(field)).toBe(false)
  })
})

describe('moving rows', () => {
  const ids = (rows: DraftRow[]) => rows.map((r) => r.chat).join(',')
  const list = [row('@a_first'), row('@b_second'), row('@c_third'), row('@d_fourth')]

  it('reorders the rows after the first', () => {
    expect(ids(moveRow(list, 3, 1))).toBe('@a_first,@d_fourth,@b_second,@c_third')
    expect(ids(moveRow(list, 1, 2))).toBe('@a_first,@c_third,@b_second,@d_fourth')
  })

  it('never moves the first row, nor a row above it', () => {
    expect(moveRow(list, 0, 2)).toBe(list)
    expect(moveRow(list, 2, 0)).toBe(list)
    expect(moveRow(list, 2, 9)).toBe(list)
    expect(moveRow(list, 2, 2)).toBe(list)
  })
})

describe('the texts', () => {
  const get = (o: unknown, path: string) => path.split('.').reduce<unknown>((x, k) => (x as Record<string, unknown> | undefined)?.[k], o)

  it('exist in both languages', () => {
    for (const [lang, catalog] of [['en', en], ['fa', fa]] as const) {
      for (const k of [
        'settings.group.join.title', 'settings.group.join.desc', 'settings.group.join.note', 'settings.join.locked', 'settings.join.dormant',
        'settings.join.count', 'settings.join.count_unlimited', 'settings.join.limit_note', 'settings.join.over_note', 'settings.join.empty',
        'settings.join.first', 'settings.join.nth', 'settings.join.inactive', 'settings.join.inactive_hint', 'settings.join.move_up',
        'settings.join.move_down', 'settings.join.remove', 'settings.join.add', 'settings.join.max', 'settings.join.see_license',
        'settings.join.field.chat', 'settings.join.field.link', 'settings.join.field.label', 'settings.invalid.required',
        'settings.invalid.duplicate', 'license.refused.max_join_chats',
      ]) {
        const v = get(catalog, k)
        expect(typeof v === 'string' && v.trim() !== '', `${lang} ${k}`).toBe(true)
      }
    }
  })

  it('say the limit and the plan that has none', () => {
    expect(get(en, 'settings.join.count')).toBe('Required channels and groups: {used} of {limit} (allowed by your plan)')
    expect(String(get(en, 'license.refused.max_join_chats'))).toContain('Pro')
    expect(String(get(fa, 'license.refused.max_join_chats'))).toContain('Pro')
  })
})
