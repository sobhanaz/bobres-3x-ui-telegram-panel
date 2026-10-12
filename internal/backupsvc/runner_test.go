package backupsvc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

var (
	testKey  = []byte("0123456789abcdef0123456789abcdef-test")
	testKeys = map[string]string{"BOBRES_CORE_MASTER_KEY": "core-master-key-for-tests-0123456789"}
)

const (
	coreToken = "core-token-0123456789abcdef0123456789" //gitleaks:allow test fixture
	botToken  = "backup-token-0123456789abcdef01234567" //gitleaks:allow test fixture
)

// clock advances a minute on every reading, so each run gets its own name.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Minute)
	return c.t
}

func newRunner(t *testing.T, mod func(*Options)) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 11, 3, 30, 0, 0, time.UTC)}
	o := Options{
		Dir: dir, Key: testKey, Keys: testKeys,
		Dump:      DumpFunc(func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, "PGDMP fake"); return err }),
		Now:       c.now,
		FreeSpace: func(string) (uint64, error) { return 1 << 40, nil },
		Hour:      3, Minute: 30,
	}
	if mod != nil {
		mod(&o)
	}
	r, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return r, dir
}

func files(t *testing.T, dir, suffix string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*"+suffix))
	if err != nil {
		t.Fatal(err)
	}
	for i := range m {
		m[i] = filepath.Base(m[i])
	}
	return m
}

// fakePgDump writes a shell script standing in for pg_dump: it records its
// arguments and whether it got a password, then runs body.
func fakePgDump(t *testing.T, body string) (path, record string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	d := t.TempDir()
	record = filepath.Join(d, "record")
	path = filepath.Join(d, "pg_dump")
	script := "#!/bin/sh\necho \"$@\" > " + record + "\necho \"pw=$PGPASSWORD key=$BOBRES_BACKUP_KEY\" >> " + record + "\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // an executable test script
		t.Fatal(err)
	}
	return path, record
}

func TestBackupWithPgDumpMakesAReadableFile(t *testing.T) {
	t.Setenv("BOBRES_BACKUP_KEY", "must-not-reach-pg-dump")
	dump := strings.Repeat("PGDMP-data-", 20000) // several chunks
	pg, record := fakePgDump(t, "printf '%s' '"+dump+"'")
	r, dir := newRunner(t, func(o *Options) {
		o.Dump = PgDump{Path: pg, URL: "postgres://svc_backup:s3cret-pw@postgres:5432/bobres?sslmode=disable"} //nolint:gosec // test fixture
	})
	sc, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := files(t, dir, ".bkp"); len(got) != 1 || got[0] != sc.File {
		t.Fatalf("files %v, sidecar %+v", got, sc)
	}
	raw, err := os.ReadFile(filepath.Join(dir, sc.File))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if sc.Size != int64(len(raw)) || sc.SHA256 != hex.EncodeToString(sum[:]) || sc.Error != "" || sc.SentAt != nil {
		t.Fatalf("sidecar %+v", sc)
	}
	disk, err := backup.ReadSidecar(filepath.Join(dir, backup.SidecarName(sc.File)))
	if err != nil || disk.SHA256 != sc.SHA256 || disk.Size != sc.Size {
		t.Fatalf("sidecar on disk %+v %v", disk, err)
	}
	br, err := backup.Open(bytes.NewReader(raw), testKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(br.Dump())
	if err != nil || string(got) != dump || br.Keys()["BOBRES_CORE_MASTER_KEY"] != testKeys["BOBRES_CORE_MASTER_KEY"] {
		t.Fatalf("read back: %d bytes, keys %v, %v", len(got), br.Keys(), err)
	}
	if _, err := backup.Verify(bytes.NewReader(raw), testKey); err != nil {
		t.Fatal(err)
	}
	rec, _ := os.ReadFile(record)
	if strings.Contains(strings.SplitN(string(rec), "\n", 2)[0], "s3cret-pw") {
		t.Fatalf("password on the pg_dump command line: %s", rec)
	}
	for _, want := range []string{"--format=custom", "--no-password", "--dbname=postgres://svc_backup@postgres:5432/bobres?sslmode=disable", "pw=s3cret-pw key=\n"} {
		if !strings.Contains(string(rec), want) {
			t.Errorf("pg_dump record lacks %q:\n%s", want, rec)
		}
	}
	if tmp := files(t, dir, ".tmp"); len(tmp) != 0 {
		t.Fatalf("temp files left: %v", tmp)
	}
}

func TestFailedDumpLeavesOnlyASidecar(t *testing.T) {
	pg, _ := fakePgDump(t, "printf 'partial'\necho 'pg_dump: error: connection refused' >&2\nexit 1")
	r, dir := newRunner(t, func(o *Options) { o.Dump = PgDump{Path: pg, URL: "postgres://u@h/db"} })
	sc, err := r.RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err %v", err)
	}
	if b := files(t, dir, ".bkp"); len(b) != 0 {
		t.Fatalf("backup file left after a failed dump: %v", b)
	}
	if tmp := files(t, dir, ".tmp"); len(tmp) != 0 {
		t.Fatalf("temp files left: %v", tmp)
	}
	disk, err := backup.ReadSidecar(filepath.Join(dir, backup.SidecarName(sc.File)))
	if err != nil || !strings.Contains(disk.Error, "connection refused") || disk.Size != 0 {
		t.Fatalf("sidecar %+v %v", disk, err)
	}
	if st := r.Status(); st.LastError != disk.Error || st.Running || len(st.Backups) != 1 {
		t.Fatalf("status %+v", st)
	}
}

