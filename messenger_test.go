package messenger_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/steve-gm-walsh/messenger"
	"github.com/steve-gm-walsh/messenger/telegram"
	"github.com/steve-gm-walsh/messenger/telegram/telegramtest"
)

func TestCommand(t *testing.T) {
	cases := []struct{ in, name, args string }{
		{"/start abc123", "start", "abc123"},
		{" /Status@cat_bot ", "status", ""},
		{"/pickup  ibuprofen 3 ", "pickup", "ibuprofen 3"},
		{"10.8 lb", "", "10.8 lb"},
	}
	for _, c := range cases {
		if n, a := messenger.Command(c.in); n != c.name || a != c.args {
			t.Errorf("Command(%q) = %q, %q", c.in, n, a)
		}
	}
}

func TestNewCode(t *testing.T) {
	a, b := messenger.NewCode(4), messenger.NewCode(4)
	if len(a) != 8 || a == b {
		t.Fatalf("codes %q %q", a, b)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	for _, body := range []string{`{"a":1}`, `{"a":2}`} {
		if err := messenger.WriteFileAtomic(path, []byte(body)); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); string(got) != body {
			t.Fatalf("got %s", got)
		}
	}
	if left, _ := filepath.Glob(path + ".*"); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestBackoffAndKind(t *testing.T) {
	plain := errors.New("x")
	if messenger.KindOf(plain) != messenger.Failed || messenger.Backoff(plain) != 5*time.Second {
		t.Error("plain errors are Failed with the short backoff")
	}
	bad := &messenger.Error{Kind: messenger.Unauthorized, Service: "Telegram"}
	if messenger.Backoff(bad) != time.Minute || bad.Error() != "Telegram rejected the bot token" {
		t.Errorf("unauthorized: %v %q", messenger.Backoff(bad), bad.Error())
	}
	wrapped := errors.Join(errors.New("ctx"), &messenger.Error{Kind: messenger.Conflict})
	if messenger.KindOf(wrapped) != messenger.Conflict {
		t.Error("KindOf should see through wrapping")
	}
}

// The Listener hands every update to Handle once, resumes from the
// app's cursor, and reports poll errors.
func TestListener(t *testing.T) {
	fake := telegramtest.New(t, "7:tok")
	bot := &telegram.Bot{Token: "7:tok", BaseURL: fake.URL}
	now := time.Now()
	fake.Message(1, "one", now)
	fake.Message(1, "two", now)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var got []string
	var reports []error
	configured := false
	l := messenger.Listener{
		Backend: func() (messenger.Backend, int64) {
			mu.Lock()
			defer mu.Unlock()
			if !configured { // first round: not set up yet
				configured = true
				return nil, 0
			}
			return bot, 0 // an app that keeps no cursor of its own
		},
		Handle: func(ctx context.Context, b messenger.Backend, in messenger.Incoming) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, in.Text)
			if len(got) == 3 {
				cancel()
			}
			return nil
		},
		Report: func(err error) {
			mu.Lock()
			defer mu.Unlock()
			reports = append(reports, err)
		},
		Wait: time.Second, Idle: 10 * time.Millisecond,
	}
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("handled %d updates", n)
		case <-time.After(10 * time.Millisecond):
		}
	}
	fake.Message(1, "three", now)
	select {
	case <-done:
	case <-deadline:
		t.Fatal("listener did not stop")
	}
	if len(got) != 3 || got[0] != "one" || got[2] != "three" {
		t.Fatalf("handled %v; an update was repeated or lost", got)
	}
	offs := fake.Offsets()
	if offs[len(offs)-1] != 3 {
		t.Fatalf("last poll resumed from %d, want 3 (in-memory cursor)", offs[len(offs)-1])
	}

	// A failing poll is reported, then Run backs off until cancelled.
	fake.Fail("getUpdates", 502, 0, "Bad Gateway")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	reports = nil
	l.Run(ctx2)
	if len(reports) != 1 || messenger.KindOf(reports[0]) != messenger.Failed {
		t.Fatalf("reports %v", reports)
	}
}
