// Package tg is a small Telegram Bot API client: only the methods the bot
// uses, typed, with the token kept out of every error message.
package tg

import "strings"

// User is a Telegram user or bot.
type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
}

// Chat is where a message lives.
type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`               // private | group | supergroup | channel
	Title    string `json:"title,omitempty"`    // groups and channels
	Username string `json:"username,omitempty"` // public chats
}

// ChatMember is a user's membership in a chat (getChatMember).
type ChatMember struct {
	// Status: creator, administrator, member, restricted, left or kicked.
	Status string `json:"status"`
	// IsMember: whether a restricted user is still in the chat.
	IsMember bool `json:"is_member,omitempty"`
	User     User `json:"user"`
}

// Joined reports whether the user is in the chat.
func (m *ChatMember) Joined() bool {
	switch m.Status {
	case "creator", "administrator", "member":
		return true
	case "restricted":
		return m.IsMember
	}
	return false
}

// IsAdmin reports whether the user administers the chat.
func (m *ChatMember) IsAdmin() bool { return m.Status == "creator" || m.Status == "administrator" }

// PhotoSize is one resolution of a photo.
type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int    `json:"file_size,omitempty"`
}

// Message is a chat message.
type Message struct {
	MessageID int         `json:"message_id"`
	From      *User       `json:"from,omitempty"`
	Chat      Chat        `json:"chat"`
	Date      int64       `json:"date"`
	Text      string      `json:"text,omitempty"`
	Caption   string      `json:"caption,omitempty"`
	Photo     []PhotoSize `json:"photo,omitempty"`
	// Document: a file, e.g. a screenshot sent "as file" (uncompressed).
	Document *Document `json:"document,omitempty"`
	// SuccessfulPayment: a Telegram Stars payment completed.
	SuccessfulPayment *SuccessfulPayment `json:"successful_payment,omitempty"`
}

// Document is a file attached to a message.
type Document struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// ImageFile returns the file id of a picture in the message: the largest photo
// size, or an image sent as a file. "" when there is none.
func (m *Message) ImageFile() string {
	if id := m.LargestPhoto(); id != "" {
		return id
	}
	if d := m.Document; d != nil && strings.HasPrefix(strings.ToLower(d.MimeType), "image/") {
		return d.FileID
	}
	return ""
}

// SuccessfulPayment is the service message after a completed payment.
type SuccessfulPayment struct {
	Currency                string `json:"currency"`
	TotalAmount             int64  `json:"total_amount"`
	InvoicePayload          string `json:"invoice_payload"`
	TelegramPaymentChargeID string `json:"telegram_payment_charge_id"`
	ProviderPaymentChargeID string `json:"provider_payment_charge_id,omitempty"`
}

// PreCheckoutQuery asks the bot to confirm a payment before Telegram charges
// it; it must be answered within 10 seconds.
type PreCheckoutQuery struct {
	ID             string `json:"id"`
	From           User   `json:"from"`
	Currency       string `json:"currency"`
	TotalAmount    int64  `json:"total_amount"`
	InvoicePayload string `json:"invoice_payload"`
}

// LabeledPrice is one invoice line (Stars invoices have exactly one).
type LabeledPrice struct {
	Label  string `json:"label"`
	Amount int64  `json:"amount"`
}

// Invoice is a Telegram Stars invoice (currency XTR).
type Invoice struct {
	Title       string // 1-32 characters
	Description string // 1-255 characters
	Payload     string // 1-128 bytes, opaque to the user
	Stars       int64
	// StartParameter non-empty: a forwarded copy cannot be paid by someone else.
	StartParameter string
}

// LargestPhoto returns the file id of the biggest photo size, or "".
func (m *Message) LargestPhoto() string {
	best, id := -1, ""
	for _, p := range m.Photo {
		if p.Width*p.Height > best {
			best, id = p.Width*p.Height, p.FileID
		}
	}
	return id
}

// CallbackQuery is a press on an inline keyboard button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// Update is one incoming event.
type Update struct {
	UpdateID         int64             `json:"update_id"`
	Message          *Message          `json:"message,omitempty"`
	CallbackQuery    *CallbackQuery    `json:"callback_query,omitempty"`
	PreCheckoutQuery *PreCheckoutQuery `json:"pre_checkout_query,omitempty"`
}

// Sender returns the user who caused the update, or nil.
func (u *Update) Sender() *User {
	switch {
	case u.CallbackQuery != nil:
		return &u.CallbackQuery.From
	case u.PreCheckoutQuery != nil:
		return &u.PreCheckoutQuery.From
	case u.Message != nil:
		return u.Message.From
	default:
		return nil
	}
}

// Button is one inline keyboard button: either callback data or a URL.
type Button struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// Keyboard is an inline keyboard: rows of buttons.
type Keyboard struct {
	InlineKeyboard [][]Button `json:"inline_keyboard"`
}

// Row appends a row of buttons and returns the keyboard (for chaining).
func (k *Keyboard) Row(buttons ...Button) *Keyboard {
	if len(buttons) > 0 {
		k.InlineKeyboard = append(k.InlineKeyboard, buttons)
	}
	return k
}

// CB is a callback button.
func CB(text, data string) Button { return Button{Text: text, CallbackData: data} }

// Link is a URL button.
func Link(text, url string) Button { return Button{Text: text, URL: url} }
