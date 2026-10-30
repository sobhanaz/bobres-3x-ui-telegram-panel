package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
)

// Setting kinds: how a value is checked and stored.
const (
	KindText    = "text"
	KindInt     = "int"
	KindBool    = "bool"
	KindEnum    = "enum"
	KindTZ      = "tz"
	KindTime    = "time" // a time of day, HH:MM
	KindURL     = "url"
	KindChannel = "channel"
	KindColor   = "color"
	// KindList is a list of rows kept as compact JSON text (the reseller targets' tiers, the wheel's prizes): the dashboard has an editor for it,
	// the bot's admin menu leaves it out.
	KindList = "list"
)

// Dashboard permissions a setting needs (the web package's names).
const (
	permSettings  = "settings.write"
	permGateways  = "gateways.write" // owner only: where customers' money goes
	permSystem    = "system.write"   // owner only: how the server itself runs (its updates)
	permBranding  = "branding.write"
	permDiscounts = "discounts.write"
	// permBroadcasts: messages to the resellers (the weekly settlement reminder) are broadcasts.
	permBroadcasts = "broadcasts.write"
	// permSecurity is how staff sign in (the approval of sign-ins): the owner's, changed in the dashboard with the owner signed in (or on
	// the server with bobres admin approval), never in the bot.
	permSecurity = "security.write"
)

// The layouts of the bot's plan lists (SettingPlanLayout): a table puts each
// plan on a row of two buttons (its name and its price) under a header row;
// a list has one button per plan.
const (
	SettingPlanLayout = "bot.plan_layout"
	PlanLayoutTable   = "table"
	PlanLayoutList    = "list"
)

// ownerOnlyPerm reports whether a setting's permission is held by the owner
// alone (the web package's ownerOnly: gateways.write and system.write), so an
// admin neither sees nor changes it, in the dashboard or the bot.
func ownerOnlyPerm(perm string) bool {
	return perm == permGateways || perm == permSystem || perm == permSecurity
}

// LegalTextMax is the longest text (characters) the owner may write for a Terms or Privacy page.
const LegalTextMax = 30000

// SettingSpec describes one setting: where it is edited, who may change it
// and what values it takes. Every value is stored as a JSON string; "" (or
// no row) means the default.
type SettingSpec struct {
	Key     string
	Group   string
	Kind    string
	Perm    string
	Default string
	Min     int64 // KindInt
	Max     int64 // KindInt
	Options []string
	MaxLen  int // KindText, in characters
	// Multiline lets a KindText hold line breaks and tabs (a page's text); its limit in bytes is MaxLen * 4. Line breaks are kept as \n.
	Multiline bool
	Help      string
}

