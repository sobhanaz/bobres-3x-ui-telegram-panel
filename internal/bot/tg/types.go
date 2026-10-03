// Package tg is a small Telegram Bot API client: only the methods the bot
// uses, typed, with the token kept out of every error message.
package tg

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
	ID   int64  `json:"id"`
	Type string `json:"type"` // private | group | supergroup | channel
}

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
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// Sender returns the user who caused the update, or nil.
func (u *Update) Sender() *User {
	switch {
	case u.CallbackQuery != nil:
		return &u.CallbackQuery.From
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
