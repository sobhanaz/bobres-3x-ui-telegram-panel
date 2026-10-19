package domain

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// A server may name several owners (root admins). The ids are cleaned (no
// zero, no repeat) and keep their order: the first is the main owner.
func TestOwnerIDsAreCleanedAndKeepTheirOrder(t *testing.T) {
	svc, s := testService(t)
	got := New(s, Config{OwnerTelegramIDs: []int64{30, 0, 10, 30, -4, 20}})
	if want := []int64{30, 10, 20}; !slices.Equal(got.OwnerIDs(), want) || got.PrimaryOwnerID() != 30 {
		t.Fatalf("owners %v, primary %d; want %v, 30", got.OwnerIDs(), got.PrimaryOwnerID(), want)
	}
	ids := got.OwnerIDs()
	ids[0] = 99 // a copy: changing it leaves the service alone
	if got.PrimaryOwnerID() != 30 {
		t.Fatal("OwnerIDs handed out the service's own slice")
	}
	none := New(s, Config{})
	if none.OwnerConfigured() || none.PrimaryOwnerID() != 0 || len(none.OwnerIDs()) != 0 {
		t.Fatalf("no owner named: %v %d", none.OwnerIDs(), none.PrimaryOwnerID())
	}
	if !svc.OwnerConfigured() || svc.PrimaryOwnerID() != ownerTG {
		t.Fatalf("one owner named: %v", svc.OwnerIDs())
	}
}

// Every configured owner becomes an owner on first contact, and a visit from
// someone not on the list changes nothing.
func TestEveryConfiguredOwnerIsPromotedOnFirstContact(t *testing.T) {
	_, s := testService(t)
	ctx := context.Background()
	svc := New(s, Config{OwnerTelegramIDs: []int64{ownerTG, 5001, 5002}})
	for _, tg := range []int64{ownerTG, 5001, 5002} {
		u, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: tg})
		if err != nil || u.Role != "owner" || u.Status != "active" || !svc.IsConfiguredOwner(u) {
			t.Fatalf("owner %d: %v %+v", tg, err, u)
		}
	}
	if n := auditCount(t, s, "staff.owner_bootstrap"); n != 3 {
		t.Fatalf("bootstrap audit entries: %d, want 3", n)
	}
	stranger, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: 5003})
	if err != nil || stranger.Role != "user" || svc.IsConfiguredOwner(stranger) {
		t.Fatalf("someone not on the list: %v %+v", err, stranger)
	}
	if svc.IsConfiguredOwner(nil) {
		t.Fatal("nil is nobody's owner")
	}
}

// At start-up core promotes the owners who already have an account, all of
// them, so a new id added with bobres admin owners works without a visit.
func TestEnsureOwnerPromotesEveryExistingAccount(t *testing.T) {
	_, s := testService(t)
	ctx := context.Background()
	a, b, c := seedUser(t, s, 5101), seedUser(t, s, 5102), seedUser(t, s, 5103)
	svc := New(s, Config{OwnerTelegramIDs: []int64{5101, 5102, 5199}}) // 5199 never started the bot
	if err := svc.EnsureOwner(ctx); err != nil {
		t.Fatal(err)
	}
	for u, want := range map[*store.User]string{a: "owner", b: "owner", c: "user"} {
		got, err := s.GetUser(ctx, s.Conn(), u.ID)
		if err != nil || got.Role != want {
			t.Fatalf("telegram %d: %v %+v, want role %s", u.TelegramID, err, got, want)
		}
	}
}

// Any configured owner, not only the first, is a root of trust: they can give
// the owner role to someone, and nobody can change any of them (the owners are
// set in the server settings), not even another configured owner.
func TestAnyConfiguredOwnerGrantsOwnerAndNoneCanBeEdited(t *testing.T) {
	_, s := testService(t)
	ctx := context.Background()
	svc := New(s, Config{OwnerTelegramIDs: []int64{ownerTG, 5001}})
	first, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: ownerTG})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: 5001})
	if err != nil {
		t.Fatal(err)
	}
	if !svc.CanGrantOwner(first) || !svc.CanGrantOwner(second) {
		t.Fatal("every configured owner may grant the owner role")
	}
	// An owner made by another owner (not on the list) may not.
	partner := seedUser(t, s, 5002)
	added, err := svc.AddStaff(ctx, second, partner.ID, "owner", "a partner")
	if err != nil || added.Role != "owner" {
		t.Fatalf("the second configured owner adds an owner: %v %+v", err, added)
	}
	if svc.CanGrantOwner(added) {
		t.Fatal("an owner added by an owner is not a root of trust")
	}
	if _, err := svc.AddStaff(ctx, added, seedUser(t, s, 5003).ID, "owner", "x"); !errors.Is(err, ErrState) {
		t.Fatalf("a non-configured owner adds an owner: %v", err)
	}
	// No one edits a configured owner, whoever asks, themselves included.
	for _, target := range []*store.User{first, second} {
		if _, err := svc.SetStaffRole(ctx, second, target.ID, "admin", "demote"); !errors.Is(err, ErrState) {
			t.Fatalf("demoting configured owner %d: %v", target.TelegramID, err)
		}
	}
}

