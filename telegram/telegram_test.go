package telegram_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/steve-gm-walsh/messenger"
	"github.com/steve-gm-walsh/messenger/telegram"
	"github.com/steve-gm-walsh/messenger/telegram/telegramtest"
)

const token = "123:secret-token"

func setup(t *testing.T) (*telegramtest.Server, *telegram.Bot) {
	t.Helper()
	fake := telegramtest.New(t, token)
	return fake, &telegram.Bot{Token: token, BaseURL: fake.URL}
}

func TestTokenHelpers(t *testing.T) {
	for s, want := range map[string]bool{"123:abc_DEF-9": true, "123": false, "abc:def": false, " 1:a": false, "": false} {
		if telegram.ValidToken(s) != want {
			t.Errorf("ValidToken(%q) = %v", s, !want)
		}
	}
	if telegram.BotID("8850:xyz") != "8850" || telegram.New("8850:xyz").Account() != "8850" {
		t.Error("BotID/Account should be the digits before the colon")
	}
}

func TestMe(t *testing.T) {
	_, bot := setup(t)
	me, err := bot.Me(context.Background())
	if err != nil || me.ID != "123" || me.Handle != "test_bot" {
		t.Fatalf("Me = %+v, %v", me, err)
	}
	bad := &telegram.Bot{Token: "999:wrong", BaseURL: bot.BaseURL}
	_, err = bad.Me(context.Background())
	if messenger.KindOf(err) != messenger.Unauthorized {
		t.Fatalf("wrong token: %v", err)
	}
}

func TestSendShapes(t *testing.T) {
	fake, bot := setup(t)
	ctx := context.Background()
	id, err := bot.Send(ctx, "42", messenger.Message{Text: "<b>hi</b>", Format: messenger.HTML, NoPreview: true,
		Buttons: [][]messenger.Button{{{Label: "Done", Data: "done:1"}}}})
	if err != nil || id == "" {
		t.Fatalf("send: %q %v", id, err)
	}
	if _, err := bot.Send(ctx, "42", messenger.Message{Text: "how many?", Prompt: &messenger.Prompt{Placeholder: "e.g. 3"}}); err != nil {
		t.Fatal(err)
	}
	var img bytes.Buffer
	_ = png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	if _, err := bot.Send(ctx, "42", messenger.Message{Text: strings.Repeat("é", 1100), Photo: img.Bytes()}); err != nil {
		t.Fatal(err)
	}

	sent := fake.Sent()
	if len(sent) != 3 {
		t.Fatalf("sent %d", len(sent))
	}
	first := sent[0]
	if first.Chat != "42" || first.Params["parse_mode"] != "HTML" || first.Params["link_preview_options"] == nil {
		t.Errorf("text send: %+v", first)
	}
	if kb, _ := first.Markup["inline_keyboard"].([]any); len(kb) != 1 {
		t.Errorf("buttons: %v", first.Markup)
	}
	if sent[1].Markup["force_reply"] != true || sent[1].Markup["input_field_placeholder"] != "e.g. 3" {
		t.Errorf("prompt markup: %v", sent[1].Markup)
	}
	if n := len([]rune(sent[2].Text)); sent[2].Method != "sendPhoto" || n != 1024 || !strings.HasSuffix(sent[2].Text, "...") {
		t.Errorf("photo caption %d runes via %s", n, sent[2].Method)
	}
	if _, ok := first.Params["chat_id"].(float64); !ok {
		t.Errorf("numeric chat ids go out as JSON numbers, got %T", first.Params["chat_id"])
	}
}

