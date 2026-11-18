package notify

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const tutorialID = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

// bundleCore is fakeCore with a service's links and the tutorial of the Tutorials section the owner picked.
type bundleCore struct {
	fakeCore
	links    *corev1.SubscriptionLinks
	entry    *corev1.HelpEntry
	entryErr error
	asked    []*corev1.GetHelpEntryRequest
}

func (c *bundleCore) GetSubscriptionLinks(context.Context, *corev1.GetSubscriptionLinksRequest, ...grpc.CallOption) (*corev1.SubscriptionLinks, error) {
	return c.links, nil
}

func (c *bundleCore) GetHelpEntry(_ context.Context, in *corev1.GetHelpEntryRequest, _ ...grpc.CallOption) (*corev1.HelpEntry, error) {
	c.asked = append(c.asked, in)
	if c.entryErr != nil {
		return nil, c.entryErr
	}
	return c.entry, nil
}

const (
	firstConfig  = "vless://u@h.example:443?type=tcp&security=reality#first"
	secondConfig = "vless://u@h.example:444?type=ws#second"
)

func bundleNotifier(t *testing.T) (*Notifier, *bundleCore, *fakeTG, *settings.Store) {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	tgf := &fakeTG{}
	core := &bundleCore{
		fakeCore: fakeCore{langs: map[int64]string{ownerTG: "fa", adminTG: "en"}},
		links:    &corev1.SubscriptionLinks{ConfigLinks: []string{firstConfig, secondConfig}},
		entry: &corev1.HelpEntry{Id: tutorialID, Kind: "tutorial", Title: "Connect on Android", Body: "Open <b>v2rayNG</b>, tap +.", Language: "en",
			Buttons: []*corev1.HelpButton{
				{Label: "🎬 Watch", Url: "https://youtu.be/abc"},
				{Label: "Insecure", Url: "http://insecure.example/x"},
				{Label: "", Url: "https://no-label.example"},
				{Label: "Guide", Url: "https://guide.example/android"},
			}},
	}
	set := settings.New()
	return New(core, tgf, cat, set, []int64{ownerTG}, nil), core, tgf, set
}

// purchase is a service a customer bought (the English customer).
func purchase() events.SubscriptionProvisionedEvent {
	return events.SubscriptionProvisionedEvent{SubscriptionID: "sub-1", OrderID: "order-1", UserID: "u1", TelegramID: adminTG,
		SubscriptionLink: "https://sub.example.com/sub/x", TrafficBytes: 50 << 30}
}

func deliver(t *testing.T, n *Notifier, e events.SubscriptionProvisionedEvent) {
	t.Helper()
	if err := handleEvent(t, n, events.SubscriptionProvisioned, e); err != nil {
		t.Fatalf("the delivery failed (it would be sent again): %v", err)
	}
}

// A delivered service brings, after its link, one direct config: the first of its configs, in a code span to tap and copy, with the characters
// HTML takes escaped. Nothing else is set, so there is no video and no tutorial.
func TestDeliveryBringsOneDirectConfig(t *testing.T) {
	n, core, tgf, _ := bundleNotifier(t)
	deliver(t, n, purchase())
	got := tgf.take()
	if len(got) != 2 {
		t.Fatalf("%d messages: %+v", len(got), got)
	}
	if !strings.Contains(got[0].text, "Your service is ready") {
		t.Errorf("the first message is the ready message: %q", got[0].text)
	}
	if got[1].chat != adminTG || got[1].kb != nil || !strings.Contains(got[1].text, "Direct config") ||
		!strings.Contains(got[1].text, "<code>vless://u@h.example:443?type=tcp&amp;security=reality#first</code>") {
		t.Errorf("the config message: %+v", got[1])
	}
	if strings.Contains(got[1].text, "second") {
		t.Errorf("only the first config is sent: %q", got[1].text)
	}
	if len(core.asked) != 0 || len(tgf.takeCopies()) != 0 {
		t.Error("no tutorial or video was set, none was asked for")
	}

	// In the customer's language.
	p := purchase()
	p.TelegramID = ownerTG
	deliver(t, n, p)
	if got := tgf.take(); len(got) != 2 || !strings.Contains(got[1].text, "کانفیگ مستقیم") {
		t.Errorf("the Persian config message: %+v", got)
	}

	// A service without a config (a panel that gives none) has only its link.
	core.links = &corev1.SubscriptionLinks{}
	deliver(t, n, purchase())
	if got := tgf.take(); len(got) != 1 {
		t.Errorf("no configs: %+v", got)
	}
	core.links = nil // the links could not be read: the ready message goes alone, as ever
	deliver(t, n, purchase())
	if got := tgf.take(); len(got) != 1 {
		t.Errorf("no links: %+v", got)
	}
}

