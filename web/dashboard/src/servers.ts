// Helpers for the Servers page, the plan editor's server choice and the
// overview's servers card: how a server's health reads, what the add and
// edit form accepts and sends, and the warnings staff should see. Core and
// the provisioner check everything again; this only spares a round trip.
// Pure functions (no API, no Vue).
import { asciiNumber } from './format'
import type { InboundItem, OverviewServer, PlanInbound, ServerItem } from './types'

/** Limits core checks again. */
export const SERVER_NAME_MAX = 64
export const WEIGHT_MIN = 1
export const WEIGHT_MAX = 1000
export const WEIGHT_DEFAULT = 100
/** the largest limit on a server's services (0 or empty is none) */
export const MAX_CLIENTS_MAX = 1_000_000
/** the share of its limit at which a server counts as nearly full: the Servers page warns from here, and so does the
 * watchdog's notice in Telegram (core's CapacityWarnPct) */
export const CAPACITY_WARN_PCT = 90

/** ok: answering; down: not answering (no new services meanwhile);
 * unknown: not checked yet; off: takes no new services (not checked). */
export type Health = 'ok' | 'down' | 'unknown' | 'off'

export function healthOf(s: Pick<ServerItem, 'enabled' | 'healthy' | 'checked_at'>): Health {
  if (!s.enabled) return 'off'
  if (!s.checked_at) return 'unknown'
  return s.healthy ? 'ok' : 'down'
}

export const healthSeverity: Record<Health, 'success' | 'danger' | 'secondary' | 'warn'> = {
  ok: 'success',
  down: 'danger',
  unknown: 'warn',
  off: 'secondary',
}

/** The add and edit form, as staff type it. */
export type PanelKind = 'xui' | 'marzban'
export const PANEL_KINDS: PanelKind[] = ['xui', 'marzban']

/** A Marzban server is logged in with an admin's "username:password". */
export function marzbanLoginOK(token: string): boolean {
  const i = token.indexOf(':')
  return i > 0 && i < token.length - 1
}

/** The add and edit form, as staff type it. */
export interface ServerForm {
  /** the panel software; fixed once the server is saved */
  kind: PanelKind
  name: string
  baseURL: string
  subURL: string
  /** a new token; '' keeps the stored one */
  token: string
  weight: string
  /** the most services the server takes; '' or 0 = no limit */
  maxClients: string
  enabled: boolean
  allowPrivate: boolean
  /** save even when the panel does not answer */
  skipCheck: boolean
}

export function blankServer(): ServerForm {
  return { kind: 'xui', name: '', baseURL: '', subURL: '', token: '', weight: String(WEIGHT_DEFAULT), maxClients: '', enabled: true, allowPrivate: false, skipCheck: false }
}

/** serverFormOf reads a server into the editor; the token stays empty
 * (it is never sent back). */
export function serverFormOf(s: ServerItem): ServerForm {
  return {
    kind: s.kind === 'marzban' ? 'marzban' : 'xui',
    name: s.name,
    baseURL: s.base_url,
    subURL: s.sub_base_url ?? '',
    token: '',
    weight: String(s.weight || WEIGHT_DEFAULT),
    maxClients: s.max_clients ? String(s.max_clients) : '',
    enabled: s.enabled,
    allowPrivate: s.allow_private,
    skipCheck: false,
  }
}

function parseURL(v: string): URL | null {
  if (!v || /\s/.test(v)) return null
  try {
    return new URL(v)
  } catch {
    return null
  }
}

/** panelURLProblem: the provisioner wants https, or plain http only for a
 * panel on a private network, and never a user name in the address. */
export function panelURLProblem(raw: string, allowPrivate: boolean): '' | 'url' | 'https' | 'credentials' {
  const u = parseURL(raw.trim())
  if (!u || !u.hostname || (u.protocol !== 'https:' && u.protocol !== 'http:')) return 'url'
  if (u.username || u.password) return 'credentials'
  if (u.protocol === 'http:' && !allowPrivate) return 'https'
  return ''
}

/** subURLProblem: empty, or an http(s) address. */
export function subURLProblem(raw: string): '' | 'url' {
  const v = raw.trim()
  if (!v) return ''
  const u = parseURL(v)
  return u && u.hostname && (u.protocol === 'https:' || u.protocol === 'http:') ? '' : 'url'
}

/** weightOf reads a typed weight (either digit set), or null outside 1..1000. */
export function weightOf(raw: string): number | null {
  const v = asciiNumber(raw)
  if (!/^\d{1,4}$/.test(v)) return null
  const n = Number(v)
  return n >= WEIGHT_MIN && n <= WEIGHT_MAX ? n : null
}

/** maxClientsOf reads a typed limit on a server's services (either digit set): nothing typed is 0, no limit; null when it
 * is not a whole number from 0 to 1,000,000. */
