package handler

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Resellers (milestone 5): the reseller panel. A reseller buys services for
// their own customers at their tier's price, on credit down to a limit per
// currency, names each service, and earns a commission on every purchase by
// a customer who joined through their link. Owner and admins manage them in
// the dashboard; core keeps every rule, and customers see none of this.

const (
	// maxLabel is the longest name a reseller gives a service, in characters
	// (core's domain.MaxLabel).
	maxLabel = 64
	// maxNote is the longest private note on a service, in characters (core's
	// domain.MaxNote).
	maxNote = 200
	// servicesPage, statementPage and customersPage are what one page lists.
	servicesPage  = 8
	statementPage = 10
	customersPage = 10
	// maxSearch is the longest search, in bytes: the paging buttons carry it
	// within Telegram's 64 bytes of callback data.
	maxSearch = 40
	// maxPage bounds a page number read from a button.
	maxPage = 100_000
)

// routeReseller handles "rs:<verb>:<arg>", for active resellers only.
func (h *Handler) routeReseller(r *req, rest string) {
	if !r.isReseller() {
		h.notReseller(r)
		return
	}
	r.clearState() // a question left open (a name, a search) no longer applies
	verb, arg, _ := strings.Cut(rest, ":")
	switch verb {
	case "buy":
		h.showResellerPlans(r)
	case "p":
		h.askLabel(r, arg)
	case "sk": // no name: on to the payment
		if c, ok := h.loadCheckout(r, arg); ok {
			h.showCheckout(r, c)
		}
	case "subs":
		h.showResellerServices(r, pageArg(arg), "", "")
	case "sq":
		page, query, _ := strings.Cut(arg, ":")
		h.showResellerServices(r, pageArg(page), query, "")
	case "sf": // "rs:sf:<filter>:<page>[:<search>]"
		h.onServiceFilter(r, arg)
	case "find": // "rs:find[:<filter>]": the search is made within the filter
		r.setState(sceneResellerFind, map[string]string{"f": arg})
		r.show(r.t("rs.search_ask"), cancelKeyboard(r))
	case "rn":
		h.askRename(r, arg)
	case "rc":
		h.setLabel(r, arg, "")
	case "pz":
		h.setPaused(r, arg, true)
	case "rz":
		h.setPaused(r, arg, false)
	case "dl":
		h.askDelete(r, arg)
	case "dy":
		h.deleteService(r, arg)
	case "nt":
		h.askNote(r, arg)
	case "nc":
		h.setNote(r, arg, "")
	case "cu":
		h.showInvitedCustomers(r, pageArg(arg))
	case "st":
		h.showStatement(r, pageArg(arg))
	case "link":
		h.showResellerLink(r)
	default:
		h.showResellerPanel(r)
	}
}

// notReseller answers a reseller's button or question from someone who is
// not one (any more); a banned reseller is told they are restricted.
func (h *Handler) notReseller(r *req) {
	key := "rs.forbidden"
	if r.user != nil && r.user.GetRole() == "reseller" && r.user.GetStatus() != "active" {
		key = "error.banned"
	}
	r.clearState()
	if r.cb != nil {
		r.toast, r.alert = r.t(key), true
	} else {
		r.send(r.t(key), nil)
	}
	h.showHome(r)
}

// pageArg reads a page number from a button (1 for anything else).
func pageArg(s string) int32 {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil || n < 1 || n > maxPage {
		return 1
	}
	return int32(n) //nolint:gosec // between 1 and maxPage
}

// pager is a list's Previous and Next buttons ("rs:<...>:<page>" from data).
func pager(r *req, page int32, more bool, data func(int32) string) []tg.Button {
	var out []tg.Button
	if page > 1 {
		out = append(out, r.button("btn.prev", data(page-1)))
	}
	if more {
		out = append(out, r.button("btn.next", data(page+1)))
	}
	return out
}

// earned lists money per currency (nothing yet: 0 Toman).
func earned(r *req, ms []*commonv1.Money) string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, r.money(m.GetAmount(), m.GetCurrency()))
	}
	if len(out) == 0 {
		out = append(out, r.money(0, "IRT"))
	}
	return strings.Join(out, " · ")
}

// resellerInfo is the reseller's terms and money; someone core no longer
// knows as a reseller is answered as such.
func (h *Handler) resellerInfo(r *req) (*corev1.ResellerInfo, bool) {
	info, err := h.core.GetResellerInfo(r.ctx, &corev1.GetResellerInfoRequest{UserId: r.user.GetId()})
	if status.Code(err) == codes.NotFound {
		h.notReseller(r)
		return nil, false
	}
	if err != nil {
		r.fail(err)
		return nil, false
	}
	return info, true
}