func TestDiskCheckRefuses(t *testing.T) {
	free := uint64(50 << 20)
	r, dir := newRunner(t, func(o *Options) {
		o.FreeSpace = func(string) (uint64, error) { return free, nil }
	})
	if _, err := r.RunOnce(context.Background()); err == nil || !strings.Contains(err.Error(), "not enough free disk space") {
		t.Fatalf("first backup with 50 MiB free: %v", err)
	}
	if b := files(t, dir, ".bkp"); len(b) != 0 {
		t.Fatalf("refused run wrote %v", b)
	}
	free = 200 << 20
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The last backup is 2 MiB: 3 MiB free is less than twice that.
	big := filepath.Join(dir, backup.FileName(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)))
	if err := os.WriteFile(big, make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	free = 3 << 20
	if _, err := r.RunOnce(context.Background()); err == nil || !strings.Contains(err.Error(), "4.0 MiB needed") {
		t.Fatalf("expected a refusal: %v", err)
	}
	free = 5 << 20
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryToTheBot(t *testing.T) {
	var gotBody []byte
	var gotName, gotAuth string
	var gotLen int64
	bot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/backups/deliver" {
			http.NotFound(w, r)
			return
		}
		gotAuth, gotName, gotLen = r.Header.Get("Authorization"), r.Header.Get("X-Backup-Name"), r.ContentLength
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"sent":true,"message_id":77}`)
	}))
	defer bot.Close()
	r, dir := newRunner(t, func(o *Options) { o.BotURL, o.Token = bot.URL+"/", botToken })
	sc, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, sc.File))
	if gotAuth != "Bearer "+botToken || gotName != sc.File || gotLen != int64(len(raw)) || !bytes.Equal(gotBody, raw) {
		t.Fatalf("bot got auth=%q name=%q len=%d (%d bytes)", gotAuth, gotName, gotLen, len(gotBody))
	}
	disk, _ := backup.ReadSidecar(filepath.Join(dir, backup.SidecarName(sc.File)))
	if disk.SentAt == nil || disk.TelegramError != "" {
		t.Fatalf("sidecar %+v", disk)
	}

	// Not sent: the bot's reason is recorded and the file is kept.
	tooBig := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"sent":false,"reason":"too_big"}`)
	}))
	defer tooBig.Close()
	r2, dir2 := newRunner(t, func(o *Options) { o.BotURL, o.Token = tooBig.URL, botToken })
	if sc, err = r2.RunOnce(context.Background()); err != nil || sc.TelegramError != "too_big" || sc.SentAt != nil {
		t.Fatalf("too big: %+v %v", sc, err)
	}
	if b := files(t, dir2, ".bkp"); len(b) != 1 {
		t.Fatalf("files %v", b)
	}

	// Bot down.
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()
	r3, dir3 := newRunner(t, func(o *Options) { o.BotURL, o.Token = downURL, botToken })
	if sc, err = r3.RunOnce(context.Background()); err != nil || !strings.Contains(sc.TelegramError, "unreachable") || sc.SentAt != nil {
		t.Fatalf("bot down: %+v %v", sc, err)
	}
	disk, _ = backup.ReadSidecar(filepath.Join(dir3, backup.SidecarName(sc.File)))
	if disk.TelegramError != sc.TelegramError || len(files(t, dir3, ".bkp")) != 1 {
		t.Fatalf("bot down: sidecar %+v files %v", disk, files(t, dir3, ".bkp"))
	}

	// A refusal that is not JSON.
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer deny.Close()
	r4, _ := newRunner(t, func(o *Options) { o.BotURL, o.Token = deny.URL, botToken })
	if sc, err = r4.RunOnce(context.Background()); err != nil || !strings.Contains(sc.TelegramError, "HTTP 401") {
		t.Fatalf("refused: %+v %v", sc, err)
	}
}