export function maxClientsOf(raw: string): number | null {
  const v = asciiNumber(raw)
  if (v === '') return 0
  if (!/^\d{1,7}$/.test(v)) return null
  const n = Number(v)
  return n <= MAX_CLIENTS_MAX ? n : null
}

export type Fill = 'none' | 'ok' | 'near' | 'full'

/** How full a server is: its services against its limit. */
export interface Capacity {
  clients: number
  /** the limit; 0 = none */
  max: number
  /** the whole percent it is at (more than 100 when the limit was set below what it holds); 0 without a limit */
  pct: number
  /** none: no limit; ok; near: from 90% on; full: at the limit or over it */
  state: Fill
}

/** capacityOf reads a server's fill the way core's watchdog does: a limit of 0 or none is no limit, from 90% on is nearly
 * full, and the limit reached is full. */
export function capacityOf(s: { clients: number; max_clients?: number }): Capacity {
  const clients = Math.max(0, s.clients || 0)
  const max = Math.max(0, s.max_clients || 0)
  if (max === 0) return { clients, max, pct: 0, state: 'none' }
  const pct = Math.floor((clients * 100) / max)
  return { clients, max, pct, state: clients >= max ? 'full' : pct >= CAPACITY_WARN_PCT ? 'near' : 'ok' }
}

/** nearFull lists the servers that take new services and are nearly or entirely full, in the order given (a switched-off
 * server is not watched: it takes nothing anyway). */
export function nearFull<T extends { clients: number; max_clients?: number; enabled?: boolean }>(list: readonly T[]): T[] {
  return list.filter((s) => s.enabled !== false && ['near', 'full'].includes(capacityOf(s).state))
}

/** hostOf is the host and port of an address as written ("" when none),
 * the way the provisioner compares them. */
