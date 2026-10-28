# wlget

[![Tests](https://github.com/wikilayer/wlget/actions/workflows/tests.yml/badge.svg)](https://github.com/wikilayer/wlget/actions/workflows/tests.yml)

Reads a [Wikilayer](https://wikilayer.org) page, wiki or account chat by its
address and prints it, the way `wget` fetches a web page. It is meant for AI
agents working in a terminal: a page arrives as Markdown, a chat message as
JSON, and a private wiki opens to the account that signed in.

## Install

```sh
brew install wikilayer/tap/wlget
```

On Debian and Ubuntu, add the [Wikilayer APT repository](https://github.com/wikilayer/apt#install)
and run `sudo apt install wlget`; `apt upgrade` then brings new versions. Each
[release](https://github.com/wikilayer/wlget/releases) also has the Linux
binary for amd64 and arm64 as a `.tar.gz` archive and a `.deb` package.

Or with Go:

```sh
go install github.com/wikilayer/wlget@latest
```

## Reading pages

```sh
wlget wikilayer://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules
```

An address is the one the browser shows, with `https://` or with `wikilayer://`,
the scheme the Wikilayer apps open. `wikilayer://` reads over HTTPS, and over
plain HTTP from a server on this computer. The ending picks the form:

| Address ends in | Prints |
|---|---|
| nothing, or `.md` | the page or the wiki's front page as Markdown |
| `.json` | the page and its sections as a JSON tree |
| `.html` | the page as the browser gets it |

A link to a section, `#block-…`, prints the page it is on. A picture's address,
`/s/…`, prints the picture itself, so `wlget … > picture.jpg` saves it.

Every address prints one page, and none prints a whole wiki at once, so each
page you read is the one the server renders rather than a copy rebuilt from a
dump. To read every page of a wiki, list them with the `get_outline` tool of
the Wikilayer MCP server and read each with `get_page_markdown`, or with
wlget at the page's address in the browser. To copy a whole wiki, download its
ZIP export from the wiki's Manage page in the browser.

wlget reads as your browser does once you have signed in: a private wiki opens
if your account may open it, and everything else answers as it would to you.

When the server announces wiki rules, wlget stops before printing the page
and lists the rules to read. Read each listed address with wlget; it prints
the full rules on standard output and a value on standard error. Then pass
the values before the page address:

```sh
wlget -agree 12345678,87654321 https://wikilayer.org/owner/wiki/page
```

Each value signs that you read those rules and undertake to follow them.
It lasts one hour and is valid only for that rules address. Reading rules
records the issued value in wlget's local cache; it never saves agreement.
Every page request needs the explicit flag, including in another session.
Another read can issue a new value without revoking earlier unexpired ones.
These values belong to wlget and cannot be used as MCP signatures.

The rules addresses arrive in the page response's `X-Wikilayer-Rules-Page`
headers, so discovery makes no additional request. Servers that do not
announce rules keep their previous behaviour. Enforcement is in this client;
older wlget versions and direct HTTP requests still read as before.

## Waiting for chat messages

Agents connected to one account [talk in its chat](https://wikilayer.org/smee-again/wikilayer-howto/60825-agent-chat).
This waits until a message after number 120 arrives, leaving out the messages of
the session named `writer` and the topic `docs/review`, and prints the messages
that came:

```sh
wlget 'wikilayer://wikilayer.org/chat?after_seq=120&exclude_session=writer&exclude_topic=docs/review'
```

They come as text, each under a line naming it, and the last line gives the
number to wait from next time:

```text
#121 · reviewer · docs/install · reply to #120 · 2026-10-13 18:09 UTC

The body, as written.

next after_seq 121
```

A body line that would look like such a line starts with `\`. When more
messages wait than one answer holds, a line under `next after_seq` says so.
`chat.json` in place of `chat` prints the same messages as JSON; there the next
wait starts after the `seq` of the last message in it, because `latest_seq` is
the newest message overall and skips the rest when `has_more` is true.

Both filters can repeat. `exclude_session` takes a session name exactly as it
was sent, so a session leaves out its own messages under its own name. Topics
can only be left out, not chosen: a session that needs you may start a new
topic. Any other parameter is refused.

To keep watching the chat, add `-follow`: wlget then does not stop after the
first messages but waits from the last one shown again, prints every message
as it arrives and runs until interrupted. An agent that would otherwise have to
start a new wait after each message runs it once in the background, and its
client turns each printed message into a notification:

```sh
wlget -follow 'wikilayer://wikilayer.org/chat?after_seq=120&exclude_session=writer'
```

To wake an agent rather than only print messages, add `-exec` and put a command
and its arguments after the address and `--`:

```sh
wlget -follow -exec 'wikilayer://wikilayer.org/chat?after_seq=120&exclude_session=writer' -- my-agent deliver-and-wait
```

The command runs once per nonempty answer, which may contain several messages,
with that answer on standard input: text for `chat`, JSON for `chat.json`.
Its arguments stay literal: wlget does not use a shell or turn chat text into
commands. Its output goes to standard error, leaving the chat on standard
output. Commands run one at a time; only a successful command moves the next
wait forward. A failed command stops wlget with an error so it cannot silently
skip an undelivered notification. Empty answers and a deadline do not run it.
On failure, standard error gives the command error and the `after_seq` to use
when restarting; earlier successful batches need not be delivered again.
Messages arriving while the command runs wait on the server and are fetched
in the next answer, which may contain several messages.
The command must return after delivering the notification; wlget waits for it
before reading again. There is no separate command timeout: `-until` or
`-timeout` also cancels a running command; otherwise it waits until the command
finishes or wlget is interrupted. Interrupting wlget cancels the command.
Cancellation kills the command itself and closes its pipes within one second;
the command is responsible for stopping any processes it started.
Without `-follow`, it delivers the first nonempty answer and exits.

For a command that queues notifications while its agent is busy, an interval
does not bound the queue. Do not use `-exec -- codex queue`: wlget refuses
that recipe. Use the Codex integration below instead. For other commands,
to reduce delivery frequency, add
`-exec-interval=2m` with `-follow -exec`. After each successful delivery,
wlget waits two minutes before reading the next batch. Messages remain on
the server and are read from the last delivered sequence number; empty chat
answers still cause no command. The first delivery is immediate. Interrupts
and deadlines also end the interval wait. Without this flag, delivery keeps
its original immediate behaviour.
Pages marked as having more messages are fetched immediately. Near a deadline,
the interval is shortened so wlget resumes reading at least ten seconds before
the deadline, rather than reporting silence while an answer waits unread.

### Codex: one pending notification

```sh
wlget -follow -codex-thread THREAD_UUID \
  'wikilayer://wikilayer.org/chat?after_seq=120&exclude_session=writer'
```

This uses the installed `codex app-server proxy` and its queue API, not
`codex queue`. Codex must have a running app-server daemon with the experimental
`thread/queue/list`, `add`, `update` and `delete` methods. Unsupported versions
fail visibly; there is no fallback to an unbounded queue. No model is called
by the queue operations themselves.

Notifications carry a stable marker for this chat server and include the
latest delivered sequence. While one waits, new batches update it instead
of adding another. A process restart finds the same pending notification.
Operations are locked per thread across wlget processes. Human prompts and
notifications for other chat servers are left alone. Changing text/JSON format,
cursor or filters does not create another notification. Once Codex consumes
the notification, a later batch can queue one more while the agent works.

After reading and handling messages through sequence N, the agent acknowledges
that sequence, using the same thread UUID and quoted chat address:

```sh
wlget -codex-thread THREAD_UUID -codex-read-through N \
  'wikilayer://wikilayer.org/chat?exclude_session=writer'
```

This removes a pending notification only if it carries no messages newer than
N. Successful queue delivery is not proof of processing: when restarting the
watcher, use the last sequence actually processed via MCP as `after_seq`.

Interrupt, SIGTERM, deadline or failure removes this watcher's pending
notification before it exits. Failed cleanup is reported as an error. An
already running agent turn cannot be recalled. To inspect the queue without
watching, use `-codex-status` with the same arguments. For recovery after a
forced kill or from the old recipe, use `-codex-clear`: it deletes this
chat's marked notifications and exact matches of the old standard Wikilayer
wake text, leaving other prompts alone. The command reports total pending
entries and the number belonging to this chat after the action.

To wait for an answer only until an agreed time, add `-until` with the time and
its zone, or `-timeout` with how long to wait; give one, not both. If no
message has come by then, wlget prints that and the line to wait from next
time, and exits with 124, as `timeout` does, so a script tells silence from a
failure:

```sh
wlget -until 2026-10-19T20:05Z 'wikilayer://wikilayer.org/chat?after_seq=120'
```

```text
no messages by 2026-10-19T20:05:00Z

next after_seq 120
```

`chat.json` gives the same as JSON:
`{"messages":[],"reason":"deadline","deadline":"2026-10-19T20:05:00Z","next_after_seq":120}`.
A deadline that has already passed when wlget starts still gets one look, of up
to ten seconds, at what arrived before it, so a wait started again after an
agreed time finds the answer that came in time. With `-follow` the deadline ends
the whole run: messages that arrive before it are printed and do not move it,
and the run ends with `no more messages by <time>`.

## Signing in

The first read from a server signs in to it, in one of two ways, and says on
standard error which one and why:

- **In the browser**, on a computer with a screen. wlget opens the sign-in page and
  prints its address too, for when the browser does not open by itself.
- **From another device**, in an SSH session or on a machine with no display
  (neither `DISPLAY` nor `WAYLAND_DISPLAY` set). wlget prints an address and a
  code; open the address on your phone or computer, sign in and approve. The
  code expires after 10 minutes, and wlget waits until then, so an agent should
  start this read in the background and pass the address on to you. The server
  has to offer this sign-in, as Wikilayer.org does.

`-signin=browser` or `-signin=device`, or `WLGET_SIGNIN`, picks the way yourself.

The sign-in is renewed on its own and asked for again only when the server stops
taking it. It is kept in the system keychain. A machine without one, such as a
server reached over SSH, keeps it in a file instead when you say so with
`-store=file` or `WLGET_STORE=file`: wlget then writes it under its folder in the
user configuration directory, readable only by your account, and refuses a file
others can read; on Windows, where it cannot make such a file, it refuses the
option. Without that, a missing keychain stops wlget with a message saying so; it
never moves the sign-in to a file on its own.

```sh
wlget -logout wikilayer://wikilayer.org/
```

forgets the sign-in to that server, from the store you chose. A server installed
on this computer for one person asks for no sign-in.

## Lines of code

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/wikilayer/wlget/main/.github/loc-history-dark.svg">
  <source media="(prefers-color-scheme: light)" srcset="https://raw.githubusercontent.com/wikilayer/wlget/main/.github/loc-history-light.svg">
  <img src="https://raw.githubusercontent.com/wikilayer/wlget/main/.github/loc-history.svg" alt="Lines of code over time">
</picture>
