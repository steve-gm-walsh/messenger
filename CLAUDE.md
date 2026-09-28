# messenger

A public Go library, `github.com/steve-gm-walsh/messenger`, that holds the
chat-bot plumbing the house apps share. Telegram is the one backend. It was
created on 2026-09-28 from the copies in cat-weight-tracker,
prescription-reminder, lift-log and objective-news.

## Entry points

- `messenger.go`: `Backend`, `Message`, `Incoming`, `Identity`.
- `errors.go`: `Error`/`Kind`, `KindOf`, and `Backoff`. Each Kind has fixed text that is safe for a page.
- `listen.go`: `Listener.Run`, the long poll with backoff; `Sleep`.
- `helpers.go`: `NewCode`, `Command`, `WriteFileAtomic`.
- `telegram/telegram.go`: `Bot` implements `Backend`; `Call` covers methods that are not wrapped.
- `telegram/telegramtest`: the fake Bot API used by this module's tests and by the apps' tests.

## Verbs

`run.bat setup | test | build | clean`. Bare `run.bat` builds and prints how to use the module. No port and no `app.json`: it is a library.

## Consumers

These apps require it at a tag, through proxy.golang.org:

- cat-weight-tracker
- prescription-reminder
- lift-log
- objective-news

Consumers use no `replace` directive. That is the opposite of psx-graphics; the reason is in the 2026-09-28 entry of the decision log in `standards/go/STANDARDS.md`.

## Sharp edges

- **The token is in every URL.** Never return a transport error or a URL. `do()` maps every failure to `*messenger.Error`, and `Detail` is scrubbed.
- **Numeric chat ids go out as JSON numbers** (`chatID`). The apps' own test fakes decode them as int64.
- **`Receive` returns skipped updates.** Updates it does not understand come back with only `Cursor` set, so apps can still persist the offset past them.
- **The Listener keeps an in-memory cursor per `Account`.** An app that stores no offset, such as lift-log, still never sees an update twice while it is running.
- **Caption limit.** Photo captions are cut to 1024 runes here. Text messages are not split: 4096 is the caller's problem.
- **Releasing.** This is a public module and tags are immutable. Test, then tag `vX.Y.Z`, then `go get` it in each app. Never move a tag.
