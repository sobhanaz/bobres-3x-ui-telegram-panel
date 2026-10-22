package i18n

import (
	"slices"
	"strings"
	"testing"
)

// Every built-in text must pass the checks its overrides get, or the
// contexts and limits are wrong.
func TestDefaultsPassValidation(t *testing.T) {
	c := MustLoad()
	for _, info := range c.Infos() {
		for _, lang := range info.Langs {
			def := c.Default(lang, info.Key)
			got, probs := c.ValidateOverride(lang, info.Key, def)
			if len(probs) > 0 {
				t.Errorf("%s/%s: %v", lang, info.Key, probs)
			}
			if got != NormalizeText(def) {
				t.Errorf("%s/%s: normalised to %q", lang, info.Key, got)
			}
		}
	}
}

func TestContexts(t *testing.T) {
	cases := map[string]Context{
		"btn.buy": CtxButton, "sub.status.active": CtxButtonHTML, "error.rate": CtxToast,
		"discount.bad.expired": CtxToastHTML, "pay.stars_title": CtxInvoiceTitle,
		"sub.qr_caption": CtxCaption, "welcome": CtxHTML, "subpage.guide.hiddify": CtxPlain, "btn.open_page": CtxButton,
		"rs.forbidden": CtxToastHTML, "rs.label_removed": CtxToast, "wallet.debt": CtxButtonHTML, "btn.rs_buy": CtxButton,
		"rs.panel": CtxHTML, "menu.placeholder": CtxPlaceholder, "lang.changed": CtxToastHTML,
	}
	for k, want := range cases {
		if got := ContextOf(k); got != want {
			t.Errorf("%s: %s, want %s", k, got, want)
		}
	}
	c := MustLoad()
	info, ok := c.Info("lang.prompt")
	if !ok || !slices.Equal(info.Langs, []string{"en"}) {
		t.Fatalf("lang.prompt is English only: %+v", info)
	}
	if _, probs := c.ValidateOverride("fa", "lang.prompt", "x"); len(probs) != 1 || probs[0].Code != ProblemLangUnused {
		t.Fatalf("fa lang.prompt: %v", probs)
	}
	if _, probs := c.ValidateOverride("en", "no.such", "x"); len(probs) != 1 || probs[0].Code != ProblemUnknownKey {
		t.Fatalf("unknown key: %v", probs)
	}
	if w, _ := c.Info("welcome"); !slices.Equal(w.Placeholders, []string{"brand"}) || w.Group != "general" {
		t.Fatalf("welcome info: %+v", w)
	}
	// The reseller panel's texts have their own heading in the editor.
	if p, _ := c.Info("rs.panel"); p.Group != "rs" || !slices.Equal(p.Placeholders, []string{"balances", "commission", "customers", "discount", "earned", "tier"}) {
		t.Fatalf("rs.panel info: %+v", p)
	}
}

// The bottom menu's hint is one line of plain text, at most 64 characters:
// Telegram's limit for the message field's placeholder.
func TestPlaceholderContext(t *testing.T) {
	c := MustLoad()
	if info, _ := c.Info("menu.placeholder"); info.Context != CtxPlaceholder || info.Max != 64 || info.Group != "general" {
		t.Fatalf("menu.placeholder: %+v", info)
	}
	for v, code := range map[string]string{
		"<b>Choose</b>":         ProblemMarkup,
		"Choose\nfrom the menu": ProblemControlChar,
		strings.Repeat("x", 65): ProblemTooLong,
	} {
		_, probs := c.ValidateOverride("en", "menu.placeholder", v)
		if !slices.ContainsFunc(probs, func(p Problem) bool { return p.Code == code }) {
			t.Errorf("%q: %v, want %s", v, probs, code)
		}
	}
	if _, probs := c.ValidateOverride("fa", "menu.placeholder", "یکی از دکمه\u200cها را بزنید"); len(probs) != 0 {
		t.Fatalf("a good hint: %v", probs)
	}
}

// Every colored button is a text of the catalog, in a color Telegram knows.
func TestButtonStyles(t *testing.T) {
	c := MustLoad()
	for key, style := range buttonStyles {
		if _, ok := c.Info(key); !ok {
			t.Errorf("%s: no such text", key)
		}
		if style != "primary" && style != "success" && style != "danger" {
			t.Errorf("%s: style %q", key, style)
		}
	}
	for key, want := range map[string]string{"btn.buy": "success", "btn.wallet": "primary", "btn.cancel": "danger", "btn.home": ""} {
		if got := ButtonStyle(key); got != want {
			t.Errorf("%s: %q, want %q", key, got, want)
		}
	}
}

