// Package web serves the payments service's public pages, behind Caddy:
//
//   - GET /pay/<intent id>: the page a customer opens from the bot. Zarinpal
//     accepts a payment only when it starts from a page on the merchant's
//     registered domain (the browser's Referer), so the bot links here, not to
//     Zarinpal directly. A plain redirect would not carry the Referer.
//   - GET /webhooks/zarinpal: Zarinpal sends the customer's browser back here.
//     Nothing in the query is trusted: the payment is always verified with
//     Zarinpal before the result page is shown.
package web

import (
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
)

var (
	idRe        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	authorityRe = regexp.MustCompile(`^[AS][0-9a-zA-Z]{35}$`)
)

// Handler serves the pages.
type Handler struct {
	svc *domain.Service
	st  *store.Store
	log *slog.Logger

	mu   sync.Mutex
	last map[string]time.Time // intent id -> last return-page check
}

// returnGap: a refreshed or re-opened return page asks the gateway at most
// this often per intent.
const returnGap = 3 * time.Second

// New builds the handler.
func New(svc *domain.Service, st *store.Store, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Handler{svc: svc, st: st, log: log, last: map[string]time.Time{}}
}

// Register mounts the routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /pay/{id}", h.payPage)
	mux.HandleFunc("GET /webhooks/zarinpal", h.zarinpalReturn)
}

func headers(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Zarinpal checks that the payment starts from our registered domain.
	w.Header().Set("Referrer-Policy", "origin")
}

func (h *Handler) payPage(w http.ResponseWriter, r *http.Request) {
	headers(w)
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		h.render(w, http.StatusNotFound, page{Kind: "missing"})
		return
	}
	in, err := h.st.GetIntent(r.Context(), nil, id)
	if err != nil || in.Provider != "zarinpal" {
		h.render(w, http.StatusNotFound, page{Kind: "missing"})
		return
	}
	p := pageFor(in)
	if in.Status == "pending" && in.PayURL != nil && (in.ExpiresAt == nil || time.Now().Before(*in.ExpiresAt)) {
		p.Kind, p.PayURL = "pay", *in.PayURL
	}
	h.render(w, http.StatusOK, p)
}

func (h *Handler) zarinpalReturn(w http.ResponseWriter, r *http.Request) {
	headers(w)
	authority := r.URL.Query().Get("Authority")
	if !authorityRe.MatchString(authority) {
		h.render(w, http.StatusNotFound, page{Kind: "missing"})
		return
	}
	in, err := h.st.IntentByExternalID(r.Context(), "zarinpal", authority)
	if errors.Is(err, store.ErrNotFound) {
		h.render(w, http.StatusNotFound, page{Kind: "missing"})
		return
	}
	if err != nil {
		h.log.Warn("zarinpal return: lookup", "err", err)
		h.render(w, http.StatusServiceUnavailable, page{Kind: "error"})
		return
	}
	statusOK := r.URL.Query().Get("Status") == "OK"
	if in.Status != "succeeded" && h.mayCheck(in.ID) {
		// Not tied to the browser: a closed tab must not cut a verify short, and
		// 20 s stays under the server's write timeout. The service throttles
		// repeated checks of one intent.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 20*time.Second)
		checked, err := h.svc.CheckAfterReturn(ctx, in.ID)
		cancel()
		if err != nil {
			h.log.Warn("zarinpal return: check", "intent", in.ID, "err", err)
		} else {
			in = checked
		}
	}
	p := pageFor(in)
	if p.Kind == "pending" && !statusOK {
		p.Kind = "cancelled"
	}
	h.render(w, http.StatusOK, p)
}

func (h *Handler) mayCheck(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if t, ok := h.last[id]; ok && now.Sub(t) < returnGap {
		return false
	}
	if len(h.last) > 10_000 { // bounded: drop old entries
		for k, t := range h.last {
			if now.Sub(t) > time.Minute {
				delete(h.last, k)
			}
		}
	}
	h.last[id] = now
	return true
}

type page struct {
	Kind      string // pay, paid, pending, cancelled, failed, expired, missing, error
	AmountEN  string
	AmountFA  string
	PayURL    string
	Reference string
	ReasonEN  string
	ReasonFA  string
}

func pageFor(in *store.Intent) page {
	p := page{AmountEN: i18n.Money("en", in.Amount, in.Currency), AmountFA: i18n.Money("fa", in.Amount, in.Currency)}
	switch in.Status {
	case "succeeded":
		p.Kind = "paid"
		if in.ProviderRef != nil {
			p.Reference = *in.ProviderRef
		}
	case "failed":
		p.Kind = "failed"
		if in.FailureReason != nil && *in.FailureReason == domain.ReasonAmountMismatch {
			p.ReasonEN = "The amount paid did not match. Contact support with your bank receipt."
			p.ReasonFA = "مبلغ پرداخت‌شده با فاکتور یکی نبود. با رسید بانکی به پشتیبانی پیام دهید." //nolint:staticcheck // Persian needs the zero-width non-joiner
		}
	case "expired":
		p.Kind = "expired"
	default:
		p.Kind = "pending"
	}
	return p
}

