package fake

import (
	"context"
	"sync"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type UserRepository struct {
	mu      sync.RWMutex
	users   map[uuid.UUID]*domain.User
	byEmail map[string]uuid.UUID
}

func NewUserRepository() *UserRepository {
	return &UserRepository{
		users:   make(map[uuid.UUID]*domain.User),
		byEmail: make(map[string]uuid.UUID),
	}
}

func (r *UserRepository) Create(_ context.Context, user *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byEmail[user.Email]; exists {
		return domain.ErrAlreadyExists
	}
	stored := *user
	r.users[user.ID] = &stored
	r.byEmail[user.Email] = user.ID
	return nil
}

func (r *UserRepository) GetByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	user, exists := r.users[id]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *user
	return &result, nil
}

func (r *UserRepository) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, exists := r.byEmail[email]
	if !exists {
		return nil, domain.ErrNotFound
	}
	user := r.users[id]
	result := *user
	return &result, nil
}

func (r *UserRepository) UpdatePublicKey(_ context.Context, id uuid.UUID, publicKey []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	user, exists := r.users[id]
	if !exists {
		return domain.ErrNotFound
	}
	user.PublicKey = publicKey
	return nil
}

func (r *UserRepository) Update(_ context.Context, user *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.users[user.ID]; !exists {
		return domain.ErrNotFound
	}
	stored := *user
	r.users[user.ID] = &stored
	return nil
}
