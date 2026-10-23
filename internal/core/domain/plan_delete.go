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
