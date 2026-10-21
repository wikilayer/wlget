package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCodexQueue struct {
	items           []queuedNotice
	adds            int
	updates         int
	fail            bool
	consumeOnUpdate bool
}

func (f *fakeCodexQueue) call(method string, params any, result any) error {
	if f.fail {
		return fmt.Errorf("queue unavailable")
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var p struct {
		Input               []noticeInput
		ClientUserMessageID string `json:"clientUserMessageId"`
		ID                  string `json:"queuedSubmissionId"`
	}
	if err := json.Unmarshal(encoded, &p); err != nil {
		return err
	}
	switch method {
	case "thread/queue/list":
		encoded, err = json.Marshal(map[string]any{"data": f.items})
		if err != nil {
			return err
		}
		return json.Unmarshal(encoded, result)
	case "thread/queue/add":
		f.adds++
		f.items = append(f.items, queuedNotice{ID: fmt.Sprint(f.adds), ClientUserMessageID: p.ClientUserMessageID, Input: p.Input})
	case "thread/queue/update":
		f.updates++
		if f.consumeOnUpdate {
			f.consumeOnUpdate = false
			f.items = nil
			return fmt.Errorf("submission already started")
		}
		for i := range f.items {
			if f.items[i].ID == p.ID {
				f.items[i].Input = p.Input
				return nil
			}
		}
		return fmt.Errorf("submission already started")
	case "thread/queue/delete":
		for i := range f.items {
			if f.items[i].ID == p.ID {
				f.items = append(f.items[:i], f.items[i+1:]...)
				break
			}
		}
	default:
		return fmt.Errorf("unexpected method %s", method)
	}
	return nil
}

func TestCodexNoticeCoalescesBusyAgentAndRetainsOtherInput(t *testing.T) {
	f := &fakeCodexQueue{items: []queuedNotice{{ID: "human", ClientUserMessageID: "human", Input: []noticeInput{{Type: "text", Text: "Please review this"}}}}}
	n := codexNotice{thread: "thread", key: "wlget-source-", address: "https://example.com/chat", call: f.call}
	for seq := int64(1); seq <= 100; seq++ {
		require.NoError(t, n.deliver(seq))
	}
	require.Len(t, f.items, 2)
	assert.Equal(t, 1, f.adds)
	assert.Equal(t, 99, f.updates)
	assert.Contains(t, f.items[1].Input[0].Text, "100")
	assert.NoError(t, n.clear(99, false))
	assert.Len(t, f.items, 2, "an acknowledgement cannot remove newer work")
	require.NoError(t, n.clear(100, false))
	require.Len(t, f.items, 1)
	assert.Equal(t, "human", f.items[0].ID)
}

func TestCodexNoticeRestartFindsPendingAndNewArrivalAfterConsumption(t *testing.T) {
	f := &fakeCodexQueue{}
	n := codexNotice{thread: "thread", key: "wlget-source-", address: "https://example.com/chat", call: f.call}
	require.NoError(t, n.deliver(10))
	restarted := codexNotice{thread: n.thread, key: n.key, address: n.address, call: f.call}
	require.NoError(t, restarted.deliver(20))
	assert.Equal(t, 1, f.adds)
	f.items = nil
	require.NoError(t, restarted.deliver(30))
	assert.Equal(t, 2, f.adds)
	require.NoError(t, restarted.clear(0, false))
	assert.Empty(t, f.items)
}

func TestCodexNoticeFailureIsNotDelivery(t *testing.T) {
	f := &fakeCodexQueue{fail: true}
	n := codexNotice{thread: "thread", key: "wlget-source-", call: f.call}
	assert.Error(t, n.deliver(10))
}

func TestCodexNoticeHandlesConsumptionDuringUpdate(t *testing.T) {
	f := &fakeCodexQueue{}
	n := codexNotice{thread: "thread", key: "wlget-source-", address: "https://example.com/chat", call: f.call}
	require.NoError(t, n.deliver(10))
	f.consumeOnUpdate = true
	require.NoError(t, n.deliver(20))
	require.Len(t, f.items, 1)
	assert.Contains(t, f.items[0].Input[0].Text, "through_seq=20")
}

func TestCodexNoticeClearsOnlyExplicitLegacyAndOwnNotifications(t *testing.T) {
	f := &fakeCodexQueue{items: []queuedNotice{
		{ID: "human", ClientUserMessageID: "human", Input: []noticeInput{{Type: "text", Text: "Do this next"}}},
		{ID: "legacy", ClientUserMessageID: "old", Input: []noticeInput{{Type: "text", Text: legacyChatWake}}},
	}}
	n := codexNotice{thread: "thread", key: "wlget-source-", call: f.call}
	require.NoError(t, n.clear(0, false))
	assert.Len(t, f.items, 2)
	require.NoError(t, n.clear(0, true))
	require.Len(t, f.items, 1)
	assert.Equal(t, "human", f.items[0].ID)
}

func TestRunRefusesUnboundedCodexQueueRecipe(t *testing.T) {
	var stdout, stderr strings.Builder
	assert.Equal(t, 2, run([]string{"-follow", "-exec", "https://example.com/chat", "--", "codex", "queue", "--thread", "thread", "--message", "wake"}, &stdout, &stderr))
	assert.Contains(t, stderr.String(), "-codex-thread")
}

func TestCodexNoticeRejectsCanceledConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, withCodex(ctx, "thread", "", func(*codexNotice) error { t.Fatal("canceled connection used"); return nil }))
}

func TestCodexChatIdentitySurvivesFormatFilterAndCursorChanges(t *testing.T) {
	key, _, err := codexChatKey("wikilayer://example.com/chat?after_seq=10&exclude_session=writer")
	require.NoError(t, err)
	for _, address := range []string{"https://example.com/chat.json?after_seq=20", "https://example.com/chat?exclude_topic=unrelated"} {
		actual, _, err := codexChatKey(address)
		require.NoError(t, err)
		assert.Equal(t, key, actual)
	}
	other, _, err := codexChatKey("https://other.example.com/chat")
	require.NoError(t, err)
	assert.NotEqual(t, key, other)
}
