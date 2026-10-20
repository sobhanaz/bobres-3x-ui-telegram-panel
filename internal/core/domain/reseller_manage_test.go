package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// rsWorld is an install with a plan, a tier, a staff member and a fake panel,
// for the tests of what a reseller does with their services.
type rsWorld struct {
	t     *testing.T
	svc   *Service
	s     *store.Store
	admin *store.User
	tier  *store.ResellerTier
	plan  *store.Plan
	fp    *fakeProvisioner
	w     *ProvisionWorker
	n     int
}

func newRSWorld(t *testing.T) *rsWorld {
	t.Helper()
	svc, s := testService(t)
	admin := staffMember(t, s, 9500, "admin")
	fp := &fakeProvisioner{}
	return &rsWorld{t: t, svc: svc, s: s, admin: admin, fp: fp, w: svc.NewProvisionWorker(fp, nil),
		tier: seedTier(t, svc, admin, "Gold", 20, 10, nil), plan: planWithLimits(t, s, 30, 50<<30)}
}

// reseller makes a new reseller with 10x the plan price in credit.
func (w *rsWorld) reseller() *store.User {
	w.t.Helper()
	w.n++
	return reseller(w.t, w.svc, w.s, w.admin, int64(9510+w.n), w.tier, w.plan.Price*10)
}

// buy has u buy a service on credit and waits for its delivery.
func (w *rsWorld) buy(u *store.User, label string) *store.Subscription {
	w.t.Helper()
	ctx := context.Background()
	w.n++
	o, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: w.plan.ID, IdempotencyKey: fmt.Sprintf("rsw-%d", w.n), Label: label})
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		w.t.Fatalf("pay: %v", err)
	}
	if worked, err := w.w.ProvisionNext(ctx); !worked || err != nil {
		w.t.Fatalf("provision: %v %v", worked, err)
	}
	sub, err := w.s.SubscriptionByOrder(ctx, w.s.Conn(), o.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return sub
}

func (w *rsWorld) status(id string) string {
	w.t.Helper()
	sub, err := w.s.GetSubscription(context.Background(), w.s.Conn(), id)
	if err != nil {
		w.t.Fatal(err)
	}
	return sub.Status
}

// audited returns the audit entries of an action on one entity.
func (w *rsWorld) audited(action, entityID string) []store.AuditRow {
	w.t.Helper()
	rows, _, err := w.s.ListAudit(context.Background(), w.s.Conn(), store.AuditFilter{Action: action, EntityID: entityID}, store.Page{Limit: 50})
	if err != nil {
		w.t.Fatal(err)
	}
	return rows
}

