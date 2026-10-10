package domain

import (
	"context"

	"github.com/jackc/pgx/v5"
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
