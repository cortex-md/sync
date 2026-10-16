package realtime

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type Event struct {
	ID        string
	EventType string
	Data      string
	target    *eventTarget
}

type eventTarget struct {
	FilePath string `json:"file_path"`
	OldPath  string `json:"old_path"`
	UserID   string `json:"user_id"`
	valid    bool
}

func (e *Event) resolveTarget() *eventTarget {
	if e.target == nil {
		target := &eventTarget{}
		target.valid = json.Unmarshal([]byte(e.Data), target) == nil
		e.target = target
	}
	return e.target
}

func (b *EventBroker) PublishSyncEvent(event *domain.SyncEvent) {
	data, err := event.PayloadJSON()
	if err != nil {
		return
	}
	b.Publish(event.VaultID, Event{
		ID:        strconv.FormatInt(event.ID, 10),
		EventType: string(event.EventType),
		Data:      string(data),
	})
}

type client struct {
	ch chan Event
}

type EventBroker struct {
	mu      sync.RWMutex
	vaults  map[uuid.UUID]map[string]*client
	bufSize int
}

func NewEventBroker(bufSize int) *EventBroker {
	if bufSize <= 0 {
		bufSize = 64
	}
	return &EventBroker{
		vaults:  make(map[uuid.UUID]map[string]*client),
		bufSize: bufSize,
	}
}

func (b *EventBroker) Subscribe(_ context.Context, vaultID uuid.UUID, clientID string) (<-chan Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.vaults[vaultID]; !exists {
		b.vaults[vaultID] = make(map[string]*client)
	}

	if existing, exists := b.vaults[vaultID][clientID]; exists {
		close(existing.ch)
	}

	ch := make(chan Event, b.bufSize)
	b.vaults[vaultID][clientID] = &client{ch: ch}

	return ch, nil
}

func (b *EventBroker) Unsubscribe(vaultID uuid.UUID, clientID string) {
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

func (b *EventBroker) Publish(vaultID uuid.UUID, event Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	clients, exists := b.vaults[vaultID]
	if !exists {
		return
	}

	event.resolveTarget()
	for clientID, c := range clients {
		select {
		case c.ch <- event:
		default:
			close(c.ch)
			delete(clients, clientID)
		}
	}

	if len(clients) == 0 {
		delete(b.vaults, vaultID)
	}
}

func (b *EventBroker) Vaults() []uuid.UUID {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ids := make([]uuid.UUID, 0, len(b.vaults))
	for id := range b.vaults {
		ids = append(ids, id)
	}
	return ids
}

func (b *EventBroker) SubscriberCount(vaultID uuid.UUID) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	clients, exists := b.vaults[vaultID]
	if !exists {
		return 0
	}
	return len(clients)
}

func (b *EventBroker) TotalSubscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	total := 0
	for _, clients := range b.vaults {
		total += len(clients)
	}
	return total
}

func (b *EventBroker) DisconnectUser(vaultID uuid.UUID, userID uuid.UUID) {
	b.mu.Lock()
	defer b.mu.Unlock()

	clients, exists := b.vaults[vaultID]
	if !exists {
		return
	}

	prefix := userID.String() + ":"
	for clientID, c := range clients {
		if strings.HasPrefix(clientID, prefix) {
			close(c.ch)
			delete(clients, clientID)
		}
	}
	if len(clients) == 0 {
		delete(b.vaults, vaultID)
	}
}