// With the owner's video and tutorial set, the customer gets, in this order: the ready message, the direct config, the video (the owner's
// channel post, copied into the chat) and the tutorial with a button for each https link it has.
func TestDeliveryBringsTheVideoAndTheTutorial(t *testing.T) {
	n, core, tgf, set := bundleNotifier(t)
	set.Replace(map[string]string{"delivery.video_post": "https://t.me/heisen_tutorial/12", "delivery.tutorial": tutorialID})
	deliver(t, n, purchase())

	if order := tgf.takeOrder(); !reflect.DeepEqual(order, []string{"msg", "msg", "copy", "msg"}) {
		t.Fatalf("order: %v (ready message, config, video, tutorial)", order)
	}
	copies := tgf.takeCopies()
	if !reflect.DeepEqual(copies, []copied{{adminTG, "@heisen_tutorial", 12}}) {
		t.Errorf("the video: %+v", copies)
	}
	if len(core.asked) != 1 || core.asked[0].GetId() != tutorialID || core.asked[0].GetUserId() != "u1" || core.asked[0].GetLanguage() != "en" {
		t.Errorf("the tutorial was asked for as %+v", core.asked)
	}
	got := tgf.take()
	tut := got[2]
	if tut.text != "<b>Connect on Android</b>\n\nOpen <b>v2rayNG</b>, tap +." {
		t.Errorf("the tutorial: %q", tut.text)
	}
	if tut.kb == nil || len(tut.kb.InlineKeyboard) != 2 || tut.kb.InlineKeyboard[0][0].URL != "https://youtu.be/abc" ||
		tut.kb.InlineKeyboard[0][0].Text != "🎬 Watch" || tut.kb.InlineKeyboard[1][0].URL != "https://guide.example/android" {
		t.Errorf("only the https links with a label are buttons: %+v", tut.kb)
	}

	// A private channel's post is copied by its id.
	set.Replace(map[string]string{"delivery.video_post": "https://t.me/c/1234567890/45"})
	deliver(t, n, purchase())
	if copies := tgf.takeCopies(); !reflect.DeepEqual(copies, []copied{{adminTG, "-1001234567890", 45}}) {
		t.Errorf("a private post: %+v", copies)
	}
}

