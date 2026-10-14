package main

import (
	"context"
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
	"time"
)

var version string

const usage = `Usage: wlget [-logout] [-follow] <address>

Prints a Wikilayer page, wiki or chat to standard output.

  wlget wikilayer://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules
  wlget https://wikilayer.org/smee-again/codestyle.json
  wlget 'wikilayer://wikilayer.org/chat?after_seq=120&exclude_session=writer'
  wlget -follow 'wikilayer://wikilayer.org/chat?after_seq=120&exclude_session=writer'

A page comes as Markdown unless the address ends in .json or .html. A chat
address waits until a message after after_seq arrives and prints the messages
as text, with a line "next after_seq N" naming where to wait from next time;
chat.json prints JSON. With -follow it does not stop there: it waits from
that point again and prints every message as it arrives, until interrupted.
exclude_session leaves out a session name, such as your own, and
exclude_topic a topic; there is no list of topics to wait for, because a
session that needs you may start a new one.
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
	showVersion := flags.Bool("version", false, "print the version")
	follow := flags.Bool("follow", false, "on a chat address, keep waiting and print every message as it arrives")
	signInFlag := flags.String("signin", "", "how to sign in: browser, or device to approve a code on another device (default: device in an SSH session or on Linux with no display, browser otherwise; also WLGET_SIGNIN)")
	storeFlag := flags.String("store", "", "where to keep the sign-in: keychain, or file for a file only your account can read (default keychain; also WLGET_STORE)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, buildVersion())
		return 0
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
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
		lockDir:    filepath.Join(cache, "wlget"),
		store:      store,
		signInWith: signInWith,
		follow:     *follow,
		open:       openBrowser,
		pause:      backoff,
		http:       &http.Client{Timeout: 90 * time.Second},
		stdout:     stdout,
		stderr:     stderr,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *logout {
		err = a.logout(ctx, flags.Arg(0))
	} else {
		err = a.get(ctx, flags.Arg(0))
	}
	if err != nil {
		fmt.Fprintf(stderr, "wlget: %v\n", err)
		return 1
	}
	return 0
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