// SettingSpecs lists every setting in the order the dashboard shows them.
// Bot texts (texts.<lang>.<key>) are settings too, checked by the bot's
// catalog instead of a spec.
var SettingSpecs = []SettingSpec{
	{Key: "general.timezone", Group: "general", Kind: KindTZ, Perm: permSettings,
		Help: "time zone of the dates the bot shows, e.g. Asia/Tehran (empty = the server's BOBRES_TIMEZONE)"},
	{Key: "general.bot_profile", Group: "general", Kind: KindBool, Perm: permSettings, Default: "true",
		Help: "true = the bot keeps its Telegram profile (menu commands, description, short description) in Persian and English from its texts, and its profile photo from the Branding page; false = the bot takes back its Persian and English texts and the staff menus (the photo stays), and you set them in @BotFather"},
	{Key: "branding.sub_page.enabled", Group: "general", Kind: KindBool, Perm: permBranding, Default: "true",
		Help: "true = every service has a branded page at /sub/<token> (its link, QR code and app guides); false = those pages answer 404 (the links in the bot and dashboard are hidden too)"},
	{Key: "branding.bot_username", Group: "general", Kind: KindText, Perm: permBranding, MaxLen: 33,
		Help: "the bot's @username: the renew button of the subscription page opens https://t.me/<username> (empty = no renew button)"},
	{Key: "miniapp.enabled", Group: "general", Kind: KindBool, Perm: permSettings, Default: "true",
		Help: "true = the Telegram Mini App store is open at /app/ (the whole store inside Telegram: plans, buying, renewing, wallet, support, invites and the reseller panel) and the bot opens it from a Store button and Telegram's menu button; false = /app/ and its API answer 404 and the buttons go. It needs the store's public HTTPS address, see docs/17-mini-app.md"},
	{Key: SettingGiftCodes, Group: "general", Kind: KindBool, Perm: permDiscounts, Default: "true",
		Help: "true = customers can apply a gift code (a code the store made, worth a fixed amount of wallet credit) from the wallet screen in the bot; false = that button goes and no code is accepted (codes can still be made in the dashboard). Needs the master key"},
	{Key: "maintenance.enabled", Group: "maintenance", Kind: KindBool, Perm: permSettings, Default: "false",
		Help: "true = customers only get the maintenance message; staff and payments in progress still work"},
	{Key: "join.channel", Group: "join", Kind: KindChannel, Perm: permSettings,
		Help: "the first channel or group customers must join to use the bot: @name or -100… id (the bot must be its admin; empty = off, the extra required chats too)"},
	{Key: "join.link", Group: "join", Kind: KindURL, Perm: permSettings,
		Help: "https://t.me/… link of the join button (needed for a private channel)"},
	{Key: "join.label", Group: "join", Kind: KindText, Perm: permSettings, MaxLen: JoinLabelMax,
		Help: "short name of this channel on the join button when customers must join several (empty = its @name)"},
	{Key: "notify.new_payment", Group: "notify", Kind: KindBool, Perm: permSettings, Default: "true",
		Help: "message staff when a payment needs review"},
	{Key: "notify.new_ticket", Group: "notify", Kind: KindBool, Perm: permSettings, Default: "true",
		Help: "message staff when a customer writes to support"},
	{Key: "notify.provision_failed", Group: "notify", Kind: KindBool, Perm: permSettings, Default: "true",
		Help: "message staff when a service could not be created on the panel"},
	{Key: "notify.system_alerts", Group: "notify", Kind: KindBool, Perm: permSettings, Default: "true",
		Help: "message staff when a service, a 3x-ui server, Zarinpal, automatic USDT, backups (and their off-site copies), the disk, deliveries, the bot's event feed or the license has a problem, and when it is fixed"},
	{Key: "notify.sales", Group: "notify", Kind: KindBool, Perm: permSettings, Default: "false",
		Help: "message staff on every paid order"},
	{Key: "notify.recipients", Group: "notify", Kind: KindEnum, Perm: permSettings, Default: "owner",
		Options: []string{"owner", "staff"}, Help: "owner = the server's admin Telegram account; staff = every owner and admin (and support staff for ticket alerts)"},
	{Key: SettingDailySummary, Group: "reports", Kind: KindBool, Perm: permSettings, Default: "false",
		Help: "true = every day at the time below, the bot messages yesterday's sales (revenue per currency, paid orders, refunds, top-ups, new customers, free trials) to every owner and admin; off by default"},
	{Key: SettingDailySummaryTime, Group: "reports", Kind: KindTime, Perm: permSettings, Default: DefaultDailySummaryTime,
		Help: "time of day (HH:MM, 24-hour) in the store's time zone at which the daily sales summary is sent"},
	{Key: SettingPlanLayout, Group: "shop", Kind: KindEnum, Perm: permSettings, Default: PlanLayoutTable, Options: []string{PlanLayoutTable, PlanLayoutList},
		Help: "how the bot lists plans: table = each plan is a row of two buttons (its name and its price) under a header row; list = one button per plan"},
	{Key: SettingVendorEnabled, Group: "shop", Kind: KindBool, Perm: permSettings, Default: "false",
		Help: "true = the bot sells BOBRES licenses (the owner's own store only: it needs the license server's address and admin key, set in the dashboard); false = it does not"},
	{Key: SettingSupplierEnabled, Group: "shop", Kind: KindBool, Perm: permSettings, Default: "false",
		Help: "true = the bot has a Digital accounts button: the owner's own store resells a supplier's accounts, paid from the wallet in Toman (it needs the supplier's API key and the products switched on in the dashboard); false = no button, and core refuses the purchase"},
	{Key: SettingBulkEnabled, Group: "shop", Kind: KindBool, Perm: permSettings, Default: "true",
		Help: "true = the bot's home menu has a Bulk purchase button: a customer buys several services of one plan at once and pays from the wallet; false = no button, and core refuses bulk purchases"},
	{Key: SettingBulkMax, Group: "shop", Kind: KindInt, Perm: permSettings, Default: strconv.Itoa(DefaultBulkMax), Min: MinBulkQuantity, Max: MaxBulkQuantity,
		Help: "the most services one bulk purchase may hold (2 to 100)"},
	{Key: "support.autoclose_days", Group: "support", Kind: KindInt, Perm: permSettings, Default: "7", Min: 0, Max: 90,
		Help: "close a ticket this many days after staff answered, when the customer does not write back (0 = never)"},
	{Key: "limits.bot_per_10s", Group: "limits", Kind: KindInt, Perm: permSettings, Default: "20", Min: 5, Max: 200,
		Help: "messages and taps a customer may send in 10 seconds"},
	{Key: "limits.tickets_per_day", Group: "limits", Kind: KindInt, Perm: permSettings, Default: "10", Min: 0, Max: 100,
		Help: "support messages a customer may send in a day (0 = no limit)"},
	{Key: "limits.broadcast_per_sec", Group: "limits", Kind: KindInt, Perm: permSettings, Default: "20", Min: 1, Max: 25,
		Help: "broadcast messages sent per second (Telegram allows about 30 in all; replies to customers go first)"},
	{Key: "payments.manual_card.enabled", Group: "payments", Kind: KindBool, Perm: permGateways, Default: "true",
		Help: "offer card-to-card payments (they also need the card number)"},
	{Key: "payments.card_number", Group: "payments", Kind: KindText, Perm: permGateways, MaxLen: 40,
		Help: "card number shown for card-to-card payments (16 digits, or an IR sheba)"},
	{Key: "payments.card_holder", Group: "payments", Kind: KindText, Perm: permGateways, MaxLen: 64,
		Help: "card holder name"},
	{Key: "payments.manual_crypto.enabled", Group: "payments", Kind: KindBool, Perm: permGateways, Default: "true",
		Help: "offer USDT payments (they also need a deposit address)"},
	{Key: "payments.usdt_trc20", Group: "payments", Kind: KindText, Perm: permGateways, MaxLen: 34,
		Help: "USDT TRC20 deposit address"},
	{Key: "payments.usdt_erc20", Group: "payments", Kind: KindText, Perm: permGateways, MaxLen: 42,
		Help: "USDT ERC20 deposit address"},
	{Key: SettingUSDTAuto, Group: "payments", Kind: KindBool, Perm: permGateways, Default: "true",
		Help: "true = the Toman price of one USDT is read live (every couple of minutes) and Toman prices are turned into USDT at that rate, so a crypto payment is always priced at the moment; the rate below is the fallback when the live price cannot be read; false = only the rate below is used"},
	{Key: SettingUSDTMargin, Group: "payments", Kind: KindInt, Perm: permGateways, Min: 0, Max: MaxUSDTMargin,
		Help: "percent added on top of the live USDT rate (0 to 30), to cover the exchange's spread and fees (empty or 0 = none)"},
	{Key: "payments.usdt_rate", Group: "payments", Kind: KindInt, Perm: permGateways, Min: 1, Max: 1_000_000_000_000,
		Help: "Toman per 1 USDT, to quote Toman prices in USDT"},
	{Key: "payments.stars.enabled", Group: "payments", Kind: KindBool, Perm: permGateways, Default: "true",
		Help: "offer Telegram Stars (it also needs the Stars rate)"},
	{Key: "payments.stars_rate", Group: "payments", Kind: KindInt, Perm: permGateways, Min: 1, Max: 1_000_000_000,
		Help: "Toman per Telegram Star, to price plans in Stars (empty = no Stars)"},
	{Key: "payments.manual_zarinpal.enabled", Group: "payments", Kind: KindBool, Perm: permGateways, Default: "true",
		Help: "offer payments through your Zarinpal link, approved by hand (they also need the link)"},
	{Key: "payments.zarinpal_link", Group: "payments", Kind: KindURL, Perm: permGateways,
		Help: "your Zarinpal payment link (https://zarinp.al/...); customers pay there and send the receipt screenshot for approval"},
	{Key: "referral.reward_percent", Group: "referral", Kind: KindInt, Perm: permDiscounts, Min: 0, Max: 100,
		Help: "percent of an invited user's first purchase credited to the inviter's wallet (empty or 0 = no referral program)"},
	{Key: SettingLeaderboard, Group: "growth", Kind: KindBool, Perm: permSettings, Default: "true",
		Help: "true = while the referral program is on, customers see a Top users board in the bot: the customers who invited the most people, with their names and Telegram ids masked (never staff, resellers or banned users); false = the Top users button and the board are gone"},
	// Cashback (cashback.go): a share of a delivered order given back to the buyer's wallet. Off by default.
	{Key: SettingCashbackEnabled, Group: "cashback", Kind: KindBool, Perm: permDiscounts, Default: "false",
		Help: "true = when a customer's order has been paid and delivered, a share of what it cost is credited to their wallet as cashback, once; false = no cashback (cashback already paid stays in the wallets)"},
	{Key: SettingCashbackPercent, Group: "cashback", Kind: KindText, Perm: permDiscounts, MaxLen: 8,
		Help: "the share of the order's price given back, from 0.01 to 20 percent, for example 2.5 (worked out in whole Toman, rounded down); empty = no cashback"},
	{Key: SettingCashbackMax, Group: "cashback", Kind: KindInt, Perm: permDiscounts, Min: 0, Max: MaxGrowthAmount,
		Help: "the most cashback one order earns, in Toman (empty or 0 = no maximum)"},
	{Key: SettingCashbackMinOrder, Group: "cashback", Kind: KindInt, Perm: permDiscounts, Min: 0, Max: MaxGrowthAmount,
		Help: "an order must cost at least this many Toman to earn cashback (empty or 0 = any order that costs something)"},
	{Key: SettingCashbackRenewals, Group: "cashback", Kind: KindBool, Perm: permDiscounts, Default: "true",
		Help: "true = renewals and traffic packages earn cashback too; false = only new services do"},
	// Reseller monthly targets (targets.go): a bonus for the services a reseller sells in a month. Off by default.
	{Key: SettingTargetsEnabled, Group: "targets", Kind: KindBool, Perm: permDiscounts, Default: "false",
		Help: "true = a reseller who sells enough services in a month is paid a bonus for each tier they reach, once for that month; false = no bonuses (what was paid stays in the wallets). Sales are counted from the moment it is switched on"},
	{Key: SettingTargetsTiers, Group: "targets", Kind: KindList, Perm: permDiscounts,
		Help: "the tiers: how many new services a reseller sells in a month and the bonus in Toman for reaching it, for example [{\"threshold\":10,\"bonus\":50000}]; each tier pays its own bonus on top of the lower tiers' (at most 10 tiers)"},
	{Key: SettingTargetsCalendar, Group: "targets", Kind: KindEnum, Perm: permDiscounts, Default: CalendarGregorian, Options: []string{CalendarGregorian, CalendarJalali},
		Help: "the calendar a month is read in: gregorian (1 to the end of the month) or jalali (Persian months); change it between months, not in the middle of one"},
	// The daily wheel of fortune (wheel.go): one free spin a day for customers who have paid orders. Off by default, and closed until it has a budget.
	{Key: SettingWheelEnabled, Group: "wheel", Kind: KindBool, Perm: permDiscounts, Default: "false",
		Help: "true = customers with enough paid orders get one free spin of the wheel a day, for a chance at wallet credit; false = no wheel (prizes already paid stay in the wallets). It also needs prizes and a daily budget"},
	{Key: SettingWheelPrizes, Group: "wheel", Kind: KindList, Perm: permDiscounts,
		Help: "the prizes: each an amount in Toman and a weight, its share of the chances, for example [{\"amount\":10000,\"weight\":10}] (at most 12 prizes, no amount twice)"},
	{Key: SettingWheelNoPrize, Group: "wheel", Kind: KindInt, Perm: permDiscounts, Min: 0, Max: MaxWheelNoPrizeWeight,
		Help: "the weight of the segment that pays nothing: the chance of no prize is this out of the sum of all the weights (empty or 0 = every spin wins something)"},
	{Key: SettingWheelBudget, Group: "wheel", Kind: KindInt, Perm: permDiscounts, Min: 0, Max: MaxGrowthAmount,
		Help: "the most Toman the wheel pays out in one day (store time); when it cannot cover a prize the customer is asked to come back tomorrow and their spin is not used (empty or 0 = the wheel is closed)"},
	{Key: SettingWheelMinOrders, Group: "wheel", Kind: KindInt, Perm: permDiscounts, Min: 1, Max: MaxWheelMinOrders, Default: "1",
		Help: "how many paid orders a customer needs before they may spin, at least 1"},
	{Key: SettingReminderEnabled, Group: "reseller_reminder", Kind: KindBool, Perm: permBroadcasts, Default: "false",
		Help: "true = once a week, at the weekday and time below (the store's time zone), the resellers get a reminder to settle what they owe, in their own bot language; false = no reminder (the default). Switching it on after this week's time starts next week; a week missed while the server was down is not made up"},
	{Key: SettingReminderWeekday, Group: "reseller_reminder", Kind: KindEnum, Perm: permBroadcasts, Default: DefaultReminderWeekday, Options: ReminderWeekdays,
		Help: "the day of the week the reminder goes out"},
	{Key: SettingReminderTime, Group: "reseller_reminder", Kind: KindTime, Perm: permBroadcasts, Default: DefaultReminderTime,
		Help: "the time of day (HH:MM, 24-hour, the store's time zone) the reminder goes out"},
	{Key: SettingReminderDebtOnly, Group: "reseller_reminder", Kind: KindBool, Perm: permBroadcasts, Default: "true",
		Help: "true = only the resellers who owe money (a negative balance) get the reminder; false = every active reseller does"},
	{Key: SettingReminderSettle, Group: "reseller_reminder", Kind: KindBool, Perm: permBroadcasts, Default: "true",
		Help: "true = a Settle / Top up button under the reminder opens the reseller's wallet in the bot"},
	{Key: SettingReminderText + "fa", Group: "reseller_reminder", Kind: KindText, Perm: permBroadcasts, MaxLen: reminderTextMax, Multiline: true,
		Help: "the reminder in Persian (Telegram HTML); {name}, {debt}, {balance}, {credit_limit}, {tier} and {customers} are filled in for each reseller (empty = the built-in text)"},
	{Key: SettingReminderText + "en", Group: "reseller_reminder", Kind: KindText, Perm: permBroadcasts, MaxLen: reminderTextMax, Multiline: true,
		Help: "the reminder in English (Telegram HTML); {name}, {debt}, {balance}, {credit_limit}, {tier} and {customers} are filled in for each reseller (empty = the built-in text)"},
	// New product announcements (announce.go): a card with a Buy button in the store's channel and/or in private messages to the customers who
	// said yes to product news. Off by default; staff send each one by hand from the product's page.
	{Key: SettingAnnounceEnabled, Group: "announce", Kind: KindBool, Perm: permBroadcasts, Default: "false",
		Help: "true = staff can announce a product (a plan, a digital account, a BOBRES license) from its page, in the store's channel and/or in private messages, and the bot asks each customer once whether they want news of new products; false = no announcements and no question (the default)"},
	{Key: SettingAnnounceChannel, Group: "announce", Kind: KindChannel, Perm: permBroadcasts,
		Help: "the channel announcements are posted in: @name or -100… id; the bot must be its admin, allowed to post (empty = the first required channel, join.channel)"},
	{Key: SettingAnnounceChannelLangs, Group: "announce", Kind: KindEnum, Perm: permBroadcasts, Default: AnnounceLangsDefault,
		Options: []string{AnnounceLangsDefault, AnnounceLangsBoth},
		Help:    "the language of a channel post: default = the store's default language (Persian); both = every language, one under the other (private messages are always in each customer's own language)"},
	{Key: SettingAnnounceDailyMax, Group: "announce", Kind: KindInt, Perm: permBroadcasts, Default: strconv.Itoa(DefaultAnnounceDailyMax), Min: 1, Max: 50,
		Help: "the most announcements that go out in one day (the store's time zone), 1 to 50"},
	{Key: SettingAnnounceQuietStart, Group: "announce", Kind: KindTime, Perm: permBroadcasts, Default: DefaultAnnounceQuietStart,
		Help: "quiet hours start (HH:MM, the store's time zone): an announcement sent in the quiet hours waits until they end (the same start and end = no quiet hours)"},
	{Key: SettingAnnounceQuietEnd, Group: "announce", Kind: KindTime, Perm: permBroadcasts, Default: DefaultAnnounceQuietEnd,
		Help: "quiet hours end (HH:MM, the store's time zone): queued announcements go out then"},
	{Key: "branding.name", Group: "branding", Kind: KindText, Perm: permBranding, MaxLen: 64,
		Help: "store name shown to users"},
	{Key: "branding.support", Group: "branding", Kind: KindText, Perm: permBranding, MaxLen: 64,
		Help: "support contact (e.g. @support)"},
	{Key: "branding.color", Group: "branding", Kind: KindColor, Perm: permBranding,
		Help: "brand colour of the dashboard, #rrggbb"},
	{Key: "branding.terms_url", Group: "branding", Kind: KindURL, Perm: permBranding,
		Help: "https link to the store's terms"},
	{Key: "branding.privacy_url", Group: "branding", Kind: KindURL, Perm: permBranding,
		Help: "https link to the store's privacy policy"},
	{Key: "legal.terms.fa", Group: "branding", Kind: KindText, Perm: permBranding, MaxLen: LegalTextMax, Multiline: true,
		Help: "your own Persian text of the store's terms page (/terms); empty = the default text"},
	{Key: "legal.terms.en", Group: "branding", Kind: KindText, Perm: permBranding, MaxLen: LegalTextMax, Multiline: true,
		Help: "your own English text of the store's terms page (/terms); empty = the default text"},
	{Key: "legal.privacy.fa", Group: "branding", Kind: KindText, Perm: permBranding, MaxLen: LegalTextMax, Multiline: true,
		Help: "your own Persian text of the store's privacy page (/privacy); empty = the default text"},
	{Key: "legal.privacy.en", Group: "branding", Kind: KindText, Perm: permBranding, MaxLen: LegalTextMax, Multiline: true,
		Help: "your own English text of the store's privacy page (/privacy); empty = the default text"},
	{Key: "branding.currency.fa", Group: "branding", Kind: KindText, Perm: permBranding, MaxLen: 16,
		Help: "Persian name of the Toman in prices (empty = تومان)"},
	{Key: "branding.currency.en", Group: "branding", Kind: KindText, Perm: permBranding, MaxLen: 16,
		Help: "English name of the Toman in prices (empty = Toman)"},
	// Monitoring (System > Monitoring, docs/04-dashboard.md): whether core samples the host and the stack
	// and raises the monitoring alerts, and the thresholds of those alerts.
	{Key: SettingMonitorEnabled, Group: "monitor", Kind: KindBool, Perm: permSettings, Default: "true",
		Help: "true = core records the server's and the stack's figures every 30 seconds for the Monitoring page and raises its alerts (a full disk, memory, a container that is down, unhealthy or restarting, a stopped collector, a failing CI); false = it records and alerts nothing (the history stays readable)"},
	{Key: SettingMonitorDiskPct, Group: "monitor", Kind: KindInt, Perm: permSettings, Default: "85", Min: 50, Max: 99,
		Help: "percent full a disk (BOBRES's or Docker's) may be before a monitoring alert is raised"},
	{Key: SettingMonitorMemPct, Group: "monitor", Kind: KindInt, Perm: permSettings, Default: "90", Min: 50, Max: 99,
		Help: "percent of memory in use that, for the number of minutes below, raises a monitoring alert"},
	{Key: SettingMonitorMemMinutes, Group: "monitor", Kind: KindInt, Perm: permSettings, Default: "10", Min: 1, Max: 120,
		Help: "minutes memory must stay above the percent before the alert is raised"},
	{Key: SettingMonitorRestarts, Group: "monitor", Kind: KindInt, Perm: permSettings, Default: "3", Min: 2, Max: 50,
		Help: "restarts of one container within 15 minutes that raise a monitoring alert (a crash loop)"},
	{Key: SettingMonitorCollectorMinutes, Group: "monitor", Kind: KindInt, Perm: permSettings, Default: "5", Min: 2, Max: 60,
		Help: "minutes without a snapshot from the host collector before a monitoring alert is raised (only where the collector is installed)"},
	{Key: SettingMonitorCIAlert, Group: "monitor", Kind: KindBool, Perm: permSettings, Default: "false",
		Help: "true = a monitoring alert is raised while the latest CI run on the repository's main branch has failed (for a store built from main; off for a store that runs releases)"},
	// How staff sign in (Settings > Sign-in security, the owner only, in the dashboard: domain/loginapproval.go). They are not on the generic
	// Settings form and not in the bot's /set: turning them on needs the owner signed in, their authenticator code and a confirmation.
	{Key: SettingLoginApproval, Group: "security", Kind: KindBool, Perm: permSecurity, Default: "false",
		Help: "true = a staff sign-in to the dashboard (a login link, or a password with its authenticator code) opens a session only after the staff member, or an owner, approves it in Telegram within 5 minutes; false = it opens at once"},
	{Key: SettingLoginApprovalNotifyOwner, Group: "security", Kind: KindBool, Perm: permSecurity, Default: "true",
		Help: "true = the owners are asked to approve the sign-in of a staff member who is not an owner as well; false = only that staff member is"},
	{Key: SettingStatusPage, Group: "monitor", Kind: KindBool, Perm: permSettings, Default: "false",
		Help: "true = the store has a public status page at /status (and /status.json for a monitoring service) that says whether the dashboard, the bot, payments and the panels work, in general words only: no server names, versions or numbers; false = both addresses answer 404 (off by default)"},
	// The Updates tab edits it (System > Updates, the owner only); it is not on the Settings page.
	{Key: release.SettingChannel, Group: "updates", Kind: KindEnum, Perm: permSystem, Options: []string{release.ChannelStable, release.ChannelBeta},
		Help: "the release channel updates follow: stable = the newest stable release, beta = the newest of all releases, pre-releases included (empty = the server's BOBRES_UPDATE_CHANNEL, stable when unset)"},
}

