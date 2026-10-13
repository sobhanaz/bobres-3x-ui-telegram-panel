package server

import (
	"bytes"
	"context"
	"strconv"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The bot learns the photo's version with every settings read and fetches
// the photo itself only when that changes.
func TestBotPhotoOverGRPC(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	ctx := context.Background()
	if _, err := f.st.DB().Exec(ctx, `TRUNCATE core.assets`); err != nil {
		t.Fatal(err)
	}
	version := func() string {
		t.Helper()
		s, err := c.GetSettings(ctx, &corev1.GetSettingsRequest{})
		if err != nil {
			t.Fatalf("GetSettings: %v", err)
		}
		return s.GetBotPhotoVersion()
	}

	// None yet: no version, and NotFound.
	if v := version(); v != "" {
		t.Fatalf("version without a photo: %q", v)
	}
	if _, err := c.GetBotPhoto(ctx, &corev1.GetBotPhotoRequest{}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetBotPhoto without a photo: %v", err)
	}
	// A logo is not the bot photo.
	if err := f.st.PutAsset(ctx, f.st.Conn(), "logo", "image/png", []byte("png")); err != nil {
		t.Fatal(err)
	}
	if v := version(); v != "" {
		t.Fatalf("version with only a logo: %q", v)
	}

	jpeg := []byte("\xff\xd8\xff first photo")
	if err := f.st.PutAsset(ctx, f.st.Conn(), store.AssetBotPhoto, "image/jpeg", jpeg); err != nil {
		t.Fatal(err)
	}
	a, err := f.st.AssetInfo(ctx, f.st.Conn(), store.AssetBotPhoto)
	if err != nil {
		t.Fatal(err)
	}
	v1 := version()
	if v1 != strconv.FormatInt(a.UpdatedAt.UnixNano(), 10) {
		t.Fatalf("version %q, uploaded at %v", v1, a.UpdatedAt)
	}
	p, err := c.GetBotPhoto(ctx, &corev1.GetBotPhotoRequest{})
	if err != nil || p.GetVersion() != v1 || !bytes.Equal(p.GetJpeg(), jpeg) {
		t.Fatalf("GetBotPhoto: %v %q %q", err, p.GetVersion(), p.GetJpeg())
	}
	// Another upload is another version.
	if err := f.st.PutAsset(ctx, f.st.Conn(), store.AssetBotPhoto, "image/jpeg", []byte("\xff\xd8\xff second")); err != nil {
		t.Fatal(err)
	}
	if v2 := version(); v2 == v1 || v2 == "" {
		t.Fatalf("version after another upload: %q (was %q)", v2, v1)
	}

	// The photo cannot be read now: "?", so the bot changes nothing, while
	// the settings still arrive.
	if _, err := f.st.DB().Exec(ctx, `ALTER TABLE core.assets RENAME TO assets_away`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.st.DB().Exec(context.Background(), `ALTER TABLE core.assets_away RENAME TO assets`) })
	if v := version(); v != "?" {
		t.Fatalf("version when the photo cannot be read: %q", v)
	}
	if _, err := c.GetBotPhoto(ctx, &corev1.GetBotPhotoRequest{}); status.Code(err) != codes.Internal {
		t.Fatalf("GetBotPhoto when the photo cannot be read: %v", err)
	}
	if _, err := f.st.DB().Exec(ctx, `ALTER TABLE core.assets_away RENAME TO assets`); err != nil {
		t.Fatal(err)
	}

	// Removed: no version again.
	if _, err := f.st.DeleteAsset(ctx, f.st.Conn(), store.AssetBotPhoto); err != nil {
		t.Fatal(err)
	}
	if v := version(); v != "" {
		t.Fatalf("version after the removal: %q", v)
	}
}
