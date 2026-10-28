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
  /** the branded page's address (detail endpoints only; empty while it is off) */
  page_url?: string
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
  /** the cashback the order earned its buyer (only the one-order answer has it); `reversed` is what a refund took back */
  cashback?: { amount: Money; percent_bp: number; capped: boolean; at: number; reversed?: Money; reversed_at?: number } | null
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
  /** the panel inbounds a new service gets, per server; a server without an
   * entry gets every attachable inbound (the default). Older cores leave it out. */
  inbounds?: PlanInbound[]
  /** the plan group it is listed under (null: none). Older cores leave it out. */
  group_id?: string | null
  /** the discount code a paid purchase gives its buyer for any plan they buy next (null: none). Older cores leave it out. */
  reward?: PlanReward | null
}

/** A plan's reward: a percent or an amount in the plan's currency, the days the code lasts and the text the buyer reads with it. */
export interface PlanReward {
  kind: 'percent' | 'amount'
  percent?: number
  amount?: Money
  days: number
  text: Names
}

/** A named group of plans with a text the customer reads before choosing one of its plans. */
export interface PlanGroupItem {
  id: string
  name: Names
  description: Names
  sort: number
  /** how many plans are in it */
  plans: number
}

/** A plan's choice of inbounds on one server (panel inbound ids). */
export interface PlanInbound {
  server_id: string
  inbound_ids: number[]
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

/** A gift code in the list: never the code itself, only its last four
 * characters (core keeps a keyed hash of it). */
export interface GiftCodeItem {
  id: string
  tail: string
  /** made by BOBRES (true) or typed by staff (false) */
  generated: boolean
  amount: Money
  max_redemptions: number
  per_customer_limit: number
  redeemed: number
  expires_at: number | null
  enabled: boolean
  status: 'active' | 'disabled' | 'expired' | 'used_up'
  note: string
  created_at: number
  created_by: UserBrief | null
  last_redeemed_at: number | null
}

/** One customer's use of a gift code. */
export interface GiftRedemptionItem {
  id: string
  user: UserBrief
  /** the customer's n-th redemption of this code */
  seq: number
  amount: Money
  balance_after: Money
  created_at: number
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
  kind: 'text' | 'int' | 'bool' | 'enum' | 'tz' | 'time' | 'url' | 'channel' | 'color' | 'list'
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

/** A required channel or group (GET and PUT /join-chats). The first row is the
 * setting join.channel; the others are the extra chats. */
export interface JoinChatRow {
  chat: string
  link: string
  label: string
  primary: boolean
  /** customers are asked to join it under the license in effect */
  active: boolean
}

export interface JoinChatsReply {
  chats: JoinChatRow[]
  /** the first chat is set, so customers must join */
  on: boolean
  /** how many the license allows; -1 = no limit */
  limit: number
  used: number
  /** the most the list can hold, whatever the license says */
  max: number
  basis: string
  /** free, trial, standard or pro */
  tier: string
  /** this role may change the list (the owner) */
  editable: boolean
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
  /** The owner's own text of the store's Terms and Privacy pages ("" = the default); older servers leave it out. */
  legal?: Record<'terms' | 'privacy', LangPair>
  /** The default text of each page, to start from. */
  legal_defaults?: Record<'terms' | 'privacy', LangPair>
  logo: { url: string; type: string; size: number; updated_at: number } | null
  /** The bot's Telegram profile photo (a square JPEG); older servers leave it out. */
  bot_photo?: { url: string; size: number; updated_at: number } | null
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

/** The two kinds of help content: the questions under Support and the tutorials. */
export type HelpKind = 'faq' | 'tutorial'

/** A tutorial's URL button: an https address and a label per language. */
export interface HelpButton {
  url: string
  label: LangPair
}

/** One question or tutorial, as core stores it (GET /help). */
export interface HelpItem {
  id: string
  kind: HelpKind
  /** the question, or the tutorial's title: plain text on one line */
  title: LangPair
  /** the answer, or the tutorial's text: the limited HTML the bot's texts allow */
  body: LangPair
  buttons: HelpButton[]
  sort: number
  enabled: boolean
  /** the version read: sent back with an edit, so one made from an old copy is refused */
  version: number
  updated_at: number
}

/** What an entry is checked against, from core (the same numbers the bot applies). */
export interface HelpLimits {
  max_faq: number
  max_tutorials: number
  question: number
  tutorial_title: number
  body: number
  buttons: number
  button_label: number
  url: number
  one_message_entries: number
  one_message_units: number
}

/** How the bot shows the enabled FAQ in a language ("" when there is none). */
export type HelpLayout = '' | 'message' | 'list'

export interface HelpPage {
  faq: HelpItem[]
  tutorials: HelpItem[]
  faq_layout: Record<keyof LangPair, HelpLayout>
  limits: HelpLimits
  languages: string[]
}

/** Why core cannot save an entry: the field ("title.fa", "body.en", "buttons.0.url", "buttons.1.label.fa", "buttons"; "" for the whole entry), a code, and what it names. */
export interface HelpProblem {
  field: string
  code: string
  arg: string
}

/** What is written in the editor (and sent to core). */
export interface HelpDraft {
  kind: HelpKind
  enabled: boolean
  title: LangPair
  body: LangPair
  buttons: HelpButton[]
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
  method: 'link' | 'password' | 'password_initial'
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
  /** what their configs are called and grouped as in the 3x-ui panel: the prefix of the names
   * ("" until the reseller chooses it), the NAME of the group "BOBRES-NAME" and the group itself
   * ("" until the owner gives one), and whether the configs made before the group were moved into it.
   * Older cores leave them out. */
  config_prefix?: string
  panel_group?: string
  panel_group_full?: string
  groups_synced?: boolean
  /** this month against the owner's monthly targets (absent while they are off) */
  targets?: TargetsView
}

export interface TargetTierView {
  threshold: number
  bonus: number
  reached: boolean
  paid: boolean
}

/** A reseller's month against the owner's targets (core's targetsJSON): from and to are the Gregorian dates the month covers, ends_at unix seconds. */
export interface TargetsView {
  period: string
  calendar: 'gregorian' | 'jalali'
  from: string
  to: string
  ends_at: number
  sold: number
  tiers: TargetTierView[]
  remaining: number
  bonus_paid: number
  currency: string
  scale: number
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
  /** the panel software: xui | marzban; absent from older cores (xui) */
  kind?: string
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
 * merchant id, the proxy and the TronGrid API key are never sent back: only a
 * hint and whether set. */
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
  /** a proxy is stored (Zarinpal, IDPay or NextPay: for the gateway calls; automatic USDT: for TronGrid) */
  proxy_set: boolean
  /** usdt_trc20_auto: the TRON (TRC20) address that receives USDT, '' when
   * none (public: customers see it) */
  address?: string
  /** usdt_trc20_auto, idpay, nextpay: an API key is stored (the key is never sent back) */
  key_set?: boolean
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

/** USDT that reached the store's TRON address in the period but matched no
 * payment (another amount, or too late): nothing was credited. */
export interface UnmatchedDeposit {
  /** the transaction id (64 hex digits) */
  txid: string
  /** the transfer's place in its transaction, from 0 (one transaction may
   * carry more than one); with txid, what marks it handled */
  event_index: number
  /** the sender's TRON address */
  from: string
  amount: Money
  /** its block time, unix seconds */
  at: number
  /** why it matched nothing, in core's words (English); absent when not said */
  reason?: string
  /** why, as a code: no_payment | already_paid | closed | ambiguous | dust
   * (others may come) */
  reason_code?: string
}

/** GET /gateways/report: payments by status in a period, and the latest events. */
export interface GatewayReport extends GatewayHealth {
  provider: string
  from: number
  to: number
  /** by status: how many, and the sum charged per currency */
  totals: { status: string; count: number; amounts: Money[] }[]
  /** usdt_trc20_auto only: the deposits still to handle, whatever the
   * period (the newest, at most 50) */
  unmatched?: UnmatchedDeposit[]
  /** usdt_trc20_auto only: how many deposits are still to handle in all */
  unmatched_count?: number
  /** the failed payment checks (Zarinpal) kept in the period, newest first,
   * at most 20; absent from older cores */
  check_errors?: GatewayCheckError[] | null
  /** how many failed checks there are in the period (the list may hold
   * fewer) */
  check_error_count?: number
}

/** A failed payment check: payments could not ask the gateway about a
 * payment, or the gateway refused. Each payment's are kept at most once an
 * hour, so an outage leaves a trace without flooding the report. */
export interface GatewayCheckError {
  /** unix seconds */
  at: number
  /** the payment it was checking; '' when none */
  intent_id: string
  /** the error, already redacted (English) */
  detail: string
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
  /** the watchdog's open problems: empty when all is clear, null when core
   * could not read them; absent from older cores */
  alerts?: SystemAlert[] | null
}

/** One open problem core's watchdog found (core.alert_state): a check that
 * failed twice in a row. The bot tells staff on Telegram when one starts,
 * once a day while it lasts and when it is fixed. */
export interface SystemAlert {
  /** the check: service.bot, server.<id>, backup.stale... */
  key: string
  /** critical (customers are affected now) | warning (needs a look soon) */
  severity: string
  /** the bot's text for it (alert.backup_stale), named the same way here */
  title_key?: string
  /** its values, as strings: count, days, hours and minutes are whole
   * numbers, free is a size in bytes, date is unix seconds, anything else
   * (service, server) is a name */
  args?: Record<string, string | number> | null
  /** what is wrong in a few English words, when core gives it */
  title?: string
  /** a few English words from the check (an error), or '' */
  detail?: string
  /** when the problem was first seen: unix seconds (or an RFC 3339 time) */
  since: number | string
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
  /** '' | not_checked (not yet, since the bot started) | unreachable
   * (GitHub) | no_release | bad_signature | invalid | bot unreachable (core
   * could not ask the bot) | anything else, in a few English words */
  error: string
  /** the bot's short English reason for the error; '' when fine */
  detail?: string
  /** the release channel this install follows (BOBRES_UPDATE_CHANNEL):
   * stable | beta; '' or absent when the bot does not say */
  channel?: string
  /** when latest was signed (2006-01-02T15:04Z, UTC); absent when the bot
   * does not say */
  issued?: string
  /** ADVISORY: the installed license's update date (updates_until) is not
   * before the release's date. false only adds a note ("this release is newer
   * than your license's update date"): the update is offered and installed
   * all the same (security updates are never withheld). null when no license
   * is in force or the date is not known, absent from older cores */
  covered?: boolean | null
  /** latest is a pre-release (1.5.0-rc.1); core says so from the version it
   * checked (GET /system/updates only) */
  prerelease?: boolean
  /** where latest's notes are on GitHub, built by core from a release number
   * (GET /system/updates only; absent when latest is not one) */
  notes_url?: string
}

/** The server's updater: the root service on the host that runs the same
 * verified `bobres update` when core asks (docs/04-dashboard.md). Every
 * text in it comes from the updater and is shown as text, never as markup. */
export interface UpdateHelper {
  /** installed, and its status could be read */
  available: boolean
  /** why not: no_dir | no_status | not_installed | bad_status | unsupported; '' when available */
  problem: string
  /** the updater's own version */
  version: string
  /** idle | running | done | failed | rolled_back; '' when unknown */
  state: string
  /** the request being run, or the last one run */
  id: string
  from: string
  to: string
  channel: string
  /** verify | backup | pull | restart | health | rollback | done; '' when none */
  step: string
  /** one short sentence from the updater */
  message: string
  started_at: number | null
  finished_at: number | null
  /** the newest lines of the update's log: the owner's only */
  log?: string[]
  /** what the status is about: update | restore ('' for none; absent from an older core) */
  action?: string
  /** this updater can restore a backup (false for one from before that existed) */
  restore?: boolean
  /** a restore's backup (its file name, or the name an upload had), where it
   * came from (backup | upload), who asked and when */
  subject?: string
  source?: string
  by?: string
  requested_at?: number | null
}

/** An update, or a restore of a backup, asked for in the dashboard (core.system_updates). */
export interface UpdateRun {
  id: string
  /** the dashboard user who asked */
  by: string
  from: string
  to: string
  channel: string
  requested_at: number
  /** '' while it is open; then done | failed | rolled_back | not_started */
  outcome: string
  outcome_at: number | null
  message: string
  /** while open: queued (the updater has not taken it up) | running | done |
   * failed | rolled_back (the updater finished, core has not recorded it yet) */
  phase?: string
  /** update | restore (absent from an older core: an update) */
  action?: string
  /** a restore's backup and where it came from (backup | upload) */
  subject?: string
  source?: string
}

/** A backup the owner uploaded, waiting for the confirmation of its restore. */
export interface RestoreUpload {
  id: string
  /** the name it had on the owner's computer */
  name: string
  size: number
  sha256: string
  at: number
  expires_at: number
}

/** GET /system/restore: the restore part of the Backups tab. */
export interface RestoreState {
  version: string
  helper: UpdateHelper
  /** the restore core handed the updater and has not ended; null when none */
  run: UpdateRun | null
  /** the restore that ended last */
  last: UpdateRun | null
  /** "update" while an update is open (a restore waits for it) */
  other: string | null
  /** allowed: this role may; ready: a restore would be accepted now; reason: why
   * not (no_domain | helper_missing | restore_unsupported | already_running) */
  can: { allowed: boolean; ready: boolean; reason: string }
  /** what the owner types to confirm (the owner's only) */
  domain?: string
  /** the owner's only */
  upload: RestoreUpload | null
  limits: { max_upload: number; staged_seconds: number }
}

/** POST /system/backups/download-ticket */
export interface DownloadTicket {
  url: string
  expires_in: number
}

/** GET /system/updates/progress: the light state the page polls while an
 * update runs, and while core restarts in the middle of it. */
export interface UpdatesProgress {
  /** the version core runs */
  version: string
  helper: UpdateHelper
  run: UpdateRun | null
  last: UpdateRun | null
}

/** GET /system/updates: everything the Updates tab shows. */
export interface UpdatesState extends UpdatesProgress {
  check: UpdateCheck
  /** setting: what the owner chose here ('' none: the server's .env decides);
   * effective: the channel the bot follows now; source: setting | server */
  channel: { setting: string; effective: string; source: string }
  /** would an update be accepted now: allowed (this role may), ready (it
   * would), reason (why not: helper_missing | already_running |
   * check_unavailable | no_release | not_newer | dev_build), target (the release it
   * would install) */
  apply: { allowed: boolean; ready: boolean; reason: string; target: string }
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
  /** why it was not sent: too_big | no_owner | busy | blocked |
   * telegram_error[: …] | anything else, in English */
  telegram_error?: string
  /** the run failed: why, in English */
  error?: string
  /** when the encrypted file was copied to off-site storage (an
   * S3-compatible bucket); null or absent = not copied */
  uploaded_at?: number | string | null
  /** where it was copied: s3://bucket/key (what bobres restore takes) */
  uploaded_to?: string
  /** why the copy to off-site storage failed, in English (the backup itself
   * is kept on the server) */
  upload_error?: string
}

/** The backup service's off-site storage: an S3-compatible bucket each new
 * backup is copied to. Only where it is, never its keys. */
export interface BackupOffsite {
  /** each backup is copied (false when not set up, or when its settings
   * cannot be used: then error says why) */
  configured: boolean
  /** the storage's host name (s3.ir-thr-at1.arvanstorage.ir) */
  endpoint_host?: string
  bucket?: string
  /** '' or a folder ending in "/": each backup is prefix + its file name */
  prefix?: string
  /** how many backups the bucket keeps */
  keep?: number
  /** why settings that are there cannot be used, in English */
  error?: string
}

/** GET /system/backups (owner and admin): the backup service's state.
 * installed false: this install has no backup service; error "unreachable":
 * core could not ask it. */
export interface SystemBackups {
  /** true with the list; only false means not installed */
  installed?: boolean
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
  /** off-site storage; absent from older backup services */
  offsite?: BackupOffsite | null
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

/** What a license (or the trial, or the free tier) grants, each limit with
 * what this install uses. */
export interface LicenseEntitlements {
  white_label: boolean
  resellers: boolean
  crypto_watcher: boolean
  max_servers: LicenseLimit
  max_active_services: LicenseLimit
  /** staff besides the owner */
  dashboard_staff_seats: LicenseLimit
  /** the required channels and groups customers must join (absent from an older core) */
  max_join_chats?: LicenseLimit
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
  /** when the license itself ends, for a short token the license server
   * renews (expires_at is the token's own end); null when the token does
   * not say (no end date, or an older token) */
  license_ends_at?: number | null
}

/** What applies on this install now: a valid license, a license in its grace
 * period, the implicit 30-day trial, or the free tier ("free core, paid
 * premium"). */
export interface LicenseEffective {
  /** license | grace | trial (the implicit trial) | free */
  basis: string
  /** the tier to show: a license's (trial | standard | pro), "trial" for the
   * implicit trial, "free" for the free tier */
  tier: string
  trial_running: boolean
  /** when the implicit trial ends (or ended), unix seconds; null when not known */
  trial_ends_at: number | null
  /** whole days it has left; 0 when it is not running */
  trial_days_left: number
  /** how long the implicit trial lasts, in days (absent from an older core:
   * 30) */
  trial_days?: number
  /** whom to ask to buy or renew a license (the owner's BOBRES_SELLER_CONTACT);
   * '' when none is set: the page then says to ask the seller BOBRES came from */
  contact: string
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
  /** what applies now (GET /license and the answers that install a license;
   * absent from a check, which only reads a token) */
  effective?: LicenseEffective | null
  /** what applies now with this install's use (what the checked token would
   * grant, for a check) */
  entitlements?: LicenseEntitlements | null
  /** when it was saved, and by whom (GET /license only) */
  updated_at?: number | null
  updated_by?: { id: string; username: string } | null
  /** when the daily refresh last installed a renewed token from the license
   * server; null when it has not since the license was installed (GET
   * /license only, absent from older cores) */
  refreshed_at?: number | null
  /** how the token in use came: manual (pasted here or imported with
   * bobres license) | activation (with a license key) | refresh (renewed by
   * the license server since); null when none is installed */
  source?: string | null
}

/** A certificate as GET /system/tls describes it (times in unix seconds).
 * Never a key. */
export interface TlsCert {
  subject: string
  issuer: string
  dns_names: string[]
  ips: string[]
  not_before: number
  not_after: number
}

/** One address the store is served on, as it serves now. */
export interface TlsAddress {
  address: string
  /** domain | ip | zarinpal */
  role: string
  /** the certificate it should serve: public | internal | manual */
  expect: string
  /** Caddy answered the dial, whatever it then served; false on every
   * address means the gateway is down, stopped or restarting */
  reachable: boolean
  /** a certificate came back */
  served: boolean
  /** what this address should serve, and good now */
  valid: boolean
  name_ok: boolean
  /** publicly trusted for the address; null when unknown */
  trusted: boolean | null
  /** less than min(14 days, a quarter of its life) left */
  expiring: boolean
  days_left: number
  cert: TlsCert | null
  /** why it is not good (English); '' when it is */
  error: string
}

/** The tls-agent's state; null in the view when this install has none. */
export interface TlsAgent {
  generation: number
  /** the agent answered the current request */
  answered: boolean
  ok: boolean
  /** invalid | write_failed | caddy_refused | caddy_unreachable | agent_error */
  code: string
  error: string
  applied_at: number | null
}

/** A code and the same in English. */
export interface TlsNote {
  code: string
  message: string
}

/** GET /system/tls (system.read), and PUT's answer (which adds warnings and
 * pending, and has no live addresses). */
export interface TlsView {
  state: {
    mode: string
    ip: string
    cert: TlsCert | null
    updated_at: number | null
    domain: string
    zarinpal_host: string
    /** core has a master key, so a certificate of your own can be sealed */
    manual_allowed: boolean
  }
  /** what core asks for: the saved setting, or auto when problem says it cannot be used; null: nothing can be served */
  effective: { mode: string; ip: string } | null
  problem: TlsNote | null
  status: { checked_at: number | null; addresses: TlsAddress[]; agent: TlsAgent | null }
  public_url: string
  detected_ip: string | null
  /** PUT only */
  warnings?: TlsNote[]
  /** PUT only: Caddy was not running; it loads the setting when it starts */
  pending?: boolean
}

/** What the store charges for one USDT now (GET /usdt-rate): Toman per USDT, where it comes from, and the last live price read. */
export interface UsdtRate {
  auto: boolean
  margin: number
  manual: number
  rate: number
  source: 'live' | 'manual' | ''
  live: { rate: number; at: number; sources: string[]; fresh: boolean } | null
}
