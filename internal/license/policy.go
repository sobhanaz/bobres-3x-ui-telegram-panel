package license

import (
	"slices"
	"time"
)

// The license policy: "free core, paid premium" (decided 2026-10-14).
//
// An install without a valid license is not blocked. It runs on the FREE
// TIER, and a short implicit TRIAL protects new and existing installs from
// meeting the free tier on their first day. A license unlocks more. Nothing
// here ever touches what exists: the limits below only refuse NEW actions
// (a new server, a new service, a new reseller, ...), and security updates
// are never withheld.
//
// Resolve is the one function that says what an install may do; every gate,
// every dashboard view and the CLI use it.

// Gateway names a license can list. The wallet is always allowed and is not
// listed. Automatic USDT (GatewayUSDTAuto) and automatic TON (GatewayTONAuto) also need the crypto_watcher
// feature.
const (
	GatewayZarinpal       = "zarinpal"
	GatewayIDPay          = "idpay"
	GatewayNextPay        = "nextpay"
	GatewayAqayepardakht  = "aqayepardakht"
	GatewayStars          = "stars"
	GatewayUSDTAuto       = "usdt_trc20_auto"
	GatewayTONAuto        = "ton_auto"
	GatewayManualCard     = "manual_card"
	GatewayManualCrypto   = "manual_crypto"
	GatewayManualZarinpal = "manual_zarinpal"
)

// AllGateways are the payment methods a license can name (the wallet is
// always allowed). The trial allows every one of them.
func AllGateways() []string {
	return []string{GatewayZarinpal, GatewayIDPay, GatewayNextPay, GatewayAqayepardakht, GatewayStars, GatewayUSDTAuto, GatewayTONAuto, GatewayManualCard, GatewayManualCrypto, GatewayManualZarinpal}
}

// The free tier. These constants are the OWNER'S to tune: change a number
// here and every gate, view and document that reads FreeEntitlements follows
// (docs/05-license.md lists them; keep it in step).
const (
	// FreeMaxServers: 3x-ui servers an install may have.
	FreeMaxServers = 1
	// FreeMaxActiveServices: services customers have now (active or about
	// to expire) before new sales are refused. Existing services keep
	// working, renewing and topping up over the limit.
	FreeMaxActiveServices = 25
	// FreeStaffSeats: staff members: admins, support and owners, except the
	// main owner the server names (the first id in BOBRES_ADMIN_TELEGRAM_ID),
	// who takes none.
	FreeStaffSeats = 2
	// FreeMaxJoinChats: channels and groups customers must join to use the
	// bot (the required chats). The first one is the free tier's; more
	// than one is a paid feature.
	FreeMaxJoinChats = 1
)

// FreeEntitlements is what an install without a license (and without a
// running trial) may do. The wallet is always allowed on top of the
// gateways; automatic USDT (crypto_watcher), resellers and white label are
// paid features. There is no updates_until: security updates are never
// withheld from a free install.
func FreeEntitlements() Entitlements {
	return Entitlements{
		WhiteLabel:         false,
		MaxServers:         FreeMaxServers,
		MaxActiveServices:  FreeMaxActiveServices,
		Resellers:          false,
		Gateways:           []string{GatewayZarinpal, GatewayIDPay, GatewayNextPay, GatewayAqayepardakht, GatewayStars, GatewayManualCard, GatewayManualCrypto, GatewayManualZarinpal},
		CryptoWatcher:      false,
		DashboardStaffSeat: FreeStaffSeats,
		MaxJoinChats:       FreeMaxJoinChats,
	}
}

// The implicit trial: every install has TrialDays from the first start of a
// version with this policy (a fresh install and an upgraded one alike), with
// the pro-like entitlements below. When it ends, nothing is deleted or
// switched off: the free tier's limits then apply to new actions.
const (
	TrialDays = 30
	// TrialStaffSeats: dashboard seats during the trial.
	TrialStaffSeats = 5
)

// TrialEntitlements is what a running trial allows: unlimited servers,
// services and required chats, resellers, every payment method and the automatic USDT watcher,
// five staff seats. White label is not part of it: the "Powered by BOBRES"
// mark stays on during the trial.
func TrialEntitlements() Entitlements {
	return Entitlements{
		WhiteLabel:         false,
		MaxServers:         Unlimited,
		MaxActiveServices:  Unlimited,
		Resellers:          true,
		Gateways:           AllGateways(),
		CryptoWatcher:      true,
		DashboardStaffSeat: TrialStaffSeats,
		MaxJoinChats:       Unlimited,
	}
}

// TrialEnds is when a trial that began at start ends.
func TrialEnds(start time.Time) time.Time { return start.Add(TrialDays * 24 * time.Hour) }

