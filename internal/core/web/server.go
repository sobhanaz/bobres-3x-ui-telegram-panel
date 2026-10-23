// Package web is the dashboard's JSON API (/api/v1) and the dashboard itself
// (/admin), served by core behind Caddy.
//
// Security model: a session is a random cookie value (only its hash is
// stored) in a __Host- cookie (Secure, HttpOnly, SameSite=Strict), ending
// after SessionTTL or SessionIdle without use, and re-checked against the
// user's role on every request. Every request that changes something carries
// the session's CSRF token in X-CSRF-Token; logins check the request's
// origin. Permissions come from the staff member's role (perms.go).
package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
)

// Session limits.
const (
	SessionTTL  = 12 * time.Hour
	SessionIdle = 2 * time.Hour
	cookieName  = "__Host-bobres_session"
	csrfHeader  = "X-CSRF-Token"
	maxBody     = 1 << 20
)

// Config wires the API.
type Config struct {
	Store  *store.Store
	Domain *domain.Service
	// Payments and Provisioner feed the overview and the sales pages.
	Payments    domain.PaymentsClient
	Provisioner domain.Provisioner
	// Servers and Gateways are the provisioner's server registry and
	// payments' gateway settings (nil: those pages answer 503).
	Servers  domain.ServerAdmin
	Gateways domain.GatewayAdmin
	// Env is BOBRES_ENV ("prod" refuses the Zarinpal sandbox).
	Env string
	// Files fetches receipt photos through the bot (nil: not shown).
	Files ReceiptFiles
	// Bot reloads settings and checks channels (nil: the bot picks changes
	// up within a minute, channel checks are unavailable).
	Bot BotPeer
	// Secrets encrypts authenticator secrets; nil disables password logins.
	Secrets *bcrypto.Envelope
	// System asks every service's health for the System page (nil: no
	// services are listed).
	System SystemHealth
	// Logs reads the bot's, payments' and provisioner's kept warnings and
	// errors by service name (core reads its own; missing: not configured).
	Logs map[string]LogSource
	// Backups lists and starts backups (nil: the backup service is not
	// installed).
	Backups Backups
	// Updates is "Update now" and the channel setting of the System > Updates
	// tab: the bot's check, the server's updater (core shares a folder with
	// it, never the Docker socket) and the record of each request (nil: those
	// endpoints answer 503).
	Updates *domain.Updates
	// LicenseServer reaches the vendor's license server through the bot, to
	// activate a license key (nil: no bot, activation answers 503).
	LicenseServer domain.LicenseServer
	// TLS is the HTTPS manager (nil: the HTTPS tab answers 503).
	TLS *domain.TLS
	// PublicIP detects the server's public IP address through the bot (nil:
	// no bot; detection answers 503 and an IP address needs a
	// confirmation).
	PublicIP PublicIPSource
	// Dashboard serves the built app under /admin/ (nil: not mounted).
	Dashboard http.Handler
	// MiniApp serves the Telegram Mini App store under /app/ (nil: not
	// mounted) and MiniAppVerify checks the init data it signs in with, by
	// asking the bot (nil: no bot; signing in answers 503).
	MiniApp       http.Handler
	MiniAppVerify MiniAppVerifier
	// MiniAppBot makes the Mini App's Stars invoice links, sends its receipts
	// to the customer's chat and has the staff alerted to a payment proof, all
	// through the bot, which alone holds the Telegram token (nil: the Mini App
	// offers no Stars and no manual payment).
	MiniAppBot MiniAppBot
	// MiniAppMembers asks the bot whether a customer is in the store's required
	// channel (nil: no bot; the app then lets everyone in).
	MiniAppMembers MiniAppMembers
	// Monitor serves the Monitoring section (nil: its endpoints answer 503).
	Monitor MonitorSource
	Log     *slog.Logger
}