func TestValidateOverride(t *testing.T) {
	c := MustLoad()
	codes := func(lang, key, v string) []string {
		_, probs := c.ValidateOverride(lang, key, v)
		var out []string
		for _, p := range probs {
			out = append(out, p.Code+":"+p.Arg)
		}
		return out
	}
	ok := []struct{ key, v string }{
		{"welcome", "سلام به <b>{brand}</b>!\r\nخوش آمدید"},
		{"welcome", `<a href="https://t.me/x">{brand}</a> &amp; &lt;3 &#128512;`},
		{"welcome", `<blockquote expandable>{brand}</blockquote><span class="tg-spoiler">x</span>`},
		{"welcome", "<pre><code>{brand}</code></pre>"},
		{"sub.ready", "<code>{link}</code> {expires} {traffic}"},
		{"btn.buy", "🛒 خرید"},
		{"welcome", ""}, // reset
	}
	for _, tc := range ok {
		if got := codes("fa", tc.key, tc.v); len(got) != 0 {
			t.Errorf("%s %q: %v", tc.key, tc.v, got)
		}
	}
	bad := []struct {
		key, v string
		want   string
	}{
		{"welcome", "hello", "placeholder_missing:brand"},
		{"welcome", "{brand} {name}", "placeholder_unknown:name"},
		{"welcome", "{brand} { brand }", "placeholder_unknown:{ brand }"},
		{"welcome", "<b>{brand}", "tag_unclosed:b"},
		{"welcome", "<b>{brand}</i>", "tag_mismatch:i"},
		{"welcome", "<b><i>{brand}</b></i>", "tag_mismatch:b"},
		{"welcome", "<script>{brand}</script>", "tag_not_allowed:script"},
		{"welcome", "{brand}<br>", "tag_not_allowed:br"},
		{"welcome", "<code><b>{brand}</b></code>", "tag_in_code:b"},
		{"welcome", `<a href="javascript:x">{brand}</a>`, "bad_link:javascript:x"},
		{"welcome", `<a href="http://x.ir">{brand}</a>`, "bad_link:http://x.ir"},
		{"welcome", `<b onclick="x">{brand}</b>`, "bad_attr:b"},
		{"welcome", "{brand} 1 < 2", "bad_entity:<"},
		{"welcome", "{brand} a & b", "bad_entity:&"},
		{"welcome", "{brand} &nbsp;", "bad_entity:&"},
		{"welcome", "{brand}\x00", `control_char:'\x00'`},
		{"btn.buy", "<b>Buy</b>", "markup_not_allowed:<"},
		{"btn.buy", "Buy & sell", "markup_not_allowed:&"},
		{"btn.buy", strings.Repeat("x", 65), "too_long:64"},
		{"error.rate", strings.Repeat("ب", 201), "too_long:200"},
		{"pay.stars_title", strings.Repeat("x", 33), "too_long:32"},
		{"welcome", "{brand}" + strings.Repeat("x", 3500), "too_long:3500"},
		// {addresses} carries <code>, so it cannot sit inside <code>.
		{"pay.crypto", "{amount} <code>{addresses}</code> {ref}", "tag_in_code:b"},
		// {balances} carries <b> too (the reseller panel's money lines).
		{"rs.panel", "{tier} {discount} {commission} <code>{balances}</code> {customers} {earned}", "tag_in_code:b"},
		// The debt shown inside a balance is plain text.
		{"wallet.debt", "<b>{amount}</b>", "markup_not_allowed:<"},
		{"sub.ready", `<a href="{link}">x</a> {expires} {traffic}`, "bad_link:x"},
	}
	for _, tc := range bad {
		if got := codes("en", tc.key, tc.v); !slices.Contains(got, tc.want) {
			t.Errorf("%s %q: %v, want %s", tc.key, tc.v, got, tc.want)
		}
	}
	if _, probs := c.ValidateOverride("en", "welcome", "{brand} \xff"); len(probs) != 1 || probs[0].Code != ProblemBadUTF8 {
		t.Fatalf("bad utf-8: %v", probs)
	}
}

