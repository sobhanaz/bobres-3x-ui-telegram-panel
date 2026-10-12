package backup

import (
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

var (
	testKey  = []byte("0123456789abcdef0123456789abcdef-backup")
	testKeys = map[string]string{
		"BOBRES_CORE_MASTER_KEY":        "core-secret",
		"BOBRES_PROVISIONER_MASTER_KEY": "prov-secret",
		"BOBRES_PAYMENTS_MASTER_KEY":    "pay-secret",
	}
)

const fullChunk = chunkSize + tagLen

func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := crand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func seal(t *testing.T, dump []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, testKey, testKeys, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// readAll opens a backup and reads the whole dump.
func readAll(file []byte, key []byte) (*Reader, []byte, error) {
	r, err := Open(bytes.NewReader(file), key)
	if err != nil {
		return nil, nil, err
	}
	dump, err := io.ReadAll(r.Dump())
	return r, dump, err
}

func TestRoundTrip(t *testing.T) {
	keysJSON, _ := json.Marshal(testKeys)
	sizes := map[string]int{
		"empty":       0,
		"small":       10,
		"multi-chunk": 3<<20 + 12345,
		// The plaintext fills exactly two chunks, so the last chunk is full.
		"chunk-aligned": 2*chunkSize - 4 - len(keysJSON),
	}
	for name, size := range sizes {
		t.Run(name, func(t *testing.T) {
			dump := randBytes(t, size)
			before := time.Now().Add(-time.Second)
			r, got, err := readAll(seal(t, dump), testKey)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, dump) {
				t.Fatalf("dump differs: got %d bytes, want %d", len(got), len(dump))
			}
			if !reflect.DeepEqual(r.Keys(), testKeys) {
				t.Fatalf("keys = %v", r.Keys())
			}
			if c := r.Created(); c.Before(before.Truncate(time.Second)) || c.After(time.Now()) {
				t.Fatalf("created = %v", c)
			}
		})
	}
}

func TestVerify(t *testing.T) {
	file := seal(t, randBytes(t, 200_000))
	s, err := Verify(bytes.NewReader(file), testKey)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(s.KeyNames)
	want := []string{"BOBRES_CORE_MASTER_KEY", "BOBRES_PAYMENTS_MASTER_KEY", "BOBRES_PROVISIONER_MASTER_KEY"}
	if s.DumpSize != 200_000 || !reflect.DeepEqual(s.KeyNames, want) {
		t.Fatalf("summary = %+v", s)
	}
	file[len(file)-1] ^= 1
	if _, err := Verify(bytes.NewReader(file), testKey); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Verify on damaged file: %v", err)
	}
}

func TestShortKeyRejected(t *testing.T) {
	if err := Write(io.Discard, []byte("short"), nil, bytes.NewReader(nil)); err == nil {
		t.Fatal("Write accepted a short key")
	}
	if _, err := Open(bytes.NewReader(nil), []byte("short")); err == nil {
		t.Fatal("Open accepted a short key")
	}
}

func TestWrongKey(t *testing.T) {
	file := seal(t, []byte("dump"))
	other := bytes.Repeat([]byte("k"), 40)
	if _, err := Open(bytes.NewReader(file), other); !errors.Is(err, ErrKey) {
		t.Fatalf("err = %v, want ErrKey", err)
	}
}

// multiChunk is a dump that spans four chunks, the last one partial.
func multiChunk(t *testing.T) []byte {
	return seal(t, randBytes(t, 3*chunkSize+1000))
}

func TestTruncated(t *testing.T) {
	file := multiChunk(t)
	lastStart := headerLen + 3*fullChunk
	cases := map[string]struct {
		file []byte
		want error
	}{
		"drop last chunk":      {file[:lastStart], ErrCorrupt},
		"drop two chunks":      {file[:lastStart-fullChunk], ErrCorrupt},
		"cut mid last chunk":   {file[:len(file)-100], ErrCorrupt},
		"cut mid middle chunk": {file[:headerLen+fullChunk+500], ErrCorrupt},
		"cut inside the tag":   {file[:len(file)-3], ErrCorrupt},
		"header only":          {file[:headerLen], ErrCorrupt},
		"cut inside header":    {file[:headerLen-1], ErrFormat},
		"empty":                {nil, ErrFormat},
		"trailing garbage":     {append(append([]byte{}, file...), 0), ErrCorrupt},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := readAll(tc.file, testKey); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReorderedChunks(t *testing.T) {
	file := multiChunk(t)
	c1 := headerLen + fullChunk
	c2 := c1 + fullChunk
	swapped := append([]byte{}, file[:c1]...)
	swapped = append(swapped, file[c2:c2+fullChunk]...)
	swapped = append(swapped, file[c1:c2]...)
	swapped = append(swapped, file[c2+fullChunk:]...)
	if _, _, err := readAll(swapped, testKey); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}
}

func TestChunkFromAnotherFile(t *testing.T) {
	a, b := multiChunk(t), multiChunk(t)
	mixed := append([]byte{}, a...)
	copy(mixed[headerLen+fullChunk:], b[headerLen+fullChunk:headerLen+2*fullChunk])
	if _, _, err := readAll(mixed, testKey); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}
}