// Who may use the reseller functions: an active reseller whose install's
// entitlements include resellers; nobody else, and nothing says why.
func TestResellerAccess(t *testing.T) {
	w := newRSWorld(t)
	ctx := context.Background()
	r := w.reseller()

	if u, err := w.svc.ResellerAccess(ctx, r.ID); err != nil || u.ID != r.ID {
		t.Fatalf("a reseller: %v %+v", err, u)
	}
	customer := seedUser(t, w.s, 9601)
	for name, tc := range map[string]struct {
		id   string
		want error
	}{
		"an unknown user": {"00000000-0000-0000-0000-000000000099", store.ErrNotFound},
		"a malformed id":  {"nobody", store.ErrNotFound},
		"an empty id":     {"", store.ErrNotFound},
		"a customer":      {customer.ID, ErrForbidden},
		"a staff member":  {w.admin.ID, ErrForbidden},
	} {
		if _, err := w.svc.ResellerAccess(ctx, tc.id); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}

	// A reseller role without terms (never made so) is no reseller.
	odd := seedUser(t, w.s, 9602)
	if err := w.s.SetUserRole(ctx, w.s.Conn(), odd.ID, ResellerRole, "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.ResellerAccess(ctx, odd.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("a reseller role without terms: %v", err)
	}

	// Banned: refused as banned. Back to active: allowed again.
	if err := w.s.SetUserStatus(ctx, w.s.Conn(), r.ID, "banned"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.ResellerAccess(ctx, r.ID); !errors.Is(err, ErrUserBanned) {
		t.Errorf("a banned reseller: %v", err)
	}
	if err := w.s.SetUserStatus(ctx, w.s.Conn(), r.ID, "active"); err != nil {
		t.Fatal(err)
	}

	// Resellers left the entitlements (the trial is over, no license): refused,
	// with the plain forbidden, never a word about licensing.
	endTrial(t, w.s, time.Now())
	_, err := w.svc.ResellerAccess(ctx, r.ID)
	if !errors.Is(err, ErrForbidden) || strings.Contains(strings.ToLower(err.Error()), "licen") {
		t.Errorf("a reseller after the license: %v", err)
	}
	setTrialStart(t, w.s, time.Now())
	if _, err := w.svc.ResellerAccess(ctx, r.ID); err != nil {
		t.Errorf("a reseller with the trial back: %v", err)
	}

	// A former reseller (removed) is a customer again.
	if err := w.svc.RemoveReseller(ctx, w.admin, r.ID, "closed"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.ResellerAccess(ctx, r.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("a former reseller: %v", err)
	}
}

// Pausing and resuming: the panel client is turned off and on, the audit log
// names the reseller (source bot, whatever the caller said), doing either twice
// changes nothing, and resuming reads the real status from the panel.
func TestResellerPausesAndResumes(t *testing.T) {
	w := newRSWorld(t)
	// The caller says "dashboard"; a reseller's entries say bot all the same.
	ctx := store.WithSource(context.Background(), store.SourceDashboard)
	r := w.reseller()
	sub := w.buy(r, "Ali")

	got, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, false)
	if err != nil || got.Status != "disabled" || w.status(sub.ID) != "disabled" {
		t.Fatalf("pause: %v %+v", err, got)
	}
	if ops := w.fp.opsDone(); len(ops) != 1 || ops[0] != sub.ID+"|disable" {
		t.Fatalf("panel calls after the pause: %v", ops)
	}
	rows := w.audited("subscription.disable", sub.ID)
	if len(rows) != 1 || rows[0].ActorID == nil || *rows[0].ActorID != r.ID || rows[0].Source != store.SourceBot ||
		rows[0].ActorRole != ResellerRole || rows[0].Reason != reasonResellerPause {
		t.Fatalf("audit of the pause: %+v", rows)
	}

	// Pausing again: nothing more happens.
	if got, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, false); err != nil || got.Status != "disabled" {
		t.Fatalf("pause again: %v %+v", err, got)
	}
	if len(w.fp.opsDone()) != 1 || len(w.audited("subscription.disable", sub.ID)) != 1 {
		t.Fatalf("a second pause reached the panel or the log: %v", w.fp.opsDone())
	}

	// The panel says the service ran out of time while it was paused: resuming
	// turns it on and shows what it is.
	w.fp.setUsage(sub.ID, Usage{UsedBytes: 1 << 30, TotalBytes: 50 << 30, ExpiresAt: time.Now().Add(-time.Hour).Unix(), Enabled: true})
	got, err = w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, true)
	if err != nil || got.Status != "expired" {
		t.Fatalf("resume: %v %+v", err, got)
	}
	if ops := w.fp.opsDone(); len(ops) != 2 || ops[1] != sub.ID+"|enable" {
		t.Fatalf("panel calls after the resume: %v", ops)
	}
	rows = w.audited("subscription.enable", sub.ID)
	if len(rows) != 1 || *rows[0].ActorID != r.ID || rows[0].Source != store.SourceBot || rows[0].Reason != reasonResellerResume {
		t.Fatalf("audit of the resume: %+v", rows)
	}

	// Resuming a service that runs changes nothing and does not read the panel.
	w.fp.mu.Lock()
	w.fp.usage = nil
	w.fp.mu.Unlock()
	if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, true); err != nil {
		t.Fatalf("resume again: %v", err)
	}
	if len(w.fp.opsDone()) != 2 || len(w.audited("subscription.enable", sub.ID)) != 1 {
		t.Fatalf("a second resume reached the panel or the log: %v", w.fp.opsDone())
	}

	// A panel that does not answer leaves the service as it was.
	w.fp.mu.Lock()
	w.fp.opsErr = errors.New("panel unreachable")
	w.fp.mu.Unlock()
	if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, false); err == nil {
		t.Fatal("pausing with the panel down worked")
	}
	if st := w.status(sub.ID); st != "expired" {
		t.Fatalf("status after a failed pause: %s", st)
	}
	if n := len(w.audited("subscription.disable", sub.ID)); n != 1 {
		t.Fatalf("a failed pause was audited: %d", n)
	}
}

