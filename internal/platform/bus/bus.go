package bus

import (
	"context"
	"sync"

	"github.com/cortexnotes/cortex-sync/internal/platform/txhook"
	"github.com/google/uuid"
)

const (
	KindSSE                = "sse"
	KindAccessChanged      = "access"
	KindVaultAccessChanged = "vault_access"
	KindCollab             = "collab"
	KindTokensRevoked      = "tokens_revoked"
)

type Signal struct {
	Kind     string    `json:"k"`
	Origin   string    `json:"o,omitempty"`
	VaultID  uuid.UUID `json:"v"`
	UserID   uuid.UUID `json:"u,omitempty"`
	FilePath string    `json:"p,omitempty"`
	ClientID string    `json:"c,omitempty"`
	Event    string    `json:"e,omitempty"`
	Data     []byte    `json:"d,omitempty"`
}

type Bus interface {
	Publish(ctx context.Context, signal Signal) error
	Subscribe(kind string, handler func(Signal))
	InstanceID() string
}

type Handlers struct {
	mu       sync.RWMutex
	handlers map[string][]func(Signal)
}

func (h *Handlers) Subscribe(kind string, handler func(Signal)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.handlers == nil {
		h.handlers = make(map[string][]func(Signal))
	}
	h.handlers[kind] = append(h.handlers[kind], handler)
}

func (h *Handlers) Dispatch(signal Signal) {
	h.mu.RLock()
	handlers := h.handlers[signal.Kind]
	h.mu.RUnlock()
	for _, handler := range handlers {
		handler(signal)
	}
}

type Memory struct {
	Handlers
	id string
}

func NewMemory() *Memory {
	return &Memory{id: uuid.NewString()}
}

func (m *Memory) InstanceID() string {
	return m.id
}

func (m *Memory) Publish(ctx context.Context, signal Signal) error {
	signal.Origin = m.id
	txhook.AfterCommit(ctx, func() { m.Dispatch(signal) })
	return nil
}
