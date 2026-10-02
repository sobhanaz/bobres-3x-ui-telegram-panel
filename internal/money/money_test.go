package money

import "testing"

func TestScale(t *testing.T) {
	if got := Scale("IRT"); got != 0 {
		t.Errorf("IRT scale = %d, want 0", got)
	}
	if got := Scale("USDT"); got != 6 {
		t.Errorf("USDT scale = %d, want 6", got)
	}
	if _, err := New(100, "DOGE"); err == nil {
		t.Error("unknown currency accepted")
	}
}

func TestValidate(t *testing.T) {
	a, err := New(1500, "IRT")
	if err != nil {
		t.Fatal(err)
	}
	if a.MinorUnits() != 1500 || a.Currency() != "IRT" {
		t.Errorf("got %d %q", a.MinorUnits(), a.Currency())
	}
	if _, err := New(-1, "IRT"); err == nil {
		t.Error("negative amount accepted")
	}
}

func TestFormat(t *testing.T) {
	irt, _ := New(42000, "IRT")
	if got := Format(irt); got != "42000 IRT" {
		t.Errorf("IRT format = %q", got)
	}
	usdt, _ := New(12345678, "USDT")
	if got := Format(usdt); got != "12.345678 USDT" {
		t.Errorf("USDT format = %q", got)
	}
}