// The owner can switch it all off, and the free test has a switch of its own; a reseller never gets it, and a service of a bulk purchase is
// announced in the bulk's one message.
func TestWhoGetsTheBundle(t *testing.T) {
	n, _, tgf, set := bundleNotifier(t)
	set.Replace(map[string]string{"delivery.video_post": "https://t.me/heisen_tutorial/12", "delivery.tutorial": tutorialID})
	count := func(e events.SubscriptionProvisionedEvent) int {
		t.Helper()
		deliver(t, n, e)
		tgf.takeCopies()
		return len(tgf.take())
	}

	// the free test brings it too, unless the owner says it should not
	trial := purchase()
	trial.Trial = true
	if got := count(trial); got != 3 {
		t.Errorf("a free test: %d messages", got)
	}
	set.Replace(map[string]string{"delivery.bundle_trial": "false", "delivery.video_post": "https://t.me/heisen_tutorial/12", "delivery.tutorial": tutorialID})
	if got := count(trial); got != 1 {
		t.Errorf("a free test with the switch off: %d messages", got)
	}
	if got := count(purchase()); got != 3 {
		t.Errorf("a purchase is not held back by the free test's switch: %d messages", got)
	}

	reseller := purchase()
	reseller.Reseller = true
	if got := count(reseller); got != 1 {
		t.Errorf("a reseller: %d messages", got)
	}

	set.Replace(map[string]string{"delivery.bundle": "false", "delivery.video_post": "https://t.me/heisen_tutorial/12", "delivery.tutorial": tutorialID})
	if got := count(purchase()); got != 1 {
		t.Errorf("the owner switched it off: %d messages", got)
	}
	if got := count(trial); got != 1 {
		t.Errorf("switched off, for the free test too: %d messages", got)
	}

	set.Replace(map[string]string{"delivery.bundle": "true"})
	bulk := purchase()
	bulk.BulkID = "bulk-1"
	deliver(t, n, bulk)
	if got := tgf.take(); len(got) != 0 || len(tgf.takeCopies()) != 0 {
		t.Errorf("a service of a bulk: %+v", got)
	}
}

// Every part is best effort: a part that cannot be sent is left out and the others still go; the delivery never fails because of it (the
// event would come back and send the link again).
func TestBundleFailuresAreLeftOut(t *testing.T) {
	n, core, tgf, set := bundleNotifier(t)
	set.Replace(map[string]string{"delivery.video_post": "https://t.me/heisen_tutorial/12", "delivery.tutorial": tutorialID})

	tgf.copyErr = errors.New("Bad Request: chat not found") // the bot cannot read the channel
	deliver(t, n, purchase())
	if got := tgf.take(); len(got) != 3 {
		t.Errorf("without the video the tutorial still goes: %d messages", len(got))
	}
	tgf.copyErr = nil

	for name, err := range map[string]error{
		"tutorial gone":     status.Error(codes.NotFound, "no such entry"),
		"tutorial disabled": status.Error(codes.InvalidArgument, "off"),
		"older core":        status.Error(codes.Unimplemented, "unknown method"),
		"core down":         status.Error(codes.Unavailable, "down"),
	} {
		core.entryErr = err
		deliver(t, n, purchase())
		if got := tgf.take(); len(got) != 2 {
			t.Errorf("%s: %d messages, want the ready message and the config", name, len(got))
		}
		tgf.takeCopies()
	}
	core.entryErr = nil

	// a link that is not a post: no copy, the rest goes
	set.Replace(map[string]string{"delivery.video_post": "https://example.com/video.mp4", "delivery.tutorial": tutorialID})
	deliver(t, n, purchase())
	if got := tgf.take(); len(got) != 3 || len(tgf.takeCopies()) != 0 {
		t.Errorf("a bad video link: %d messages", len(got))
	}

	// a config too long for one message is left out
	set.Replace(nil)
	core.links = &corev1.SubscriptionLinks{ConfigLinks: []string{"vless://" + strings.Repeat("a", 5000)}}
	deliver(t, n, purchase())
	if got := tgf.take(); len(got) != 1 {
		t.Errorf("a config over the limit: %d messages", len(got))
	}
}

// A tutorial too long for a message (a row written by hand: core's checks keep an entry from it) is sent as plain text, shortened.
func TestLongTutorialIsShortened(t *testing.T) {
	n, core, tgf, set := bundleNotifier(t)
	set.Replace(map[string]string{"delivery.tutorial": tutorialID})
	core.entry = &corev1.HelpEntry{Id: tutorialID, Title: "Long", Body: "<b>" + strings.Repeat("word ", 2000) + "</b>"}
	deliver(t, n, purchase())
	got := tgf.take()
	if len(got) != 3 || got[2].kb != nil || strings.Contains(got[2].text, "<b>word") || i18n.TextLength(got[2].text) > i18n.MaxMessage {
		t.Errorf("%d messages, the last of %d characters", len(got), len(got[len(got)-1].text))
	}
}