// A service staff paused stays paused: the reseller cannot undo it. A pause
// of the reseller's own they can undo, whoever turned the service on and off
// before.
func TestResellerCannotResumeWhatStaffPaused(t *testing.T) {
	w := newRSWorld(t)
	ctx := context.Background()
	r := w.reseller()
	sub := w.buy(r, "")
	w.fp.setUsage(sub.ID, Usage{TotalBytes: 50 << 30, ExpiresAt: time.Now().Add(20 * 24 * time.Hour).Unix(), Enabled: true})

	if _, err := w.svc.SetSubscriptionEnabled(ctx, w.admin, w.fp, sub.ID, false, "abuse"); err != nil {
		t.Fatal(err)
	}
	_, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, true)
	if !errors.Is(err, ErrPausedByStaff) || !errors.Is(err, ErrState) {
		t.Fatalf("resumed what staff paused: %v", err)
	}
	if st := w.status(sub.ID); st != "disabled" {
		t.Fatalf("status: %s", st)
	}
	if ops := w.fp.opsDone(); len(ops) != 1 || ops[0] != sub.ID+"|disable" {
		t.Fatalf("panel calls: %v", ops)
	}
	// Pausing what is paused is harmless; it does not make it theirs.
	if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, false); err != nil {
		t.Fatalf("pause what staff paused: %v", err)
	}
	if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, true); !errors.Is(err, ErrPausedByStaff) {
		t.Fatalf("resumed after a no-op pause: %v", err)
	}

	// Staff turns it on: the reseller pauses it and resumes it themselves.
	if _, err := w.svc.SetSubscriptionEnabled(ctx, w.admin, w.fp, sub.ID, true, "sorted out"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, true); err != nil || got.Status != "active" {
		t.Fatalf("resume of their own pause: %v %+v", err, got)
	}

	// Staff pauses it again after the reseller's resume: staff's pause wins.
	if _, err := w.svc.SetSubscriptionEnabled(ctx, w.admin, w.fp, sub.ID, false, "again"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, true); !errors.Is(err, ErrPausedByStaff) {
		t.Fatalf("resumed staff's second pause: %v", err)
	}

	// A pause nobody recorded (no audit entry) is left to staff too.
	other := w.buy(r, "")
	if _, err := w.s.DB().Exec(ctx, `UPDATE core.subscriptions SET status = 'disabled' WHERE id = $1`, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, other.ID, true); !errors.Is(err, ErrPausedByStaff) {
		t.Fatalf("resumed an unrecorded pause: %v", err)
	}
}

// Deleting removes the client from the panel and marks the service deleted,
// audited as the reseller's; a second delete is a no-op; no money moves.
func TestResellerDeletesOwnService(t *testing.T) {
	w := newRSWorld(t)
	ctx := context.Background()
	r := w.reseller()
	sub := w.buy(r, "Ali")
	keep := w.buy(r, "Reza")
	balance, sum, rows := ledger(t, w.s, r.ID)

	if err := w.svc.ResellerDeleteService(ctx, w.fp, r.ID, sub.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if w.status(sub.ID) != "deleted" || w.status(keep.ID) == "deleted" {
		t.Fatalf("statuses: %s %s", w.status(sub.ID), w.status(keep.ID))
	}
	if ops := w.fp.opsDone(); len(ops) != 1 || ops[0] != sub.ID+"|delete" {
		t.Fatalf("panel calls: %v", ops)
	}
	audit := w.audited("subscription.delete", sub.ID)
	if len(audit) != 1 || audit[0].ActorID == nil || *audit[0].ActorID != r.ID || audit[0].Source != store.SourceBot ||
		audit[0].ActorRole != ResellerRole || audit[0].Reason != reasonResellerDelete {
		t.Fatalf("audit: %+v", audit)
	}
	// No refund, no credit, no debit.
	if b, s2, n := ledger(t, w.s, r.ID); b != balance || s2 != sum || n != rows {
		t.Fatalf("the ledger moved: %d/%d/%d -> %d/%d/%d", balance, sum, rows, b, s2, n)
	}

	// Again: still fine, nothing more happens.
	if err := w.svc.ResellerDeleteService(ctx, w.fp, r.ID, sub.ID); err != nil {
		t.Fatalf("delete again: %v", err)
	}
	if len(w.fp.opsDone()) != 1 || len(w.audited("subscription.delete", sub.ID)) != 1 {
		t.Fatalf("a second delete reached the panel or the log: %v", w.fp.opsDone())
	}
	// A deleted service is gone for the other powers.
	if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, false); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("pausing a deleted service: %v", err)
	}
	if _, err := w.svc.ResellerSetNote(ctx, r.ID, sub.ID, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a note on a deleted service: %v", err)
	}

	// A panel that does not answer: the service stays, nothing is audited.
	w.fp.mu.Lock()
	w.fp.opsErr = errors.New("panel unreachable")
	w.fp.mu.Unlock()
	if err := w.svc.ResellerDeleteService(ctx, w.fp, r.ID, keep.ID); err == nil {
		t.Fatal("deleting with the panel down worked")
	}
	if w.status(keep.ID) == "deleted" || len(w.audited("subscription.delete", keep.ID)) != 0 {
		t.Fatal("a failed delete left a trace")
	}
	w.fp.mu.Lock()
	w.fp.opsErr = nil
	w.fp.mu.Unlock()

	// A renewal still being applied holds the delete back, as it does staff's.
	ro, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: w.plan.ID, Type: OrderRenew, SubscriptionID: keep.ID, IdempotencyKey: "rsw-renew"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.PayOrderWithWallet(ctx, ro.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.ResellerDeleteService(ctx, w.fp, r.ID, keep.ID); !errors.Is(err, ErrState) {
		t.Fatalf("deleted a service with a renewal waiting: %v", err)
	}
}

