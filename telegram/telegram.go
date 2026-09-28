// Package telegram is the Telegram Bot API backend for messenger: plain
// net/http, an outgoing long poll (getUpdates), no webhook, so an app
// that uses it still listens on nothing but its own loopback port.
//
// The token is part of every request URL, so transport errors (which
// quote the URL) are never passed on: every failure is a
// *messenger.Error with fixed text.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/steve-gm-walsh/messenger"
)

// DefaultBaseURL is the public Bot API.
const DefaultBaseURL = "https://api.telegram.org"

// Service is the name used in error text.
const Service = "Telegram"

// Bot is one bot token. The zero BaseURL and HTTP use the public API and
// http.DefaultClient; tests point BaseURL at a fake (package
// telegramtest).
type Bot struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
}

var _ messenger.Backend = (*Bot)(nil)

// New returns a Bot for token on the public API.
func New(token string) *Bot { return &Bot{Token: token} }

var tokenRe = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

// ValidToken reports whether s looks like a @BotFather token (digits, a
// colon, then letters). It does not ask Telegram; Me does that.
func ValidToken(s string) bool { return tokenRe.MatchString(s) }

// BotID is the numeric bot id at the front of a token.
func BotID(token string) string {
	id, _, _ := strings.Cut(token, ":")
	return id
}

// Service implements messenger.Backend.
func (b *Bot) Service() string { return Service }

// Account implements messenger.Backend: the bot id from the token.
func (b *Bot) Account() string { return BotID(b.Token) }

func (b *Bot) base() string {
	if b.BaseURL != "" {
		return strings.TrimRight(b.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (b *Bot) client() *http.Client {
	if b.HTTP != nil {
		return b.HTTP
	}
	return http.DefaultClient
}

// Call invokes any Bot API method with JSON params, decoding the result
// into out (which may be nil). It is the escape hatch for methods this
// package does not wrap.
func (b *Bot) Call(ctx context.Context, method string, params, out any) error {
	if params == nil {
		params = map[string]any{}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base()+"/bot"+b.Token+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return &messenger.Error{Kind: messenger.Failed, Service: Service}
	}
	req.Header.Set("Content-Type", "application/json")
	return b.do(req, out)
}

// do sends one request and decodes the Bot API envelope.
func (b *Bot) do(req *http.Request, out any) error {
	resp, err := b.client().Do(req)
	if err != nil {
		if cerr := req.Context().Err(); cerr != nil {
			return cerr
		}
		return &messenger.Error{Kind: messenger.Unreachable, Service: Service}
	}
	defer resp.Body.Close()
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Code        int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&env); err != nil {
		return &messenger.Error{Kind: messenger.Failed, Service: Service, Code: resp.StatusCode}
	}
	if !env.OK {
		code := env.Code
		if code == 0 {
			code = resp.StatusCode
		}
		return classify(code, time.Duration(env.Parameters.RetryAfter)*time.Second, b.scrub(env.Description))
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return &messenger.Error{Kind: messenger.Failed, Service: Service, Code: resp.StatusCode}
		}
	}
	return nil
}

// classify maps a Bot API error code onto a messenger.Kind.
func classify(code int, retry time.Duration, desc string) *messenger.Error {
	e := &messenger.Error{Service: Service, Code: code, RetryAfter: retry, Detail: desc}
	lower := strings.ToLower(desc)
	switch {
	case code == 401 || code == 404:
		e.Kind = messenger.Unauthorized
	case code == 403, code == 400 && strings.Contains(lower, "chat not found"):
		e.Kind = messenger.ChatGone
	case code == 409:
		e.Kind = messenger.Conflict
	case code == 429:
		e.Kind = messenger.RateLimited
	case code == 400:
		e.Kind = messenger.Rejected
	default:
		e.Kind = messenger.Failed
	}
	return e
}

// scrub removes the token from s, raw and URL-escaped.
func (b *Bot) scrub(s string) string {
	if b.Token == "" {
		return s
	}
	s = strings.ReplaceAll(s, b.Token, "<token>")
	return strings.ReplaceAll(s, strings.ReplaceAll(b.Token, ":", "%3A"), "<token>")
}

// Me implements messenger.Backend with getMe.
func (b *Bot) Me(ctx context.Context) (messenger.Identity, error) {
	var u struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	}
	if err := b.Call(ctx, "getMe", nil, &u); err != nil {
		return messenger.Identity{}, err
	}
	return messenger.Identity{ID: strconv.FormatInt(u.ID, 10), Name: u.FirstName, Handle: u.Username}, nil
}