// showResellerPanel handles "rs": the reseller's tier, money per currency
// (what they owe shown as a debt), customers and commission.
func (h *Handler) showResellerPanel(r *req) {
	info, ok := h.resellerInfo(r)
	if !ok {
		return
	}
	blocks := make([]string, 0, len(info.GetBalances()))
	for _, b := range info.GetBalances() {
		cur := b.GetCurrency()
		blocks = append(blocks, r.t("rs.balance", "balance", r.balance(b.GetBalance(), cur),
			"limit", r.money(b.GetCreditLimit(), cur), "available", r.money(b.GetAvailable(), cur)))
	}
	kb := (&tg.Keyboard{}).
		Row(r.button("btn.rs_buy", "rs:buy"), r.button("btn.rs_services", "rs:subs:1")).
		Row(r.button("btn.rs_customers", "rs:cu:1"), r.button("btn.rs_statement", "rs:st:1")).
		Row(r.button("btn.rs_link", "rs:link")).
		Row(r.button("btn.home", "home"))
	r.show(r.t("rs.panel", "tier", esc(info.GetTierName()),
		"discount", i18n.Number(r.lang, int64(info.GetDiscountPercent())),
		"commission", i18n.Number(r.lang, int64(info.GetCommissionPercent())),
		"balances", strings.Join(blocks, "\n\n"), "customers", i18n.Number(r.lang, info.GetCustomers()),
		"earned", earned(r, info.GetCommissionEarned())), kb)
}

// showResellerPlans handles "rs:buy": the plans at the reseller's prices,
// each named for the customer next.
func (h *Handler) showResellerPlans(r *req) {
	ps, err := h.regularPlans(r)
	if err != nil {
		r.fail(err)
		return
	}
	if len(ps) == 0 {
		r.show(r.t("plans.empty"), backHome(r, "rs"))
		return
	}
	kb := &tg.Keyboard{}
	h.addPlans(r, kb, ps, func(p *corev1.Plan) string { return "rs:p:" + p.GetId() })
	kb.Row(r.button("btn.back", "rs"), r.button("btn.home", "home"))
	r.show(r.t("rs.buy_title"), kb)
}

// askLabel handles "rs:p:<plan id>": the purchase is saved under a nonce and
// the next message names the service, or Skip goes on without a name.
func (h *Handler) askLabel(r *req, planID string) {
	p, err := h.findPlan(r, planID, false)
	if err != nil {
		r.fail(err)
		return
	}
	n, ok := h.saveCheckout(r, state.Checkout{Type: "new", PlanID: p.GetId(), ForCustomer: true})
	if !ok {
		return
	}
	r.setState(sceneResellerLabel, map[string]string{"co": n})
	r.show(r.t("rs.label_ask", "name", esc(planName(r, p)), "max", i18n.Number(r.lang, maxLabel)), skipKeyboard(r, n))
}

func skipKeyboard(r *req, n string) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(r.button("btn.skip", "rs:sk:"+n), r.button("btn.cancel", "cancel"))
}

// cleanLabel is a service name as typed: runs of spaces and line breaks
// become one space. False when it is longer than maxLabel characters or
// holds other control characters (core refuses both).
func cleanLabel(s string) (string, bool) {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || !utf8.ValidString(s) || strings.ContainsFunc(s, unicode.IsControl) || utf8.RuneCountInString(s) > maxLabel {
		return "", false
	}
	return s, true
}

// searchText is a search as typed, on one line, cut to maxSearch bytes
// (whole characters).
func searchText(s string) string {
	s = strings.Join(strings.Fields(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) && !unicode.IsSpace(c) {
			return -1
		}
		return c
	}, s)), " ")
	for len(s) > maxSearch {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return strings.TrimSpace(s)
}

// onResellerScene takes what a reseller typed: a name for the service being
// bought, a search, or a new name for a service.
func (h *Handler) onResellerScene(r *req, st state.State) {
	if !r.isReseller() {
		h.notReseller(r)
		return
	}
	bad := r.t("rs.label_bad", "max", i18n.Number(r.lang, maxLabel))
	switch st.Scene {
	case sceneResellerLabel:
		label, ok := cleanLabel(r.msg.Text)
		if !ok {
			r.send(bad, skipKeyboard(r, st.Get("co")))
			return
		}
		c, ok := h.loadCheckout(r, st.Get("co"))
		r.clearState()
		if !ok {
			return
		}
		c.Label = label
		h.showCheckout(r, c)
	case sceneResellerFind:
		q := searchText(r.msg.Text)
		if q == "" {
			r.send(r.t("rs.search_ask"), cancelKeyboard(r))
			return
		}
		r.clearState()
		bucket, _ := filterBucket(st.Get("f")) // the search is made within the filter it was asked in
		h.showResellerServices(r, 1, q, bucket)
	case sceneResellerRename:
		label, ok := cleanLabel(r.msg.Text)
		if !ok {
			r.send(bad, cancelKeyboard(r))
			return
		}
		h.setLabel(r, st.Get("sub"), label)
	case sceneResellerNote:
		note, ok := cleanNote(r.msg.Text)
		if !ok {
			r.send(r.t("rs.note_bad", "max", i18n.Number(r.lang, maxNote)), cancelKeyboard(r))
			return
		}
		h.setNote(r, st.Get("sub"), note)
	}
}

