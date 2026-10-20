package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Update channels. Stable follows the latest release that is not a
// pre-release (GitHub's releases/latest/download, as install.sh); beta the
// highest version published, pre-releases included (stable releases too,
// when they are the highest).
const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

// APIURL lists this repository's releases, newest first (GitHub's REST API;
// 60 requests an hour per address without a token).
const APIURL = "https://api.github.com/repos/sobhanaz/bobres-3x-ui-telegram-panel/releases"

// SettingChannel is the store setting (core's settings table, read by the bot
// with its other settings) that picks the channel from the dashboard; unset,
// BOBRES_UPDATE_CHANNEL decides (EffectiveChannel).
const SettingChannel = "updates.channel"

// ErrNotFound marks a release, a release file or a release list that is not
// published.
var ErrNotFound = errors.New("not found")

// ParseChannel reads a channel setting (BOBRES_UPDATE_CHANNEL, --channel):
// "" is stable, and anything but stable or beta is an error.
func ParseChannel(s string) (string, error) {
	switch c := strings.ToLower(strings.TrimSpace(s)); c {
	case "", ChannelStable:
		return ChannelStable, nil
	case ChannelBeta:
		return ChannelBeta, nil
	}
	return "", fmt.Errorf("unknown update channel %q: it is stable or beta", s)
}

// EffectiveChannel is the channel a store follows: the dashboard's setting
// (updates.channel) when it names one, else fallback (BOBRES_UPDATE_CHANNEL as
// the bot read it; "" is stable). A setting that is not stable or beta is
// ignored, never guessed at.
func EffectiveChannel(setting, fallback string) string {
	if s := strings.TrimSpace(setting); s != "" {
		if c, err := ParseChannel(s); err == nil {
			return c
		}
	}
	c, err := ParseChannel(fallback)
	if err != nil {
		return ChannelStable
	}
	return c
}

// releaseNumberRe is a version as release tags are made: X.Y.Z with an
// optional -label.N, nothing that could change a web address.
var releaseNumberRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// ValidNumber reports whether v is exactly a release number, X.Y.Z or
// X.Y.Z-label.N (no leading v, no build metadata, at most 64 characters): the
// only form that is put into a request for an update or a web address.
func ValidNumber(v string) bool { return len(v) <= 64 && releaseNumberRe.MatchString(v) }

// NotesURL is where a release's notes are on GitHub: its release page ("" for
// anything that is not a release number).
func NotesURL(version string) string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if !ValidNumber(v) {
		return ""
	}
	return releasesURL + "/tag/v" + v
}

// ChannelAllows reports whether an install following channel takes a
// release whose signed header names released: beta takes any, stable never
// one signed for beta (release.yml signs every pre-release tag so).
func ChannelAllows(channel, released string) bool {
	return channel == ChannelBeta || released != ChannelBeta
}

// Covered reports whether a license whose updates end on updatesUntil
// (YYYY-MM-DD, "" for no end) covers a release signed at issued (its
// header's time): it does when the release was issued on or before that
// day. A release that does not say when it was issued, or an end that is not
// a date, is covered: what cannot be read never refuses an update.
func Covered(updatesUntil, issued string) bool {
	until, err := time.Parse(time.DateOnly, strings.TrimSpace(updatesUntil))
	if err != nil {
		return true
	}
	day := IssuedDay(issued)
	return day == "" || day <= until.Format(time.DateOnly)
}

// IssuedDay is the day (YYYY-MM-DD, UTC) of a header's issued time, "" when
// it has none or it does not parse.
func IssuedDay(issued string) string {
	at, err := time.Parse(timeLayout, strings.TrimSpace(issued))
	if err != nil {
		return ""
	}
	return at.UTC().Format(time.DateOnly)
}

// Source is where the releases are published.
type Source struct {
	// Latest is the latest stable release's files (DefaultBaseURL).
	Latest string
	// API lists every release, pre-releases included (APIURL).
	API string
	// Download is where one release's files are (DownloadURL).
	Download func(version string) string
	// HC makes the API call; it should bound it in time.
	HC *http.Client
}

// DefaultSource is this repository's releases on GitHub.
func DefaultSource(hc *http.Client) Source {
	return Source{Latest: DefaultBaseURL, API: APIURL, Download: DownloadURL, HC: hc}
}

// maxReleaseList bounds the API's answer (30 releases with their notes and
// asset lists).
const maxReleaseList = 4 << 20

// Resolve finds the newest release of channel: where its files are, and the
// version they must be signed for. On stable that is latest/download and ""
// (that address names no version; the header says which). On beta it is the
// highest version the API lists and that version: the API's answer is not
// signed, so the files must name the same version or they are refused. An
// error wrapping ErrNotFound means nothing is published.
func (s Source) Resolve(ctx context.Context, channel string) (base, want string, err error) {
	if channel != ChannelBeta {
		return s.Latest, "", nil
	}
	v, err := s.highest(ctx)
	if err != nil {
		return "", "", err
	}
	return s.Download(v), v, nil
}

// highest is the highest version among the published releases the API
// lists first (drafts and tags that are not vX.Y.Z[-pre] left out).
func (s Source) highest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.API+"?per_page=30", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	hc := s.HC
	if hc == nil {
		hc = &http.Client{Timeout: time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("list the releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("list the releases: %w", ErrNotFound)
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return "", fmt.Errorf("list the releases: GitHub's API answered %d (its limit is 60 requests an hour per address)", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("list the releases: GitHub's API answered %d", resp.StatusCode)
	}
	var list []struct {
		Tag   string `json:"tag_name"`
		Draft bool   `json:"draft"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseList)).Decode(&list); err != nil {
		return "", fmt.Errorf("list the releases: %w", err)
	}
	best := ""
	for _, r := range list {
		v, ok := strings.CutPrefix(r.Tag, "v")
		if r.Draft || !ok || !Valid(v) || strings.HasPrefix(v, "v") || strings.ContainsAny(v, "+ \t") {
			continue
		}
		if best == "" || Compare(v, best) > 0 {
			best = v
		}
	}
	if best == "" {
		return "", fmt.Errorf("list the releases: %w", ErrNotFound)
	}
	return best, nil
}
