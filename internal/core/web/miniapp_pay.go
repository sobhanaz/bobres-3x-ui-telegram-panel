package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/receipt"
)

// Telegram Stars and the manual payment methods (card to card, USDT to an
// address, a Zarinpal payment link) in the Mini App (docs/17-mini-app.md).
//
// Stars: core makes the payment as the bot does (the same payment key, so an
// order started in either place goes on in the other) and asks the bot for an
// invoice link, which the app opens inside Telegram. The bot's own handlers
// settle it: Telegram sends them the pre-checkout and the payment of a link
// as it does for an invoice message.
//
// Manual methods: core makes the payment as the bot does and answers what
// the bot shows (the card, the addresses, the link, the payment reference and
// the exact amount). The customer then sends the proof, a receipt image or a
// transaction id, to POST /payments/{id}/proof, which stores it through the
// same domain call the bot's proof flow uses: the review list, the staff
// alert and the admin card work as they do for a proof sent in the bot's chat.
// A customer who left the screen before sending it (Telegram reloaded the app)
// finds the payment again in GET /payments/pending.
// A receipt image is not kept by core, which has no Telegram token: the bot
// sends it to the customer's own chat and core keeps the file id Telegram
// gives back, the kind of receipt the bot's flow stores.

// MiniAppBot is what the Mini App's payments ask of the bot (botfiles.Client).
type MiniAppBot interface {
	// CreateInvoiceLink makes a Telegram Stars invoice link.
	CreateInvoiceLink(ctx context.Context, in botfiles.InvoiceRequest) (string, error)
	// SendReceipt sends a receipt image to the customer's chat and returns its
	// Telegram file id.
	SendReceipt(ctx context.Context, r botfiles.Receipt) (fileID string, err error)
	// NotifyProof has the staff alerted to a payment proof.
	NotifyProof(ctx context.Context, n botfiles.ProofNotice) error
}

const (
	// miniUploadBody is the largest request a proof may be: a receipt of at
	// most receipt.MaxBytes and the form around it.
	miniUploadBody = receipt.MaxBytes + 256<<10
	// miniUploadRate receipts a customer may send in a miniUploadWindow, on top
	// of the limit on all their changes.
	miniUploadRate   = 6
	miniUploadWindow = 10 * time.Minute
	// miniUploadSlots receipts are checked and sent at the same time; each is
	// held in memory meanwhile.
	miniUploadSlots = 4
	// miniReferenceMax is the longest bank reference number, in characters, and
	// miniReferenceBytes in bytes (payments takes 256, whatever the characters).
	miniReferenceMax   = 128
	miniReferenceBytes = 256
	// miniPendingMax is the most payments GET /payments/pending lists (the newest).
	miniPendingMax = 10
)

// miniTxidRe is a transaction hash as the bot takes it: 64 hexadecimal
// characters, with a 0x in front for an Ethereum (ERC20) transaction.
var miniTxidRe = regexp.MustCompile(`^(0x)?[0-9a-fA-F]{64}$`)

// isMiniUpload reports whether r is the call that may carry a receipt image,
// which alone may be larger than a small JSON body.
func isMiniUpload(r *http.Request) bool {
	return r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/mini/v1/payments/") && strings.HasSuffix(r.URL.Path, "/proof")
}

// miniInflight holds the proofs being sent: a customer's second submission
// for a payment, while the first is on its way, is turned away.
type miniInflight struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

func newMiniInflight() *miniInflight { return &miniInflight{ids: map[string]struct{}{}} }

// begin takes id; false when it is taken.
func (f *miniInflight) begin(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, busy := f.ids[id]; busy {
		return false
	}
	f.ids[id] = struct{}{}
	return true
}

func (f *miniInflight) end(id string) {
	f.mu.Lock()
	delete(f.ids, id)
	f.mu.Unlock()
}

// miniPrice is what a payment the customer starts is for.
type miniPrice struct {
	amount   int64
	currency string
	// planID is the plan an order is for ("" for a wallet top-up).
	planID string
}

// miniOffered lists the payment methods the store offers this customer for a
// price: the list GET /methods answers, and what a payment they start is
// checked against. Without the bot there are no Stars and no manual method (a
// receipt, or an invoice, cannot be sent).
func (s *Server) miniOffered(ctx context.Context, currency string, amount int64) ([]string, error) {
	methods := []string{}
	if s.cfg.Payments == nil {
		return methods, nil
	}
	all, err := s.cfg.Domain.OfferedMethods(ctx, s.cfg.Payments, currency, amount)
	if err != nil {
		return nil, err
	}
	for _, m := range all {
		if !miniMethods[m] || (s.cfg.MiniAppBot == nil && (m == domain.Stars || domain.IsManual(m))) {
			continue
		}
		methods = append(methods, m)
	}
	return methods, nil
}

