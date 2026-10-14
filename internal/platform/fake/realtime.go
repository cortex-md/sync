package fake

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type CollabPresenceRepository struct {
	mu       sync.Mutex
	presence map[string]domain.CollabPresence
}

func NewCollabPresenceRepository() *CollabPresenceRepository {
	return &CollabPresenceRepository{presence: make(map[string]domain.CollabPresence)}
}

func (r *CollabPresenceRepository) Join(_ context.Context, p *domain.CollabPresence) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *p
	stored.JoinedAt = time.Now()
	stored.LastSeenAt = stored.JoinedAt
	r.presence[p.ClientID] = stored
	return nil
}

func (r *CollabPresenceRepository) Leave(_ context.Context, clientID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.presence, clientID)
	return nil
}

func (r *CollabPresenceRepository) Touch(_ context.Context, clientIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range clientIDs {
		if p, ok := r.presence[id]; ok {
			p.LastSeenAt = time.Now()
			r.presence[id] = p
		}
	}
	return nil
}

func (r *CollabPresenceRepository) ListRoom(_ context.Context, vaultID uuid.UUID, filePath string) ([]domain.CollabPresence, error) {
	return r.filter(func(p domain.CollabPresence) bool { return p.VaultID == vaultID && p.FilePath == filePath }), nil
}

func (r *CollabPresenceRepository) ListVault(_ context.Context, vaultID uuid.UUID) ([]domain.CollabPresence, error) {
	return r.filter(func(p domain.CollabPresence) bool { return p.VaultID == vaultID }), nil
}

func (r *CollabPresenceRepository) filter(match func(domain.CollabPresence) bool) []domain.CollabPresence {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.CollabPresence
	for _, p := range r.presence {
		if match(p) {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].JoinedAt.Before(result[j].JoinedAt) })
	return result
}

func (r *CollabPresenceRepository) DeleteStale(_ context.Context, before time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	for id, p := range r.presence {
		if p.LastSeenAt.Before(before) {
			delete(r.presence, id)
			count++
		}
	}
	return count, nil
}
