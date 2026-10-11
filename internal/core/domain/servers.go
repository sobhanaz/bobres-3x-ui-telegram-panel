package domain

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// 3x-ui servers (milestone 6a): the provisioner keeps them, with their API
// tokens encrypted under its own key; core only forwards what staff type and
// audits it. A token goes in and never comes back, so it is never stored,
// logged or audited here.

// ServerAdmin is core's view of the provisioner's server registry.
type ServerAdmin interface {
	ListServers(ctx context.Context) (*ServerList, error)
	AddServer(ctx context.Context, in ServerInput) (*Server, error)
	// UpdateServer replaces every setting; an empty token keeps the stored one.
	UpdateServer(ctx context.Context, id string, in ServerInput) (*Server, error)
	DeleteServer(ctx context.Context, id string) error
	// TestServer connects and lists inbounds; it saves nothing.
	TestServer(ctx context.Context, p ServerProbe) (*ServerTest, error)
	ListInbounds(ctx context.Context, serverID string) ([]Inbound, error)
	SetInboundAttach(ctx context.Context, serverID string, inboundID int32, attach bool) error
}

// Server is a registered 3x-ui panel. It never holds the token: HasToken
// only says one is stored.
type Server struct {
	ID, Name, BaseURL, SubBaseURL string
	Enabled, AllowPrivate         bool
	Weight                        int32
	Managed                       string // env | dashboard
	HasToken                      bool
	Clients                       int64 // deliveries in progress included
	PanelVersion                  string
	VersionUntested               bool
	// The last health check; Healthy means nothing while CheckedAt is 0.
	Healthy     bool
	HealthError string
	CheckedAt   int64 // unix seconds
}

// ServerList is every server and the panel versions BOBRES is tested with.
type ServerList struct {
	Servers              []Server
	TestedMin, TestedMax string
}

// Find returns one server of the list; store.ErrNotFound when it has none.
func (l *ServerList) Find(id string) (*Server, error) {
	for i := range l.Servers {
		if strings.EqualFold(l.Servers[i].ID, id) {
			return &l.Servers[i], nil
		}
	}
	return nil, store.ErrNotFound
}

// ServerInput is a server's settings as staff send them. Token is
// write-only: empty keeps the stored one (the provisioner sends it only to
// the host it was entered for).
type ServerInput struct {
	Name, BaseURL, SubBaseURL, Token string
	Weight                           int32
	Enabled, AllowPrivate, SkipCheck bool
}

// ServerProbe is a connection to try without saving: a saved server (ID),
// optionally at a new address or with a new token, or candidate settings.
type ServerProbe struct {
	ID, BaseURL, Token string
	AllowPrivate       bool
}

// ServerTest is the result of a connection test.
type ServerTest struct {
	OK           bool
	PanelVersion string
	Inbounds     int32
	Detail       string // why it failed, in the provisioner's words
}

// Inbound is one of a panel's inbounds and whether new clients join it.
type Inbound struct {
	ID            int32
	Tag, Protocol string
	Port          int32
	Enabled       bool // the panel's own switch
	Attach        bool // new clients join it (BOBRES's switch)
}

// Server limits (the provisioner checks them again).
const (
	maxServerName = 64
	maxTokenLen   = 512
	defaultWeight = 100
)

// addFollowUp bounds what AddServer does once the server exists (switching
// it off, the audit entry).
const addFollowUp = 10 * time.Second

// ErrPrivateOwnerOnly: only the owner lets the provisioner reach private
// addresses with a token of their choosing (an admin could otherwise probe
// the internal network through it).
var ErrPrivateOwnerOnly = fmt.Errorf("%w: only the store owner can let BOBRES reach a panel on a private network", ErrForbidden)