// A service still being delivered is not deleted or paused (staff's rules),
// but can be given a note already.
func TestResellerPendingService(t *testing.T) {
	w := newRSWorld(t)
	ctx := context.Background()
	r := w.reseller()
	sub := w.seedServiceRow(r, "Soon", "pending", 0, 0, 0)

	if err := w.svc.ResellerDeleteService(ctx, w.fp, r.ID, sub.ID); !errors.Is(err, ErrState) {
		t.Fatalf("deleting a pending service: %v", err)
	}
	if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, false); !errors.Is(err, ErrState) {
		t.Fatalf("pausing a pending service: %v", err)
	}
	if got, err := w.svc.ResellerSetNote(ctx, r.ID, sub.ID, "call him"); err != nil || got.Note == nil || *got.Note != "call him" {
		t.Fatalf("a note on a pending service: %v %+v", err, got)
	}
	if len(w.fp.opsDone()) != 0 || w.status(sub.ID) != "pending" {
		t.Fatalf("something changed: %v %s", w.fp.opsDone(), w.status(sub.ID))
	}
}

// Every power is for the reseller's own services: another reseller's, a
// customer's, a missing one and a malformed id are all "not found", and
// nothing changes (no panel call, no audit entry, no note).
func TestResellerCannotTouchOthersServices(t *testing.T) {
	w := newRSWorld(t)
	ctx := context.Background()
	mine, theirs := w.reseller(), w.reseller()
	mineSub := w.buy(mine, "mine")
	theirSub := w.buy(theirs, "theirs")
	customer := seedUser(t, w.s, 9701)
	fund(t, w.s, customer.ID, w.plan.Price)
	custSub := w.buy(customer, "")
	missing := "00000000-0000-0000-0000-0000000000aa"

	for name, id := range map[string]string{"another reseller's": theirSub.ID, "a customer's": custSub.ID, "a missing one": missing, "a malformed id": "not-an-id", "an empty id": ""} {
		if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, mine.ID, id, false); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("pause %s: %v", name, err)
		}
		if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, mine.ID, id, true); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("resume %s: %v", name, err)
		}
		if err := w.svc.ResellerDeleteService(ctx, w.fp, mine.ID, id); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("delete %s: %v", name, err)
		}
		if _, err := w.svc.ResellerSetNote(ctx, mine.ID, id, "x"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("note on %s: %v", name, err)
		}
		if _, err := w.svc.ResellerSetLabel(ctx, mine.ID, id, "x"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("name %s: %v", name, err)
		}
	}
	if len(w.fp.opsDone()) != 0 {
		t.Fatalf("the panel was called for a service that is not theirs: %v", w.fp.opsDone())
	}
	for _, a := range []string{"subscription.disable", "subscription.enable", "subscription.delete"} {
		if n := auditCount(t, w.s, a); n != 0 {
			t.Errorf("%s was audited %d times", a, n)
		}
	}
	for _, sub := range []*store.Subscription{theirSub, custSub} {
		got, _ := w.s.GetSubscription(ctx, w.s.Conn(), sub.ID)
		if got.Status != sub.Status || got.Note != nil || (got.Label != nil && *got.Label == "x") {
			t.Errorf("a service that is not theirs changed: %+v", got)
		}
	}
	// A customer cannot use the reseller functions on their own service.
	if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, customer.ID, custSub.ID, false); !errors.Is(err, ErrForbidden) {
		t.Errorf("a customer paused their service: %v", err)
	}
	if err := w.svc.ResellerDeleteService(ctx, w.fp, customer.ID, custSub.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("a customer deleted their service: %v", err)
	}
	// And their own work still works.
	if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, mine.ID, mineSub.ID, false); err != nil {
		t.Fatalf("their own: %v", err)
	}
}

