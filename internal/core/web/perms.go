package web

import "sort"

// Permission flags. A role grants a set; handlers check one flag each, so
// custom roles can be added later without touching handlers.
const (
	PermOverviewRead   = "overview.read"
	PermUsersRead      = "users.read"
	PermUsersWrite     = "users.write"
	PermWalletAdjust   = "wallet.adjust"
	PermServicesRead   = "services.read"
	PermServicesExtend = "services.extend"
	PermServicesWrite  = "services.write"
	PermPlansWrite     = "plans.write"
	PermPaymentsRead   = "payments.read"
	PermPaymentsReview = "payments.review"
	PermLedgerRead     = "ledger.read"
	PermDiscountsWrite = "discounts.write"
	PermBrandingWrite  = "branding.write"
	PermSettingsWrite  = "settings.write"
	PermTicketsWrite   = "tickets.write"
	PermBroadcastWrite = "broadcasts.write"
	PermResellersWrite = "resellers.write"
	PermServersWrite   = "servers.write"
	PermGatewaysWrite  = "gateways.write"
	PermSystemRead     = "system.read"
	PermSystemWrite    = "system.write"
	PermAuditRead      = "audit.read"
	PermStaffWrite     = "staff.write"
	PermLicenseWrite   = "license.write"
)

var allPerms = []string{
	PermOverviewRead, PermUsersRead, PermUsersWrite, PermWalletAdjust, PermServicesRead, PermServicesExtend,
	PermServicesWrite, PermPlansWrite, PermPaymentsRead, PermPaymentsReview, PermLedgerRead, PermDiscountsWrite,
	PermBrandingWrite, PermSettingsWrite, PermTicketsWrite, PermBroadcastWrite, PermResellersWrite, PermServersWrite,
	PermGatewaysWrite, PermSystemRead, PermSystemWrite, PermAuditRead, PermStaffWrite, PermLicenseWrite,
}

// ownerOnly: staff accounts, the license, gateway secrets and system actions
// (backups) stay with the owner.
var ownerOnly = map[string]bool{
	PermStaffWrite: true, PermLicenseWrite: true, PermGatewaysWrite: true, PermSystemWrite: true,
}

var supportPerms = map[string]bool{
	PermOverviewRead: true, PermUsersRead: true, PermServicesRead: true, PermServicesExtend: true,
	PermPaymentsRead: true, PermTicketsWrite: true,
}

// Allowed reports whether a role grants a permission.
func Allowed(role, perm string) bool {
	switch role {
	case "owner":
		return true
	case "admin":
		return !ownerOnly[perm] && contains(allPerms, perm)
	case "support":
		return supportPerms[perm]
	}
	return false
}

// Permissions lists what a role may do (the dashboard hides the rest).
func Permissions(role string) []string {
	var out []string
	for _, p := range allPerms {
		if Allowed(role, p) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
