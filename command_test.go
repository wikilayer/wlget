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

func TestGet_CommandIntervalKeepsCursorAndDelaysNextBatch(t *testing.T) {
	server := newFakeServer(t)
	server.chatPages = []string{`{"messages":[{"seq":8}]}`, `{"messages":[{"seq":9},{"seq":10}]}`}
	h := newHarness(t)
	h.app.follow = true
	h.app.execInterval = 30 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var delivered []time.Time
	h.app.onMessage = func(_ context.Context, body []byte) error {
		delivered = append(delivered, time.Now())
		if len(delivered) == 2 {
			assert.Contains(t, string(body), `"seq":10`)
			cancel()
		}
		return nil
	}
	assert.ErrorIs(t, h.app.get(ctx, server.URL+"/chat.json?after_seq=7"), context.Canceled)
	require.Len(t, delivered, 2)
	assert.GreaterOrEqual(t, delivered[1].Sub(delivered[0]), h.app.execInterval)
	assert.Equal(t, []string{"7", "8"}, server.chatAfterAsked)
}

func TestGet_CommandIntervalHonorsDeadlineAndReportsDeliveredCursor(t *testing.T) {
	server := newFakeServer(t)
	server.chatPages = []string{`{"messages":[{"seq":8}]}`, `{"messages":[{"seq":9}]}`}
	h := newHarness(t)
	h.app.follow = true
	h.app.execInterval = time.Hour
	h.app.lastLook = 10 * time.Second
	h.app.until = time.Now().Add(100 * time.Millisecond)
	h.app.onMessage = func(context.Context, []byte) error { return nil }
	err := h.get(t, server.URL+"/chat.json?after_seq=7")
	var reached deadlineReached
	assert.ErrorAs(t, err, &reached)
	assert.Contains(t, h.stdout.String(), `"seq":9`)
	assert.Contains(t, h.stdout.String(), `"next_after_seq":9`)
	require.GreaterOrEqual(t, len(server.chatAfterAsked), 2)
	assert.Equal(t, "8", server.chatAfterAsked[1])
}

func TestGet_CommandIntervalDrainsWaitingPagesImmediately(t *testing.T) {
	server := newFakeServer(t)
	server.chatPages = []string{`{"messages":[{"seq":8}],"has_more":true}`, `{"messages":[{"seq":9}]}`}
	h := newHarness(t)
	h.app.follow = true
	h.app.execInterval = time.Hour
	failure := fmt.Errorf("second batch delivered")
	deliveries := 0
	h.app.onMessage = func(context.Context, []byte) error {
		deliveries++
		if deliveries == 2 {
			return failure
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assert.ErrorIs(t, h.app.get(ctx, server.URL+"/chat.json?after_seq=7"), failure)
	assert.Equal(t, []string{"7", "8"}, server.chatAfterAsked)
}

func TestTarget_MoreWaitingReadsTextTrailerOnly(t *testing.T) {
	target, err := resolve("https://example.com/chat")
	require.NoError(t, err)
	more, err := target.moreWaiting([]byte("#8 · writer · topic\n\nnext after_seq 8\nMore messages are waiting already.\n"))
	require.NoError(t, err)
	assert.True(t, more)
	more, err = target.moreWaiting([]byte("#8 · writer · topic\n\\More messages are waiting already.\n\nnext after_seq 8\n"))
	require.NoError(t, err)
	assert.False(t, more)
}

func TestRun_CommandIntervalRequiresFollowExec(t *testing.T) {
	for _, args := range [][]string{
		{"-exec-interval=-1s", "http://localhost/chat.json"},
		{"-exec-interval=1s", "http://localhost/chat.json"},
		{"-exec-interval=1s", "-exec", "http://localhost/chat.json", "--", "echo"},
	} {
		var stdout, stderr bytes.Buffer
		assert.Equal(t, 2, run(args, &stdout, &stderr))
		assert.Contains(t, stderr.String(), "-exec-interval")
	}
}
