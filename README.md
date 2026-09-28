# messenger

Chat-bot plumbing shared by the house apps. It lets a small desktop app send
you a message on your phone and act on your reply. Examples: a Monday weigh-in
reminder you answer with "10.8 lb", or a prescription reminder with "Picked
up" buttons.

It is a Go library, not an app. It has no window and no port.

- **`messenger`** is the backend-neutral part:
  - the `Backend` interface (send, receive, acknowledge a button);
  - `Message` and `Incoming`;
  - typed errors (`Kind`: `Unauthorized`, `ChatGone`, `Conflict`, `RateLimited`, …) with `Backoff`;
  - a long-poll `Listener`;
  - pairing codes, `/command` parsing, and an atomic 0600 writer for `secrets.json`.
- **`messenger/telegram`** is the Telegram Bot API backend. It uses plain
  `net/http` and an outgoing long poll, with no webhook. The token is never
  in an error.
- **`messenger/telegram/telegramtest`** is a fake Bot API for tests.

Telegram is the only backend today. Apps depend on `Backend` and `Kind`, not on
Telegram's JSON or error codes, so a second service would be a new package
next to `telegram/`.

## Using it

```sh
go get github.com/steve-gm-walsh/messenger@latest
```

```go
bot := telegram.New(token)
if _, err := bot.Me(ctx); err != nil { ... }          // validate the token
bot.Send(ctx, chat, messenger.Message{Text: "Weigh-in day", Prompt: &messenger.Prompt{Placeholder: "10.8 lb"}})

messenger.Listener{
    Backend: func() (messenger.Backend, int64) { return bot, savedCursor },
    Handle: func(ctx context.Context, b messenger.Backend, in messenger.Incoming) error {
        // pair, run the command, then save in.Cursor
    },
    Report: setStatusError,
}.Run(ctx)
```

These stay in each app, because they differ from app to app:

- the layout of `secrets.json`;
- how a chat is paired;
- the commands;
- any outbox.

Give every app its own bot. Two programs long-polling one token get
`Conflict` (HTTP 409).

## Run

| | |
|---|---|
| `run.bat setup` | download modules, build |
| `run.bat test` | gofmt, vet, `go mod tidy -diff`, staticcheck, govulncheck, `go test -race` |
| `run.bat build` / `clean` | build every package / clear caches |
| `run.bat` | build and print how to consume it |

No port. There is nothing to launch, so there is no `app.json`.

## Releasing

This is a public module, fetched through proxy.golang.org. Consumers do not
need a `replace` directive. To release:

1. Tag with semver (`git tag v0.2.0 && git push --tags`).
2. In each app, run `go get github.com/steve-gm-walsh/messenger@v0.2.0`.

A breaking change to the API needs a minor bump while the version is below 1.0.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the diagrams.
