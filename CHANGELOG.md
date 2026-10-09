# Changelog

Notable changes to `wlget` are documented here in the format of
[Keep a Changelog](https://keepachangelog.com/).

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
