// Package money represents exact monetary amounts as int64 minor units plus a
// currency code. IRT (Iranian Toman) has scale 0, USDT has scale 6. Floating
// point is never used.
package money

import "fmt"

// scales maps supported currency codes to decimal scale.
var scales = map[string]int{
	"IRT":  0,
	"USDT": 6,
}

// Amount is a validated money value.
type Amount struct {
	minorUnits int64
	currency   string
}

// Scale returns the decimal scale of a supported currency, or -1 if unknown.
func Scale(currency string) int {
	if s, ok := scales[currency]; ok {
		return s
	}
	return -1
}

// New validates minorUnits (must be >= 0) and currency.
func New(minorUnits int64, currency string) (Amount, error) {
	if Scale(currency) < 0 {
		return Amount{}, fmt.Errorf("money: unsupported currency %q", currency)
	}
	if minorUnits < 0 {
		return Amount{}, fmt.Errorf("money: negative amount %d", minorUnits)
	}
	return Amount{minorUnits: minorUnits, currency: currency}, nil
}

// MinorUnits returns the raw int64 value.
func (a Amount) MinorUnits() int64 { return a.minorUnits }

// Currency returns the ISO-like currency code.
func (a Amount) Currency() string { return a.currency }

// Format renders the amount in major units with the currency code.
func Format(a Amount) string {
	scale := Scale(a.currency)
	if scale == 0 {
		return fmt.Sprintf("%d %s", a.minorUnits, a.currency)
	}
	div := int64(1)
	for i := 0; i < scale; i++ {
		div *= 10
	}
	whole := a.minorUnits / div
	frac := a.minorUnits % div
	return fmt.Sprintf("%d.%0*d %s", whole, scale, frac, a.currency)
}
