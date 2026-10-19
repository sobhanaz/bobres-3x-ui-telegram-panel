package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// requireStaff loads the actor and checks they are an active admin or owner.
func (s *Service) requireStaff(ctx context.Context, actorTelegramID int64) (*store.User, error) {
	if actorTelegramID <= 0 {
		return nil, ErrForbidden
	}
	u, err := s.st.GetUserByTelegramID(ctx, s.st.Conn(), actorTelegramID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	if u.Status != "active" || (u.Role != "admin" && u.Role != "owner") {
		return nil, ErrForbidden
	}
	return u, nil
}

func (s *Service) audit(ctx context.Context, q pgx.Tx, actor *store.User, action, entity, entityID string, after any, reason string) error {
	return s.auditChange(ctx, q, actor, action, entity, entityID, nil, after, reason)
}

// auditChange writes an audit entry with the item's state before and after.
// A nil actor is the system (a configured rule, not a person).
func (s *Service) auditChange(ctx context.Context, q pgx.Tx, actor *store.User, action, entity, entityID string, before, after any, reason string) error {
	enc := func(v any) ([]byte, error) {
		if v == nil {
			return nil, nil
		}
		return json.Marshal(v)
	}
	b, err := enc(before)
	if err != nil {
		return err
	}
	payload, err := enc(after)
	if err != nil {
		return err
	}
	a := &store.Audit{Action: action, Entity: entity, Before: b, After: payload}
	if actor != nil {
		a.ActorID = &actor.ID
	}
	if entityID != "" {
		a.EntityID = &entityID
	}
	if reason != "" {
		a.Reason = &reason
	}
	if q != nil {
		return s.st.WriteAudit(ctx, q, a)
	}
	return s.st.WriteAudit(ctx, s.st.Conn(), a)
}

// Stats are the admin panel's headline figures. PendingPayments is -1, and
// Servers nil with PanelHealthy false, when a backend could not be reached.
type Stats struct {
	store.UserCounts
	PendingPayments int64
	Servers         *ServerHealth
	// PanelHealthy: every server taking new services answers; PanelDetail
	// says why not, in English.
	PanelHealthy bool
	PanelDetail  string
}

// ServerHealth sums up the 3x-ui servers that take new services (the
// switched-off ones are not checked), as the dashboard's overview does:
// Healthy of Total answered their last health check or are not checked yet,
// and Down names the others, in the list's order. Listed counts every server,
// the switched-off ones included.
type ServerHealth struct {
	Total, Healthy, Listed int
	Down                   []string
}

// Health sums up the servers of the list.
func (l *ServerList) Health() ServerHealth {
	h := ServerHealth{Listed: len(l.Servers)}
	for i := range l.Servers {
		sv := &l.Servers[i]
		if !sv.Enabled {
			continue
		}
		h.Total++
		if sv.CheckedAt == 0 || sv.Healthy {
			h.Healthy++
		} else {
			h.Down = append(h.Down, sv.Name)
		}
	}
	return h
}

// AdminStats gathers the figures; an unreachable backend degrades the result
// instead of failing it. The servers' health comes from the provisioner's
// list (its last checks), so no panel is called here.
func (s *Service) AdminStats(ctx context.Context, actorTelegramID int64, pay PaymentsClient, sa ServerAdmin) (*Stats, error) {
	if _, err := s.requireStaff(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	c, err := s.st.Counts(ctx, s.st.Conn(), time.Now().Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	out := &Stats{UserCounts: c, PendingPayments: -1, PanelDetail: "provisioner unreachable"}
	if pending, err := pay.ListPending(ctx, 200); err == nil {
		out.PendingPayments = int64(len(pending))
	}
	list, err := sa.ListServers(ctx)
	if err != nil {
		return out, nil
	}
	h := list.Health()
	out.Servers = &h
	out.PanelHealthy = h.Total > 0 && len(h.Down) == 0
	switch {
	case len(list.Servers) == 0:
		out.PanelDetail = "no 3x-ui server yet"
	case h.Total == 0:
		out.PanelDetail = "no server takes new services"
	case len(h.Down) > 0:
		out.PanelDetail = fmt.Sprintf("%d of %d servers do not answer: %s", len(h.Down), h.Total, strings.Join(h.Down, ", "))
	default:
		out.PanelDetail = ""
	}
	return out, nil
}

// PendingView is a pending payment with its payer.
type PendingView struct {
	PendingPayment
	User *store.User
}

// AdminListPendingPayments returns the manual review queue, oldest first.
func (s *Service) AdminListPendingPayments(ctx context.Context, actorTelegramID int64, pay PaymentsClient, limit int) ([]PendingView, error) {
	if _, err := s.requireStaff(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	pending, err := pay.ListPending(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]PendingView, 0, len(pending))
	for _, p := range pending {
		out = append(out, s.pendingView(ctx, p))
	}
	return out, nil
}

// pendingLookupLimit is how many of the oldest waiting payments
// AdminGetPayment searches for the one it is asked about (the review queue's
// own ceiling).
const pendingLookupLimit = 200

// PaymentReviewView is one manual payment as staff see it: the payer and the
// proof, and, once someone has decided, what they decided and when.
type PaymentReviewView struct {
	PendingView
	// Status is the payment's own status: confirming while it waits for a
	// decision, then succeeded (approved) or failed (rejected).
	Status string
	// Decision is "" while nobody has decided, else approved or rejected.
	Decision string
	// Reason is the rejection reason as it was stored ("preset:<name>" or
	// the reviewer's own words).
	Reason string
	// Reviewer is who decided (nil while pending, or when the account is gone).
	Reviewer   *store.User
	ReviewedAt time.Time
}

// AdminGetPayment shows one manual payment to the staff: still waiting, with
// the signs that it may be a duplicate, or already decided, by whom and when.
// A staff alert opens it, and another owner may have decided it first.
func (s *Service) AdminGetPayment(ctx context.Context, actorTelegramID int64, pay PaymentsClient, intentID string) (*PaymentReviewView, error) {
	if _, err := s.requireStaff(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	intentID = strings.TrimSpace(intentID)
	if intentID == "" {
		return nil, invalid("a payment is required")
	}
	// Waiting: the review queue knows it, and whether it may be a duplicate.
	pending, err := pay.ListPending(ctx, pendingLookupLimit)
	if err != nil {
		return nil, err
	}
	for _, p := range pending {
		if p.IntentID == intentID {
			return &PaymentReviewView{PendingView: s.pendingView(ctx, p), Status: "confirming"}, nil
		}
	}
	// Otherwise the history has it: decided, or not a manual payment at all.
	recs, _, err := pay.ListPayments(ctx, PaymentFilter{IntentID: intentID}, 1, 1)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 || recs[0].Proof == nil {
		return nil, store.ErrNotFound
	}
	rec, proof := recs[0], recs[0].Proof
	v := &PaymentReviewView{
		PendingView: s.pendingView(ctx, PendingPayment{
			IntentID: rec.IntentID, OrderID: rec.OrderID, UserID: rec.UserID, Provider: rec.Provider,
			Amount: rec.Amount, Currency: rec.Currency,
			ReceiptFile: proof.ReceiptFile, ReferenceNumber: proof.ReferenceNumber, Network: proof.Network, TXID: proof.TXID,
			SubmittedAt: proof.SubmittedAt,
		}),
		Status: rec.Status, Decision: proof.Decision, Reason: proof.Reason,
	}
	if proof.ReviewedAt != nil {
		v.ReviewedAt = *proof.ReviewedAt
	}
	if proof.ReviewedBy != "" {
		if u, err := s.st.GetUser(ctx, s.st.Conn(), proof.ReviewedBy); err == nil {
			v.Reviewer = u
		}
	}
	return v, nil
}

// pendingView adds the payer to a payment.
func (s *Service) pendingView(ctx context.Context, p PendingPayment) PendingView {
	v := PendingView{PendingPayment: p}
	if u, err := s.st.GetUser(ctx, s.st.Conn(), p.UserID); err == nil {
		v.User = u
	}
	return v
}

// AdminReviewPayment approves or rejects a manual payment as the actor.
func (s *Service) AdminReviewPayment(ctx context.Context, actorTelegramID int64, pay PaymentsClient, intentID, decision, reason string) (string, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return "", err
	}
	return s.ReviewPayment(ctx, actor, pay, intentID, decision, reason)
}

// ReviewPayment approves or rejects a manual payment. Like every staff
// operation taking an actor, the caller has checked that the actor may.
func (s *Service) ReviewPayment(ctx context.Context, actor *store.User, pay PaymentsClient, intentID, decision, reason string) (string, error) {
	if decision != "approved" && decision != "rejected" {
		return "", invalid("decision must be approved or rejected")
	}
	status, err := pay.Review(ctx, actor.ID, intentID, decision, reason)
	if err != nil {
		return "", err
	}
	after := map[string]string{"decision": decision}
	// The order makes the entry easy to follow from the audit log.
	if recs, _, err := pay.ListPayments(ctx, PaymentFilter{IntentID: intentID}, 1, 1); err == nil && len(recs) == 1 {
		after["order_id"] = recs[0].OrderID
	}
	// The review already happened; an audit failure is logged by the caller
	// through the returned error but does not undo it.
	if err := s.audit(ctx, nil, actor, "payment.review", "payment_intent", intentID, after, reason); err != nil {
		return status, fmt.Errorf("payment reviewed but audit failed: %w", err)
	}
	return status, nil
}

// UserView is what an admin sees about a user.
type UserView struct {
	User          *store.User
	Wallets       []store.Wallet
	Subscriptions int64
	Orders        int64
}

// AdminFindUser looks a user up by Telegram id or @username.
func (s *Service) AdminFindUser(ctx context.Context, actorTelegramID int64, query string) (*UserView, error) {
	if _, err := s.requireStaff(ctx, actorTelegramID); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, invalid("query required")
	}
	var (
		u   *store.User
		err error
	)
	if id, perr := strconv.ParseInt(query, 10, 64); perr == nil {
		u, err = s.st.GetUserByTelegramID(ctx, s.st.Conn(), id)
	} else {
		u, err = s.st.GetUserByUsername(ctx, s.st.Conn(), query)
	}
	if err != nil {
		return nil, err
	}
	wallets, err := s.st.ListWallets(ctx, s.st.Conn(), u.ID)
	if err != nil {
		return nil, err
	}
	subs, orders, err := s.st.UserActivity(ctx, s.st.Conn(), u.ID)
	if err != nil {
		return nil, err
	}
	return &UserView{User: u, Wallets: wallets, Subscriptions: subs, Orders: orders}, nil
}

// AdminAdjustBalance credits (delta > 0) or debits (delta < 0) a wallet with
// an "adjust" ledger entry. A reason is mandatory; the key makes retries safe.
func (s *Service) AdminAdjustBalance(ctx context.Context, actorTelegramID int64, userID string, delta int64, currency, reason, idemKey string) (*store.Wallet, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
	return s.AdjustBalance(ctx, actor, userID, delta, currency, reason, idemKey)
}

// AdjustBalance is AdminAdjustBalance for an actor the caller has checked.
func (s *Service) AdjustBalance(ctx context.Context, actor *store.User, userID string, delta int64, currency, reason, idemKey string) (*store.Wallet, error) {
	reason = strings.TrimSpace(reason)
	switch {
	case delta == 0 || delta > maxTopupMinor || delta < -maxTopupMinor:
		return nil, invalid("adjustment out of range")
	case money.Scale(currency) < 0:
		return nil, invalid("unsupported currency %q", currency)
	case reason == "":
		return nil, invalid("a reason is required for balance changes")
	case idemKey == "":
		return nil, invalid("idempotency key required")
	}
	target, err := s.st.GetUser(ctx, s.st.Conn(), userID)
	if err != nil {
		return nil, err
	}
	var w *store.Wallet
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		e := &store.LedgerEntry{Kind: "adjust", RefType: strptr("admin"), RefID: strptr(actor.ID), IdempotencyKey: "adjust:" + idemKey}
		var err error
		if delta > 0 {
			w, err = s.st.Credit(ctx, tx, userID, currency, delta, e)
		} else {
			w, err = s.st.Debit(ctx, tx, userID, currency, -delta, e)
		}
		if errors.Is(err, store.ErrDuplicateIdempotency) {
			return nil // a retry: already applied
		}
		if err != nil {
			return err
		}
		if err := s.audit(ctx, tx, actor, "wallet.adjust", "user", userID,
			map[string]any{"delta": delta, "currency": currency, "balance": w.Balance}, reason); err != nil {
			return err
		}
		if delta < 0 {
			return nil
		}
		return s.publish(ctx, tx, events.WalletCredited, events.WalletCreditedEvent{
			UserID: userID, TelegramID: target.TelegramID, Amount: delta, Currency: currency,
			Balance: w.Balance, Reason: events.CreditAdminAdjust,
		})
	})
	if err != nil {
		return nil, err
	}
	if w == nil { // replay
		return s.st.GetWallet(ctx, s.st.Conn(), userID, currency)
	}
	return w, nil
}

