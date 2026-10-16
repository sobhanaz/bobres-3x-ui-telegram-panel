package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/branding"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// SetLogo stores the store's logo (the caller checked the image).
func (s *Service) SetLogo(ctx context.Context, actor *store.User, contentType string, data []byte, width, height int) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.PutAsset(ctx, tx, "logo", contentType, data); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "branding.logo.set", "branding", "logo",
			map[string]any{"type": contentType, "size": len(data), "width": width, "height": height}, "")
	})
}

// RemoveLogo goes back to the default logo.
func (s *Service) RemoveLogo(ctx context.Context, actor *store.User) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		removed, err := s.st.DeleteAsset(ctx, tx, "logo")
		if err != nil || !removed {
			return err
		}
		return s.audit(ctx, tx, actor, "branding.logo.removed", "branding", "logo", nil, "")
	})
}

// SetBotPhoto stores the bot's Telegram profile photo, a square JPEG (the
// caller converted it); the bot sets it in Telegram. The photo already
// stored, byte for byte, is left as it is, not audited and not a new
// version (the bot would upload it again): changed is false then.
func (s *Service) SetBotPhoto(ctx context.Context, actor *store.User, jpeg []byte, width, height int) (changed bool, err error) {
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		cur, err := s.st.GetAsset(ctx, tx, store.AssetBotPhoto)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err == nil && cur.ContentType == "image/jpeg" && bytes.Equal(cur.Data, jpeg) {
			return nil
		}
		if err := s.st.PutAsset(ctx, tx, store.AssetBotPhoto, "image/jpeg", jpeg); err != nil {
			return err
		}
		changed = true
		return s.audit(ctx, tx, actor, "branding.bot_photo.set", "branding", store.AssetBotPhoto,
			map[string]any{"size": len(jpeg), "width": width, "height": height}, "")
	})
	return changed && err == nil, err
}

// RemoveBotPhoto removes the bot photo; the bot then takes off the photo it
// set in Telegram, if that is still its current one.
func (s *Service) RemoveBotPhoto(ctx context.Context, actor *store.User) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		removed, err := s.st.DeleteAsset(ctx, tx, store.AssetBotPhoto)
		if err != nil || !removed {
			return err
		}
		return s.audit(ctx, tx, actor, "branding.bot_photo.removed", "branding", store.AssetBotPhoto, nil, "")
	})
}

// botPhotoDefaultMark records that the default bot photo was offered once.
// Like dailySummaryMark it is internal state with no SettingSpec, so the
// dashboard never lists or edits it.
const botPhotoDefaultMark = "state.bot_photo.default_applied"

// EnsureDefaultBotPhoto gives a new store the BOBRES logo as the bot's
// profile photo, once. It stores the embedded photo only when there is no
// bot photo yet and the marker is unset, and sets the marker in the same
// transaction. After that the operator owns the photo: a replaced photo is
// left alone, and a removed one is not put back (the marker stays set).
// applied is true when the default was stored by this call.
func (s *Service) EnsureDefaultBotPhoto(ctx context.Context) (applied bool, err error) {
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		applied = false
		cur, err := s.st.LockSettings(ctx, tx, []string{botPhotoDefaultMark})
		if err != nil {
			return err
		}
		if _, done := cur[botPhotoDefaultMark]; done {
			return nil
		}
		_, err = s.st.AssetInfo(ctx, tx, store.AssetBotPhoto)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err == nil { // the operator's own photo: leave it, and do not come back
			return s.markBotPhotoDefault(ctx, tx)
		}
		jpeg := branding.DefaultBotPhoto()
		if err := s.st.PutAsset(ctx, tx, store.AssetBotPhoto, "image/jpeg", jpeg); err != nil {
			return err
		}
		if err := s.markBotPhotoDefault(ctx, tx); err != nil {
			return err
		}
		applied = true
		return s.audit(ctx, tx, nil, "branding.bot_photo.default", "branding", store.AssetBotPhoto,
			map[string]any{"size": len(jpeg)}, "")
	})
	return applied && err == nil, err
}

func (s *Service) markBotPhotoDefault(ctx context.Context, tx pgx.Tx) error {
	raw, _ := json.Marshal("1")
	return s.st.SetSetting(ctx, tx, botPhotoDefaultMark, raw)
}
