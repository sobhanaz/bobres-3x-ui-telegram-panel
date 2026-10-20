package domain

import (
	"context"
	crand "crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Gift codes (docs/01-database.md, docs/03-bot-ux.md): a code a customer
// applies from the bot's wallet screen that adds a fixed amount to their wallet.
//
// How a code is kept: never in clear. The database holds an HMAC of its
// normalised form under a key from core's master key (giftHash), so a dump or
// a backup cannot be turned back into codes or tried offline, and the list in
// the dashboard shows a code's last four characters only. A code is shown in
// full once: in the answer that made it.

// SettingGiftCodes is the owner's switch for gift codes (on unless "false").
const SettingGiftCodes = "gift_codes.enabled"

// The limits of what staff make, and of what a customer may get wrong.
const (
	maxGiftBatch       = 500       // codes one request makes
	maxGiftRedemptions = 1_000_000 // uses of one code, in all
	maxGiftPerCustomer = 100       // uses of one code by one customer
	maxGiftNote        = 200       // characters of a note
	maxGiftExpiry      = 10 * 365 * 24 * time.Hour

	// A customer who sends giftFailLimit wrong codes within giftFailWindow
	// cannot enter a gift code for giftLockFor. Only wrong codes count (never a
	// right one, and a success does not reset the count), so the one who owns a
	// valid multi-use code cannot buy more guesses with it.
	giftFailLimit  = 5
	giftFailWindow = time.Hour
	giftLockFor    = time.Hour
)

// Why a code was not redeemed (GiftCodeError.Reason).
const (
	// GiftInvalid is the answer for a code that is unknown, switched off or
	// expired, and for text that cannot be a code: the same one for all, so
	// nothing tells a guesser which codes exist.
	GiftInvalid = "invalid"
	// GiftUsedUp: the code has been redeemed as often as it may be (told only
	// to someone who sent the whole code).
	GiftUsedUp = "used_up"
	// GiftAlreadyRedeemed: this customer used the code as often as they may.
	GiftAlreadyRedeemed = "already_redeemed"
	// GiftLocked: too many wrong codes; RetryAfter says how long.
	GiftLocked = "locked"
	// GiftOff: the owner switched gift codes off (or core cannot key them).
	GiftOff = "off"
)

// GiftCodeError says why a code was not redeemed. It carries nothing else: no
// part of a code, no state of one that was not guessed whole.
type GiftCodeError struct {
	Reason string
	// RetryAfter is how long entering codes stays closed (GiftLocked only).
	RetryAfter time.Duration
}

func (e *GiftCodeError) Error() string { return "gift code: " + e.Reason }

// errGiftNoKey is what staff are told when core has no master key.
func errGiftNoKey() error {
	return stateErr("gift codes need core's master key (BOBRES_CORE_MASTER_KEY in .env), which this install does not have")
}

// GiftCodesAvailable: core can key gift codes (it has a master key).
func (s *Service) GiftCodesAvailable() bool { return s.cfg.Secrets != nil }

// GiftCodesOn reads the owner's switch.
func (s *Service) GiftCodesOn(ctx context.Context) bool {
	return s.setting(ctx, SettingGiftCodes) != "false"
}

// giftHash is how a normalised code is stored and looked up. It is keyed, so
// whoever holds the database but not the master key cannot compute it, and the
// lookup's timing tells nothing: a guesser cannot choose a hash.
func (s *Service) giftHash(canon string) []byte {
	return s.cfg.Secrets.Tag([]byte(giftHashContext + canon))
}

// GiftCodeSpec is what staff ask for.
type GiftCodeSpec struct {
	Amount   int64 // in minor units of Currency
	Currency string
	// MaxRedemptions is how many times the code may be redeemed in all, by
	// anyone; PerCustomerLimit how many by one customer (0: once).
	MaxRedemptions   int
	PerCustomerLimit int
	ExpiresAt        *time.Time
	Note             string
	// Code is a code staff chose (one code: Count must be 0 or 1, no Prefix);
	// empty: the codes are generated, Count of them (0: one), each starting
	// with Prefix when there is one.
	Code   string
	Prefix string
	Count  int
}

// NewGiftCode is a code that was just made: its only copy, and its row.
type NewGiftCode struct {
	Code string // ABCD-EFGH-JKMN, as the customer types it
	store.GiftCode
}

// GiftCodeStatus is where a code stands now: disabled (switched off),
// expired, used_up or active, the first that applies.
func GiftCodeStatus(g *store.GiftCode, now time.Time) string {
	switch {
	case !g.Enabled:
		return "disabled"
	case g.ExpiresAt != nil && !now.Before(*g.ExpiresAt):
		return "expired"
	case g.Redeemed >= g.MaxRedemptions:
		return "used_up"
	}
	return "active"
}

// checkGiftSpec checks what staff asked for and returns it normalised.
func checkGiftSpec(spec GiftCodeSpec, now time.Time) (GiftCodeSpec, error) {
	spec.Currency = strings.ToUpper(strings.TrimSpace(spec.Currency))
	switch {
	case money.Scale(spec.Currency) < 0:
		return spec, &FieldError{"currency", "unsupported currency"}
	case spec.Amount <= 0 || spec.Amount > maxTopupMinor:
		return spec, &FieldError{"amount", "out of range"}
	case spec.MaxRedemptions < 1 || spec.MaxRedemptions > maxGiftRedemptions:
		return spec, &FieldError{"max_redemptions", fmt.Sprintf("1 to %d", maxGiftRedemptions)}
	}
	if spec.PerCustomerLimit == 0 {
		spec.PerCustomerLimit = 1
	}
	switch {
	case spec.PerCustomerLimit < 1 || spec.PerCustomerLimit > maxGiftPerCustomer || spec.PerCustomerLimit > spec.MaxRedemptions:
		return spec, &FieldError{"per_customer_limit", fmt.Sprintf("1 to %d, and no more than the uses in all", maxGiftPerCustomer)}
	case spec.Amount > math.MaxInt64/int64(spec.MaxRedemptions):
		return spec, &FieldError{"amount", "too large for this many uses"}
	}
	if spec.ExpiresAt != nil {
		switch e := *spec.ExpiresAt; {
		case !e.After(now):
			return spec, &FieldError{"expires_at", "must be in the future"}
		case e.After(now.Add(maxGiftExpiry)):
			return spec, &FieldError{"expires_at", "at most 10 years ahead"}
		}
	}
	spec.Note = strings.TrimSpace(spec.Note)
	switch {
	case !utf8.ValidString(spec.Note) || strings.ContainsFunc(spec.Note, unicode.IsControl):
		return spec, &FieldError{"note", "one line of text"}
	case utf8.RuneCountInString(spec.Note) > maxGiftNote:
		return spec, &FieldError{"note", fmt.Sprintf("at most %d characters", maxGiftNote)}
	}
	if strings.TrimSpace(spec.Code) != "" {
		if spec.Count > 1 {
			return spec, &FieldError{"count", "a code of your own is one code"}
		}
		if strings.TrimSpace(spec.Prefix) != "" {
			return spec, &FieldError{"prefix", "a code of your own has no prefix"}
		}
		spec.Count = 1
		return spec, nil
	}
	if spec.Count == 0 {
		spec.Count = 1
	}
	if spec.Count < 1 || spec.Count > maxGiftBatch {
		return spec, &FieldError{"count", fmt.Sprintf("1 to %d", maxGiftBatch)}
	}
	prefix, ok := normalizeGiftPrefix(spec.Prefix)
	if !ok {
		return spec, &FieldError{"prefix", fmt.Sprintf("at most %d letters or digits", giftMaxPrefixLen)}
	}
	spec.Prefix = prefix
	return spec, nil
}

// CreateGiftCodes makes one gift code of the owner's own words, or a batch of
// generated ones, in one transaction (all or none), with one audit entry each.
// The caller has checked that the actor may. The codes are in the answer and
// nowhere else: it is the only time they can be read.
func (s *Service) CreateGiftCodes(ctx context.Context, actor *store.User, spec GiftCodeSpec) ([]NewGiftCode, error) {
	if s.cfg.Secrets == nil {
		return nil, errGiftNoKey()
	}
	spec, err := checkGiftSpec(spec, time.Now())
	if err != nil {
		return nil, err
	}
	var (
		custom, display string
		generated       = strings.TrimSpace(spec.Code) == ""
	)
	if !generated {
		if custom, display, err = checkCustomGiftCode(spec.Code); err != nil {
			return nil, err
		}
	}
	var out []NewGiftCode
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		out = out[:0]
		for range spec.Count {
			g := store.GiftCode{Generated: generated, Amount: spec.Amount, Currency: spec.Currency,
				MaxRedemptions: spec.MaxRedemptions, PerCustomerLimit: spec.PerCustomerLimit, ExpiresAt: spec.ExpiresAt,
				Enabled: true, Note: spec.Note, CreatedBy: &actor.ID}
			shown := display
			if generated {
				// A generated code that already exists (two in 10^17 draws)
				// is drawn again; a code of the owner's own that exists is
				// refused, so they pick another.
				var inserted bool
				for attempt := 0; !inserted; attempt++ {
					if attempt == 5 {
						return errGiftRandom
					}
					body, err := randomGiftBody(crand.Reader)
					if err != nil {
						return err
					}
					canon := spec.Prefix + body
					g.Tail = canon[len(canon)-giftGroupLen:]
					if inserted, err = s.st.InsertGiftCode(ctx, tx, &g, s.giftHash(canon)); err != nil {
						return err
					}
					shown = formatGiftCode(spec.Prefix, body)
				}
			} else {
				g.Tail = custom[len(custom)-giftGroupLen:]
				inserted, err := s.st.InsertGiftCode(ctx, tx, &g, s.giftHash(custom))
				if err != nil {
					return err
				}
				if !inserted {
					return &FieldError{"code", "this code exists already: choose another"}
				}
			}
			if err := s.audit(ctx, tx, actor, "gift_code.create", "gift_code", g.ID, giftAudit(&g, spec.Count), ""); err != nil {
				return err
			}
			out = append(out, NewGiftCode{Code: shown, GiftCode: g})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// giftAudit is what an audit entry says about a code: its terms and its last
// four characters, never the code.
func giftAudit(g *store.GiftCode, batch int) map[string]any {
	m := map[string]any{"tail": g.Tail, "generated": g.Generated, "amount": g.Amount, "currency": g.Currency,
		"max_redemptions": g.MaxRedemptions, "per_customer_limit": g.PerCustomerLimit, "enabled": g.Enabled}
	if g.ExpiresAt != nil {
		m["expires_at"] = g.ExpiresAt.Unix()
	}
	if g.Note != "" {
		m["note"] = g.Note
	}
	if batch > 1 {
		m["batch"] = batch
	}
	return m
}

// SetGiftCodeEnabled switches a code off or on again. What it has paid out
// stays paid (nothing is taken back); a code that is off answers as an unknown
// one. The caller has checked that the actor may.
func (s *Service) SetGiftCodeEnabled(ctx context.Context, actor *store.User, id string, enabled bool) (*store.GiftCode, error) {
	var out *store.GiftCode
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		g, err := s.st.LockGiftCode(ctx, tx, id)
		if err != nil {
			return err
		}
		if g.Enabled == enabled {
			out = g
			return nil
		}
		if err := s.st.SetGiftCodeEnabled(ctx, tx, id, enabled); err != nil {
			return err
		}
		action := "gift_code.disable"
		if enabled {
			action = "gift_code.enable"
		}
		if err := s.auditChange(ctx, tx, actor, action, "gift_code", id,
			map[string]any{"enabled": g.Enabled, "tail": g.Tail}, map[string]any{"enabled": enabled, "tail": g.Tail}, ""); err != nil {
			return err
		}
		g.Enabled = enabled
		out = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListGiftCodes lists the codes, newest first, with how many match.
func (s *Service) ListGiftCodes(ctx context.Context, f store.GiftCodeFilter, p store.Page) ([]store.GiftCode, int64, error) {
	switch f.Status {
	case "", "active", "disabled", "expired", "used_up":
	default:
		return nil, 0, invalid("status must be active, disabled, expired or used_up")
	}
	return s.st.ListGiftCodes(ctx, s.st.Conn(), f, p)
}

// GiftCodeRedemptions lists who redeemed one code, newest first.
func (s *Service) GiftCodeRedemptions(ctx context.Context, id string, p store.Page) (*store.GiftCode, []store.GiftRedemptionRow, int64, error) {
	g, err := s.st.GetGiftCode(ctx, s.st.Conn(), id)
	if err != nil {
		return nil, nil, 0, err
	}
	rows, total, err := s.st.ListGiftRedemptions(ctx, s.st.Conn(), id, p)
	return g, rows, total, err
}

// RedeemGiftCodeParams are the inputs of RedeemGiftCode.
type RedeemGiftCodeParams struct {
	UserID string
	// Code is what the customer typed, as they typed it.
	Code string
	// RequestKey identifies the request (the bot's message): a retry with the
	// same key finds its first answer and credits nothing more.
	RequestKey string
}

// GiftRedeemed is a code that was applied.
type GiftRedeemed struct {
	Amount   int64
	Currency string
	// Balance is the wallet's balance now.
	Balance int64
	// Replayed: the request had been served before; nothing was credited this time.
	Replayed bool
}

// RedeemGiftCode adds a gift code's amount to the customer's wallet. Active
// customers and resellers may; a banned account may not.
//
// The whole of it is one transaction that holds the code's row: the code is
// checked (on, not expired, used as often as it may be in all and by this
// customer), the wallet is credited under the key gift:<code>:<customer>:<n>
// (the n-th time this customer redeems it), the redemption is recorded with
// the ledger entry it wrote and the request's key, the code's count goes up and
// the audit entry and the wallet.credited.v1 event are written. Two
// customers redeeming the last use of a code take turns: one wins. A request
// repeated with the same key returns the first answer, whatever the code is
// now.
//
// The answers a guesser could learn from are one: a code that is unknown,
// switched off or expired, and text that cannot be a code, are all
// GiftInvalid, and each counts as a wrong code (giftFailLimit in giftFailWindow
// lock entering codes for giftLockFor). Only someone who sent a whole code is
// told it is used up, or that they used it already.
func (s *Service) RedeemGiftCode(ctx context.Context, p RedeemGiftCodeParams) (*GiftRedeemed, error) {
	key := strings.TrimSpace(p.RequestKey)
	if _, err := buuid.Parse(p.UserID); err != nil {
		return nil, invalid("user required")
	}
	switch {
	case key == "" || len(key) > 128:
		return nil, invalid("an idempotency key of at most 128 characters is required")
	}
	if s.cfg.Secrets == nil || !s.GiftCodesOn(ctx) {
		return nil, &GiftCodeError{Reason: GiftOff}
	}
	u, err := s.activeUser(ctx, nil, p.UserID)
	if err != nil {
		return nil, err
	}
	canon, wellFormed := normalizeGiftCode(p.Code)
	var hash []byte
	if wellFormed {
		hash = s.giftHash(canon)
	}

	// A request that was served before gets its answer again, whatever has
	// happened to the code (or to this customer's count of wrong ones) since.
	if r, rhash, err := s.st.GiftRedemptionByKey(ctx, s.st.Conn(), u.ID, key); err == nil {
		return s.giftReplay(ctx, nil, r, rhash, hash)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	wait, err := s.st.GiftLockedFor(ctx, s.st.Conn(), u.ID)
	if err != nil {
		return nil, err
	}
	if wait > 0 {
		return nil, &GiftCodeError{Reason: GiftLocked, RetryAfter: wait}
	}
	if !wellFormed {
		return nil, s.giftWrong(ctx, u)
	}

	var out *GiftRedeemed
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		g, err := s.st.LockGiftCodeByHash(ctx, tx, hash)
		if errors.Is(err, store.ErrNotFound) {
			return &GiftCodeError{Reason: GiftInvalid}
		}
		if err != nil {
			return err
		}
		// Found by this very hash, so equal; compared again in constant time
		// as the last line against a lookup that ever matched the wrong row.
		if subtle.ConstantTimeCompare(g.Hash, hash) != 1 {
			return &GiftCodeError{Reason: GiftInvalid}
		}
		// The customer may have been banned, or this very request served by
		// a concurrent copy of it, while the lock was awaited.
		if _, err := s.activeUser(ctx, tx, u.ID); err != nil {
			return err
		}
		if r, rhash, err := s.st.GiftRedemptionByKey(ctx, tx, u.ID, key); err == nil {
			if out, err = s.giftReplay(ctx, tx, r, rhash, hash); err != nil {
				return err
			}
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		n, err := s.st.CountGiftRedemptions(ctx, tx, g.ID, u.ID)
		if err != nil {
			return err
		}
		// What this customer did with the code themselves comes first: it
		// tells nobody anything they did not do.
		switch {
		case n >= g.PerCustomerLimit:
			return &GiftCodeError{Reason: GiftAlreadyRedeemed}
		case !g.Enabled, g.ExpiresAt != nil && !time.Now().Before(*g.ExpiresAt):
			return &GiftCodeError{Reason: GiftInvalid}
		case g.Redeemed >= g.MaxRedemptions:
			return &GiftCodeError{Reason: GiftUsedUp}
		}
		seq := n + 1
		entry := &store.LedgerEntry{Kind: "gift_code", RefType: strptr("gift_code"), RefID: strptr(g.ID),
			IdempotencyKey: fmt.Sprintf("gift:%s:%s:%d", g.ID, u.ID, seq)}
		w, err := s.st.Credit(ctx, tx, u.ID, g.Currency, g.Amount, entry)
		if errors.Is(err, store.ErrDuplicateIdempotency) {
			// A ledger entry under this key with no redemption beside it:
			// the data disagrees with itself. Refuse; never guess.
			return fmt.Errorf("gift code %s, customer %s, redemption %d: %w", g.ID, u.ID, seq, store.ErrIdempotencyConflict)
		}
		if err != nil {
			return err
		}
		if err := s.st.InsertGiftRedemption(ctx, tx, &store.GiftRedemption{GiftCodeID: g.ID, UserID: u.ID, Seq: seq,
			RequestKey: key, Amount: g.Amount, Currency: g.Currency, LedgerEntryID: entry.ID, BalanceAfter: w.Balance}); err != nil {
			return err
		}
		if err := s.audit(ctx, tx, u, "gift_code.redeem", "gift_code", g.ID,
			map[string]any{"tail": g.Tail, "amount": g.Amount, "currency": g.Currency, "balance": w.Balance, "n": seq}, ""); err != nil {
			return err
		}
		out = &GiftRedeemed{Amount: g.Amount, Currency: g.Currency, Balance: w.Balance}
		// Last: it takes the outbox's lock, which is held only for the commit.
		return s.publish(ctx, tx, events.WalletCredited, events.WalletCreditedEvent{
			UserID: u.ID, TelegramID: u.TelegramID, Amount: g.Amount, Currency: g.Currency,
			Balance: w.Balance, Reason: events.CreditGiftCode,
		})
	})
	var ge *GiftCodeError
	if errors.As(err, &ge) && ge.Reason == GiftInvalid {
		return nil, s.giftWrong(ctx, u)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// giftReplay answers a request that was served before: the same redemption
// (the key names it) with the wallet's balance now. A key that redeemed
// another code is a caller's mistake, never an answer. In a transaction (tx)
// it reads through it: a second connection would wait for the one it holds.
func (s *Service) giftReplay(ctx context.Context, tx pgx.Tx, r *store.GiftRedemption, storedHash, hash []byte) (*GiftRedeemed, error) {
	if hash == nil || subtle.ConstantTimeCompare(storedHash, hash) != 1 {
		return nil, store.ErrIdempotencyConflict
	}
	var (
		w   *store.Wallet
		err error
	)
	if tx != nil {
		w, err = s.st.GetWallet(ctx, tx, r.UserID, r.Currency)
	} else {
		w, err = s.st.GetWallet(ctx, s.st.Conn(), r.UserID, r.Currency)
	}
	if err != nil {
		return nil, err
	}
	return &GiftRedeemed{Amount: r.Amount, Currency: r.Currency, Balance: w.Balance, Replayed: true}, nil
}

// giftWrong counts a wrong code against a customer and answers it: the one
// answer for every wrong code, or, for the code that makes it too many (and
// any that was let through at the same moment), that entering codes is closed
// for a while. The code that closes it is in the audit log.
func (s *Service) giftWrong(ctx context.Context, u *store.User) error {
	f, err := s.st.RecordGiftFailure(ctx, s.st.Conn(), u.ID, giftFailWindow, giftLockFor, giftFailLimit)
	if err != nil {
		return err // closed to the attempt: no free guess when the count cannot be kept
	}
	if f.Failures < giftFailLimit {
		return &GiftCodeError{Reason: GiftInvalid}
	}
	if f.Locked {
		if err := s.audit(ctx, nil, u, "gift_code.lock", "user", u.ID,
			map[string]any{"wrong_codes": f.Failures, "minutes": int(giftLockFor.Minutes())}, ""); err != nil {
			s.cfg.Log.Warn("gift code lock not audited", "user", u.ID, "err", err)
		}
	}
	return &GiftCodeError{Reason: GiftLocked, RetryAfter: giftLockFor}
}
