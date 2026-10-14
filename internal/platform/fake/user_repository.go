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

func (r *UserRepository) LockRegistrations(_ context.Context) error {
	return nil
}

func (r *UserRepository) Count(_ context.Context) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return int64(len(r.users)), nil
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
	existing, exists := r.users[user.ID]
	if !exists {
		return domain.ErrNotFound
	}
	if owner, taken := r.byEmail[user.Email]; taken && owner != user.ID {
		return domain.ErrAlreadyExists
	}
	delete(r.byEmail, existing.Email)
	stored := *user
	r.users[user.ID] = &stored
	r.byEmail[user.Email] = user.ID
	return nil
}

func (r *UserRepository) ListByIDs(_ context.Context, ids []uuid.UUID) ([]domain.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]domain.User, 0, len(ids))
	for _, id := range ids {
		if user, ok := r.users[id]; ok {
			result = append(result, *user)
		}
	}
	return result, nil
}