// errMethodOff: the method is not usable for this payment after all (what it
// shows was removed from the settings since the list was drawn).
var errMethodOff = errors.New("web: payment method not available")

// miniPaymentExtras adds what the app needs beyond the intent itself: for
// Stars the invoice link and the number of Stars, for crypto the USDT amount.
// It refuses (errMethodOff) a manual method whose details are missing.
func (s *Server) miniPaymentExtras(ctx context.Context, u *store.User, res *domain.IntentResult, price miniPrice, body map[string]any) error {
	switch {
	case res.Provider == domain.Stars:
		body["stars"] = res.GatewayAmount
		if res.Status != "pending" { // paid or over: there is no invoice to open
			return nil
		}
		link, err := s.cfg.MiniAppBot.CreateInvoiceLink(ctx, botfiles.InvoiceRequest{
			Payload: res.IntentID, Stars: res.GatewayAmount, Lang: u.Language, Title: s.miniPlanTitle(ctx, price.planID, u.Language),
			Amount: res.Amount, Currency: res.Currency, Topup: price.planID == "",
		})
		if err != nil {
			return err
		}
		body["invoice_url"] = link
	case domain.IsManual(res.Provider):
		d := res.Details
		switch res.Provider {
		case domain.ManualCard:
			if d["card_number"] == "" {
				return errMethodOff
			}
		case domain.ManualCrypto:
			if d["usdt_trc20"] == "" && d["usdt_erc20"] == "" {
				return errMethodOff
			}
			micro, ok := s.cfg.Domain.ManualCryptoAmount(ctx, res.Amount, res.Currency)
			if !ok {
				return errMethodOff
			}
			body["usdt_amount"] = micro
			if res.Currency == "IRT" {
				// the Toman price of one USDT this amount was worked out at (the live price when the store follows it)
				body["usdt_rate"] = s.cfg.Domain.UsdtRate(ctx)
			}
		case domain.ManualZarinpal:
			if l := d["zarinpal_link"]; !strings.HasPrefix(l, "https://") && !strings.HasPrefix(l, "http://") {
				return errMethodOff
			}
		}
	}
	return nil
}

// miniPlanTitle is a plan's name for an invoice's title, as the bot picks it:
// in the customer's language, else Persian, else English, else "" (the bot
// then uses the store's own title).
func (s *Server) miniPlanTitle(ctx context.Context, planID, lang string) string {
	if planID == "" {
		return ""
	}
	p, err := s.cfg.Store.GetPlan(ctx, s.cfg.Store.Conn(), planID)
	if err != nil {
		return ""
	}
	for _, l := range []string{lang, "fa", "en"} {
		if n := p.NameI18n[l]; n != "" {
			return n
		}
	}
	return ""
}

// miniBotFail answers a failure of the bot to do what a payment needed.
func (s *Server) miniBotFail(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, botfiles.ErrInvoiceRefused):
		writeError(w, http.StatusConflict, "invoice_refused", "Telegram would not make this invoice")
	case errors.Is(err, botfiles.ErrReceiptTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "the receipt is too large")
	case errors.Is(err, botfiles.ErrReceiptType):
		writeError(w, http.StatusUnsupportedMediaType, "bad_type", "the receipt must be a JPEG, PNG or WebP image")
	case errors.Is(err, botfiles.ErrChatClosed):
		writeError(w, http.StatusConflict, "chat_closed", "the bot cannot write to this customer: open the bot and press Start")
	case errors.Is(err, botfiles.ErrBotBusy):
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusServiceUnavailable, "busy", "many receipts are being sent; try again in a moment")
	default:
		s.cfg.Log.Warn("mini app: the bot did not do what a payment needed", "what", what, "err", err)
		writeError(w, http.StatusBadGateway, "bot_unavailable", "Telegram could not be reached; try again in a minute")
	}
}

