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

func (r *EmailOutboxRepository) Claim(ctx context.Context, id int64, lease time.Duration) (*domain.OutboxEmail, error) {
	row, err := r.q(ctx).ClaimEmail(ctx, db.ClaimEmailParams{ID: id, LeaseSeconds: int32(lease.Seconds())})
	if err != nil {
		return nil, mapError(err)
	}
	email := outboxEmail(row.ID, row.ToAddress, row.Template, row.Locale, row.Data, row.Attempts, row.NextAttemptAt, row.CreatedAt)
	return &email, nil
}

func (r *EmailOutboxRepository) ClaimDue(ctx context.Context, limit int, lease time.Duration) ([]domain.OutboxEmail, error) {
	rows, err := r.q(ctx).ClaimDueEmails(ctx, db.ClaimDueEmailsParams{RowLimit: int32(limit), LeaseSeconds: int32(lease.Seconds())})
	if err != nil {
		return nil, mapError(err)
	}
	emails := make([]domain.OutboxEmail, len(rows))
	for i, e := range rows {
		emails[i] = outboxEmail(e.ID, e.ToAddress, e.Template, e.Locale, e.Data, e.Attempts, e.NextAttemptAt, e.CreatedAt)
	}
	return emails, nil
}

func outboxEmail(id int64, to, template, locale string, data []byte, attempts int32, next, created time.Time) domain.OutboxEmail {
	email := domain.OutboxEmail{ID: id, To: to, Template: template, Locale: locale, Attempts: int(attempts), NextAttemptAt: next, CreatedAt: created}
	_ = json.Unmarshal(data, &email.Data)
	return email
}

func (r *EmailOutboxRepository) MarkSent(ctx context.Context, id int64) error {
	return mapError(r.q(ctx).MarkEmailSent(ctx, id))
}

func (r *EmailOutboxRepository) MarkRetry(ctx context.Context, id int64, lastError string, nextAttemptAt time.Time) error {
	return mapError(r.q(ctx).MarkEmailRetry(ctx, db.MarkEmailRetryParams{ID: id, LastError: lastError, NextAttemptAt: nextAttemptAt}))
}

func (r *EmailOutboxRepository) MarkDead(ctx context.Context, id int64, lastError string) error {
	return mapError(r.q(ctx).MarkEmailDead(ctx, db.MarkEmailDeadParams{ID: id, LastError: lastError}))
}

func (r *EmailOutboxRepository) DeleteFinished(ctx context.Context, sentBefore, deadBefore time.Time) (int64, error) {
	return deleteInBatches(ctx, func(limit int32) (int64, error) {
		return r.q(ctx).DeleteFinishedEmails(ctx, db.DeleteFinishedEmailsParams{SentBefore: sentBefore, DeadBefore: deadBefore, RowLimit: limit})
	})
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