// SettingKeys maps each setting to its help text (the bot's /set list).
var SettingKeys = func() map[string]string {
	m := make(map[string]string, len(SettingSpecs))
	for _, sp := range SettingSpecs {
		m[sp.Key] = sp.Help
	}
	return m
}()

// SpecOf finds a setting's spec.
func SpecOf(key string) (SettingSpec, bool) {
	for _, sp := range SettingSpecs {
		if sp.Key == key {
			return sp, true
		}
	}
	return SettingSpec{}, false
}

// SettingPerm is the dashboard permission needed to change key; "" when key
// is not a setting.
func SettingPerm(key string) string {
	if sp, ok := SpecOf(key); ok {
		return sp.Perm
	}
	if lang, k, ok := i18n.SplitTextKey(key); ok && slices.Contains(i18n.Languages, lang) {
		return TextPerm(k)
	}
	return ""
}

// TextPerm is the permission needed to change a bot text: the payment
// screens (where customers are told where to send money) are the owner's,
// like the payment details themselves; "" for an unknown text.
func TextPerm(key string) string {
	info, ok := Texts().Info(key)
	switch {
	case !ok:
		return ""
	case info.Group == "pay":
		return permGateways
	}
	return permBranding
}

// Texts is the bot's text catalog, which core uses to check overrides.
var Texts = sync.OnceValue(i18n.MustLoad)

