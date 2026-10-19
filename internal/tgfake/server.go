// Package tgfake is a fake Telegram Bot API for tests and the CI end-to-end
// run: it answers the methods the bot uses, hands out injected updates
// through getUpdates, and records everything the bot sends (and, apart, the
// calls that set its own profile).
//
// Besides the Bot API paths (/bot<token>/<method>) it serves three helpers:
// POST /_inject (an Update without update_id), GET /_sent (the recorded
// outgoing messages as JSON) and POST /_fail (a Failure for one chat, as
// {"chat_id", "code", "description", "retry_after", "times"}).
package tgfake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// Sent is one outgoing message the bot produced. For edits MessageID is the
// edited message, so replaying the log gives each message's current state.
type Sent struct {
	Method    string          `json:"method"`
	ChatID    int64           `json:"chat_id"`
	MessageID int             `json:"message_id"`
	Text      string          `json:"text"`
	Markup    json.RawMessage `json:"reply_markup,omitempty"`
	// Stars invoices (sendInvoice) and pre-checkout answers.
	Payload string `json:"payload,omitempty"`
	Stars   int64  `json:"stars,omitempty"`
	QueryID string `json:"query_id,omitempty"`
	OK      *bool  `json:"ok,omitempty"`
	// An uploaded file (multipart sendDocument or sendPhoto): its name,
	// size and SHA-256 (hex). Text then holds the caption.
	FileName   string `json:"file_name,omitempty"`
	FileSize   int64  `json:"file_size,omitempty"`
	FileSHA256 string `json:"file_sha256,omitempty"`
}

// ProfileCall is a call that sets the bot's own profile (setMyCommands,
// deleteMyCommands, setMyDescription, setMyShortDescription,
// setMyProfilePhoto, removeMyProfilePhoto): its parameters as sent, and for
// a photo the uploaded file's name, size and SHA-256 (hex).
type ProfileCall struct {
	Method     string         `json:"method"`
	Params     map[string]any `json:"params,omitempty"`
	FileName   string         `json:"file_name,omitempty"`
	FileSize   int64          `json:"file_size,omitempty"`
	FileSHA256 string         `json:"file_sha256,omitempty"`
}

// Server is the fake API state.
type Server struct {
	mu       sync.Mutex
	updates  []tg.Update
	sent     []Sent
	profile  []ProfileCall
	nextUpd  int64
	nextMsg  int
	webhook  string
	notified chan struct{}
	files    map[string][]byte // by file id, served by getFile and /file/
	fails    map[int64]*Failure
	// photo is the bot's current profile photo (nil: none), which
	// getUserProfilePhotos returns for the bot itself; photos counts the
	// photos set, each a new one, like Telegram's.
	photo  []tg.PhotoSize
	photos int
}

// BotID is the fake bot's own user id (getMe).
const BotID = 1000

// New returns an empty fake.
func New() *Server {
	return &Server{nextUpd: 1, nextMsg: 1, notified: make(chan struct{}, 1), files: map[string][]byte{},
		fails: map[int64]*Failure{}}
}

// Failure is what Telegram answers a send or edit to one chat with,
// instead of delivering it.
type Failure struct {
	Code        int    `json:"code"`
	Description string `json:"description"`
	RetryAfter  int    `json:"retry_after,omitempty"` // seconds, with a 429
	// Times is how many calls fail before the chat works again (0: until
	// ClearChat).
	Times int `json:"times,omitempty"`
}

// Blocked is the user having blocked the bot (403).
func Blocked() Failure {
	return Failure{Code: 403, Description: "Forbidden: bot was blocked by the user"}
}

// ChatNotFound is a chat Telegram does not know (the account is gone).
func ChatNotFound() Failure { return Failure{Code: 400, Description: "Bad Request: chat not found"} }

// Flood is a 429 asking to wait retryAfter seconds, for the next times calls.
func Flood(retryAfter, times int) Failure {
	return Failure{Code: 429, Description: "Too Many Requests: retry after " + strconv.Itoa(retryAfter), RetryAfter: retryAfter, Times: times}
}

// BadRequest is any other refusal of the message (400).
func BadRequest(desc string) Failure { return Failure{Code: 400, Description: "Bad Request: " + desc} }

