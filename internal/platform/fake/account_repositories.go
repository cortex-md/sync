package fake

import (
	"context"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type UserTokenRepository struct {
	mu     sync.Mutex
	tokens map[string]*domain.UserToken
}

func NewUserTokenRepository() *UserTokenRepository {
	return &UserTokenRepository{tokens: make(map[string]*domain.UserToken)}
}

func (r *UserTokenRepository) Create(_ context.Context, token *domain.UserToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *token
	r.tokens[token.TokenHash] = &stored
	return nil
}

func (r *UserTokenRepository) Consume(_ context.Context, kind domain.UserTokenKind, tokenHash string) (*domain.UserToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.tokens[tokenHash]
	if !ok || token.Kind != kind || token.UsedAt != nil || !time.Now().Before(token.ExpiresAt) {
		return nil, domain.ErrNotFound
	}
	now := time.Now()
	token.UsedAt = &now
	result := *token
	return &result, nil
}

func (r *UserTokenRepository) Peek(_ context.Context, kind domain.UserTokenKind, tokenHash string) (*domain.UserToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token, ok := r.tokens[tokenHash]
	if !ok || token.Kind != kind || token.UsedAt != nil || !time.Now().Before(token.ExpiresAt) {
		return nil, domain.ErrNotFound
	}
	result := *token
	return &result, nil
}

func (r *UserTokenRepository) DeleteByUserAndKind(_ context.Context, userID uuid.UUID, kind domain.UserTokenKind) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for hash, token := range r.tokens {
		if token.UserID == userID && token.Kind == kind {
			delete(r.tokens, hash)
		}
	}
	return nil
}

func (r *UserTokenRepository) DeleteExpired(_ context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	for hash, token := range r.tokens {
		if token.UsedAt != nil || time.Now().After(token.ExpiresAt) {
			delete(r.tokens, hash)
			count++
		}
	}
	return count, nil
}

type EmailOutboxRepository struct {
	mu     sync.Mutex
	emails []*domain.OutboxEmail
	sent   map[int64]bool
	nextID int64
}

func NewEmailOutboxRepository() *EmailOutboxRepository {
	return &EmailOutboxRepository{sent: make(map[int64]bool), nextID: 1}
}

func (r *EmailOutboxRepository) Enqueue(_ context.Context, email *domain.OutboxEmail) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	email.ID = r.nextID
	r.nextID++
	email.CreatedAt = time.Now()
	email.NextAttemptAt = email.CreatedAt
	stored := *email
	r.emails = append(r.emails, &stored)
	return nil
}

func (r *EmailOutboxRepository) ClaimDue(_ context.Context, limit int) ([]domain.OutboxEmail, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.OutboxEmail
	for _, e := range r.emails {
		if len(result) >= limit {
			break
		}
		if !r.sent[e.ID] && !e.NextAttemptAt.After(time.Now()) {
			result = append(result, *e)
		}
	}
	return result, nil
}

func (r *EmailOutboxRepository) MarkSent(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent[id] = true
	return nil
}

func (r *EmailOutboxRepository) MarkFailed(_ context.Context, id int64, _ string, nextAttemptAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.emails {
		if e.ID == id {
			e.Attempts++
			e.NextAttemptAt = nextAttemptAt
		}
	}
	return nil
}

func (r *EmailOutboxRepository) Pending() []domain.OutboxEmail {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.OutboxEmail
	for _, e := range r.emails {
		if !r.sent[e.ID] {
			result = append(result, *e)
		}
	}
	return result
}

type Locker struct{}

func NewLocker() *Locker {
	return &Locker{}
}

func (l *Locker) TryLock(context.Context, int64) (bool, error) {
	return true, nil
}
