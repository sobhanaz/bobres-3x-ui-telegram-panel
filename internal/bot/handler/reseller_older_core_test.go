package handler

import (
	"context"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// olderCore is a core that does not know the reseller management calls yet
// (the bot was updated first): gRPC answers them as unimplemented.
type olderCore struct{ corev1.CoreServiceClient }

var errOlder = status.Error(codes.Unimplemented, "unknown method")

func (olderCore) ListResellerServices(context.Context, *corev1.ListResellerServicesRequest, ...grpc.CallOption) (*corev1.ListResellerServicesResponse, error) {
	return nil, errOlder
}

func (olderCore) SetResellerServiceEnabled(context.Context, *corev1.SetResellerServiceEnabledRequest, ...grpc.CallOption) (*corev1.Subscription, error) {
	return nil, errOlder
}

func (olderCore) DeleteResellerService(context.Context, *corev1.DeleteResellerServiceRequest, ...grpc.CallOption) (*corev1.DeleteResellerServiceResponse, error) {
	return nil, errOlder
}

func (olderCore) SetSubscriptionNote(context.Context, *corev1.SetSubscriptionNoteRequest, ...grpc.CallOption) (*corev1.Subscription, error) {
	return nil, errOlder
}

func (olderCore) ListInvitedCustomers(context.Context, *corev1.ListInvitedCustomersRequest, ...grpc.CallOption) (*corev1.ListInvitedCustomersResponse, error) {
	return nil, errOlder
}

// A bot updated before its core still shows a reseller the plain list of
// their services and each service; the new buttons say "not available" and
// change nothing.
func TestResellerWithACoreOlderThanTheBot(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.customer(2601, "rsl")
	w.makeReseller(2601, 0, 10, 0)
	id := w.seedService(2601, "Ali")
	w.h = New(olderCore{w.stack.Core}, w.tg, state.NewMemory(), i18n.MustLoad(), nil,
		Config{AdminTelegramIDs: []int64{ownerID}, BotName: "TestVPN", BotUsername: "test_vpn_bot"}, nil)

	rs.text("/menu")
	rs.press("subs")
	rs.shownHas("📦 <b>Customer services</b> (1)")
	if strings.Contains(strings.Join(rs.shown().labels(), "|"), "Active (") {
		t.Fatalf("groups from a core that has none: %v", rs.shown().labels())
	}
	rs.press("sub:" + id)
	rs.shownHas("Subscription link")
	for _, d := range []string{"rs:pz:", "rs:dy:", "rs:cu:"} {
		rs.tap(d + id)
		rs.toast("not available right now")
	}
	rs.tap("rs:nt:" + id)
	rs.text("a note")
	rs.sees("not available right now")
	if got := w.subscription(id); got.Status != "active" || got.Note != nil {
		t.Fatalf("the service changed: %+v", got)
	}
}
