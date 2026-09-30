package xui_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
)

const (
	token = "test-token-not-a-secret"
	gib   = int64(1) << 30
	dayMs = int64(24 * 60 * 60 * 1000)
)

func setup(t *testing.T) (*xui.Client, *xuifake.Server) {
	t.Helper()
	s := xuifake.NewServer(token)
	t.Cleanup(s.Close)
	return xui.New(s.URL, token), s
}

func spec(email string) xui.ClientSpec {
	return xui.ClientSpec{
		ID: "11111111-1111-1111-1111-111111111111", Email: email, SubID: "sub-" + email,
		TotalGB: 10 * gib, ExpiryTime: time.Now().Add(30 * 24 * time.Hour).UnixMilli(), Enable: true,
	}
}

func TestLifecycle(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	email := "u1-o1"
	if err := c.AddClient(ctx, spec(email), []int{1, 2}); err != nil {
		t.Fatal(err)
	}
	d, err := c.GetClient(ctx, email)
	if err != nil || d.Client.Email != email || len(d.InboundIDs) != 2 {
		t.Fatalf("get: %v %+v", err, d)
	}
	links, err := c.Links(ctx, email)
	if err != nil || len(links) != 2 {
		t.Fatalf("links: %v %v", links, err)
	}
	s.AddUsage(email, 100, 200)
	tr, err := c.Traffic(ctx, email)
	if err != nil || tr.Used() != 300 {
		t.Fatalf("traffic: %v %+v", err, tr)
	}
	if err := c.ResetTraffic(ctx, email); err != nil {
		t.Fatal(err)
	}
	if tr, _ = c.Traffic(ctx, email); tr.Used() != 0 {
		t.Fatalf("reset failed: %+v", tr)
	}
	if err := c.DeleteClient(ctx, email, false); err != nil {
		t.Fatal(err)
	}
	if s.Has(email) {
		t.Fatal("client still exists")
	}
}

func TestRenewalUsesMilliseconds(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	sp := spec("r1")
	if err := c.AddClient(ctx, sp, []int{1}); err != nil {
		t.Fatal(err)
	}
	res, err := c.BulkAdjust(ctx, xui.AdjustRequest{Emails: []string{"r1"}, AddDays: 30, AddBytes: 5 * gib})
	if err != nil || res.Adjusted != 1 {
		t.Fatalf("adjust: %v %+v", err, res)
	}
	snap, _ := s.Snapshot("r1")
	if snap.ExpiryTime != sp.ExpiryTime+30*dayMs {
		t.Fatalf("expiry %d want %d", snap.ExpiryTime, sp.ExpiryTime+30*dayMs)
	}
	if snap.TotalGB != 15*gib {
		t.Fatalf("quota %d", snap.TotalGB)
	}
}

func TestRenewReenablesDepletedClient(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	sp := spec("d1")
	sp.Enable = false // panel disabled it because the quota was used up
	if err := c.AddClient(ctx, sp, []int{1}); err != nil {
		t.Fatal(err)
	}
	s.AddUsage("d1", 5*gib, 5*gib) // used == total: depleted
	if _, err := c.BulkAdjust(ctx, xui.AdjustRequest{Emails: []string{"d1"}, AddBytes: 10 * gib}); err != nil {
		t.Fatal(err)
	}
	if snap, _ := s.Snapshot("d1"); !snap.Enable {
		t.Fatal("client should be re-enabled after top-up")
	}
}

func TestUnlimitedFieldsAreSkippedNotConverted(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	sp := spec("u9")
	sp.TotalGB, sp.ExpiryTime = 0, 0 // unlimited both
	_ = c.AddClient(ctx, sp, []int{1})
	res, err := c.BulkAdjust(ctx, xui.AdjustRequest{Emails: []string{"u9"}, AddDays: 30, AddBytes: gib})
	if err != nil {
		t.Fatal(err)
	}
	if res.Adjusted != 0 || len(res.Skipped) != 2 {
		t.Fatalf("want 0 adjusted / 2 skipped, got %+v", res)
	}
	if snap, _ := s.Snapshot("u9"); snap.ExpiryTime != 0 || snap.TotalGB != 0 {
		t.Fatalf("unlimited client was converted to limited: %+v", snap)
	}
}

