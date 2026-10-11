// Shapes of core's /api/v1 answers (see internal/core/web).
import type { Money } from './format'

export interface UserBrief {
  id: string
  telegram_id: number
  username: string
}

export interface Page<T> {
  items: T[]
  total: number
  page: number
  size: number
}

export type Names = Record<string, string>

export interface PlanRef {
  id: string
  name: Names
}

export interface UserItem {
  id: string
  telegram_id: number
  username: string
  language: string
  role: string
  status: string
  created_at: number
  ref_code?: string
  wallets: Money[]
  subscriptions?: number
  orders?: number
}

export interface UserDetail {
  user: UserItem
  counts: { subscriptions: number; orders: number }
  referral: { referred_by: UserBrief | null; invited: number; rewarded: number; earned: Money[] }
  can_ban: boolean
}

export interface ServiceItem {
  id: string
  user: UserBrief
  plan: PlanRef
  status: string
  client_email: string
  sub_link: string
  expires_at: number | null
  traffic_total: number | null
  traffic_used: number
  last_synced_at: number | null
  created_at: number
  /** the name a reseller gave it (M5) */
  label?: string | null
}

export interface OrderItem {
  id: string
  user: UserBrief
  plan: PlanRef
  type: string
  status: string
  amount: Money
  discount: { code: string; amount: Money } | null
  subscription_id: string | null
  created_at: number
  updated_at: number
  attempts: number
  last_error: string
  next_attempt_at: number | null
  staff: string
  extend: { days: number | null; bytes: number | null } | null
  refund: { amount: Money; at: number } | null
}

export interface Proof {
  has_file: boolean
  reference: string
  network: string
  txid: string
  submitted_at: number
  decision: string
  reason: string
  reviewed_by: UserBrief | null
  reviewed_at: number | null
}

export interface PaymentItem {
  id: string
  order_id: string
  user: UserBrief | null
  provider: string
  status: string
  amount: Money
  gateway_amount: Money | null
  provider_ref: string
  failure_reason: string
  created_at: number
  proof: Proof | null
}

export interface PendingItem {
  id: string
  order_id: string
  user: UserBrief | null
  provider: string
  amount: Money
  reference: string
  network: string
  txid: string
  has_file: boolean
  submitted_at: number
  possible_duplicate: boolean
}

export interface LedgerItem {
  id: string
  user: UserBrief
  amount: Money
  balance_after: Money
  kind: string
  ref_type: string
  ref_id: string
  created_at: number
}

export interface PlanItem {
  id: string
  name: Names
  kind: string
  duration_days: number | null
  traffic_bytes: number | null
  price: Money
  enabled: boolean
  is_trial: boolean
  is_topup: boolean
  sort: number
  sales: number
  /** the 3x-ui servers its new services may go to; empty = any (M6).
   * Older cores leave it out. */
  server_ids?: string[]
}

export interface DiscountItem {
  code: string
  percent: number | null
  amount: Money | null
  max_uses: number | null
  used: number
  expires_at: number | null
  enabled: boolean
}

export interface ReferrerItem {
  user: UserBrief
  invited: number
  rewarded: number
  earned: Money[]
}

export interface AuditItem {
  id: string
  actor: UserBrief | null
  actor_role: string
  action: string
  entity: string
  entity_id: string
  before: unknown
  after: unknown
  reason: string
  ip: string
  source: string
  created_at: number
}

export interface SettingItem {
  key: string
  group: string
  kind: 'text' | 'int' | 'bool' | 'enum' | 'tz' | 'url' | 'channel' | 'color'
  value: string
  default: string
  options: string[]
  min: number | null
  max: number | null
  editable: boolean
}

export interface SettingsReply {
  items: SettingItem[]
  groups: string[]
}

export interface ChannelCheck {
  ok: boolean
  title?: string
  bot_admin?: boolean
  problem?: 'not_found' | 'not_admin' | 'not_channel' | 'error'
  detail?: string
}

export interface LangPair {
  fa: string
  en: string
}

export interface BrandingInfo {
  name: string
  support: string
  color: string
  terms_url: string
  privacy_url: string
  currency: LangPair
  logo: { url: string; type: string; size: number; updated_at: number } | null
  defaults: { name: string; color: string; currency: LangPair }
}

/** A problem a text override has; arg is the placeholder, tag or limit. */
export interface TextProblem {
  key?: string
  lang?: string
  code: string
  arg: string
}

export interface TextItem {
  key: string
  group: string
  context: string
  placeholders: string[]
  max: number
  langs: string[]
  default: LangPair
  override: LangPair
  /** false: a payment text, which only the store owner may change */
  editable?: boolean
}

