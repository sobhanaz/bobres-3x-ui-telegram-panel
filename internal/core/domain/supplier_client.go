package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The supplier's reseller API (docs/supplier.md): GET products, GET balance, POST order (with an external order id, so a
// retry returns the same order and charges nothing again). Core has no internet access: every call goes through the bot
// (botfiles.Client.PostSupplier). The supplier's answers are read tolerantly: the catalog is a list under "products" (or
// the root, "data", "items", "result"), each product has an id, a name, a description, a cost and a stock under the names
// below; what is not understood stays in the product's raw JSON for the owner to see.

// SupplierTransport carries one call to the supplier and brings back its status and body (the bot, in production).
type SupplierTransport interface {
	PostSupplier(ctx context.Context, action, apiKey string, body []byte) (status int, answer []byte, err error)
}

// SupplierError is an answer of the supplier that is not a success. A 4xx other than 408 and 429 is final.
type SupplierError struct {
	Status  int
	Code    string
	Message string
}

func (e *SupplierError) Error() string {
	return fmt.Sprintf("supplier: %d %s: %s", e.Status, e.Code, e.Message)
}

// Final reports whether retrying the same request can never succeed.
func (e *SupplierError) Final() bool {
	return e.Status >= 400 && e.Status < 500 && e.Status != http.StatusRequestTimeout && e.Status != http.StatusTooManyRequests
}

// SupplierProductInfo is a product as the supplier lists it.
type SupplierProductInfo struct {
	ID, Name, Description string
	CostMicro             *int64 // millionths of CostCurrency
	CostCurrency          string
	Stock                 *int32
	Raw                   json.RawMessage
}

// SupplierDelivery is what an order brought: the text a customer receives, and whether the supplier is still working on it.
type SupplierDelivery struct {
	Text    string
	Pending bool
	Raw     json.RawMessage
}

type supplierCall struct {
	tr  SupplierTransport
	key string
}