const maxSettingLen = 1000

// FieldError is a value a setting does not take. It is an ErrInvalid.
type FieldError struct {
	Field string
	Msg   string
}

func (e *FieldError) Error() string { return ErrInvalid.Error() + ": " + e.Field + ": " + e.Msg }
func (e *FieldError) Unwrap() error { return ErrInvalid }

// TextError is a bot text that would break the messages it is used in.
type TextError struct {
	Lang, Key string
	Problems  []i18n.Problem
}

func (e *TextError) Error() string {
	codes := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		codes[i] = p.Code
		if p.Arg != "" {
			codes[i] += " (" + p.Arg + ")"
		}
	}
	return ErrInvalid.Error() + ": text " + e.Lang + "." + e.Key + ": " + strings.Join(codes, ", ")
}
func (e *TextError) Unwrap() error { return ErrInvalid }

var (
	channelRe = regexp.MustCompile(`^(@[A-Za-z][A-Za-z0-9_]{4,31}|-100[0-9]{5,15})$`)
	colorRe   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	// botUsernameRe is a Telegram username (5-32 characters).
	botUsernameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{4,31}$`)
	trc20Re       = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`)
	erc20Re       = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	cardRe        = regexp.MustCompile(`^([0-9]{16}|IR[0-9]{24})$`)
)

