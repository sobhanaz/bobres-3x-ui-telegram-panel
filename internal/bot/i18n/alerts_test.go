package i18n

import (
	"fmt"
	"html"
	"testing"
	"time"
)

// A system alert's values: numbers in the language's digits, a size, a
// date; names (and anything that is not a number) escaped.
func TestAlertArgs(t *testing.T) {
	args := map[string]string{
		"count": "1200", "minutes": "25", "free": fmt.Sprint(1536 << 20),
		"date":    fmt.Sprint(time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC).Unix()),
		"server":  "DE <1>",
		"service": "42", // a name made of digits stays as it is
		"days":    "soon",
	}
	for lang, want := range map[string]string{
		"en": "[count 1,200 date 2 Nov 2026 days soon free 1.5 GB minutes 25 server DE &lt;1&gt; service 42]",
		"fa": "[count ۱٬۲۰۰ date ۱۱ آبان ۱۴۰۵ days soon free ۱٫۵ گیگابایت minutes ۲۵ server DE &lt;1&gt; service 42]",
	} {
		if got := fmt.Sprint(AlertArgs(lang, args, html.EscapeString)); got != want {
			t.Errorf("%s:\n got %s\nwant %s", lang, got, want)
		}
	}
	if got := AlertArgs("en", nil, html.EscapeString); len(got) != 0 {
		t.Errorf("no args: %v", got)
	}
}

// The alert texts are staff texts, filed with the admin alerts in the
// texts editor; the headings take a rendered title.
func TestAlertTextsAreAdminTexts(t *testing.T) {
	c := MustLoad()
	for _, key := range []string{"alert.critical", "alert.server_down", "alert.duration_hours"} {
		info, ok := c.Info(key)
		if !ok || info.Group != "admin" || info.Context != CtxHTML {
			t.Errorf("%s: %+v %v", key, info, ok)
		}
	}
	if _, probs := c.ValidateOverride("en", "alert.critical", "<code>{title}</code>"); len(probs) == 0 {
		t.Error("a title inside <code> was accepted")
	}
}
