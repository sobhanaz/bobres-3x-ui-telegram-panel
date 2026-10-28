package loginwire

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
)

var (
	idRe       = regexp.MustCompile(`^[0-9a-f]{32}$`)
	nonceRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{16}$`)
	usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{0,32}$`)
)

var (
	methods  = []string{"link", "password", "password_initial"}
	roles    = []string{"owner", "admin", "support"}
	results  = []string{"approved", "denied", "expired", "cancelled"}
	kinds    = []string{"enabled", "disabled"}
	sources  = []string{"dashboard", "server"}
	approver = []string{"self", "owner"}
)

// ID reports whether s is a sign-in's id as the buttons carry it.
func ID(s string) bool { return idRe.MatchString(s) }

// Nonce reports whether s is an approver's nonce as core makes it.
func Nonce(s string) bool { return nonceRe.MatchString(s) }

func (a Account) valid() error {
	if !usernameRe.MatchString(a.Username) || a.TelegramID <= 0 || !slices.Contains(roles, a.Role) {
		return errors.New("an account is not valid")
	}
	return nil
}

func validTargets(ts []Target, need func(Target) error) error {
	if len(ts) == 0 || len(ts) > MaxTargets {
		return fmt.Errorf("between 1 and %d targets are needed", MaxTargets)
	}
	seen := map[int64]bool{}
	for _, t := range ts {
		if t.TelegramID <= 0 || seen[t.TelegramID] {
			return errors.New("a target has no Telegram id, or has it twice")
		}
		seen[t.TelegramID] = true
		if len(t.Language) > 8 {
			return errors.New("a target's language is not valid")
		}
		if err := need(t); err != nil {
			return err
		}
	}
	return nil
}

// Valid checks a Request: every field is of the shape core makes it, and nothing else is accepted.
func (r Request) Valid() error {
	switch {
	case !ID(r.ApprovalID):
		return errors.New("the approval id is not valid")
	case !slices.Contains(methods, r.Method):
		return errors.New("the method is not valid")
	case r.IP != "" && net.ParseIP(r.IP) == nil:
		return errors.New("the address is not valid")
	case r.At <= 0 || r.ExpiresAt <= r.At:
		return errors.New("the times are not valid")
	}
	if err := r.Account.valid(); err != nil {
		return err
	}
	return validTargets(r.Targets, func(t Target) error {
		if !Nonce(t.Nonce) || !slices.Contains(approver, t.Role) {
			return errors.New("a target has no valid nonce or role")
		}
		return nil
	})
}

// Valid checks a Resolution.
func (r Resolution) Valid() error {
	switch {
	case !ID(r.ApprovalID):
		return errors.New("the approval id is not valid")
	case !slices.Contains(results, r.Result):
		return errors.New("the result is not valid")
	case r.At <= 0:
		return errors.New("the time is not valid")
	}
	if err := r.Account.valid(); err != nil {
		return err
	}
	if r.By != nil {
		if err := r.By.valid(); err != nil {
			return err
		}
	}
	return validTargets(r.Targets, func(t Target) error {
		if t.MessageID <= 0 {
			return errors.New("a target has no message")
		}
		return nil
	})
}

// Valid checks a Notice.
func (n Notice) Valid() error {
	if !slices.Contains(kinds, n.Kind) || !slices.Contains(sources, n.Source) {
		return errors.New("the kind or the source is not valid")
	}
	if n.By != nil {
		if err := n.By.valid(); err != nil {
			return err
		}
	}
	return validTargets(n.Targets, func(Target) error { return nil })
}