func TestPruneKeepsSeven(t *testing.T) {
	r, dir := newRunner(t, nil)
	for range 9 {
		if _, err := r.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if b, j := files(t, dir, ".bkp"), files(t, dir, ".json"); len(b) != backup.Keep || len(j) != backup.Keep {
		t.Fatalf("kept %d backups and %d sidecars: %v %v", len(b), len(j), b, j)
	}
	st := r.Status()
	if len(st.Backups) != backup.Keep || !st.Backups[0].CreatedAt.After(st.Backups[1].CreatedAt) {
		t.Fatalf("status not newest first: %+v", st.Backups)
	}
}

func TestOneBackupAtATime(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	r, _ := newRunner(t, func(o *Options) {
		o.Dump = DumpFunc(func(_ context.Context, w io.Writer) error {
			close(started)
			<-release
			_, err := io.WriteString(w, "PGDMP")
			return err
		})
	})
	done := make(chan error, 1)
	go func() { _, err := r.RunOnce(context.Background()); done <- err }()
	<-started
	if _, err := r.RunOnce(context.Background()); !errors.Is(err, ErrRunning) {
		t.Fatalf("second run: %v", err)
	}
	if r.Trigger() || !r.Status().Running {
		t.Fatal("trigger accepted while running")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if r.Status().Running {
		t.Fatal("still running")
	}
}

func TestNextRun(t *testing.T) {
	tehran, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		t.Skip(err)
	}
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip(err)
	}
	for _, c := range []struct {
		now          time.Time
		hour, minute int
		loc          *time.Location
		want         time.Time
	}{
		// Before the time today, after it, exactly at it.
		{time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC), 3, 30, time.UTC, time.Date(2026, 10, 11, 3, 30, 0, 0, time.UTC)},
		{time.Date(2026, 10, 11, 4, 0, 0, 0, time.UTC), 3, 30, time.UTC, time.Date(2026, 10, 12, 3, 30, 0, 0, time.UTC)},
		{time.Date(2026, 10, 11, 3, 30, 0, 0, time.UTC), 3, 30, time.UTC, time.Date(2026, 10, 12, 3, 30, 0, 0, time.UTC)},
		// Across midnight: 23:50 asks for 00:10, the next calendar day.
		{time.Date(2026, 10, 11, 23, 50, 0, 0, time.UTC), 0, 10, time.UTC, time.Date(2026, 10, 12, 0, 10, 0, 0, time.UTC)},
		// 23:00 UTC is already 02:30 the next day in Tehran (+03:30): 03:30 Tehran is an hour away.
		{time.Date(2026, 10, 11, 23, 0, 0, 0, time.UTC), 3, 30, tehran, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)},
		// 01:00 UTC is 04:30 in Tehran: tomorrow's 03:30 Tehran.
		{time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC), 3, 30, tehran, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)},
		// Daylight saving ends in New York on 2026-11-01: 03:30 stays 03:30 local.
		{time.Date(2026, 10, 31, 12, 0, 0, 0, ny), 3, 30, ny, time.Date(2026, 11, 1, 3, 30, 0, 0, ny)},
		{time.Date(2026, 11, 1, 4, 0, 0, 0, ny), 3, 30, ny, time.Date(2026, 11, 2, 3, 30, 0, 0, ny)},
	} {
		got := NextRun(c.now, c.hour, c.minute, c.loc)
		if !got.Equal(c.want) {
			t.Errorf("NextRun(%v, %02d:%02d %s) = %v, want %v", c.now, c.hour, c.minute, c.loc, got, c.want)
		}
	}
}