export interface TextCheck {
  ok: boolean
  value: string
  problems: TextProblem[]
  length: number
  max: number
}

export interface StaffItem {
  id: string
  telegram_id: number
  username: string
  language: string
  role: 'owner' | 'admin' | 'support'
  status: string
  configured_owner: boolean
  me: boolean
  created_at: number
  password: { state: 'off' | 'pending' | 'on'; username: string; locked_until: number | null; failed_attempts: number }
  last_login: { at: number; method: string; ip: string } | null
  sessions: number
}

export interface SessionItem {
  id: string
  method: 'link' | 'password'
  ip: string
  user_agent: string
  created_at: number
  last_seen_at: number
  expires_at: number
  current: boolean
}

export interface TicketUser {
  id: string
  telegram_id: number
  username: string
  status: string
  language: string
  bot_blocked?: boolean
}

export interface TicketItem {
  id: string
  number: number
  status: 'open' | 'answered' | 'closed'
  category: string
  subject: string
  user: TicketUser
  assignee: UserBrief | null
  created_at: number
  updated_at: number
  last_customer_at: number | null
  last_staff_at: number | null
  closed_at: number | null
  close_reason: string
  last_message: { kind: string; preview: string; at: number } | null
}

export interface TicketMessageItem {
  id: string
  kind: 'customer' | 'staff' | 'note'
  author: UserBrief | null
  body: string
  file: { type: string; mime: string; name: string; size: number; url: string } | null
  delivery: { status: 'pending' | 'sent' | 'blocked' | 'failed'; at: number | null } | null
  created_at: number
}

export interface TicketDetail {
  ticket: TicketItem
  messages: TicketMessageItem[]
  can_manage: boolean
}

export interface TicketCounts {
  waiting: number
  mine: number
  unassigned: number
  answered: number
}

export interface CannedReply {
  id: string
  title: string
  body: LangPair
  sort: number
}

export interface BroadcastItem {
  id: string
  status: 'draft' | 'scheduled' | 'running' | 'paused' | 'done' | 'cancelled'
  segment: 'all' | 'active' | 'expired' | 'never_bought'
  language: '' | 'fa' | 'en'
  texts: LangPair
  button: { url: string; labels: LangPair } | null
  scheduled_at: number | null
  started_at: number | null
  finished_at: number | null
  counts: { total: number; pending: number; sent: number; blocked: number; failed: number; unknown: number }
  rate_per_sec: number
  eta_seconds: number | null
  last_error: string
  created_by: UserBrief
  created_at: number
  version: number
}

export interface AudienceCount {
  total: number
  languages: { fa: number; en: number }
}

/** A reseller's wallet in one currency, in minor units: a negative balance
 * is what they owe, down to -credit_limit. */
export interface ResellerBalance {
  currency: string
  balance: number
  credit_limit: number
}

/** A row of GET /resellers (owner and admin only). */
export interface ResellerItem {
  user: UserBrief & { status: string }
  tier: { id: string; name: string; discount_percent: number; commission_percent: number }
  /** the commission in force: the reseller's own, else the tier's */
  commission_percent: number
  commission_override: number | null
  balances: ResellerBalance[]
  /** customers who joined through the reseller's link */
  customers: number
  commission_earned: Money[]
  created_at: number
}

/** GET /resellers/{id} {item}: the row, the staff note, the version a
 * change must name, and whether removing is allowed (nothing owed). */
export interface ResellerDetail extends ResellerItem {
  note: string
  version: number
  can_remove: boolean
}

/** A reseller tier: a discount off store prices, or a fixed price per plan
 * (a decimal in the plan's currency), and the commission on customers. */
export interface TierItem {
  id: string
  name: string
  discount_percent: number
  commission_percent: number
  prices: { plan_id: string; price: string; currency?: string }[]
  /** resellers on this tier (it cannot be deleted while any are) */
  resellers: number
  version: number
}

export interface BroadcastRecipientItem {
  user: UserBrief
  status: string
  error: string
  done_at: number | null
}

/** A 3x-ui panel (GET /servers, owner and admin). Its token is never sent
 * back: has_token only says one is stored. */
export interface ServerItem {
  id: string
  name: string
  base_url: string
  /** where subscription links point; '' = the panel's own links */
  sub_base_url: string
  /** takes new services; its existing services keep working when off */
  enabled: boolean
  /** may reach private addresses (and plain http there) */
  allow_private: boolean
  /** 1 to 1000: new services go where clients ÷ weight is lowest */
  weight: number
  panel_version: string
  /** the version is outside the range BOBRES is tested with */
  version_untested?: boolean
  /** the last health check (meaningless while checked_at is null) */
  healthy: boolean
  /** why it failed, in the provisioner's words (English) */
  health_error: string
  /** null before the first check */
  checked_at: number | null
  /** services on this panel (deliveries in progress included) */
  clients: number
  has_token: boolean
  /** the plans that name this server */
  plans: PlanRef[]
}