// miniPendingPayments: GET /api/mini/v1/payments/pending, the customer's own
// manual payments that still wait for their proof, newest first (at most
// miniPendingMax). A customer who left the payment screen between paying and
// sending the receipt (Telegram reloaded the app, they closed it) takes the
// payment up again from here: each comes as the call that started it answered
// (what to pay and where, the reference, the exact amount; the answer of pay and
// topup, so the app draws the same screen) with the time it began. Everything
// comes from the payments service and the store's settings: the customer is the
// session's, and the request carries nothing. A payment the store would not
// start again now (its method switched off or its details gone, its order closed)
// is not listed, nor is one that has its proof in or was settled or rejected.
func (s *Server) miniPendingPayments(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	if s.cfg.Payments == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "payments are not connected")
		return
	}
	out := []map[string]any{}
	// A receipt goes through the bot: without it nothing here can be sent (as in miniOffered).
	if s.cfg.MiniAppBot != nil {
		list, err := s.cfg.Domain.PendingManualPayments(ctx, s.cfg.Payments, c.user.ID, miniPendingMax)
		if err != nil {
			s.miniFail(w, "mini app pending payments", err)
			return
		}
		for i := range list {
			res := &list[i].IntentResult
			body := miniIntentBody(res)
			// The same checks as when the payment was started: a method whose details are gone shows nothing to pay.
			switch err := s.miniPaymentExtras(ctx, c.user, res, miniPrice{}, body); {
			case errors.Is(err, errMethodOff):
				continue
			case err != nil:
				s.miniFail(w, "mini app pending payments", err)
				return
			}
			body["created_at"] = list[i].CreatedAt.Unix()
			out = append(out, body)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"payments": out})
}

// miniProof: POST /api/mini/v1/payments/{id}/proof, the proof of the
// customer's own manual payment: multipart/form-data with the receipt image
// (field "receipt") and the bank reference number ("reference"), or JSON with
// a crypto payment's "txid" and "network". It is accepted once, while the
// payment waits for it. The amount, the currency and the payment's owner all
// come from the payment, never from the request.
func (s *Server) miniProof(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	if s.cfg.Payments == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "payments are not connected")
		return
	}
	// The same payment in capitals is the same payment (the database takes
	// either), so the one-at-a-time guard below must not tell them apart.
	id := strings.ToLower(r.PathValue("id"))
	if !miniUUIDRe.MatchString(id) {
		s.miniFail(w, "mini app proof", store.ErrNotFound)
		return
	}
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "multipart/form-data" && media != "application/json" {
		writeError(w, http.StatusBadRequest, "invalid", "send a receipt image as multipart/form-data, or a transaction id as JSON")
		return
	}
	if r.ContentLength > miniUploadBody {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "the upload is too large")
		return
	}
	// One proof at a time for a payment of this customer.
	key := c.user.ID + ":" + id
	if !s.mini.proofs.begin(key) {
		writeError(w, http.StatusConflict, "busy", "a proof for this payment is being sent")
		return
	}
	defer s.mini.proofs.end(key)

	// The payment must be the customer's own, a manual one that waits for its
	// proof: asked before anything is read or sent.
	gi, err := s.cfg.Domain.CheckPayment(ctx, s.cfg.Payments, c.user.ID, id)
	if err != nil {
		s.miniFail(w, "mini app proof", err)
		return
	}
	switch {
	case !domain.IsManual(gi.Provider):
		writeError(w, http.StatusBadRequest, "invalid", "this payment takes no receipt")
		return
	case gi.Status == "confirming" || gi.Status == "succeeded":
		writeError(w, http.StatusConflict, "already_submitted", "a proof was already sent for this payment")
		return
	case gi.Status != "pending":
		writeError(w, http.StatusConflict, "payment_closed", "this payment is closed")
		return
	}

	var (
		p  domain.ProofParams
		ok bool
	)
	if media == "application/json" {
		p, ok = s.miniTxidProof(w, r, gi)
	} else {
		p, ok = s.miniReceiptProof(w, r, gi)
	}
	if !ok {
		return
	}
	p.UserID, p.IntentID = c.user.ID, id
	st, err := s.cfg.Domain.SubmitPaymentProof(ctx, s.cfg.Payments, p)
	if err != nil {
		s.miniProofFail(w, err)
		return
	}
	s.alertProof(ctx, c.user, gi)
	writeJSON(w, http.StatusOK, map[string]any{"intent_id": id, "status": st})
}