// A banned reseller, a former reseller and a reseller after resellers left
// the license can do nothing, and nothing changes.
func TestResellerPowersNeedAnActiveResellerAndTheLicense(t *testing.T) {
	w := newRSWorld(t)
	ctx := context.Background()
	r := w.reseller()
	sub := w.buy(r, "x")

	attempts := func(who string, want error) {
		t.Helper()
		if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, false); !errors.Is(err, want) {
			t.Errorf("%s: pause: %v", who, err)
		}
		if _, err := w.svc.ResellerSetServiceEnabled(ctx, w.fp, r.ID, sub.ID, true); !errors.Is(err, want) {
			t.Errorf("%s: resume: %v", who, err)
		}
		if err := w.svc.ResellerDeleteService(ctx, w.fp, r.ID, sub.ID); !errors.Is(err, want) {
			t.Errorf("%s: delete: %v", who, err)
		}
		if _, err := w.svc.ResellerSetNote(ctx, r.ID, sub.ID, "n"); !errors.Is(err, want) {
			t.Errorf("%s: note: %v", who, err)
		}
		if _, err := w.svc.ResellerSetLabel(ctx, r.ID, sub.ID, "n"); !errors.Is(err, want) {
			t.Errorf("%s: name: %v", who, err)
		}
		if _, err := w.svc.ListResellerServices(ctx, r.ID, ResellerServiceFilter{}, store.Page{}); !errors.Is(err, want) {
			t.Errorf("%s: list: %v", who, err)
		}
		if _, err := w.svc.ListInvitedCustomers(ctx, r.ID, store.Page{}); !errors.Is(err, want) {
			t.Errorf("%s: invited customers: %v", who, err)
		}
		if len(w.fp.opsDone()) != 0 || w.status(sub.ID) != "active" {
			t.Errorf("%s: something changed: %v %s", who, w.fp.opsDone(), w.status(sub.ID))
		}
		if got, _ := w.s.GetSubscription(ctx, w.s.Conn(), sub.ID); got.Note != nil || *got.Label != "x" {
			t.Errorf("%s: the note or name changed: %+v", who, got)
		}
	}

	endTrial(t, w.s, time.Now())
	attempts("after the license", ErrForbidden)
	setTrialStart(t, w.s, time.Now())

	if err := w.s.SetUserStatus(ctx, w.s.Conn(), r.ID, "banned"); err != nil {
		t.Fatal(err)
	}
	attempts("banned", ErrUserBanned)
	if err := w.s.SetUserStatus(ctx, w.s.Conn(), r.ID, "active"); err != nil {
		t.Fatal(err)
	}

	// They owe the price of the service; settle it, then remove them.
	if _, _, err := w.svc.RecordResellerPayment(ctx, w.admin, r.ID, w.plan.Price*80/100, "IRT", "paid", "settle-former-reseller"); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.RemoveReseller(ctx, w.admin, r.ID, "closed"); err != nil {
		t.Fatal(err)
	}
	attempts("a former reseller", ErrForbidden)
	if n := auditCount(t, w.s, "subscription.disable") + auditCount(t, w.s, "subscription.delete"); n != 0 {
		t.Fatalf("audit entries: %d", n)
	}
}

