package domain

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Bounds of a plan group: the name is a button label, the text one Telegram
// message together with the group's plan list; the count keeps the group list
// inside one keyboard (Telegram allows 100 buttons).
const (
	maxPlanGroupName = 60
	maxPlanGroupText = 1500
	maxPlanGroups    = 50
)

// ListPlanGroups returns every plan group in the order customers see them.
func (s *Service) ListPlanGroups(ctx context.Context) ([]store.PlanGroup, error) {
	return s.st.ListPlanGroups(ctx, s.st.Conn())
}

// UpsertPlanGroup creates (empty ID) or updates a plan group.
func (s *Service) UpsertPlanGroup(ctx context.Context, actor *store.User, g *store.PlanGroup) (*store.PlanGroup, error) {
	if err := validatePlanGroup(g); err != nil {
		return nil, err
	}
	var before any
	if g.ID != "" {
		u, err := buuid.Parse(g.ID)
		if err != nil {
			return nil, &FieldError{"id", "not a group id"}
		}
		g.ID = u.String()
		old, err := s.st.GetPlanGroup(ctx, s.st.Conn(), g.ID)
		if err != nil {
			return nil, err // editing a group that does not exist
		}
		before = old
	}
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if before == nil {
			groups, err := s.st.ListPlanGroups(ctx, tx)
			if err != nil {
				return err
			}
			if len(groups) >= maxPlanGroups {
				return invalid("at most %d plan groups", maxPlanGroups)
			}
		}
		if err := s.st.UpsertPlanGroup(ctx, tx, g); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "plan_group.upsert", "plan_group", g.ID, before, g, "")
	})
	if err != nil {
		return nil, err
	}
	return s.st.GetPlanGroup(ctx, s.st.Conn(), g.ID)
}

// DeletePlanGroup deletes a group. Its plans stay on sale, ungrouped.
func (s *Service) DeletePlanGroup(ctx context.Context, actor *store.User, id string) error {
	u, err := buuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return store.ErrNotFound
	}
	id = u.String()
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		old, err := s.st.GetPlanGroup(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := s.st.DeletePlanGroup(ctx, tx, id); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "plan_group.delete", "plan_group", id, old, nil, "")
	})
}

func validatePlanGroup(g *store.PlanGroup) error {
	name, desc := map[string]string{}, map[string]string{}
	for _, lang := range []string{"fa", "en"} {
		if v := strings.TrimSpace(g.NameI18n[lang]); v != "" {
			if utf8.RuneCountInString(v) > maxPlanGroupName {
				return &FieldError{"name", "a group name is at most 60 characters"}
			}
			name[lang] = v
		}
		if v := strings.TrimSpace(g.DescriptionI18n[lang]); v != "" {
			if utf8.RuneCountInString(v) > maxPlanGroupText {
				return &FieldError{"description", "a group text is at most 1500 characters"}
			}
			desc[lang] = v
		}
	}
	if len(name) == 0 {
		return invalid("a group needs an English or Persian name")
	}
	if g.Sort < -100000 || g.Sort > 100000 {
		return &FieldError{"sort", "out of range"}
	}
	g.NameI18n, g.DescriptionI18n = name, desc
	return nil
}

// planGroupOf settles the group a plan is saved with. A nil p.GroupID keeps the
// stored one (old; a new plan has none: the bot's plan editor never sends it),
// a pointer to "" takes the plan out of its group, anything else must be an
// existing group. A free trial and a traffic package are never sold from a
// group, so they have none.
func (s *Service) planGroupOf(ctx context.Context, p *store.Plan, old *store.Plan) error {
	switch {
	case p.IsTrial || p.IsTopup:
		p.GroupID = nil
	case p.GroupID == nil:
		if old != nil {
			p.GroupID = old.GroupID
		}
	case strings.TrimSpace(*p.GroupID) == "":
		p.GroupID = nil
	default:
		u, err := buuid.Parse(strings.TrimSpace(*p.GroupID))
		if err != nil {
			return &FieldError{"group_id", "not a group id"}
		}
		id := u.String()
		if _, err := s.st.GetPlanGroup(ctx, s.st.Conn(), id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return &FieldError{"group_id", "no such group"}
			}
			return err
		}
		p.GroupID = &id
	}
	return nil
}
