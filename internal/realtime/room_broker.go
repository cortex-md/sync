package realtime

import (
	"sync"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type peer struct {
	id string
	ch chan CollabMessage
}

type room struct {
	peers       map[string]*peer
	updateBuf   [][]byte
	bufBytes    int
	maxPeers    int
	maxBufBytes int
	flushing    bool
}

type RoomBroker struct {
	mu          sync.RWMutex
	rooms       map[string]*room
	maxPeers    int
	maxBufBytes int
}

func NewRoomBroker(maxPeersPerRoom int, maxBufBytes int) *RoomBroker {
	if maxPeersPerRoom <= 0 {
		maxPeersPerRoom = 10
	}
	if maxBufBytes <= 0 {
		maxBufBytes = 4 * 1024 * 1024
	}
	return &RoomBroker{
		rooms:       make(map[string]*room),
		maxPeers:    maxPeersPerRoom,
		maxBufBytes: maxBufBytes,
	}
}

func roomKey(vaultID uuid.UUID, filePath string) string {
	return vaultID.String() + ":" + filePath
}

func (b *RoomBroker) Join(vaultID uuid.UUID, filePath string, clientID string, ch chan CollabMessage) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := roomKey(vaultID, filePath)
	r, exists := b.rooms[key]
	if !exists {
		r = &room{
			peers:       make(map[string]*peer),
			maxPeers:    b.maxPeers,
			maxBufBytes: b.maxBufBytes,
		}
		b.rooms[key] = r
	}

	if existing, alreadyJoined := r.peers[clientID]; alreadyJoined {
		if existing.ch != ch {
			close(existing.ch)
		}
	} else if len(r.peers) >= r.maxPeers {
		return 0, domain.ErrCollabRoomFull
	}

	r.peers[clientID] = &peer{
		id: clientID,
		ch: ch,
	}
	return len(r.peers), nil
}

func (b *RoomBroker) Leave(vaultID uuid.UUID, filePath string, clientID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := roomKey(vaultID, filePath)
	r, exists := b.rooms[key]
	if !exists {
		return
	}

	if p, ok := r.peers[clientID]; ok {
		close(p.ch)
		delete(r.peers, clientID)
	}
	if len(r.peers) == 0 && len(r.updateBuf) == 0 && !r.flushing {
		delete(b.rooms, key)
	}
}

func (b *RoomBroker) BroadcastFrame(vaultID uuid.UUID, filePath string, senderID string, frame *Outbound) {
	b.deliver(vaultID, filePath, CollabMessage{ClientID: senderID, Frame: frame})
}

func (b *RoomBroker) deliver(vaultID uuid.UUID, filePath string, msg CollabMessage) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := roomKey(vaultID, filePath)
	r, exists := b.rooms[key]
	if !exists {
		return
	}
	senderID := msg.ClientID

	for id, p := range r.peers {
		if id == senderID {
			continue
		}
		select {
		case p.ch <- msg:
		default:
			close(p.ch)
			delete(r.peers, id)
		}
	}
}

func (b *RoomBroker) PendingUpdates(vaultID uuid.UUID, filePath string) [][]byte {
	b.mu.RLock()
	defer b.mu.RUnlock()

	r, exists := b.rooms[roomKey(vaultID, filePath)]
	if !exists || len(r.updateBuf) == 0 {
		return nil
	}
	pending := make([][]byte, len(r.updateBuf))
	copy(pending, r.updateBuf)
	return pending
}

func (b *RoomBroker) HasRoom(vaultID uuid.UUID, filePath string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	r, exists := b.rooms[roomKey(vaultID, filePath)]
	return exists && len(r.peers) > 0
}

func (b *RoomBroker) BufferUpdate(vaultID uuid.UUID, filePath string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := roomKey(vaultID, filePath)
	r, exists := b.rooms[key]
	if !exists {
		return domain.ErrCollabNotActive
	}

	if r.bufBytes+len(data) > r.maxBufBytes {
		return domain.ErrCollabBufferFull
	}

	cp := make([]byte, len(data))
	copy(cp, data)
	r.updateBuf = append(r.updateBuf, cp)
	r.bufBytes += len(data)
	return nil
}

func (b *RoomBroker) BeginFlush(vaultID uuid.UUID, filePath string) [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := roomKey(vaultID, filePath)
	r, exists := b.rooms[key]
	if !exists || r.flushing || len(r.updateBuf) == 0 {
		return nil
	}

	r.flushing = true
	flushed := make([][]byte, len(r.updateBuf))
	copy(flushed, r.updateBuf)
	return flushed
}

func (b *RoomBroker) FinishFlush(vaultID uuid.UUID, filePath string, flushedCount int, succeeded bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := roomKey(vaultID, filePath)
	r, exists := b.rooms[key]
	if !exists {
		return
	}

	if succeeded {
		if flushedCount > len(r.updateBuf) {
			flushedCount = len(r.updateBuf)
		}
		for _, update := range r.updateBuf[:flushedCount] {
			r.bufBytes -= len(update)
		}
		r.updateBuf = r.updateBuf[flushedCount:]
	}
	r.flushing = false
	if len(r.peers) == 0 && len(r.updateBuf) == 0 {
		delete(b.rooms, key)
	}
}

func (b *RoomBroker) FlushUpdates(vaultID uuid.UUID, filePath string) [][]byte {
	flushed := b.BeginFlush(vaultID, filePath)
	b.FinishFlush(vaultID, filePath, len(flushed), true)
	return flushed
}

func (b *RoomBroker) PeerCount(vaultID uuid.UUID, filePath string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	key := roomKey(vaultID, filePath)
	r, exists := b.rooms[key]
	if !exists {
		return 0
	}
	return len(r.peers)
}

func (b *RoomBroker) PeerIDs(vaultID uuid.UUID, filePath string) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	key := roomKey(vaultID, filePath)
	r, exists := b.rooms[key]
	if !exists {
		return nil
	}

	ids := make([]string, 0, len(r.peers))
	for id := range r.peers {
		ids = append(ids, id)
	}
	return ids
}
