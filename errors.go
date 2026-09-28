package messenger

import (
	"errors"
	"fmt"
	"time"
)

// Kind classifies a failure the same way whatever the backend.
type Kind int

const (
	Failed       Kind = iota // anything not covered below
	Unreachable              // no network, DNS, TLS or timeout
	Unauthorized             // the token was rejected
	ChatGone                 // the chat blocked the bot, left, or no longer exists
	Conflict                 // another program is polling this bot account
	RateLimited              // slow down; see RetryAfter
	Rejected                 // the service refused this particular message
)

// Error is what backends return for a failed call. Its text is fixed per
// Kind and never includes the token, the URL or the service's own
// description, so it is safe to show in a page or a log.
type Error struct {
	Kind       Kind
	Service    string
	Code       int           // the service's own status code, if any
	RetryAfter time.Duration // set for RateLimited when the service says
	// Detail is the service's description. It is token-free but may echo
	// message text back, so it is for matching and logs, not for pages.
	Detail string
}

func (e *Error) Error() string {
	s := e.Service
	if s == "" {
		s = "the chat service"
	}
	switch e.Kind {
	case Unreachable:
		return fmt.Sprintf("cannot reach %s; check the network", s)
	case Unauthorized:
		return s + " rejected the bot token"
	case ChatGone:
		return s + " cannot message the chat; unblock the bot and press Start"
	case Conflict:
		return "another program is polling this bot; give this app its own bot"
	case RateLimited:
		return s + " rate limit; retrying"
	case Rejected:
		return fmt.Sprintf("%s refused the message (code %d)", s, e.Code)
	}
	return fmt.Sprintf("%s request failed (code %d)", s, e.Code)
}

// KindOf returns the Kind of err, or Failed when err is not an *Error.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return Failed
}

// Backoff is how long to wait before retrying after err: a minute for
// problems a retry will not fix soon (a bad token, a second poller), the
// service's RetryAfter when rate limited, and five seconds otherwise.
func Backoff(err error) time.Duration {
	d := 5 * time.Second
	var e *Error
	if errors.As(err, &e) {
		if e.Kind == Unauthorized || e.Kind == Conflict {
			d = time.Minute
		}
		d = max(d, e.RetryAfter)
	}
	return d
}
