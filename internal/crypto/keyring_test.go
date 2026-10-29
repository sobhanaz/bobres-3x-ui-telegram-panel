package crypto

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// Values made by the envelope as it was before key rotation (version 1),
// for the master key goldenMaster: they must still be read, and Tag must
// still answer what it did, or every stored token and every gift code hash
// would be lost by the update that brings rotation.
const (
	goldenMaster     = "0123456789abcdef0123456789abcdef"                                                                 //gitleaks:allow test fixture
	goldenCipherHex  = "01614cf6352a978faeba2c493bffa5a8952dbb4564c08112b40fc6d8db191ade8f6b8649ec14b3c9b2acbb0bf6f253f8" //gitleaks:allow test fixture
	goldenPlain      = "3x-ui-api-token-abc"                                                                              //gitleaks:allow test fixture
	goldenTagHex     = "4aaba698bbb7c0db5d389a88f8af7223c5b6e770b72e3c4062f2790c25d4246d"                                 //gitleaks:allow test fixture
	goldenTagKeyHex  = "c827c38d6cc42925f8b006751562358f1d53e779ec09be6630c7113623a17d51"                                 //gitleaks:allow test fixture
	masterB, masterC = "fedcba9876543210fedcba9876543210", "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"                             //gitleaks:allow test fixture
)

func keyring(t *testing.T, k Keys) *Envelope {
	t.Helper()
	e, err := NewKeyring(k)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return e
}

func TestVersion1ValuesAreStillRead(t *testing.T) {
	ct, _ := hex.DecodeString(goldenCipherHex)
	e := keyring(t, Keys{Master: goldenMaster})
	pt, err := e.Decrypt(ct)
	if err != nil || string(pt) != goldenPlain {
		t.Fatalf("a version 1 value: %q, %v", pt, err)
	}
	if got := hex.EncodeToString(e.Tag([]byte("sub-1"))); got != goldenTagHex {
		t.Fatalf("Tag changed: %s", got)
	}
	if e.Check(ct) != Current {
		t.Fatal("a version 1 value under the current key is not Current")
	}
	if got, err := e.Reseal(ct); err != nil || !bytes.Equal(got, ct) {
		t.Fatal("a value the current key reads must come back unchanged")
	}
	// the same value is read through the previous key of a newer master key
	next := keyring(t, Keys{Master: masterB, Previous: []string{goldenMaster}})
	if pt, err := next.Decrypt(ct); err != nil || string(pt) != goldenPlain {
		t.Fatalf("a version 1 value through a previous key: %q, %v", pt, err)
	}
	if next.Check(ct) != Previous {
		t.Fatal("a version 1 value under an older key is not Previous")
	}
}

func TestNewValuesAreVersion2WithTheKeyID(t *testing.T) {
	e := keyring(t, Keys{Master: masterB})
	ct, err := e.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if ct[0] != 2 || hex.EncodeToString(ct[1:5]) != e.KeyID() {
		t.Fatalf("header %x, key id %s", ct[:5], e.KeyID())
	}
	other := keyring(t, Keys{Master: masterC})
	if other.KeyID() == e.KeyID() {
		t.Fatal("two keys with one id")
	}
	if strings.Contains(e.KeyID(), masterB[:4]) {
		t.Fatal("the key id looks derived from the key's text")
	}
}

func TestRotationReadsBothKeysAndWritesTheNewOne(t *testing.T) {
	old := keyring(t, Keys{Master: masterB})
	oldCT, _ := old.Encrypt([]byte("sealed by the old key"))
	v1CT, _ := hex.DecodeString(goldenCipherHex)

	during := keyring(t, Keys{Master: masterC, Previous: []string{masterB, goldenMaster}})
	for name, ct := range map[string][]byte{"v2 old": oldCT, "v1 older": v1CT} {
		if during.Check(ct) != Previous {
			t.Errorf("%s: want Previous", name)
		}
		if _, err := during.Decrypt(ct); err != nil {
			t.Errorf("%s: not read through the previous key: %v", name, err)
		}
	}
	// what is written now is sealed with the new key, and the old key alone cannot read it
	fresh, _ := during.Encrypt([]byte("new"))
	if during.Check(fresh) != Current {
		t.Fatal("a new value is not Current")
	}
	if _, err := old.Decrypt(fresh); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("the old key read a value sealed with the new one: %v", err)
	}

	for _, ct := range [][]byte{oldCT, v1CT} {
		sealed, err := during.Reseal(ct)
		if err != nil {
			t.Fatal(err)
		}
		if during.Check(sealed) != Current || during.CurrentOnly().Check(sealed) != Current {
			t.Fatal("a resealed value needs more than the current key")
		}
		a, _ := during.Decrypt(ct)
		b, _ := during.CurrentOnly().Decrypt(sealed)
		if !bytes.Equal(a, b) {
			t.Fatal("resealing changed the plaintext")
		}
	}
	// once the previous keys are gone, the value left behind says so instead of misreading
	after := keyring(t, Keys{Master: masterC})
	if after.Check(oldCT) != Unreadable || after.Check(v1CT) != Unreadable {
		t.Fatal("values of removed keys must be Unreadable")
	}
	if _, err := after.Reseal(oldCT); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("Reseal of a value no key reads: %v", err)
	}
}