// AdminSetUserStatus bans or unbans a customer. Nobody can ban themselves or
// a staff member (owner, admin, support): staff are managed by the owner.
func (s *Service) AdminSetUserStatus(ctx context.Context, actorTelegramID int64, userID, status, reason string) (*store.User, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
	return s.SetUserStatus(ctx, actor, userID, status, reason)
}

// SetUserStatus is AdminSetUserStatus for an actor the caller has checked.
// Staff cannot ban staff: demote them first (an owner's decision).
func (s *Service) SetUserStatus(ctx context.Context, actor *store.User, userID, status, reason string) (*store.User, error) {
	if status != "active" && status != "banned" {
		return nil, invalid("status must be active or banned")
	}
	if strings.TrimSpace(reason) == "" {
		return nil, invalid("a reason is required")
	}
	target, err := s.st.GetUser(ctx, s.st.Conn(), userID)
	if err != nil {
		return nil, err
	}
	if target.ID == actor.ID || IsStaffRole(target.Role) {
		return nil, ErrForbidden
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetUserStatus(ctx, tx, userID, status); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "user.status", "user", userID,
			map[string]string{"status": target.Status}, map[string]string{"status": status}, reason)
	})
	if err != nil {
		return nil, err
	}
	target.Status = status
	return target, nil
}

