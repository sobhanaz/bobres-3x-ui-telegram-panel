package web

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

var giftCodeShape = regexp.MustCompile(`^SPRING(-[A-HJKMNP-Z2-9]{4}){3}$`)

func giftPlain(code string) string { return strings.ReplaceAll(strings.ToUpper(code), "-", "") }

// A code is in one answer, the one that made it; the list, the audit log and the exports hold the last four
// characters only.
func TestGiftCodesDashboard(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	soon := time.Now().Add(30 * 24 * time.Hour).Unix()

	code, out := b.do(http.MethodGet, "/api/v1/gift-codes", nil)
	if code != 200 || len(items(out)) != 0 || out["total"] != float64(0) || out["enabled"] != true || out["available"] != true {
		t.Fatalf("an empty list: %d %v", code, out)
	}

	// A batch of generated codes with a prefix.
	code, out = b.do(http.MethodPost, "/api/v1/gift-codes", map[string]any{"amount": "25,000", "currency": "irt", "max_redemptions": 5,
		"per_customer_limit": 1, "expires_at": soon, "note": " Spring giveaway ", "prefix": "spring", "count": 3})
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	codes, _ := out["codes"].([]any)
	made := items(out)
	if len(codes) != 3 || len(made) != 3 {
		t.Fatalf("a batch of 3: %v", out)
	}
	var shown []string
	for i, c := range codes {
		s, _ := c.(string)
		if !giftCodeShape.MatchString(s) {
			t.Fatalf("code %q", s)
		}
		shown = append(shown, s)
		it := made[i]
		amount := it["amount"].(map[string]any)
		by := it["created_by"].(map[string]any)
		if it["tail"] != giftPlain(s)[len(giftPlain(s))-4:] || amount["amount"] != float64(25_000) || amount["currency"] != "IRT" || it["max_redemptions"] != float64(5) ||
			it["per_customer_limit"] != float64(1) || it["redeemed"] != float64(0) || it["status"] != "active" || it["enabled"] != true || it["generated"] != true ||
			it["note"] != "Spring giveaway" || it["expires_at"] != float64(soon) || by["telegram_id"] != float64(ownerTG) {
			t.Fatalf("item %d: %v", i, it)
		}
	}

	// The list, the audit log and the CSV hold no code, only its last four characters.
	_, _, listBody := b.get("/api/v1/gift-codes")
	_, auditOut := b.do(http.MethodGet, "/api/v1/audit?action=gift_code", nil)
	if auditOut["total"] != float64(3) {
		t.Fatalf("%v audit entries for 3 new codes", auditOut["total"])
	}
	_, csvHeaders, csvBody := b.get("/api/v1/gift-codes.csv")
	for _, s := range shown {
		for _, text := range []string{string(listBody), asJSON(auditOut), string(csvBody)} {
			if strings.Contains(strings.ToUpper(text), giftPlain(s)) || strings.Contains(strings.ToUpper(text), strings.ToUpper(s)) {
				t.Fatalf("the code %q is in a list, an audit entry or an export", s)
			}
		}
	}
	if !strings.HasPrefix(csvHeaders.Get("Content-Disposition"), `attachment; filename="gift-codes-`) || csvHeaders.Get("Content-Type") != "text/csv; charset=utf-8" || csvHeaders.Get("Cache-Control") != "no-store" {
		t.Fatalf("csv headers: %v", csvHeaders)
	}

	// A code of the owner's own words; made twice, it is refused.
	code, out = b.do(http.MethodPost, "/api/v1/gift-codes", map[string]any{"amount": "2.5", "currency": "USDT", "max_redemptions": 10, "per_customer_limit": 2, "code": "nowruz-1405", "note": "=HYPERLINK(\"http://evil\")"})
	if code != 200 || len(out["codes"].([]any)) != 1 || out["codes"].([]any)[0] != "NOWRUZ-1405" || items(out)[0]["generated"] != false ||
		items(out)[0]["amount"].(map[string]any)["amount"] != float64(2_500_000) {
		t.Fatalf("own code: %d %v", code, out)
	}
	own := items(out)[0]
	if code, out := b.do(http.MethodPost, "/api/v1/gift-codes", map[string]any{"amount": "1", "currency": "IRT", "max_redemptions": 1, "code": "NOWRUZ1405"}); code != 400 || out["field"] != "code" {
		t.Fatalf("the same code twice: %d %v", code, out)
	}

	// What is refused names the field, and nothing is made.
	for name, c := range map[string]struct {
		body  map[string]any
		field string
	}{
		"amount 0":              {map[string]any{"amount": "0", "currency": "IRT", "max_redemptions": 1}, "amount"},
		"a negative amount":     {map[string]any{"amount": "-5", "currency": "IRT", "max_redemptions": 1}, "amount"},
		"not a number":          {map[string]any{"amount": "lots", "currency": "IRT", "max_redemptions": 1}, "amount"},
		"too many digits":       {map[string]any{"amount": "9223372036854775807", "currency": "IRT", "max_redemptions": 1}, "amount"},
		"a huge amount":         {map[string]any{"amount": "99999999999999", "currency": "IRT", "max_redemptions": 1}, "amount"},
		"decimals in Toman":     {map[string]any{"amount": "1.5", "currency": "IRT", "max_redemptions": 1}, "amount"},
		"a currency not sold":   {map[string]any{"amount": "5", "currency": "EUR", "max_redemptions": 1}, "amount"},
		"no uses":               {map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 0}, "max_redemptions"},
		"too many uses":         {map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1_000_001}, "max_redemptions"},
		"more per customer":     {map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 2, "per_customer_limit": 3}, "per_customer_limit"},
		"an expiry in the past": {map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1, "expires_at": time.Now().Add(-time.Hour).Unix()}, "expires_at"},
		"501 codes":             {map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1, "count": 501}, "count"},
		"a short custom code":   {map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1, "code": "ABC123"}, "code"},
		"a long note":           {map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1, "note": strings.Repeat("x", 201)}, "note"},
		"a long prefix":         {map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1, "prefix": "TOOLONGPREFIX"}, "prefix"},
	} {
		if code, out := b.do(http.MethodPost, "/api/v1/gift-codes", c.body); code != 400 || out["field"] != c.field {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	for name, body := range map[string]any{
		"an unknown field":  map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1, "code_hash": "x"},
		"a number amount":   map[string]any{"amount": 5, "currency": "IRT", "max_redemptions": 1},
		"a string for uses": map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": "many"},
		"a count too big":   map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1, "count": 1e30},
	} {
		if code, _ := b.do(http.MethodPost, "/api/v1/gift-codes", body); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if _, out := b.do(http.MethodGet, "/api/v1/gift-codes", nil); out["total"] != float64(4) {
		t.Fatalf("%v codes after the refused ones", out["total"])
	}

	// A customer redeems one; the redemptions say who and when, and the list counts it.
	cust := f.customer(8801, "dara")
	if r, err := f.dom.RedeemGiftCode(context.Background(), domain.RedeemGiftCodeParams{UserID: cust.ID, Code: strings.ToLower(shown[0]), RequestKey: "k1"}); err != nil || r.Balance != 25_000 {
		t.Fatalf("redeem: %+v %v", r, err)
	}
	id := made[0]["id"].(string)
	code, out = b.do(http.MethodGet, "/api/v1/gift-codes/"+id+"/redemptions", nil)
	if code != 200 || out["total"] != float64(1) || len(items(out)) != 1 {
		t.Fatalf("redemptions: %d %v", code, out)
	}
	red := items(out)[0]
	user := red["user"].(map[string]any)
	if user["telegram_id"] != float64(8801) || user["username"] != "dara" || red["seq"] != float64(1) || red["amount"].(map[string]any)["amount"] != float64(25_000) ||
		red["balance_after"].(map[string]any)["amount"] != float64(25_000) || red["created_at"] == nil || out["code"].(map[string]any)["redeemed"] != float64(1) {
		t.Fatalf("redemption: %v", red)
	}
	_, list := b.do(http.MethodGet, "/api/v1/gift-codes?q="+made[0]["tail"].(string), nil)
	var listed map[string]any
	for _, it := range items(list) {
		if it["id"] == id {
			listed = it
		}
	}
	if listed == nil || listed["last_redeemed_at"] == nil || listed["redeemed"] != float64(1) {
		t.Fatalf("the list after a redemption: %v", list)
	}

	// Switching a code off: it is listed as disabled, what it paid stays, and the audit log says who.
	code, out = b.do(http.MethodPut, "/api/v1/gift-codes/"+id+"/enabled", map[string]any{"enabled": false})
	if code != 200 || out["item"].(map[string]any)["enabled"] != false || out["item"].(map[string]any)["status"] != "disabled" || out["item"].(map[string]any)["redeemed"] != float64(1) {
		t.Fatalf("disable: %d %v", code, out)
	}
	if e := lastAudit(t, b, "action=gift_code.disable"); e["entity"] != "gift_code" || e["entity_id"] != id || e["actor_role"] != "owner" || e["source"] != "dashboard" {
		t.Fatalf("audit: %v", e)
	}
	if w, err := f.st.GetWallet(context.Background(), f.st.Conn(), cust.ID, "IRT"); err != nil || w.Balance != 25_000 {
		t.Fatalf("disabling took money back: %+v %v", w, err)
	}
	if r, err := f.dom.RedeemGiftCode(context.Background(), domain.RedeemGiftCodeParams{UserID: f.customer(8802, "x").ID, Code: shown[0], RequestKey: "k2"}); err == nil || r != nil {
		t.Fatalf("a disabled code was redeemed: %+v %v", r, err)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/gift-codes/"+id+"/enabled", map[string]any{"enabled": true}); code != 200 || out["item"].(map[string]any)["status"] != "active" {
		t.Fatalf("enable: %d %v", code, out)
	}

	// Filters and paging.
	if _, out := b.do(http.MethodPut, "/api/v1/gift-codes/"+made[1]["id"].(string)+"/enabled", map[string]any{"enabled": false}); out["item"] == nil {
		t.Fatal(out)
	}
	_, out = b.do(http.MethodGet, "/api/v1/gift-codes?status=disabled", nil)
	if len(items(out)) != 1 || out["total"] != float64(1) || items(out)[0]["id"] != made[1]["id"] {
		t.Fatalf("status=disabled: %v", out)
	}
	_, out = b.do(http.MethodGet, "/api/v1/gift-codes?q=giveaway+spring", nil)
	if out["total"] != float64(0) {
		t.Fatalf("a search of words that are in no note: %v", out)
	}
	_, out = b.do(http.MethodGet, "/api/v1/gift-codes?q=giveaway&size=2&page=2", nil)
	if out["total"] != float64(3) || len(items(out)) != 1 || out["page"] != float64(2) || out["size"] != float64(2) {
		t.Fatalf("a search of a note, page 2 of size 2: %v", out)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/gift-codes?status=bogus", nil); code != 400 {
		t.Fatalf("an unknown status: %d", code)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/gift-codes/not-an-id/redemptions", nil); code != 404 {
		t.Fatalf("a bad id: %d", code)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/gift-codes/00000000-0000-0000-0000-00000000beef/redemptions", nil); code != 404 {
		t.Fatalf("a code that is not there: %d", code)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/gift-codes/00000000-0000-0000-0000-00000000beef/enabled", map[string]any{"enabled": true}); code != 404 {
		t.Fatalf("enabling a code that is not there: %d", code)
	}

	// The CSV files: every code, with the note quoted so it cannot run as a formula, and who redeemed one.
	_, _, body := b.get("/api/v1/gift-codes.csv")
	rows := parseCSV(t, body)
	if len(rows) != 5 || strings.Join(rows[0], ",") !=
		"created_utc,code_last4,kind,amount,currency,max_redemptions,per_customer_limit,redeemed,status,expires_utc,last_redeemed_utc,note,created_by_telegram_id,code_id,created_local,tz" {
		t.Fatalf("gift-codes.csv: %v", rows)
	}
	var ownRow []string
	for _, r := range rows[1:] {
		if r[13] == own["id"] {
			ownRow = r
		}
	}
	if ownRow == nil || ownRow[2] != "custom" || ownRow[3] != "2.500000" || ownRow[4] != "USDT" || ownRow[7] != "0" || ownRow[8] != "active" || ownRow[11] != `'=HYPERLINK("http://evil")` ||
		ownRow[12] != strconv.Itoa(ownerTG) || ownRow[1] != "1405" {
		t.Fatalf("the own code's row: %v", ownRow)
	}
	if e := lastAudit(t, b, "action=export.gift_codes"); !strings.Contains(asJSON(e["after"]), `"outcome":"complete"`) || !strings.Contains(asJSON(e["after"]), `"rows":4`) {
		t.Fatalf("export audit: %v", e)
	}
	_, h, body := b.get("/api/v1/gift-codes/" + id + "/redemptions.csv")
	rows = parseCSV(t, body)
	if len(rows) != 2 || strings.Join(rows[0], ",") != "time_utc,telegram_id,username,number,amount,currency,balance_after,redemption_id,time_local,tz" ||
		rows[1][1] != "8801" || rows[1][2] != "dara" || rows[1][3] != "1" || rows[1][4] != "25000" || rows[1][6] != "25000" ||
		!strings.Contains(h.Get("Content-Disposition"), "-redemptions-") {
		t.Fatalf("redemptions.csv: %v (%v)", rows, h)
	}
	if code, _, _ := b.get("/api/v1/gift-codes.csv?status=bogus"); code != 400 {
		t.Fatalf("csv with an unknown status: %d", code)
	}
	if code, _, _ := b.get("/api/v1/gift-codes/00000000-0000-0000-0000-00000000beef/redemptions.csv"); code != 404 {
		t.Fatalf("redemptions.csv of a code that is not there: %d", code)
	}
}

