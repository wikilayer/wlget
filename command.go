package main

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"time"
)

func chatCommand(args []string, output io.Writer) func(context.Context, []byte) error {
	return func(ctx context.Context, body []byte) error {
		command := exec.CommandContext(ctx, args[0], args[1:]...)
		command.WaitDelay = time.Second
		command.Stdin = bytes.NewReader(body)
		command.Stdout = output
		command.Stderr = output
		return command.Run()
	}
}
