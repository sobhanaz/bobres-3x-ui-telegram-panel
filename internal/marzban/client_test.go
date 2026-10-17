package marzban

import "testing"

func TestParseCredentials(t *testing.T) {
	u, p, err := ParseCredentials("admin:pa:ss")
	if err != nil || u != "admin" || p != "pa:ss" {
		t.Fatalf("got %q %q %v", u, p, err)
	}
	for _, bad := range []string{"", "x", ":p", "u:"} {
		if _, _, err := ParseCredentials(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestValidateUsername(t *testing.T) {
	for _, ok := range []string{"u1-abcd1234", "a_b", "x@y.z", "abc"} {
		if ValidateUsername(ok) != nil {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "ab", "has space", "a+b", "../x", "u/1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if ValidateUsername(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
