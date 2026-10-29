package rekey

import (
	"context"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

// The real provisioner store, before and after a rotation: a panel's token reads with the new key alone, and the 3x-ui
// subscription id of a subscription is the one it was (it comes from Tag, which the rotation keeps with a tag key).
func TestAStoreReadsWhatItSavedAfterTheRotation(t *testing.T) {
	p := pool(t)
	ctx := context.Background()
	dsn := testdb.DSN(t)

	old := envelope(t, crypto.Keys{Master: oldMaster})
	st, err := store.New(ctx, dsn, old)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	saved, err := st.AddServer(ctx, &store.Server{Name: "panel-1", BaseURL: "https://panel.example", Token: "secret-token-1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	subID := st.SubIDFor("subscription-1")

	// What `bobres rotate-key` puts in .env: the new key, the old one for reading, the tag key the old one made.
	tag, err := crypto.TagKeyHex([]byte(oldMaster))
	if err != nil {
		t.Fatal(err)
	}
	rotating := envelope(t, crypto.Keys{Master: newMaster, Previous: []string{oldMaster}, Tag: tag})
	if _, err := Run(ctx, p, rotating, Tables["provisioner"], Options{}); err != nil {
		t.Fatal(err)
	}

	// After it, .env holds the new key and the tag key, nothing else.
	after, err := store.New(ctx, dsn, envelope(t, crypto.Keys{Master: newMaster, Tag: tag}))
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	got, err := after.GetServer(ctx, saved.ID)
	if err != nil || got.Token != "secret-token-1" {
		t.Fatalf("the token after the rotation: %v", err)
	}
	if after.SubIDFor("subscription-1") != subID {
		t.Fatal("a 3x-ui subscription id changed with the master key: every client of a panel would be lost")
	}
	// Without the tag key it would have changed: why the rotation sets it.
	if lost, err := store.New(ctx, dsn, envelope(t, crypto.Keys{Master: newMaster})); err != nil {
		t.Fatal(err)
	} else {
		defer lost.Close()
		if lost.SubIDFor("subscription-1") == subID {
			t.Fatal("the id did not depend on the key at all")
		}
	}
	// And the old key alone reads nothing any more.
	stale, err := store.New(ctx, dsn, old)
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	if _, err := stale.GetServer(ctx, saved.ID); err == nil {
		t.Fatal("the old key still reads the token")
	}
}