func TestAKeyIDCannotBeForgedOrMoved(t *testing.T) {
	a := keyring(t, Keys{Master: masterB})
	b := keyring(t, Keys{Master: masterC})
	both := keyring(t, Keys{Master: masterC, Previous: []string{masterB}})
	ct, _ := a.Encrypt([]byte("x"))
	// the id names key B's rival: the value is not read, though both keys are known
	forged := bytes.Clone(ct)
	copy(forged[1:5], mustID(b))
	if _, err := both.Decrypt(forged); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("a value with another key's id was read: %v", err)
	}
	// an unknown id, a changed version, a cut value
	unknown := bytes.Clone(ct)
	copy(unknown[1:5], []byte{9, 9, 9, 9})
	if both.Check(unknown) != Unreadable {
		t.Fatal("an unknown key id was read")
	}
	asV1 := bytes.Clone(ct)
	asV1[0] = 1
	if both.Check(asV1) != Unreadable {
		t.Fatal("a version 2 value read as version 1")
	}
	for _, cut := range []int{0, 1, 5, 17, len(ct) - 1} {
		if both.Check(ct[:cut]) != Unreadable {
			t.Fatalf("a value cut to %d bytes was read", cut)
		}
	}
	if both.Check(nil) != Unreadable || both.Check([]byte{}) != Unreadable {
		t.Fatal("nothing is not a value")
	}
}

func mustID(e *Envelope) []byte {
	id, _ := hex.DecodeString(e.KeyID())
	return id
}

func TestTagStaysTheSameThroughARotation(t *testing.T) {
	old := keyring(t, Keys{Master: goldenMaster})
	want := old.Tag([]byte("sub-1"))
	// without a tag key the new master key changes every tag: why the rotation keeps one
	if bytes.Equal(keyring(t, Keys{Master: masterB}).Tag([]byte("sub-1")), want) {
		t.Fatal("a tag cannot be the same under two master keys")
	}
	key, err := TagKeyHex([]byte(goldenMaster))
	if err != nil || key != goldenTagKeyHex {
		t.Fatalf("TagKeyHex = %q, %v", key, err)
	}
	next := keyring(t, Keys{Master: masterB, Previous: []string{goldenMaster}, Tag: key})
	if !bytes.Equal(next.Tag([]byte("sub-1")), want) {
		t.Fatal("the tag changed with the master key although a tag key was kept")
	}
	if !bytes.Equal(next.CurrentOnly().Tag([]byte("sub-1")), want) {
		t.Fatal("CurrentOnly lost the tag key")
	}
	for _, bad := range []string{"zz", strings.Repeat("a", 63), strings.Repeat("a", 66)} {
		if _, err := NewKeyring(Keys{Master: masterB, Tag: bad}); err == nil {
			t.Errorf("tag key %q accepted", bad)
		}
	}
}

func TestKeyringRejectsWeakKeysAndIgnoresADuplicate(t *testing.T) {
	if _, err := NewKeyring(Keys{Master: masterB, Previous: []string{"short"}}); err == nil {
		t.Fatal("a short previous key was accepted")
	}
	if _, err := TagKeyHex([]byte("short")); err == nil {
		t.Fatal("TagKeyHex took a short key")
	}
	e := keyring(t, Keys{Master: masterB, Previous: []string{masterB, ""}})
	if e.HasPrevious() {
		t.Fatal("the current key counted as a previous one")
	}
	if !keyring(t, Keys{Master: masterB, Previous: []string{masterC}}).HasPrevious() {
		t.Fatal("a previous key was dropped")
	}
}
