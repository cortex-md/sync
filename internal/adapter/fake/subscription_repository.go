package fake

import (
	"context"
	"sync"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type SubscriptionRepository struct {
	mu             sync.RWMutex
	subs           map[uuid.UUID]*domain.Subscription
	byUserID       map[uuid.UUID]uuid.UUID
	byExternalSubID map[string]uuid.UUID
}

func NewSubscriptionRepository() *SubscriptionRepository {
	return &SubscriptionRepository{
		subs:            make(map[uuid.UUID]*domain.Subscription),
		byUserID:        make(map[uuid.UUID]uuid.UUID),
		byExternalSubID: make(map[string]uuid.UUID),
	}
}

func (r *SubscriptionRepository) Create(_ context.Context, sub *domain.Subscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byUserID[sub.UserID]; exists {
		return domain.ErrAlreadyExists
	}
	stored := *sub
	r.subs[sub.ID] = &stored
	r.byUserID[sub.UserID] = sub.ID
	if sub.ExternalSubscriptionID != "" {
		r.byExternalSubID[sub.ExternalSubscriptionID] = sub.ID
	}
	return nil
}

func (r *SubscriptionRepository) GetByUserID(_ context.Context, userID uuid.UUID) (*domain.Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, exists := r.byUserID[userID]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *r.subs[id]
	return &result, nil
}

func (r *SubscriptionRepository) Update(_ context.Context, sub *domain.Subscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, exists := r.subs[sub.ID]
	if !exists {
		return domain.ErrNotFound
	}
	if existing.ExternalSubscriptionID != "" {
		delete(r.byExternalSubID, existing.ExternalSubscriptionID)
	}
	stored := *sub
	r.subs[sub.ID] = &stored
	if sub.ExternalSubscriptionID != "" {
		r.byExternalSubID[sub.ExternalSubscriptionID] = sub.ID
	}
	return nil
}

func (r *SubscriptionRepository) GetByExternalSubscriptionID(_ context.Context, externalID string) (*domain.Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, exists := r.byExternalSubID[externalID]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *r.subs[id]
	return &result, nil
}