/** GET /servers: the panels, and the panel versions BOBRES is tested with. */
export interface ServersReply {
  items: ServerItem[]
  tested_min_version?: string
  tested_max_version?: string
}

/** An inbound of a panel (GET /servers/{id}/inbounds). */
export interface InboundItem {
  id: number
  tag: string
  protocol?: string
  port?: number
  /** turned on in the panel itself */
  enabled: boolean
  /** new services join it; false = skipped (existing services keep theirs) */
  attach: boolean
}

/** POST /servers/test: whether core reached the panel (nothing is saved). */
export interface ServerTest {
  ok: boolean
  panel_version: string
  inbounds: number
  version_untested?: boolean
  /** why not, in the provisioner's words (English) */
  detail: string
}

/** What a gateway's payments say about it (from its report). */
export interface GatewayHealth {
  healthy: boolean
  /** payments started and not finished yet */
  open: number
  oldest_open_at: number | null
  last_paid_at: number | null
  last_error_at: number | null
  /** the last error, already redacted (English) */
  last_error: string
}

/** An automated gateway's configuration (GET /gateways, owner only). The
 * merchant id and proxy are never sent back: only a hint and whether set. */
export interface GatewayConfig {
  provider: string
  /** offered to customers */
  enabled: boolean
  /** has a merchant id (open payments are still checked when not offered) */
  configured: boolean
  /** new payments can use it now: enabled, configured and no problem */
  offered?: boolean
  /** why the settings cannot be used: '' | unreadable | sandbox_refused |
   * public_url | invalid */
  problem?: string
  sandbox: boolean
  /** may the sandbox be turned on (never in production); absent = yes */
  sandbox_allowed?: boolean
  /** the merchant id's last 4 characters, '' when none */
  merchant_hint: string
  proxy_set: boolean
  /** payments has no master key: the settings come from .env, read only */
  key_missing: boolean
  health?: GatewayHealth | null
}

/** GET /gateways: the automated gateways, and the payments settings (the
 * manual methods and Stars, their switches and details) as settings items. */
export interface GatewaysReply {
  providers: GatewayConfig[]
  settings: SettingItem[]
}

/** POST /gateways/{provider}/test: a read-only call to the gateway. */
export interface GatewayTest {
  ok: boolean
  /** the gateway's or core's words when it failed (English) */
  detail: string
  /** paid payments the gateway still waits to have verified */
  unverified?: number
}

/** GET /gateways/report: payments by status in a period, and the latest events. */
export interface GatewayReport extends GatewayHealth {
  provider: string
  from: number
  to: number
  /** by status: how many, and the sum charged per currency */
  totals: { status: string; count: number; amounts: Money[] }[]
}

/** A 3x-ui server on the overview (every staff member who reads it). */
export interface OverviewServer {
  id: string
  name: string
  /** its last check; a server not checked yet counts as answering */
  healthy: boolean
  /** why not, in English */
  detail: string
  clients: number
  /** takes new services; absent from older cores (then: yes) */
  enabled?: boolean
}

/** A payment method on the overview: offered or not, and whether it works. */
export interface OverviewGateway {
  provider: string
  enabled: boolean
  healthy: boolean
  /** false: on, but a detail it needs is missing (not shown to customers);
   * absent from older cores */
  ready?: boolean
}

/** One BOBRES service on the System page: its /healthz and /readyz as core
 * read them (each within 3 s). */
export interface ServiceHealth {
  /** core | bot | payments | provisioner | backup */
  name: string
  /** ok | not_ready (a readiness check failed) | down (no answer) |
   * not_installed (an optional service this install does not run) |
   * not_configured (core has no address for it) */
  status: string
  /** as the service reports it ("v1.2.3", "1.2.3", "dev"); '' when unknown */
  version: string
  /** the readiness checks: name → "ok" | "fail" */
  checks?: Record<string, string> | null
  /** why it is down or not ready, in a few English words */
  detail?: string
  latency_ms?: number
  /** its version differs from core's */
  version_mismatch?: boolean
}

/** GET /system (owner and admin): every service, and the summaries the
 * overview shows. */
export interface SystemReply {
  services: ServiceHealth[]
  /** core's own version, the one the others should match */
  version: string
  /** any service runs another version than core */
  version_mismatch?: boolean
  servers?: OverviewServer[]
  gateways?: OverviewGateway[]
  checked_at?: number
  /** the bot's cached update check; absent from older cores */
  updates?: UpdateCheck | null
}

