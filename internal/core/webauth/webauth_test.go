package webauth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$m=32768,t=3,p=2$") {
		t.Fatalf("hash: %q %v", h, err)
	}
	if !CheckPassword(h, "correct horse battery") {
		t.Fatal("the right password was refused")
	}
	if CheckPassword(h, "correct horse batterY") || CheckPassword(h, "") {
		t.Fatal("a wrong password was accepted")
	}
	other, _ := HashPassword("correct horse battery")
	if other == h {
		t.Fatal("two hashes of one password are equal: no salt")
	}
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$AA$AA", strings.Replace(h, "m=32768", "m=99999999", 1)} {
		if CheckPassword(bad, "correct horse battery") {
			t.Errorf("malformed hash accepted: %q", bad)
		}
	}
	if ValidPassword("short") == nil || ValidPassword("long enough pw") != nil {
		t.Fatal("password rules")
	}
}

// The RFC 6238 test vectors (SHA1, 20-byte key "12345678901234567890"),
// truncated to the 6 digits authenticator apps show.
func TestTOTPVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"} {
		got, err := TOTPCode(secret, TOTPStep(time.Unix(unix, 0)))
		if err != nil || got != want {
			t.Errorf("t=%d: %s %v, want %s", unix, got, err, want)
		}
	}
}

func TestCheckTOTP(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	cur := TOTPStep(now)
	code, _ := TOTPCode(secret, cur)
	step, ok := CheckTOTP(secret, code, now, 0)
	if !ok || step != cur {
		t.Fatalf("current code refused: %v %d", ok, step)
	}
	if _, ok := CheckTOTP(secret, code, now, cur); ok {
		t.Fatal("a code was accepted twice")
	}
	prev, _ := TOTPCode(secret, cur-1)
	if _, ok := CheckTOTP(secret, prev, now, 0); !ok {
		t.Fatal("the previous step (clock drift) was refused")
	}
	old, _ := TOTPCode(secret, cur-3)
	for _, bad := range []string{old, "12345", "abcdef", ""} {
		if _, ok := CheckTOTP(secret, bad, now, 0); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	u := TOTPURL("BOBRES Shop", "boss", secret)
	if !strings.HasPrefix(u, "otpauth://totp/BOBRES%20Shop:boss?") || !strings.Contains(u, "secret="+secret) {
		t.Fatalf("otpauth url: %s", u)
	}
}

func TestToken(t *testing.T) {
	tok, h, err := NewToken()
	if err != nil || len(tok) != 43 || len(h) != 32 || string(HashToken(tok)) != string(h) {
		t.Fatalf("token: %q %x %v", tok, h, err)
	}
	other, _, _ := NewToken()
	if other == tok {
		t.Fatal("tokens repeat")
	}
}
