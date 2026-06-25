package fake

import (
	"context"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type RefreshTokenRepository struct {
	mu     sync.RWMutex
	tokens map[uuid.UUID]*domain.RefreshToken
	byHash map[string]uuid.UUID
}

func NewRefreshTokenRepository() *RefreshTokenRepository {
	return &RefreshTokenRepository{
		tokens: make(map[uuid.UUID]*domain.RefreshToken),
		byHash: make(map[string]uuid.UUID),
	}
}

func (r *RefreshTokenRepository) Create(_ context.Context, token *domain.RefreshToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *token
	r.tokens[token.ID] = &stored
	r.byHash[token.TokenHash] = token.ID
	return nil
}

func (r *RefreshTokenRepository) GetByTokenHash(_ context.Context, tokenHash string) (*domain.RefreshToken, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, exists := r.byHash[tokenHash]
	if !exists {
		return nil, domain.ErrNotFound
	}
	token := r.tokens[id]
	result := *token
	return &result, nil
}

func (r *RefreshTokenRepository) RevokeByID(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, exists := r.tokens[id]
	if !exists {
		return domain.ErrNotFound
	}
	token.Revoked = true
	return nil
}

func (r *RefreshTokenRepository) RevokeByFamilyID(_ context.Context, familyID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, token := range r.tokens {
		if token.FamilyID == familyID {
			token.Revoked = true
		}
	}
	return nil
}

func (r *RefreshTokenRepository) RevokeAllByUserID(_ context.Context, userID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, token := range r.tokens {
		if token.UserID == userID {
			token.Revoked = true
		}
	}
	return nil
}

func (r *RefreshTokenRepository) RevokeAllByDeviceID(_ context.Context, deviceID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, token := range r.tokens {
		if token.DeviceID == deviceID {
			token.Revoked = true
		}
	}
	return nil
}

func (r *RefreshTokenRepository) DeleteExpired(_ context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	now := time.Now()
	for id, token := range r.tokens {
		if token.ExpiresAt.Before(now) {
			delete(r.byHash, token.TokenHash)
			delete(r.tokens, id)
			count++
		}
	}
	return count, nil
}
