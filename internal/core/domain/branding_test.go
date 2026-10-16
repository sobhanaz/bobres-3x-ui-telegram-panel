package domain

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/branding"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

func botPhoto(t *testing.T, s *store.Store) *store.Asset {
	t.Helper()
	a, err := s.GetAsset(context.Background(), s.Conn(), store.AssetBotPhoto)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// The first start stores the BOBRES logo; the second changes nothing.
func TestDefaultBotPhotoIsAppliedOnce(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	applied, err := svc.EnsureDefaultBotPhoto(ctx)
	if err != nil || !applied {
		t.Fatalf("first start: applied=%v err=%v", applied, err)
	}
	a := botPhoto(t, s)
	if a == nil || a.ContentType != "image/jpeg" || !bytes.Equal(a.Data, branding.DefaultBotPhoto()) {
		t.Fatalf("default photo not stored: %+v", a)
	}
	first := a.UpdatedAt
	applied, err = svc.EnsureDefaultBotPhoto(ctx)
	if err != nil || applied {
		t.Fatalf("second start: applied=%v err=%v", applied, err)
	}
	if !botPhoto(t, s).UpdatedAt.Equal(first) {
		t.Fatal("second start rewrote the photo")
	}
}

// A photo the operator removed is not put back at the next start.
func TestDefaultBotPhotoIsNotRestoredAfterRemoval(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	if _, err := svc.EnsureDefaultBotPhoto(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveBotPhoto(ctx, nil); err != nil {
		t.Fatal(err)
	}
	applied, err := svc.EnsureDefaultBotPhoto(ctx)
	if err != nil || applied || botPhoto(t, s) != nil {
		t.Fatalf("restored after removal: applied=%v err=%v", applied, err)
	}
}

// An operator's own photo is never replaced, and the default does not come
// back if they remove theirs later.
func TestDefaultBotPhotoKeepsTheOperatorsPhoto(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := []byte("\xff\xd8operator photo\xff\xd9")
	if err := s.PutAsset(ctx, s.Conn(), store.AssetBotPhoto, "image/jpeg", own); err != nil {
		t.Fatal(err)
	}
	applied, err := svc.EnsureDefaultBotPhoto(ctx)
	if err != nil || applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if a := botPhoto(t, s); a == nil || !bytes.Equal(a.Data, own) {
		t.Fatal("operator photo was replaced")
	}
	if err := svc.RemoveBotPhoto(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if applied, _ := svc.EnsureDefaultBotPhoto(ctx); applied || botPhoto(t, s) != nil {
		t.Fatal("default came back after the operator removed theirs")
	}
}

// The marker is internal state: it is no setting spec, so no settings list
// (dashboard or bot) shows or accepts it.
func TestDefaultBotPhotoMarkerIsNotASetting(t *testing.T) {
	svc, _ := testService(t)
	if _, err := svc.EnsureDefaultBotPhoto(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := SpecOf(botPhotoDefaultMark); ok {
		t.Fatal("marker has a SettingSpec")
	}
	for _, sp := range SettingSpecs {
		if sp.Key == botPhotoDefaultMark {
			t.Fatal("marker is listed in SettingSpecs")
		}
	}
	if _, err := svc.SetSettings(context.Background(), nil, map[string]string{botPhotoDefaultMark: "x"}); err == nil {
		t.Fatal("marker is editable through SetSettings")
	}
}
