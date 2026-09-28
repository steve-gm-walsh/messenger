// Package telegramtest is a fake Telegram Bot API for tests: an
// httptest server that answers getMe, getUpdates, sendMessage,
// sendPhoto and answerCallbackQuery, records what was sent, and can be
// told to fail. Point telegram.Bot.BaseURL at Server.URL.
package telegramtest

import (
	"encoding/json"
	"image"
	_ "image/jpeg" // photo uploads may be JPEG
	_ "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Sent is one call the fake received.
type Sent struct {
	Method   string
	Chat     string
	Text     string // text, or a photo's caption
	Photo    []byte
	Markup   map[string]any // reply_markup, decoded
	Params   map[string]any // everything else, decoded
	AnswerID string         // answerCallbackQuery's callback_query_id
}

// Server is the fake. Its methods are safe from any goroutine.
type Server struct {
	*httptest.Server
	Token    string
	Username string

	mu      sync.Mutex
	nextID  int64
	msgID   int64
	queue   []map[string]any
	sent    []Sent
	offsets []int64
	fail    map[string]failure
}

type failure struct {
	code  int
	retry int
	desc  string
}

// New starts a fake for token, closed when t ends. Any other token gets
// a 401, as the real API does.
func New(t testing.TB, token string) *Server {
	s := &Server{Token: token, Username: "test_bot", nextID: 1, msgID: 1000, fail: map[string]failure{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Message queues a text message from a private chat whose id is also
// the sender's, dated at.
func (s *Server) Message(chat int64, text string, at time.Time) {
	s.Push(map[string]any{"message": map[string]any{
		"message_id": s.newMsgID(), "date": at.Unix(), "text": text,
		"chat": map[string]any{"id": chat, "type": "private", "first_name": "Tester"},
		"from": map[string]any{"id": chat},
	}})
}

// Press queues a button press on message msgID in a private chat.
func (s *Server) Press(chat, msgID int64, data string) {
	s.Push(map[string]any{"callback_query": map[string]any{
		"id": "cb" + strconv.FormatInt(s.newMsgID(), 10), "data": data,
		"from":    map[string]any{"id": chat},
		"message": map[string]any{"message_id": msgID, "chat": map[string]any{"id": chat, "type": "private"}},
	}})
}

// Push queues a raw update; update_id is filled in.
func (s *Server) Push(u map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u["update_id"] = s.nextID
	s.nextID++
	s.queue = append(s.queue, u)
}

func (s *Server) newMsgID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgID++
	return s.msgID
}

// Fail makes every later call to method fail with code (and retry_after
// seconds, for 429) until Fail(method, 0, 0, "") clears it.
func (s *Server) Fail(method string, code, retryAfter int, desc string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if code == 0 {
		delete(s.fail, method)
		return
	}
	s.fail[method] = failure{code, retryAfter, desc}
}

// Sent returns every send, photo and callback answer so far.
func (s *Server) Sent() []Sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Sent(nil), s.sent...)
}

// Offsets returns the offset of every getUpdates call so far.
func (s *Server) Offsets() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.offsets...)
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/bot")
	token, method, _ := strings.Cut(path, "/")
	if token != s.Token {
		reply(w, 401, map[string]any{"ok": false, "error_code": 401, "description": "Unauthorized"})
		return
	}
	params, photo := decode(r)
	s.mu.Lock()
	f, failing := s.fail[method]
	s.mu.Unlock()
	if failing {
		body := map[string]any{"ok": false, "error_code": f.code, "description": f.desc}
		if f.retry > 0 {
			body["parameters"] = map[string]any{"retry_after": f.retry}
		}
		reply(w, f.code, body)
		return
	}
	ok := func(result any) { reply(w, 200, map[string]any{"ok": true, "result": result}) }
	switch method {
	case "getMe":
		id, _ := strconv.ParseInt(strings.SplitN(s.Token, ":", 2)[0], 10, 64)
		ok(map[string]any{"id": id, "is_bot": true, "first_name": "Test", "username": s.Username})
	case "getUpdates":
		off := int64(num(params["offset"]))
		s.mu.Lock()
		s.offsets = append(s.offsets, off)
		var out []map[string]any
		for _, u := range s.queue {
			if u["update_id"].(int64) >= off {
				out = append(out, u)
			}
		}
		s.mu.Unlock()
		if out == nil {
			out = []map[string]any{}
			if num(params["timeout"]) > 0 {
				time.Sleep(20 * time.Millisecond) // a long poll, briefly: keeps pollers off a hot spin
			}
		}
		ok(out)
	case "sendMessage", "sendPhoto":
		sent := Sent{Method: method, Chat: str(params["chat_id"]), Photo: photo}
		sent.Text, _ = params["text"].(string)
		if method == "sendPhoto" {
			sent.Text, _ = params["caption"].(string)
			if _, _, err := image.Decode(strings.NewReader(string(photo))); err != nil {
				reply(w, 400, map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: IMAGE_PROCESS_FAILED"})
				return
			}
		}
		sent.Markup, _ = params["reply_markup"].(map[string]any)
		delete(params, "reply_markup")
		sent.Params = params
		id := s.newMsgID()
		s.mu.Lock()
		s.sent = append(s.sent, sent)
		s.mu.Unlock()
		ok(map[string]any{"message_id": id, "date": time.Now().Unix()})
	case "answerCallbackQuery":
		s.mu.Lock()
		s.sent = append(s.sent, Sent{Method: method, AnswerID: str(params["callback_query_id"]), Params: params})
		s.mu.Unlock()
		ok(true)
	default:
		reply(w, 404, map[string]any{"ok": false, "error_code": 404, "description": "Not Found"})
	}
}

// decode reads JSON or multipart params. Multipart fields holding JSON
// (reply_markup) are decoded too.
func decode(r *http.Request) (map[string]any, []byte) {
	params := map[string]any{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return params, nil
		}
		for k, vs := range r.MultipartForm.Value {
			var v any
			if json.Unmarshal([]byte(vs[0]), &v) == nil {
				if _, isMap := v.(map[string]any); isMap {
					params[k] = v
					continue
				}
			}
			params[k] = vs[0]
		}
		var photo []byte
		for _, fhs := range r.MultipartForm.File {
			if f, err := fhs[0].Open(); err == nil {
				photo, _ = io.ReadAll(f)
				defer f.Close()
			}
		}
		return params, photo
	}
	_ = json.NewDecoder(r.Body).Decode(&params)
	return params, nil
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	}
	return ""
}