// FailChat makes sends and edits to chatID fail with f.
func (s *Server) FailChat(chatID int64, f Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fails[chatID] = &f
}

// ClearChat makes chatID work again.
func (s *Server) ClearChat(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fails, chatID)
}

// failure is the failure due for a call to chatID, if any (counting it).
func (s *Server) failure(chatID int64) *Failure {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.fails[chatID]
	if !ok {
		return nil
	}
	out := *f
	if f.Times > 0 {
		if f.Times--; f.Times == 0 {
			delete(s.fails, chatID)
		}
	}
	return &out
}

// AddFile makes a file available by id (a photo a user sent).
func (s *Server) AddFile(fileID string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[fileID] = data
}

// Inject queues an update for getUpdates and returns its id.
func (s *Server) Inject(u tg.Update) int64 {
	s.mu.Lock()
	u.UpdateID = s.nextUpd
	s.nextUpd++
	s.updates = append(s.updates, u)
	s.mu.Unlock()
	select {
	case s.notified <- struct{}{}:
	default:
	}
	return u.UpdateID
}

// Sent returns a copy of everything sent so far.
func (s *Server) Sent() []Sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Sent(nil), s.sent...)
}

// Profile returns a copy of the profile calls so far.
func (s *Server) Profile() []ProfileCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ProfileCall(nil), s.profile...)
}

// ProfilePhoto returns the identity of the bot's current profile photo (the
// file_unique_id of its largest size), "" when it has none.
func (s *Server) ProfilePhoto() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.photo) == 0 {
		return ""
	}
	return s.photo[len(s.photo)-1].FileUniqueID
}

// SetProfilePhoto gives the bot a new profile photo the way @BotFather
// does, outside the Bot API, and returns its identity.
func (s *Server) SetProfilePhoto() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.newPhoto()
}

// newPhoto makes a new current profile photo, in three sizes like
// Telegram's (the largest last), and returns its identity. Call with mu
// held.
func (s *Server) newPhoto() string {
	s.photos++
	id := "fake-profile-photo-" + strconv.Itoa(s.photos)
	s.photo = nil
	for _, side := range []int{160, 320, 640} {
		uid := id + "-" + strconv.Itoa(side)
		s.photo = append(s.photo, tg.PhotoSize{FileID: uid + "-file", FileUniqueID: uid, Width: side, Height: side})
	}
	return s.photo[len(s.photo)-1].FileUniqueID
}

// Webhook returns the URL set by setWebhook ("" after deleteWebhook).
func (s *Server) Webhook() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.webhook
}

// Handler serves the fake.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/_inject", s.inject)
	mux.HandleFunc("/_sent", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Sent())
	})
	mux.HandleFunc("/_fail", func(w http.ResponseWriter, r *http.Request) {
		var f struct {
			ChatID int64 `json:"chat_id"`
			Failure
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&f) != nil || f.ChatID == 0 {
			http.Error(w, `POST {"chat_id", "code", "description", "retry_after", "times"}`, http.StatusBadRequest)
			return
		}
		if f.Code == 0 {
			s.ClearChat(f.ChatID)
		} else {
			s.FailChat(f.ChatID, f.Failure)
		}
		reply(w, true)
	})
	mux.HandleFunc("/file/", s.file)
	mux.HandleFunc("/", s.api)
	return mux
}

// file serves /file/bot<token>/files/<file id>, the path getFile returns.
func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/file/"), "/", 3)
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "bot") || parts[1] != "files" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	data, ok := s.files[parts[2]]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(data)
}

func reply(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func fail(w http.ResponseWriter, code int, desc string) {
	failWith(w, Failure{Code: code, Description: desc})
}

func failWith(w http.ResponseWriter, f Failure) {
	body := map[string]any{"ok": false, "error_code": f.Code, "description": f.Description}
	if f.RetryAfter > 0 {
		body["parameters"] = map[string]any{"retry_after": f.RetryAfter}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.Code)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) inject(w http.ResponseWriter, r *http.Request) {
	var u tg.Update
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&u) != nil {
		http.Error(w, "POST an Update JSON", http.StatusBadRequest)
		return
	}
	id := s.Inject(u)
	reply(w, map[string]int64{"update_id": id})
}

