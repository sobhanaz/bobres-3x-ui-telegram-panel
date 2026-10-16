package handler

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Discount codes: the list (a tap turns a code on or off), the wizard that
// creates one, and the commands /discount_add, /discount_off, /discounts.

var discountCodeRe = regexp.MustCompile(`^[A-Z0-9_-]{3,32}$`)

func (h *Handler) listDiscounts(r *req) ([]*corev1.Discount, error) {
	list, err := h.core.AdminListDiscounts(r.ctx, &corev1.AdminListDiscountsRequest{ActorTelegramId: h.actor(r)})
	return list.GetDiscounts(), err
}

// showDiscountMenu lists the codes as buttons: a tap turns one on or off.
func (h *Handler) showDiscountMenu(r *req) {
	list, err := h.listDiscounts(r)
	if err != nil {
		r.fail(err)
		return
	}
	lines := []string{r.t("admin.discounts_title")}
	kb := &tg.Keyboard{}
	if len(list) == 0 {
		lines = append(lines, r.t("admin.discounts_empty"))
	}
	for _, d := range list {
		lines = append(lines, h.discountLine(r, d))
		mark := r.t("admin.plan_off")
		if d.GetEnabled() {
			mark = r.t("admin.plan_on")
		}
		kb.Row(tg.CB(mark+" "+d.GetCode(), "adm:dt:"+d.GetCode()))
	}
	kb.Row(r.button("btn.adm_new_discount", "adm:dn"))
	kb.Row(r.button("btn.back", "adm"))
	r.show(strings.Join(lines, "\n")+"\n\n"+r.t("admin.discounts_hint"), kb)
}

// toggleDiscount turns a code on or off (its history stays).
func (h *Handler) toggleDiscount(r *req, code string) {
	list, err := h.listDiscounts(r)
	if err != nil {
		r.fail(err)
		return
	}
	for _, d := range list {
		if d.GetCode() == code {
			d.Enabled = !d.GetEnabled()
			if _, err := h.core.AdminUpsertDiscount(r.ctx, &corev1.AdminUpsertDiscountRequest{ActorTelegramId: h.actor(r), Discount: d}); err != nil {
				r.fail(err)
				return
			}
			break
		}
	}
	h.showDiscountMenu(r)
}

func discountWizard() *wizard {
	isAmount := func(d map[string]string) bool { return d["kind"] == "amount" }
	return &wizard{
		steps: []wizStep{
			{
				id: "code",
				prompt: func(r *req, d map[string]string) (string, [][]tg.Button) {
					return promptText(r, "wiz.disc.code", nil), nil
				},
				parse: func(r *req, d map[string]string, in string) (string, string) {
					code := strings.ToUpper(in)
					if !discountCodeRe.MatchString(code) {
						return "", r.t("admin.err_code")
					}
					return code, ""
				},
			},
			choiceStep("kind", "wiz.disc.kind", [2]string{"percent", "btn.adm_kind_percent"}, [2]string{"amount", "btn.adm_kind_amount"}),
			func() wizStep { s := currencyStep(); s.when = isAmount; return s }(),
			{
				id: "value",
				prompt: func(r *req, d map[string]string) (string, [][]tg.Button) {
					if isAmount(d) {
						return promptText(r, "wiz.disc.amount", []any{"currency", d["cur"]}), nil
					}
					return promptText(r, "wiz.disc.percent", nil, r.rangeLine(1, 100)), nil
				},
				parse: func(r *req, d map[string]string, in string) (string, string) {
					if isAmount(d) {
						n, ok := parseMinor(in, d["cur"])
						if !ok || n <= 0 {
							return "", r.t("admin.err_price", "currency", d["cur"])
						}
						return strconv.FormatInt(n, 10), ""
					}
					n, bad := parseInt(r, strings.TrimSuffix(in, "%"), 1, 100)
					return strconv.FormatInt(n, 10), bad
				},
			},
			intStep("max", "wiz.disc.max", 0, 1_000_000, "0", nil),
			intStep("days", "wiz.disc.days", 0, 3650, "0", nil),
		},
		confirm: func(r *req, d map[string]string) (string, string) {
			return r.t("admin.confirm_disc", "code", esc(d["code"]), "terms", discountTerms(r, d),
				"max", unlimitedOr(r, minorOf(d["max"])), "days", unlimitedOr(r, minorOf(d["days"]))), ""
		},
		save: func(r *req, d map[string]string) string {
			disc := &corev1.Discount{Code: d["code"], Enabled: true, MaxUses: int32(minorOf(d["max"]))} //nolint:gosec // 0..1e6
			if d["kind"] == "amount" {
				disc.Amount = &commonv1.Money{Amount: minorOf(d["value"]), Currency: d["cur"]}
			} else {
				disc.Percent = int32(minorOf(d["value"])) //nolint:gosec // 1..100
			}
			if days := minorOf(d["days"]); days > 0 {
				disc.ExpiresAt = time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix()
			}
			saved, err := r.h.core.AdminUpsertDiscount(r.ctx, &corev1.AdminUpsertDiscountRequest{ActorTelegramId: r.h.actor(r), Discount: disc})
			if err != nil {
				return r.h.refused(r, err)
			}
			r.show(r.t("admin.discount_saved", "line", r.h.discountLine(r, saved)), (&tg.Keyboard{}).Row(r.button("btn.back", "adm:disc")))
			return ""
		},
		home: func(map[string]string) string { return "disc" },
	}
}