// latinDigits turns Persian and Arabic digits into ASCII ones.
func latinDigits(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '۰' && r <= '۹':
			return '0' + r - '۰'
		case r >= '٠' && r <= '٩':
			return '0' + r - '٠'
		}
		return r
	}, s)
}

// NormalizeSetting checks value for the setting key and returns what is
// stored. Bot texts get the catalog's checks (a *TextError).
func NormalizeSetting(key, value string) (string, error) {
	if lang, k, ok := i18n.SplitTextKey(key); ok {
		if !slices.Contains(i18n.Languages, lang) {
			return "", &FieldError{key, "unknown language"}
		}
		v, probs := Texts().ValidateOverride(lang, k, value)
		if len(probs) > 0 {
			if probs[0].Code == i18n.ProblemUnknownKey {
				return "", &FieldError{key, "unknown setting"}
			}
			return "", &TextError{Lang: lang, Key: k, Problems: probs}
		}
		return v, nil
	}
	sp, ok := SpecOf(key)
	if !ok {
		return "", &FieldError{key, "unknown setting"}
	}
	if sp.Multiline {
		value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	bad := func(msg string) (string, error) { return "", &FieldError{key, msg} }
	limit := maxSettingLen
	if sp.Multiline {
		limit = sp.MaxLen * 4
	}
	if !utf8.ValidString(value) || len(value) > limit {
		return bad("too long")
	}
	for _, r := range value {
		if unicode.IsControl(r) && (!sp.Multiline || (r != '\n' && r != '\t')) {
			return bad("must be one line of text")
		}
	}
	switch sp.Kind {
	case KindText:
		if utf8.RuneCountInString(value) > sp.MaxLen {
			return bad("at most " + strconv.Itoa(sp.MaxLen) + " characters")
		}
		if sp.Multiline {
			// Line breaks stay; each line is trimmed and runs of blank lines become one.
			lines := strings.Split(value, "\n")
			for i, l := range lines {
				lines[i] = strings.TrimRight(l, " \t")
			}
			value = strings.TrimSpace(strings.Join(lines, "\n"))
			for strings.Contains(value, "\n\n\n") {
				value = strings.ReplaceAll(value, "\n\n\n", "\n\n")
			}
		} else {
			value = strings.Join(strings.Fields(value), " ")
		}
		switch key {
		case SettingReminderText + "fa", SettingReminderText + "en":
			// A message to resellers: Telegram's HTML and only the placeholders it is given.
			return checkReminderText(key, value)
		case "branding.currency.fa", "branding.currency.en":
			// It goes into every price, also inside HTML messages and buttons.
			if strings.ContainsAny(value, "<>&") {
				return bad("may not contain <, > or &")
			}
		case SettingCashbackPercent:
			bp, err := ParsePercentBP(value)
			if err != nil {
				return bad("a percent from 0.01 to 20, for example 2.5")
			}
			value = FormatPercentBP(bp)
		case "branding.bot_username":
			value = strings.TrimPrefix(value, "@")
			if !botUsernameRe.MatchString(value) {
				return bad("a Telegram username: 5 to 32 letters, digits or underscores, starting with a letter (e.g. @my_vpn_bot)")
			}
		case "payments.card_number":
			value = latinDigits(value)
			if !cardRe.MatchString(strings.NewReplacer(" ", "", "-", "").Replace(strings.ToUpper(value))) {
				return bad("a card number has 16 digits (spaces and dashes are fine), a sheba is IR and 24 digits")
			}
		case "payments.usdt_trc20":
			if !trc20Re.MatchString(value) {
				return bad("not a TRC20 address (T followed by 33 letters and digits)")
			}
		case "payments.usdt_erc20":
			if !erc20Re.MatchString(value) {
				return bad("not an ERC20 address (0x followed by 40 hex digits)")
			}
		}
	case KindInt:
		digits := strings.NewReplacer(",", "", "_", "", " ", "", "٬", "", "،", "").Replace(latinDigits(value))
		n, err := strconv.ParseInt(digits, 10, 64)
		if err != nil || n < sp.Min || n > sp.Max {
			return bad("a whole number from " + strconv.FormatInt(sp.Min, 10) + " to " + strconv.FormatInt(sp.Max, 10))
		}
		value = strconv.FormatInt(n, 10)
	case KindBool:
		switch strings.ToLower(value) {
		case "true", "yes", "on", "1":
			value = "true"
		case "false", "no", "off", "0":
			value = "false"
		default:
			return bad("true or false")
		}
	case KindEnum:
		if !slices.Contains(sp.Options, value) {
			return bad("one of " + strings.Join(sp.Options, ", "))
		}
	case KindTZ:
		if value == "Local" {
			return bad("name a time zone, e.g. Asia/Tehran")
		}
		if _, err := time.LoadLocation(value); err != nil {
			return bad("unknown time zone; use a name such as Asia/Tehran or Europe/Berlin")
		}
	case KindTime:
		t, ok := clockTime(latinDigits(value))
		if !ok {
			return bad("a time of day as HH:MM, 24-hour, for example 09:00")
		}
		value = t
	case KindURL:
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return bad("must be an https:// link")
		}
		if key == "join.link" && u.Host != "t.me" && u.Host != "telegram.me" {
			return bad("must be a https://t.me/… link")
		}
	case KindChannel:
		if !channelRe.MatchString(value) {
			return bad("a public channel's @name, or a private channel's id starting with -100")
		}
	case KindColor:
		if !colorRe.MatchString(value) {
			return bad("a colour like #14b8a6")
		}
		value = strings.ToLower(value)
	case KindList:
		list, err := normalizeList(key, value)
		if err != nil {
			return bad(err.Error())
		}
		value = list
	}
	return value, nil
}

