package domain

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// DeletePlan takes a plan off sale and out of every list. The plan itself is never erased from the database, whether or not orders point to
// it: its history, the reports and the refunds need it, and an owner may want what a plan was long after. Nobody can order or edit it any
// more; services already made from it keep working and renewing (a renewal picks a plan on sale).
func (s *Service) DeletePlan(ctx context.Context, actor *store.User, id string) error {
	u, err := buuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return store.ErrNotFound
	}
	id = u.String()
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		old, err := s.st.GetPlan(ctx, tx, id)
		if err != nil {
			return err
		}
		if old.DeletedAt != nil {
			return store.ErrNotFound
		}
		if err := s.st.ArchivePlan(ctx, tx, id); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "plan.delete", "plan", id, old, nil, "")
	})
}

// DeletedPlans lists the plans that were deleted, newest deletion first.
func (s *Service) DeletedPlans(ctx context.Context) ([]store.Plan, error) {
	return s.st.ListDeletedPlans(ctx, s.st.Conn())
}

// RestorePlan brings a deleted plan back into the lists. It comes back off sale: the owner turns it on when they want it. It is the owner's, as
// deleting is.
func (s *Service) RestorePlan(ctx context.Context, actor *store.User, id string) (*store.Plan, error) {
	u, err := buuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return nil, store.ErrNotFound
	}
	id = u.String()
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		old, err := s.st.GetPlan(ctx, tx, id)
		if err != nil {
			return err
		}
		if old.DeletedAt == nil {
			return store.ErrNotFound
		}
		if err := s.st.RestorePlan(ctx, tx, id); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "plan.restore", "plan", id, old, nil, "")
	})
	if err != nil {
		return nil, err
	}
	return s.st.GetPlan(ctx, s.st.Conn(), id)
}
