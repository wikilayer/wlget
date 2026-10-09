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
	"runtime"
	"runtime/debug"
	"time"
)

var version string

const usage = `Usage: wlget [-logout] <address>

Prints a Wikilayer page, wiki or chat to standard output.

  wlget wikilayer://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules
  wlget https://wikilayer.org/smee-again/codestyle.json
  wlget 'wikilayer://wikilayer.org/chat?after_seq=120&exclude_topic=octopus/dns'

A page comes as Markdown unless the address ends in .json or .html. A chat
address waits until a message after after_seq arrives and prints it as JSON.
The first read from a server asks you to sign in to it in the browser.

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

	a := &app{
		store:  keyringStore{},
		open:   openBrowser,
		pause:  backoff,
		http:   &http.Client{Timeout: 90 * time.Second},
		stdout: stdout,
		stderr: stderr,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	if *logout {
		err = a.logout(flags.Arg(0))
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
