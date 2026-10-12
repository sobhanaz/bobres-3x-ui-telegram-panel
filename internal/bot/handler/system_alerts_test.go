package handler

import (
	"context"
	"testing"

	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
)

// stubHealth reports the payments service in one state.
type stubHealth struct{ status string }

func (s *stubHealth) Check(context.Context) syshealth.Report {
	return syshealth.Report{Services: []syshealth.Service{{Name: "payments", Status: s.status, Detail: "does not answer"}}}
}

// Core's watchdog finds a problem and the bot tells the staff who get
// alerts about failed deliveries, each in their language, never support
// staff; then that it is fixed. notify.system_alerts switches it off.
func TestSystemAlertsReachStaff(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	adm := w.person(861, "ali")
	adm.text("/start")
	adm.press("lang:fa")
	w.role(861, "admin")
	sup := w.person(862, "sara")
	sup.text("/start")
	sup.press("lang:en")
	w.role(862, "support")
	owner.text("/set notify.recipients staff")

	health := &stubHealth{status: syshealth.StatusDown}
	dom := coredomain.New(w.stack.Store, coredomain.Config{OwnerTelegramID: ownerID})
	watch := dom.NewWatchdog(coredomain.WatchdogSources{Health: health}, nil)
	// The watchdog runs on the real clock here: a problem is fixed the second
	// time it passes, and may be announced again right after its fix.
	watch.FixAfter, watch.QuietAfter = 0, 0
	step := func() {
		t.Helper()
		if err := watch.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	step() // seen once: not announced yet
	w.drain()
	if owner.count("System alert") != 0 {
		t.Fatal("a problem seen once was announced")
	}
	step()
	owner.eventuallySees("🚨 <b>System alert</b>\nThe payments service is down.\n<code>does not answer</code>")
	adm.eventuallySees("🚨 <b>هشدار سیستم</b>\nسرویس payments از کار افتاده است.")
	step()
	w.drain()
	if sup.count("payments service") != 0 || owner.count("payments service") != 1 {
		t.Fatalf("support: %d alerts, owner: %d", sup.count("payments service"), owner.count("payments service"))
	}

	health.status = syshealth.StatusOK
	step() // passing: still open
	w.drain()
	if owner.count("Fixed") != 0 {
		t.Fatal("fixed the first time it passed")
	}
	step()
	owner.eventuallySees("✅ <b>Fixed after 1 min</b>\nThe payments service is down.")
	adm.eventuallySees("✅ <b>پس از ۱ دقیقه برطرف شد</b>")

	owner.text("/set notify.system_alerts false")
	owner.sees("Saved")
	health.status = syshealth.StatusDown
	step()
	step()
	w.drain()
	if n := owner.count("payments service"); n != 2 {
		t.Fatalf("switched off, the owner has %d messages about payments", n)
	}
}
