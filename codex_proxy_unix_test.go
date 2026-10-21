//go:build !windows

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if ready := os.Getenv("WLGET_BLOCKED_CODEX_PROXY"); ready != "" {
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(ready, []byte("handshake received"), 0o600); err != nil {
			os.Exit(2)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCodexCancellationInterruptsBlockedWebsocketHandshake(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(executable, filepath.Join(dir, "codex")))
	ready := filepath.Join(dir, "ready")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WLGET_BLOCKED_CODEX_PROXY", ready)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- withCodex(ctx, "test-cancellation", "https://example.com/chat", func(*codexNotice) error { return fmt.Errorf("unexpected initialized connection") })
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-finished:
			t.Fatalf("proxy exited before handshake: %v", err)
		case <-deadline.C:
			t.Fatal("proxy did not receive the handshake")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-finished:
		assert.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not stop the blocked proxy")
	}
}