// Both gift code files are spreadsheet files like the four of csv_files_test.go: the safe download name, the
// byte-order mark, nothing cached or sniffed, one kind of line break (also inside a cell), numbers that stay
// numbers, and every row as wide as the header.
func TestGiftCodesCSVIsASpreadsheetFile(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	code, out := b.do(http.MethodPost, "/api/v1/gift-codes", map[string]any{"amount": "2.5", "currency": "USDT", "max_redemptions": 3,
		"code": "WINTER-GIFT-2026", "note": "one, with a \"quote\" and a +formula"})
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	id := items(out)[0]["id"].(string)
	// somebody with a name that is a formula redeemed it
	cust := f.customer(8803, "=cmd|' /C calc'!A0")
	if _, err := f.dom.RedeemGiftCode(context.Background(), domain.RedeemGiftCodeParams{UserID: cust.ID, Code: "winter-gift-2026", RequestKey: "k1"}); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	for name, path := range map[string]string{"codes": "/api/v1/gift-codes.csv", "redemptions": "/api/v1/gift-codes/" + id + "/redemptions.csv"} {
		code, h, body := b.get(path)
		if code != 200 || h.Get("Content-Type") != "text/csv; charset=utf-8" || !fileName.MatchString(h.Get("Content-Disposition")) ||
			h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s headers: %d %v", name, code, h)
		}
		text := string(body[min(3, len(body)):])
		if strings.Count(text, "\n") != strings.Count(text, "\r\n") || strings.Count(text, "\r") != strings.Count(text, "\r\n") {
			t.Errorf("%s mixes line breaks: %q", name, text)
		}
		rows := parseCSV(t, body)
		if len(rows) < 2 {
			t.Fatalf("%s: %v", name, rows)
		}
		number := map[string]bool{"amount": true, "max_redemptions": true, "per_customer_limit": true, "redeemed": true,
			"telegram_id": true, "number": true, "balance_after": true, "created_by_telegram_id": true}
		for _, r := range rows[1:] {
			if len(r) != len(rows[0]) {
				t.Fatalf("%s: a row has %d cells, the header %d: %v", name, len(r), len(rows[0]), r)
			}
			for i, cell := range r {
				if number[rows[0][i]] && cell != "" && !plainNumber.MatchString(cell) {
					t.Errorf("%s.%s: %q is not a plain number", name, rows[0][i], cell)
				}
				if trimmed := strings.TrimLeft(cell, " \t\r\n"); trimmed != "" && strings.ContainsRune("=+@", rune(trimmed[0])) {
					t.Errorf("%s.%s: %q could run as a formula", name, rows[0][i], cell)
				}
			}
		}
	}
}

