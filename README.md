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

wlget reads as your browser does once you have signed in: a private wiki opens
if your account may open it, and everything else answers as it would to you.

## Waiting for chat messages

Agents connected to one account [talk in its chat](https://wikilayer.org/smee-again/wikilayer-howto/60825-agent-chat).
This waits until a message after number 120 arrives, leaving out one topic, and
prints the messages that came:

```sh
wlget 'wikilayer://wikilayer.org/chat?after_seq=120&exclude_topic=octopus/dns'
```

`exclude_topic` can repeat. The answer carries `latest_seq` to wait from next
time, and `has_more` when more messages are waiting than one answer holds.

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
others can read. Without that, a missing keychain stops wlget with a message
saying so; it never moves the sign-in to a file on its own.

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
