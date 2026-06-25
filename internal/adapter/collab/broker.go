package collab

import (
	"sync"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

type peer struct {
	id string
	ch chan<- port.CollabMessage
}

type room struct {
	peers       map[string]*peer
	updateBuf   [][]byte
	bufBytes    int
	maxPeers    int
	maxBufBytes int
	flushing    bool
}

type Broker struct {
	mu          sync.RWMutex
	rooms       map[string]*room
	maxPeers    int
	maxBufBytes int
}

func NewBroker(maxPeersPerRoom int, maxBufBytes int) *Broker {
	if maxPeersPerRoom <= 0 {
		maxPeersPerRoom = 10
	}
	if maxBufBytes <= 0 {
		maxBufBytes = 4 * 1024 * 1024
	}
	return &Broker{
		rooms:       make(map[string]*room),
		maxPeers:    maxPeersPerRoom,
		maxBufBytes: maxBufBytes,
	}
}

func roomKey(vaultID uuid.UUID, filePath string) string {
	return vaultID.String() + ":" + filePath
}

func (b *Broker) Join(vaultID uuid.UUID, filePath string, clientID string, ch chan<- port.CollabMessage) (int, error) {
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

	if _, alreadyJoined := r.peers[clientID]; !alreadyJoined {
		if len(r.peers) >= r.maxPeers {
			return 0, domain.ErrCollabRoomFull
		}
	}

	r.peers[clientID] = &peer{
		id: clientID,
		ch: ch,
	}
	return len(r.peers), nil
}

func (b *Broker) Leave(vaultID uuid.UUID, filePath string, clientID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := roomKey(vaultID, filePath)
	r, exists := b.rooms[key]
	if !exists {
		return
	}

	delete(r.peers, clientID)
	if len(r.peers) == 0 && len(r.updateBuf) == 0 && !r.flushing {
		delete(b.rooms, key)
	}
}

func (b *Broker) Broadcast(vaultID uuid.UUID, filePath string, senderID string, data []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	key := roomKey(vaultID, filePath)
	r, exists := b.rooms[key]
	if !exists {
		return
	}

	msg := port.CollabMessage{
		ClientID: senderID,
		Data:     data,
	}

	for _, p := range r.peers {
		if p.id != senderID {
			select {
			case p.ch <- msg:
			default:
			}
		}
	}
}

func (b *Broker) BufferUpdate(vaultID uuid.UUID, filePath string, data []byte) error {
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

func (b *Broker) BeginFlush(vaultID uuid.UUID, filePath string) [][]byte {
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

func (b *Broker) FinishFlush(vaultID uuid.UUID, filePath string, flushedCount int, succeeded bool) {
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

func (b *Broker) FlushUpdates(vaultID uuid.UUID, filePath string) [][]byte {
	flushed := b.BeginFlush(vaultID, filePath)
	b.FinishFlush(vaultID, filePath, len(flushed), true)
	return flushed
}

func (b *Broker) PeerCount(vaultID uuid.UUID, filePath string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	key := roomKey(vaultID, filePath)
	r, exists := b.rooms[key]
	if !exists {
		return 0
	}
	return len(r.peers)
}

func (b *Broker) PeerIDs(vaultID uuid.UUID, filePath string) []string {
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