// Server is the dashboard API.
type Server struct {
	cfg     Config
	limiter *limiter
	// subPages limits the public service page per client address.
	subPages *limiter
	// tests limits broadcast test sends per staff member.
	tests *limiter
	// probes limits connection tests (servers, gateways) per staff member.
	probes *limiter
	// uploads, restores and downloads limit the dashboard's backup uploads (5 an hour), restore
	// requests (6 an hour) and download links (10 an hour) per owner; tickets are the download links.
	uploads, restores, downloads *limiter
	tickets                      tickets
	// hashing holds a slot per password hash being computed or checked:
	// each takes 32 MiB, so a burst of logins must not run them all at once.
	hashing chan struct{}
	// photoSlot is held while a bot photo is decoded and encoded (up to
	// about 50 MiB): one at a time.
	photoSlot chan struct{}
	// exports holds a place for each CSV export running (maxExports).
	exports chan struct{}
	// exportTime and rowPause are for tests: how long an export may run (0:
	// domain.ExportDeadline) and a pause before each row of the orders export.
	exportTime time.Duration
	rowPause   time.Duration
	now        func() time.Time
	// mini holds the Mini App's sessions and rate limits.
	mini *miniState
}

// hashSlots is how many password hashes core computes at a time (32 MiB
// each; core's container has 512 MiB).
const hashSlots = 2

// New builds the API.
func New(cfg Config) *Server {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Server{cfg: cfg, limiter: newLimiter(20, time.Minute), subPages: newLimiter(subPageRate, time.Minute), tests: newLimiter(5, time.Minute),
		probes: newLimiter(10, time.Minute), uploads: newLimiter(5, time.Hour), restores: newLimiter(6, time.Hour), downloads: newLimiter(10, time.Hour), hashing: make(chan struct{}, hashSlots), photoSlot: make(chan struct{}, 1), exports: make(chan struct{}, maxExports),
		now: time.Now, mini: newMiniState()}
}

