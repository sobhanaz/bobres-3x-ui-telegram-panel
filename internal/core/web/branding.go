package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	_ "image/jpeg" // logo sizes
	_ "image/png"
	"io"
	"net/http"
	"strconv"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Logo limits: small enough for a sidebar and a login page.
const (
	maxLogo     = 512 << 10
	minLogoSide = 16
	maxLogoSide = 2048
	logoAsset   = "logo"
	// defaultColor is the dashboard's own teal.
	defaultColor = "#14b8a6"
	defaultBrand = "BOBRES"
)

// brandingKeys maps the Branding page's fields to settings.
var brandingKeys = map[string]string{
	"name": "branding.name", "support": "branding.support", "color": "branding.color",
	"terms_url": "branding.terms_url", "privacy_url": "branding.privacy_url",
	"currency.fa": "branding.currency.fa", "currency.en": "branding.currency.en",
}

type langPair struct {
	FA string `json:"fa"`
	EN string `json:"en"`
}

// logoURL is where the logo is served, with its version so a browser may
// keep it; "" when there is none.
func (s *Server) logoURL(ctx context.Context) string {
	a, err := s.cfg.Store.AssetInfo(ctx, s.cfg.Store.Conn(), logoAsset)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.cfg.Log.Warn("logo info", "err", err)
		}
		return ""
	}
	return "/api/v1/brand/logo?v=" + strconv.FormatInt(a.UpdatedAt.Unix(), 10)
}

// getBranding: GET /api/v1/branding, the Branding page's store details.
func (s *Server) getBranding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v, err := s.cfg.Domain.Settings(ctx)
	if err != nil {
		s.fail(w, "branding", err)
		return
	}
	var logo any
	if a, err := s.cfg.Store.AssetInfo(ctx, s.cfg.Store.Conn(), logoAsset); err == nil {
		logo = map[string]any{"url": "/api/v1/brand/logo?v=" + strconv.FormatInt(a.UpdatedAt.Unix(), 10),
			"type": a.ContentType, "size": a.Size, "updated_at": a.UpdatedAt.Unix()}
	} else if !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "logo", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": v["branding.name"], "support": v["branding.support"], "color": v["branding.color"],
		"terms_url": v["branding.terms_url"], "privacy_url": v["branding.privacy_url"],
		"currency": langPair{v["branding.currency.fa"], v["branding.currency.en"]},
		"logo":     logo,
		"defaults": map[string]any{"name": defaultBrand, "color": defaultColor, "currency": langPair{"تومان", "Toman"}},
	})
}

// putBranding: PUT /api/v1/branding, every store detail at once.
func (s *Server) putBranding(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string   `json:"name"`
		Support    string   `json:"support"`
		Color      string   `json:"color"`
		TermsURL   string   `json:"terms_url"`
		PrivacyURL string   `json:"privacy_url"`
		Currency   langPair `json:"currency"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	values := map[string]string{
		brandingKeys["name"]: req.Name, brandingKeys["support"]: req.Support, brandingKeys["color"]: req.Color,
		brandingKeys["terms_url"]: req.TermsURL, brandingKeys["privacy_url"]: req.PrivacyURL,
		brandingKeys["currency.fa"]: req.Currency.FA, brandingKeys["currency.en"]: req.Currency.EN,
	}
	if _, err := s.cfg.Domain.SetSettings(r.Context(), actor(r), values); err != nil {
		var fe *domain.FieldError
		if errors.As(err, &fe) {
			// Name the page's field, not the setting.
			for field, key := range brandingKeys {
				if key == fe.Field {
					writeFieldError(w, field, fe.Msg)
					return
				}
			}
		}
		s.fail(w, "save branding", err)
		return
	}
	s.refreshBot()
	s.getBranding(w, r)
}

// putLogo: PUT /api/v1/branding/logo with the image as the body.
func (s *Server) putLogo(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxLogo+1))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) || len(data) > maxLogo {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "the logo may be at most 512 KB")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "could not read the image")
		return
	}
	ctype, wd, ht, ok := imageInfo(data)
	if !ok {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported", "the logo must be a PNG, JPEG or WebP image")
		return
	}
	if wd < minLogoSide || ht < minLogoSide || wd > maxLogoSide || ht > maxLogoSide {
		writeError(w, http.StatusBadRequest, "invalid", "the logo must be 16 to 2048 pixels on each side")
		return
	}
	if err := s.cfg.Domain.SetLogo(r.Context(), actor(r), ctype, data, wd, ht); err != nil {
		s.fail(w, "save logo", err)
		return
	}
	s.getBranding(w, r)
}

// deleteLogo: DELETE /api/v1/branding/logo.
func (s *Server) deleteLogo(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Domain.RemoveLogo(r.Context(), actor(r)); err != nil {
		s.fail(w, "remove logo", err)
		return
	}
	s.getBranding(w, r)
}

// publicBrand: GET /api/v1/brand, what the login page shows before anyone
// logs in (name, colour, logo; nothing private).
func (s *Server) publicBrand(w http.ResponseWriter, r *http.Request) {
	v, err := s.cfg.Domain.Settings(r.Context())
	if err != nil {
		s.fail(w, "brand", err)
		return
	}
	var logo any
	if u := s.logoURL(r.Context()); u != "" {
		logo = u
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": brandName(v), "color": v["branding.color"], "logo": logo})
}

// logo: GET /api/v1/brand/logo, the image itself.
func (s *Server) logo(w http.ResponseWriter, r *http.Request) {
	a, err := s.cfg.Store.GetAsset(r.Context(), s.cfg.Store.Conn(), logoAsset)
	if err != nil {
		s.fail(w, "logo", err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(a.Data)))
	// The address changes with every upload (?v=), so it may be kept.
	h.Set("Cache-Control", "public, max-age=86400")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Disposition", "inline")
	_, _ = w.Write(a.Data)
}

func brandName(settings map[string]string) string {
	if n := settings["branding.name"]; n != "" {
		return n
	}
	return defaultBrand
}

// imageInfo recognises a PNG, JPEG or WebP image and reads its size.
func imageInfo(data []byte) (ctype string, w, h int, ok bool) {
	switch ctype = http.DetectContentType(data); ctype {
	case "image/png", "image/jpeg":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return "", 0, 0, false
		}
		return ctype, cfg.Width, cfg.Height, true
	case "image/webp":
		w, h, ok = webpSize(data)
		return ctype, w, h, ok
	}
	return "", 0, 0, false
}

// webpSize reads a WebP image's size from its first chunk (lossy VP8,
// lossless VP8L or extended VP8X), without decoding it.
func webpSize(b []byte) (w, h int, ok bool) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, false
	}
	switch string(b[12:16]) {
	case "VP8 ":
		if b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, false
		}
		return int(binary.LittleEndian.Uint16(b[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(b[28:30]) & 0x3fff), true
	case "VP8L":
		if b[20] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(b[21:25])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, true
	case "VP8X":
		w = int(b[24]) | int(b[25])<<8 | int(b[26])<<16
		h = int(b[27]) | int(b[28])<<8 | int(b[29])<<16
		return w + 1, h + 1, true
	}
	return 0, 0, false
}
