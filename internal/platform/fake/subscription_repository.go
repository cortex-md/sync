package fake

import (
	"context"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type SubscriptionRepository struct {
	mu              sync.RWMutex
	subs            map[uuid.UUID]*domain.Subscription
	byUserID        map[uuid.UUID]uuid.UUID
	byExternalSubID map[string]uuid.UUID
	byCheckoutID    map[string]uuid.UUID
	byCustomerID    map[string]uuid.UUID
	webhookEvents   map[string]*domain.SubscriptionWebhookEvent
}

func NewSubscriptionRepository() *SubscriptionRepository {
	return &SubscriptionRepository{
		subs:            make(map[uuid.UUID]*domain.Subscription),
		byUserID:        make(map[uuid.UUID]uuid.UUID),
		byExternalSubID: make(map[string]uuid.UUID),
		byCheckoutID:    make(map[string]uuid.UUID),
		byCustomerID:    make(map[string]uuid.UUID),
		webhookEvents:   make(map[string]*domain.SubscriptionWebhookEvent),
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
	if sub.ExternalCheckoutID != "" {
		r.byCheckoutID[sub.ExternalCheckoutID] = sub.ID
	}
	if sub.ExternalCustomerID != "" {
		r.byCustomerID[sub.ExternalCustomerID] = sub.ID
	}
	return nil
}

func (r *SubscriptionRepository) GetByID(_ context.Context, id uuid.UUID) (*domain.Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sub, exists := r.subs[id]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *sub
	return &result, nil
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
	if existing.ExternalCheckoutID != "" {
		delete(r.byCheckoutID, existing.ExternalCheckoutID)
	}
	if existing.ExternalCustomerID != "" {
		delete(r.byCustomerID, existing.ExternalCustomerID)
	}
	stored := *sub
	r.subs[sub.ID] = &stored
	if sub.ExternalSubscriptionID != "" {
		r.byExternalSubID[sub.ExternalSubscriptionID] = sub.ID
	}
	if sub.ExternalCheckoutID != "" {
		r.byCheckoutID[sub.ExternalCheckoutID] = sub.ID
	}
	if sub.ExternalCustomerID != "" {
		r.byCustomerID[sub.ExternalCustomerID] = sub.ID
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

func (r *SubscriptionRepository) GetByExternalCheckoutID(_ context.Context, externalID string) (*domain.Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, exists := r.byCheckoutID[externalID]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *r.subs[id]
	return &result, nil
}

func (r *SubscriptionRepository) GetByExternalCustomerID(_ context.Context, externalID string) (*domain.Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, exists := r.byCustomerID[externalID]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *r.subs[id]
	return &result, nil
}

func (r *SubscriptionRepository) CreateWebhookEvent(_ context.Context, event *domain.SubscriptionWebhookEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.webhookEvents[event.ID]; exists {
		return domain.ErrAlreadyExists
	}
	stored := *event
	r.webhookEvents[event.ID] = &stored
	return nil
}

func (r *SubscriptionRepository) MarkWebhookEventProcessed(_ context.Context, id string, processedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	event, exists := r.webhookEvents[id]
	if !exists {
		return domain.ErrNotFound
	}
	event.ProcessedAt = processedAt
	return nil
}

func (r *SubscriptionRepository) DeleteWebhookEvent(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.webhookEvents, id)
	return nil
}
