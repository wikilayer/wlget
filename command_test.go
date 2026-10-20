package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatCommand_PassesMessagesAsInputAndArgumentsLiterally(t *testing.T) {
	if os.Getenv("WLGET_COMMAND_HELPER") == "1" {
		body, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(1)
		}
		fmt.Printf("%s|%s", os.Args[len(os.Args)-1], body)
		os.Exit(0)
	}
	t.Setenv("WLGET_COMMAND_HELPER", "1")
	executable, err := os.Executable()
	require.NoError(t, err)
	var output bytes.Buffer
	command := chatCommand([]string{executable, "-test.run=^TestChatCommand_PassesMessagesAsInputAndArgumentsLiterally$", "--", "$(not-a-command); literal"}, &output)
	require.NoError(t, command(context.Background(), []byte("untrusted chat; $(also-not-a-command)")))
	assert.Equal(t, "$(not-a-command); literal|untrusted chat; $(also-not-a-command)", output.String())
}

func TestChatCommand_ReportsFailure(t *testing.T) {
	command := chatCommand([]string{t.TempDir() + "/missing-command"}, io.Discard)
	assert.Error(t, command(context.Background(), []byte("Ready")))
}

func TestChatCommand_CancellationDoesNotWaitForInheritedInput(t *testing.T) {
	mode := os.Getenv("WLGET_CANCEL_HELPER")
	if mode == "child" {
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	if mode == "parent" {
		executable, err := os.Executable()
		if err != nil {
			os.Exit(1)
		}
		child := exec.Command(executable, "-test.run=^TestChatCommand_CancellationDoesNotWaitForInheritedInput$")
		child.Env = append(os.Environ(), "WLGET_CANCEL_HELPER=child")
		child.Stdin = os.Stdin
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		fmt.Println(child.Process.Pid)
		if err := child.Wait(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	t.Setenv("WLGET_CANCEL_HELPER", "parent")
	executable, err := os.Executable()
	require.NoError(t, err)
	output, writer := io.Pipe()
	defer output.Close()
	defer writer.Close()
	command := chatCommand([]string{executable, "-test.run=^TestChatCommand_CancellationDoesNotWaitForInheritedInput$"}, writer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- command(ctx, bytes.Repeat([]byte("x"), 1024*1024))
	}()
	ready := make(chan string, 1)
	go func() {
		line, readErr := bufio.NewReader(output).ReadString('\n')
		if readErr == nil {
			ready <- line
		}
	}()
	var line string
	select {
	case line = <-ready:
	case err := <-finished:
		t.Fatalf("command stopped before its child was ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("command did not report its child")
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(line))
	require.NoError(t, parseErr)
	child, findErr := os.FindProcess(pid)
	require.NoError(t, findErr)
	defer func() { assert.NoError(t, child.Kill()) }()
	cancel()
	select {
	case err := <-finished:
		assert.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("command cancellation waited for its child to close stdin")
	}
}

func TestGet_CommandDoesNotRunForEmptyAnswersAndFailureStopsFollow(t *testing.T) {
	server := newFakeServer(t)
	first := `{"messages":[{"seq":8}],"latest_seq":99,"has_more":true}`
	second := `{"messages":[{"seq":9}],"latest_seq":99,"has_more":true}`
	server.chatPages = []string{`{"messages":[]}`, first, second}
	h := newHarness(t)
	h.app.follow = true
	var received []string
	failure := fmt.Errorf("queue unavailable")
	h.app.onMessage = func(_ context.Context, body []byte) error {
		received = append(received, string(body))
		if len(received) == 2 {
			return failure
		}
		return nil
	}
	err := h.get(t, server.URL+"/chat.json?after_seq=7")
	assert.ErrorIs(t, err, failure)
	assert.Equal(t, []string{first, second}, received)
	assert.Equal(t, []string{"7", "7", "8"}, server.chatAfterAsked)
	assert.Contains(t, h.stderr.String(), "restart with after_seq=8")
}

func TestRun_CommandRequiresAddressSeparatorAndExecutable(t *testing.T) {
	for _, args := range [][]string{
		{"-exec", "http://localhost/chat.json"},
		{"-exec", "http://localhost/chat.json", "--"},
		{"-exec", "http://localhost/chat.json", "echo"},
		{"-exec", "-logout", "http://localhost/chat.json", "--", "echo"},
	} {
		var stdout, stderr bytes.Buffer
		assert.Equal(t, 2, run(args, &stdout, &stderr), args)
	}
}
