package main

import "testing"

// The daily sales summary is for who may read the reports: core is given the
// dashboard's permission table through this function (the domain's tests use a
// stand-in), so this pins the real one. Revenue is for owners and admins; support
// staff, customers and a role nobody knows get nothing.
func TestMayReadReportsIsTheDashboardsPermissionTable(t *testing.T) {
	for role, want := range map[string]bool{
		"owner": true, "admin": true,
		"support": false, "user": false, "reseller": false, "": false, "OWNER": false, "superadmin": false,
	} {
		if got := mayReadReports(role); got != want {
			t.Errorf("role %q may read the reports: %v, want %v", role, got, want)
		}
	}
}
