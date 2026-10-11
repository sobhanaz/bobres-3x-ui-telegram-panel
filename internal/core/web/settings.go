package web

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// BotPeer is what the dashboard asks the bot (botfiles.Client).
type BotPeer interface {
	RefreshSettings(ctx context.Context) error
	CheckChannel(ctx context.Context, chat string) (*botfiles.ChannelCheck, error)
}

// settingsGroups are the groups of the Settings page; branding and texts
// have their own page, the referral reward is on the Discounts page.
var settingsGroups = []string{"general", "maintenance", "join", "notify", "limits", "payments"}

type settingJSON struct {
	Key      string   `json:"key"`
	Group    string   `json:"group"`
	Kind     string   `json:"kind"`
	Value    string   `json:"value"`
	Default  string   `json:"default"`
	Options  []string `json:"options"`
	Min      *int64   `json:"min"`
	Max      *int64   `json:"max"`
	Editable bool     `json:"editable"`
}

// getSettings: GET /api/v1/settings, the Settings page.
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	values, err := s.cfg.Domain.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	role := actor(r).Role
	items := []settingJSON{}
	for _, sp := range domain.SettingSpecs {
		if !slices.Contains(settingsGroups, sp.Group) {
			continue
		}
		it := settingJSON{Key: sp.Key, Group: sp.Group, Kind: sp.Kind, Value: values[sp.Key], Default: sp.Default,
			Options: append([]string{}, sp.Options...), Editable: Allowed(role, sp.Perm)}
		if sp.Kind == domain.KindInt {
			it.Min, it.Max = &sp.Min, &sp.Max
		}
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "groups": settingsGroups})
}

// putSettings: PUT /api/v1/settings {values:{key:value}}, all or nothing.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Values map[string]string `json:"values"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	role := actor(r).Role
	for k := range req.Values {
		sp, ok := domain.SpecOf(k)
		if !ok || !slices.Contains(settingsGroups, sp.Group) {
			writeFieldError(w, k, "not a setting of this page")
			return
		}
		if !Allowed(role, sp.Perm) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden", "field": k,
				"message": "only the store owner can change this"})
			return
		}
	}
	if _, err := s.cfg.Domain.SetSettings(r.Context(), actor(r), req.Values); err != nil {
		s.fail(w, "save settings", err)
		return
	}
	s.refreshBot()
	s.getSettings(w, r)
}

// checkChannel: POST /api/v1/settings/channel-check {chat}, asks the bot
// whether it can check who joined the channel.
func (s *Server) checkChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Chat string `json:"chat"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	chat, err := domain.NormalizeSetting("join.channel", req.Chat)
	if err != nil || chat == "" {
		writeFieldError(w, "join.channel", "a public channel's @name, or a private channel's id starting with -100")
		return
	}
	if s.cfg.Bot == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the bot's address is not configured (BOBRES_BOT_URL)")
		return
	}
	res, err := s.cfg.Bot.CheckChannel(r.Context(), chat)
	if err != nil {
		s.cfg.Log.Warn("channel check", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the bot did not answer; try again in a minute")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// refreshBot tells the bot a setting changed, without making the staff
// member wait: the bot also reloads every minute.
func (s *Server) refreshBot() {
	if s.cfg.Bot == nil {
		return
	}
	go func() {
		if err := s.cfg.Bot.RefreshSettings(context.Background()); err != nil {
			s.cfg.Log.Info("bot settings refresh (it reloads within a minute anyway)", "err", err)
		}
	}()
}

func writeFieldError(w http.ResponseWriter, field, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "field": field, "message": field + ": " + msg})
}

// textProblem is one problem of a bot text, for the dashboard.
type textProblem struct {
	Key  string `json:"key,omitempty"`
	Lang string `json:"lang,omitempty"`
	Code string `json:"code"`
	Arg  string `json:"arg"`
}

// settingError answers the domain's setting errors with the field or the
// text problems; false when err is not one of them.
func settingError(w http.ResponseWriter, err error) bool {
	var (
		fe  *domain.FieldError
		te  *domain.TextError
		tes *domain.TextsError
	)
	switch {
	case errors.As(err, &tes):
		var probs []textProblem
		for _, t := range tes.Texts {
			for _, p := range t.Problems {
				probs = append(probs, textProblem{Key: t.Key, Lang: t.Lang, Code: p.Code, Arg: p.Arg})
			}
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "message": tes.Error(), "problems": probs})
	case errors.As(err, &te):
		probs := make([]textProblem, 0, len(te.Problems))
		for _, p := range te.Problems {
			probs = append(probs, textProblem{Key: te.Key, Lang: te.Lang, Code: p.Code, Arg: p.Arg})
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "message": reason(err, domain.ErrInvalid), "problems": probs})
	case errors.As(err, &fe):
		writeFieldError(w, fe.Field, fe.Msg)
	default:
		return false
	}
	return true
}