func (h *Handler) render(w http.ResponseWriter, status int, p page) {
	w.WriteHeader(status)
	if err := tmpl.Execute(w, p); err != nil {
		h.log.Warn("render payment page", "err", err)
	}
}

// The page text is Persian, which needs the zero-width non-joiner (U+200C).
//
//nolint:staticcheck // ST1018: Persian typography needs U+200C in the literal
var tmpl = template.Must(template.New("page").Parse(strings.TrimSpace(`
<!doctype html>
<html lang="fa" dir="rtl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>پرداخت / Payment</title>
<style>
body{font-family:Vazirmatn,Tahoma,system-ui,sans-serif;background:#f4f5f7;color:#1d2433;margin:0;padding:24px 16px}
main{max-width:440px;margin:0 auto;background:#fff;border-radius:14px;padding:24px;box-shadow:0 2px 12px rgba(0,0,0,.06)}
h1{font-size:20px;margin:0 0 12px}.en{direction:ltr;text-align:left;color:#5b6475;font-size:14px;margin-top:20px;border-top:1px solid #eceef2;padding-top:16px}
.amount{font-size:26px;font-weight:700;margin:12px 0}.btn{display:block;text-align:center;background:#1a7f37;color:#fff;text-decoration:none;font-size:18px;padding:14px;border-radius:10px;margin:18px 0}
.note{background:#fff8e1;border-radius:8px;padding:10px 12px;font-size:14px}.ok{color:#1a7f37}.bad{color:#b42318}
</style>
</head>
<body><main>
{{if eq .Kind "pay"}}
<h1>پرداخت آنلاین</h1>
<div class="amount">{{.AmountFA}}</div>
<p class="note">پیش از پرداخت، فیلترشکن (VPN) را خاموش کنید؛ درگاه بانکی فقط از اینترنت ایران باز می‌شود.</p>
<a class="btn" href="{{.PayURL}}" referrerpolicy="origin">پرداخت با زرین‌پال</a>
<p>پس از پرداخت به همین صفحه برمی‌گردید و سرویس شما در ربات تحویل می‌شود.</p>
<div class="en"><b>Online payment: {{.AmountEN}}</b><br>Turn your VPN off before paying: the bank page only opens from an Iranian connection. <a href="{{.PayURL}}" referrerpolicy="origin">Pay with Zarinpal</a>. You will come back here, and the bot delivers your service.</div>
{{else if eq .Kind "paid"}}
<h1 class="ok">پرداخت انجام شد ✓</h1>
<div class="amount">{{.AmountFA}}</div>
{{if .Reference}}<p>شماره پیگیری: <b>{{.Reference}}</b></p>{{end}}
<p>به تلگرام برگردید؛ سرویس شما در ربات تحویل داده می‌شود.</p>
<div class="en"><b>Payment received.</b> {{if .Reference}}Reference: {{.Reference}}. {{end}}Go back to Telegram: the bot delivers your service.</div>
{{else if eq .Kind "pending"}}
<h1>در انتظار تأیید پرداخت</h1>
<p>اگر پرداخت کرده‌اید، چند دقیقه دیگر نتیجه در ربات اعلام می‌شود. اگر پولی کسر نشده، دوباره از ربات اقدام کنید.</p>
<div class="en"><b>Waiting for confirmation.</b> If you paid, the bot confirms it within a few minutes. If no money was taken, start again from the bot.</div>
{{else if eq .Kind "cancelled"}}
<h1 class="bad">پرداخت انجام نشد</h1>
<p>پرداخت لغو شد یا ناموفق بود. اگر پولی کسر شده، ظرف چند دقیقه بررسی و نتیجه در ربات اعلام می‌شود؛ در غیر این صورت دوباره از ربات اقدام کنید.</p>
<div class="en"><b>The payment did not go through.</b> If money was taken, it is checked within minutes and the bot tells you; otherwise start again from the bot.</div>
{{else if eq .Kind "failed"}}
<h1 class="bad">پرداخت ناموفق</h1>
<p>{{if .ReasonFA}}{{.ReasonFA}}{{else}}این پرداخت تأیید نشد. دوباره از ربات اقدام کنید.{{end}}</p>
<div class="en"><b>Payment failed.</b> {{if .ReasonEN}}{{.ReasonEN}}{{else}}This payment was not confirmed. Start again from the bot.{{end}}</div>
{{else if eq .Kind "expired"}}
<h1>این لینک پرداخت منقضی شده است</h1>
<p>از ربات دوباره روش پرداخت را انتخاب کنید.</p>
<div class="en"><b>This payment link has expired.</b> Choose the payment method again in the bot.</div>
{{else if eq .Kind "error"}}
<h1>خطای موقت</h1>
<p>چند لحظه بعد دوباره تلاش کنید.</p>
<div class="en"><b>Temporary error.</b> Try again in a moment.</div>
{{else}}
<h1>پرداخت پیدا نشد</h1>
<div class="en"><b>Payment not found.</b></div>
{{end}}
</main></body></html>
`)))
