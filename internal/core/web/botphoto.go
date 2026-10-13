package web

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"net/http"
	"strconv"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Bot photo limits. The upload may be as large as a logo (maxLogo) and at
// most maxLogoSide pixels on a side, which bounds what one decoding takes
// (2048x2048 at 8 bytes a pixel is 32 MiB, plus the square it is drawn on;
// one conversion runs at a time). Telegram refuses small profile photos.
// The JPEG goes to the bot in one gRPC message (4 MiB at most): a picture
// that makes a larger one (1-bit noise does) is refused.
const (
	minBotPhotoSide = 160
	maxBotPhotoJPEG = 3 << 20
	botPhotoQuality = 90
)

// botPhotoURL is where the Branding page reads the stored photo, with its
// version so the browser may keep it.
func botPhotoURL(a *store.Asset) string {
	return "/api/v1/branding/bot-photo?v=" + strconv.FormatInt(a.UpdatedAt.UnixNano(), 10)
}

// putBotPhoto: PUT /api/v1/branding/bot-photo with a PNG or JPEG as the
// body. It is stored as the square JPEG Telegram takes as a profile photo,
// and the bot is told at once. The same picture again (the same JPEG comes
// out of the conversion) changes nothing: the bot would only add one more
// photo to Telegram's history of its photos.
func (s *Server) putBotPhoto(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxLogo+1))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) || len(data) > maxLogo {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "the photo may be at most 512 KB")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "could not read the image")
		return
	}
	switch http.DetectContentType(data) {
	case "image/png", "image/jpeg":
	case "image/webp":
		writeError(w, http.StatusUnsupportedMediaType, "unsupported", "a WebP image cannot be converted for Telegram; upload a PNG or JPEG")
		return
	default:
		writeError(w, http.StatusUnsupportedMediaType, "unsupported", "the photo must be a PNG or JPEG image")
		return
	}
	// The size from the header first: nothing is decoded for a picture of
	// the wrong size.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported", "the photo must be a PNG or JPEG image")
		return
	}
	if cfg.Width < minBotPhotoSide || cfg.Height < minBotPhotoSide || cfg.Width > maxLogoSide || cfg.Height > maxLogoSide {
		writeError(w, http.StatusBadRequest, "invalid", "the photo must be 160 to 2048 pixels on each side")
		return
	}
	photo, side, err := s.convertBotPhoto(r.Context(), data)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return // the browser went away while it waited for its turn
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "could not read the image")
		return
	}
	if len(photo) > maxBotPhotoJPEG {
		writeError(w, http.StatusBadRequest, "invalid", "this picture makes a JPEG larger than 3 MB; use a simpler or smaller picture")
		return
	}
	changed, err := s.cfg.Domain.SetBotPhoto(r.Context(), actor(r), photo, side, side)
	if err != nil {
		s.fail(w, "save bot photo", err)
		return
	}
	if changed {
		s.refreshBot()
	}
	s.getBranding(w, r)
}

// convertBotPhoto runs botPhotoJPEG when no other conversion is running.
func (s *Server) convertBotPhoto(ctx context.Context, data []byte) ([]byte, int, error) {
	select {
	case s.photoSlot <- struct{}{}:
		defer func() { <-s.photoSlot }()
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
	return botPhotoJPEG(data)
}

// botPhotoJPEG decodes a PNG or JPEG and encodes the bot photo: a square
// JPEG, a picture that is not square centred on white, any transparency
// flattened onto white. A JPEG is first turned upright as its EXIF
// orientation says (a phone's picture is stored sideways). side is the
// square's side in pixels.
func botPhotoJPEG(data []byte) (photo []byte, side int, err error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	if format == "jpeg" {
		img = orient(img, jpegOrientation(data))
	}
	b := img.Bounds()
	side = max(b.Dx(), b.Dy())
	sq := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.Draw(sq, sq.Bounds(), image.White, image.Point{}, draw.Src)
	at := image.Pt((side-b.Dx())/2, (side-b.Dy())/2)
	draw.Draw(sq, image.Rectangle{Min: at, Max: at.Add(b.Size())}, img, b.Min, draw.Over)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, sq, &jpeg.Options{Quality: botPhotoQuality}); err != nil {
		return nil, 0, err
	}
	return buf.Bytes(), side, nil
}

// deleteBotPhoto: DELETE /api/v1/branding/bot-photo.
func (s *Server) deleteBotPhoto(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Domain.RemoveBotPhoto(r.Context(), actor(r)); err != nil {
		s.fail(w, "remove bot photo", err)
		return
	}
	s.refreshBot()
	s.getBranding(w, r)
}

// botPhoto: GET /api/v1/branding/bot-photo, the stored photo, for the
// Branding page's preview. Unlike the logo it is not public.
func (s *Server) botPhoto(w http.ResponseWriter, r *http.Request) {
	a, err := s.cfg.Store.GetAsset(r.Context(), s.cfg.Store.Conn(), store.AssetBotPhoto)
	if err != nil {
		s.fail(w, "bot photo", err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(a.Data)))
	// The address changes with every upload (?v=); only this browser keeps it.
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Disposition", "inline")
	_, _ = w.Write(a.Data)
}
