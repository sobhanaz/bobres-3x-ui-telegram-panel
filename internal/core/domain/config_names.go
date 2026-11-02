package domain

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Config names (docs/03-bot-ux.md, "Config names"): instead of the automatic name ("u<telegram id>-<random>"), a buyer may
// choose the name of a new service's config, and rename one they have. Two settings switch it on, for customers and for
// resellers, both off by default. A name is 4 to 20 lowercase letters and digits with at least one of each; a reseller's
// prefix stays in front ("sob-sobhan1234"), and the full name is what must be free. A name is checked before the order is
// made (so nobody pays for a name that is taken) against core's records and, through the provisioner, every enabled panel.
// A panel that still refuses it when the service is made (someone took it meanwhile) gets the automatic name instead, and
// the customer is told; the delivery never fails because of the name. A rename keeps the subscription link, so it is
// offered only on servers whose panel can keep it (3x-ui, Hiddify; not Marzban or S-UI). Renewals and top-ups never ask.

// The settings that switch config names on.
const (
	SettingConfigNamesCustomers = "config_names.customers"
	SettingConfigNamesResellers = "config_names.resellers"
)

// The length of a config name as typed (without a reseller's prefix). The longest full name, a 10-character prefix, "-"
// and 20 characters, fits Marzban's 32.
const (
	MinConfigName = 4
	MaxConfigName = 20
)

// Why a config name cannot be used (ConfigNameCheck.Reason, ConfigNameError.Reason).
const (
	NameOff          = "off"           // config names are off for the user
	NameLength       = "length"        // not MinConfigName to MaxConfigName characters
	NameChars        = "chars"         // not only a-z and 0-9
	NameMix          = "mix"           // no letter, or no digit
	NameAutomatic    = "automatic"     // shaped like the automatic names (a u and a digit first)
	NameTaken        = "taken"         // another config has it, here or on a panel
	NameUnchecked    = "unchecked"     // a panel could not be asked: try again
	NameNotRenamable = "not_renamable" // the service cannot be renamed now (not delivered, or its server cannot keep the link)
)

var (
	// ErrClientNameTaken: a panel (or the provisioner's records) has a client of that name (provisionerclient).
	ErrClientNameTaken = errors.New("domain: config name taken")
	// ErrNotRenamable: the service's server cannot rename a client and keep its link (provisionerclient).
	ErrNotRenamable = errors.New("domain: the service cannot be renamed")
)

// ConfigNameError is a config name core does not take; Reason is one of the Name* reasons. It is ErrInvalid, and its text
// ends with "config name: <reason>" so the bot and the dashboard can say why in the user's language.
type ConfigNameError struct{ Reason string }

func (e *ConfigNameError) Error() string { return ErrInvalid.Error() + ": config name: " + e.Reason }
func (e *ConfigNameError) Unwrap() error { return ErrInvalid }

// ConfigNameReason is the reason of a ConfigNameError in err ("" when there is none).
func ConfigNameReason(err error) string {
	var ce *ConfigNameError
	if errors.As(err, &ce) {
		return ce.Reason
	}
	return ""
}

// automaticNameRe is the start of an automatic name ("u123-..."): a name that looks like one would only confuse.
var automaticNameRe = regexp.MustCompile(`^u[0-9]`)

// NormalizeConfigName is a config name as typed made into the name kept (trimmed, lowercased, Latin digits), or a
// *ConfigNameError saying which rule it breaks. Persian and Arabic digits count as 0-9: a customer in Iran types "sobhan۱۲۳۴" as readily.
func NormalizeConfigName(s string) (string, error) {
	n := strings.ToLower(latinDigits(strings.TrimSpace(s)))
	letter, digit := false, false
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z':
			letter = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			if l := utf8.RuneCountInString(n); l < MinConfigName || l > MaxConfigName {
				return "", &ConfigNameError{NameLength}
			}
			return "", &ConfigNameError{NameChars}
		}
	}
	switch {
	case len(n) < MinConfigName || len(n) > MaxConfigName:
		return "", &ConfigNameError{NameLength}
	case !letter || !digit:
		return "", &ConfigNameError{NameMix}
	case automaticNameRe.MatchString(n):
		return "", &ConfigNameError{NameAutomatic}
	}
	return n, nil
}

