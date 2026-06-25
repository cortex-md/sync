package sse

import (
	"context"
	"sync"

	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
)

type client struct {
	ch chan port.SSEEvent
}

type Broker struct {
	mu      sync.RWMutex
	vaults  map[uuid.UUID]map[string]*client
	bufSize int
}

func NewBroker(bufSize int) *Broker {
	if bufSize <= 0 {
		bufSize = 64
	}
	return &Broker{
		vaults:  make(map[uuid.UUID]map[string]*client),
		bufSize: bufSize,
	}
}

func (b *Broker) Subscribe(_ context.Context, vaultID uuid.UUID, clientID string) (<-chan port.SSEEvent, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.vaults[vaultID]; !exists {
		b.vaults[vaultID] = make(map[string]*client)
	}

	if existing, exists := b.vaults[vaultID][clientID]; exists {
		close(existing.ch)
	}

	ch := make(chan port.SSEEvent, b.bufSize)
	b.vaults[vaultID][clientID] = &client{ch: ch}

	return ch, nil
}

func (b *Broker) Unsubscribe(vaultID uuid.UUID, clientID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	clients, exists := b.vaults[vaultID]
	if !exists {
		return
	}

	c, exists := clients[clientID]
	if !exists {
		return
	}

	close(c.ch)
	delete(clients, clientID)

	if len(clients) == 0 {
		delete(b.vaults, vaultID)
	}
}

func (b *Broker) Publish(vaultID uuid.UUID, event port.SSEEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	clients, exists := b.vaults[vaultID]
	if !exists {
		return
	}

	for _, c := range clients {
		select {
		case c.ch <- event:
		default:
		}
	}
}

func (b *Broker) SubscriberCount(vaultID uuid.UUID) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	clients, exists := b.vaults[vaultID]
	if !exists {
		return 0
	}
	return len(clients)
}

func (b *Broker) TotalSubscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	total := 0
	for _, clients := range b.vaults {
		total += len(clients)
	}
	return total
}