// Register mounts the API on /api/v1/ and the dashboard on /admin/.
func (s *Server) Register(mux *http.ServeMux) {
	api := http.NewServeMux()
	api.HandleFunc("POST /api/v1/auth/link", s.loginWithLink)
	api.HandleFunc("POST /api/v1/auth/login", s.loginWithPassword)
	api.HandleFunc("POST /api/v1/auth/logout", s.restrictedSession(s.logout))
	api.HandleFunc("GET /api/v1/me", s.restrictedSession(s.me))
	api.HandleFunc("POST /api/v1/me/password", s.restrictedSession(s.startPassword))
	api.HandleFunc("POST /api/v1/me/password/confirm", s.restrictedSession(s.confirmPassword))
	api.HandleFunc("DELETE /api/v1/me/password", s.session(s.removePassword))
	api.HandleFunc("GET /api/v1/me/sessions", s.session(s.mySessions))
	api.HandleFunc("POST /api/v1/me/sessions/revoke-others", s.session(s.revokeMyOtherSessions))
	api.HandleFunc("POST /api/v1/me/sessions/{sid}/revoke", s.session(s.revokeMySession))
	api.HandleFunc("GET /api/v1/overview", s.session(s.require(PermOverviewRead, s.overview)))
	// The store's look, for the login page (public: nothing private in it).
	api.HandleFunc("GET /api/v1/brand", s.publicBrand)
	api.HandleFunc("GET /api/v1/brand/logo", s.logo)

	// Customers.
	s.route(api, "GET /api/v1/users", PermUsersRead, s.listUsers)
	s.route(api, "GET /api/v1/users/{id}", PermUsersRead, s.getUser)
	s.route(api, "POST /api/v1/users/{id}/status", PermUsersWrite, s.setUserStatus)
	s.route(api, "POST /api/v1/users/{id}/balance", PermWalletAdjust, s.adjustBalance)
	s.route(api, "GET /api/v1/services", PermServicesRead, s.listServices)
	s.route(api, "GET /api/v1/services/{id}", PermServicesRead, s.getService)
	s.route(api, "POST /api/v1/services/{id}/extend", PermServicesExtend, s.extendService)
	s.route(api, "POST /api/v1/services/{id}/sync", PermServicesExtend, s.syncService)
	s.route(api, "POST /api/v1/services/{id}/reset-traffic", PermServicesWrite, s.resetService)
	s.route(api, "POST /api/v1/services/{id}/enabled", PermServicesWrite, s.setServiceEnabled)
	s.route(api, "POST /api/v1/services/{id}/delete", PermServicesWrite, s.deleteService)
	s.route(api, "POST /api/v1/services/{id}/page-token/regenerate", PermServicesWrite, s.regeneratePage)

	// Sales.
	s.route(api, "GET /api/v1/plans", PermPlansWrite, s.listPlans)
	s.route(api, "POST /api/v1/plans", PermPlansWrite, s.createPlan)
	s.route(api, "PUT /api/v1/plans/{id}", PermPlansWrite, s.updatePlan)
	s.route(api, "DELETE /api/v1/plans/{id}", PermPlansDelete, s.deletePlan)
	s.route(api, "GET /api/v1/plans/deleted", PermPlansDelete, s.listDeletedPlans)
	s.route(api, "POST /api/v1/plans/{id}/restore", PermPlansDelete, s.restorePlan)
	s.route(api, "GET /api/v1/plan-groups", PermPlansWrite, s.listPlanGroups)
	s.route(api, "POST /api/v1/plan-groups", PermPlansWrite, s.createPlanGroup)
	s.route(api, "PUT /api/v1/plan-groups/{id}", PermPlansWrite, s.updatePlanGroup)
	s.route(api, "DELETE /api/v1/plan-groups/{id}", PermPlansWrite, s.deletePlanGroup)
	s.route(api, "GET /api/v1/orders", PermPaymentsRead, s.listOrders)
	s.route(api, "GET /api/v1/orders/{id}", PermPaymentsRead, s.getOrder)
	s.route(api, "POST /api/v1/orders/{id}/retry", PermPaymentsReview, s.retryOrder)
	s.route(api, "POST /api/v1/orders/{id}/refund", PermPaymentsReview, s.refundOrder)
	s.route(api, "GET /api/v1/payments", PermPaymentsRead, s.listPayments)
	s.route(api, "GET /api/v1/payments/pending", PermPaymentsRead, s.pendingPayments)
	s.route(api, "GET /api/v1/payments/{id}/receipt", PermPaymentsRead, s.paymentReceipt)
	s.route(api, "POST /api/v1/payments/{id}/review", PermPaymentsReview, s.reviewPayment)
	s.route(api, "GET /api/v1/ledger", PermLedgerRead, s.listLedger)
	s.route(api, "GET /api/v1/ledger.csv", PermLedgerRead, s.exportLedger)
	s.route(api, "GET /api/v1/reports/summary", PermReportsRead, s.reportSummary)
	s.route(api, "GET /api/v1/reports/daily-summary", PermReportsRead, s.reportDailySummary)
	s.route(api, "GET /api/v1/reports/summary.csv", PermReportsRead, s.exportReportSummary)
	s.route(api, "GET /api/v1/reports/plans", PermReportsRead, s.reportPlans)
	s.route(api, "GET /api/v1/reports/methods", PermReportsRead, s.reportMethods)
	s.route(api, "GET /api/v1/reports/plan-series", PermReportsRead, s.reportPlanSeries)
	s.route(api, "GET /api/v1/reports/hours", PermReportsRead, s.reportHours)
	s.route(api, "GET /api/v1/reports/orders.csv", PermReportsRead, s.exportReportOrders)
	s.route(api, "GET /api/v1/discounts", PermDiscountsWrite, s.listDiscounts)
	s.route(api, "POST /api/v1/discounts", PermDiscountsWrite, s.saveDiscount)
	s.route(api, "PUT /api/v1/discounts/{code}/enabled", PermDiscountsWrite, s.setDiscountEnabled)
	s.route(api, "GET /api/v1/usdt-rate", PermGatewaysWrite, s.getUsdtRate)
	s.route(api, "POST /api/v1/usdt-rate/refresh", PermGatewaysWrite, s.refreshUsdtRate)
	s.privateRoute(api, "GET /api/v1/supplier", PermGatewaysWrite, s.getSupplier)
	s.privateRoute(api, "PUT /api/v1/supplier/config", PermGatewaysWrite, s.putSupplierConfig)
	s.privateRoute(api, "GET /api/v1/supplier/balance", PermGatewaysWrite, s.getSupplierBalance)
	s.privateRoute(api, "POST /api/v1/supplier/sync", PermGatewaysWrite, s.syncSupplier)
	s.privateRoute(api, "GET /api/v1/supplier/products", PermGatewaysWrite, s.listSupplierProducts)
	s.privateRoute(api, "PUT /api/v1/supplier/products/{id}", PermGatewaysWrite, s.updateSupplierProduct)
	s.privateRoute(api, "GET /api/v1/supplier/orders", PermGatewaysWrite, s.listSupplierOrders)
	s.privateRoute(api, "POST /api/v1/supplier/orders/{id}/retry", PermGatewaysWrite, s.retrySupplierOrder)
	s.privateRoute(api, "POST /api/v1/supplier/orders/{id}/refund", PermGatewaysWrite, s.refundSupplierOrder)
	s.privateRoute(api, "GET /api/v1/vendor", PermLicenseWrite, s.getVendor)
	s.privateRoute(api, "PUT /api/v1/vendor/config", PermLicenseWrite, s.putVendorConfig)
	s.privateRoute(api, "GET /api/v1/vendor/licenses", PermLicenseWrite, s.listSoldLicenses)
	s.privateRoute(api, "POST /api/v1/vendor/licenses/{id}/grant", PermLicenseWrite, s.grantSoldLicense)
	s.privateRoute(api, "GET /api/v1/vendor/server-licenses", PermLicenseWrite, s.listServerLicenses)
	s.privateRoute(api, "POST /api/v1/vendor/server-licenses", PermLicenseWrite, s.createServerLicense)
	s.privateRoute(api, "GET /api/v1/vendor/server-licenses/{id}", PermLicenseWrite, s.getServerLicense)
	s.privateRoute(api, "PUT /api/v1/vendor/server-licenses/{id}", PermLicenseWrite, s.updateServerLicense)
	s.privateRoute(api, "POST /api/v1/vendor/server-licenses/{id}/revoke", PermLicenseWrite, s.revokeServerLicense)
	s.privateRoute(api, "POST /api/v1/vendor/server-licenses/{id}/rekey", PermLicenseWrite, s.rekeyServerLicense)
	s.privateRoute(api, "POST /api/v1/vendor/licenses/{id}/revoke", PermLicenseWrite, s.revokeSoldLicense)
	s.route(api, "GET /api/v1/gift-codes", PermDiscountsWrite, s.listGiftCodes)
	s.route(api, "POST /api/v1/gift-codes", PermDiscountsWrite, s.createGiftCodes)
	s.route(api, "GET /api/v1/gift-codes.csv", PermDiscountsWrite, s.exportGiftCodes)
	s.route(api, "PUT /api/v1/gift-codes/{id}/enabled", PermDiscountsWrite, s.setGiftCodeEnabled)
	s.route(api, "GET /api/v1/gift-codes/{id}/redemptions", PermDiscountsWrite, s.listGiftRedemptions)
	s.route(api, "GET /api/v1/gift-codes/{id}/redemptions.csv", PermDiscountsWrite, s.exportGiftRedemptions)
	s.route(api, "GET /api/v1/referrals", PermDiscountsWrite, s.getReferrals)
	s.route(api, "PUT /api/v1/referrals", PermDiscountsWrite, s.setReferralReward)

	// Resellers: they work in the bot; owner and admin manage them here.
	s.route(api, "GET /api/v1/resellers", PermResellersWrite, s.listResellers)
	s.route(api, "POST /api/v1/resellers", PermResellersWrite, s.createReseller)
	s.route(api, "GET /api/v1/resellers/{id}", PermResellersWrite, s.getReseller)
	s.route(api, "PUT /api/v1/resellers/{id}", PermResellersWrite, s.updateReseller)
	s.route(api, "PUT /api/v1/resellers/{id}/naming", PermResellersWrite, s.updateResellerNaming)
	s.route(api, "POST /api/v1/resellers/{id}/group-sync", PermResellersWrite, s.resellerGroupSync)
	s.route(api, "POST /api/v1/resellers/{id}/payments", PermResellersWrite, s.resellerPayment)
	s.route(api, "POST /api/v1/resellers/{id}/remove", PermResellersWrite, s.removeReseller)
	s.route(api, "GET /api/v1/reseller-tiers", PermResellersWrite, s.listTiers)
	s.route(api, "POST /api/v1/reseller-tiers", PermResellersWrite, s.createTier)
	s.route(api, "PUT /api/v1/reseller-tiers/{id}", PermResellersWrite, s.updateTier)
	s.route(api, "DELETE /api/v1/reseller-tiers/{id}", PermResellersWrite, s.deleteTier)

	// Support and broadcasts.
	s.route(api, "GET /api/v1/tickets", PermTicketsWrite, s.listTickets)
	s.route(api, "GET /api/v1/tickets/counts", PermTicketsWrite, s.ticketCounts)
	s.route(api, "GET /api/v1/tickets/assignees", PermTicketsWrite, s.ticketAssignees)
	s.route(api, "GET /api/v1/tickets/{id}", PermTicketsWrite, s.getTicket)
	s.route(api, "POST /api/v1/tickets/{id}/messages", PermTicketsWrite, s.replyTicket)
	s.route(api, "POST /api/v1/tickets/{id}/status", PermTicketsWrite, s.setTicketStatus)
	s.route(api, "PUT /api/v1/tickets/{id}/assignee", PermTicketsWrite, s.assignTicket)
	s.route(api, "GET /api/v1/tickets/{id}/messages/{mid}/file", PermTicketsWrite, s.ticketFile)
	s.route(api, "GET /api/v1/canned-replies", PermTicketsWrite, s.listCanned)
	s.route(api, "POST /api/v1/canned-replies", PermTicketsManage, s.createCanned)
	s.route(api, "PUT /api/v1/canned-replies/{id}", PermTicketsManage, s.updateCanned)
	s.route(api, "DELETE /api/v1/canned-replies/{id}", PermTicketsManage, s.deleteCanned)
	s.route(api, "GET /api/v1/broadcasts", PermBroadcastWrite, s.listBroadcasts)
	s.route(api, "POST /api/v1/broadcasts", PermBroadcastWrite, s.createBroadcast)
	s.route(api, "POST /api/v1/broadcasts/audience", PermBroadcastWrite, s.broadcastAudience)
	s.route(api, "POST /api/v1/broadcasts/check", PermBroadcastWrite, s.checkBroadcastText)
	s.route(api, "POST /api/v1/broadcasts/test", PermBroadcastWrite, s.testBroadcast)
	s.route(api, "GET /api/v1/broadcasts/{id}", PermBroadcastWrite, s.getBroadcast)
	s.route(api, "PUT /api/v1/broadcasts/{id}", PermBroadcastWrite, s.updateBroadcast)
	s.route(api, "DELETE /api/v1/broadcasts/{id}", PermBroadcastWrite, s.deleteBroadcast)
	s.route(api, "POST /api/v1/broadcasts/{id}/send", PermBroadcastWrite, s.sendBroadcast)
	s.route(api, "POST /api/v1/broadcasts/{id}/pause", PermBroadcastWrite, s.pauseBroadcast)
	s.route(api, "POST /api/v1/broadcasts/{id}/resume", PermBroadcastWrite, s.resumeBroadcast)
	s.route(api, "POST /api/v1/broadcasts/{id}/cancel", PermBroadcastWrite, s.cancelBroadcast)
	s.route(api, "GET /api/v1/broadcasts/{id}/recipients", PermBroadcastWrite, s.broadcastRecipients)

	// Store setup.
	s.route(api, "GET /api/v1/branding", PermBrandingWrite, s.getBranding)
	s.route(api, "PUT /api/v1/branding", PermBrandingWrite, s.putBranding)
	s.route(api, "PUT /api/v1/branding/logo", PermBrandingWrite, s.putLogo)
	s.route(api, "DELETE /api/v1/branding/logo", PermBrandingWrite, s.deleteLogo)
	s.route(api, "GET /api/v1/branding/bot-photo", PermBrandingWrite, s.botPhoto)
	s.route(api, "PUT /api/v1/branding/bot-photo", PermBrandingWrite, s.putBotPhoto)
	s.route(api, "DELETE /api/v1/branding/bot-photo", PermBrandingWrite, s.deleteBotPhoto)
	s.route(api, "GET /api/v1/texts", PermBrandingWrite, s.listTexts)
	s.route(api, "POST /api/v1/texts/check", PermBrandingWrite, s.checkText)
	s.route(api, "PUT /api/v1/texts", PermBrandingWrite, s.putText)
	s.route(api, "GET /api/v1/texts.json", PermBrandingWrite, s.exportTexts)
	s.route(api, "POST /api/v1/texts/import", PermBrandingWrite, s.importTexts)
	// Help content: the FAQ and the tutorials the bot shows (Branding, Help content).
	s.route(api, "GET /api/v1/help", PermBrandingWrite, s.listHelp)
	s.route(api, "POST /api/v1/help", PermBrandingWrite, s.createHelp)
	s.route(api, "POST /api/v1/help/check", PermBrandingWrite, s.checkHelp)
	s.route(api, "POST /api/v1/help/order", PermBrandingWrite, s.orderHelp)
	s.route(api, "POST /api/v1/help/defaults", PermBrandingWrite, s.defaultHelp)
	s.route(api, "PUT /api/v1/help/{id}", PermBrandingWrite, s.updateHelp)
	s.route(api, "PUT /api/v1/help/{id}/enabled", PermBrandingWrite, s.setHelpEnabled)
	s.route(api, "DELETE /api/v1/help/{id}", PermBrandingWrite, s.deleteHelp)
	s.route(api, "GET /api/v1/settings", PermSettingsWrite, s.getSettings)
	s.route(api, "PUT /api/v1/settings", PermSettingsWrite, s.putSettings)
	s.route(api, "POST /api/v1/settings/channel-check", PermSettingsWrite, s.checkChannel)
	// The required channels and groups: the Settings page's Join card (seen by
	// owner and admin, changed by the owner).
	s.route(api, "GET /api/v1/join-chats", PermSettingsWrite, s.getJoinChats)
	s.route(api, "PUT /api/v1/join-chats", PermJoinWrite, s.putJoinChats)

	// Infrastructure: 3x-ui servers (owner and admin), payment gateways (owner).
	s.route(api, "GET /api/v1/servers", PermServersWrite, s.listServers)
	s.route(api, "POST /api/v1/servers", PermServersWrite, s.addServer)
	s.route(api, "POST /api/v1/servers/test", PermServersWrite, s.testServer)
	s.route(api, "PUT /api/v1/servers/{id}", PermServersWrite, s.updateServer)
	s.route(api, "DELETE /api/v1/servers/{id}", PermServersWrite, s.deleteServer)
	s.route(api, "GET /api/v1/servers/{id}/inbounds", PermServersWrite, s.listInbounds)
	s.route(api, "PUT /api/v1/servers/{id}/inbounds/{inbound}", PermServersWrite, s.setInboundAttach)
	s.route(api, "GET /api/v1/gateways", PermGatewaysWrite, s.listGateways)
	s.route(api, "GET /api/v1/gateways/report", PermGatewaysWrite, s.gatewayReport)
	s.route(api, "PUT /api/v1/gateways/{provider}", PermGatewaysWrite, s.putGateway)
	s.route(api, "POST /api/v1/gateways/{provider}/test", PermGatewaysWrite, s.testGateway)
	s.route(api, "GET /api/v1/gateways/deposits", PermGatewaysWrite, s.listDeposits)
	s.route(api, "POST /api/v1/gateways/deposits/resolve", PermGatewaysWrite, s.resolveDeposit)

	// System: health and versions (owner and admin), the license (state for
	// owner and admin, changes by the owner).
	s.route(api, "GET /api/v1/system", PermSystemRead, s.system)
	// Logs carry Telegram ids: the owner only.
	s.route(api, "GET /api/v1/system/logs", PermSystemWrite, s.systemLogs)
	// Backups: the list for owner and admin, "back up now" for the owner.
	s.route(api, "GET /api/v1/system/backups", PermSystemRead, s.systemBackups)
	s.route(api, "POST /api/v1/system/backups/run", PermSystemWrite, s.runBackup)
	// Downloading a backup and restoring one: the owner's. A download is a ticket (it needs the
	// authenticator code) and then the browser's own GET; a restore is an upload or a backup of the
	// server, a typed confirmation and the code. The upload is mounted below, outside this mux, for
	// it is not a small request.
	s.route(api, "POST /api/v1/system/backups/download-ticket", PermSystemWrite, s.downloadTicket)
	s.route(api, "GET /api/v1/system/backups/download/{ticket}", PermSystemWrite, s.downloadBackup)
	s.route(api, "GET /api/v1/system/restore", PermSystemRead, s.getRestore)
	s.route(api, "POST /api/v1/system/restore", PermSystemWrite, s.applyRestore)
	// Updates: the state (the bot's check, the updater's progress) for owner
	// and admin; checking again, the channel and "Update now" for the owner.
	s.route(api, "GET /api/v1/system/updates", PermSystemRead, s.getUpdates)
	s.route(api, "GET /api/v1/system/updates/progress", PermSystemRead, s.updateProgress)
	s.route(api, "POST /api/v1/system/updates/check", PermSystemWrite, s.checkUpdatesNow)
	s.route(api, "PUT /api/v1/system/updates/channel", PermSystemWrite, s.putUpdateChannel)
	s.route(api, "POST /api/v1/system/updates/apply", PermSystemWrite, s.applyUpdate)
	// HTTPS: the state for owner and admin, changes and detecting the
	// server's IP address (through the bot) by the owner.
	s.route(api, "GET /api/v1/system/tls", PermSystemRead, s.getTLS)
	s.route(api, "PUT /api/v1/system/tls", PermSystemWrite, s.putTLS)
	s.route(api, "POST /api/v1/system/tls/detect-ip", PermSystemWrite, s.detectIP)
	// Monitoring (read only; owner and admin): the stack at a glance, the metrics over time, the
	// recent warnings and errors.
	s.route(api, "GET /api/v1/monitor/overview", PermMonitorRead, s.monitorOverview)
	s.route(api, "GET /api/v1/monitor/series", PermMonitorRead, s.monitorSeries)
	s.route(api, "GET /api/v1/monitor/metrics", PermMonitorRead, s.monitorMetrics)
	s.route(api, "GET /api/v1/monitor/events", PermMonitorRead, s.monitorEvents)
	s.route(api, "GET /api/v1/license", PermSystemRead, s.getLicense)
	s.route(api, "POST /api/v1/license/check", PermLicenseWrite, s.checkLicense)
	s.route(api, "PUT /api/v1/license", PermLicenseWrite, s.putLicense)
	s.route(api, "POST /api/v1/license/activate", PermLicenseWrite, s.activateLicense)

	// Administration.
	s.route(api, "GET /api/v1/staff", PermStaffWrite, s.listStaff)
	s.route(api, "POST /api/v1/staff", PermStaffWrite, s.addStaff)
	s.route(api, "PUT /api/v1/staff/{id}/role", PermStaffWrite, s.setStaffRole)
	s.route(api, "POST /api/v1/staff/{id}/remove", PermStaffWrite, s.removeStaff)
	s.route(api, "GET /api/v1/staff/{id}/sessions", PermStaffWrite, s.staffSessions)
	s.route(api, "POST /api/v1/staff/{id}/sessions/revoke", PermStaffWrite, s.revokeStaffSessions)
	s.route(api, "POST /api/v1/staff/{id}/sessions/{sid}/revoke", PermStaffWrite, s.revokeStaffSession)
	s.route(api, "POST /api/v1/staff/{id}/password/reset", PermStaffWrite, s.resetStaffPassword)
	s.route(api, "POST /api/v1/staff/{id}/unlock", PermStaffWrite, s.unlockStaff)
	s.route(api, "GET /api/v1/audit", PermAuditRead, s.listAudit)
	s.route(api, "GET /api/v1/audit/actions", PermAuditRead, s.auditActions)
	s.route(api, "GET /api/v1/audit.csv", PermAuditRead, s.exportAudit)
	api.HandleFunc("/api/v1/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	mux.Handle("/api/v1/", apiHeaders(api))
	// A backup uploaded for a restore is the one request that is not small: it has the usual
	// headers and the audit context, and its own limit (domain.StagedUpload, uploadRestore).
	mux.Handle("POST /api/v1/system/restore/upload", apiHeadersLimit(s.session(s.require(PermSystemWrite, s.uploadRestore)), 0))
	// The branded service page: public, the token in the address is the
	// credential. Every other path under /sub/ gets the same generic 404.
	mux.HandleFunc("GET /sub/{token}", s.subPage)
	// The store's own Terms and Privacy pages (docs/legal/README.md): public, default text the owner can edit.
	mux.HandleFunc("GET /terms", s.termsPage)
	mux.HandleFunc("GET /privacy", s.privacyPage)
	mux.HandleFunc("/sub/", s.pageNotFound)
	s.registerMiniApp(mux)
	if s.cfg.Dashboard != nil {
		mux.Handle("/admin/", s.cfg.Dashboard)
		mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
		})
	}
}

