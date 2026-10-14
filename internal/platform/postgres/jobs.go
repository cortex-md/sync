package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/postgres/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EmailOutboxRepository struct{ store }

func NewEmailOutboxRepository(pool *pgxpool.Pool) *EmailOutboxRepository {
	return &EmailOutboxRepository{store{pool}}
}

func (r *EmailOutboxRepository) Enqueue(ctx context.Context, email *domain.OutboxEmail) error {
	data := email.Data
	if data == nil {
		data = map[string]string{}
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	row, err := r.q(ctx).EnqueueEmail(ctx, db.EnqueueEmailParams{ToAddress: email.To, Template: email.Template, Locale: email.Locale, Data: payload})
	if err != nil {
		return mapError(err)
	}
	email.ID, email.NextAttemptAt, email.CreatedAt = row.ID, row.NextAttemptAt, row.CreatedAt
	return nil
}

func (r *EmailOutboxRepository) ClaimDue(ctx context.Context, limit int) ([]domain.OutboxEmail, error) {
	rows, err := r.q(ctx).ClaimDueEmails(ctx, int32(limit))
	if err != nil {
		return nil, mapError(err)
	}
	emails := make([]domain.OutboxEmail, len(rows))
	for i, e := range rows {
		emails[i] = domain.OutboxEmail{
			ID: e.ID, To: e.ToAddress, Template: e.Template, Locale: e.Locale,
			Attempts: int(e.Attempts), NextAttemptAt: e.NextAttemptAt, CreatedAt: e.CreatedAt,
		}
		_ = json.Unmarshal(e.Data, &emails[i].Data)
	}
	return emails, nil
}

func (r *EmailOutboxRepository) MarkSent(ctx context.Context, id int64) error {
	return mapError(r.q(ctx).MarkEmailSent(ctx, id))
}

func (r *EmailOutboxRepository) MarkFailed(ctx context.Context, id int64, lastError string, nextAttemptAt time.Time) error {
	return mapError(r.q(ctx).MarkEmailFailed(ctx, db.MarkEmailFailedParams{ID: id, LastError: lastError, NextAttemptAt: nextAttemptAt}))
}

type Locker struct{ store }

func NewLocker(pool *pgxpool.Pool) *Locker {
	return &Locker{store{pool}}
}

func (l *Locker) TryLock(ctx context.Context, key int64) (bool, error) {
	acquired, err := l.q(ctx).TryAdvisoryLock(ctx, key)
	return acquired, mapError(err)
}

type BlobQueue struct{ store }

func NewBlobQueue(pool *pgxpool.Pool) *BlobQueue {
	return &BlobQueue{store{pool}}
}

func (b *BlobQueue) RegisterPending(ctx context.Context, key string) error {
	return mapError(b.q(ctx).RegisterPendingBlob(ctx, key))
}

func (b *BlobQueue) ResolvePending(ctx context.Context, key string) error {
	return mapError(b.q(ctx).ResolvePendingBlob(ctx, key))
}

func (b *BlobQueue) ClaimStalePending(ctx context.Context, before time.Time, limit int) ([]string, error) {
	keys, err := b.q(ctx).ClaimStalePendingBlobs(ctx, db.ClaimStalePendingBlobsParams{Before: before, RowLimit: int32(limit)})
	return keys, mapError(err)
}

func (b *BlobQueue) EnqueueDeletion(ctx context.Context, keys ...string) error {
	q := b.q(ctx)
	for _, key := range keys {
		if err := q.EnqueueBlobDeletion(ctx, key); err != nil {
			return mapError(err)
		}
	}
	return nil
}

func (b *BlobQueue) ClaimDueDeletions(ctx context.Context, limit int) ([]domain.BlobDeletion, error) {
	rows, err := b.q(ctx).ClaimDueBlobDeletions(ctx, int32(limit))
	if err != nil {
		return nil, mapError(err)
	}
	deletions := make([]domain.BlobDeletion, len(rows))
	for i, d := range rows {
		deletions[i] = domain.BlobDeletion{ID: d.ID, Key: d.BlobKey, Attempts: int(d.Attempts)}
	}
	return deletions, nil
}

func (b *BlobQueue) CompleteDeletion(ctx context.Context, id int64) error {
	return mapError(b.q(ctx).CompleteBlobDeletion(ctx, id))
}

func (b *BlobQueue) RetryDeletion(ctx context.Context, id int64, lastError string, next time.Time) error {
	return mapError(b.q(ctx).RetryBlobDeletion(ctx, db.RetryBlobDeletionParams{ID: id, LastError: lastError, NextAttemptAt: next}))
}