func (c supplierCall) do(ctx context.Context, action string, body []byte) (json.RawMessage, error) {
	status, data, err := c.tr.PostSupplier(ctx, action, c.key, body)
	if err != nil {
		return nil, fmt.Errorf("supplier: %w", err)
	}
	if status/100 != 2 {
		e := &SupplierError{Status: status}
		var b struct {
			Error   any    `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &b) == nil {
			if s, ok := b.Error.(string); ok {
				e.Code = clipStr(s, 64)
			}
			e.Message = clipStr(b.Message, 300)
		}
		return nil, e
	}
	return data, nil
}

func clipStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// SupplierBalance is the API account's balance at the supplier. Raw is the supplier's answer (clipped, the key taken out)
// and is set only when the amount could not be read from it.
type SupplierBalance struct {
	Amount   string
	Currency string
	Raw      string
}

// SupplierClient is what core needs from the supplier.
type SupplierClient interface {
	Products(ctx context.Context) ([]SupplierProductInfo, error)
	// Balance is the supplier balance as the supplier writes it (a number and its currency); when the answer is not
	// understood only Raw is set.
	Balance(ctx context.Context) (SupplierBalance, error)
	// Order buys quantity of a product with a unique external id; asking again with the same id returns the same order.
	Order(ctx context.Context, productID string, quantity int, externalID string) (*SupplierDelivery, error)
}

// NewSupplierClient builds the client that reaches the supplier through tr with the API key.
func NewSupplierClient(tr SupplierTransport, apiKey string) SupplierClient {
	return supplierCall{tr: tr, key: apiKey}
}

func (c supplierCall) Products(ctx context.Context) ([]SupplierProductInfo, error) {
	raw, err := c.do(ctx, "products", nil)
	if err != nil {
		return nil, err
	}
	return parseSupplierCatalog(raw)
}

func (c supplierCall) Balance(ctx context.Context) (SupplierBalance, error) {
	raw, err := c.do(ctx, "balance", nil)
	if err != nil {
		return SupplierBalance{}, err
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		if amount, cur := findBalance(v); amount != "" {
			return SupplierBalance{Amount: amount, Currency: cur}, nil
		}
	}
	// Not understood: the answer itself (it carries no key; the key is taken out anyway) so the owner can see what the
	// supplier says and the parser can be taught.
	shown := string(raw)
	if c.key != "" {
		shown = strings.ReplaceAll(shown, c.key, "***")
	}
	return SupplierBalance{Raw: clipStr(shown, 400)}, nil
}

// findBalance reads the balance out of the supplier's answer: a number or a text like "5.53 USDT" under a key that
// names a balance, at the top or up to three levels down ({"data":{"balance":...}}). The currency is the answer's own
// ("currency", or the letters after the number) and USDT when it names none.
func findBalance(v any) (amount, currency string) {
	exact := []string{"balance", "api_balance", "apibalance", "available_balance", "credit", "credits", "funds", "available", "amount"}
	level := []map[string]any{}
	if m, ok := v.(map[string]any); ok {
		level = append(level, m)
	}
	for depth := 0; depth < 4 && len(level) > 0; depth++ {
		var next []map[string]any
		for _, m := range level {
			// A key that is exactly a balance name first, then any key that mentions a balance.
			keys := append([]string{}, exact...)
			var rest []string
			for k := range m {
				if lk := strings.ToLower(k); strings.Contains(lk, "balance") && !slices.Contains(exact, lk) {
					rest = append(rest, k)
				}
			}
			sort.Strings(rest)
			for _, k := range append(keys, rest...) {
				mk := matchKey(m, k)
				if mk == "" {
					continue
				}
				switch x := m[mk].(type) {
				case float64:
					return strconv.FormatFloat(x, 'f', -1, 64), balanceCurrency(m, "")
				case string:
					if n, cur := splitAmount(x); n != "" {
						return n, balanceCurrency(m, cur)
					}
				case map[string]any:
					next = append(next, x)
				}
			}
			for _, k := range []string{"data", "result", "account", "api", "reseller", "wallet"} {
				if inner, ok := m[k].(map[string]any); ok {
					next = append(next, inner)
				}
			}
		}
		level = next
	}
	return "", ""
}

// matchKey is the key of m that equals k ignoring case ("" when none).
func matchKey(m map[string]any, k string) string {
	if _, ok := m[k]; ok {
		return k
	}
	for mk := range m {
		if strings.EqualFold(mk, k) {
			return mk
		}
	}
	return ""
}

var amountRe = regexp.MustCompile(`^\s*[$€£]?\s*(-?\d+(?:[.,]\d+)?)\s*([A-Za-z]{3,5})?\s*$`)

// splitAmount reads "5.53", "5.53 USDT" or "$5.53" (the number, and the currency letters when there are some).
func splitAmount(s string) (number, currency string) {
	m := amountRe.FindStringSubmatch(s)
	if m == nil {
		return "", ""
	}
	return strings.ReplaceAll(m[1], ",", "."), strings.ToUpper(m[2])
}

// balanceCurrency is the currency named next to the balance, else the one found in the number's text, else USDT.
func balanceCurrency(m map[string]any, fromText string) string {
	if c := strings.ToUpper(strFrom(m, "currency", "currency_code", "unit")); c != "" {
		return c
	}
	if fromText != "" {
		return fromText
	}
	return "USDT"
}

func (c supplierCall) Order(ctx context.Context, productID string, quantity int, externalID string) (*SupplierDelivery, error) {
	body, err := json.Marshal(map[string]any{"product_id": productID, "quantity": quantity, "external_order_id": externalID})
	if err != nil {
		return nil, err
	}
	raw, err := c.do(ctx, "order", body)
	if err != nil {
		return nil, err
	}
	return parseSupplierOrder(raw)
}

// ---- tolerant reading of the supplier's JSON

// firstObject is v if it is an object, else the first object in a list.
func firstObject(v any) map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return x
	case []any:
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				return m
			}
		}
	}
	return map[string]any{}
}

func strFrom(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case json.Number:
			return v.String()
		}
	}
	return ""
}

func numFrom(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return v, true
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

// catalogList finds the product list in an answer: the root array, or an array under a known key (also one level down).
func catalogList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any:
		for _, k := range []string{"products", "data", "items", "result", "catalog"} {
			if inner, ok := x[k]; ok {
				if l := catalogList(inner); l != nil {
					return l
				}
			}
		}
	}
	return nil
}

const maxSupplierProducts = 2000

func parseSupplierCatalog(raw json.RawMessage) ([]SupplierProductInfo, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, errors.New("supplier: the catalog is not JSON")
	}
	list := catalogList(v)
	if list == nil {
		return nil, errors.New("supplier: the catalog has no product list")
	}
	var out []SupplierProductInfo
	seen := map[string]bool{}
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		id := strFrom(m, "id", "product_id", "sku", "slug", "code")
		if id == "" || len(id) > 100 || seen[id] || strings.ContainsAny(id, " \t\r\n/\\") {
			continue
		}
		seen[id] = true
		p := SupplierProductInfo{ID: id, Name: clipStr(cleanSupplierText(strFrom(m, "name", "title", "product_name")), 300),
			Description: clipStr(cleanSupplierText(strFrom(m, "description", "desc", "details", "about")), 4000), CostCurrency: "USDT"}
		if f, ok := numFrom(m, "price", "cost", "wholesale_price", "wholesale", "amount", "unit_price"); ok && f >= 0 && f < 1e9 {
			micro := int64(math.Round(f * 1e6))
			p.CostMicro = &micro
		}
		if c := strings.ToUpper(strFrom(m, "currency", "currency_code")); len(c) >= 3 && len(c) <= 5 {
			p.CostCurrency = c
		}
		if f, ok := numFrom(m, "stock", "quantity", "available", "in_stock", "qty", "available_stock"); ok && f >= 0 && f < 1e9 {
			n := int32(f)
			p.Stock = &n
		} else if b, ok := m["in_stock"].(bool); ok && !b {
			zero := int32(0)
			p.Stock = &zero
		}
		if b, ok := m["active"].(bool); ok && !b {
			zero := int32(0) // a product the supplier switched off is out of stock for us
			p.Stock = &zero
		}
		if rawItem, err := json.Marshal(m); err == nil && len(rawItem) <= 16<<10 {
			p.Raw = rawItem
		}
		out = append(out, p)
		if len(out) >= maxSupplierProducts {
			break
		}
	}
	if len(out) == 0 {
		return nil, errors.New("supplier: the catalog has no usable product")
	}
	return out, nil
}

// premiumEmojiRe matches the placeholders of Telegram's custom (premium) emoji, "{pe:5244675575502628242}", which the
// supplier's texts carry from its own bot and which mean nothing anywhere else.
var premiumEmojiRe = regexp.MustCompile(`\{pe:\d+\}[ \t]*`)

// cleanSupplierText is a supplier text as people read it: without the custom-emoji placeholders, with plain line breaks.
func cleanSupplierText(s string) string {
	s = premiumEmojiRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(s)
}

// deliveryKeys are where an order's answer keeps what the customer receives, most specific first.
var deliveryKeys = []string{"delivery", "delivered", "deliverables", "accounts", "account", "credentials", "items", "codes", "keys", "license", "result", "content", "details", "data"}

// skipKeys are the order's bookkeeping, not part of what is delivered.
var skipKeys = map[string]bool{"id": true, "order_id": true, "external_order_id": true, "balance": true, "api_balance": true, "status": true, "success": true,
	"price": true, "cost": true, "total": true, "total_cost": true, "created_at": true, "updated_at": true, "product_id": true, "quantity": true, "currency": true, "message": true}

func parseSupplierOrder(raw json.RawMessage) (*SupplierDelivery, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, errors.New("supplier: the order answer is not JSON")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("supplier: the order answer is not an object")
	}
	if b, ok := m["success"].(bool); ok && !b {
		return nil, &SupplierError{Status: http.StatusUnprocessableEntity, Code: "failed", Message: clipStr(strFrom(m, "message", "error"), 300)}
	}
	if s, ok := m["error"].(string); ok && s != "" {
		return nil, &SupplierError{Status: http.StatusUnprocessableEntity, Code: clipStr(s, 64), Message: clipStr(strFrom(m, "message"), 300)}
	}
	status := strings.ToLower(strFrom(m, "status", "state"))
	switch status {
	case "failed", "error", "cancelled", "canceled", "rejected", "refunded", "out_of_stock":
		return nil, &SupplierError{Status: http.StatusUnprocessableEntity, Code: status, Message: clipStr(strFrom(m, "message", "error"), 300)}
	}
	text := deliveryText(m)
	pending := text == "" && (status == "pending" || status == "processing" || status == "queued" || status == "waiting" || status == "")
	return &SupplierDelivery{Text: text, Pending: pending, Raw: raw}, nil
}

// deliveryText is what the customer receives, read from the answer: a delivery field, else an "order" or "data" object's.
func deliveryText(m map[string]any) string {
	for _, k := range deliveryKeys {
		if v, ok := m[k]; ok {
			if s := render(v, 0); s != "" {
				return s
			}
		}
	}
	for _, k := range []string{"order", "data", "result"} {
		if inner, ok := m[k].(map[string]any); ok {
			if s := deliveryText(inner); s != "" {
				return s
			}
		}
	}
	// Nothing named: every other field that is not bookkeeping.
	return renderMap(m, true)
}

const maxDeliveryText = 3500 // a Telegram message is at most 4096 characters; the rest of the message is ours

func render(v any, depth int) string {
	if depth > 3 {
		return ""
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return ""
	case []any:
		var lines []string
		for _, e := range x {
			if s := render(e, depth+1); s != "" {
				lines = append(lines, s)
			}
		}
		return clipStr(strings.Join(lines, "\n\n"), maxDeliveryText)
	case map[string]any:
		return renderMap(x, false)
	}
	return ""
}

func renderMap(m map[string]any, skip bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		if skip && skipKeys[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		if s := render(m[k], 1); s != "" {
			if strings.Contains(s, "\n") {
				lines = append(lines, k+":\n"+s)
			} else {
				lines = append(lines, k+": "+s)
			}
		}
	}
	return clipStr(strings.Join(lines, "\n"), maxDeliveryText)
}