export function hostOf(raw: string): string {
  const m = /^[a-z][a-z0-9+.-]*:\/\/([^/?#]*)/i.exec(raw.trim())
  return m ? m[1].replace(/^[^@]*@/, '').toLowerCase() : ''
}

/** needsToken: no token is stored, or the address moved to another host or
 * port. The provisioner never sends a stored token to a host it was not
 * entered for. */
export function needsToken(f: Pick<ServerForm, 'baseURL'>, editing: Pick<ServerItem, 'has_token' | 'base_url'> | null): boolean {
  if (!editing?.has_token) return true
  return hostOf(f.baseURL) !== hostOf(editing.base_url)
}

/** serverProblems lists the fields that are not right yet, by the names core
 * uses. A token is needed when none can be kept. */
export function serverProblems(f: ServerForm, editing: Pick<ServerItem, 'has_token' | 'base_url'> | null): string[] {
  const out: string[] = []
  const name = f.name.trim()
  if (!name || [...name].length > SERVER_NAME_MAX) out.push('name')
  if (panelURLProblem(f.baseURL, f.allowPrivate)) out.push('base_url')
  if (subURLProblem(f.subURL)) out.push('sub_base_url')
  const tok = f.token.trim()
  if (/\s/.test(tok) || (!tok && needsToken(f, editing)) || (f.kind === 'marzban' && tok && !marzbanLoginOK(tok))) out.push('token')
  if (weightOf(f.weight) === null) out.push('weight')
  if (maxClientsOf(f.maxClients) === null) out.push('max_clients')
  return out
}

/** serverBody is what POST /servers and PUT /servers/{id} take; an empty
 * token keeps the stored one. */
export function serverBody(f: ServerForm) {
  return {
    kind: f.kind,
    name: f.name.trim(),
    base_url: f.baseURL.trim(),
    sub_base_url: f.subURL.trim(),
    token: f.token.trim(),
    weight: weightOf(f.weight) ?? WEIGHT_DEFAULT,
    max_clients: maxClientsOf(f.maxClients) ?? 0,
    enabled: f.enabled,
    allow_private: f.allowPrivate,
    skip_check: f.skipCheck,
  }
}

/** testBody is what POST /servers/test takes: the typed connection, and
 * for a saved server its id, so an empty token means the stored one. */
export function testBody(f: ServerForm, id = '') {
  return { ...(id ? { id } : {}), kind: f.kind, base_url: f.baseURL.trim(), token: f.token.trim(), allow_private: f.allowPrivate }
}

/** canTest: enough is typed to try the connection. */
export function canTest(f: ServerForm, editing: Pick<ServerItem, 'has_token' | 'base_url'> | null): boolean {
  const p = serverProblems(f, editing)
  return !p.includes('base_url') && !p.includes('token')
}

const sameAddress = (a: string, b: string) => a.trim().replace(/\/+$/, '') === b.trim().replace(/\/+$/, '')

/** addressChanged: the panel address of a saved server was edited. Its
 * services stay mapped to it, so this is only right when the same panel moved. */
export function addressChanged(f: ServerForm, s: Pick<ServerItem, 'base_url'> | null): boolean {
  return !!s && !sameAddress(f.baseURL, s.base_url)
}

/** privateNeedsOwner: core leaves a panel on a private network to the store
 * owner when it is new, newly marked private, or gets another address or
 * token (an admin could otherwise probe the internal network). Renaming,
 * reweighting or switching off one stays open to admins. */
export function privateNeedsOwner(
  f: Pick<ServerForm, 'allowPrivate' | 'baseURL' | 'token'>,
  editing: Pick<ServerItem, 'allow_private' | 'base_url'> | null,
): boolean {
  if (!f.allowPrivate) return false
  return !editing || !editing.allow_private || f.token.trim() !== '' || !sameAddress(f.baseURL, editing.base_url)
}

/** serverSummary sums up the overview's servers: how many take new services
 * (an older core does not say: all of them) and which of those do not
 * answer. A switched-off server is not checked, so its last result does not
 * count. */
export function serverSummary(list: readonly OverviewServer[]): { live: number; down: OverviewServer[] } {
  const live = list.filter((s) => s.enabled !== false)
  return { live: live.length, down: live.filter((s) => !s.healthy) }
}

/** A choice in the plan editor's server list. */
export interface ServerOption {
  value: string
  label: string
  /** takes no new services */
  off: boolean
  /** a server that no longer exists, still named by the plan */
  removed: boolean
}

/** serverOptions lists the servers to choose from, plus any the plan still
 * names that were removed meanwhile (so staff can take them out). */
export function serverOptions(servers: readonly ServerItem[], selected: readonly string[] = []): ServerOption[] {
  const out: ServerOption[] = servers.map((s) => ({ value: s.id, label: s.name, off: !s.enabled, removed: false }))
  for (const id of selected) {
    if (!servers.some((s) => s.id === id)) out.push({ value: id, label: id.slice(0, 8), off: true, removed: true })
  }
  return out
}

/** noUsableServer: a plan names servers, but none of them takes new
 * services, so its orders would wait for a server. */
export function noUsableServer(servers: readonly ServerItem[], selected: readonly string[]): boolean {
  if (!selected.length) return false
  return !selected.some((id) => servers.find((s) => s.id === id)?.enabled)
}

/** serverNames are the names of a plan's servers ('' for a removed one). */
export function serverNames(ids: readonly string[], servers: readonly ServerItem[]): string[] {
  return ids.map((id) => servers.find((s) => s.id === id)?.name ?? '')
}

/** noInbound: no inbound is both on in the panel and attached, so new
 * services on this server would fail. */
export function noInbound(list: readonly InboundItem[]): boolean {
  return !list.some((i) => i.enabled && i.attach)
}

/** A plan's inbound choice as the editor holds it: server id -> inbound ids
 * (a server without an entry, or with none, gets the default). */
export type InboundChoice = Record<string, number[]>

/** choiceOf reads the plan's inbounds into the editor's form. */
export function choiceOf(list: readonly PlanInbound[] | undefined): InboundChoice {
  const out: InboundChoice = {}
  for (const c of list ?? []) {
    if (c.inbound_ids?.length) out[c.server_id] = [...c.inbound_ids]
  }
  return out
}

/** inboundsBody is the list a save sends: the servers the plan can go to (all
 * of them while it names none) that have a choice. Choices for servers the
 * plan no longer names are dropped, as core refuses them. */
export function inboundsBody(choice: InboundChoice, planServers: readonly string[]): PlanInbound[] {
  return Object.entries(choice)
    .filter(([id, ids]) => ids.length > 0 && (!planServers.length || planServers.includes(id)))
    .map(([server_id, ids]) => ({ server_id, inbound_ids: [...ids].sort((a, b) => a - b) }))
    .sort((a, b) => a.server_id.localeCompare(b.server_id))
}

/** A choice in the plan editor's inbound list. */
export interface InboundOption {
  value: number
  label: string
  /** turned off in the panel: it is not given to new services */
  off: boolean
  /** the plan names it, but the panel no longer has it */
  gone: boolean
}

/** inboundOptions lists a server's inbounds to choose from, plus any the plan
 * still names that the panel no longer has (so staff can take them out). */
export function inboundOptions(list: readonly InboundItem[], chosen: readonly number[] = []): InboundOption[] {
  const out: InboundOption[] = list.map((i) => ({ value: i.id, label: i.tag || '#' + i.id, off: !i.enabled, gone: false }))
  for (const id of chosen) {
    if (!list.some((i) => i.id === id)) out.push({ value: id, label: '#' + id, off: true, gone: true })
  }
  return out
}

/** goneInbounds: the chosen inbounds of a server that its panel does not have
 * or has turned off (a service would get fewer, or none). */
export function goneInbounds(list: readonly InboundItem[], chosen: readonly number[]): number[] {
  return chosen.filter((id) => !list.some((i) => i.id === id && i.enabled))
}
