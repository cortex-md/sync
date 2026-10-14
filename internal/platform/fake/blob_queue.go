package fake

import (
	"context"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
)

type BlobQueue struct {
	mu        sync.Mutex
	pending   map[string]time.Time
	deletions []domain.BlobDeletion
	nextID    int64
}

func NewBlobQueue() *BlobQueue {
	return &BlobQueue{pending: make(map[string]time.Time), nextID: 1}
}

func (q *BlobQueue) RegisterPending(_ context.Context, key string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending[key] = time.Now()
	return nil
}

func (q *BlobQueue) ResolvePending(_ context.Context, key string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.pending, key)
	return nil
}

func (q *BlobQueue) ClaimStalePending(_ context.Context, before time.Time, limit int) ([]string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var keys []string
	for key, at := range q.pending {
		if len(keys) >= limit {
			break
		}
		if at.Before(before) {
			keys = append(keys, key)
			delete(q.pending, key)
		}
	}
	return keys, nil
}

func (q *BlobQueue) EnqueueDeletion(_ context.Context, keys ...string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, key := range keys {
		q.deletions = append(q.deletions, domain.BlobDeletion{ID: q.nextID, Key: key})
		q.nextID++
	}
	return nil
}

func (q *BlobQueue) ClaimDueDeletions(_ context.Context, limit int) ([]domain.BlobDeletion, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := min(limit, len(q.deletions))
	result := make([]domain.BlobDeletion, n)
	copy(result, q.deletions[:n])
	return result, nil
}

func (q *BlobQueue) CompleteDeletion(_ context.Context, id int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, d := range q.deletions {
		if d.ID == id {
			q.deletions = append(q.deletions[:i], q.deletions[i+1:]...)
			return nil
		}
	}
	return nil
}

func (q *BlobQueue) RetryDeletion(_ context.Context, id int64, _ string, _ time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.deletions {
		if q.deletions[i].ID == id {
			q.deletions[i].Attempts++
		}
	}
	return nil
}

func (q *BlobQueue) PendingDeletions() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.deletions)
}
