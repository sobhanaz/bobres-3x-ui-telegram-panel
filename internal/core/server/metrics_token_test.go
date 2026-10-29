package server

import (
	"context"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
)

// The bot reads every setting except two kinds that are core's alone: the texts of the store's own legal pages and the hash of
// the metrics token. The hash opens nothing by itself, but the token is core's to guard and no other service has a use for it.
func TestSettingsLeaveTheMetricsTokenOutForTheBot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hash := strings.Repeat("ab", 32)
	if _, err := f.st.DB().Exec(ctx, `INSERT INTO core.settings (key, value) VALUES ('monitor.metrics_token', $1::text::jsonb), ('monitor.disk_pct', '"90"')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, `"`+hash+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.st.DB().Exec(context.Background(), `DELETE FROM core.settings WHERE key IN ('monitor.metrics_token', 'monitor.disk_pct')`)
	})
	s, err := f.client(t, botToken).GetSettings(ctx, &corev1.GetSettingsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GetValues()["monitor.metrics_token"]; ok {
		t.Error("the metrics token's hash went to the bot")
	}
	for k, v := range s.GetValues() {
		if strings.Contains(v, hash) {
			t.Errorf("the hash is in %s", k)
		}
	}
	if s.GetValues()["monitor.disk_pct"] != `"90"` {
		t.Errorf("another monitor setting went missing: %v", s.GetValues()["monitor.disk_pct"])
	}
}