const captionLimit = 1024 // runes, Telegram's photo caption limit

// Send implements messenger.Backend with sendMessage, or sendPhoto when
// m.Photo is set. A caption over Telegram's limit is cut with "...".
func (b *Bot) Send(ctx context.Context, chat string, m messenger.Message) (string, error) {
	params := map[string]any{"chat_id": chatID(chat)}
	if m.Format == messenger.HTML {
		params["parse_mode"] = "HTML"
	}
	if mk := markup(m); mk != nil {
		params["reply_markup"] = mk
	}
	var sent json.RawMessage
	if m.Photo == nil {
		params["text"] = m.Text
		if m.NoPreview {
			params["link_preview_options"] = map[string]any{"is_disabled": true}
		}
		if err := b.Call(ctx, "sendMessage", params, &sent); err != nil {
			return "", err
		}
		return messageID(sent), nil
	}
	params["caption"] = truncate(m.Text, captionLimit)
	if err := b.upload(ctx, "sendPhoto", params, "photo", m.PhotoName, m.Photo, &sent); err != nil {
		return "", err
	}
	return messageID(sent), nil
}

// messageID reads the id of a sent message. The send has succeeded by
// the time this runs, so a result without one (a proxy or fake answering
// "true") gives "" rather than an error.
func messageID(raw json.RawMessage) string {
	var m struct {
		ID int64 `json:"message_id"`
	}
	if json.Unmarshal(raw, &m) != nil || m.ID == 0 {
		return ""
	}
	return strconv.FormatInt(m.ID, 10)
}

// chatID sends a numeric chat id as a JSON number, as Telegram's own
// examples do; "@channel" names stay strings.
func chatID(chat string) any {
	if n, err := strconv.ParseInt(chat, 10, 64); err == nil {
		return n
	}
	return chat
}

func markup(m messenger.Message) any {
	if len(m.Buttons) > 0 {
		rows := make([][]map[string]string, 0, len(m.Buttons))
		for _, row := range m.Buttons {
			r := make([]map[string]string, 0, len(row))
			for _, btn := range row {
				r = append(r, map[string]string{"text": btn.Label, "callback_data": btn.Data})
			}
			rows = append(rows, r)
		}
		return map[string]any{"inline_keyboard": rows}
	}
	if m.Prompt != nil {
		mk := map[string]any{"force_reply": true, "selective": true}
		if m.Prompt.Placeholder != "" {
			mk["input_field_placeholder"] = truncate(m.Prompt.Placeholder, 64)
		}
		return mk
	}
	return nil
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-3]) + "..."
}