// showCustomerServices is the reseller's list of services, all of them.
func (h *Handler) showCustomerServices(r *req, page int32, query string) {
	h.showResellerServices(r, page, query, "")
}

// The filters of the list, as the buttons carry them: a short code in the
// callback data and the status group core knows it by.
var resellerFilters = []struct{ code, bucket string }{
	{"a", "active"}, {"e", "expiring_soon"}, {"x", "expired"}, {"p", "paused"},
}

// filterBucket is the status group of a filter's code (false for anything
// else, which means no filter).
func filterBucket(code string) (string, bool) {
	for _, f := range resellerFilters {
		if f.code == code {
			return f.bucket, true
		}
	}
	return "", false
}

func filterCode(bucket string) string {
	for _, f := range resellerFilters {
		if f.bucket == bucket {
			return f.code
		}
	}
	return ""
}

// serviceCount is how many services the counts say a group has ("" = all).
func serviceCount(c *corev1.ServiceCounts, bucket string) int64 {
	switch bucket {
	case "active":
		return c.GetActive()
	case "expiring_soon":
		return c.GetExpiringSoon()
	case "expired":
		return c.GetExpired()
	case "paused":
		return c.GetPaused()
	}
	return c.GetTotal()
}

// onServiceFilter handles "rs:sf:<filter>:<page>[:<search>]".
func (h *Handler) onServiceFilter(r *req, arg string) {
	code, rest, _ := strings.Cut(arg, ":")
	bucket, ok := filterBucket(code)
	if !ok {
		h.showResellerServices(r, 1, "", "")
		return
	}
	page, query, _ := strings.Cut(rest, ":")
	h.showResellerServices(r, pageArg(page), query, bucket)
}

// serviceListData is the callback data that shows a page of the list in a
// filter (bucket "" = all) and a search.
func serviceListData(bucket, query string, page int32) string {
	p := strconv.Itoa(int(page))
	switch {
	case bucket == "" && query == "":
		return "rs:subs:" + p
	case bucket == "":
		return "rs:sq:" + p + ":" + query
	case query == "":
		return "rs:sf:" + filterCode(bucket) + ":" + p
	}
	return "rs:sf:" + filterCode(bucket) + ":" + p + ":" + query
}

