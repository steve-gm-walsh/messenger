// Package messenger is the chat-bot plumbing the house apps share: a
// Backend interface that says what an app needs from a chat service
// (send, receive, acknowledge a button), the typed errors every backend
// maps onto, a long-poll Listener with backoff, and small helpers for
// pairing codes, commands and the secrets file.
//
// Telegram is the one backend today (package telegram). Apps talk to
// Backend and Kind, not to Telegram's error codes or JSON, so a second
// backend is a new package rather than a rewrite of every app.
//
// What stays in each app: its secrets.json layout, how a chat is paired,
// its commands, and any outbox. Those differ app to app on purpose.
package messenger

import (
	"context"
	"time"
)

// Backend is one bot account on one chat service.
type Backend interface {
	// Service names the chat service for messages shown to the user,
	// e.g. "Telegram".
	Service() string
	// Account identifies the bot account without a network call, so a
	// changed token can be told apart from the old one.
	Account() string
	// Me checks the credentials and returns the bot's own identity.
	Me(ctx context.Context) (Identity, error)
	// Send delivers one message and returns its id in that chat.
	Send(ctx context.Context, chat string, m Message) (string, error)
	// Receive long-polls for updates after cursor, waiting up to wait.
	// Every update the service returns comes back as one Incoming, even
	// ones the backend does not understand (they carry only Cursor), so
	// the caller can advance past them.
	Receive(ctx context.Context, cursor int64, wait time.Duration) ([]Incoming, error)
	// Answer acknowledges a button press, optionally with a short toast.
	// It does nothing for an Incoming that is not a button press.
	Answer(ctx context.Context, in Incoming, text string) error
}

// Identity is the bot's account as the service reports it.
type Identity struct {
	ID     string
	Name   string // display name
	Handle string // @-less username, when the service has one
}

// Format is how Message.Text is interpreted.
type Format int

const (
	Plain Format = iota
	HTML         // the service's HTML subset (<b>, <i>, <a>, <pre>, <code>)
)

// Message is one outgoing message. Fields a backend cannot honour are
// dropped rather than failing the send.
type Message struct {
	Text   string
	Format Format
	// Photo, when set, is sent as an image with Text as its caption.
	Photo     []byte
	PhotoName string // file name for the upload; "image.png" if empty
	// Buttons are rows of inline buttons under the message.
	Buttons [][]Button
	// Prompt asks the client to open a reply to this message, so a bare
	// answer can be tied back to it (Incoming.ReplyTo). Ignored when
	// Buttons are set, since a message carries one kind of markup.
	Prompt *Prompt
	// NoPreview turns off link previews.
	NoPreview bool
}

// Button is one inline button; pressing it arrives as an Incoming with
// Button set to Data.
type Button struct {
	Label string
	Data  string
}

// Prompt is a request for a typed reply.
type Prompt struct {
	Placeholder string // hint shown in the input box
}

// Incoming is one update from the service: a message, a button press,
// or (with only Cursor set) something the backend skipped.
type Incoming struct {
	// Cursor is where to resume after this update; persisting it is how
	// an app avoids handling the same update twice.
	Cursor int64

	Chat     string // chat the update belongs to
	ChatName string // title, or the person's name in a private chat
	Private  bool   // a one-to-one chat with the bot
	From     string // the sender
	ID       string // message id; for a button press, the message it sits under
	At       time.Time

	Text string // message text or photo caption

	// ReplyTo is the id of the message this one answers, if any, and
	// ReplyData the button data that message carried.
	ReplyTo   string
	ReplyData []string

	// Button is the data of a pressed button; AnswerID is what Answer
	// needs to acknowledge it.
	Button   string
	AnswerID string
}

// Skipped reports an update the backend did not understand.
func (in Incoming) Skipped() bool { return in.Chat == "" }