// The main owner takes no staff seat; every other owner takes one, a second
// configured owner included (the free tier has 2 seats, the trial 5).
func TestOnlyTheMainOwnerTakesNoSeat(t *testing.T) {
	_, s := testService(t)
	ctx := context.Background()
	svc := New(s, Config{OwnerTelegramIDs: []int64{ownerTG, 5001, 5002}})
	for _, tg := range []int64{ownerTG, 5001, 5002} {
		if _, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: tg}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.CountStaffSeats(ctx, s.Conn(), svc.PrimaryOwnerID()); err != nil || n != 2 {
		t.Fatalf("seats with three configured owners: %d %v, want 2", n, err)
	}
	if u, err := svc.LicenseUsage(ctx, nil, nil); err != nil || u.StaffSeats != 2 {
		t.Fatalf("license usage: %+v %v", u, err)
	}
}

// bobres admin password serves the main owner unless it is told another
// owner's id, and refuses an id the server does not list.
func TestInitialPasswordForAChosenOwner(t *testing.T) {
	_, s := testService(t)
	ctx := context.Background()
	svc := New(s, Config{OwnerTelegramIDs: []int64{ownerTG, 5001}})
	login, err := svc.IssueInitialPassword(ctx, 0)
	if err != nil || login.Username != "owner" || login.Password == "" {
		t.Fatalf("the main owner by default: %v %+v", err, login)
	}
	main, err := s.GetUserByTelegramID(ctx, s.Conn(), ownerTG)
	if err != nil {
		t.Fatal(err)
	}
	if creds, err := s.CredentialsFor(ctx, s.Conn(), main.ID); err != nil || !creds.MustChange {
		t.Fatalf("the main owner's login: %+v %v", creds, err)
	}
	if _, err := s.GetUserByTelegramID(ctx, s.Conn(), 5001); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the second owner has no account yet: %v", err)
	}
	second, err := svc.IssueInitialPassword(ctx, 5001)
	if err != nil || second.Username == login.Username || second.Password == login.Password {
		t.Fatalf("the second owner by id: %v %+v (the first got %+v)", err, second, login)
	}
	u, err := s.GetUserByTelegramID(ctx, s.Conn(), 5001)
	if err != nil || u.Role != "owner" {
		t.Fatalf("the second owner's account: %+v %v", u, err)
	}
	if _, err := svc.IssueInitialPassword(ctx, 77); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an id the server does not list: %v", err)
	}
	if _, err := New(s, Config{}).IssueInitialPassword(ctx, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no owner named: %v", err)
	}
}

// One payment as the staff see it: waiting (with its duplicate warning),
// decided (by whom and when), missing, or not a manual payment.
func TestAdminGetPayment(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 7001, "admin")
	partner := staffMember(t, s, 7002, "owner")
	payer := seedUser(t, s, 7003)
	customer := seedUser(t, s, 7004)
	when := time.Now().Add(-time.Hour).Truncate(time.Second)
	decidedAt := when.Add(10 * time.Minute)

	pay := &fakePayments{
		pending: []PendingPayment{{IntentID: "i-wait", OrderID: "o-1", UserID: payer.ID, Provider: "manual_card", Amount: 150000,
			Currency: "IRT", ReceiptFile: "file-1", ReferenceNumber: "R1", SubmittedAt: when, PossibleDuplicate: true}},
		history: []PaymentRecord{{IntentID: "i-done", OrderID: "o-2", UserID: payer.ID, Provider: "manual_crypto", Status: "succeeded",
			Amount: 90000, Currency: "IRT", Proof: &PaymentProof{Network: "TRC20", TXID: "abc", SubmittedAt: when,
				Decision: "approved", ReviewedBy: partner.ID, ReviewedAt: &decidedAt}}},
	}

	if _, err := svc.AdminGetPayment(ctx, customer.TelegramID, pay, "i-wait"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a customer: %v", err)
	}
	if _, err := svc.AdminGetPayment(ctx, admin.TelegramID, pay, "  "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no payment named: %v", err)
	}

	v, err := svc.AdminGetPayment(ctx, admin.TelegramID, pay, "i-wait")
	if err != nil || v.Status != "confirming" || v.Decision != "" || v.Reviewer != nil || !v.ReviewedAt.IsZero() ||
		!v.PossibleDuplicate || v.ReferenceNumber != "R1" || v.User == nil || v.User.ID != payer.ID || v.Amount != 150000 {
		t.Fatalf("a waiting payment: %+v %v", v, err)
	}

	v, err = svc.AdminGetPayment(ctx, admin.TelegramID, pay, "i-done")
	if err != nil || v.Status != "succeeded" || v.Decision != "approved" || v.Reviewer == nil || v.Reviewer.TelegramID != 7002 ||
		!v.ReviewedAt.Equal(decidedAt) || v.TXID != "abc" || v.Network != "TRC20" || v.User == nil || v.User.ID != payer.ID {
		t.Fatalf("a decided payment: %+v %v", v, err)
	}

	// The fake history answers with whatever it holds, like the real one
	// filtered to the payment asked for: nothing, then a payment with no proof.
	pay.history = nil
	if _, err := svc.AdminGetPayment(ctx, admin.TelegramID, pay, "i-none"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown payment: %v", err)
	}
	pay.history = []PaymentRecord{{IntentID: "i-gateway", UserID: payer.ID, Provider: "zarinpal", Status: "succeeded"}}
	if _, err := svc.AdminGetPayment(ctx, admin.TelegramID, pay, "i-gateway"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a gateway payment has no proof to review: %v", err)
	}
}
