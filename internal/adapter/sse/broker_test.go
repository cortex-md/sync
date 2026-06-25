package sse_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/sse"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroker_SubscribeAndPublish(t *testing.T) {
	b := sse.NewBroker(64)
	vaultID := uuid.New()

	ch, err := b.Subscribe(context.Background(), vaultID, "client-1")
	require.NoError(t, err)

	event := port.SSEEvent{ID: "1", EventType: "file_updated", Data: `{"file_path":"test.md"}`}
	b.Publish(vaultID, event)

	select {
	case received := <-ch:
		assert.Equal(t, event.ID, received.ID)
		assert.Equal(t, event.EventType, received.EventType)
		assert.Equal(t, event.Data, received.Data)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestBroker_MultipleSubscribers(t *testing.T) {
	b := sse.NewBroker(64)
	vaultID := uuid.New()

	ch1, err := b.Subscribe(context.Background(), vaultID, "client-1")
	require.NoError(t, err)
	ch2, err := b.Subscribe(context.Background(), vaultID, "client-2")
	require.NoError(t, err)

	event := port.SSEEvent{ID: "1", EventType: "file_created", Data: `{"file_path":"new.md"}`}
	b.Publish(vaultID, event)

	select {
	case received := <-ch1:
		assert.Equal(t, "1", received.ID)
	case <-time.After(time.Second):
		t.Fatal("client-1 timed out")
	}

	select {
	case received := <-ch2:
		assert.Equal(t, "1", received.ID)
	case <-time.After(time.Second):
		t.Fatal("client-2 timed out")
	}
}

func TestBroker_PublishToCorrectVault(t *testing.T) {
	b := sse.NewBroker(64)
	vault1 := uuid.New()
	vault2 := uuid.New()

	ch1, err := b.Subscribe(context.Background(), vault1, "client-1")
	require.NoError(t, err)
	ch2, err := b.Subscribe(context.Background(), vault2, "client-2")
	require.NoError(t, err)

	event := port.SSEEvent{ID: "1", EventType: "file_updated", Data: `{"vault":"1"}`}
	b.Publish(vault1, event)

	select {
	case received := <-ch1:
		assert.Equal(t, "1", received.ID)
	case <-time.After(time.Second):
		t.Fatal("vault1 subscriber timed out")
	}

	select {
	case <-ch2:
		t.Fatal("vault2 subscriber should not receive vault1 event")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBroker_Unsubscribe(t *testing.T) {
	b := sse.NewBroker(64)
	vaultID := uuid.New()

	ch, err := b.Subscribe(context.Background(), vaultID, "client-1")
	require.NoError(t, err)
	assert.Equal(t, 1, b.SubscriberCount(vaultID))

	b.Unsubscribe(vaultID, "client-1")
	assert.Equal(t, 0, b.SubscriberCount(vaultID))

	_, ok := <-ch
	assert.False(t, ok, "channel should be closed after unsubscribe")
}

func TestBroker_UnsubscribeNonexistent(t *testing.T) {
	b := sse.NewBroker(64)
	b.Unsubscribe(uuid.New(), "nonexistent")
}

func TestBroker_ResubscribeSameClient(t *testing.T) {
	b := sse.NewBroker(64)
	vaultID := uuid.New()

	ch1, err := b.Subscribe(context.Background(), vaultID, "client-1")
	require.NoError(t, err)

	ch2, err := b.Subscribe(context.Background(), vaultID, "client-1")
	require.NoError(t, err)

	_, ok := <-ch1
	assert.False(t, ok, "old channel should be closed on resubscribe")

	event := port.SSEEvent{ID: "1", EventType: "file_updated", Data: "test"}
	b.Publish(vaultID, event)

	select {
	case received := <-ch2:
		assert.Equal(t, "1", received.ID)
	case <-time.After(time.Second):
		t.Fatal("new channel should receive events")
	}

	assert.Equal(t, 1, b.SubscriberCount(vaultID))
}

func TestBroker_PublishNoSubscribers(t *testing.T) {
	b := sse.NewBroker(64)
	b.Publish(uuid.New(), port.SSEEvent{ID: "1", EventType: "file_updated", Data: "test"})
}

func TestBroker_DropsEventsOnFullBuffer(t *testing.T) {
	b := sse.NewBroker(2)
	vaultID := uuid.New()

	ch, err := b.Subscribe(context.Background(), vaultID, "slow-client")
	require.NoError(t, err)

	b.Publish(vaultID, port.SSEEvent{ID: "1", EventType: "file_updated", Data: "event-1"})
	b.Publish(vaultID, port.SSEEvent{ID: "2", EventType: "file_updated", Data: "event-2"})
	b.Publish(vaultID, port.SSEEvent{ID: "3", EventType: "file_updated", Data: "event-3"})

	received := 0
	for {
		select {
		case <-ch:
			received++
		case <-time.After(100 * time.Millisecond):
			assert.Equal(t, 2, received, "buffer size is 2, third event should be dropped")
			return
		}
	}
}

func TestBroker_SubscriberCount(t *testing.T) {
	b := sse.NewBroker(64)
	vaultID := uuid.New()

	assert.Equal(t, 0, b.SubscriberCount(vaultID))

	b.Subscribe(context.Background(), vaultID, "client-1")
	assert.Equal(t, 1, b.SubscriberCount(vaultID))

	b.Subscribe(context.Background(), vaultID, "client-2")
	assert.Equal(t, 2, b.SubscriberCount(vaultID))

	b.Unsubscribe(vaultID, "client-1")
	assert.Equal(t, 1, b.SubscriberCount(vaultID))

	b.Unsubscribe(vaultID, "client-2")
	assert.Equal(t, 0, b.SubscriberCount(vaultID))
}

func TestBroker_TotalSubscribers(t *testing.T) {
	b := sse.NewBroker(64)

	assert.Equal(t, 0, b.TotalSubscribers())

	b.Subscribe(context.Background(), uuid.New(), "client-1")
	b.Subscribe(context.Background(), uuid.New(), "client-2")
	b.Subscribe(context.Background(), uuid.New(), "client-3")

	assert.Equal(t, 3, b.TotalSubscribers())
}

func TestBroker_ConcurrentPublishSubscribe(t *testing.T) {
	b := sse.NewBroker(256)
	vaultID := uuid.New()

	var wg sync.WaitGroup
	subscriberCount := 10
	eventCount := 50

	channels := make([]<-chan port.SSEEvent, subscriberCount)
	for i := 0; i < subscriberCount; i++ {
		ch, err := b.Subscribe(context.Background(), vaultID, uuid.New().String())
		require.NoError(t, err)
		channels[i] = ch
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < eventCount; i++ {
			b.Publish(vaultID, port.SSEEvent{
				ID:        uuid.New().String(),
				EventType: "file_updated",
				Data:      "concurrent-event",
			})
		}
	}()

	received := make([]int, subscriberCount)
	for i, ch := range channels {
		wg.Add(1)
		go func(idx int, c <-chan port.SSEEvent) {
			defer wg.Done()
			timeout := time.After(2 * time.Second)
			for {
				select {
				case _, ok := <-c:
					if !ok {
						return
					}
					received[idx]++
				case <-timeout:
					return
				}
			}
		}(i, ch)
	}

	wg.Wait()

	for i, count := range received {
		assert.Equal(t, eventCount, count, "subscriber %d should receive all events", i)
	}
}

func TestBroker_CleanupEmptyVault(t *testing.T) {
	b := sse.NewBroker(64)
	vaultID := uuid.New()

	b.Subscribe(context.Background(), vaultID, "client-1")
	assert.Equal(t, 1, b.SubscriberCount(vaultID))
	assert.Equal(t, 1, b.TotalSubscribers())

	b.Unsubscribe(vaultID, "client-1")
	assert.Equal(t, 0, b.SubscriberCount(vaultID))
	assert.Equal(t, 0, b.TotalSubscribers())
}