func discountTerms(r *req, d map[string]string) string {
	if d["kind"] == "amount" {
		return r.money(minorOf(d["value"]), d["cur"])
	}
	return i18n.Number(r.lang, minorOf(d["value"])) + "%"
}

// unlimitedOr shows a number, or "unlimited" for 0.
func unlimitedOr(r *req, n int64) string {
	if n == 0 {
		return r.t("admin.unlimited")
	}
	return i18n.Number(r.lang, n)
}

// onDiscountAdd: "/discount_add <CODE> <20%|50000 IRT> [max uses] [days valid]".
func (h *Handler) onDiscountAdd(r *req, args string) {
	f := strings.Fields(args)
	if len(f) < 2 {
		r.send(r.t("admin.discount_usage"), nil)
		return
	}
	d := &corev1.Discount{Code: strings.ToUpper(f[0]), Enabled: true}
	rest := f[2:]
	if pct, isPct := strings.CutSuffix(f[1], "%"); isPct {
		n, ok := i18n.ParseNumber(pct)
		if !ok || n < 1 || n > 100 {
			r.send(r.t("admin.discount_usage"), nil)
			return
		}
		d.Percent = int32(n) //nolint:gosec // 1..100
	} else {
		if len(f) < 3 {
			r.send(r.t("admin.discount_usage"), nil)
			return
		}
		cur := strings.ToUpper(f[2])
		amount, ok := parseMinor(f[1], cur)
		if !ok || amount <= 0 {
			r.send(r.t("admin.discount_usage"), nil)
			return
		}
		d.Amount = &commonv1.Money{Amount: amount, Currency: cur}
		rest = f[3:]
	}
	for i, tok := range rest {
		n, ok := i18n.ParseNumber(tok)
		if !ok || n <= 0 || i > 1 {
			r.send(r.t("admin.discount_usage"), nil)
			return
		}
		if i == 0 {
			d.MaxUses = int32(min(n, 1_000_000)) //nolint:gosec // bounded
		} else {
			d.ExpiresAt = time.Now().Add(time.Duration(min(n, 3650)) * 24 * time.Hour).Unix()
		}
	}
	h.saveDiscount(r, d)
}

func (h *Handler) saveDiscount(r *req, d *corev1.Discount) {
	saved, err := h.core.AdminUpsertDiscount(r.ctx, &corev1.AdminUpsertDiscountRequest{ActorTelegramId: h.actor(r), Discount: d})
	if err != nil {
		if status.Code(err) == codes.InvalidArgument {
			r.send(esc(status.Convert(err).Message())+"\n"+r.t("admin.discount_usage"), nil)
			return
		}
		r.fail(err)
		return
	}
	r.send(r.t("admin.discount_saved", "line", h.discountLine(r, saved)), nil)
}

// onDiscountOff: "/discount_off <CODE>" stops a code (its history stays).
func (h *Handler) onDiscountOff(r *req, code string) {
	list, err := h.listDiscounts(r)
	if err != nil {
		r.fail(err)
		return
	}
	for _, d := range list {
		if strings.EqualFold(d.GetCode(), code) && code != "" {
			d.Enabled = false
			h.saveDiscount(r, d)
			return
		}
	}
	r.send(r.t("admin.discount_unknown", "code", esc(code)), nil)
}

func (h *Handler) showDiscounts(r *req) {
	list, err := h.listDiscounts(r)
	if err != nil {
		r.fail(err)
		return
	}
	if len(list) == 0 {
		r.send(r.t("admin.discounts_empty")+"\n"+r.t("admin.discount_usage"), nil)
		return
	}
	lines := []string{r.t("admin.discounts_title")}
	for _, d := range list {
		lines = append(lines, h.discountLine(r, d))
	}
	r.send(strings.Join(lines, "\n"), nil)
}

// discountLine shows a code's terms: "SPRING20 · 20% · 3/100 used · until … · off".
func (h *Handler) discountLine(r *req, d *corev1.Discount) string {
	parts := []string{"<code>" + esc(d.GetCode()) + "</code>"}
	if d.GetPercent() > 0 {
		parts = append(parts, i18n.Number(r.lang, int64(d.GetPercent()))+"%")
	} else {
		parts = append(parts, r.money(d.GetAmount().GetAmount(), d.GetAmount().GetCurrency()))
	}
	used := i18n.Number(r.lang, int64(d.GetUsed()))
	if d.GetMaxUses() > 0 {
		used += "/" + i18n.Number(r.lang, int64(d.GetMaxUses()))
	}
	parts = append(parts, r.t("admin.discount_used", "n", used))
	if d.GetExpiresAt() > 0 {
		parts = append(parts, r.t("admin.discount_until", "date", i18n.Date(r.lang, time.Unix(d.GetExpiresAt(), 0))))
	}
	if !d.GetEnabled() {
		parts = append(parts, r.t("admin.discount_off"))
	}
	return strings.Join(parts, " · ")
}