// normalizeServer trims and checks what staff typed; a FieldError names the
// field. adding: a new server needs a token and takes weight 0 as 100.
func normalizeServer(in ServerInput, adding bool) (ServerInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	in.SubBaseURL = strings.TrimSpace(in.SubBaseURL)
	in.Token = strings.TrimSpace(in.Token)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > maxServerName || hasControl(in.Name) {
		return in, &FieldError{"name", fmt.Sprintf("a name of 1 to %d characters on one line", maxServerName)}
	}
	if err := checkPanelURL(in.BaseURL, in.AllowPrivate); err != nil {
		return in, err
	}
	if in.SubBaseURL != "" {
		u, err := url.Parse(in.SubBaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || hasSpace(in.SubBaseURL) {
			return in, &FieldError{"sub_base_url", "an http(s) address such as https://sub.example.com:2096/sub/, or empty"}
		}
	}
	switch {
	case hasSpace(in.Token) || len(in.Token) > maxTokenLen:
		return in, &FieldError{"token", "the API token is one word without spaces"}
	case adding && in.Token == "":
		return in, &FieldError{"token", "the panel's API token is required (3x-ui: Settings > Security)"}
	}
	if adding && in.Weight == 0 {
		in.Weight = defaultWeight
	}
	if in.Weight < 1 || in.Weight > 1000 {
		return in, &FieldError{"weight", "a whole number from 1 to 1000"}
	}
	return in, nil
}

// checkPanelURL: https, or plain http for a panel on a private network, and
// never a user name or password in the address. It never repeats the value.
func checkPanelURL(raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	switch {
	case raw == "" || err != nil || u.Host == "" || hasSpace(raw) || (u.Scheme != "https" && u.Scheme != "http"):
		return &FieldError{"base_url", "the panel's address, such as https://panel.example.com:2053/path"}
	case u.User != nil:
		return &FieldError{"base_url", "no user name or password in the address: the API token is the login"}
	case u.Scheme == "http" && !allowPrivate:
		return &FieldError{"base_url", "plain http only for a panel on this server or a private network (allow private addresses)"}
	}
	return nil
}

func hasSpace(s string) bool { return strings.IndexFunc(s, unicode.IsSpace) >= 0 || hasControl(s) }

func hasControl(s string) bool { return strings.IndexFunc(s, unicode.IsControl) >= 0 }

// sameAddress compares two panel addresses as staff typed them (a trailing
// slash aside).
func sameAddress(a, b string) bool {
	return strings.TrimRight(strings.TrimSpace(a), "/") == strings.TrimRight(strings.TrimSpace(b), "/")
}

// privateNeedsOwner: a connection to a private address, with an address or a
// token that is new, is the owner's to allow. An admin may still rename,
// reweight or switch off a private server as long as its address and token
// stay.
func privateNeedsOwner(actor *store.User, allowPrivate bool, baseURL, token string, stored *Server) bool {
	if actor.Role == "owner" || !allowPrivate {
		return false
	}
	return stored == nil || !stored.AllowPrivate || token != "" || !sameAddress(baseURL, stored.BaseURL)
}

// serverAudit is what an audit entry keeps of a server: its settings, and
// "changed" for a token that was sent (never the token).
func serverAudit(sv *Server, tokenSent bool) map[string]any {
	out := map[string]any{
		"name": sv.Name, "base_url": sv.BaseURL, "sub_base_url": sv.SubBaseURL, "enabled": sv.Enabled,
		"allow_private": sv.AllowPrivate, "weight": sv.Weight,
	}
	if tokenSent {
		out["token"] = "changed"
	}
	return out
}

// AddServer registers a panel; the provisioner tests the connection first
// unless SkipCheck. The provisioner adds servers switched on: one added
// switched off is switched off right after (nothing is tested again). When
// that fails the server is taken out again, so it never takes new services
// against the owner's wish; if even that fails, staff are told it is on.
func (s *Service) AddServer(ctx context.Context, actor *store.User, sa ServerAdmin, in ServerInput) (*Server, error) {
	in, err := normalizeServer(in, true)
	if err != nil {
		return nil, err
	}
	if privateNeedsOwner(actor, in.AllowPrivate, in.BaseURL, in.Token, nil) {
		return nil, ErrPrivateOwnerOnly
	}
	sv, err := sa.AddServer(ctx, in)
	if err != nil {
		return nil, err
	}
	// The server exists now: the rest runs to the end even if the request is
	// cancelled (or its time is used up by the connection test).
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), addFollowUp)
	defer cancel()
	var stillOn error
	if !in.Enabled && sv.Enabled {
		off := in
		off.Token, off.SkipCheck = "", true
		got, err := sa.UpdateServer(ctx, sv.ID, off)
		if err == nil {
			sv = got
		} else {
			s.cfg.Log.Warn("new server: switching it off failed", "server_id", sv.ID, "err", err)
			derr := sa.DeleteServer(ctx, sv.ID)
			if derr == nil {
				return nil, stateErr("the server could not be saved switched off, so it was not added: try again")
			}
			s.cfg.Log.Warn("new server: removing it again failed", "server_id", sv.ID, "err", derr)
			stillOn = stateErr("the server was added but takes new services, as switching it off failed: switch it off in the list")
		}
	}
	if err := s.audit(ctx, nil, actor, "server.add", "server", sv.ID, serverAudit(sv, true), ""); err != nil {
		return nil, err
	}
	if stillOn != nil {
		return nil, stillOn
	}
	return sv, nil
}