// upload is a file part of a multipart request, read as it streams in.
type upload struct {
	field  string // the part's form name
	name   string
	size   int64
	sha256 string
}

// maxField bounds a multipart text field (a caption or a keyboard).
const maxField = 1 << 20

// params reads JSON or form/multipart parameters into a flat map. A
// multipart request is streamed: a file part is counted and hashed, never
// kept, so a large upload costs no memory or disk. A multipart form cut
// short (no closing boundary) is an error.
func params(r *http.Request) (map[string]any, *upload, error) {
	m := map[string]any{}
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/json"):
		_ = json.NewDecoder(r.Body).Decode(&m)
	case strings.HasPrefix(ct, "multipart/form-data"):
		mr, err := r.MultipartReader()
		if err != nil {
			return m, nil, nil
		}
		var file *upload
		for {
			p, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				return m, file, nil
			}
			if err != nil {
				// Like Telegram, nothing from a form cut short (no closing
				// boundary): it is not a smaller file.
				return nil, nil, err
			}
			if p.FileName() != "" {
				h := sha256.New()
				n, err := io.Copy(h, p)
				if err != nil {
					return nil, nil, err
				}
				file = &upload{field: p.FormName(), name: p.FileName(), size: n, sha256: hex.EncodeToString(h.Sum(nil))}
				continue
			}
			v, _ := io.ReadAll(io.LimitReader(p, maxField))
			m[p.FormName()] = string(v)
		}
	default:
		_ = r.ParseForm()
		for k, v := range r.Form {
			if len(v) > 0 {
				m[k] = v[0]
			}
		}
	}
	return m, nil, nil
}