func TestResellerNotes(t *testing.T) {
	w := newRSWorld(t)
	ctx := context.Background()
	r := w.reseller()
	sub := w.buy(r, "Ali")

	got, err := w.svc.ResellerSetNote(ctx, r.ID, sub.ID, "  pays on the 5th, Ali's brother  ")
	if err != nil || got.Note == nil || *got.Note != "pays on the 5th, Ali's brother" {
		t.Fatalf("set: %v %+v", err, got)
	}
	if again, _ := w.s.GetSubscription(ctx, w.s.Conn(), sub.ID); again.Note == nil || *again.Note != "pays on the 5th, Ali's brother" ||
		again.Label == nil || *again.Label != "Ali" {
		t.Fatalf("stored: %+v", again)
	}
	// The same twice is the same.
	if _, err := w.svc.ResellerSetNote(ctx, r.ID, sub.ID, "pays on the 5th, Ali's brother"); err != nil {
		t.Fatal(err)
	}
	// 200 characters of Persian fit; 201 do not; two lines and control characters do not.
	long := strings.Repeat("ب", MaxNote)
	if got, err := w.svc.ResellerSetNote(ctx, r.ID, sub.ID, long); err != nil || *got.Note != long {
		t.Fatalf("200 characters: %v", err)
	}
	for name, note := range map[string]string{"too long": long + "ب", "two lines": "a\nb", "a tab": "a\tb", "a bell": "a\x07b", "bad utf-8": "a\xffb"} {
		if _, err := w.svc.ResellerSetNote(ctx, r.ID, sub.ID, note); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if again, _ := w.s.GetSubscription(ctx, w.s.Conn(), sub.ID); *again.Note != long {
		t.Fatalf("a refused note changed the stored one: %q", *again.Note)
	}
	// Empty clears it.
	if got, err := w.svc.ResellerSetNote(ctx, r.ID, sub.ID, "   "); err != nil || got.Note != nil {
		t.Fatalf("clear: %v %+v", err, got)
	}
	if again, _ := w.s.GetSubscription(ctx, w.s.Conn(), sub.ID); again.Note != nil {
		t.Fatalf("not cleared: %q", *again.Note)
	}
}

// seedServiceRow gives u a service in a status, named label, created offset
// ago (so the order of a list is known).
func (w *rsWorld) seedServiceRow(u *store.User, label, status string, ago time.Duration, used, total int64) *store.Subscription {
	w.t.Helper()
	ctx := context.Background()
	w.n++
	o, _, err := w.s.CreateOrder(ctx, w.s.Conn(), &store.Order{UserID: u.ID, PlanID: w.plan.ID, Type: "new", Status: "active",
		Amount: 1, Currency: "IRT", IdempotencyKey: fmt.Sprintf("seed-row-%d", w.n)})
	if err != nil {
		w.t.Fatal(err)
	}
	var name *string
	if label != "" {
		name = &label
	}
	sub, err := w.s.CreateSubscription(ctx, w.s.Conn(), &store.Subscription{UserID: u.ID, OrderID: o.ID, ClientEmail: fmt.Sprintf("seed-row-%d", w.n),
		Label: name, TrafficUsed: used})
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.s.DB().Exec(ctx, `UPDATE core.subscriptions SET status = $2, created_at = now() - make_interval(secs => $3),
		traffic_total_bytes = NULLIF($4::bigint, 0) WHERE id = $1`, sub.ID, status, ago.Seconds(), total); err != nil {
		w.t.Fatal(err)
	}
	return sub
}

func labelsOf(subs []store.Subscription) []string {
	out := make([]string, len(subs))
	for i := range subs {
		out[i] = *subs[i].Label
	}
	return out
}

// The reseller's list: their own services only, newest first, filtered by the
// status buckets and searched by name or number, paged, with the counts per
// status (not changed by the filter) and each service's usage.
func TestListResellerServices(t *testing.T) {
	w := newRSWorld(t)
	ctx := context.Background()
	r, other := w.reseller(), w.reseller()
	h := time.Hour
	w.seedServiceRow(r, "a-active-1", "active", 10*h, 10<<30, 50<<30)
	w.seedServiceRow(r, "b-active-2", "active", 9*h, 0, 0) // unlimited traffic
	w.seedServiceRow(r, "c-expiring", "expiring_soon", 8*h, 49<<30, 50<<30)
	w.seedServiceRow(r, "d-expired", "expired", 7*h, 1, 50<<30)
	w.seedServiceRow(r, "e-depleted", "depleted", 6*h, 50<<30, 50<<30)
	w.seedServiceRow(r, "f-paused", "disabled", 5*h, 0, 50<<30)
	w.seedServiceRow(r, "g-pending", "pending", 4*h, 0, 0)
	w.seedServiceRow(r, "h-deleted", "deleted", 3*h, 0, 0)
	w.seedServiceRow(other, "z-theirs", "active", 2*h, 0, 0)
	custom := seedUser(t, w.s, 9801)
	w.seedServiceRow(custom, "z-a-customers", "active", h, 0, 0)

	list := func(f ResellerServiceFilter, p store.Page) *ResellerServices {
		t.Helper()
		got, err := w.svc.ListResellerServices(ctx, r.ID, f, p)
		if err != nil {
			t.Fatalf("list %+v: %v", f, err)
		}
		return got
	}

	all := list(ResellerServiceFilter{}, store.Page{Limit: 50})
	want := store.ServiceCounts{Total: 7, Active: 2, ExpiringSoon: 1, Expired: 2, Paused: 1, Pending: 1}
	if all.Total != 7 || len(all.Items) != 7 || all.Counts != want {
		t.Fatalf("all: total %d, %d items, counts %+v", all.Total, len(all.Items), all.Counts)
	}
	if got := strings.Join(labelsOf(all.Items), " "); got != "g-pending f-paused e-depleted d-expired c-expiring b-active-2 a-active-1" {
		t.Fatalf("order (newest first): %s", got)
	}
	// Usage rides on each service.
	for _, sub := range all.Items {
		if *sub.Label == "c-expiring" && (sub.TrafficUsed != 49<<30 || sub.TrafficTotal == nil || *sub.TrafficTotal != 50<<30) {
			t.Errorf("usage of c-expiring: %d of %v", sub.TrafficUsed, sub.TrafficTotal)
		}
		if *sub.Label == "b-active-2" && sub.TrafficTotal != nil {
			t.Errorf("an unlimited service has a total: %v", *sub.TrafficTotal)
		}
	}

	for status, wantNames := range map[string]string{
		"active":        "b-active-2 a-active-1",
		"expiring_soon": "c-expiring",
		"expired":       "e-depleted d-expired", // time and traffic both
		"paused":        "f-paused",
		"pending":       "g-pending",
	} {
		got := list(ResellerServiceFilter{Status: status}, store.Page{Limit: 50})
		if strings.Join(labelsOf(got.Items), " ") != wantNames || got.Total != int64(len(got.Items)) || got.Counts != want {
			t.Errorf("%s: %v (total %d, counts %+v)", status, labelsOf(got.Items), got.Total, got.Counts)
		}
	}

	// Search by name (any case) and by number, alone and with a status.
	if got := list(ResellerServiceFilter{Query: "  ACTIVE "}, store.Page{}); strings.Join(labelsOf(got.Items), " ") != "b-active-2 a-active-1" || got.Total != 2 {
		t.Errorf("search by name: %v", labelsOf(got.Items))
	}
	if got := list(ResellerServiceFilter{Query: "ed", Status: "expired"}, store.Page{}); strings.Join(labelsOf(got.Items), " ") != "e-depleted d-expired" {
		t.Errorf("search in a status: %v", labelsOf(got.Items))
	}
	if got := list(ResellerServiceFilter{Query: "ed", Status: "active"}, store.Page{}); len(got.Items) != 0 || got.Total != 0 || got.Counts != want {
		t.Errorf("search that matches nothing in a status: %v", labelsOf(got.Items))
	}
	target := all.Items[4] // c-expiring
	short := strings.ReplaceAll(target.ID, "-", "")[26:]
	if got := list(ResellerServiceFilter{Query: "#" + short}, store.Page{}); len(got.Items) != 1 || got.Items[0].ID != target.ID {
		t.Errorf("search by number: %v", labelsOf(got.Items))
	}
	// A search is a pattern of its own: % and _ match themselves only.
	if got := list(ResellerServiceFilter{Query: "%"}, store.Page{}); len(got.Items) != 0 {
		t.Errorf("a wildcard matched: %v", labelsOf(got.Items))
	}

	// Paging: the total is the matches, not the page.
	p1 := list(ResellerServiceFilter{}, store.Page{Limit: 3})
	p3 := list(ResellerServiceFilter{}, store.Page{Limit: 3, Offset: 6})
	if p1.Total != 7 || len(p1.Items) != 3 || len(p3.Items) != 1 || *p3.Items[0].Label != "a-active-1" {
		t.Errorf("paging: %v %v", labelsOf(p1.Items), labelsOf(p3.Items))
	}

	// Bad input.
	if _, err := w.svc.ListResellerServices(ctx, r.ID, ResellerServiceFilter{Status: "deleted"}, store.Page{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("status deleted: %v", err)
	}
	if _, err := w.svc.ListResellerServices(ctx, r.ID, ResellerServiceFilter{Status: "bogus"}, store.Page{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown status: %v", err)
	}
	if _, err := w.svc.ListResellerServices(ctx, r.ID, ResellerServiceFilter{Query: strings.Repeat("ج", 101)}, store.Page{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("long search: %v", err)
	}
	// The other reseller sees theirs only.
	if got, err := w.svc.ListResellerServices(ctx, other.ID, ResellerServiceFilter{}, store.Page{}); err != nil || len(got.Items) != 1 || *got.Items[0].Label != "z-theirs" || got.Counts.Total != 1 {
		t.Errorf("another reseller's list: %v %+v", err, got)
	}
}

// Whoever joined through THIS reseller's link is listed, newest first, with
// what they bought and what it paid the reseller; nobody else is.
func TestListInvitedCustomers(t *testing.T) {
	w := newRSWorld(t)
	ctx := context.Background()
	r, other := w.reseller(), w.reseller()
	plan := w.plan // 1000 Toman, 10% commission on Gold

	invite := func(inviter *store.User, tg int64, username string, joined time.Time) *store.User {
		t.Helper()
		u, err := w.s.UpsertUser(ctx, w.s.Conn(), tg, username, "fa", inviter.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.s.DB().Exec(ctx, `UPDATE core.users SET created_at = $2 WHERE id = $1`, u.ID, joined); err != nil {
			t.Fatal(err)
		}
		return u
	}
	now := time.Now()
	c1 := invite(r, 9901, "ali_shop", now.Add(-72*time.Hour))
	invite(r, 9902, "", now.Add(-48*time.Hour)) // no @username
	c3 := invite(r, 9903, "newest", now.Add(-24*time.Hour))
	c4 := invite(other, 9904, "theirs_only", now.Add(-12*time.Hour))
	stranger := seedUser(t, w.s, 9905) // joined through nobody
	for _, u := range []*store.User{c1, c3, c4, stranger} {
		fund(t, w.s, u.ID, plan.Price*10)
	}

	pay := func(u *store.User, key string) *store.Order {
		t.Helper()
		o, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
			t.Fatal(err)
		}
		return o
	}
	// c1 buys twice (each pays the reseller 10%), and has a refunded order, an
	// unpaid one and a free one that count for nothing.
	pay(c1, "inv-c1-a")
	pay(c1, "inv-c1-b")
	refunded := pay(c1, "inv-c1-c")
	if _, err := w.svc.RefundOrder(ctx, w.admin, w.fp, refunded.ID, "mistake"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: c1.ID, PlanID: plan.ID, IdempotencyKey: "inv-c1-unpaid"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.s.CreateOrder(ctx, w.s.Conn(), &store.Order{UserID: c1.ID, PlanID: plan.ID, Type: "new", Status: "paid", Amount: 0,
		Currency: "IRT", IdempotencyKey: "inv-c1-free"}); err != nil {
		t.Fatal(err)
	}
	// c3 bought once; c2 never; c4 (the other reseller's) bought, and the stranger too.
	pay(c3, "inv-c3-a")
	pay(c4, "inv-c4-a")
	pay(stranger, "inv-s-a")

	got, err := w.svc.ListInvitedCustomers(ctx, r.ID, store.Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 3 || len(got.Items) != 3 {
		t.Fatalf("customers: total %d, %d items", got.Total, len(got.Items))
	}
	byTG := map[int64]store.InvitedCustomer{}
	var order []int64
	for _, c := range got.Items {
		byTG[c.TelegramID] = c
		order = append(order, c.TelegramID)
	}
	if fmt.Sprint(order) != "[9903 9902 9901]" {
		t.Fatalf("newest first: %v", order)
	}
	if _, leaked := byTG[9904]; leaked {
		t.Fatal("another reseller's customer is listed")
	}
	if _, leaked := byTG[9905]; leaked {
		t.Fatal("someone who joined through nobody is listed")
	}

	// c1: the refund, the unpaid order and the free order count for nothing.
	// Two purchases at 1000 Toman, a 10% commission each (the refunded one paid
	// its commission before the refund and the ledger keeps it).
	c := byTG[9901]
	if c.Username != "ali_shop" || c.PaidOrders != 2 || c.Spent["IRT"] != 2000 || len(c.Spent) != 1 || !c.JoinedAt.Before(now.Add(-71*time.Hour)) {
		t.Errorf("c1: %+v", c)
	}
	if c.Commission["IRT"] != 300 {
		t.Errorf("c1 commission: %v (three purchases paid 100 each)", c.Commission)
	}
	if c := byTG[9902]; c.Username != "" || c.PaidOrders != 0 || len(c.Spent) != 0 || len(c.Commission) != 0 {
		t.Errorf("c2: %+v", c)
	}
	if c := byTG[9903]; c.PaidOrders != 1 || c.Spent["IRT"] != 1000 || c.Commission["IRT"] != 100 {
		t.Errorf("c3: %+v", c)
	}
	// What the reseller earned in all is the sum of what the list shows.
	earned, err := w.s.CommissionEarned(ctx, w.s.Conn(), r.ID)
	if err != nil || earned["IRT"] != 400 {
		t.Fatalf("earned: %v %v", err, earned)
	}

	// Paging: the total is everyone, a page is a slice.
	p2, err := w.svc.ListInvitedCustomers(ctx, r.ID, store.Page{Limit: 2, Offset: 2})
	if err != nil || p2.Total != 3 || len(p2.Items) != 1 || p2.Items[0].TelegramID != 9901 {
		t.Fatalf("page 2: %v %+v", err, p2)
	}
	if past, err := w.svc.ListInvitedCustomers(ctx, r.ID, store.Page{Limit: 2, Offset: 20}); err != nil || past.Total != 3 || len(past.Items) != 0 {
		t.Fatalf("past the end: %v %+v", err, past)
	}
	// The other reseller sees theirs only; a reseller with nobody sees none.
	if o, err := w.svc.ListInvitedCustomers(ctx, other.ID, store.Page{}); err != nil || o.Total != 1 || o.Items[0].TelegramID != 9904 || o.Items[0].Commission["IRT"] != 100 {
		t.Fatalf("the other reseller's customers: %v %+v", err, o)
	}
	third := w.reseller()
	if o, err := w.svc.ListInvitedCustomers(ctx, third.ID, store.Page{}); err != nil || o.Total != 0 || len(o.Items) != 0 {
		t.Fatalf("a reseller nobody joined: %v %+v", err, o)
	}
}
