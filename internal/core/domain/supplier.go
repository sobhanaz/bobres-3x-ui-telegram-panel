package domain

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Reselling a supplier's digital accounts from the owner's own store.
//
// The catalog is read from the supplier (through the bot: core has no internet access) and imported OFF: the owner
// writes the price in Toman and the texts of each product and switches it on. A customer pays from the wallet, and the
// purchase is made the way a license is (vendor.go): ONE transaction takes the money and writes the order; the supplier
// is asked outside it with the order's id as its external order id, so asking again never charges twice; the answer
// (the account) is kept encrypted and handed over; a server that does not answer leaves the order paid and a worker asks
// again; a refusal for good gives the money back.

// SettingSupplierEnabled is the owner's switch for the "Digital accounts" button of the bot (off unless true).
const SettingSupplierEnabled = "supplier.enabled"

// SupplierCurrency is what a supplier product is sold for: Toman, the wallet's own currency.
const SupplierCurrency = "IRT"

const (
	supplierMaxPrice     = 1_000_000_000_000
	supplierMaxQuantity  = 10
	supplierSettleBatch  = 20
	supplierFirstWait    = 30 * time.Second
	supplierMaxWait      = time.Hour
	supplierAttemptsCap  = 7
	supplierSyncInterval = 30 * time.Minute
)