/** What the bot last read from the latest release (it checks every 12 h;
 * core has no way out to the internet). */
export interface UpdateCheck {
  /** this install's version ("1.5.0") */
  current: string
  /** the latest release's version; '' when it could not be read */
  latest: string
  /** latest is newer than current */
  newer: boolean
  /** the release's signature was checked against the release key ring */
  verified: boolean
  /** when the bot last checked; 0 = not yet */
  checked_at: number
  /** '' | unreachable (GitHub) | bot unreachable (core could not ask the
   * bot) | anything else, in a few English words */
  error: string
}

/** One kept log line: a warning or an error, already redacted. */
export interface LogLine {
  time: number
  /** WARN | ERROR */
  level: string
  msg: string
  /** the line's other fields as a JSON object; '' or "{}" when none */
  attrs?: string
}

/** GET /system/logs?service= (owner): the warnings and errors a service
 * kept since it started (the newest 500), oldest first. */
export interface SystemLogs {
  service: string
  lines: LogLine[] | null
  /** '' | unreachable (core could not ask the service) | anything else, in
   * a few English words */
  error?: string
}

/** One kept backup (or a failed run) as the backup service's sidecar says. */
export interface BackupFile {
  file: string
  /** bytes; 0 for a failed run */
  size: number
  sha256: string
  /** unix seconds (an RFC 3339 time from a service answering directly) */
  created_at: number | string
  /** when the bot sent it to the owner on Telegram; null = not sent */
  sent_at: number | string | null
  /** why it was not sent: too_big | no_owner | busy | spool_failed |
   * blocked | telegram_error[: …] | anything else, in English */
  telegram_error?: string
  /** the run failed: why, in English */
  error?: string
}

/** GET /system/backups (owner and admin): the backup service's state.
 * installed false: this install has no backup service; error "unreachable":
 * core could not ask it. */
export interface SystemBackups {
  installed: boolean
  error?: string
  /** newest first */
  backups?: BackupFile[] | null
  /** a backup is being made now */
  running?: boolean
  /** why the last run failed; '' when it did not */
  last_error?: string
  /** unix seconds of the next daily run; 0 = not known */
  next_run?: number
  /** free bytes where the backups are kept */
  disk_free?: number
  /** that folder, inside the backup service */
  dir?: string
}

/** POST /system/backups/run (owner): 202 started, or 409 already running. */
export interface BackupRun {
  started: boolean
  reason?: string
}

/** A license limit with its use: -1 = unlimited, 0 = none; used is null
 * when it could not be counted. */
export interface LicenseLimit {
  limit: number
  used: number | null
  /** used is 90% of the limit or more */
  near?: boolean
  /** nothing more may be added */
  full?: boolean
}

/** What a license grants, each limit with what this install uses. */
export interface LicenseEntitlements {
  white_label: boolean
  resellers: boolean
  crypto_watcher: boolean
  max_servers: LicenseLimit
  max_active_services: LicenseLimit
  /** staff besides the owner */
  dashboard_staff_seats: LicenseLimit
  /** the payment methods it names, and those turned on now (the wallet,
   * always allowed, left out) */
  gateways: { licensed: string[]; enabled: string[] }
  /** YYYY-MM-DD: newer releases are not installable; '' = no limit */
  updates_until?: string
}

/** The contents of a license token whose signature checked out. */
export interface LicenseToken {
  license_id: string
  customer: string
  /** trial | standard | pro */
  tier: string
  /** the vendor key that signed it */
  kid: string
  issued_at: number
  expires_at: number
  grace_days: number
  grace_ends_at?: number
  /** what it is bound to ('' = not bound) */
  domain: string
  install_id: string
}

/** GET /license (owner and admin; never the token itself), and the answer of
 * POST /license/check and PUT /license. */
export interface LicenseInfo {
  /** missing | invalid | active | grace | expired */
  state: string
  /** why it is invalid: malformed, bad_signature, unknown_key, version,
   * domain, install_id, unbound, not_yet_valid ('' otherwise) */
  reason: string
  /** this install: its id and its BOBRES_DOMAIN */
  install_id: string
  domain: string
  /** the vendor keys built into this core (0: no license can verify yet) */
  keys?: number
  /** null when there is none, or its signature was not accepted */
  license: LicenseToken | null
  /** the token's binding against this install */
  binding?: { domain: string; install_id: string } | null
  entitlements?: LicenseEntitlements | null
  /** when it was saved, and by whom (GET /license only) */
  updated_at?: number | null
  updated_by?: { id: string; username: string } | null
}
