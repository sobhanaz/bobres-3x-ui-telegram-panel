package domain

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// A reseller application is a ticket of its own in the reseller category: the
// ticket alert goes out as for any new ticket, a customer has one open
// application, it does not join (nor start beside) another open conversation,
// and only a customer may make one, while the license in effect includes
// resellers.
func TestResellerApplication(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	c := seedUser(t, s, 7301)
	staff := staffMember(t, s, 7302, "support")

	first, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Category: CategoryReseller,
		Text: "I run a shop in Tehran\nand want to resell", TGMessageID: 10})
	if err != nil || !first.NewTicket || !first.Created || first.Ticket.Category != CategoryReseller || first.Ticket.Subject != "I run a shop in Tehran" {
		t.Fatalf("the application: %+v %v", first, err)
	}
	evs := outboxEvents(t, s, events.TicketOpened)
	if len(evs) != 1 || !strings.Contains(string(evs[0]), `"category": "reseller"`) {
		t.Fatalf("the ticket alert: %s", evs)
	}

	// The same Telegram message again is the same application; another one
	// while this is open is refused, saying so.
	if again, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Category: CategoryReseller, Text: "x", TGMessageID: 10}); err != nil || again.Created || again.Ticket.ID != first.Ticket.ID {
		t.Fatalf("a repeat: %+v %v", again, err)
	}
	_, err = svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Category: CategoryReseller, Text: "once more", TGMessageID: 11})
	if !errors.Is(err, ErrState) || !strings.Contains(err.Error(), ApplicationOpen) {
		t.Fatalf("a second application while one is open: %v", err)
	}
	// What the customer writes to support afterwards joins the application's
	// ticket (one open conversation), whatever the topic.
	more, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Category: "payment", Text: "one more detail", TGMessageID: 12})
	if err != nil || more.NewTicket || more.Ticket.ID != first.Ticket.ID || more.Ticket.Category != CategoryReseller {
		t.Fatalf("a message after the application: %+v %v", more, err)
	}
	if n := len(outboxEvents(t, s, events.TicketOpened)); n != 1 {
		t.Fatalf("opened events: %d", n)
	}

	// Staff answer, close it: the customer may apply again (a new ticket).
	if _, _, err := svc.ReplyTicket(ctx, staff, first.Ticket.ID, "Please send your shop's address", "reply", false, "k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetTicketStatus(ctx, staff, first.Ticket.ID, "closed", ""); err != nil {
		t.Fatal(err)
	}
	second, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Category: CategoryReseller, Text: "here it is again", TGMessageID: 13})
	if err != nil || !second.NewTicket || second.Ticket.ID == first.Ticket.ID || second.Ticket.Category != CategoryReseller {
		t.Fatalf("an application after the first was closed: %+v %v", second, err)
	}
	if n := len(outboxEvents(t, s, events.TicketOpened)); n != 2 {
		t.Fatalf("opened events: %d", n)
	}

	// An application is a conversation of its own: never an answer in another.
	if _, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Category: CategoryReseller, TicketID: second.Ticket.ID, Text: "x", TGMessageID: 14}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an application naming a ticket: %v", err)
	}
	if _, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Category: CategoryReseller, ReplyToTGID: 555, Text: "x", TGMessageID: 15}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an application answering a staff reply: %v", err)
	}
}

// A customer with another conversation open cannot start an application beside
// it (a customer has one open ticket at a time) and it does not join that one:
// the open ticket stays as it was.
func TestResellerApplicationNeedsNoOtherOpenTicket(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	c := seedUser(t, s, 7311)
	open, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Category: "payment", Text: "my payment is missing", TGMessageID: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Category: CategoryReseller, Text: "I want to resell", TGMessageID: 2})
	if !errors.Is(err, ErrState) || !strings.Contains(err.Error(), ConversationOpen) || strings.Contains(err.Error(), ApplicationOpen) {
		t.Fatalf("an application beside an open conversation: %v", err)
	}
	if msgs, err := s.TicketMessages(ctx, s.Conn(), open.Ticket.ID, true, 0); err != nil || len(msgs) != 1 {
		t.Fatalf("the open ticket was changed: %d messages, %v", len(msgs), err)
	}
	if t2, err := s.OpenTicketOf(ctx, s.Conn(), c.ID); err != nil || t2.ID != open.Ticket.ID || t2.Category != "payment" {
		t.Fatalf("the open ticket: %+v %v", t2, err)
	}
}

// Only an active customer applies: not a reseller, not staff, not a banned
// user; and only while the license in effect includes resellers (the free tier
// does not). Core refuses, whatever the bot showed.
func TestResellerApplicationWho(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	apply := func(u *store.User, n int64) error {
		_, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: u.ID, Category: CategoryReseller, Text: "please", TGMessageID: n})
		return err
	}
	for i, role := range []string{"reseller", "support", "admin", "owner"} {
		u := staffMember(t, s, 7320+int64(i), role)
		if err := apply(u, 1); !errors.Is(err, ErrState) || !strings.Contains(err.Error(), "only a customer") {
			t.Errorf("a %s applying: %v", role, err)
		}
	}
	banned := seedUser(t, s, 7330)
	if err := s.SetUserStatus(ctx, s.Conn(), banned.ID, "banned"); err != nil {
		t.Fatal(err)
	}
	if err := apply(banned, 1); !errors.Is(err, ErrUserBanned) {
		t.Errorf("a banned customer applying: %v", err)
	}
	// A banned customer may still write to support, to appeal.
	if _, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: banned.ID, Category: "other", Text: "unban me", TGMessageID: 2}); err != nil {
		t.Errorf("a banned customer writing to support: %v", err)
	}

	// The free tier has no resellers: the refusal is the license's, and no
	// ticket is made.
	if _, err := s.DB().Exec(ctx, `UPDATE core.install SET trial_started_at = now() - interval '31 days'`); err != nil {
		t.Fatal(err)
	}
	c := seedUser(t, s, 7331)
	err := apply(c, 3)
	var refusal *LicenseRefusal
	if !errors.As(err, &refusal) || refusal.Gate != "resellers" {
		t.Fatalf("an application under the free tier: %v", err)
	}
	if _, err := s.OpenTicketOf(ctx, s.Conn(), c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a ticket was made under the free tier: %v", err)
	}
}

// The categories: a reseller application is one a ticket can have, not one a
// customer picks when they write to support.
func TestTicketCategoryLists(t *testing.T) {
	if !slices.Contains(TicketCategories, CategoryReseller) || slices.Contains(SupportCategories, CategoryReseller) {
		t.Fatalf("categories: %v, topics to pick: %v", TicketCategories, SupportCategories)
	}
	for _, c := range SupportCategories {
		if !slices.Contains(TicketCategories, c) {
			t.Errorf("%q can be picked but is not a category", c)
		}
	}
}
