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
	sent   map[int64]time.Time
	dead   map[int64]time.Time
	nextID int64
}

func NewEmailOutboxRepository() *EmailOutboxRepository {
	return &EmailOutboxRepository{sent: make(map[int64]time.Time), dead: make(map[int64]time.Time), nextID: 1}
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

func (r *EmailOutboxRepository) claimable(e *domain.OutboxEmail, now time.Time) bool {
	_, sent := r.sent[e.ID]
	_, dead := r.dead[e.ID]
	return !sent && !dead && !e.NextAttemptAt.After(now)
}

func (r *EmailOutboxRepository) Claim(_ context.Context, id int64, lease time.Duration) (*domain.OutboxEmail, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for _, e := range r.emails {
		if e.ID == id && r.claimable(e, now) {
			e.Attempts++
			e.NextAttemptAt = now.Add(lease)
			claimed := *e
			return &claimed, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *EmailOutboxRepository) ClaimDue(_ context.Context, limit int, lease time.Duration) ([]domain.OutboxEmail, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	var result []domain.OutboxEmail
	for _, e := range r.emails {
		if len(result) >= limit {
			break
		}
		if r.claimable(e, now) {
			e.Attempts++
			e.NextAttemptAt = now.Add(lease)
			result = append(result, *e)
		}
	}
	return result, nil
}

func (r *EmailOutboxRepository) MarkSent(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent[id] = time.Now()
	return nil
}

func (r *EmailOutboxRepository) MarkRetry(_ context.Context, id int64, _ string, nextAttemptAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.emails {
		if e.ID == id {
			e.NextAttemptAt = nextAttemptAt
		}
	}
	return nil
}

func (r *EmailOutboxRepository) MarkDead(_ context.Context, id int64, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dead[id] = time.Now()
	return nil
}

func (r *EmailOutboxRepository) DeleteFinished(_ context.Context, sentBefore, deadBefore time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	kept := r.emails[:0]
	for _, e := range r.emails {
		sentAt, sent := r.sent[e.ID]
		deadAt, dead := r.dead[e.ID]
		if (sent && sentAt.Before(sentBefore)) || (dead && deadAt.Before(deadBefore)) {
			delete(r.sent, e.ID)
			delete(r.dead, e.ID)
			count++
			continue
		}
		kept = append(kept, e)
	}
	r.emails = kept
	return count, nil
}

func (r *EmailOutboxRepository) Pending() []domain.OutboxEmail {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []domain.OutboxEmail
	for _, e := range r.emails {
		_, sent := r.sent[e.ID]
		_, dead := r.dead[e.ID]
		if !sent && !dead {
			result = append(result, *e)
		}
	}
	return result
}

func (r *EmailOutboxRepository) Sent() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]int64, 0, len(r.sent))
	for id := range r.sent {
		ids = append(ids, id)
	}
	return ids
}

type Locker struct{}

func NewLocker() *Locker {
	return &Locker{}
}

func (l *Locker) TryLock(context.Context, int64) (bool, error) {
	return true, nil
}