// route mounts a staff endpoint: a live session, then the permission.
func (s *Server) route(mux *http.ServeMux, pattern, perm string, h http.HandlerFunc) {
	mux.HandleFunc(pattern, s.session(s.require(perm, h)))
}

// privateRoute is route for the vendor's own features (selling licenses, the license server, the supplier resale): a store whose
// license does not grant them answers as if the path did not exist.
func (s *Server) privateRoute(mux *http.ServeMux, pattern, perm string, h http.HandlerFunc) {
	s.route(mux, pattern, perm, func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.Domain.PrivateFeatures(r.Context()) {
			writeError(w, http.StatusNotFound, "not_found", "no such page")
			return
		}
		h(w, r)
	})
}

func apiHeaders(next http.Handler) http.Handler { return apiHeadersLimit(next, maxBody) }

// apiHeadersLimit is apiHeaders with another limit on the body (0: the handler sets its own).
func apiHeadersLimit(next http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if limit > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		// Audit entries written for this request record where it came from.
		ctx := store.WithSource(store.WithClientIP(r.Context(), clientIP(r)), store.SourceDashboard)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// staffKey carries the request's staff member and session.
type staffKey struct{}

type staff struct {
	user    *store.User
	session *store.WebSession
	idHash  []byte
}

func staffFrom(ctx context.Context) *staff { s, _ := ctx.Value(staffKey{}).(*staff); return s }

// session requires a live session of an active staff member; requests that
// change something must also carry the session's CSRF token.
//
// A restricted session (the first login with an initial password, method
// password_initial) is refused here with 403 must_change_password: only the
// handlers wrapped in restrictedSession answer it.
func (s *Server) session(next http.HandlerFunc) http.HandlerFunc { return s.sessionFor(false, next) }

// restrictedSession is session for the few endpoints a restricted session
// may use: who am I, choosing the new password and authenticator app, and
// logging out.
func (s *Server) restrictedSession(next http.HandlerFunc) http.HandlerFunc {
	return s.sessionFor(true, next)
}

func (s *Server) sessionFor(allowRestricted bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "log in first")
			return
		}
		hash := webauth.HashToken(c.Value)
		sess, err := s.cfg.Store.ActiveSession(r.Context(), s.cfg.Store.Conn(), hash, SessionIdle)
		if errors.Is(err, store.ErrNotFound) {
			clearCookie(w)
			writeError(w, http.StatusUnauthorized, "unauthorized", "the session ended; log in again")
			return
		}
		if err != nil {
			s.internal(w, "load session", err)
			return
		}
		user, err := s.cfg.Domain.StaffUser(r.Context(), sess.UserID)
		if err != nil { // demoted or banned since the login
			_ = s.cfg.Store.RevokeSession(r.Context(), s.cfg.Store.Conn(), hash)
			clearCookie(w)
			writeError(w, http.StatusUnauthorized, "unauthorized", "this account may no longer use the dashboard")
			return
		}
		if sess.Method == methodInitial && !allowRestricted {
			writeError(w, http.StatusForbidden, "must_change_password", "set a new password and turn on two-factor authentication first")
			return
		}
		if !safeMethod(r.Method) {
			got := r.Header.Get(csrfHeader)
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
				writeError(w, http.StatusForbidden, "csrf", "missing or wrong CSRF token")
				return
			}
		}
		if s.now().Sub(sess.LastSeenAt) > time.Minute {
			_ = s.cfg.Store.TouchSession(r.Context(), s.cfg.Store.Conn(), hash)
		}
		ctx := context.WithValue(r.Context(), staffKey{}, &staff{user: user, session: sess, idHash: hash})
		next(w, r.WithContext(ctx))
	}
}

// require checks a permission of the staff member's role.
func (s *Server) require(perm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !Allowed(staffFrom(r.Context()).user.Role, perm) {
			writeError(w, http.StatusForbidden, "forbidden", "your role may not do this")
			return
		}
		next(w, r)
	}
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin guards the login endpoints (no session, so no CSRF token yet):
// a browser request must come from this site. Non-browser clients send
// neither header.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
	default:
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	}
	return true
}

// clientKey is clientIP for rate limits: an IPv6 address counts as its /64,
// since one host usually holds a whole /64 and could otherwise use a fresh
// address for every request.
func clientKey(r *http.Request) string {
	ip := clientIP(r)
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}

// clientIP is the address Caddy saw (it overwrites X-Forwarded-For; core's
// port is not reachable from outside).
func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		ip, _, _ := strings.Cut(f, ",")
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": code, "message": msg})
}

func (s *Server) internal(w http.ResponseWriter, what string, err error) {
	s.cfg.Log.Error("dashboard api: "+what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "something went wrong; try again")
}

// readJSON decodes a small JSON body strictly.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "the request body is not valid JSON for this endpoint")
		return false
	}
	return true
}

func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}