var supplierKeyRe = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]{8,300}$`)

// SupplierAvailable reports whether core can resell at all: it has the master key that protects the API key and the
// delivered accounts.
func (s *Service) SupplierAvailable() bool { return s.cfg.Secrets != nil }

// SetSupplierTransport sets the way to the supplier (the bot, in cmd/core).
func (s *Service) SetSupplierTransport(tr SupplierTransport) { s.cfg.SupplierTransport = tr }

// SupplierConfigured reports whether an API key is stored.
func (s *Service) SupplierConfigured(ctx context.Context) bool {
	_, err := s.st.GetSupplierKey(ctx, s.st.Conn())
	return err == nil
}

// SupplierOn reports whether the resale is on: the owner switched it on and set the API key.
func (s *Service) SupplierOn(ctx context.Context) bool {
	return s.SupplierAvailable() && s.PrivateFeatures(ctx) && s.setting(ctx, SettingSupplierEnabled) == "true" && s.SupplierConfigured(ctx)
}

// SetSupplierKey stores the supplier's API key, encrypted. It is write-only: nothing returns it. The audit says who
// changed it, never the key.
func (s *Service) SetSupplierKey(ctx context.Context, actor *store.User, apiKey string) error {
	if err := s.requirePrivate(ctx); err != nil {
		return err
	}
	if s.cfg.Secrets == nil {
		return stateErr("core has no master key, so it cannot keep the API key")
	}
	apiKey = strings.TrimSpace(apiKey)
	if !supplierKeyRe.MatchString(apiKey) {
		return &FieldError{"api_key", "not an API key (8 to 300 letters, digits and . _ ~ + / = -)"}
	}
	enc, err := s.cfg.Secrets.Encrypt([]byte(apiKey))
	if err != nil {
		return err
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.SetSupplierKey(ctx, tx, enc); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "supplier.config", "supplier", "config", map[string]any{"api_key": "changed"}, "")
	})
}

func (s *Service) supplierClient(ctx context.Context) (SupplierClient, error) {
	if s.cfg.Secrets == nil {
		return nil, stateErr("core has no master key")
	}
	enc, err := s.st.GetSupplierKey(ctx, s.st.Conn())
	if errors.Is(err, store.ErrNotFound) {
		return nil, stateErr("the supplier's API key is not set")
	}
	if err != nil {
		return nil, err
	}
	raw, err := s.cfg.Secrets.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("the stored API key cannot be read: %w", err)
	}
	if s.cfg.SupplierTransport == nil {
		return nil, stateErr("core has no way to reach the supplier (the bot)")
	}
	return NewSupplierClient(s.cfg.SupplierTransport, string(raw)), nil
}

// SupplierBalance is the balance of the API account at the supplier, as the supplier writes it.
func (s *Service) SupplierBalance(ctx context.Context) (amount, currency string, err error) {
	cl, err := s.supplierClient(ctx)
	if err != nil {
		return "", "", err
	}
	return cl.Balance(ctx)
}

// SyncSupplierCatalog reads the supplier's catalog into the store (new products come in OFF) and returns how many
// products it listed.
func (s *Service) SyncSupplierCatalog(ctx context.Context) (int, error) {
	if !s.PrivateFeatures(ctx) {
		return 0, nil // the worker's half-hourly read does nothing on a store without the feature
	}
	cl, err := s.supplierClient(ctx)
	if err != nil {
		return 0, err
	}
	list, err := cl.Products(ctx)
	if err != nil {
		return 0, err
	}
	rows := make([]store.SupplierProduct, 0, len(list))
	for _, p := range list {
		var stock *int32
		if p.Stock != nil {
			v := *p.Stock
			stock = &v
		}
		rows = append(rows, store.SupplierProduct{ID: p.ID, Name: p.Name, Description: p.Description, CostMicro: p.CostMicro, CostCurrency: p.CostCurrency, Stock: stock, Raw: p.Raw})
	}
	return len(rows), s.st.WithTx(ctx, func(tx pgx.Tx) error { return s.st.SyncSupplierCatalog(ctx, tx, rows) })
}

// ListSupplierProducts is every product of the catalog as read, with the owner's choices.
func (s *Service) ListSupplierProducts(ctx context.Context) ([]store.SupplierProduct, error) {
	return s.st.ListSupplierProducts(ctx, s.st.Conn())
}

// SupplierEdit is what the owner may change of a product (nil: keep). Title and Desc are per language (fa, en); a
// language left out or empty keeps the supplier's text.
type SupplierEdit struct {
	Enabled   *bool
	SellPrice *int64 // Toman; 0 clears it
	Title     map[string]string
	Desc      map[string]string
	Sort      *int32
}

func cleanI18n(in map[string]string, max int) (map[string]string, error) {
	if in == nil {
		return nil, nil
	}
	out := map[string]string{}
	for lang, v := range in {
		if lang != "fa" && lang != "en" {
			return nil, invalid("language %q is fa or en", lang)
		}
		v = strings.TrimSpace(v)
		if utf8.RuneCountInString(v) > max {
			return nil, invalid("text is at most %d characters", max)
		}
		if v != "" {
			out[lang] = v
		}
	}
	return out, nil
}

// UpdateSupplierProduct applies the owner's edit: the price, the texts, the order and whether it is sold. A product is
// switched on only with a price.
func (s *Service) UpdateSupplierProduct(ctx context.Context, actor *store.User, id string, e SupplierEdit) (*store.SupplierProduct, error) {
	if e.SellPrice != nil && (*e.SellPrice < 0 || *e.SellPrice > supplierMaxPrice) {
		return nil, &FieldError{"sell_price", "a price in Toman, 1 to 1 000 000 000 000 (0 clears it)"}
	}
	title, err := cleanI18n(e.Title, 200)
	if err != nil {
		return nil, err
	}
	desc, err := cleanI18n(e.Desc, 2000)
	if err != nil {
		return nil, err
	}
	var out *store.SupplierProduct
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.st.GetSupplierProduct(ctx, tx, id, true)
		if err != nil {
			return err
		}
		price := before.SellPrice
		if e.SellPrice != nil {
			if *e.SellPrice == 0 {
				price = nil
			} else {
				price = e.SellPrice
			}
		}
		if e.Enabled != nil && *e.Enabled && price == nil {
			return &FieldError{"sell_price", "set a price before selling this product"}
		}
		if price == nil && before.Enabled && (e.Enabled == nil || *e.Enabled) {
			return &FieldError{"sell_price", "a product on sale needs a price: switch it off first"}
		}
		if out, err = s.st.UpdateSupplierProduct(ctx, tx, id, store.SupplierProductEdit{Enabled: e.Enabled, SellPrice: e.SellPrice, Title: title, Desc: desc, Sort: e.Sort}); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "supplier.product", "supplier_product", id,
			map[string]any{"enabled": before.Enabled, "sell_price": before.SellPrice}, map[string]any{"enabled": out.Enabled, "sell_price": out.SellPrice}, "")
	})
	return out, err
}

// SupplierOffers are the products a customer may buy now, in the owner's order.
func (s *Service) SupplierOffers(ctx context.Context) ([]store.SupplierProduct, error) {
	if !s.SupplierOn(ctx) {
		return nil, nil
	}
	return s.st.ListSupplierOffers(ctx, s.st.Conn())
}

// SupplierBuyParams carries the inputs of BuySupplierProduct.
type SupplierBuyParams struct {
	UserID, ProductID string
	Quantity          int
	// IdempotencyKey makes a retry or a double tap return the first result (required).
	IdempotencyKey string
	// ExpectedAmount, when positive, is the total the buyer was shown: refused if the price came to another.
	ExpectedAmount int64
}

// SupplierPurchase is the state of a purchase.
type SupplierPurchase struct {
	Order store.SupplierOrder
	// Delivery is what the customer receives (the account), once the order is done.
	Delivery string
	Replayed bool
}

// Pending reports that the customer has paid and the supplier has not delivered yet.
func (p *SupplierPurchase) Pending() bool { return p.Order.Status == "paid" }

// BuySupplierProduct sells a product for the buyer's wallet.
func (s *Service) BuySupplierProduct(ctx context.Context, p SupplierBuyParams) (*SupplierPurchase, error) {
	if !s.SupplierOn(ctx) {
		return nil, stateErr("digital accounts are not for sale")
	}
	if s.cfg.SupplierTransport == nil {
		return nil, stateErr("core has no way to reach the supplier, so it sells nothing") // before any money moves
	}
	if p.IdempotencyKey == "" || len(p.IdempotencyKey) > 180 {
		return nil, invalid("an idempotency key is required (at most 180 characters)")
	}
	if p.Quantity < 1 || p.Quantity > supplierMaxQuantity {
		return nil, invalid("quantity is 1 to %d", supplierMaxQuantity)
	}
	u, err := s.st.GetUser(ctx, s.st.Conn(), p.UserID)
	if err != nil {
		return nil, err
	}
	if u.Status == "banned" {
		return nil, ErrUserBanned
	}
	prod, err := s.st.GetSupplierProduct(ctx, s.st.Conn(), p.ProductID, false)
	if errors.Is(err, store.ErrNotFound) {
		return nil, invalid("no such product")
	}
	if err != nil {
		return nil, err
	}
	if !prod.Enabled || !prod.Listed || prod.SellPrice == nil || (prod.Stock != nil && *prod.Stock < int32(p.Quantity)) { //nolint:gosec // 1..10
		return nil, stateErr("this product is not available now")
	}
	amount := *prod.SellPrice * int64(p.Quantity)
	if p.ExpectedAmount > 0 && p.ExpectedAmount != amount {
		return nil, stateErr("the price changed: look at it again")
	}
	order := &store.SupplierOrder{UserID: u.ID, ProductID: prod.ID, Quantity: p.Quantity, Amount: amount, Currency: SupplierCurrency,
		IdempotencyKey: "supplier:" + p.IdempotencyKey}
	var got *store.SupplierOrder
	var inserted bool
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		if got, inserted, err = s.st.CreateSupplierOrder(ctx, tx, order); err != nil || !inserted {
			return err
		}
		_, err = s.st.Debit(ctx, tx, u.ID, got.Currency, got.Amount, &store.LedgerEntry{
			Kind: "supplier_purchase", RefType: strptr("supplier_order"), RefID: strptr(got.ID), IdempotencyKey: "supplier_purchase:" + got.ID,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	if inserted {
		if got, err = s.fulfillSupplierOrder(ctx, got.ID); err != nil {
			s.cfg.Log.Warn("supplier order not delivered yet", "order", got.ID, "err", err)
		}
	}
	return s.supplierPurchase(ctx, got, !inserted)
}

type supplierDeliveryBlob struct {
	Text string          `json:"text"`
	Raw  json.RawMessage `json:"raw,omitempty"`
}

func (s *Service) supplierPurchase(ctx context.Context, o *store.SupplierOrder, replayed bool) (*SupplierPurchase, error) {
	out := &SupplierPurchase{Order: *o, Replayed: replayed}
	if o.Status == "done" && len(o.DeliveryEnc) > 0 {
		text, err := s.decryptDelivery(o)
		if err != nil {
			return nil, err
		}
		out.Delivery = text
	}
	return out, nil
}

func (s *Service) decryptDelivery(o *store.SupplierOrder) (string, error) {
	raw, err := s.cfg.Secrets.Decrypt(o.DeliveryEnc)
	if err != nil {
		return "", err
	}
	var b supplierDeliveryBlob
	if err := json.Unmarshal(raw, &b); err != nil {
		return "", err
	}
	return b.Text, nil
}

// MySupplierOrders lists a customer's purchases, newest first (never what was delivered).
func (s *Service) MySupplierOrders(ctx context.Context, userID string) ([]store.SupplierOrder, error) {
	return s.st.UserSupplierOrders(ctx, s.st.Conn(), userID)
}

// SupplierDelivery is what a customer's done order delivered, to show it again.
func (s *Service) SupplierDelivery(ctx context.Context, userID, orderID string) (*SupplierPurchase, error) {
	if s.cfg.Secrets == nil {
		return nil, stateErr("core has no master key")
	}
	o, err := s.st.GetSupplierOrder(ctx, s.st.Conn(), orderID, false)
	if err != nil {
		return nil, err
	}
	if o.UserID != userID {
		return nil, ErrForbidden
	}
	return s.supplierPurchase(ctx, o, true)
}

func supplierWait(attempts int) time.Duration {
	return min(supplierFirstWait<<min(attempts, supplierAttemptsCap), supplierMaxWait)
}

// fulfillSupplierOrder asks the supplier for what a paid order bought and records the answer. It is safe to call again and
// again (the supplier takes the order id once, and the final write checks the order is still paid under its lock).
func (s *Service) fulfillSupplierOrder(ctx context.Context, orderID string) (*store.SupplierOrder, error) {
	o, err := s.st.GetSupplierOrder(ctx, s.st.Conn(), orderID, false)
	if err != nil || o.Status != "paid" {
		return o, err
	}
	cl, err := s.supplierClient(ctx)
	var d *SupplierDelivery
	if err == nil {
		d, err = cl.Order(ctx, o.ProductID, o.Quantity, o.ID)
	}
	if err == nil && d.Pending {
		err = errors.New("the supplier has not delivered yet")
	}
	if err != nil {
		var se *SupplierError
		if errors.As(err, &se) && se.Final() {
			s.cfg.Log.Warn("the supplier refused an order; refunding", "order", o.ID, "status", se.Status, "code", se.Code)
			return s.refundSupplierOrder(ctx, o.ID, se.Error())
		}
		next := s.cfg.vendorNow().Add(supplierWait(o.Attempts))
		if derr := s.st.DeferSupplierOrder(ctx, s.st.Conn(), o.ID, next, trimErr(err)); derr != nil {
			s.cfg.Log.Error("supplier order: recording the failed attempt", "order", o.ID, "err", derr)
		}
		return o, err
	}
	blob, err := json.Marshal(supplierDeliveryBlob{Text: d.Text, Raw: d.Raw})
	if err != nil {
		return o, err
	}
	enc, err := s.cfg.Secrets.Encrypt(blob)
	if err != nil {
		return o, err
	}
	var out *store.SupplierOrder
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		cur, err := s.st.GetSupplierOrder(ctx, tx, orderID, true)
		if err != nil {
			return err
		}
		out = cur
		if cur.Status != "paid" {
			return nil // another run finished it first
		}
		if err := s.st.FinishSupplierOrder(ctx, tx, cur.ID, "done", enc, ""); err != nil {
			return err
		}
		cur.Status, cur.DeliveryEnc = "done", enc
		return nil
	})
	return out, err
}

// refundSupplierOrder gives the money back and closes the order as refunded.
func (s *Service) refundSupplierOrder(ctx context.Context, orderID, why string) (*store.SupplierOrder, error) {
	if len(why) > 480 {
		why = why[:480]
	}
	var out *store.SupplierOrder
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		cur, err := s.st.GetSupplierOrder(ctx, tx, orderID, true)
		if err != nil {
			return err
		}
		out = cur
		if cur.Status != "paid" {
			return nil
		}
		if _, err := s.st.Credit(ctx, tx, cur.UserID, cur.Currency, cur.Amount, &store.LedgerEntry{
			Kind: "supplier_refund", RefType: strptr("supplier_order"), RefID: strptr(cur.ID), IdempotencyKey: "supplier_refund:" + cur.ID,
		}); err != nil && !errors.Is(err, store.ErrDuplicateIdempotency) {
			return err
		}
		if err := s.st.FinishSupplierOrder(ctx, tx, cur.ID, "refunded", nil, why); err != nil {
			return err
		}
		cur.Status, cur.LastError = "refunded", why
		return nil
	})
	return out, err
}

// RefundSupplierOrder is the owner's refund of an order still waiting for the supplier (one the supplier never delivers):
// the money goes back to the customer's wallet. Do it only when the supplier did not charge for it.
func (s *Service) RefundSupplierOrder(ctx context.Context, actor *store.User, orderID, reason string) (*store.SupplierOrder, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 400 {
		return nil, invalid("a reason is required (at most 400 characters)")
	}
	o, err := s.refundSupplierOrder(ctx, orderID, "refunded by the owner: "+reason)
	if err != nil {
		return nil, err
	}
	if o.Status != "refunded" {
		return nil, stateErr("only an order that waits for the supplier can be refunded")
	}
	if err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		return s.audit(ctx, tx, actor, "supplier.refund", "supplier_order", orderID, map[string]any{"amount": o.Amount}, reason)
	}); err != nil {
		return nil, err
	}
	return o, nil
}

// RetrySupplierOrder asks the supplier again for a waiting order now.
func (s *Service) RetrySupplierOrder(ctx context.Context, orderID string) (*store.SupplierOrder, error) {
	return s.fulfillSupplierOrder(ctx, orderID)
}

// ListSupplierOrdersAdmin lists every order (never what was delivered), newest first.
func (s *Service) ListSupplierOrdersAdmin(ctx context.Context, status string, p store.Page) ([]store.SupplierOrderRow, int64, error) {
	if status != "" && status != "paid" && status != "done" && status != "refunded" {
		return nil, 0, invalid("status is paid, done or refunded")
	}
	return s.st.ListSupplierOrders(ctx, s.st.Conn(), status, p)
}

// SettleSupplierOrders asks the supplier again for paid orders that wait (the worker's step).
func (s *Service) SettleSupplierOrders(ctx context.Context) (int, error) {
	if s.cfg.Secrets == nil || s.cfg.SupplierTransport == nil {
		return 0, nil
	}
	due, err := s.st.DueSupplierOrders(ctx, s.st.Conn(), s.cfg.vendorNow(), supplierSettleBatch)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range due {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		n++
		if _, err := s.fulfillSupplierOrder(ctx, id); err != nil {
			s.cfg.Log.Warn("supplier order still waiting", "order", id, "err", err)
		}
	}
	return n, nil
}

// RunSupplierWorker settles waiting orders every half minute and reads the catalog every half hour, until ctx ends.
func (s *Service) RunSupplierWorker(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	lastSync := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.SettleSupplierOrders(ctx); err != nil && ctx.Err() == nil {
				s.cfg.Log.Warn("supplier orders", "err", err)
			}
			if s.SupplierOn(ctx) && time.Since(lastSync) >= supplierSyncInterval {
				lastSync = time.Now()
				if _, err := s.SyncSupplierCatalog(ctx); err != nil && ctx.Err() == nil {
					s.cfg.Log.Warn("supplier catalog", "err", err)
				}
			}
		}
	}
}

// SupplierSID is the short id of a product that the bot puts in its buttons (a callback is at most 64 bytes, a supplier's
// id may be longer): ten letters and digits of the id's SHA-256.
func SupplierSID(id string) string {
	sum := sha256.Sum256([]byte("bobres:supplier:" + id))
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])[:10])
}

// SupplierProductBySID finds the product of a short id among those on sale.
func (s *Service) SupplierProductBySID(ctx context.Context, sid string) (*store.SupplierProduct, error) {
	offers, err := s.SupplierOffers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range offers {
		if SupplierSID(offers[i].ID) == sid {
			return &offers[i], nil
		}
	}
	return nil, store.ErrNotFound
}

// SupplierName is a product's name for a language: the owner's, else the supplier's.
func SupplierName(p *store.SupplierProduct, lang string) string {
	if v := p.Title[lang]; v != "" {
		return v
	}
	if v := p.Title["fa"]; v != "" && lang != "en" {
		return v
	}
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

// SupplierDescription is a product's description for a language: the owner's text only (the supplier's own text is for the
// owner to read, not for customers).
func SupplierDescription(p *store.SupplierProduct, lang string) string {
	if v := p.Desc[lang]; v != "" {
		return v
	}
	if lang != "en" {
		return p.Desc["fa"]
	}
	return ""
}
