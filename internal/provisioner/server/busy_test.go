package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// What a busy 3x-ui answers (SQLite is writing): a refusal inside HTTP 200
// that says nothing about the client. And a page that is not the panel's.
const (
	panelLocked = `{"success":false,"msg":"database is locked","obj":null}`
	notPanel    = `<html><body>The panel is restarting</body></html>`
)

// front is a proxy before a fake panel that answers chosen routes itself
// ("add", "get", "del", "options"), or holds them before passing them on.
type front struct {
	mu     sync.Mutex
	answer map[string]string        // route → the body answered with HTTP 200
	hold   map[string]time.Duration // route → how long it waits first
}

func (fr *front) set(body string, routes ...string) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	fr.answer = map[string]string{}
	for _, r := range routes {
		fr.answer[r] = body
	}
}

func (fr *front) holdRoute(route string, d time.Duration) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	fr.hold = map[string]time.Duration{route: d}
}

// route names a panel API path the way xuifake does.
func route(p string) string {
	p = strings.TrimPrefix(p, "/panel/api/")
	if p == "inbounds/options" {
		return "options"
	}
	name, _, _ := strings.Cut(strings.TrimPrefix(p, "clients/"), "/")
	return name
}

// frontPanel registers one more fake panel, reached through a front.
func (f *fixture) frontPanel(t *testing.T, name string) (*xuifake.Server, *store.Server, *front) {
	t.Helper()
	fake := xuifake.NewServer(panelTok)
	t.Cleanup(fake.Close)
	target, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	pass := httputil.NewSingleHostReverseProxy(target)
	fr := &front{}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt := route(r.URL.Path)
		fr.mu.Lock()
		body, answer := fr.answer[rt]
		hold := fr.hold[rt]
		fr.mu.Unlock()
		switch {
		case answer:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, body)
		case hold > 0:
			// The panel runs the call on its own time: it lands even when the
			// caller gave up waiting.
			in, _ := io.ReadAll(r.Body)
			time.Sleep(hold)
			out, err := http.NewRequestWithContext(context.Background(), r.Method, fake.URL+r.URL.RequestURI(), bytes.NewReader(in)) //nolint:gosec // a test proxy relays to the test fake
			if err != nil {
				return
			}
			out.Header = r.Header.Clone()
			if resp, err := http.DefaultClient.Do(out); err == nil { //nolint:gosec // a test proxy relays to the test fake
				_, _ = io.Copy(w, resp.Body)
				_ = resp.Body.Close()
			}
		default:
			pass.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(proxy.Close)
	sv, err := f.st.AddServer(context.Background(), &store.Server{Name: name, BaseURL: proxy.URL, Token: panelTok, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return fake, sv, fr
}

// Only the panel saying "record not found" means a client is not there.
func TestNoSuchClient(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&xui.APIError{Status: http.StatusOK, Msg: "record not found"}, true},
		{&xui.APIError{Status: http.StatusOK, Msg: "Obtain failed (Record Not Found)"}, true},
		{&xui.APIError{Status: http.StatusOK, Msg: "client u1-new not found"}, true},
		{fmt.Errorf("lookup: %w", &xui.APIError{Status: http.StatusOK, Msg: "record not found"}), true},
		{&xui.APIError{Status: http.StatusOK, Msg: "database is locked"}, false},
		{&xui.APIError{Status: http.StatusOK, Msg: "unexpected non-JSON response"}, false},
		{&xui.APIError{Status: http.StatusOK, Msg: "client u1-new is in use"}, false},
		{&xui.APIError{Status: http.StatusInternalServerError, Msg: "record not found"}, false},
		{&xui.APIError{Status: http.StatusNotFound, Msg: "Not Found"}, false},
		{errors.New("xui: request failed: connection refused"), false},
		{nil, false},
	} {
		if got := noSuchClient(c.err); got != c.want {
			t.Errorf("noSuchClient(%v) = %v, want %v", c.err, got, c.want)
		}
	}
	if !answered(&xui.APIError{Status: http.StatusOK, Msg: "database is locked"}) || answered(context.DeadlineExceeded) ||
		answered(errors.New("xui: request failed: timeout")) {
		t.Fatal("answered")
	}
}

