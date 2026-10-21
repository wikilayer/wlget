//go:build integration

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexQueueIntegration(t *testing.T) {
	thread := os.Getenv("WLGET_CODEX_THREAD")
	require.NotEmpty(t, thread, "run make test-codex THREAD=<busy test thread UUID>")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := withCodex(ctx, thread, "https://wlget-integration.invalid/chat", func(n *codexNotice) error {
		defer func() { assert.NoError(t, n.clear(0, false)) }()
		for seq := int64(1); seq <= 20; seq++ {
			if err := n.deliver(seq); err != nil {
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
				seq, err := noticeSequence(item)
				if err != nil {
					return err
				}
				assert.EqualValues(t, 20, seq)
			}
		}
		assert.Equal(t, 1, own, "twenty events must leave one pending wake")
		if err := n.clear(19, false); err != nil {
			return err
		}
		items, err = n.pending()
		if err != nil {
			return err
		}
		own = 0
		for _, item := range items {
			if n.owns(item) {
				own++
			}
		}
		assert.Equal(t, 1, own, "older acknowledgement cannot erase the latest wake")
		return n.clear(20, false)
	})
	require.NoError(t, err)
	assert.NoError(t, withCodex(ctx, thread, "https://wlget-integration.invalid/chat.json?after_seq=20", func(n *codexNotice) error {
		items, err := n.pending()
		if err != nil {
			return err
		}
		for _, item := range items {
			assert.False(t, n.owns(item), "acknowledged notification survived a reconnect")
		}
		return nil
	}))
}
