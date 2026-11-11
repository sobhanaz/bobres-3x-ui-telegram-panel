package domain

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Internal orders (docs/14-reports.md, "Internal orders"; the stamping is in store/internal.go): what an owner account buys is marked
// internal while reports.exclude_owner_orders is on, and left out of revenue, the reports and the growth rewards. These are the report of
// the internal rows and the two actions on the owner accounts' earlier rows.

// ExcludeOwnerOrders reports whether reports.exclude_owner_orders is on: what owner accounts buy from now on is marked internal.
func (s *Service) ExcludeOwnerOrders(ctx context.Context) (bool, error) {
	return s.st.ExcludeOwnerOrders(ctx, s.st.Conn())
}

// InternalOrders is the Reports page's "Internal orders" card for a range: the internal purchases and what refunds gave back of them, per
// currency and per owner account.
func (s *Service) InternalOrders(ctx context.Context, rng *ReportRange) (*store.InternalReport, error) {
	ctx, cancel := context.WithTimeout(ctx, ReportDeadline())
	defer cancel()
	rep, err := s.st.InternalOrders(ctx, rng.Query())
	return rep, reportError(err)
}

// InternalBackfillPreview is the dry run of CountOwnerRowsInternal: what it would move, changing nothing.
func (s *Service) InternalBackfillPreview(ctx context.Context) (*store.InternalMove, error) {
	return s.st.OwnerRowsToMove(ctx, s.st.Conn(), true)
}

// CountOwnerRowsInternal marks every order and ledger entry the owner accounts have made so far internal, so they leave revenue and the
// reports, and says how many rows changed. Only the flag changes: no order, payment, balance or ledger amount is touched. A second call
// finds nothing left to mark and changes nothing. It is audited (reports.internal_backfill) with the counts when it changed something.
// The web layer checks the permission.
func (s *Service) CountOwnerRowsInternal(ctx context.Context, actor *store.User) (*store.InternalMove, error) {
	return s.setOwnerRowsInternal(ctx, actor, true, "reports.internal_backfill")
}

// UndoOwnerRowsInternal puts the owner accounts' rows back into revenue and the reports (every internal row of an owner account, the ones
// stamped when they were made as well). Idempotent; audited (reports.internal_undo) when it changed something.
func (s *Service) UndoOwnerRowsInternal(ctx context.Context, actor *store.User) (*store.InternalMove, error) {
	return s.setOwnerRowsInternal(ctx, actor, false, "reports.internal_undo")
}

func (s *Service) setOwnerRowsInternal(ctx context.Context, actor *store.User, internal bool, action string) (*store.InternalMove, error) {
	var out *store.InternalMove
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		// The money that moves is read in the transaction that moves it, so the audit entry says what this call did.
		moved, err := s.st.OwnerRowsToMove(ctx, tx, internal)
		if err != nil {
			return err
		}
		orders, entries, err := s.st.SetOwnerRowsInternal(ctx, tx, internal)
		if err != nil {
			return err
		}
		moved.Orders, moved.Entries = orders, entries
		out = moved
		if orders == 0 && entries == 0 {
			return nil
		}
		return s.audit(ctx, tx, actor, action, "reports", "internal", internalAudit(moved), "")
	})
	return out, err
}

// internalAudit is what an audit entry of the two actions holds: the counts and the money per currency, no names.
func internalAudit(m *store.InternalMove) map[string]any {
	cur := make([]map[string]any, 0, len(m.Currencies))
	for _, c := range m.Currencies {
		cur = append(cur, map[string]any{"currency": c.Currency, "orders": c.Orders, "amount": c.Amount, "refunds": c.Refunds, "refunds_amount": c.RefundsAmount})
	}
	return map[string]any{"orders": m.Orders, "ledger_entries": m.Entries, "currencies": cur}
}
