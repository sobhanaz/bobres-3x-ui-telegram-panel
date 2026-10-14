package license

import (
	"strings"
	"testing"
)

// A limit of 0 (or one that was left out) is raised to the free tier's by the
// install, and the vendor is told when a license is made that way.
func TestFloorWarnings(t *testing.T) {
	for name, c := range map[string]struct {
		e    Entitlements
		want bool
	}{
		"missing":   {Entitlements{MaxServers: 3}, true},
		"zero":      {Entitlements{MaxActiveServices: 0}, true},
		"a count":   {Entitlements{MaxActiveServices: 500}, false},
		"unlimited": {Entitlements{MaxActiveServices: Unlimited}, false},
	} {
		w := c.e.FloorWarnings()
		if (len(w) == 1) != c.want || len(w) > 1 {
			t.Errorf("%s: %q", name, w)
		}
		if c.want && !strings.Contains(w[0], "max_active_services is 0 or missing: the install will floor it at the free tier's 25") {
			t.Errorf("%s: %q", name, w)
		}
	}
	// What is said is what the policy does.
	if got := (Entitlements{}).AtLeast(FreeEntitlements()).MaxActiveServices; got != FreeMaxActiveServices {
		t.Errorf("the floor is %d", got)
	}
	// None of the tier presets trips it.
	p, err := LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range p.Names() {
		if tier, _ := p.Get(n); len(tier.Entitlements.FloorWarnings()) != 0 {
			t.Errorf("preset %s: %q", n, tier.Entitlements.FloorWarnings())
		}
	}
}
