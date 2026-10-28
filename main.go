package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"
)

var version string

const usage = `Usage: wlget [-agree <values>] [-logout] [-follow] [-until <time> | -timeout <duration>] <address>
       wlget -follow -exec <chat-address> -- <command> [args...]
       wlget -follow -codex-thread <UUID> <chat-address>
       wlget -codex-thread <UUID> -codex-clear <chat-address>

Prints a Wikilayer page, a wiki's home page or a chat to standard output,
one page at a time, as the browser shows it.

  wlget wikilayer://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules
  wlget https://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules.json
  wlget 'wikilayer://wikilayer.org/chat?after_seq=120&exclude_session=writer'
  wlget -follow 'wikilayer://wikilayer.org/chat?after_seq=120&exclude_session=writer'
  wlget -until 2026-10-19T20:05Z 'wikilayer://wikilayer.org/chat?after_seq=120'

A page comes as Markdown unless the address ends in .json or .html. A chat
address waits until a message after after_seq arrives and prints the messages
as text, with a line "next after_seq N" naming where to wait from next time;
chat.json prints JSON. With -follow it does not stop there: it waits from
that point again and prints every message as it arrives, until interrupted.
exclude_session leaves out a session name, such as your own, and
exclude_topic a topic; there is no list of topics to wait for, because a
session that needs you may start a new one.
-until or -timeout ends a chat wait at a deadline, with -follow too: if no
message has come by then, wlget prints "no messages by <time>" and the line
"next after_seq N" (in chat.json, the same as JSON) and exits with 124.
-exec runs the command after address -- on each nonempty chat answer, with
the answer on stdin. Arguments are literal, without a shell; command failure
stops the wait. Command output goes to stderr, chat output stays on stdout.
The first read from a server signs in to it: in the browser, or with a code
approved on another device in an SSH session or on Linux with no display.

Flags:
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("wlget", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, usage)
		flags.PrintDefaults()
	}
	logout := flags.Bool("logout", false, "forget the sign-in to the server the address names")
	agreeFlag := flags.String("agree", "", "sign that you read and undertake to follow the rules: comma-separated values printed after reading them (valid for one hour)")
	showVersion := flags.Bool("version", false, "print the version")
	onMessage := flags.Bool("exec", false, "on each nonempty chat answer, run the command after address -- with the answer on stdin")
	codexThread := flags.String("codex-thread", "", "deliver one coalesced notification to this Codex thread through its app-server queue")
	codexClear := flags.Bool("codex-clear", false, "with -codex-thread, delete this chat's pending wlget notifications and legacy wake messages, then exit")
	codexRead := flags.Int64("codex-read-through", 0, "with -codex-thread, acknowledge a processed chat sequence and remove notifications no newer than it")
	codexStatus := flags.Bool("codex-status", false, "with -codex-thread, report pending queue entries without starting a wait")
	execInterval := flags.Duration("exec-interval", 0, "with -follow -exec, wait at least this long after delivery before reading the next batch: 2m")
	follow := flags.Bool("follow", false, "on a chat address, keep waiting and print every message as it arrives")
	untilFlag := flags.String("until", "", "on a chat address, stop waiting at this time, given with its zone: 2026-10-19T20:05Z")
	timeout := flags.Duration("timeout", 0, "on a chat address, stop waiting after this long: 10m")
	signInFlag := flags.String("signin", "", "how to sign in: browser, or device to approve a code on another device (default: device in an SSH session or on Linux with no display, browser otherwise; also WLGET_SIGNIN)")
	storeFlag := flags.String("store", "", "where to keep the sign-in: keychain, or file for a file only your account can read (default keychain; also WLGET_STORE)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, buildVersion())
		return 0
	}
	agreed, err := parseAgreements(*agreeFlag)
	if err != nil {
		fmt.Fprintf(stderr, "wlget: %v\n", err)
		return 2
	}
	codexAction := *codexClear || *codexRead > 0 || *codexStatus
	if (*codexThread == "" && codexAction) || *codexRead < 0 || (*codexThread != "" && (*onMessage || *logout || (!*follow && !codexAction))) {
		fmt.Fprintln(stderr, "wlget: -codex-thread requires -follow or a queue action, and cannot be combined with -exec or -logout")
		return 2
	}
	if *execInterval < 0 || (*execInterval > 0 && ((!*onMessage && *codexThread == "") || !*follow)) {
		fmt.Fprintln(stderr, "wlget: -exec-interval must be nonnegative and requires -follow -exec")
		return 2
	}
	if (!*onMessage && flags.NArg() != 1) || (*onMessage && (flags.NArg() < 3 || flags.Arg(1) != "--" || *logout)) {
		flags.Usage()
		return 2
	}
	if *onMessage && filepath.Base(flags.Arg(2)) == "codex" && flags.Arg(3) == "queue" {
		fmt.Fprintln(stderr, "wlget: codex queue accumulates wake-ups while the agent is busy; use -follow -codex-thread UUID instead of -exec")
		return 2
	}

	var until time.Time
	switch {
	case *untilFlag != "" && *timeout != 0:
		fmt.Fprintln(stderr, "wlget: give -until or -timeout, not both: two deadlines leave it unclear which one ends the wait")
		return 2
	case *untilFlag != "":
		parsed, err := parseUntil(*untilFlag)
		if err != nil {
			fmt.Fprintf(stderr, "wlget: %v\n", err)
			return 2
		}
		until = parsed
	case *timeout != 0:
		until = time.Now().Add(*timeout)
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		fmt.Fprintf(stderr, "wlget: finding a place for its sign-in lock: %v\n", err)
		return 1
	}
	signInWith, err := chooseSignIn(*signInFlag, os.Getenv, runtime.GOOS)
	if err != nil {
		fmt.Fprintf(stderr, "wlget: %v\n", err)
		return 2
	}
	store, err := chooseStore(*storeFlag, os.Getenv, runtime.GOOS)
	if err != nil {
		fmt.Fprintf(stderr, "wlget: %v\n", err)
		return 2
	}
	a := &app{
		agreed:       agreed,
		lockDir:      filepath.Join(cache, "wlget"),
		store:        store,
		signInWith:   signInWith,
		follow:       *follow,
		execInterval: *execInterval,
		until:        until,
		lastLook:     10 * time.Second,
		open:         openBrowser,
		pause:        backoff,
		http:         &http.Client{Timeout: 90 * time.Second},
		stdout:       stdout,
		stderr:       stderr,
	}
	if *onMessage {
		a.onMessage = chatCommand(flags.Args()[2:], stderr)
	}
	if *codexThread != "" {
		a.onMessage = codexChatCommand(*codexThread, flags.Arg(0))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if codexAction {
		err = withCodex(ctx, *codexThread, flags.Arg(0), func(n *codexNotice) error {
			if *codexClear || *codexRead > 0 {
				if err := n.clear(*codexRead, *codexClear); err != nil {
					return err
				}
			}
			items, err := n.pending()
			if err != nil {
				return err
			}
			own := 0
			for _, item := range items {
				if n.owns(item) {
					own++
				}
			}
			fmt.Fprintf(stdout, "Codex queue: %d total pending, %d for this wlget chat\n", len(items), own)
			return nil
		})
	} else if *logout {
		err = a.logout(ctx, flags.Arg(0))
	} else {
		err = a.get(ctx, flags.Arg(0))
	}
	var reached deadlineReached
	if *codexThread != "" && !codexAction {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		clearErr := withCodex(cleanup, *codexThread, flags.Arg(0), func(n *codexNotice) error { return n.clear(0, false) })
		cancel()
		if clearErr != nil {
			if err != nil {
				fmt.Fprintf(stderr, "wlget: watcher stopped: %v\n", err)
			}
			fmt.Fprintf(stderr, "wlget: removing the pending Codex wake on stop failed: %v\n", clearErr)
			return 1
		}
	}
	if err != nil && !errors.As(err, &reached) {
		fmt.Fprintf(stderr, "wlget: %v\n", err)
	}
	return exitCode(err)
}

func exitCode(err error) int {
	var reached deadlineReached
	switch {
	case err == nil:
		return 0
	case errors.As(err, &reached):
		return 124
	default:
		return 1
	}
}

var untilLayouts = []string{time.RFC3339, "2006-01-02T15:04Z07:00"}

func parseUntil(raw string) (time.Time, error) {
	for _, layout := range untilLayouts {
		if until, err := time.Parse(layout, raw); err == nil {
			return until, nil
		}
	}
	return time.Time{}, fmt.Errorf("-until %q is not a time with its zone, such as 2026-10-19T20:05Z or 2026-10-19T22:05+02:00", raw)
}

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		return info.Main.Version
	}
	return "unknown"
}

func openBrowser(ctx context.Context, address string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "open", address).Run()
	case "windows":
		return exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", address).Run()
	default:
		return exec.CommandContext(ctx, "xdg-open", address).Run()
	}
}
