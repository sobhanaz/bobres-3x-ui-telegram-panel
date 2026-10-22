package domain

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// What a reseller's clients are called and grouped as in the 3x-ui panel. The owner gives a reseller a group name when making
// them, and the group is "BOBRES-<NAME>" (BOBRES-SOBHANADMIN); the reseller chooses a prefix once for the names of their configs
// ("<prefix>-u<id>-<random>"), and after that only the owner changes it. A reseller with neither is as any customer: the usual
// names, in the default group.
const (
	// resellerGroupPrefix is xui.ResellerGroupPrefix (core does not import the panel client; a test keeps them equal).
	resellerGroupPrefix = "BOBRES-"
	// MaxConfigPrefix is the longest config prefix: Marzban takes names of 32 characters, and "<prefix>-u<10 digits>-<8>" must fit.
	MaxConfigPrefix = 10
	minConfigPrefix = 2
	// MaxPanelGroupName is the longest NAME of "BOBRES-NAME".
	MaxPanelGroupName = 24
)

var (
	configPrefixRe   = regexp.MustCompile(`^[a-z0-9]{2,10}$`)
	panelGroupNameRe = regexp.MustCompile(`^[A-Z0-9_]{2,24}$`)
	// defaultNameRe is the start of a customer's usual client name ("u123-..."): a prefix that looks like it would only confuse.
	defaultNameRe = regexp.MustCompile(`^u[0-9]*$`)
)

// NormalizeConfigPrefix is a config prefix as it is stored: lowercase letters and digits, 2 to 10, not the shape of
// the usual names.
func NormalizeConfigPrefix(s string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(s))
	switch {
	case !configPrefixRe.MatchString(p):
		return "", &FieldError{"config_prefix", "2 to 10 letters or digits (a-z, 0-9)"}
	case defaultNameRe.MatchString(p):
		return "", &FieldError{"config_prefix", "it must not look like the usual names (a u and digits)"}
	}
	return p, nil
}

// NormalizePanelGroupName is the NAME of a reseller's group as it is stored: capital letters, digits and _, 2 to 24. The
// whole group name ("BOBRES-SOBHANADMIN") is accepted too.
func NormalizePanelGroupName(s string) (string, error) {
	n := strings.ToUpper(strings.TrimSpace(s))
	n = strings.TrimPrefix(n, resellerGroupPrefix)
	if !panelGroupNameRe.MatchString(n) {
		return "", &FieldError{"panel_group", "2 to 24 letters, digits or _ (A-Z, 0-9)"}
	}
	return n, nil
}

// PanelGroupOf is the 3x-ui group of a reseller's clients: "BOBRES-NAME", or "" (the default group) when they have no name.
func PanelGroupOf(name string) string {
	if name == "" {
		return ""
	}
	return resellerGroupPrefix + name
}

// ResellerNaming returns a reseller's prefix and group name (store.ErrNotFound when the user is not a reseller).
func (s *Service) ResellerNaming(ctx context.Context, userID string) (*store.ResellerNaming, error) {
	return s.st.GetResellerNaming(ctx, s.st.Conn(), userID)
}

// SetResellerPanelGroup gives a reseller their group name ("" takes it away): the owner's. The reseller's clients made before
// are moved into the group in the background (ResellerGroupSync), and new ones are made in it.
func (s *Service) SetResellerPanelGroup(ctx context.Context, actor *store.User, userID, name string) error {
	if err := requireOwner(actor); err != nil {
		return err
	}
	if strings.TrimSpace(name) != "" {
		n, err := NormalizePanelGroupName(name)
		if err != nil {
			return err
		}
		name = n
	} else {
		name = ""
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.st.GetResellerNaming(ctx, tx, userID)
		if err != nil {
			return err
		}
		if err := s.st.SetResellerPanelGroup(ctx, tx, userID, name); err != nil {
			if errors.Is(err, store.ErrNameTaken) {
				return &FieldError{"panel_group", "another reseller has this group name"}
			}
			return err
		}
		return s.auditChange(ctx, tx, actor, "reseller.panel_group", "reseller", userID,
			map[string]string{"panel_group": before.PanelGroup}, map[string]string{"panel_group": name}, "")
	})
}

