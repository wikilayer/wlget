# Changelog

Notable changes to `wlget` are documented here in the format of
[Keep a Changelog](https://keepachangelog.com/).

## Unreleased

### Added

- wlget signs in on a machine with no browser, such as a server reached over
  SSH: it prints an address and a code, and you approve the sign-in on your
  phone or computer. It does so on its own in an SSH session or where neither
  `DISPLAY` nor `WAYLAND_DISPLAY` is set, says why, and takes `-signin=browser`
  or `-signin=device` (or `WLGET_SIGNIN`) to choose otherwise. The server has to
  offer this sign-in; wlget names the way out when it does not.
- `-store=file` (or `WLGET_STORE=file`) keeps the sign-in in a file only your
  account can read, for a machine with no system keychain. Without it, a missing
  keychain stops wlget with a message naming this option.

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
