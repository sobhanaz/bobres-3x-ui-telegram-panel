// The Settings page's list of required channels and groups: what a row accepts,
// what is sent, which rows the license counts, and how core's answers read.
// Core checks everything again (it is the source of truth); this only spares the
// owner a round trip. The first row is the setting join.channel, join.link and
// join.label; the others are the extra chats.
import { ApiError } from './api'
import { normalizeChannel, numericChannel, validChannel, validJoinLink, type Problem } from './settings'
import type { JoinChatRow } from './types'

/** The longest name of a chat, in characters (core takes the same). */
export const LABEL_MAX = 32

const controlChars = /[\u0000-\u001f\u007f]/

/** A row being edited. id only keys the list, so that a row keeps its inputs
 * when others move. */
export interface DraftRow {
  id: number
  chat: string
  link: string
  label: string
}

export type RowField = 'chat' | 'link' | 'label'
export type RowProblems = Partial<Record<RowField, Problem>>

let nextId = 1

/** blank is a new, empty row. */
export function blank(): DraftRow {
  return { id: nextId++, chat: '', link: '', label: '' }
}

/** draftsOf turns the saved list into rows to edit. */
export function draftsOf(chats: JoinChatRow[]): DraftRow[] {
  return chats.map((c) => ({ id: nextId++, chat: c.chat, link: c.link, label: c.label }))
}

/** normalizeRow reads what the owner typed: a channel as @name or -100id (a
 * t.me link of a public channel and Persian digits are understood), a link
 * trimmed, a name on one line. */
export function normalizeRow(r: Pick<DraftRow, 'chat' | 'link' | 'label'>): Pick<DraftRow, 'chat' | 'link' | 'label'> {
  return { chat: normalizeChannel(r.chat), link: r.link.trim(), label: r.label.trim().replace(/\s+/g, ' ') }
}

/** rowProblems checks every row the way core does: the same rules as the
 * settings (a channel, a t.me link, a private channel needs its link) and a
 * chat only once in the list. */
export function rowProblems(rows: DraftRow[]): RowProblems[] {
  const seen = new Set<string>()
  return rows.map((row) => {
    const r = normalizeRow(row)
    const p: RowProblems = {}
    if (r.chat === '') p.chat = { code: 'required' }
    else if (!validChannel(r.chat)) p.chat = { code: 'channel' }
    else if (seen.has(r.chat.toLowerCase())) p.chat = { code: 'duplicate' }
    if (r.chat !== '') seen.add(r.chat.toLowerCase())
    if (r.link === '') {
      if (numericChannel(r.chat)) p.link = { code: 'link_required' }
    } else if (controlChars.test(r.link) || !validJoinLink(r.link)) {
      p.link = { code: 'tme_link' }
    }
    if (controlChars.test(r.label)) p.label = { code: 'one_line' }
    else if ([...r.label].length > LABEL_MAX) p.label = { code: 'too_long', params: { max: LABEL_MAX } }
    return p
  })
}

/** hasProblems: some row would be refused. */
export function hasProblems(p: RowProblems[]): boolean {
  return p.some((r) => Object.keys(r).length > 0)
}

/** firstProblem is the first field with a problem, to focus: [row, field]. */
export function firstProblem(p: RowProblems[]): [number, RowField] | null {
  for (const [i, r] of p.entries()) {
    for (const f of ['chat', 'link', 'label'] as const) if (r[f]) return [i, f]
  }
  return null
}

/** payload is the PUT body: every row normalized. */
export function payload(rows: DraftRow[]): { chats: { chat: string; link: string; label: string }[] } {
  return { chats: rows.map(normalizeRow) }
}

/** changed: the rows would change what is saved (rows are compared as they would be sent). */
export function changed(rows: DraftRow[], saved: JoinChatRow[]): boolean {
  const now = rows.map(normalizeRow)
  return now.length !== saved.length || now.some((r, i) => r.chat !== saved[i].chat || r.link !== saved[i].link || r.label !== saved[i].label)
}

/** inactiveFrom is the index of the first row customers are not asked about
 * (rows.length when all count). The license counts the first N rows, the first
 * one always; no limit is -1. */
export function inactiveFrom(limit: number, count: number): number {
  return limit < 0 ? count : Math.min(count, Math.max(limit, 1))
}

/** canAdd: one more row fits the list and the license. */
export function canAdd(limit: number, count: number, max: number): boolean {
  return count < max && (limit < 0 || count < limit)
}

/** over: the list has more rows than the license counts (a license that ended). */
export function over(limit: number, count: number): boolean {
  return limit >= 0 && count > Math.max(limit, 1)
}

/** moveRow moves a row to another place among the rows after the first: the
 * first row is the setting join.channel and stays first. */
export function moveRow(rows: DraftRow[], from: number, to: number): DraftRow[] {
  if (from < 1 || to < 1 || from >= rows.length || to >= rows.length || from === to) return rows
  const out = rows.slice()
  const [r] = out.splice(from, 1)
  out.splice(to, 0, r)
  return out
}

/** serverField is the row and field a 400 from core names ("chats.2.link"), or null. */
export function serverField(e: unknown): { index: number; field: RowField } | null {
  if (!(e instanceof ApiError) || e.status !== 400 || typeof e.data.field !== 'string') return null
  const m = /^chats\.(\d+)\.(chat|link|label)$/.exec(e.data.field)
  return m ? { index: Number(m[1]), field: m[2] as RowField } : null
}

/** isLimit: core refused to add a chat past the license (409, license max_join_chats). */
export function isLimit(e: unknown): boolean {
  return e instanceof ApiError && e.status === 409 && e.data.license === 'max_join_chats'
}