// The files follow the rules of docs/14-reports.md for dates: UTC instants, the local wall clock and the zone, a
// Jalali column at the very end of a Jalali file, a file name that says so, and a 400 for a calendar or a zone
// nobody knows.
func TestGiftCodesCSVDates(t *testing.T) {
	_, b, _, _ := salesFixture(t)
	if code, out := b.do(http.MethodPost, "/api/v1/gift-codes", map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1, "note": "dates"}); code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	code, h, body := b.get("/api/v1/gift-codes.csv?tz=Asia/Tehran")
	rows := parseCSV(t, body)
	if code != 200 || len(rows) < 2 || rows[0][len(rows[0])-1] != "tz" || rows[1][len(rows[1])-1] != "Asia/Tehran" ||
		!regexp.MustCompile(`^attachment; filename="gift-codes-all-to-\d{4}-\d\d-\d\d\.csv"$`).MatchString(h.Get("Content-Disposition")) {
		t.Fatalf("gregorian file: %d %v %v", code, h, rows)
	}
	if utc, local := rows[1][0], rows[1][len(rows[1])-2]; !strings.HasSuffix(utc, "Z") || len(local) != len("2006-01-02 15:04:05") || local[10] != ' ' {
		t.Fatalf("created_utc %q, created_local %q", utc, local)
	}
	code, h, body = b.get("/api/v1/gift-codes.csv?tz=Asia/Tehran&calendar=jalali")
	rows = parseCSV(t, body)
	last := rows[0][len(rows[0])-1]
	if code != 200 || last != "created_jalali" || !regexp.MustCompile(`^1\d{3}-\d\d-\d\d \d\d:\d\d:\d\d$`).MatchString(rows[1][len(rows[1])-1]) ||
		!strings.HasSuffix(h.Get("Content-Disposition"), `-jalali.csv"`) {
		t.Fatalf("jalali file: %d %v %v", code, h, rows[:2])
	}
	for _, q := range []string{"calendar=hijri", "tz=Mars/Base"} {
		if code, _, _ := b.get("/api/v1/gift-codes.csv?" + q); code != 400 {
			t.Errorf("%s: %d", q, code)
		}
	}
}

