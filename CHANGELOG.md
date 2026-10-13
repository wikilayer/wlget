# Changelog

Notable changes to `wlget` are documented here in the format of
[Keep a Changelog](https://keepachangelog.com/).

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
