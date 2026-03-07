package collab_test

import (
	"sync"
	"testing"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/adapter/collab"
	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroker_JoinAndBroadcast(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch := make(chan port.CollabMessage, 10)
	_, err := b.Join(vaultID, filePath, "client-1", ch)
	require.NoError(t, err)

	b.Broadcast(vaultID, filePath, "client-2", []byte{0x01, 0x02})

	select {
	case msg := <-ch:
		assert.Equal(t, "client-2", msg.ClientID)
		assert.Equal(t, []byte{0x01, 0x02}, msg.Data)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast")
	}
}

func TestBroker_BroadcastDoesNotEchoToSender(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch := make(chan port.CollabMessage, 10)
	_, err := b.Join(vaultID, filePath, "sender", ch)
	require.NoError(t, err)

	b.Broadcast(vaultID, filePath, "sender", []byte{0x01})

	select {
	case <-ch:
		t.Fatal("sender should not receive their own broadcast")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBroker_MultiplePeersInRoom(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "shared.md"

	ch1 := make(chan port.CollabMessage, 10)
	ch2 := make(chan port.CollabMessage, 10)

	_, err := b.Join(vaultID, filePath, "peer-1", ch1)
	require.NoError(t, err)
	_, err = b.Join(vaultID, filePath, "peer-2", ch2)
	require.NoError(t, err)

	b.Broadcast(vaultID, filePath, "peer-1", []byte("hello"))

	select {
	case msg := <-ch2:
		assert.Equal(t, "peer-1", msg.ClientID)
		assert.Equal(t, []byte("hello"), msg.Data)
	case <-time.After(time.Second):
		t.Fatal("peer-2 should receive broadcast")
	}

	select {
	case <-ch1:
		t.Fatal("peer-1 should not receive own message")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBroker_IsolatedRooms(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()

	chA := make(chan port.CollabMessage, 10)
	chB := make(chan port.CollabMessage, 10)

	_, err := b.Join(vaultID, "a.md", "client-1", chA)
	require.NoError(t, err)
	_, err = b.Join(vaultID, "b.md", "client-2", chB)
	require.NoError(t, err)

	b.Broadcast(vaultID, "a.md", "sender", []byte("msg-for-a"))

	select {
	case msg := <-chA:
		assert.Equal(t, []byte("msg-for-a"), msg.Data)
	case <-time.After(time.Second):
		t.Fatal("room A should receive message")
	}

	select {
	case <-chB:
		t.Fatal("room B should not receive room A message")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBroker_IsolatedVaults(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vault1 := uuid.New()
	vault2 := uuid.New()

	ch1 := make(chan port.CollabMessage, 10)
	ch2 := make(chan port.CollabMessage, 10)

	_, err := b.Join(vault1, "doc.md", "client-1", ch1)
	require.NoError(t, err)
	_, err = b.Join(vault2, "doc.md", "client-2", ch2)
	require.NoError(t, err)

	b.Broadcast(vault1, "doc.md", "sender", []byte("vault1-msg"))

	select {
	case <-ch1:
	case <-time.After(time.Second):
		t.Fatal("vault1 should receive message")
	}

	select {
	case <-ch2:
		t.Fatal("vault2 should not receive vault1 message")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBroker_Leave(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch := make(chan port.CollabMessage, 10)
	_, err := b.Join(vaultID, filePath, "client-1", ch)
	require.NoError(t, err)
	assert.Equal(t, 1, b.PeerCount(vaultID, filePath))

	b.Leave(vaultID, filePath, "client-1")
	assert.Equal(t, 0, b.PeerCount(vaultID, filePath))
}

func TestBroker_LeaveNonexistent(t *testing.T) {
	b := collab.NewBroker(10, 0)
	b.Leave(uuid.New(), "ghost.md", "nonexistent")
}

func TestBroker_MaxPeersPerRoom(t *testing.T) {
	b := collab.NewBroker(2, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch1 := make(chan port.CollabMessage, 10)
	ch2 := make(chan port.CollabMessage, 10)
	ch3 := make(chan port.CollabMessage, 10)

	_, err := b.Join(vaultID, filePath, "peer-1", ch1)
	require.NoError(t, err)
	_, err = b.Join(vaultID, filePath, "peer-2", ch2)
	require.NoError(t, err)

	_, err = b.Join(vaultID, filePath, "peer-3", ch3)
	assert.ErrorIs(t, err, domain.ErrCollabRoomFull)
}

func TestBroker_RejoinSameClient(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch1 := make(chan port.CollabMessage, 10)
	ch2 := make(chan port.CollabMessage, 10)

	_, err := b.Join(vaultID, filePath, "client-1", ch1)
	require.NoError(t, err)
	_, err = b.Join(vaultID, filePath, "client-1", ch2)
	require.NoError(t, err)

	assert.Equal(t, 1, b.PeerCount(vaultID, filePath))

	b.Broadcast(vaultID, filePath, "sender", []byte("test"))

	select {
	case msg := <-ch2:
		assert.Equal(t, []byte("test"), msg.Data)
	case <-time.After(time.Second):
		t.Fatal("new channel should receive messages")
	}
}

func TestBroker_Join_ReturnsNewCount(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch1 := make(chan port.CollabMessage, 10)
	ch2 := make(chan port.CollabMessage, 10)

	count, err := b.Join(vaultID, filePath, "p1", ch1)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	count, err = b.Join(vaultID, filePath, "p2", ch2)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestBroker_PeerCount(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	assert.Equal(t, 0, b.PeerCount(vaultID, filePath))

	ch1 := make(chan port.CollabMessage, 10)
	ch2 := make(chan port.CollabMessage, 10)
	ch3 := make(chan port.CollabMessage, 10)

	b.Join(vaultID, filePath, "p1", ch1) //nolint:errcheck
	assert.Equal(t, 1, b.PeerCount(vaultID, filePath))

	b.Join(vaultID, filePath, "p2", ch2) //nolint:errcheck
	assert.Equal(t, 2, b.PeerCount(vaultID, filePath))

	b.Join(vaultID, filePath, "p3", ch3) //nolint:errcheck
	assert.Equal(t, 3, b.PeerCount(vaultID, filePath))

	b.Leave(vaultID, filePath, "p2")
	assert.Equal(t, 2, b.PeerCount(vaultID, filePath))
}

func TestBroker_BroadcastToEmptyRoom(t *testing.T) {
	b := collab.NewBroker(10, 0)
	b.Broadcast(uuid.New(), "empty.md", "sender", []byte("test"))
}

func TestBroker_DropsMessageOnFullBuffer(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch := make(chan port.CollabMessage, 2)
	_, err := b.Join(vaultID, filePath, "slow-client", ch)
	require.NoError(t, err)

	b.Broadcast(vaultID, filePath, "sender", []byte("msg1"))
	b.Broadcast(vaultID, filePath, "sender", []byte("msg2"))
	b.Broadcast(vaultID, filePath, "sender", []byte("msg3"))

	received := 0
	for {
		select {
		case <-ch:
			received++
		case <-time.After(100 * time.Millisecond):
			assert.Equal(t, 2, received, "buffer size is 2, third message should be dropped")
			return
		}
	}
}

func TestBroker_ConcurrentJoinLeaveBroadcast(t *testing.T) {
	b := collab.NewBroker(50, 0)
	vaultID := uuid.New()
	filePath := "concurrent.md"

	var wg sync.WaitGroup
	clientCount := 20
	messageCount := 30

	for i := 0; i < clientCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			clientID := uuid.New().String()
			ch := make(chan port.CollabMessage, 100)
			if _, err := b.Join(vaultID, filePath, clientID, ch); err != nil {
				return
			}
			defer b.Leave(vaultID, filePath, clientID)

			go func() {
				for range ch {
				}
			}()

			for j := 0; j < messageCount; j++ {
				b.Broadcast(vaultID, filePath, clientID, []byte{byte(j)})
			}
		}(i)
	}

	wg.Wait()
	assert.Equal(t, 0, b.PeerCount(vaultID, filePath))
}

func TestBroker_DefaultMaxPeers(t *testing.T) {
	b := collab.NewBroker(0, 0)
	vaultID := uuid.New()

	for i := 0; i < 10; i++ {
		ch := make(chan port.CollabMessage, 1)
		_, err := b.Join(vaultID, "doc.md", uuid.New().String(), ch)
		require.NoError(t, err)
	}

	ch := make(chan port.CollabMessage, 1)
	_, err := b.Join(vaultID, "doc.md", "extra", ch)
	assert.ErrorIs(t, err, domain.ErrCollabRoomFull)
}

func TestBroker_RoomCleanupOnLastLeave(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "cleanup.md"

	ch1 := make(chan port.CollabMessage, 10)
	ch2 := make(chan port.CollabMessage, 10)

	b.Join(vaultID, filePath, "p1", ch1) //nolint:errcheck
	b.Join(vaultID, filePath, "p2", ch2) //nolint:errcheck
	assert.Equal(t, 2, b.PeerCount(vaultID, filePath))

	b.Leave(vaultID, filePath, "p1")
	assert.Equal(t, 1, b.PeerCount(vaultID, filePath))

	b.Leave(vaultID, filePath, "p2")
	assert.Equal(t, 0, b.PeerCount(vaultID, filePath))
}

func TestBroker_BufferUpdate_AccumulatesData(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch := make(chan port.CollabMessage, 10)
	_, err := b.Join(vaultID, filePath, "p1", ch)
	require.NoError(t, err)

	b.BufferUpdate(vaultID, filePath, []byte{0x01})
	b.BufferUpdate(vaultID, filePath, []byte{0x02})
	b.BufferUpdate(vaultID, filePath, []byte{0x03})

	flushed := b.FlushUpdates(vaultID, filePath)
	require.Len(t, flushed, 3)
	assert.Equal(t, []byte{0x01}, flushed[0])
	assert.Equal(t, []byte{0x02}, flushed[1])
	assert.Equal(t, []byte{0x03}, flushed[2])
}

func TestBroker_FlushUpdates_ClearsBuffer(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch := make(chan port.CollabMessage, 10)
	_, err := b.Join(vaultID, filePath, "p1", ch)
	require.NoError(t, err)

	b.BufferUpdate(vaultID, filePath, []byte{0xAA})
	first := b.FlushUpdates(vaultID, filePath)
	assert.Len(t, first, 1)

	second := b.FlushUpdates(vaultID, filePath)
	assert.Empty(t, second)
}

func TestBroker_FlushUpdates_EmptyRoomReturnsNil(t *testing.T) {
	b := collab.NewBroker(10, 0)
	result := b.FlushUpdates(uuid.New(), "ghost.md")
	assert.Nil(t, result)
}

func TestBroker_BufferUpdate_NoOpForMissingRoom(t *testing.T) {
	b := collab.NewBroker(10, 0)
	b.BufferUpdate(uuid.New(), "ghost.md", []byte{0x01})
}

func TestBroker_BufferUpdate_CopiesData(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch := make(chan port.CollabMessage, 10)
	_, err := b.Join(vaultID, filePath, "p1", ch)
	require.NoError(t, err)

	original := []byte{0x01, 0x02}
	b.BufferUpdate(vaultID, filePath, original)
	original[0] = 0xFF

	flushed := b.FlushUpdates(vaultID, filePath)
	require.Len(t, flushed, 1)
	assert.Equal(t, byte(0x01), flushed[0][0], "buffer should hold a copy, not a reference")
}

func TestBroker_BufferUpdate_ConcurrentAccess(t *testing.T) {
	b := collab.NewBroker(100, 0)
	vaultID := uuid.New()
	filePath := "concurrent.md"

	ch := make(chan port.CollabMessage, 10)
	_, err := b.Join(vaultID, filePath, "p1", ch)
	require.NoError(t, err)

	var wg sync.WaitGroup
	n := 100
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			b.BufferUpdate(vaultID, filePath, []byte{byte(i)})
		}(i)
	}
	wg.Wait()

	all := b.FlushUpdates(vaultID, filePath)
	assert.Len(t, all, n)
}

func TestBroker_PeerIDs_ReturnsAllJoinedClients(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch1 := make(chan port.CollabMessage, 10)
	ch2 := make(chan port.CollabMessage, 10)
	_, err := b.Join(vaultID, filePath, "alpha", ch1)
	require.NoError(t, err)
	_, err = b.Join(vaultID, filePath, "beta", ch2)
	require.NoError(t, err)

	ids := b.PeerIDs(vaultID, filePath)
	assert.ElementsMatch(t, []string{"alpha", "beta"}, ids)
}

func TestBroker_PeerIDs_EmptyForMissingRoom(t *testing.T) {
	b := collab.NewBroker(10, 0)
	ids := b.PeerIDs(uuid.New(), "ghost.md")
	assert.Nil(t, ids)
}

func TestBroker_PeerIDs_UpdatedAfterLeave(t *testing.T) {
	b := collab.NewBroker(10, 0)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch1 := make(chan port.CollabMessage, 10)
	ch2 := make(chan port.CollabMessage, 10)
	_, err := b.Join(vaultID, filePath, "a", ch1)
	require.NoError(t, err)
	_, err = b.Join(vaultID, filePath, "b", ch2)
	require.NoError(t, err)

	b.Leave(vaultID, filePath, "a")

	ids := b.PeerIDs(vaultID, filePath)
	assert.Equal(t, []string{"b"}, ids)
}

func TestBroker_BufferUpdate_DropsWhenCapExceeded(t *testing.T) {
	b := collab.NewBroker(10, 10)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch := make(chan port.CollabMessage, 10)
	_, err := b.Join(vaultID, filePath, "p1", ch)
	require.NoError(t, err)

	b.BufferUpdate(vaultID, filePath, []byte("hello"))
	b.BufferUpdate(vaultID, filePath, []byte("world"))
	b.BufferUpdate(vaultID, filePath, []byte("overflow"))

	flushed := b.FlushUpdates(vaultID, filePath)
	totalBytes := 0
	for _, u := range flushed {
		totalBytes += len(u)
	}
	assert.LessOrEqual(t, totalBytes, 10, "total buffered bytes must not exceed cap")
}

func TestBroker_BufferUpdate_ResetsAfterFlush(t *testing.T) {
	b := collab.NewBroker(10, 5)
	vaultID := uuid.New()
	filePath := "doc.md"

	ch := make(chan port.CollabMessage, 10)
	_, err := b.Join(vaultID, filePath, "p1", ch)
	require.NoError(t, err)

	b.BufferUpdate(vaultID, filePath, []byte("hello"))
	b.FlushUpdates(vaultID, filePath)

	b.BufferUpdate(vaultID, filePath, []byte("world"))
	flushed := b.FlushUpdates(vaultID, filePath)
	require.Len(t, flushed, 1)
	assert.Equal(t, []byte("world"), flushed[0])
}