// miniProofFail answers a refusal of the proof by the payments service.
func (s *Server) miniProofFail(w http.ResponseWriter, err error) {
	inner := unwrapAll(err)
	msg := status.Convert(inner).Message()
	switch status.Code(inner) {
	case codes.AlreadyExists:
		writeError(w, http.StatusConflict, "duplicate", "this transaction was already submitted")
		return
	case codes.FailedPrecondition:
		switch {
		case strings.Contains(msg, "cannot change state"):
			writeError(w, http.StatusConflict, "already_submitted", "a proof was already sent for this payment")
			return
		case strings.Contains(msg, "unmatched deposit"):
			// The staff were told of this transfer as a deposit to credit by hand.
			writeError(w, http.StatusConflict, "txid_announced", "support already has this transaction")
			return
		}
	case codes.InvalidArgument:
		// What payments finds wrong is wording for the bot's staff; the app words it itself.
		writeError(w, http.StatusBadRequest, "invalid", "the payment does not take this proof")
		return
	}
	s.miniFail(w, "mini app proof", err)
}

// miniTxidProof reads a crypto payment's proof from a JSON body, as the bot
// reads a pasted hash: the network is the one the customer names, else the
// one the hash and the store's addresses say.
func (s *Server) miniTxidProof(w http.ResponseWriter, r *http.Request, gi *domain.GatewayIntent) (domain.ProofParams, bool) {
	if gi.Provider != domain.ManualCrypto {
		writeError(w, http.StatusBadRequest, "invalid", "a transaction id is the proof of a crypto payment; send a receipt image instead")
		return domain.ProofParams{}, false
	}
	var in struct {
		TXID    string `json:"txid"`
		Network string `json:"network"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, miniBody)
	if !readJSON(w, r, &in) {
		return domain.ProofParams{}, false
	}
	txid := strings.Join(strings.Fields(in.TXID), "")
	if !miniTxidRe.MatchString(txid) {
		writeError(w, http.StatusBadRequest, "bad_txid", "a transaction hash is 64 hexadecimal characters")
		return domain.ProofParams{}, false
	}
	settings, err := s.cfg.Domain.Settings(r.Context())
	if err != nil {
		s.miniFail(w, "mini app proof", err)
		return domain.ProofParams{}, false
	}
	have := map[string]bool{"TRC20": settings["payments.usdt_trc20"] != "", "ERC20": settings["payments.usdt_erc20"] != ""}
	network := strings.ToUpper(strings.TrimSpace(in.Network))
	if network == "" {
		network = "TRC20"
		if strings.HasPrefix(txid, "0x") || !have["TRC20"] {
			network = "ERC20"
		}
	}
	switch {
	case !have[network]:
		writeError(w, http.StatusBadRequest, "invalid", "the store takes no payment on that network")
		return domain.ProofParams{}, false
	case network == "TRC20" && strings.HasPrefix(txid, "0x"):
		writeError(w, http.StatusBadRequest, "bad_txid", "a TRC20 hash has no 0x in front")
		return domain.ProofParams{}, false
	}
	return domain.ProofParams{Network: network, TXID: txid}, true
}

// miniUploadErr answers a failure to read an upload's body.
func miniUploadErr(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "the upload is too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid", "the upload could not be read")
}

// cleanReference reads a bank reference number as typed: trimmed, at most
// miniReferenceMax characters and miniReferenceBytes bytes (payments refuses
// more, after the receipt has gone to Telegram), on one line.
func cleanReference(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > miniReferenceMax || len(s) > miniReferenceBytes {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return s, true
}

// miniReceiptProof reads a receipt image and its bank reference number from a
// multipart body, checks the image by its bytes (a JPEG, PNG or WebP of at
// most receipt.MaxBytes, whatever its file name or type says), and has the bot
// send it to the customer's chat. The proof is then the file id Telegram gave.
func (s *Server) miniReceiptProof(w http.ResponseWriter, r *http.Request, gi *domain.GatewayIntent) (domain.ProofParams, bool) {
	ctx, c := r.Context(), miniFrom(r.Context())
	none := domain.ProofParams{}
	if s.cfg.MiniAppBot == nil {
		writeError(w, http.StatusBadGateway, "bot_unavailable", "receipts are sent through the bot, which is not connected")
		return none, false
	}
	// One receipt of a customer at a time: the places below are few, and one
	// account must not be able to hold them all.
	if !s.mini.senders.begin(c.user.ID) {
		writeError(w, http.StatusConflict, "busy", "another receipt of yours is being sent")
		return none, false
	}
	defer s.mini.senders.end(c.user.ID)
	// A place for it, asked before the customer's allowance is used: being
	// turned away for lack of room costs them nothing.
	select {
	case s.mini.uploads <- struct{}{}:
		defer func() { <-s.mini.uploads }()
	default:
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "busy", "many receipts are being sent; try again in a moment")
		return none, false
	}
	if !s.mini.upload.allow(c.user.ID, s.now()) {
		w.Header().Set("Retry-After", "300")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many receipts; wait a few minutes")
		return none, false
	}
	// The server's deadlines are seconds; a phone's upload may take longer, up
	// to the 30 seconds the proxy gives a request body (a place is held that
	// long by a sender who is slow on purpose). Not every ResponseWriter can
	// (a test recorder): no matter.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(30 * time.Second))
	_ = rc.SetWriteDeadline(time.Now().Add(3 * time.Minute))

	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "the upload is not a form")
		return none, false
	}
	var (
		data         []byte
		reference    string
		haveFile     bool
		haveRef      bool
		refOK        = true
		maxReference = 4 * miniReferenceMax
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			miniUploadErr(w, err)
			return none, false
		}
		switch part.FormName() {
		case "receipt":
			if haveFile {
				writeError(w, http.StatusBadRequest, "one_receipt", "send one receipt")
				return none, false
			}
			haveFile = true
			// The request says how large it is: room for the picture at once, not a buffer that doubles its way up.
			var buf bytes.Buffer
			if n := r.ContentLength; n > 0 && n <= miniUploadBody {
				buf.Grow(int(n)) //nolint:gosec // G115: at most miniUploadBody
			}
			if _, err = buf.ReadFrom(io.LimitReader(part, receipt.MaxBytes+1)); err != nil {
				miniUploadErr(w, err)
				return none, false
			}
			data = buf.Bytes()
			if len(data) > receipt.MaxBytes { // no need to read what follows
				writeError(w, http.StatusRequestEntityTooLarge, "too_large", "the receipt is too large")
				return none, false
			}
		case "reference":
			if haveRef {
				writeError(w, http.StatusBadRequest, "invalid", "one reference number")
				return none, false
			}
			haveRef = true
			raw, err := io.ReadAll(io.LimitReader(part, int64(maxReference)+1))
			if err != nil {
				miniUploadErr(w, err)
				return none, false
			}
			reference, refOK = cleanReference(string(raw))
			refOK = refOK && len(raw) <= maxReference
		default:
			writeError(w, http.StatusBadRequest, "invalid", "the form has a field this call does not take")
			return none, false
		}
	}
	switch {
	case !haveFile:
		writeError(w, http.StatusBadRequest, "no_receipt", "send the receipt as a file")
		return none, false
	case !refOK:
		writeError(w, http.StatusBadRequest, "invalid", "the reference number is not valid")
		return none, false
	}
	info, err := receipt.Inspect(data)
	switch {
	case errors.Is(err, receipt.ErrEmpty):
		writeError(w, http.StatusBadRequest, "empty", "the receipt is empty")
		return none, false
	case errors.Is(err, receipt.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "the receipt is too large")
		return none, false
	case err != nil:
		writeError(w, http.StatusUnsupportedMediaType, "bad_type", "the receipt must be a JPEG, PNG or WebP image")
		return none, false
	}
	// A card transfer is matched against the bank statement by this number.
	if gi.Provider == domain.ManualCard && reference == "" {
		writeError(w, http.StatusBadRequest, "reference_required", "the bank reference number is required for a card transfer")
		return none, false
	}
	fileID, err := s.cfg.MiniAppBot.SendReceipt(ctx, botfiles.Receipt{
		ChatID: c.user.TelegramID, Lang: c.user.Language, Reference: domain.PaymentReference(gi.ID), ContentType: info.ContentType, Data: data,
	})
	if err != nil {
		s.miniBotFail(w, "receipt", err)
		return none, false
	}
	return domain.ProofParams{ReceiptFile: fileID, ReferenceNumber: reference}, true
}

// alertProof has the staff told about a proof that is now in the review list.
// The alert is the bot's (it reaches the staff); if it does not go out the
// payment is still in the list, so the failure is only logged. Nothing of the
// receipt is in the log.
func (s *Server) alertProof(ctx context.Context, u *store.User, gi *domain.GatewayIntent) {
	if s.cfg.MiniAppBot == nil {
		return
	}
	err := s.cfg.MiniAppBot.NotifyProof(context.WithoutCancel(ctx), botfiles.ProofNotice{
		TelegramID: u.TelegramID, Username: u.Username, IntentID: gi.ID, Amount: gi.Amount, Currency: gi.Currency,
	})
	if err != nil {
		s.cfg.Log.Warn("mini app: the staff were not alerted to a payment proof; it waits in the review list", "intent", gi.ID, "err", err)
	}
}
