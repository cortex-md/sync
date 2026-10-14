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
}

func NewBlobJanitor(tx Transactor, queue BlobQueue, blobs BlobDeleter, interval time.Duration) *BlobJanitor {
	return &BlobJanitor{tx: tx, queue: queue, blobs: blobs, interval: interval}
}

func (j *BlobJanitor) Run(ctx context.Context) {
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()
	for {
		if err := j.RunOnce(ctx); err != nil && ctx.Err() == nil {
			log.Error().Err(err).Msg("blob janitor: run failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (j *BlobJanitor) RunOnce(ctx context.Context) error {
	if err := j.tx.RunInTx(ctx, func(ctx context.Context) error {
		stale, err := j.queue.ClaimStalePending(ctx, time.Now().Add(-blobPendingGrace), blobBatchSize)
		if err != nil || len(stale) == 0 {
			return err
		}
		log.Info().Int("count", len(stale)).Msg("blob janitor: orphaned uploads scheduled for deletion")
		return j.queue.EnqueueDeletion(ctx, stale...)
	}); err != nil {
		return err
	}

	deletions, err := j.queue.ClaimDueDeletions(ctx, blobBatchSize)
	if err != nil {
		return err
	}
	for _, d := range deletions {
		if err := j.blobs.Delete(ctx, d.Key); err != nil {
			if retryErr := j.queue.RetryDeletion(ctx, d.ID, err.Error(), time.Now().Add(deletionBackoff(d.Attempts+1))); retryErr != nil {
				return retryErr
			}
			continue
		}
		if err := j.queue.CompleteDeletion(ctx, d.ID); err != nil {
			return err
		}
	}
	return nil
}

func deletionBackoff(attempt int) time.Duration {
	backoff := time.Minute << min(attempt-1, 10)
	return min(backoff, blobDeletionMaxBackoff)
}