// AdminUpsertPlan creates (empty ID) or updates a plan.
func (s *Service) AdminUpsertPlan(ctx context.Context, actorTelegramID int64, p *store.Plan) (*store.Plan, error) {
	actor, err := s.requireStaff(ctx, actorTelegramID)
	if err != nil {
		return nil, err
	}
	return s.UpsertPlan(ctx, actor, p)
}

// UpsertPlan is AdminUpsertPlan for an actor the caller has checked. A nil
// p.ServerIDs keeps the plan's servers (the bot never sends them); a nil
// p.Inbounds keeps its inbound choice (an older client does not send it; an
// empty list clears it, so every attachable inbound is used again).
func (s *Service) UpsertPlan(ctx context.Context, actor *store.User, p *store.Plan) (*store.Plan, error) {
	if err := validatePlan(p); err != nil {
		return nil, err
	}
	ids, err := planServerIDs(p.ServerIDs)
	if err != nil {
		return nil, err
	}
	p.ServerIDs = ids
	inbounds, err := planInbounds(p.Inbounds)
	if err != nil {
		return nil, err
	}
	p.Inbounds = inbounds
	var (
		before any // nil for a new plan
		oldCur string
	)
	if p.ID != "" {
		old, err := s.st.GetPlan(ctx, s.st.Conn(), p.ID)
		if err != nil {
			return nil, err // editing a plan that does not exist
		}
		before, oldCur = old, old.Currency
		if p.ServerIDs == nil {
			p.ServerIDs = old.ServerIDs
		}
		if p.Inbounds == nil {
			// Kept, as the servers are (the saved plan and its audit entry
			// show it). The choice kept must stay inside the servers now
			// named: a server taken out of the plan takes its inbounds along.
			p.Inbounds = old.Inbounds
			if len(p.ServerIDs) > 0 {
				p.Inbounds = inboundsOnServers(old.Inbounds, p.ServerIDs)
			}
			if p.Inbounds == nil {
				p.Inbounds = []store.PlanInbound{}
			}
		}
	} else {
		if p.ServerIDs == nil {
			p.ServerIDs = []string{}
		}
		if p.Inbounds == nil {
			p.Inbounds = []store.PlanInbound{}
		}
	}
	if err := checkInboundServers(p.Inbounds, p.ServerIDs); err != nil {
		return nil, err
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.UpsertPlan(ctx, tx, p); err != nil {
			return err
		}
		if err := s.auditChange(ctx, tx, actor, "plan.upsert", "plan", p.ID, before, p, ""); err != nil {
			return err
		}
		if oldCur == "" || oldCur == p.Currency {
			return nil
		}
		// Reseller tiers' fixed prices were in the old currency: they go (the
		// tiers' discount applies until staff set new ones).
		n, err := s.st.DropStaleTierPrices(ctx, tx, p.ID)
		if err != nil || n == 0 {
			return err
		}
		return s.audit(ctx, tx, actor, "reseller_tier.prices_dropped", "plan", p.ID,
			map[string]any{"from": oldCur, "to": p.Currency, "prices": n}, "")
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// maxPlanServers bounds a plan's server list.
const maxPlanServers = 100

// planServerIDs checks a plan's servers: ids as the provisioner gives them,
// each once, sorted (nil stays nil: keep the stored list). Whether they still
// exist is the provisioner's to say when a service is created.
func planServerIDs(ids []string) ([]string, error) {
	if ids == nil {
		return nil, nil
	}
	if len(ids) > maxPlanServers {
		return nil, &FieldError{"server_ids", fmt.Sprintf("at most %d servers", maxPlanServers)}
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		u, err := buuid.Parse(strings.TrimSpace(id))
		if err != nil {
			return nil, &FieldError{"server_ids", "not a server id"}
		}
		if c := u.String(); !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Bounds of a plan's inbound choice: servers with a choice, inbounds per server.
const (
	maxPlanInboundServers = maxPlanServers
	maxPlanInbounds       = 64
)

// planInbounds checks a plan's inbound choice: server ids as the provisioner
// gives them, positive panel inbound ids, each once, sorted by server and
// inbound (nil stays nil: keep the stored choice). A server with no inbound
// listed has no choice (the default), so it is dropped.
func planInbounds(in []store.PlanInbound) ([]store.PlanInbound, error) {
	if in == nil {
		return nil, nil
	}
	if len(in) > maxPlanInboundServers {
		return nil, &FieldError{"inbounds", fmt.Sprintf("at most %d servers", maxPlanInboundServers)}
	}
	byServer := map[string][]int32{}
	for _, sel := range in {
		u, err := buuid.Parse(strings.TrimSpace(sel.ServerID))
		if err != nil {
			return nil, &FieldError{"inbounds", "not a server id"}
		}
		sid := u.String()
		for _, id := range sel.InboundIDs {
			if id <= 0 {
				return nil, &FieldError{"inbounds", "an inbound id is a positive number"}
			}
			if !slices.Contains(byServer[sid], id) {
				byServer[sid] = append(byServer[sid], id)
			}
		}
		if len(byServer[sid]) > maxPlanInbounds {
			return nil, &FieldError{"inbounds", fmt.Sprintf("at most %d inbounds per server", maxPlanInbounds)}
		}
	}
	out := make([]store.PlanInbound, 0, len(byServer))
	for sid, ids := range byServer {
		slices.Sort(ids)
		out = append(out, store.PlanInbound{ServerID: sid, InboundIDs: ids})
	}
	slices.SortFunc(out, func(a, b store.PlanInbound) int { return strings.Compare(a.ServerID, b.ServerID) })
	return out, nil
}

// checkInboundServers refuses a choice on a server the plan does not send
// services to. A plan that names no server uses any, so any server may have
// a choice.
func checkInboundServers(in []store.PlanInbound, servers []string) error {
	if len(servers) == 0 {
		return nil
	}
	for _, sel := range in {
		if !slices.Contains(servers, sel.ServerID) {
			return &FieldError{"inbounds", "an inbound is chosen on a server the plan does not use"}
		}
	}
	return nil
}

// inboundsOnServers keeps the choices on the given servers (never nil).
func inboundsOnServers(in []store.PlanInbound, servers []string) []store.PlanInbound {
	out := make([]store.PlanInbound, 0, len(in))
	for _, sel := range in {
		if slices.Contains(servers, sel.ServerID) {
			out = append(out, sel)
		}
	}
	return out
}

// planName is a plan's English name, else any of its names, else fallback.
func planName(p *store.Plan, fallback string) string {
	if n := p.NameI18n["en"]; n != "" {
		return n
	}
	for _, n := range p.NameI18n {
		if n != "" {
			return n
		}
	}
	return fallback
}

func validatePlan(p *store.Plan) error {
	if p.NameI18n["en"] == "" && p.NameI18n["fa"] == "" {
		return invalid("plan needs an English or Persian name")
	}
	switch p.Kind {
	case "traffic", "time", "both":
	default:
		return invalid("plan kind must be traffic, time or both")
	}
	if p.Price < 0 || p.Price > maxTopupMinor || money.Scale(p.Currency) < 0 {
		return invalid("plan price or currency out of range")
	}
	if p.IsTrial && p.Price != 0 {
		return invalid("a trial plan must be free")
	}
	if p.IsTopup && (p.IsTrial || p.Kind != "traffic") {
		return invalid("a traffic package is a paid plan of kind traffic")
	}
	if p.DurationDays != nil && (*p.DurationDays < 0 || *p.DurationDays > 3650) {
		return invalid("duration out of range")
	}
	if p.TrafficBytes != nil && *p.TrafficBytes < 0 {
		return invalid("traffic out of range")
	}
	if (p.Kind == "time" || p.Kind == "both") && (p.DurationDays == nil || *p.DurationDays == 0) {
		return invalid("a %s plan needs a duration", p.Kind)
	}
	if (p.Kind == "traffic" || p.Kind == "both") && (p.TrafficBytes == nil || *p.TrafficBytes == 0) {
		return invalid("a %s plan needs a traffic limit", p.Kind)
	}
	return nil
}