func TestDuplicateEmailIsAPIError(t *testing.T) {
	ctx := context.Background()
	c, _ := setup(t)
	_ = c.AddClient(ctx, spec("dup"), []int{1})
	err := c.AddClient(ctx, spec("dup"), []int{1})
	var ae *xui.APIError
	if !errors.As(err, &ae) || !strings.Contains(ae.Msg, "Duplicate") {
		t.Fatalf("want duplicate APIError, got %v", err)
	}
}

func TestNotFound(t *testing.T) {
	c, _ := setup(t)
	if _, err := c.GetClient(context.Background(), "ghost"); err == nil {
		t.Fatal("want error")
	}
}

func TestWrongTokenAndNoTokenLeak(t *testing.T) {
	s := xuifake.NewServer(token)
	t.Cleanup(s.Close)
	bad := xui.New(s.URL, "wrong-secret-value")
	err := bad.AddClient(context.Background(), spec("x"), []int{1})
	if !xui.IsUnauthorized(err) {
		t.Fatalf("want unauthorized, got %v", err)
	}
	if strings.Contains(err.Error(), "wrong-secret-value") {
		t.Fatal("token leaked into error")
	}
}

func TestNonJSONResponseDoesNotEchoBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>internal proxy error with secret-detail</html>"))
	}))
	t.Cleanup(srv.Close)
	err := xui.New(srv.URL, token).AddClient(context.Background(), spec("x"), []int{1})
	var ae *xui.APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusBadGateway || strings.Contains(err.Error(), "secret-detail") {
		t.Fatalf("bad handling: %v", err)
	}
}

func TestInjectedFailureThenRecovery(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	s.SetFailNext(1)
	if err := c.AddClient(ctx, spec("f1"), []int{1}); err == nil {
		t.Fatal("want injected failure")
	}
	if s.Has("f1") {
		t.Fatal("failed call must not create the client")
	}
	if err := c.AddClient(ctx, spec("f1"), []int{1}); err != nil {
		t.Fatalf("retry should succeed: %v", err)
	}
}

func TestContextCancelAndTimeout(t *testing.T) {
	c, s := setup(t)
	s.SetLatency(500 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.AddClient(ctx, spec("slow"), []int{1}); err == nil {
		t.Fatal("want timeout error")
	}
}

func TestInputValidation(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	if err := c.AddClient(ctx, xui.ClientSpec{}, []int{1}); err == nil {
		t.Fatal("empty email must fail")
	}
	if err := c.AddClient(ctx, spec("x"), nil); err == nil {
		t.Fatal("no inbounds must fail")
	}
	if _, err := c.BulkAdjust(ctx, xui.AdjustRequest{}); err == nil {
		t.Fatal("no emails must fail")
	}
}

func TestInboundOptionsAndEmailEscaping(t *testing.T) {
	ctx := context.Background()
	c, _ := setup(t)
	opts, err := c.InboundOptions(ctx)
	if err != nil || len(opts) != 3 {
		t.Fatalf("options: %v %v", err, opts)
	}
	weird := "user+tag/with space@example.com"
	if err := c.AddClient(ctx, spec(weird), []int{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetClient(ctx, weird); err != nil {
		t.Fatalf("escaped path failed: %v", err)
	}
}

func TestConcurrentOverlappingAdjustsDoNotRaceOrDeadlock(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	emails := []string{"a", "b", "c"}
	for _, e := range emails {
		if err := c.AddClient(ctx, spec(e), []int{1}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			set := []string{emails[i%3], emails[(i+1)%3]} // overlapping, opposite orders
			if i%2 == 0 {
				set[0], set[1] = set[1], set[0]
			}
			if _, err := c.BulkAdjust(ctx, xui.AdjustRequest{Emails: set, AddBytes: 1}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock")
	}
	total := int64(0)
	for _, e := range emails {
		snap, _ := s.Snapshot(e)
		total += snap.TotalGB - 10*gib
	}
	if total != 60 { // 30 calls x 2 emails x 1 byte
		t.Fatalf("lost updates: total bytes added %d, want 60", total)
	}
}