func TestFlippedByte(t *testing.T) {
	file := multiChunk(t)
	saltAt := len(magic) + 1
	cases := map[string]struct {
		at   int
		want error
	}{
		"magic":          {0, ErrFormat},
		"version":        {len(magic), ErrVersion},
		"salt":           {saltAt, ErrKey},
		"nonce prefix":   {saltAt + saltLen, ErrKey},
		"created":        {aadLen - 1, ErrKey},
		"wrap nonce":     {aadLen, ErrKey},
		"wrapped key":    {aadLen + nonceLen + 3, ErrKey},
		"wrap tag":       {headerLen - 1, ErrKey},
		"first chunk":    {headerLen + 2, ErrCorrupt},
		"middle chunk":   {headerLen + fullChunk + 1000, ErrCorrupt},
		"middle tag":     {headerLen + 2*fullChunk - 1, ErrCorrupt},
		"last chunk":     {headerLen + 3*fullChunk + 10, ErrCorrupt},
		"last chunk tag": {len(file) - 1, ErrCorrupt},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			bad := append([]byte{}, file...)
			bad[tc.at] ^= 0x40
			if _, _, err := readAll(bad, testKey); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

var errReadTooFar = errors.New("read past the keys member")

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errReadTooFar }

func TestKeysWithoutReadingDump(t *testing.T) {
	file := seal(t, randBytes(t, 4<<20))
	// Open may read the first chunk and peek into the next, never further.
	limit := headerLen + 2*fullChunk
	src := io.MultiReader(bytes.NewReader(file[:limit]), failReader{})
	r, err := Open(src, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Keys(), testKeys) {
		t.Fatalf("keys = %v", r.Keys())
	}
	// Reading the dump does go further, and the error comes through.
	if _, err := io.Copy(io.Discard, r.Dump()); !errors.Is(err, errReadTooFar) {
		t.Fatalf("dump err = %v", err)
	}
}

func TestPrune(t *testing.T) {
	var names []string
	base := time.Date(2026, 10, 1, 3, 30, 0, 0, time.UTC)
	stamp := func(day int) string { return FileName(base.AddDate(0, 0, day)) }
	for day := range 10 {
		names = append(names, stamp(day))
		if day != 1 { // day 1 lost its sidecar
			names = append(names, SidecarName(stamp(day)))
		}
	}
	// Failed runs leave a sidecar and no backup.
	for day := 20; day < 29; day++ {
		names = append(names, SidecarName(stamp(day)))
	}
	names = append(names,
		"notes.txt", "bobres-latest.bkp", stamp(0)+".tmp", ".sidecar-123.tmp",
		"bobres-20261399T999999Z.bkp", "BOBRES-20261001T033000Z.bkp", "x-"+stamp(0),
	)

	got := Prune(names, Keep)
	sort.Strings(got)
	want := []string{
		stamp(0), SidecarName(stamp(0)),
		stamp(1),
		stamp(2), SidecarName(stamp(2)),
		SidecarName(stamp(20)), SidecarName(stamp(21)),
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Prune =\n%v\nwant\n%v", got, want)
	}

	if got := Prune(names[:6], Keep); len(got) != 0 {
		t.Fatalf("under the limit, Prune = %v", got)
	}
}

func TestFileNames(t *testing.T) {
	ts := time.Date(2026, 10, 11, 3, 30, 5, 0, time.FixedZone("IRST", 12600))
	if got := FileName(ts); got != "bobres-20261011T000005Z.bkp" {
		t.Fatalf("FileName = %s", got)
	}
	if got := SidecarName("bobres-20261011T000005Z.bkp"); got != "bobres-20261011T000005Z.json" {
		t.Fatalf("SidecarName = %s", got)
	}
}

func TestSidecarRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bobres-20261011T033000Z.json")
	sent := time.Date(2026, 10, 11, 3, 31, 0, 0, time.UTC)
	s := Sidecar{
		File: "bobres-20261011T033000Z.bkp", Size: 1234, SHA256: "ab12",
		CreatedAt: time.Date(2026, 10, 11, 3, 30, 0, 0, time.UTC), SentAt: &sent,
		TelegramError: "too large",
	}
	if err := WriteSidecar(path, s); err != nil {
		t.Fatal(err)
	}
	s.Error = "later failure" // overwrite in place
	if err := WriteSidecar(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSidecar(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, s) {
		t.Fatalf("got %+v, want %+v", got, s)
	}
	entries, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	if _, err := ReadSidecar(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("ReadSidecar on a missing file succeeded")
	}
}