// configNamesOn reports whether u may choose config names (the setting of their role).
func (s *Service) configNamesOn(ctx context.Context, u *store.User) bool {
	key := SettingConfigNamesCustomers
	if isReseller(u) {
		key = SettingConfigNamesResellers
	}
	return s.setting(ctx, key) == "true"
}

// ConfigNamesOn reports whether u may choose config names now (the Mini App's flag).
func (s *Service) ConfigNamesOn(ctx context.Context, u *store.User) bool {
	return u != nil && u.Status == "active" && s.configNamesOn(ctx, u)
}

// configPrefix is the prefix a user's config names carry: a reseller's own, "" for anyone else.
func (s *Service) configPrefix(ctx context.Context, u *store.User) (string, error) {
	if !isReseller(u) {
		return "", nil
	}
	n, err := s.st.GetResellerNaming(ctx, s.st.Conn(), u.ID)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return n.ConfigPrefix, nil
}

// fullConfigName is the name a config of u gets for name as typed: their prefix, "-", the normalized name.
func (s *Service) fullConfigName(ctx context.Context, u *store.User, name string) (string, error) {
	n, err := NormalizeConfigName(name)
	if err != nil {
		return "", err
	}
	prefix, err := s.configPrefix(ctx, u)
	if err != nil {
		return "", err
	}
	if prefix != "" {
		n = prefix + "-" + n
	}
	return n, nil
}

// nameFree says why a full config name cannot be used ("" when it can): a service or an order on its way has it in
// core's records (other than exceptSub, and other than exceptUser's own orders), or the provisioner says a client has it
// or a panel could not be asked. A provisioner that does not answer is "unchecked", never "free".
func (s *Service) nameFree(ctx context.Context, names NameChecker, full, exceptUser, exceptSub string) (string, error) {
	taken, err := s.st.ClientNameTaken(ctx, s.st.Conn(), full, exceptUser, exceptSub)
	if err != nil {
		return "", err
	}
	if taken {
		return NameTaken, nil
	}
	if names == nil {
		return NameUnchecked, nil
	}
	res, err := names.CheckClientName(ctx, full)
	switch {
	case err != nil:
		s.cfg.Log.Warn("a config name could not be checked with the provisioner", "err", err)
		return NameUnchecked, nil
	case res.Available:
		return "", nil
	case res.Reason == NameTaken:
		return NameTaken, nil
	}
	return NameUnchecked, nil
}

// ConfigNameCheck is CheckConfigName's answer: OK, or why not (a Name* reason). Name is the full name the config gets.
type ConfigNameCheck struct {
	OK     bool
	Reason string
	Name   string
}

// CheckConfigName says whether name can be the config name of a new service of the user (subID ""), or the new name of
// one of their services (subID: the service's own name is not "taken"). A name the user cannot use is an answer with a
// reason, not an error; a service that is not theirs is store.ErrNotFound.
func (s *Service) CheckConfigName(ctx context.Context, names NameChecker, userID, name, subID string) (*ConfigNameCheck, error) {
	u, err := s.activeUser(ctx, nil, userID)
	if err != nil {
		return nil, err
	}
	var sub *store.Subscription
	if subID != "" {
		if sub, err = s.ownService(ctx, u, subID); err != nil {
			return nil, err
		}
	}
	if !s.configNamesOn(ctx, u) {
		return &ConfigNameCheck{Reason: NameOff}, nil
	}
	full, err := s.fullConfigName(ctx, u, name)
	if r := ConfigNameReason(err); r != "" {
		return &ConfigNameCheck{Reason: r}, nil
	}
	if err != nil {
		return nil, err
	}
	except := ""
	if sub != nil {
		if sub.ClientEmail == full {
			return &ConfigNameCheck{OK: true, Name: full}, nil
		}
		except = sub.ID
	}
	reason, err := s.nameFree(ctx, names, full, u.ID, except)
	if err != nil {
		return nil, err
	}
	return &ConfigNameCheck{OK: reason == "", Reason: reason, Name: full}, nil
}

