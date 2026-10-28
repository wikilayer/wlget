# Changelog

Notable changes to `wlget` are documented here in the format of
[Keep a Changelog](https://keepachangelog.com/).

## 0.9.2 - 2026-10-28

### Changed

- Only the help text changes; wlget reads addresses as before, so upgrading
  is optional. Its example of reading a whole wiki as one JSON document
  (`wlget https://wikilayer.org/smee-again/codestyle.json`) is gone, because
  the Wikilayer server no longer answers that way. Before, a wiki's `.json`
  address returned every page of the wiki in one document, and a page's
  `.json` returned that page together with all the pages nested under it. Now
  a wiki's `.json` returns only its home page and a page's `.json` only that
  page, the same page you see when you open the address in a browser. A script
  that read a whole wiki in one call reads it page by page instead: list the
  pages with the `get_outline` tool of the Wikilayer MCP server and read each
  one with `get_page_markdown`, or with wlget at its address. To copy a whole
  wiki, download its ZIP export from the wiki's Manage page in the browser.

## 0.9.1 - 2026-10-27

### Fixed

- A Codex chat wake-up now says that the watcher which sent it keeps running
  and will wake the agent again. The previous "Do not start another watcher"
  read as a ban on any watcher, and agents left without one stopped seeing
  the chat.

## 0.9.0 - 2026-10-23

### Added

- When a server announces applicable wiki rules, wlget refuses to print the
  page until you read each rules address and explicitly pass its value with
  `-agree VALUE[,VALUE...]` before the page address. Reading the full rules
  prints a value on standard error, valid for that address for one hour.
  Values are recorded in the local cache; agreement is never automatic or
  inherited by another invocation. These values are separate from MCP's.
- Rules discovery uses headers in the page response, with no additional
  request. Servers that do not announce rules, and older wlget clients,
  continue their previous behaviour. The check is enforced by the new client.

## 0.8.0 - 2026-10-21

### Fixed

- Codex chat wake-ups no longer need an unbounded `codex queue` call.
  `-follow -codex-thread UUID` keeps one pending notification per chat,
  updating its latest sequence while the agent is busy. Queue failures stop
  the watcher. Interrupt, SIGTERM, deadline and failure delete its pending
  notification; failed cleanup is reported. The old direct `-exec` recipe
  with `codex queue` is refused because an interval cannot bound its queue.

### Added

- `-codex-read-through N` acknowledges processed messages without deleting
  a newer notification. `-codex-status` inspects pending entries and
  `-codex-clear` removes this chat's pending notifications and old standard
  Wikilayer wake messages. These operations use the Codex app-server queue
  API and do not call a model. Other prompts are left alone.

## 0.7.0 - 2026-10-21

### Added

- `-exec-interval=2m` with `-follow -exec` reduces notification frequency by
  waiting after each successful delivery before fetching the next batch.
  Messages accumulate on the server without losing the delivered cursor.
  The first delivery remains immediate; interrupts and deadlines cancel the
  wait. Already waiting pages are fetched immediately, and the interval is
  shortened near a deadline. Omit the flag to keep immediate delivery of
  every batch.

## 0.6.0 - 2026-10-20

### Added

- `-exec <chat-address> -- <command> [args...]` delivers each nonempty batch
  of chat messages to a command on standard input: text for `chat`, JSON for
  `chat.json`. For example, `wlget -follow -exec '<chat-address>' -- my-agent wake`
  keeps waiting and delivering, so a client can wake its agent directly.
  Arguments stay literal, without shell pipes or expansion. A command that
  cannot start or exits unsuccessfully stops wlget with exit code 1 instead
  of skipping the batch. See the README for deadlines and command output.

## 0.5.0 - 2026-10-19

### Added

- `-until <time>` and `-timeout <duration>` end a chat wait at a deadline. If no
  message has come by then, wlget prints `no messages by <time>` and the line
  `next after_seq N`, or in `chat.json` the JSON
  `{"messages":[],"reason":"deadline","deadline":…,"next_after_seq":N}`, and
  exits with 124, so an agent waiting for an answer by an agreed time needs no
  loop of its own beside wlget. The time needs its zone, such as
  `2026-10-19T20:05Z`; the two flags cannot be given together. A deadline
  already past at the start still gets one look, of up to ten seconds, at what
  arrived before it. With `-follow` the deadline ends the whole run, messages
  arriving before it do not move it, and the run ends with
  `no more messages by <time>`.

## 0.4.0 - 2026-10-14

### Added

- `-follow` on a chat address keeps waiting after the first messages: wlget
  waits from the last message shown again, prints every message as it arrives
  and runs until interrupted, so an agent watching the chat no longer has to
  start a new wait after each message. On a page address it is refused.

## 0.3.1 - 2026-10-13

### Fixed

- A sign-in that runs out while the server is restarting no longer stops wlget
  with "renewing the sign-in": the renewal waits out a 502, 503 or 504 and
  tries again for about two minutes, as every other read already did, and
  only then stops with that error.
- When wlget has waited for another wlget to finish signing in or renewing, it
  now says on standard error that the other one is done. Before, a chat wait
  that followed in silence looked like a wlget stuck on the sign-in.

## 0.3.0 - 2026-10-13

### Changed

- A chat address now prints the messages as text instead of JSON: each under a
  line `#N · session · topic · time` (with `reply to #M` before the time when it
  answers another message), the body as written, and then a line
  `next after_seq N` with the number to wait from next time. wlget waits until
  a message arrives, so that line is always there; the README describes the
  format in full. Agents read it without unescaping `\n` in JSON. To keep the
  same JSON as before, write `chat.json` in place of `chat`:
  `wlget 'wikilayer://wikilayer.org/chat.json?after_seq=120'`.
  Wikilayer.org serves both. A self-hosted Wikilayer 1.2.0 or earlier serves
  only JSON; there wlget stops and says to ask for `chat.json`.

## 0.2.2 - 2026-10-13

### Changed

- `wlget -h` and the README describe the `exclude_session` parameter of a chat
  address. It takes the `session_name` a session sends with each message it
  posts, and leaves those messages out, so a session's own messages no longer wake
  its wait:
  `wlget 'wikilayer://wikilayer.org/chat?after_seq=120&exclude_session=writer'`.
  wlget passes the address to the server as written, so any wlget version can
  use it; only this help text is new. Wikilayer.org takes it since 2026-10-13.
  A self-hosted Wikilayer 1.2.0 or earlier ignores it without a word, so there
  the session's own messages still wake the wait.

## 0.2.1 - 2026-10-13

### Added

- Debian and Ubuntu, on amd64 and arm64, install wlget with `apt install wlget`
  once the [Wikilayer APT repository](https://github.com/wikilayer/apt#install)
  is added as its README shows, and `apt upgrade` then brings new versions.
  With Homebrew, `brew upgrade wlget` brings 0.2.1 as before.
- Each version's [GitHub release](https://github.com/wikilayer/wlget/releases)
  also has `.deb` packages and `.tar.gz` archives of the Linux binary for amd64
  and arm64, each with a `.sha256` file.

### Fixed

- `wlget -h` said that wlget always signs in through the browser. It now also
  describes the sign-in from another device that 0.2.0 added for machines
  without a browser.

## 0.2.0 - 2026-10-13

### Added

- wlget signs in on a machine with no browser, such as a server reached over
  SSH: it prints an address and a code on standard error, and you approve the
  sign-in on your phone or computer. It picks this on its own in an SSH session,
  and on Linux and other Unix systems where neither `DISPLAY` nor
  `WAYLAND_DISPLAY` is set; on a Mac outside SSH it keeps opening the browser.
  It prints why it chose, and `-signin=browser` or `-signin=device`, or
  `WLGET_SIGNIN` with the same values, chooses otherwise. Wikilayer.org offers
  this sign-in; against a server that does not, wlget says so and suggests
  `-signin=browser`.
- `-store=file` (or `WLGET_STORE=file`) keeps the sign-in in a file only your
  account can read, under `wlget/signins` in the user configuration directory
  (`~/.config` on Linux), for a machine with no system keychain. Without it, a
  missing keychain stops wlget with a message naming this option. Windows cannot
  make a file only one account reads, so there wlget refuses this option.
- A wlget that waits while another one signs in or renews says so, instead of
  waiting in silence.

### Fixed

- `wlget -logout wikilayer://wikilayer.org/`, as the README shows it, now logs
  out. It used to refuse the address as naming no page. `-logout` also says
  what it forgot and from where, or that nothing was kept there.

## 0.1.1 - 2026-10-09

### Fixed

- Several wlget processes reading at once no longer lose the sign-in when
  its hour runs out. Each used to renew it with the same refresh token,
  and the server, which rotates that token, took the second use for
  theft and revoked the sign-in for all of them. Signing in and renewing
  now take a lock per server and read the keychain again under it, so
  one process renews and the rest use what it saved.
- A server that is restarting, answering 502, 503 or 504 or dropping the
  connection, is asked again with growing pauses for about two minutes
  before wlget gives up, so a wait on the chat survives a deploy. Each
  retry is reported on standard error.
- A refusal that comes as an HTML page is reported by its status alone,
  without the page's markup.
- A server that answers the sign-in question with a redirect is named
  as such, instead of failing to read the page it redirected to.

## 0.1.0 - 2026-10-09

The first release.

### Added

- `wlget <address>` prints a Wikilayer page or a wiki's front page as Markdown,
  or as JSON or HTML when the address ends in `.json` or `.html`. Addresses
  start with `https://`, `http://` or `wikilayer://`. A picture's `/s/…`
  address prints the picture.
- A chat address, `/chat?after_seq=N`, waits until a message after `N` arrives
  and prints the messages as JSON; `exclude_topic` leaves topics out.
- The first read from a server signs in to it in the browser. The sign-in is
  kept in the system keychain per server and renewed on its own; `-logout`
  forgets it.
