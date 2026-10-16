package job

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/rs/zerolog/log"
)

const (
	blobBatchSize          = 100
	blobPendingGrace       = time.Hour
	blobDeletionMaxBackoff = 6 * time.Hour
)

type BlobQueue interface {
	ClaimStalePending(ctx context.Context, before time.Time, limit int) ([]string, error)
	EnqueueDeletion(ctx context.Context, keys ...string) error
	ClaimDueDeletions(ctx context.Context, limit int) ([]domain.BlobDeletion, error)
	CompleteDeletion(ctx context.Context, id int64) error
	RetryDeletion(ctx context.Context, id int64, lastError string, next time.Time) error
}

type BlobDeleter interface {
	Delete(ctx context.Context, key string) error
}

type BlobJanitor struct {
	tx       Transactor
	queue    BlobQueue
	blobs    BlobDeleter
	interval time.Duration
	debounce time.Duration
	kick     chan struct{}
}

func NewBlobJanitor(tx Transactor, queue BlobQueue, blobs BlobDeleter, interval time.Duration) *BlobJanitor {
	return &BlobJanitor{tx: tx, queue: queue, blobs: blobs, interval: interval, debounce: 5 * time.Second, kick: make(chan struct{}, 1)}
}

func (j *BlobJanitor) Kick() {
	select {
	case j.kick <- struct{}{}:
	default:
	}
}

func (j *BlobJanitor) Run(ctx context.Context) {
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	for {
		j.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-j.kick:
			select {
			case <-ctx.Done():
				return
			case <-time.After(j.debounce):
			}
		}
	}
}

func (j *BlobJanitor) drain(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := j.RunOnce(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Error().Err(err).Msg("blob janitor: run failed")
			}
			return
		}
		if processed < blobBatchSize {
			return
		}
	}
}

func (j *BlobJanitor) RunOnce(ctx context.Context) (int, error) {
	stale := 0
	if err := j.tx.RunInTx(ctx, func(ctx context.Context) error {
		keys, err := j.queue.ClaimStalePending(ctx, time.Now().Add(-blobPendingGrace), blobBatchSize)
		if err != nil || len(keys) == 0 {
			return err
		}
		stale = len(keys)
		log.Info().Int("count", len(keys)).Msg("blob janitor: orphaned uploads scheduled for deletion")
		return j.queue.EnqueueDeletion(ctx, keys...)
	}); err != nil {
		return 0, err
	}

	deletions, err := j.queue.ClaimDueDeletions(ctx, blobBatchSize)
	if err != nil {
		return 0, err
	}
	for _, d := range deletions {
		if err := j.blobs.Delete(ctx, d.Key); err != nil {
			if retryErr := j.queue.RetryDeletion(ctx, d.ID, err.Error(), time.Now().Add(deletionBackoff(d.Attempts+1))); retryErr != nil {
				return 0, retryErr
			}
			continue
		}
		if err := j.queue.CompleteDeletion(ctx, d.ID); err != nil {
			return 0, err
		}
	}
	return max(stale, len(deletions)), nil
}

func deletionBackoff(attempt int) time.Duration {
	backoff := time.Minute << min(attempt-1, 10)
	return min(backoff, blobDeletionMaxBackoff)
}