// The notes of the CSV cannot run as a formula in any way a spreadsheet reads one.
func TestGiftCodesCSVQuotesNotes(t *testing.T) {
	_, b, _, _ := salesFixture(t)
	notes := []string{"=1+1", "+1", "-1", "@SUM(A1)", "  =1+1", "\t=1", "\r=1", "plain", "a=b"}
	for _, n := range notes {
		if code, out := b.do(http.MethodPost, "/api/v1/gift-codes", map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1, "note": n}); code != 200 {
			t.Fatalf("note %q: %d %v", n, code, out)
		}
	}
	_, _, body := b.get("/api/v1/gift-codes.csv")
	for _, r := range parseCSV(t, body)[1:] {
		note := r[11]
		if note == "" {
			continue
		}
		trimmed := strings.TrimLeft(note, " \t\r\n\v\f")
		if note != "plain" && note != "a=b" && !strings.HasPrefix(note, "'") {
			t.Errorf("the note %q would run as a formula", note)
		}
		if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
			t.Errorf("the note %q starts with a formula character", note)
		}
	}
}

// discounts.write, as the discount codes have: the owner and an admin; not support, not anybody without a session,
// not a request without the CSRF token.
func TestGiftCodesPermissions(t *testing.T) {
	f, owner, _, _ := salesFixture(t)
	f.staffUser(8811, "admin")
	f.staffUser(8812, "support")
	admin, support := f.browser(), f.browser()
	admin.loginWithLink(8811)
	support.loginWithLink(8812)
	body := map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1}
	if code, _ := admin.do(http.MethodPost, "/api/v1/gift-codes", body); code != 200 {
		t.Fatalf("an admin makes codes: %d", code)
	}
	_, out := owner.do(http.MethodGet, "/api/v1/gift-codes", nil)
	id := items(out)[0]["id"].(string)
	for name, call := range map[string]func() int{
		"list":   func() int { c, _ := support.do(http.MethodGet, "/api/v1/gift-codes", nil); return c },
		"create": func() int { c, _ := support.do(http.MethodPost, "/api/v1/gift-codes", body); return c },
		"enable": func() int {
			c, _ := support.do(http.MethodPut, "/api/v1/gift-codes/"+id+"/enabled", map[string]any{"enabled": false})
			return c
		},
		"redemptions":    func() int { c, _ := support.do(http.MethodGet, "/api/v1/gift-codes/"+id+"/redemptions", nil); return c },
		"csv":            func() int { c, _, _ := support.get("/api/v1/gift-codes.csv"); return c },
		"redemption csv": func() int { c, _, _ := support.get("/api/v1/gift-codes/" + id + "/redemptions.csv"); return c },
	} {
		if code := call(); code != 403 {
			t.Errorf("support, %s: %d", name, code)
		}
	}
	nobody := f.browser()
	for name, call := range map[string]func() int{
		"list":   func() int { c, _ := nobody.do(http.MethodGet, "/api/v1/gift-codes", nil); return c },
		"create": func() int { c, _ := nobody.do(http.MethodPost, "/api/v1/gift-codes", body); return c },
		"csv":    func() int { c, _, _ := nobody.get("/api/v1/gift-codes.csv"); return c },
	} {
		if code := call(); code != 401 {
			t.Errorf("no session, %s: %d", name, code)
		}
	}
	if n := func() float64 {
		_, o := owner.do(http.MethodGet, "/api/v1/gift-codes", nil)
		return o["total"].(float64)
	}(); n != 1 {
		t.Fatalf("%v codes after refused requests", n)
	}
	admin.csrf = ""
	if code, out := admin.do(http.MethodPost, "/api/v1/gift-codes", body); code != 403 || out["error"] != "csrf" {
		t.Fatalf("a request without the CSRF token: %d %v", code, out)
	}
	if code, out := admin.do(http.MethodPut, "/api/v1/gift-codes/"+id+"/enabled", map[string]any{"enabled": false}); code != 403 || out["error"] != "csrf" {
		t.Fatalf("a switch without the CSRF token: %d %v", code, out)
	}
	_, out = owner.do(http.MethodGet, "/api/v1/gift-codes", nil)
	if items(out)[0]["enabled"] != true {
		t.Fatal("a request without the CSRF token switched a code off")
	}
}

