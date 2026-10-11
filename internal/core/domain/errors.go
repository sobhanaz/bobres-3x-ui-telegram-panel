package domain

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalid marks input that can never succeed as sent (maps to
	// InvalidArgument). Wrap it with details: fmt.Errorf("%w: ...", ErrInvalid).
	ErrInvalid = errors.New("domain: invalid argument")
	// ErrForbidden: the caller may not act on this resource.
	ErrForbidden = errors.New("domain: forbidden")
	// ErrTrialAlreadyUsed blocks a second free trial for the same user.
	ErrTrialAlreadyUsed = errors.New("domain: trial already used")
	// ErrOrderNotPayable is returned when an order is not in a payable state.
	ErrOrderNotPayable = errors.New("domain: order not payable")
	// ErrPlanUnavailable: the plan is disabled, or must be used another way.
	ErrPlanUnavailable = errors.New("domain: plan unavailable")
	// ErrUserBanned blocks purchases by banned users.
	ErrUserBanned = errors.New("domain: user is banned")
	// ErrState: the service or order is not in a state that allows this now
	// (maps to FailedPrecondition / 409). Wrap it with the reason.
	ErrState = errors.New("domain: not possible in the current state")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func stateErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrState, fmt.Sprintf(format, a...))
}

// Order statuses by meaning.
var (
	payableStatuses = []string{"created", "awaiting_payment"}
	// paidStatuses hold orders whose money has been received.
	paidStatuses = []string{"paid", "provisioning", "active", "provision_failed"}
)

func isPayable(status string) bool { return contains(payableStatuses, status) }
func isPaid(status string) bool    { return contains(paidStatuses, status) }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func strptr(s string) *string { return &s }
