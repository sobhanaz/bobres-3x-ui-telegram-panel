package handler

import (
	"context"
	"strings"
	"testing"
)

// A reseller chooses, once, the prefix of the names of their configs from the reseller panel: the shape is asked about at once, a good
// one is confirmed before it is final, core refuses another reseller's, a second try (an old button) shows the one they have, and only
// the owner changes it afterwards.
func TestResellerChoosesTheirConfigPrefixOnce(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	rs := w.customer(1901, "prefixer")
	w.makeReseller(1901, 0, 10, 0)
	w.customer(1902, "taken")
	w.makeReseller(1902, 0, 10, 0)
	ctx := context.Background()

	rs.text("/menu")
	rs.press("rs")
	rs.lastButton("🏷 Config prefix")
	if text := rs.shown().text; strings.Contains(text, "Config prefix:") {
		t.Fatalf("a prefix before one was chosen: %s", text)
	}

	// The question, and a shape the bot refuses at once.
	rs.press("rs:pfx")
	rs.shownHas("Send the prefix")
	for _, bad := range []string{"x", "has-dash", "way too long prefix", "نماینده"} {
		rs.text(bad)
		rs.shownHas("That prefix is not possible")
	}
	// A good one is confirmed first (case is not important).
	rs.text("Sob")
	rs.shownHas("Use <b>sob</b> for all your configs?")
	if !hasButton(rs.shown(), "rs:pfy:sob") {
		t.Fatalf("no confirm button: %v", callbacks(rs.shown()))
	}
	// A shape core refuses (the look of the usual names) is asked again after the confirmation.
	rs.tap("rs:pfy:u12")
	rs.shownHas("That prefix is not possible")
	// Another reseller has it: refused.
	if err := w.dom().SetResellerConfigPrefix(ctx, w.storeUser(1902), w.storeUser(1902).ID, "mine"); err != nil {
		t.Fatal(err)
	}
	rs.text("mine")
	rs.press("rs:pfy:mine")
	rs.shownHas("That prefix is not possible")

	// Confirmed: saved once; the panel shows it; the question is a statement now.
	rs.text("sob")
	rs.press("rs:pfy:sob")
	rs.shownHas("Saved. Your new configs are named <b>sob</b>")
	if n, _ := w.dom().ResellerNaming(ctx, w.storeUser(1901).ID); n.ConfigPrefix != "sob" {
		t.Fatalf("stored prefix %q", n.ConfigPrefix)
	}
	rs.text("/menu")
	rs.press("rs")
	rs.shownHas("Config prefix: <b>sob</b>")
	rs.press("rs:pfx")
	rs.shownHas("Only the owner can change it")
	// An old confirm button changes nothing.
	rs.tap("rs:pfy:other")
	rs.shownHas("Only the owner can change it")
	if n, _ := w.dom().ResellerNaming(ctx, w.storeUser(1901).ID); n.ConfigPrefix != "sob" {
		t.Fatalf("an old button changed the prefix to %q", n.ConfigPrefix)
	}
	// The owner changes it; the reseller sees the new one.
	if err := w.dom().SetResellerConfigPrefix(ctx, w.storeUser(ownerID), w.storeUser(1901).ID, "newone"); err != nil {
		t.Fatal(err)
	}
	rs.press("rs")
	rs.shownHas("Config prefix: <b>newone</b>")

	// Someone who is not a reseller has none of it.
	cust := w.customer(1903, "plain")
	cust.text("/menu")
	cust.tap("rs:pfx")
	cust.toast("reseller")
}