// upload posts a multipart request with one file field; params values
// that are not strings are sent as JSON.
func (b *Bot) upload(ctx context.Context, method string, params map[string]any, field, name string, data []byte, out any) error {
	if name == "" {
		name = "image.png"
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range params {
		s, ok := v.(string)
		if !ok {
			raw, err := json.Marshal(v)
			if err != nil {
				return err
			}
			s = string(raw)
		}
		if err := w.WriteField(k, s); err != nil {
			return err
		}
	}
	part, err := w.CreateFormFile(field, name)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base()+"/bot"+b.Token+"/"+method, &body)
	if err != nil {
		return &messenger.Error{Kind: messenger.Failed, Service: Service}
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return b.do(req, out)
}

// wire types for getUpdates
type (
	chat struct {
		ID        int64  `json:"id"`
		Type      string `json:"type"`
		Title     string `json:"title"`
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	}
	user struct {
		ID int64 `json:"id"`
	}
	message struct {
		ID          int64    `json:"message_id"`
		Date        int64    `json:"date"`
		Text        string   `json:"text"`
		Caption     string   `json:"caption"`
		Chat        chat     `json:"chat"`
		From        *user    `json:"from"`
		ReplyTo     *message `json:"reply_to_message"`
		ReplyMarkup *struct {
			Keyboard [][]struct {
				Data string `json:"callback_data"`
			} `json:"inline_keyboard"`
		} `json:"reply_markup"`
	}
	callback struct {
		ID      string   `json:"id"`
		From    user     `json:"from"`
		Message *message `json:"message"`
		Data    string   `json:"data"`
	}
	update struct {
		ID       int64     `json:"update_id"`
		Message  *message  `json:"message"`
		Callback *callback `json:"callback_query"`
	}
)

// Receive implements messenger.Backend with getUpdates, asking for
// messages and button presses only.
func (b *Bot) Receive(ctx context.Context, cursor int64, wait time.Duration) ([]messenger.Incoming, error) {
	secs := int(wait / time.Second)
	// The request must outlive Telegram's own wait; the default client
	// has no timeout, so bound it here.
	ctx, cancel := context.WithTimeout(ctx, wait+30*time.Second)
	defer cancel()
	var ups []update
	if err := b.Call(ctx, "getUpdates", map[string]any{
		"offset": cursor, "timeout": secs, "allowed_updates": []string{"message", "callback_query"},
	}, &ups); err != nil {
		return nil, err
	}
	out := make([]messenger.Incoming, 0, len(ups))
	for _, u := range ups {
		in := messenger.Incoming{Cursor: u.ID + 1}
		switch {
		case u.Message != nil:
			fill(&in, u.Message)
			if u.Message.From != nil {
				in.From = strconv.FormatInt(u.Message.From.ID, 10)
			}
			in.Text = u.Message.Text
			if in.Text == "" {
				in.Text = u.Message.Caption
			}
			if r := u.Message.ReplyTo; r != nil {
				in.ReplyTo = strconv.FormatInt(r.ID, 10)
				if r.ReplyMarkup != nil {
					for _, row := range r.ReplyMarkup.Keyboard {
						for _, btn := range row {
							in.ReplyData = append(in.ReplyData, btn.Data)
						}
					}
				}
			}
		case u.Callback != nil && u.Callback.Message != nil:
			fill(&in, u.Callback.Message)
			in.From = strconv.FormatInt(u.Callback.From.ID, 10)
			in.At = time.Time{} // the press has no timestamp of its own
			in.Button, in.AnswerID = u.Callback.Data, u.Callback.ID
		}
		out = append(out, in)
	}
	return out, nil
}

func fill(in *messenger.Incoming, m *message) {
	in.Chat = strconv.FormatInt(m.Chat.ID, 10)
	in.Private = m.Chat.Type == "private"
	in.ChatName = m.Chat.Title
	if in.ChatName == "" {
		in.ChatName = m.Chat.FirstName
	}
	if in.ChatName == "" {
		in.ChatName = m.Chat.Username
	}
	in.ID = strconv.FormatInt(m.ID, 10)
	if m.Date != 0 {
		in.At = time.Unix(m.Date, 0)
	}
}

// Answer implements messenger.Backend with answerCallbackQuery.
func (b *Bot) Answer(ctx context.Context, in messenger.Incoming, text string) error {
	if in.AnswerID == "" {
		return nil
	}
	params := map[string]any{"callback_query_id": in.AnswerID}
	if text != "" {
		params["text"] = truncate(text, 200)
	}
	return b.Call(ctx, "answerCallbackQuery", params, nil)
}