// orderClientName checks the config name of a new order (CreateOrder): config names on for the buyer, the rules, and
// free. It returns the full name, or a *ConfigNameError.
func (s *Service) orderClientName(ctx context.Context, names NameChecker, u *store.User, name string) (string, error) {
	if !s.configNamesOn(ctx, u) {
		return "", &ConfigNameError{NameOff}
	}
	full, err := s.fullConfigName(ctx, u, name)
	if err != nil {
		return "", err
	}
	reason, err := s.nameFree(ctx, names, full, u.ID, "")
	if err != nil {
		return "", err
	}
	if reason != "" {
		return "", &ConfigNameError{reason}
	}
	return full, nil
}

// renameCacheTTL is how long the servers that rename are remembered: the list comes from the provisioner, and every list
// of services asks.
const renameCacheTTL = time.Minute

// renameCache remembers which servers rename their clients (ServerAdmin.ListServers).
type renameCache struct {
	mu  sync.Mutex
	at  time.Time
	ids map[string]bool
}

// renamingServers is the ids of the servers whose clients can be renamed and keep their link. A provisioner that does not
// answer gives none (no Rename button; nothing is remembered, so the next list asks again).
func (s *Service) renamingServers(ctx context.Context, servers ServerAdmin) map[string]bool {
	if servers == nil {
		return nil
	}
	c := &s.renames
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ids != nil && time.Since(c.at) < renameCacheTTL {
		return c.ids
	}
	list, err := servers.ListServers(ctx)
	if err != nil {
		s.cfg.Log.Warn("the servers could not be listed for config names", "err", err)
		return nil
	}
	ids := make(map[string]bool, len(list.Servers))
	for _, sv := range list.Servers {
		if sv.RenamesClients {
			ids[strings.ToLower(sv.ID)] = true
		}
	}
	c.ids, c.at = ids, time.Now()
	return ids
}

// ResetRenameCache forgets which servers rename, so the next list asks the provisioner (tests that change servers).
func (s *Service) ResetRenameCache() {
	s.renames.mu.Lock()
	s.renames.ids = nil
	s.renames.mu.Unlock()
}

// serviceRenamable reports whether a service can be renamed at all: delivered, not deleted, on a server that keeps the
// link.
func serviceRenamable(sub *store.Subscription, renames map[string]bool) bool {
	return sub.ServerID != "" && sub.Status != "pending" && sub.Status != "deleted" && renames[strings.ToLower(sub.ServerID)]
}

// RenamableFor is what the services of owner say about renaming (Subscription.renamable): config names on for them, and
// the service renamable. The servers are asked at most once (and only when config names are on).
func (s *Service) RenamableFor(ctx context.Context, servers ServerAdmin, owner *store.User) func(*store.Subscription) bool {
	if owner == nil || owner.Status != "active" || !s.configNamesOn(ctx, owner) {
		return func(*store.Subscription) bool { return false }
	}
	renames := s.renamingServers(ctx, servers)
	return func(sub *store.Subscription) bool { return sub.UserID == owner.ID && serviceRenamable(sub, renames) }
}

// StaffRenamable is whether staff can rename a service (the dashboard's Rename): whatever the settings, when it is
// renamable.
func (s *Service) StaffRenamable(ctx context.Context, servers ServerAdmin, sub *store.Subscription) bool {
	return serviceRenamable(sub, s.renamingServers(ctx, servers))
}

// RenameResult is RenameOwnService's answer: OK with the service as it is now, or why not (a Name* reason). Name is the
// full name it has, or would have had.
type RenameResult struct {
	OK      bool
	Reason  string
	Name    string
	Service *store.Subscription
}