// Without core's master key nothing is made, and the page is told why; the owner's switch shows too.
func TestGiftCodesWithoutAKeyAndTheSwitch(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = f.st.DB().Exec(ctx, `DELETE FROM core.settings WHERE key = 'gift_codes.enabled'`) })
	if _, out := b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"gift_codes.enabled": "false"}}); settingByKey(out, "gift_codes.enabled")["value"] != "false" {
		t.Fatalf("the switch is not a setting of the Settings page: %v", out)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/gift-codes", nil); out["enabled"] != false || out["available"] != true {
		t.Fatalf("switched off: %v", out)
	}
	// Codes can still be made while it is off.
	if code, out := b.do(http.MethodPost, "/api/v1/gift-codes", map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1}); code != 200 {
		t.Fatalf("create while switched off: %d %v", code, out)
	}
	real := f.api.cfg.Domain
	f.api.cfg.Domain = domain.New(f.st, domain.Config{OwnerTelegramIDs: []int64{ownerTG}})
	t.Cleanup(func() { f.api.cfg.Domain = real })
	if _, out := b.do(http.MethodGet, "/api/v1/gift-codes", nil); out["available"] != false {
		t.Fatalf("without a key: %v", out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gift-codes", map[string]any{"amount": "5", "currency": "IRT", "max_redemptions": 1}); code != 409 || out["error"] != "state" ||
		!strings.Contains(out["message"].(string), "master key") {
		t.Fatalf("create without a key: %d %v", code, out)
	}
}