// showResellerServices handles "rs:subs:<page>", "rs:sq:<page>:<search>" and
// "rs:sf:<filter>:<page>[:<search>]": the reseller's services, newest first,
// servicesPage at a time, each opening the service's page, with the groups
// to filter by and how many services each holds. Once resellers are no longer
// in the license core refuses the groups; the plain list still shows what
// they bought.
func (h *Handler) showResellerServices(r *req, page int32, query, bucket string) {
	resp, err := h.core.ListResellerServices(r.ctx, &corev1.ListResellerServicesRequest{UserId: r.user.GetId(), Status: bucket, Query: query,
		Pagination: &commonv1.Pagination{Page: page, PageSize: servicesPage}})
	if st := status.Convert(err); (st.Code() == codes.PermissionDenied && !strings.Contains(strings.ToLower(st.Message()), "banned")) || st.Code() == codes.Unimplemented {
		h.showPlainServices(r, page, query) // resellers left the license, or this core is older than the bot
		return
	}
	if err != nil {
		r.fail(err)
		return
	}
	if len(resp.GetSubscriptions()) == 0 && page > 1 { // past the end (services ended meanwhile): the first page
		h.showResellerServices(r, 1, query, bucket)
		return
	}
	total, counts := resp.GetPageInfo().GetTotal(), resp.GetCounts()
	pages := max((total+servicesPage-1)/servicesPage, 1)
	if counts.GetTotal() == 0 {
		r.show(r.t("rs.services_empty"), (&tg.Keyboard{}).Row(r.button("btn.rs_buy", "rs:buy")).
			Row(r.button("btn.back", "rs"), r.button("btn.home", "home")))
		return
	}
	num := func(n int64) string { return i18n.Number(r.lang, n) }
	kb := &tg.Keyboard{}
	for _, s := range resp.GetSubscriptions() {
		kb.Row(tg.CB(h.serviceButton(r, s), "sub:"+s.GetId()))
	}
	kb.Row(pager(r, page, int64(page) < pages, func(p int32) string { return serviceListData(bucket, query, p) })...)
	// The groups, with how many services each holds; the one shown is ticked.
	filter := func(b string) tg.Button {
		name := r.t("rs.filter.all")
		if b != "" {
			name = r.t("rs.filter." + b)
		}
		label := name + " (" + num(serviceCount(counts, b)) + ")"
		if b == bucket {
			label = "✔️ " + label
		}
		return tg.CB(label, serviceListData(b, "", 1))
	}
	kb.Row(filter(""), filter("active")).Row(filter("expiring_soon"), filter("expired"), filter("paused"))
	find := "rs:find"
	if bucket != "" {
		find += ":" + filterCode(bucket)
	}
	search := []tg.Button{r.button("btn.rs_search", find)}
	if query != "" {
		search = append(search, r.button("btn.rs_all", serviceListData(bucket, "", 1)))
	}
	kb.Row(search...).Row(r.button("btn.back", "rs"), r.button("btn.home", "home"))
	var text string
	filterName := ""
	if bucket != "" {
		filterName = r.t("rs.filter." + bucket)
	}
	switch {
	case total == 0 && bucket == "":
		text = r.t("rs.services_none_found", "query", esc(query))
	case total == 0 && query == "":
		text = r.t("rs.services_filtered_none", "filter", filterName)
	case total == 0:
		text = r.t("rs.services_filtered_found_none", "filter", filterName, "query", esc(query))
	case bucket == "" && query == "":
		text = r.t("rs.services_title", "total", num(total), "page", num(int64(page)), "pages", num(pages))
	case bucket == "":
		text = r.t("rs.services_found", "query", esc(query), "total", num(total), "page", num(int64(page)), "pages", num(pages))
	case query == "":
		text = r.t("rs.services_filtered", "filter", filterName, "total", num(total), "page", num(int64(page)), "pages", num(pages))
	default:
		text = r.t("rs.services_filtered_found", "filter", filterName, "query", esc(query), "total", num(total), "page", num(int64(page)), "pages", num(pages))
	}
	r.show(text, kb)
}

// showPlainServices is the list of services without the groups: all of them,
// paged and searched, as core lists any user's.
func (h *Handler) showPlainServices(r *req, page int32, query string) {
	resp, err := h.core.ListSubscriptions(r.ctx, &corev1.ListSubscriptionsRequest{UserId: r.user.GetId(), Query: query,
		Pagination: &commonv1.Pagination{Page: page, PageSize: servicesPage}})
	if err != nil {
		r.fail(err)
		return
	}
	if len(resp.GetSubscriptions()) == 0 && page > 1 { // past the end (services ended meanwhile): the first page
		h.showPlainServices(r, 1, query)
		return
	}
	total := resp.GetPageInfo().GetTotal()
	pages := max((total+servicesPage-1)/servicesPage, 1)
	if total == 0 && query == "" {
		r.show(r.t("rs.services_empty"), (&tg.Keyboard{}).Row(r.button("btn.rs_buy", "rs:buy")).
			Row(r.button("btn.back", "rs"), r.button("btn.home", "home")))
		return
	}
	kb := &tg.Keyboard{}
	for _, s := range resp.GetSubscriptions() {
		kb.Row(tg.CB(h.serviceButton(r, s), "sub:"+s.GetId()))
	}
	kb.Row(pager(r, page, int64(page) < pages, func(p int32) string {
		if query == "" {
			return "rs:subs:" + strconv.Itoa(int(p))
		}
		return "rs:sq:" + strconv.Itoa(int(p)) + ":" + query
	})...)
	search := []tg.Button{r.button("btn.rs_search", "rs:find")}
	if query != "" {
		search = append(search, r.button("btn.rs_all", "rs:subs:1"))
	}
	kb.Row(search...).Row(r.button("btn.back", "rs"), r.button("btn.home", "home"))
	num := func(n int64) string { return i18n.Number(r.lang, n) }
	var text string
	switch {
	case total == 0:
		text = r.t("rs.services_none_found", "query", esc(query))
	case query != "":
		text = r.t("rs.services_found", "query", esc(query), "total", num(total), "page", num(int64(page)), "pages", num(pages))
	default:
		text = r.t("rs.services_title", "total", num(total), "page", num(int64(page)), "pages", num(pages))
	}
	r.show(text, kb)
}