// normalizeList checks a list setting (KindList) and writes it the way it is stored: compact JSON, in order, "" for an empty list.
func normalizeList(key, value string) (string, error) {
	switch key {
	case SettingTargetsTiers:
		tiers, err := ParseTargetTiers(value)
		if err != nil {
			return "", err
		}
		return FormatTargetTiers(tiers), nil
	case SettingWheelPrizes:
		prizes, err := ParseWheelPrizes(value)
		if err != nil {
			return "", err
		}
		return FormatWheelPrizes(prizes), nil
	}
	return "", errors.New("not a list")
}

var clockRe = regexp.MustCompile(`^([01]?[0-9]|2[0-3]):([0-5][0-9])$`)

// clockMinutes reads a time of day typed as H:MM or HH:MM (24-hour) as the
// minutes since midnight.
func clockMinutes(s string) (int, bool) {
	m := clockRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	mm, _ := strconv.Atoi(m[2])
	return h*60 + mm, true
}

// clockTime writes a time of day as HH:MM.
func clockTime(s string) (string, bool) {
	n, ok := clockMinutes(s)
	return fmt.Sprintf("%02d:%02d", n/60, n%60), ok
}

// checkTogether checks settings that depend on each other, given the
// values they will have after a change.
func checkTogether(final map[string]string) error {
	if ch := final["join.channel"]; strings.HasPrefix(ch, "-100") && final["join.link"] == "" {
		return &FieldError{"join.link", "a private channel needs its invite link"}
	}
	return nil
}

