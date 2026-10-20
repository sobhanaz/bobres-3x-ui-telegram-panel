package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The dashboard's "Check again" and its channel setting: the checker follows
// a channel that can change, looks again at once when asked (once for many
// askers), and never shows one channel's release under the other's name.

func TestEffectiveChannel(t *testing.T) {
	for _, tc := range []struct{ setting, fallback, want string }{
		{"", "", "stable"},
		{"", "stable", "stable"},
		{"", "beta", "beta"},
		{"stable", "beta", "stable"}, // the dashboard's setting wins over .env
		{"beta", "stable", "beta"},
		{" Beta ", "", "beta"},
		{"nightly", "beta", "beta"}, // a setting that is not a channel is ignored, never guessed at
		{"nightly", "", "stable"},
		{"", "nightly", "stable"},
		{"BETA", "STABLE", "beta"},
	} {
		if got := EffectiveChannel(tc.setting, tc.fallback); got != tc.want {
			t.Errorf("EffectiveChannel(%q, %q) = %q, want %q", tc.setting, tc.fallback, got, tc.want)
		}
	}
}

func TestNotesURLAndValidNumber(t *testing.T) {
	const base = "https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v"
	for in, want := range map[string]string{
		"0.7.0": base + "0.7.0", "v0.7.0": base + "0.7.0", " 1.5.0-rc.10 ": base + "1.5.0-rc.10", "10.20.30-beta": base + "10.20.30-beta",
		"": "", "dev": "", "1.2": "", "1.2.3+b5": "", "1.2.3/../../x": "", "1.2.3?x=1": "", "1.2.3#top": "", "01.2.3": "", "1.2.3-": "",
		"1.2.3-rc.1\nx": "", "1.2.3 rc": "", "1.2.3-ünï": "", "https://evil.example/": "", "vv1.2.3": "",
	} {
		if got := NotesURL(in); got != want {
			t.Errorf("NotesURL(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]bool{
		"1.2.3": true, "0.0.1": true, "1.2.3-rc.1": true, "1.2.3-beta": true, "1.2.3-0.3.7": true,
		"v1.2.3": false, "1.2": false, "1.2.3+b": false, "1.02.3": false, "1.2.3-": false, "": false, "1.2.3-a..b": false,
		strings.Repeat("1", 65) + ".0.0": false,
	} {
		if ValidNumber(in) != want {
			t.Errorf("ValidNumber(%q) != %v", in, want)
		}
	}
}

// countingReleases is a fake GitHub that counts how many times each file was
// asked for, and can be made slow.
type countingReleases struct {
	srv  *httptest.Server
	keys atomic.Int32
	gate chan struct{} // when set, answers wait for it to be closed
}

func newCountingReleases(t *testing.T, files map[string][]byte) *countingReleases {
	t.Helper()
	f := &countingReleases{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.gate != nil {
			select {
			case <-f.gate:
			case <-r.Context().Done():
				return
			}
		}
		name := strings.TrimPrefix(r.URL.Path, "/releases/latest/download/")
		if name == "keys.txt" {
			f.keys.Add(1)
		}
		b, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestCheckNow(t *testing.T) {
	r := makeRelease(t, "1.4.0", "")
	files := map[string][]byte{"checksums.txt": r.sums, "checksums.txt.sig": r.sumSig, "keys.txt": r.keys, "keys.txt.sig.2": r.keysSig}

	t.Run("looks at once and returns the new outcome", func(t *testing.T) {
		f := newCountingReleases(t, files)
		c := newTestChecker("1.3.0", f.srv.URL, r.root.pub)
		if st := c.Status(); st.Error != ErrNotChecked {
			t.Fatalf("before: %+v", st)
		}
		st := c.CheckNow(context.Background())
		if st.Latest != "1.4.0" || !st.Verified || !st.Newer || st.Error != "" || st.CheckedAt != testNow.Unix() {
			t.Fatalf("got %+v", st)
		}
		if c.Status() != st {
			t.Fatal("the outcome was not kept")
		}
	})

	t.Run("a check that just worked is not repeated within the gap, then it is", func(t *testing.T) {
		f := newCountingReleases(t, files)
		c := newTestChecker("1.3.0", f.srv.URL, r.root.pub)
		first := c.CheckNow(context.Background())
		second := c.CheckNow(context.Background())
		if first != second || f.keys.Load() != 1 {
			t.Fatalf("a second click within the gap asked GitHub again: %d look(s), %+v / %+v", f.keys.Load(), first, second)
		}
		// the scheduled check counts too: it is the one a click returns
		c = newTestChecker("1.3.0", f.srv.URL, r.root.pub)
		f.keys.Store(0)
		c.Check(context.Background())
		c.CheckNow(context.Background())
		if f.keys.Load() != 1 {
			t.Fatalf("a click right after the scheduled check asked again: %d", f.keys.Load())
		}
		later := testNow.Add(11 * time.Second)
		c.now = func() time.Time { return later }
		st := c.CheckNow(context.Background())
		if f.keys.Load() != 2 || st.CheckedAt != later.Unix() {
			t.Fatalf("after the gap: %d look(s), checked at %d", f.keys.Load(), st.CheckedAt)
		}
	})

	t.Run("a failed check is retried at once", func(t *testing.T) {
		f := newCountingReleases(t, nil) // nothing published: no_release
		c := newTestChecker("1.3.0", f.srv.URL, r.root.pub)
		if st := c.CheckNow(context.Background()); st.Error != ErrNoRelease {
			t.Fatalf("got %+v", st)
		}
		// GitHub has the release now: the retry is a real look, not the failure again
		f2 := newCountingReleases(t, files)
		c.src = testSource(f2.srv.URL)
		if st := c.CheckNow(context.Background()); st.Error != "" || st.Latest != "1.4.0" {
			t.Fatalf("the retry returned the old failure: %+v", st)
		}
	})

	t.Run("many askers at once cost one look", func(t *testing.T) {
		f := newCountingReleases(t, files)
		f.gate = make(chan struct{})
		c := newTestChecker("1.3.0", f.srv.URL, r.root.pub)
		var wg sync.WaitGroup
		out := make([]Status, 6)
		for i := range out {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[i] = c.CheckNow(context.Background())
			}()
		}
		time.Sleep(50 * time.Millisecond)
		close(f.gate)
		wg.Wait()
		if f.keys.Load() != 1 {
			t.Fatalf("%d looks for 6 askers", f.keys.Load())
		}
		for i, st := range out {
			if st.Latest != "1.4.0" || !st.Verified {
				t.Fatalf("asker %d: %+v", i, st)
			}
		}
	})

	t.Run("an asker that stops waiting leaves the check running, and kept", func(t *testing.T) {
		f := newCountingReleases(t, files)
		f.gate = make(chan struct{})
		c := newTestChecker("1.3.0", f.srv.URL, r.root.pub)
		ctx, cancel := context.WithCancel(context.Background())
		got := make(chan Status, 1)
		go func() { got <- c.CheckNow(ctx) }()
		time.Sleep(30 * time.Millisecond)
		cancel()
		if st := <-got; st.Error != ErrNotChecked {
			t.Fatalf("the caller that gave up got %+v, want the last outcome", st)
		}
		close(f.gate)
		waitStatus(t, c, func(st Status) bool { return st.Verified })
	})

	t.Run("a check a person waits for gives up in time, and says so", func(t *testing.T) {
		f := newCountingReleases(t, files)
		f.gate = make(chan struct{}) // never opened: GitHub is slow
		defer close(f.gate)
		c := newTestChecker("1.3.0", f.srv.URL, r.root.pub)
		c.ManualTimeout = 80 * time.Millisecond
		st := c.CheckNow(context.Background())
		if st.Error != ErrUnreachable || st.Latest != "" || st.Detail == "" || st.CheckedAt != testNow.Unix() {
			t.Fatalf("got %+v", st)
		}
		if c.Status() != st {
			t.Fatal("the timeout was not kept: the page would show the old outcome as if nothing was tried")
		}
	})

	t.Run("a release read before stays known through a failed check", func(t *testing.T) {
		f := newCountingReleases(t, files)
		c := newTestChecker("1.3.0", f.srv.URL, r.root.pub)
		if st := c.CheckNow(context.Background()); !st.Verified {
			t.Fatalf("first: %+v", st)
		}
		f.srv.Close()
		c.now = func() time.Time { return testNow.Add(time.Minute) }
		st := c.CheckNow(context.Background())
		if st.Error != ErrUnreachable || st.Latest != "1.4.0" || !st.Newer || !st.Verified {
			t.Fatalf("got %+v", st)
		}
	})
}

func waitStatus(t *testing.T, c *Checker, ok func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := c.Status(); ok(st) {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out; the status is %+v", c.Status())
	return Status{}
}

func TestSetChannel(t *testing.T) {
	root := newKey(t)
	stable := makeChannelRelease(t, "1.4.2", ChannelStable, root, "")
	rc10 := makeChannelRelease(t, "1.5.0-rc.10", ChannelBeta, root, "")
	setup := func(t *testing.T) *fakeReleases {
		f := newFakeReleases(t)
		f.list = []byte(`[{"tag_name": "v1.5.0-rc.10", "prerelease": true}, {"tag_name": "v1.4.2"}]`)
		f.latest = stable.files()
		f.byTag["1.4.2"], f.byTag["1.5.0-rc.10"] = stable.files(), rc10.files()
		return f
	}

	t.Run("a change forgets the other channel's release", func(t *testing.T) {
		f := setup(t)
		c := newChannelChecker("1.4.0", ChannelBeta, f.srv.URL, root.pub)
		if st := c.Check(context.Background()); st.Latest != "1.5.0-rc.10" || !st.Newer || st.Channel != "beta" {
			t.Fatalf("beta: %+v", st)
		}
		if !c.SetChannel("stable") {
			t.Fatal("going back to stable was not a change")
		}
		// until stable has been looked at, nothing of beta's is shown under stable's name
		st := c.Status()
		if st.Channel != "stable" || st.Latest != "" || st.Newer || st.Verified || st.Error != ErrNotChecked || st.CheckedAt != 0 || st.Current != "1.4.0" {
			t.Fatalf("after the change: %+v", st)
		}
		if c.Channel() != "stable" {
			t.Fatalf("channel %q", c.Channel())
		}
		if st := c.Check(context.Background()); st.Latest != "1.4.2" || st.Channel != "stable" || !st.Newer {
			t.Fatalf("stable: %+v", st)
		}
	})
	t.Run("the same channel, or one that is none, changes nothing", func(t *testing.T) {
		f := setup(t)
		c := newChannelChecker("1.4.0", ChannelStable, f.srv.URL, root.pub)
		c.Check(context.Background())
		before := c.Status()
		for _, v := range []string{"stable", " STABLE ", "", "nightly", "latest", "beta,stable"} { // "" is stable too: the same channel
			if c.SetChannel(v) {
				t.Fatalf("SetChannel(%q) changed the channel to %q", v, c.Channel())
			}
		}
		if c.Channel() != "stable" || c.Status() != before {
			t.Fatalf("the status changed: %+v", c.Status())
		}
		if !c.SetChannel(" Beta ") || c.Channel() != "beta" {
			t.Fatalf("beta not taken: %q", c.Channel())
		}
	})
	t.Run("Run looks again at once on a change", func(t *testing.T) {
		f := setup(t)
		c := newChannelChecker("1.4.0", ChannelStable, f.srv.URL, root.pub)
		c.Every, c.Retry = time.Hour, time.Hour // only a change can make it look again
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- c.Run(ctx) }()
		waitStatus(t, c, func(st Status) bool { return st.Verified && st.Latest == "1.4.2" })
		c.SetChannel("beta")
		st := waitStatus(t, c, func(st Status) bool { return st.Verified && st.Channel == "beta" })
		if st.Latest != "1.5.0-rc.10" {
			t.Fatalf("beta: %+v", st)
		}
		c.SetChannel("stable")
		waitStatus(t, c, func(st Status) bool { return st.Verified && st.Channel == "stable" && st.Latest == "1.4.2" })
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
	t.Run("an answer that was being fetched for the old channel is dropped", func(t *testing.T) {
		f := setup(t)
		gate := make(chan struct{})
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-gate
			http.Redirect(w, r, f.srv.URL+r.URL.Path, http.StatusFound) //nolint:gosec // a fake GitHub that answers slowly, for the test
		}))
		defer slow.Close()
		c := newChannelChecker("1.4.0", ChannelStable, slow.URL, root.pub)
		checked := make(chan Status, 1)
		go func() { checked <- c.Check(context.Background()) }()
		time.Sleep(50 * time.Millisecond) // stable's check is waiting on GitHub
		c.SetChannel("beta")
		close(gate)
		<-checked
		st := c.Status()
		if st.Channel != "beta" || st.Latest != "" || st.Verified || st.Error != ErrNotChecked {
			t.Fatalf("stable's late answer leaked into beta's status: %+v", st)
		}
	})
}