func TestSetOverridesDropsBrokenTexts(t *testing.T) {
	c := MustLoad()
	dropped := c.SetOverrides(map[string]string{
		"texts.fa.welcome":     "سلام {brand}",
		"texts.en.welcome":     "<b>hi {brand}", // unclosed: would make Telegram refuse the menu
		"texts.en.btn.buy":     "Buy now",
		"texts.FA.btn.buy":     "x", // not a language
		"texts.en.no.such":     "x", // not a text
		"texts.fa.lang.prompt": "x", // never read
		"branding.name":        "x",
	})
	if want := []string{"texts.FA.btn.buy", "texts.en.no.such", "texts.en.welcome", "texts.fa.lang.prompt"}; !slices.Equal(dropped, want) {
		t.Fatalf("dropped %v, want %v", dropped, want)
	}
	if got := c.T("en", "welcome", "brand", "B"); strings.Contains(got, "<b>hi") {
		t.Fatalf("broken override used: %q", got)
	}
	if got := c.T("en", "btn.buy"); got != "Buy now" {
		t.Fatalf("good override lost: %q", got)
	}
}

func TestTextLengthAndStrip(t *testing.T) {
	if n := TextLength("<b>ab</b> &amp; 😀"); n != 7 { // a b space & space + 2 units for the emoji
		t.Fatalf("length %d", n)
	}
	if n := TextLength("&#128512;&#x41;"); n != 3 { // an emoji (2 units) and A
		t.Fatalf("numeric entities: %d", n)
	}
	if got := StripHTML("<b>a &lt;b&gt;</b> &amp; &quot;q&quot;"); got != `a <b> & "q"` {
		t.Fatalf("strip %q", got)
	}
}

func TestCurrencyNamesAndLocation(t *testing.T) {
	defer SetCurrencyNames(nil)
	SetCurrencyNames(map[string]string{"fa": " تومن ", "en": ""})
	if got := Money("fa", 1500, "IRT"); got != "۱٬۵۰۰ تومن" {
		t.Fatalf("fa name: %q", got)
	}
	if got := Money("en", 1500, "IRT"); got != "1,500 Toman" {
		t.Fatalf("en default: %q", got)
	}
	if got := Money("fa", 2_500_000, "USDT"); got != "۲٫۵ تتر" {
		t.Fatalf("usdt untouched: %q", got)
	}
	old := CurrentLocation()
	defer SetLocation(old)
	SetLocation(nil) // ignored
	if CurrentLocation() != old {
		t.Fatal("nil location replaced the zone")
	}
}

func TestValidateMessageAndLabel(t *testing.T) {
	codes := func(_ string, probs []Problem) []string {
		var out []string
		for _, p := range probs {
			out = append(out, p.Code+":"+p.Arg)
		}
		return out
	}
	for _, ok := range []string{
		"<b>Sale</b> {50%} off &amp; more 🎉\r\n<a href=\"https://t.me/x\">join</a>",
		strings.Repeat("😀", MaxMessage/2), // 2 units each: exactly the limit
	} {
		if got := codes(ValidateMessage(ok, MaxMessage)); len(got) != 0 {
			t.Errorf("%.40q: %v", ok, got)
		}
	}
	bad := map[string]string{
		"":                                  "empty:",
		"  \n ":                             "empty:",
		"<b>open":                           "tag_unclosed:b",
		`<a href="http://x.ir">x</a>`:       "bad_link:http://x.ir",
		"1 < 2":                             "bad_entity:<",
		"x\x07":                             `control_char:'\a'`,
		strings.Repeat("😀", MaxMessage/2+1): "too_long:4096",
	}
	for v, want := range bad {
		if got := codes(ValidateMessage(v, MaxMessage)); !slices.Contains(got, want) {
			t.Errorf("%.40q: %v, want %s", v, got, want)
		}
	}
	if v, probs := ValidateLabel("  Open   the\nstore "); len(probs) != 0 || v != "Open the store" {
		t.Fatalf("label: %q %v", v, probs)
	}
	for v, want := range map[string]string{"": "empty:", "<b>x</b>": "markup_not_allowed:<", strings.Repeat("x", 65): "too_long:64"} {
		if got := codes(ValidateLabel(v)); !slices.Contains(got, want) {
			t.Errorf("label %.20q: %v, want %s", v, got, want)
		}
	}
}

// A line break in a text is a real one: a backslash followed by an n would reach the customer as those two characters (four texts did,
// before this test).
func TestNoLiteralBackslashN(t *testing.T) {
	c := MustLoad()
	for _, info := range c.Infos() {
		for _, lang := range info.Langs {
			if def := c.Default(lang, info.Key); strings.Contains(def, `\n`) {
				t.Errorf("%s/%s has a literal \\n instead of a line break", lang, info.Key)
			}
		}
	}
}