// askRename handles "rs:rn:<subscription id>": the next message is the
// service's new name (a service with a name can also lose it).
func (h *Handler) askRename(r *req, id string) {
	s, ok := h.findSubscription(r, id)
	if !ok {
		return
	}
	r.setState(sceneResellerRename, map[string]string{"sub": s.GetId()})
	kb := &tg.Keyboard{}
	if s.GetLabel() != "" {
		kb.Row(r.button("btn.remove_name", "rs:rc:"+s.GetId()))
	}
	kb.Row(r.button("btn.cancel", "cancel"))
	r.show(r.labelled(s.GetLabel(), r.t("rs.rename_ask", "id", shortID(s.GetId()), "max", i18n.Number(r.lang, maxLabel))), kb)
}

// setLabel names one of the reseller's services ("" removes the name) and
// shows the service again.
func (h *Handler) setLabel(r *req, id, label string) {
	_, err := h.core.SetSubscriptionLabel(r.ctx, &corev1.SetSubscriptionLabelRequest{UserId: r.user.GetId(),
		SubscriptionId: id, Label: label})
	switch status.Code(err) {
	case codes.OK:
	case codes.InvalidArgument: // a name core refuses: asked again
		r.send(r.t("rs.label_bad", "max", i18n.Number(r.lang, maxLabel)), cancelKeyboard(r))
		return
	case codes.NotFound: // not theirs, or deleted meanwhile
		r.clearState()
		h.showServices(r)
		return
	default:
		r.clearState()
		r.fail(err)
		return
	}
	r.clearState()
	if label == "" {
		r.toast = r.t("rs.label_removed")
	}
	h.showService(r, id)
}

// showStatement handles "rs:st:<page>": the reseller's wallet transactions,
// commissions and payments to the store included, statementPage at a time.
func (h *Handler) showStatement(r *req, page int32) {
	list := func(page, size int32) ([]*corev1.LedgerEntry, error) {
		resp, err := h.core.ListLedgerEntries(r.ctx, &corev1.ListLedgerEntriesRequest{UserId: r.user.GetId(),
			Pagination: &commonv1.Pagination{Page: page, PageSize: size}})
		return resp.GetEntries(), err
	}
	entries, err := list(page, statementPage)
	if err != nil {
		r.fail(err)
		return
	}
	if len(entries) == 0 && page > 1 { // past the end: the first page
		h.showStatement(r, 1)
		return
	}
	// The ledger has no total: one entry asked for at the next page's first
	// position tells whether there is a next page.
	more := false
	if len(entries) == statementPage {
		next, err := list(page*statementPage+1, 1)
		if err != nil {
			r.fail(err)
			return
		}
		more = len(next) > 0
	}
	kb := (&tg.Keyboard{}).Row(pager(r, page, more, func(p int32) string { return "rs:st:" + strconv.Itoa(int(p)) })...).
		Row(r.button("btn.back", "rs"), r.button("btn.home", "home"))
	if len(entries) == 0 {
		r.show(r.t("wallet.history_empty"), kb)
		return
	}
	lines := []string{r.t("rs.statement_title", "page", i18n.Number(r.lang, int64(page))), ""}
	for _, e := range entries {
		lines = append(lines, ledgerLine(r, e))
	}
	r.show(strings.Join(lines, "\n"), kb)
}

// showResellerLink handles "rs:link": the reseller's invite link and their
// commission. It works whatever the store's referral reward (0 only turns
// referrals off for customers).
func (h *Handler) showResellerLink(r *req) {
	info, ok := h.resellerInfo(r)
	if !ok {
		return
	}
	if h.cfg.BotUsername == "" { // getMe failed at start: no link to build
		r.show(r.t("error.unavailable"), backHome(r, "rs"))
		return
	}
	link := "https://t.me/" + h.cfg.BotUsername + "?start=r_" + info.GetRefCode()
	share := "https://t.me/share/url?url=" + url.QueryEscape(link) + "&text=" + url.QueryEscape(r.t("ref.share_text", "brand", h.brand()))
	kb := (&tg.Keyboard{}).Row(r.linkButton("btn.share", share)).
		Row(r.button("btn.back", "rs"), r.button("btn.home", "home"))
	r.show(r.t("rs.link", "percent", i18n.Number(r.lang, int64(info.GetCommissionPercent())), "link", esc(link),
		"customers", i18n.Number(r.lang, info.GetCustomers()), "earned", earned(r, info.GetCommissionEarned())), kb)
	h.sendPromoCard(r, link) // a card to forward, apart from this screen
}