func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "bot") || parts[0] == "bot" {
		fail(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	p, file, err := params(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "Bad Request: incomplete request")
		return
	}
	switch parts[1] {
	case "getFile":
		id := str(p["file_id"])
		s.mu.Lock()
		data, ok := s.files[id]
		s.mu.Unlock()
		if !ok {
			fail(w, http.StatusBadRequest, "Bad Request: invalid file_id")
			return
		}
		reply(w, map[string]any{"file_id": id, "file_path": "files/" + id, "file_size": len(data)})
	case "getMe":
		reply(w, tg.User{ID: BotID, IsBot: true, FirstName: "Fake", Username: "fake_bot"})
	case "deleteWebhook":
		s.mu.Lock()
		s.webhook = ""
		s.mu.Unlock()
		reply(w, true)
	case "setWebhook":
		s.mu.Lock()
		s.webhook = str(p["url"])
		s.mu.Unlock()
		reply(w, true)
	case "getUpdates":
		reply(w, s.pending(r, num(p["offset"]), num(p["timeout"])))
	case "sendMessage", "sendPhoto", "sendDocument", "editMessageText", "editMessageReplyMarkup":
		if f := s.failure(num(p["chat_id"])); f != nil {
			failWith(w, *f)
			return
		}
		text := str(p["text"])
		if text == "" {
			text = str(p["caption"])
		}
		var markup json.RawMessage
		switch m := p["reply_markup"].(type) {
		case string:
			markup = json.RawMessage(m)
		case map[string]any:
			markup, _ = json.Marshal(m)
		}
		s.mu.Lock()
		id := int(num(p["message_id"]))
		if strings.HasPrefix(parts[1], "send") || id == 0 {
			id = s.nextMsg
			s.nextMsg++
		}
		sent := Sent{Method: parts[1], ChatID: num(p["chat_id"]), MessageID: id, Text: text, Markup: markup}
		if file != nil {
			sent.FileName, sent.FileSize, sent.FileSHA256 = file.name, file.size, file.sha256
		}
		s.sent = append(s.sent, sent)
		s.mu.Unlock()
		m := tg.Message{MessageID: id, Chat: tg.Chat{ID: num(p["chat_id"]), Type: "private"}, Text: text}
		if parts[1] == "sendPhoto" { // Telegram answers with the photo's file id, for re-sending it
			photo := str(p["photo"])
			if photo == "" {
				photo = "fake-photo-" + strconv.Itoa(id)
			}
			m.Photo = []tg.PhotoSize{{FileID: photo, Width: 320, Height: 320}}
		}
		if parts[1] == "sendDocument" && file != nil {
			m.Document = &tg.Document{FileID: "fake-document-" + strconv.Itoa(id), FileName: file.name}
		}
		reply(w, m)
	case "sendInvoice":
		var stars int64
		if prices, ok := p["prices"].([]any); ok && len(prices) == 1 {
			if lp, ok := prices[0].(map[string]any); ok {
				stars = num(lp["amount"])
			}
		}
		s.mu.Lock()
		id := s.nextMsg
		s.nextMsg++
		s.sent = append(s.sent, Sent{Method: parts[1], ChatID: num(p["chat_id"]), MessageID: id,
			Text: "[invoice] " + str(p["title"]), Payload: str(p["payload"]), Stars: stars})
		s.mu.Unlock()
		reply(w, tg.Message{MessageID: id, Chat: tg.Chat{ID: num(p["chat_id"]), Type: "private"}})
	case "answerPreCheckoutQuery":
		ok, _ := p["ok"].(bool)
		s.mu.Lock()
		s.sent = append(s.sent, Sent{Method: parts[1], QueryID: str(p["pre_checkout_query_id"]), OK: &ok, Text: str(p["error_message"])})
		s.mu.Unlock()
		reply(w, true)
	case "answerCallbackQuery":
		s.mu.Lock()
		s.sent = append(s.sent, Sent{Method: parts[1], Text: str(p["text"])})
		s.mu.Unlock()
		reply(w, true)
	case "setMyCommands", "deleteMyCommands", "setMyDescription", "setMyShortDescription", "setChatMenuButton":
		s.recordProfile(ProfileCall{Method: parts[1], Params: p}, nil)
		reply(w, true)
	case "removeMyProfilePhoto": // the current photo, whoever set it
		s.recordProfile(ProfileCall{Method: parts[1], Params: p}, func() { s.photo = nil })
		reply(w, true)
	case "setMyProfilePhoto":
		// Like Telegram: the photo field is an InputProfilePhotoStatic whose
		// file came in the part it names.
		var photo struct{ Type, Photo string }
		if json.Unmarshal([]byte(str(p["photo"])), &photo) != nil || photo.Type != "static" {
			fail(w, http.StatusBadRequest, "Bad Request: can't parse photo JSON object")
			return
		}
		if file == nil || photo.Photo != "attach://"+file.field {
			fail(w, http.StatusBadRequest, "Bad Request: photo must be uploaded as a file")
			return
		}
		s.recordProfile(ProfileCall{Method: parts[1], Params: p, FileName: file.name, FileSize: file.size, FileSHA256: file.sha256},
			func() { s.newPhoto() })
		reply(w, true)
	case "getUserProfilePhotos":
		// The bot's own photo, the one it set last (or SetProfilePhoto); no
		// other user has one here.
		s.mu.Lock()
		out := tg.UserProfilePhotos{Photos: [][]tg.PhotoSize{}}
		if num(p["user_id"]) == BotID && len(s.photo) > 0 {
			out = tg.UserProfilePhotos{TotalCount: 1, Photos: [][]tg.PhotoSize{slices.Clone(s.photo)}}
		}
		s.mu.Unlock()
		reply(w, out)
	default:
		fail(w, http.StatusNotFound, "Not Found: method "+parts[1])
	}
}

// recordProfile records a profile call and applies what it changes (then,
// may be nil) under the same lock.
func (s *Server) recordProfile(c ProfileCall, then func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profile = append(s.profile, c)
	if then != nil {
		then()
	}
}

// pending returns updates with id >= offset, waiting up to the requested
// long-poll timeout (capped at 2 s) for one to arrive.
func (s *Server) pending(r *http.Request, offset, timeoutSec int64) []tg.Update {
	deadline := time.After(time.Duration(min(timeoutSec, 2)) * time.Second)
	for {
		s.mu.Lock()
		var out []tg.Update
		for _, u := range s.updates {
			if u.UpdateID >= offset {
				out = append(out, u)
			}
		}
		s.mu.Unlock()
		if len(out) > 0 {
			return out
		}
		select {
		case <-r.Context().Done():
			return []tg.Update{}
		case <-deadline:
			return []tg.Update{}
		case <-s.notified:
		}
	}
}