// SettingChange is one changed setting in an audit entry.
type SettingChange struct {
	Key, Before, After string
}

// AdminSetSetting changes one setting from the bot. Payment details are the
// owner's: an admin may not redirect customers' money.
func (s *Service) AdminSetSetting(ctx context.Context, actorTelegramID int64, key, value string) error {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return err
	}
	if SettingPerm(key) == permSecurity {
		return &FieldError{key, "change it in the dashboard (Settings, Sign-in security) with the owner signed in, or on the server with: bobres admin approval"}
	}
	if perm := SettingPerm(key); ownerOnlyPerm(perm) && actor.Role != "owner" {
		if perm == permGateways {
			return &FieldError{key, "only the store owner can change payment details"}
		}
		return &FieldError{key, "only the store owner can change how the server runs (its updates)"}
	}
	return s.SetSetting(ctx, actor, key, value)
}

// SetSetting changes one setting for an actor the caller has checked.
func (s *Service) SetSetting(ctx context.Context, actor *store.User, key, value string) error {
	_, err := s.SetSettings(ctx, actor, map[string]string{key: value})
	return err
}

// SetSettings checks every value, then writes them in one transaction with
// one audit entry per setting that changed (with its value before). A value
// of "" removes the setting, so its default applies. The caller has checked
// that the actor may change these keys (SettingPerm).
func (s *Service) SetSettings(ctx context.Context, actor *store.User, values map[string]string) ([]SettingChange, error) {
	if len(values) == 0 {
		return nil, nil
	}
	norm := make(map[string]string, len(values))
	keys := make([]string, 0, len(values))
	for k, v := range values {
		nv, err := NormalizeSetting(k, v)
		if err != nil {
			return nil, err
		}
		norm[k] = nv
		keys = append(keys, k)
	}
	slices.Sort(keys)
	// Switching a payment method on is a new action the effective
	// entitlements may refuse.
	var gate *EffectiveLicense
	if touchesToggle(norm) {
		var err error
		if gate, err = s.Effective(ctx); err != nil {
			return nil, err
		}
	}
	var changes []SettingChange
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		changes = nil
		current, err := s.st.LockSettings(ctx, tx, append(slices.Clone(keys), "join.channel", "join.link"))
		if err != nil {
			return err
		}
		final := map[string]string{}
		for k, raw := range current {
			var v string
			if json.Unmarshal(raw, &v) == nil {
				final[k] = v
			}
		}
		before := make(map[string]string, len(keys))
		for _, k := range keys {
			before[k] = final[k]
			final[k] = norm[k]
		}
		if err := checkTogether(final); err != nil {
			return err
		}
		for _, p := range toggledOn(before, norm) {
			if err := gatewayRefusal(gate, p); err != nil {
				return err
			}
		}
		for _, k := range keys {
			if before[k] == norm[k] {
				continue
			}
			if err := s.writeSetting(ctx, tx, actor, k, before[k], norm[k]); err != nil {
				return err
			}
			changes = append(changes, SettingChange{Key: k, Before: before[k], After: norm[k]})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return changes, nil
}

// writeSetting stores one changed, already checked setting inside a
// transaction ("" removes it, so its default applies) with its audit entry
// (the value before and after).
func (s *Service) writeSetting(ctx context.Context, tx pgx.Tx, actor *store.User, key, before, after string) error {
	var err error
	if after == "" {
		err = s.st.DeleteSetting(ctx, tx, key)
	} else {
		raw, _ := json.Marshal(after)
		err = s.st.SetSetting(ctx, tx, key, raw)
	}
	if err != nil {
		return err
	}
	action, entity, entityID := "setting.set", "settings", key
	if lang, tk, ok := i18n.SplitTextKey(key); ok {
		action, entity, entityID = "text.set", "text", lang+"."+tk
		if after == "" {
			action = "text.reset"
		}
	}
	return s.auditChange(ctx, tx, actor, action, entity, entityID,
		map[string]string{"value": before}, map[string]string{"value": after}, "")
}

// ImportTexts replaces the overrides of many texts of one language at once:
// all are checked first and nothing is written when one is wrong (a
// *TextsError lists every problem). Texts left out keep their override.
func (s *Service) ImportTexts(ctx context.Context, actor *store.User, lang string, texts map[string]string) (int, error) {
	if !slices.Contains(i18n.Languages, lang) {
		return 0, invalid("unknown language %q", lang)
	}
	var all []*TextError
	values := make(map[string]string, len(texts))
	for k, v := range texts {
		key := "texts." + lang + "." + k
		nv, err := NormalizeSetting(key, v)
		var te *TextError
		switch {
		case errors.As(err, &te):
			all = append(all, te)
			continue
		case err != nil:
			all = append(all, &TextError{Lang: lang, Key: k, Problems: []i18n.Problem{{Code: i18n.ProblemUnknownKey, Arg: k}}})
			continue
		}
		values[key] = nv
	}
	if len(all) > 0 {
		slices.SortFunc(all, func(a, b *TextError) int { return strings.Compare(a.Key, b.Key) })
		return 0, &TextsError{Texts: all}
	}
	var changed []string
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		changed = nil
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		current, err := s.st.LockSettings(ctx, tx, keys)
		if err != nil {
			return err
		}
		for _, k := range keys {
			var old string
			_ = json.Unmarshal(current[k], &old)
			if old == values[k] {
				continue
			}
			if values[k] == "" {
				err = s.st.DeleteSetting(ctx, tx, k)
			} else {
				raw, _ := json.Marshal(values[k])
				err = s.st.SetSetting(ctx, tx, k, raw)
			}
			if err != nil {
				return err
			}
			_, tk, _ := i18n.SplitTextKey(k)
			changed = append(changed, tk)
		}
		if len(changed) == 0 {
			return nil
		}
		return s.auditChange(ctx, tx, actor, "text.import", "text", lang, nil,
			map[string]any{"lang": lang, "keys": changed}, "")
	})
	if err != nil {
		return 0, err
	}
	return len(changed), nil
}

// TextsError lists the texts of an import that cannot be used.
type TextsError struct{ Texts []*TextError }

func (e *TextsError) Error() string {
	return ErrInvalid.Error() + ": " + strconv.Itoa(len(e.Texts)) + " texts cannot be used"
}
func (e *TextsError) Unwrap() error { return ErrInvalid }

// Settings reads every setting as a string (unset ones are missing).
func (s *Service) Settings(ctx context.Context) (map[string]string, error) {
	raw, err := s.st.GetSettings(ctx, s.st.Conn())
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for k, r := range raw {
		var v string
		if json.Unmarshal(r, &v) == nil {
			out[k] = v
		}
	}
	return out, nil
}