// UpdateServer replaces a server's settings. When the address its links are
// built from changes, the stored links of its services are fetched again in
// the background (SubLinkWorker).
func (s *Service) UpdateServer(ctx context.Context, actor *store.User, sa ServerAdmin, id string, in ServerInput) (*Server, error) {
	in, err := normalizeServer(in, false)
	if err != nil {
		return nil, err
	}
	list, err := sa.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	before, err := list.Find(id)
	if err != nil {
		return nil, err
	}
	if privateNeedsOwner(actor, in.AllowPrivate, in.BaseURL, in.Token, before) {
		return nil, ErrPrivateOwnerOnly
	}
	sv, err := sa.UpdateServer(ctx, before.ID, in)
	if err != nil {
		return nil, err
	}
	relink := sv.SubBaseURL != before.SubBaseURL || sv.BaseURL != before.BaseURL
	var stale int64
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.auditChange(ctx, tx, actor, "server.update", "server", sv.ID,
			serverAudit(before, false), serverAudit(sv, in.Token != ""), ""); err != nil {
			return err
		}
		if !relink {
			return nil
		}
		stale, err = s.st.MarkSubLinksStale(ctx, tx, sv.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if stale > 0 {
		s.cfg.Log.Info("server address changed: refreshing stored subscription links", "server_id", sv.ID, "services", stale)
		s.kickSubLinks()
	}
	return sv, nil
}

// DeleteServer removes a server. It is refused while a plan names it (its
// paid orders could never be delivered, and taking the server out of the
// plan's list could widen it to any server), and the provisioner refuses
// while services are on it.
func (s *Service) DeleteServer(ctx context.Context, actor *store.User, sa ServerAdmin, id string) error {
	list, err := sa.ListServers(ctx)
	if err != nil {
		return err
	}
	before, err := list.Find(id)
	if err != nil {
		return err
	}
	names, err := s.plansNaming(ctx, before.ID)
	if err != nil {
		return err
	}
	if len(names) > 0 {
		return stateErr("plans still send new services to this server (%s): take it out of them first", strings.Join(names, ", "))
	}
	if err := sa.DeleteServer(ctx, before.ID); err != nil {
		return err
	}
	return s.auditChange(ctx, nil, actor, "server.delete", "server", before.ID, serverAudit(before, false), nil, "")
}

// plansNaming lists, quoted, the names of the plans (on sale or not) that
// send new services to a server: the first few, then how many more.
func (s *Service) plansNaming(ctx context.Context, serverID string) ([]string, error) {
	plans, err := s.st.ListPlans(ctx, s.st.Conn(), true)
	if err != nil {
		return nil, err
	}
	const shown = 3
	var names []string
	more := 0
	for i := range plans {
		if !slices.ContainsFunc(plans[i].ServerIDs, func(sid string) bool { return strings.EqualFold(sid, serverID) }) {
			continue
		}
		if len(names) == shown {
			more++
			continue
		}
		names = append(names, `"`+planName(&plans[i], "a plan")+`"`)
	}
	if more > 0 {
		names = append(names, fmt.Sprintf("and %d more", more))
	}
	return names, nil
}

// TestServer tries a connection without saving anything.
func (s *Service) TestServer(ctx context.Context, actor *store.User, sa ServerAdmin, p ServerProbe) (*ServerTest, error) {
	p.BaseURL, p.Token = strings.TrimSpace(p.BaseURL), strings.TrimSpace(p.Token)
	if hasSpace(p.Token) || len(p.Token) > maxTokenLen {
		return nil, &FieldError{"token", "the API token is one word without spaces"}
	}
	var stored *Server
	if p.ID != "" {
		list, err := sa.ListServers(ctx)
		if err != nil {
			return nil, err
		}
		if stored, err = list.Find(p.ID); err != nil {
			return nil, err
		}
		p.ID = stored.ID
		if p.BaseURL == "" { // the stored connection, as it is
			p.BaseURL, p.AllowPrivate = stored.BaseURL, stored.AllowPrivate
		}
	} else if p.Token == "" {
		return nil, &FieldError{"token", "the panel's API token is required (3x-ui: Settings > Security)"}
	}
	if err := checkPanelURL(p.BaseURL, p.AllowPrivate); err != nil {
		return nil, err
	}
	if privateNeedsOwner(actor, p.AllowPrivate, p.BaseURL, p.Token, stored) {
		return nil, ErrPrivateOwnerOnly
	}
	return sa.TestServer(ctx, p)
}

// SetInboundAttach says whether new services join one inbound of a server
// (existing ones keep theirs).
func (s *Service) SetInboundAttach(ctx context.Context, actor *store.User, sa ServerAdmin, serverID string, inboundID int32, attach bool) error {
	if inboundID <= 0 {
		return store.ErrNotFound
	}
	if err := sa.SetInboundAttach(ctx, serverID, inboundID, attach); err != nil {
		return err
	}
	return s.audit(ctx, nil, actor, "server.inbound", "server", serverID,
		map[string]any{"inbound_id": inboundID, "attach": attach}, "")
}
