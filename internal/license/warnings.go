package license

import "fmt"

// FloorWarnings tells the vendor where a license it is about to issue leaves
// a limit that the install will not honour: the policy never lets a license
// sit below the free tier (Entitlements.AtLeast), so a limit of 0, or one the
// payload forgot, is raised to the free tier's. The vendor tools print each
// line as a WARNING and carry on: the license is still valid, it just does not
// say what its maker may have meant.
func (e Entitlements) FloorWarnings() []string {
	var w []string
	if e.MaxActiveServices == 0 {
		w = append(w, fmt.Sprintf("max_active_services is 0 or missing: the install will floor it at the free tier's %d", FreeMaxActiveServices))
	}
	return w
}
