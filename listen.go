package messenger

import (
	"context"
	"time"
)

// Listener long-polls a Backend and hands each update to Handle, backing
// off on errors, until its context ends.
type Listener struct {
	// Backend returns the backend to poll and the cursor to resume from.
	// A nil Backend means "not configured yet"; Run checks again after
	// Idle. It is asked every round, so a newly saved token takes effect
	// without a restart.
	Backend func() (Backend, int64)
	// Handle processes one update, including skipped ones (Incoming.Skipped)
	// so their cursors can be saved. Persisting in.Cursor is Handle's job;
	// Run also remembers it in memory, per Account, so an app that keeps
	// no cursor still never sees an update twice while it runs.
	Handle func(ctx context.Context, b Backend, in Incoming) error
	// Report, if set, is called after every round with the first error of
	// that round, or nil when the round went cleanly.
	Report func(error)
	Wait   time.Duration // long-poll wait; default 50s
	Idle   time.Duration // re-check interval while unconfigured; default 5s
}

// Run polls until ctx ends.
func (l Listener) Run(ctx context.Context) {
	wait, idle := l.Wait, l.Idle
	if wait <= 0 {
		wait = 50 * time.Second
	}
	if idle <= 0 {
		idle = 5 * time.Second
	}
	seen := map[string]int64{}
	for ctx.Err() == nil {
		b, cursor := l.Backend()
		if b == nil {
			if !Sleep(ctx, idle) {
				return
			}
			continue
		}
		cursor = max(cursor, seen[b.Account()])
		ins, err := b.Receive(ctx, cursor, wait)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			l.report(err)
			if !Sleep(ctx, Backoff(err)) {
				return
			}
			continue
		}
		var first error
		for _, in := range ins {
			if err := l.Handle(ctx, b, in); err != nil && first == nil {
				first = err
			}
			seen[b.Account()] = max(seen[b.Account()], in.Cursor)
		}
		l.report(first)
	}
}

func (l Listener) report(err error) {
	if l.Report != nil {
		l.Report(err)
	}
}

// Sleep waits d, returning false if ctx ended first.
func Sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