// A busy panel refuses calls inside HTTP 200 without saying anything about
// the client: the client's row, and a reservation, stay until the panel
// answers, so nothing is left on a panel without a row to retry with.
func TestBusyPanelKeepsRowsAndReservations(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fake, sv, fr := f.frontPanel(t, "busy")
	kept := func(i int, pending bool) {
		t.Helper()
		cm, err := f.st.GetClientMap(ctx, subID(i))
		if err != nil || cm.ServerID != sv.ID || cm.Pending != pending {
			t.Fatalf("row %d: %v %+v", i, err, cm)
		}
	}
	gone := func(i int) {
		t.Helper()
		if _, err := f.st.GetClientMap(ctx, subID(i)); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("row %d kept: %v", i, err)
		}
	}

	// Deleting a delivered client while the panel is busy: refused, kept.
	if _, err := f.create(t, 1, sv.ID); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{panelLocked, notPanel} {
		fr.set(body, "del", "get")
		if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: subID(1)}); status.Code(err) != codes.Unavailable {
			t.Fatalf("delete on a busy panel (%.20s): %v", body, err)
		}
		kept(1, false)
		if !fake.Has("u1-new") {
			t.Fatal("the test proves nothing: the client was deleted")
		}
	}
	fr.set("")
	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: subID(1)}); err != nil {
		t.Fatalf("delete once the panel answers: %v", err)
	}
	gone(1)
	if fake.Has("u1-new") {
		t.Fatal("client still on the panel")
	}

	// A create refused while the lookup is refused too: the client may be
	// there, so the reservation keeps the retry on this panel.
	fr.set(panelLocked, "add", "get")
	if _, err := f.create(t, 2, sv.ID); status.Code(err) != codes.Unavailable {
		t.Fatalf("create on a busy panel: %v", err)
	}
	kept(2, true)
	fr.set("")
	if cl, err := f.create(t, 2, f.server.ID); err != nil || cl.GetServerId() != sv.ID || !fake.Has("u2-new") {
		t.Fatalf("retry: %v %+v", err, cl)
	}
	kept(2, false)

	// A create refused while the lookup says the client is not there: none
	// of ours can be, so the reservation goes.
	fr.set(panelLocked, "add")
	if _, err := f.create(t, 3, sv.ID); status.Code(err) != codes.Unavailable {
		t.Fatalf("refused create: %v", err)
	}
	gone(3)

	// An earlier attempt's reservation, the panel busy listing its inbounds
	// and looking the client up: kept. Once the lookup answers "not found",
	// it goes.
	xid := f.st.SubIDFor(subID(4))
	if _, _, err := f.st.ReserveClient(ctx, &store.ClientMap{SubscriptionID: subID(4), ServerID: sv.ID, Email: "u4-new", XUISubID: &xid}); err != nil {
		t.Fatal(err)
	}
	fr.set(panelLocked, "options", "get")
	if _, err := f.create(t, 4); status.Code(err) != codes.Unavailable {
		t.Fatalf("inbounds of a busy panel: %v", err)
	}
	kept(4, true)
	fr.set(panelLocked, "options")
	if _, err := f.create(t, 4); status.Code(err) != codes.Unavailable {
		t.Fatalf("inbounds of a busy panel, lookup answered: %v", err)
	}
	gone(4)
	if f.fake.Calls("add") != 0 {
		t.Fatal("a client went to the other panel")
	}
}

// Our client gave up on a create the panel was still running: the lookup
// right after cannot see it yet, but it lands. The reservation stays, and the
// retry adopts the client on that panel instead of making a second one.
func TestCreateStillRunningOnThePanelKeepsTheReservation(t *testing.T) {
	f := newFixture(t)
	fake, sv, fr := f.frontPanel(t, "slow")
	srv := New(f.st, xui.WithHTTPClient(&http.Client{Timeout: 300 * time.Millisecond}), xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req := &provisionerv1.CreateClientRequest{SubscriptionId: subID(1), Email: "u1-new", DurationDays: 30, ServerId: sv.ID}
	fr.holdRoute("add", time.Second)
	if _, err := srv.CreateClient(ctx, req); status.Code(err) != codes.Unavailable {
		t.Fatalf("timed-out create: %v", err)
	}
	cm, err := f.st.GetClientMap(ctx, subID(1))
	if err != nil || !cm.Pending || cm.ServerID != sv.ID {
		t.Fatalf("reservation: %v %+v", err, cm)
	}
	for !fake.Has("u1-new") {
		if ctx.Err() != nil {
			t.Fatal("the held create never landed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	fr.holdRoute("", 0)
	req.ServerId = f.server.ID // a retry goes back to the reservation's server
	cl, err := srv.CreateClient(ctx, req)
	if err != nil || cl.GetServerId() != sv.ID || cl.GetXuiSubId() != f.st.SubIDFor(subID(1)) || cl.GetExpiresAt() == 0 {
		t.Fatalf("retry: %v %+v", err, cl)
	}
	if fake.Calls("add") != 2 || f.fake.Calls("add") != 0 {
		t.Fatalf("add calls: here %d, elsewhere %d", fake.Calls("add"), f.fake.Calls("add"))
	}
	if cm, _ = f.st.GetClientMap(ctx, subID(1)); cm.Pending {
		t.Fatalf("after the retry: %+v", cm)
	}
}
