# messenger architecture

## Context

```mermaid
flowchart TB
%% C4: Context
    you(["You<br><i>on a phone</i>"])
    tg{{"Telegram Bot API<br><i>api.telegram.org</i>"}}
    subgraph apps["House desktop apps - each with its own bot token"]
        cat["cat-weight-tracker"]
        rx["prescription-reminder"]
        lift["lift-log"]
        news["objective-news"]
    end
    apps -- "send, long-poll getUpdates<br><i>messenger/telegram, HTTPS, outgoing only</i>" --> tg
    tg -- "messages, buttons" --> you
    you -- "replies, button presses" --> tg
```

Each app has its own bot token. The apps never share a poller.

## Containers (packages)

```mermaid
flowchart TB
%% C4: Container
    app["A house app<br><i>owns secrets.json, pairing, commands, outbox</i>"]
    subgraph mod["github.com/steve-gm-walsh/messenger"]
        core["messenger<br><i>Backend, Message/Incoming, Error+Kind, Backoff, Listener, helpers</i>"]
        tgp["telegram<br><i>Bot: Me, Send, Receive, Answer, Call - net/http</i>"]
        fake["telegramtest<br><i>fake Bot API on httptest</i>"]
    end
    api{{"Telegram Bot API"}}
    app -- "Listener, Kind, helpers" --> core
    app -- "telegram.New(token)" --> tgp
    tgp -- "implements Backend, returns *Error" --> core
    tgp -- "HTTPS POST /bot&lt;token&gt;/&lt;method&gt;" --> api
    fake -. "stands in for, in tests" .-> api
```

## Where the lines are drawn

- **In the library:**
  - the wire format;
  - error classification;
  - backoff;
  - the poll loop;
  - token hygiene;
  - the fake.

  All four apps had copied these, with small differences.
- **In each app:**
  - `secrets.json` field names (two naming schemes are in use, and pairings must survive the migration);
  - how a chat is chosen (a pair code, the first chat to speak, or the last private chat);
  - where the poll cursor is stored (in memory, beside the secrets, or committed with the app's own state);
  - commands and wording;
  - prescription-reminder's durable outbox.

## Trust boundary

- **Traffic is outgoing only.** The library opens no socket that listens, so the apps' rule 8 posture is unchanged.
- **The bot token is a secret carried in the URL path.**
  - Transport errors, which quote the URL, are replaced by `Unreachable`.
  - A service description is scrubbed before it goes into `Error.Detail`.
  - `Error.Error()` is fixed text per `Kind`.
- **Incoming updates are untrusted.**
  - The library only parses them.
  - Deciding which chat to answer is the app's job: `Incoming.Private` and `From` are there for that check.