// SetResellerConfigPrefix sets the prefix of a reseller's config names. The owner sets or changes or clears it; the reseller
// sets their own once, and while they have none. It applies to the configs made after it.
func (s *Service) SetResellerConfigPrefix(ctx context.Context, actor *store.User, userID, prefix string) error {
	byOwner := requireOwner(actor) == nil
	if !byOwner && (actor == nil || actor.ID != userID || actor.Role != "reseller" || actor.Status != "active") {
		return ErrForbidden
	}
	if strings.TrimSpace(prefix) != "" || !byOwner {
		p, err := NormalizeConfigPrefix(prefix)
		if err != nil {
			return err
		}
		prefix = p
	} else {
		prefix = ""
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.st.GetResellerNaming(ctx, tx, userID)
		if err != nil {
			return err
		}
		done, err := s.st.SetResellerConfigPrefix(ctx, tx, userID, prefix, !byOwner)
		if errors.Is(err, store.ErrNameTaken) {
			return &FieldError{"config_prefix", "another reseller has this prefix"}
		}
		if err != nil {
			return err
		}
		if !done {
			return stateErr("the prefix was set already: only the owner changes it now")
		}
		return s.auditChange(ctx, tx, actor, "reseller.config_prefix", "reseller", userID,
			map[string]string{"config_prefix": before.ConfigPrefix}, map[string]string{"config_prefix": prefix}, "")
	})
}

// RequestResellerGroupSync puts a reseller's existing configs back to be moved into their group: the owner's "move the existing
// configs" (what a group name set after the configs were made, or changed, needs).
func (s *Service) RequestResellerGroupSync(ctx context.Context, actor *store.User, userID string) error {
	if err := requireOwner(actor); err != nil {
		return err
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.RequestResellerGroupSync(ctx, tx, userID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return invalid("the reseller has no group name yet")
			}
			return err
		}
		return s.audit(ctx, tx, actor, "reseller.group_sync", "reseller", userID, nil, "")
	})
}

// clientEmailFor is the panel name of a new client: "u<telegram id>-<random>", with the reseller's prefix in front when the buyer
// is a reseller who has one ("<prefix>-u<id>-<random>").
func clientEmailFor(prefix string, telegramID int64) string {
	e := clientEmail(telegramID)
	if prefix == "" {
		return e
	}
	return prefix + "-" + e
}

// ResellerGroupSync moves a reseller's clients made before they had a group (or when the owner asks again) into it: it hands the
// provisioner the reseller's subscriptions, which moves on each panel only the clients that are in a group BOBRES owns.
type ResellerGroupSync struct {
	s    *Service
	prov Provisioner
	log  *slog.Logger
	// every is how often it looks for resellers to do.
	every time.Duration
}

// resellerSyncBatch is how many subscriptions go to the provisioner in a call (its limit is 500).
const resellerSyncBatch = 500

// NewResellerGroupSync builds the worker that keeps resellers' clients in their groups.
func (s *Service) NewResellerGroupSync(prov Provisioner, log *slog.Logger) *ResellerGroupSync {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &ResellerGroupSync{s: s, prov: prov, log: log.With("worker", "reseller-groups"), every: time.Minute}
}

// Run looks every minute until ctx ends.
func (w *ResellerGroupSync) Run(ctx context.Context) error {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		if _, err := w.Step(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("moving resellers' clients into their groups failed; it is tried again", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Step does the resellers that wait (at most a few): how many it finished.
func (w *ResellerGroupSync) Step(ctx context.Context) (int, error) {
	st := w.s.st
	todo, err := st.ResellersToSync(ctx, st.Conn(), 20)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, r := range todo {
		if err := w.syncOne(ctx, r); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

func (w *ResellerGroupSync) syncOne(ctx context.Context, r store.ResellerNaming) error {
	st := w.s.st
	group := PanelGroupOf(r.PanelGroup)
	moved, after := 0, ""
	for {
		ids, err := st.SubscriptionIDsOf(ctx, st.Conn(), r.UserID, after, resellerSyncBatch)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		items := make([]ClientGroup, 0, len(ids))
		for _, id := range ids {
			items = append(items, ClientGroup{SubscriptionID: id, Group: group})
		}
		n, err := w.prov.SetClientGroups(ctx, items)
		if err != nil {
			return err
		}
		moved += n
		after = ids[len(ids)-1]
	}
	if err := st.MarkResellerGroupsSynced(ctx, st.Conn(), r.UserID, r.PanelGroup); err != nil {
		return err
	}
	w.log.Info("a reseller's clients were handed over for their group", "reseller", r.UserID, "group", group, "clients", moved)
	return nil
}
