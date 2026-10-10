package web

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// Shared pieces of the dashboard's data endpoints: paging, ids, amounts,
// and turning domain errors into answers.

const (
	defaultPageSize = 25
	maxPageSize     = 100
)

// pageOf reads ?page= (from 1) and ?size= (1-100).
func pageOf(r *http.Request) (store.Page, int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if page < 1 || page > 100000 {
		page = 1
	}
	if size < 1 || size > maxPageSize {
		size = defaultPageSize
	}
	return store.Page{Limit: size, Offset: (page - 1) * size}, page, size
}

// list is a page of a list.
type list struct {
	Items any   `json:"items"`
	Total int64 `json:"total"`
	Page  int   `json:"page"`
	Size  int   `json:"size"`
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// pathID is a path's {name} when it is a UUID; otherwise it answers 404.
func pathID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id := strings.ToLower(r.PathValue(name))
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "no such item")
		return "", false
	}
	return id, true
}

// queryID is a query parameter that must be a UUID when present.
func queryID(r *http.Request, name string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get(name)))
	return v, v == "" || uuidRe.MatchString(v)
}

// idemKeyRe: what the dashboard sends as an idempotency key (a random id).
var idemKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// parseAmount turns "12,500" or "-1.25" (major units) into minor units of a
// currency. signed allows a leading minus.
func parseAmount(s, currency string, signed bool) (int64, bool) {
	scale := bmoney.Scale(currency)
	if scale < 0 {
		return 0, false
	}
	return parseDecimal(s, scale, signed)
}

// parseDecimal reads a decimal with at most scale fraction digits as an
// integer count of 10^-scale units.
func parseDecimal(s string, scale int, signed bool) (int64, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	neg := false
	if signed && strings.HasPrefix(s, "-") {
		neg, s = true, s[1:]
	}
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" && frac == "" || len(frac) > scale || len(whole) > 15 {
		return 0, false
	}
	for _, part := range []string{whole, frac} {
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
	}
	frac += strings.Repeat("0", scale-len(frac))
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}

// gbToBytes reads a traffic amount in GB (GiB, up to 3 decimals): "" is 0.
func gbToBytes(s string) (int64, bool) {
	if strings.TrimSpace(s) == "" {
		return 0, true
	}
	milli, ok := parseDecimal(s, 3, false)
	if !ok || milli > 100_000_000 { // 100,000 GB
		return 0, false
	}
	return milli * (1 << 30) / 1000, true
}

// unix is a time as unix seconds, or null.
func unix(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.Unix()
	return &v
}

// userBrief names a user in other items.
type userBrief struct {
	ID         string `json:"id"`
	TelegramID int64  `json:"telegram_id"`
	Username   string `json:"username"`
}

func briefOf(id string, tg int64, username string) *userBrief {
	return &userBrief{ID: id, TelegramID: tg, Username: username}
}

// usersByID loads the users some items point at.
func (s *Server) usersByID(ctx context.Context, ids []string) map[string]*userBrief {
	out := map[string]*userBrief{}
	if len(ids) == 0 {
		return out
	}
	users, err := s.cfg.Store.UsersByID(ctx, s.cfg.Store.Conn(), ids)
	if err != nil {
		s.cfg.Log.Warn("dashboard api: load users", "err", err)
		return out
	}
	for id, u := range users {
		out[id] = briefOf(u.ID, u.TelegramID, u.Username)
	}
	return out
}

// fail answers a domain or backend error: the reason when it helps staff,
// a generic message otherwise.
func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	if settingError(w, err) {
		return
	}
	var de *domain.DiscountError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such item")
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "this may not be done to this item")
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", reason(err, domain.ErrInvalid))
	case errors.Is(err, domain.ErrState):
		writeError(w, http.StatusConflict, "state", reason(err, domain.ErrState))
	case errors.Is(err, domain.ErrNotExtendable):
		writeError(w, http.StatusConflict, "state", reason(err, domain.ErrNotExtendable))
	case errors.As(err, &de):
		writeError(w, http.StatusBadRequest, "invalid", "discount code: "+de.Reason)
	case errors.Is(err, store.ErrInsufficientFunds):
		writeError(w, http.StatusConflict, "insufficient_funds", "the balance is too low for this")
	case errors.Is(err, store.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "conflict", "this request clashes with an earlier one; reload and try again")
	case backendDown(err):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "a service this needs (or the 3x-ui panel) did not answer; try again in a minute")
	case status.Code(unwrapAll(err)) == codes.FailedPrecondition:
		// The provisioner refuses a panel address by policy: the operator must see why.
		writeError(w, http.StatusConflict, "state", status.Convert(unwrapAll(err)).Message())
	default:
		s.internal(w, what, err)
	}
}

// reason is what follows a sentinel error's text ("domain: not possible in
// the current state: the service was deleted" gives "the service was deleted").
func reason(err, sentinel error) string {
	msg := err.Error()
	if _, after, ok := strings.Cut(msg, sentinel.Error()+": "); ok {
		return after
	}
	return msg
}

// backendDown: payments or the provisioner (or the panel behind it) did not
// answer.
func backendDown(err error) bool {
	switch status.Code(unwrapAll(err)) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// unwrapAll returns the innermost error (a gRPC status survives %w wrapping
// only at the bottom of the chain).
func unwrapAll(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}

// actor is the staff member making the request.
func actor(r *http.Request) *store.User { return staffFrom(r.Context()).user }