func TestReceive(t *testing.T) {
	fake, bot := setup(t)
	at := time.Date(2026, 9, 21, 9, 30, 0, 0, time.UTC)
	fake.Message(42, "hello", at)
	fake.Press(42, 7, "done:legs")
	fake.Push(map[string]any{"edited_message": map[string]any{"message_id": 1}})
	fake.Push(map[string]any{"message": map[string]any{
		"message_id": 9, "date": at.Unix(), "text": "3",
		"chat": map[string]any{"id": -5, "type": "group", "title": "Family"},
		"from": map[string]any{"id": 42},
		"reply_to_message": map[string]any{"message_id": 8, "chat": map[string]any{"id": -5},
			"reply_markup": map[string]any{"inline_keyboard": []any{[]any{map[string]any{"text": "x", "callback_data": "plan:a"}}}}},
	}})

	ins, err := bot.Receive(context.Background(), 0, time.Second)
	if err != nil || len(ins) != 4 {
		t.Fatalf("receive: %d %v", len(ins), err)
	}
	msg, press, skip, grp := ins[0], ins[1], ins[2], ins[3]
	if msg.Chat != "42" || msg.From != "42" || !msg.Private || msg.Text != "hello" || !msg.At.Equal(at) || msg.ChatName != "Tester" {
		t.Errorf("message: %+v", msg)
	}
	if press.Button != "done:legs" || press.AnswerID == "" || press.ID != "7" || !press.At.IsZero() {
		t.Errorf("press: %+v", press)
	}
	if !skip.Skipped() || skip.Cursor != 4 {
		t.Errorf("skipped update: %+v", skip)
	}
	if grp.Private || grp.ChatName != "Family" || grp.ReplyTo != "8" || len(grp.ReplyData) != 1 || grp.ReplyData[0] != "plan:a" {
		t.Errorf("group reply: %+v", grp)
	}
	if err := bot.Answer(context.Background(), press, "Logged"); err != nil {
		t.Fatal(err)
	}
	if err := bot.Answer(context.Background(), msg, "ignored"); err != nil {
		t.Fatal(err)
	}
	if s := fake.Sent(); len(s) != 1 || s[0].AnswerID != press.AnswerID {
		t.Errorf("answers: %+v", s)
	}
	if ins, _ := bot.Receive(context.Background(), grp.Cursor, time.Second); len(ins) != 0 {
		t.Errorf("cursor past the last update should return nothing, got %d", len(ins))
	}
}

func TestErrorKinds(t *testing.T) {
	fake, bot := setup(t)
	ctx := context.Background()
	cases := []struct {
		code  int
		retry int
		desc  string
		want  messenger.Kind
	}{
		{403, 0, "Forbidden: bot was blocked by the user", messenger.ChatGone},
		{400, 0, "Bad Request: chat not found", messenger.ChatGone},
		{400, 0, "Bad Request: message is too long", messenger.Rejected},
		{409, 0, "Conflict: terminated by other getUpdates request", messenger.Conflict},
		{429, 30, "Too Many Requests: retry after 30", messenger.RateLimited},
		{502, 0, "Bad Gateway", messenger.Failed},
	}
	for _, c := range cases {
		fake.Fail("sendMessage", c.code, c.retry, c.desc+" "+token)
		_, err := bot.Send(ctx, "42", messenger.Message{Text: "x"})
		var e *messenger.Error
		if !errors.As(err, &e) || e.Kind != c.want {
			t.Errorf("%d %q: got %v", c.code, c.desc, err)
			continue
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(e.Detail, "secret") {
			t.Errorf("token leaked: %q / %q", err.Error(), e.Detail)
		}
		if c.retry > 0 && (e.RetryAfter != 30*time.Second || messenger.Backoff(err) != 30*time.Second) {
			t.Errorf("retry after: %v", e.RetryAfter)
		}
	}
}

func TestUnreachableHidesURL(t *testing.T) {
	fake, bot := setup(t)
	fake.Close()
	_, err := bot.Send(context.Background(), "42", messenger.Message{Text: "x"})
	if messenger.KindOf(err) != messenger.Unreachable || strings.Contains(err.Error(), "secret") {
		t.Fatalf("got %v", err)
	}
}