// RenameOwnService renames one of the user's own services (a customer's, or a reseller's) on its panel and in core, keeping
// its subscription link, and audits it. What the user can get wrong is the answer's reason; a service that is not theirs
// is store.ErrNotFound.
func (s *Service) RenameOwnService(ctx context.Context, prov Provisioner, servers ServerAdmin, userID, subID, name string) (*RenameResult, error) {
	u, err := s.activeUser(ctx, nil, userID)
	if err != nil {
		return nil, err
	}
	sub, err := s.ownService(ctx, u, subID)
	if err != nil {
		return nil, err
	}
	if !s.configNamesOn(ctx, u) {
		return &RenameResult{Reason: NameOff, Service: sub}, nil
	}
	if !serviceRenamable(sub, s.renamingServers(ctx, servers)) {
		return &RenameResult{Reason: NameNotRenamable, Service: sub}, nil
	}
	full, err := s.fullConfigName(ctx, u, name)
	if r := ConfigNameReason(err); r != "" {
		return &RenameResult{Reason: r, Service: sub}, nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.rename(ctx, prov, u, u.ID, sub, full, "")
	if r := ConfigNameReason(err); r != "" {
		return &RenameResult{Reason: r, Name: full, Service: sub}, nil
	}
	if err != nil {
		return nil, err
	}
	return &RenameResult{OK: true, Name: full, Service: out}, nil
}

// RenameSubscription is staff's rename of a service (the dashboard's service page), whatever the config name settings:
// the owner's reseller prefix stays in front. A name core does not take is a *ConfigNameError (ErrInvalid); a service that
// cannot be renamed is ErrState.
func (s *Service) RenameSubscription(ctx context.Context, actor *store.User, prov Provisioner, servers ServerAdmin, id, name, reason string) (*store.Subscription, error) {
	reason, err := needReason(reason)
	if err != nil {
		return nil, err
	}
	sub, err := s.deliveredSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	if !s.StaffRenamable(ctx, servers, sub) {
		return nil, stateErr("this service's server cannot rename a config and keep its link (Marzban, S-UI)")
	}
	owner, err := s.st.GetUser(ctx, s.st.Conn(), sub.UserID)
	if err != nil {
		return nil, err
	}
	full, err := s.fullConfigName(ctx, owner, name)
	if err != nil {
		return nil, err
	}
	return s.rename(ctx, prov, actor, "", sub, full, reason)
}

// rename checks that full is free (exceptUser's orders aside), renames the client on its panel, then records the new name
// with an audit entry. A name taken or unchecked is a *ConfigNameError; a panel that cannot keep the link is ErrState.
func (s *Service) rename(ctx context.Context, prov Provisioner, actor *store.User, exceptUser string, sub *store.Subscription, full, reason string) (*store.Subscription, error) {
	if full == sub.ClientEmail {
		return sub, nil
	}
	why, err := s.nameFree(ctx, prov, full, exceptUser, sub.ID)
	if err != nil {
		return nil, err
	}
	if why != "" {
		return nil, &ConfigNameError{why}
	}
	switch err := prov.RenameClient(ctx, sub.ID, full); {
	case errors.Is(err, ErrClientNameTaken):
		return nil, &ConfigNameError{NameTaken}
	case errors.Is(err, ErrNotRenamable):
		return nil, &ConfigNameError{NameNotRenamable}
	case err != nil:
		return nil, err
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetSubscriptionClientEmail(ctx, tx, sub.ID, full); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "subscription.rename", "subscription", sub.ID,
			map[string]string{"client_email": sub.ClientEmail}, map[string]string{"client_email": full}, reason)
	})
	if err != nil {
		// The panel has the new name and core the old one: put the panel back, so the two agree.
		if back := prov.RenameClient(ctx, sub.ID, sub.ClientEmail); back != nil {
			s.cfg.Log.Warn("a config renamed on its panel could not be recorded nor renamed back", "subscription_id", sub.ID, "err", back)
		}
		if errors.Is(err, store.ErrClientNameTaken) {
			return nil, &ConfigNameError{NameTaken}
		}
		return nil, err
	}
	return s.st.GetSubscription(ctx, s.st.Conn(), sub.ID)
}