func TestLoopRunsOnScheduleAndOnTrigger(t *testing.T) {
	// The clock starts a second before 03:30 and runs at real speed.
	base, start := time.Date(2026, 10, 11, 3, 29, 59, 0, time.UTC), time.Now()
	runs := make(chan struct{}, 10)
	r, _ := newRunner(t, func(o *Options) {
		o.Now = func() time.Time { return base.Add(time.Since(start)) }
		o.Dump = DumpFunc(func(_ context.Context, w io.Writer) error {
			runs <- struct{}{}
			_, err := io.WriteString(w, "PGDMP")
			return err
		})
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Loop(ctx) }()
	select { // 03:30:00 is a second away
	case <-runs:
	case <-time.After(5 * time.Second):
		t.Fatal("the scheduled run did not happen")
	}
	waitIdle(t, r)
	if st := r.Status(); time.Unix(st.NextRun, 0).UTC() != time.Date(2026, 10, 12, 3, 30, 0, 0, time.UTC) {
		t.Fatalf("next run %v", time.Unix(st.NextRun, 0).UTC())
	}
	if !r.Trigger() {
		t.Fatal("trigger refused while idle")
	}
	select {
	case <-runs:
	case <-time.After(5 * time.Second):
		t.Fatal("the triggered run did not happen")
	}
	waitIdle(t, r)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func waitIdle(t *testing.T, r *Runner) {
	t.Helper()
	for range 500 {
		if !r.Status().Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("backup still running")
}

func TestHTTPEndpoints(t *testing.T) {
	release := make(chan struct{})
	r, dir := newRunner(t, func(o *Options) {
		o.Dump = DumpFunc(func(_ context.Context, w io.Writer) error {
			<-release
			_, err := io.WriteString(w, "PGDMP")
			return err
		})
	})
	mux := http.NewServeMux()
	mux.Handle("GET /internal/backups", List(r, coreToken))
	mux.Handle("POST /internal/backups/run", Start(r, coreToken))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Loop(ctx) }()

	do := func(method, path, token string) (int, map[string]any) {
		req, _ := http.NewRequestWithContext(context.Background(), method, srv.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var v map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&v)
		return resp.StatusCode, v
	}
	for _, tok := range []string{"", botToken, coreToken + "x"} {
		if code, _ := do(http.MethodGet, "/internal/backups", tok); code != http.StatusUnauthorized {
			t.Fatalf("list with token %q: %d", tok, code)
		}
		if code, _ := do(http.MethodPost, "/internal/backups/run", tok); code != http.StatusUnauthorized {
			t.Fatalf("run with token %q: %d", tok, code)
		}
	}
	code, v := do(http.MethodGet, "/internal/backups", coreToken)
	if code != http.StatusOK || v["running"] != false || v["dir"] != dir || v["next_run"].(float64) <= 0 ||
		v["disk_free"].(float64) != float64(1<<40) || v["last_error"] != "" {
		t.Fatalf("list: %d %v", code, v)
	}
	if b, ok := v["backups"].([]any); !ok || len(b) != 0 {
		t.Fatalf("backups not an empty list: %v", v["backups"])
	}
	if code, v = do(http.MethodPost, "/internal/backups/run", coreToken); code != http.StatusAccepted || v["started"] != true {
		t.Fatalf("run: %d %v", code, v)
	}
	if code, v = do(http.MethodPost, "/internal/backups/run", coreToken); code != http.StatusConflict || v["started"] != false || v["reason"] != "running" {
		t.Fatalf("second run: %d %v", code, v)
	}
	if _, v = do(http.MethodGet, "/internal/backups", coreToken); v["running"] != true {
		t.Fatalf("not running: %v", v)
	}
	close(release)
	waitIdle(t, r)
	_, v = do(http.MethodGet, "/internal/backups", coreToken)
	list := v["backups"].([]any)
	first, _ := list[0].(map[string]any)
	if len(list) != 1 || first["file"] == "" || first["sha256"] == "" || first["size"].(float64) <= 0 {
		t.Fatalf("after the run: %v", v)
	}
}

func TestRunNow(t *testing.T) {
	r, _ := newRunner(t, nil)
	mux := http.NewServeMux()
	mux.Handle("GET /internal/backups", List(r, coreToken))
	mux.Handle("POST /internal/backups/run", Start(r, coreToken))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Loop(ctx) }()

	var out bytes.Buffer
	if err := RunNow(ctx, srv.Client(), srv.URL, coreToken, &out, 10*time.Millisecond); err != nil {
		t.Fatalf("%v: %s", err, &out)
	}
	if !strings.Contains(out.String(), "Backup bobres-") || !strings.Contains(out.String(), "Kept on the server only") {
		t.Fatalf("output: %s", &out)
	}
	if err := RunNow(ctx, srv.Client(), srv.URL, "wrong-token", &out, time.Millisecond); err == nil || !strings.Contains(err.Error(), "refused the token") {
		t.Fatalf("wrong token: %v", err)
	}

	failing, _ := newRunner(t, func(o *Options) {
		o.Dump = DumpFunc(func(context.Context, io.Writer) error { return errors.New("pg_dump: boom") })
	})
	mux2 := http.NewServeMux()
	mux2.Handle("GET /internal/backups", List(failing, coreToken))
	mux2.Handle("POST /internal/backups/run", Start(failing, coreToken))
	srv2 := httptest.NewServer(mux2)
	defer srv2.Close()
	go func() { _ = failing.Loop(ctx) }()
	if err := RunNow(ctx, srv2.Client(), srv2.URL, coreToken, &out, 10*time.Millisecond); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("failed backup reported as %v", err)
	}
}

// TestRealPgDump dumps the test database with the pg_dump on PATH, when there
// is one that matches the server.
func TestRealPgDump(t *testing.T) {
	pg, err := exec.LookPath("pg_dump")
	if err != nil {
		t.Skip("pg_dump not on PATH")
	}
	dsn := testdb.DSN(t)
	r, dir := newRunner(t, func(o *Options) { o.Dump = PgDump{Path: pg, URL: dsn} })
	sc, err := r.RunOnce(context.Background())
	if err != nil {
		if strings.Contains(err.Error(), "version mismatch") {
			t.Skipf("pg_dump does not match the server: %v", err)
		}
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, sc.File))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	br, err := backup.Open(f, testKey)
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 5)
	if _, err := io.ReadFull(br.Dump(), head); err != nil || string(head) != "PGDMP" {
		t.Fatalf("not a pg_dump custom archive: %q %v", head, err)
	}
}