// DefaultSellerContact is where the owner of an install is told to buy or
// renew a license when BOBRES_SELLER_CONTACT is not set (the dashboard's
// License tab and `bobres license status` show it). It is empty on purpose:
// a made-up handle could belong to a stranger. The owner of this build may
// set a build-time default here (a Telegram handle or an address); an empty
// contact makes every screen say "ask the seller you got BOBRES from".
const DefaultSellerContact = ""

// Basis is what an install's effective entitlements come from.
type Basis string

// The four bases, in the order Resolve tries them.
const (
	// BasisLicense: a license that verified for this install and has not
	// expired.
	BasisLicense Basis = "license"
	// BasisGrace: such a license past its expiry, inside its grace period:
	// its entitlements still hold, with a warning.
	BasisGrace Basis = "grace"
	// BasisTrial: the implicit trial.
	BasisTrial Basis = "trial"
	// BasisFree: the free tier.
	BasisFree Basis = "free"
)

// Effective is what an install may do now.
type Effective struct {
	Basis Basis
	// Tier names it for people: the license's tier ("trial", "standard" or
	// "pro") on a license or in its grace period, "trial" for the implicit
	// trial, "free" for the free tier. Basis tells a vendor trial license
	// from the implicit trial.
	Tier         string
	Entitlements Entitlements
	// TrialEndsAt is when the implicit trial ends (or ended); zero when
	// the install's trial start is not known.
	TrialEndsAt time.Time
}

// TierFree and TierImplicitTrial are the Tier of the two bases that have no
// license (the implicit trial shares its name with a vendor trial license).
const (
	TierFree          = "free"
	TierImplicitTrial = TierTrial
)

// Resolve is the one answer to "what may this install do now":
//
//  1. a license that verified for this install and is active: its
//     entitlements, never lower than the free tier (a license that grants
//     less than the free tier, or omits a limit, still leaves the free
//     tier's: see Entitlements.AtLeast);
//  2. such a license inside its grace period: the same;
//  3. the implicit trial (now before trialStart + TrialDays): TrialEntitlements;
//  4. otherwise (no license, one that does not verify, one past its grace
//     period, the trial over): FreeEntitlements.
//
// lic is nil when no token verified for this install; state is its State at
// now. trialStart is the install's first start of a version with this
// policy; a zero trialStart means unknown, which is no trial (the free tier
// is the safe side for an unknown start, but core always knows it).
func Resolve(lic *Token, state State, trialStart, now time.Time) Effective {
	eff := Effective{}
	if trialStart.After(now) {
		// A trial cannot begin in the future (a clock that was set back, a
		// bad restore): it began now, so it never lasts longer than TrialDays.
		trialStart = now
	}
	if !trialStart.IsZero() {
		eff.TrialEndsAt = TrialEnds(trialStart)
	}
	switch {
	case lic != nil && state == Active:
		eff.Basis, eff.Tier, eff.Entitlements = BasisLicense, lic.Tier, lic.Entitlements.AtLeast(FreeEntitlements())
	case lic != nil && state == Grace:
		eff.Basis, eff.Tier, eff.Entitlements = BasisGrace, lic.Tier, lic.Entitlements.AtLeast(FreeEntitlements())
	case !trialStart.IsZero() && now.Before(eff.TrialEndsAt):
		eff.Basis, eff.Tier, eff.Entitlements = BasisTrial, TierImplicitTrial, TrialEntitlements()
	default:
		eff.Basis, eff.Tier, eff.Entitlements = BasisFree, TierFree, FreeEntitlements()
	}
	eff.Entitlements.Gateways = slices.Clone(eff.Entitlements.Gateways)
	return eff
}

// WhiteLabel: the store may hide BOBRES's name.
func (e Effective) WhiteLabel() bool { return e.Entitlements.WhiteLabel }

// PoweredBy: show "Powered by BOBRES" (the dashboard's footer and login
// page): always, except under a license that includes white_label.
func (e Effective) PoweredBy() bool { return !e.WhiteLabel() }

// TrialRunning: the implicit trial is what applies.
func (e Effective) TrialRunning() bool { return e.Basis == BasisTrial }

// TrialDaysLeft counts whole days to the end of the implicit trial, rounded
// up (0 once it has ended or when its start is not known).
func (e Effective) TrialDaysLeft(now time.Time) int {
	if e.TrialEndsAt.IsZero() || !now.Before(e.TrialEndsAt) {
		return 0
	}
	return int((e.TrialEndsAt.Sub(now) + 24*time.Hour - 1) / (24 * time.Hour))
}
