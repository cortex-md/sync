package server

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"

	"github.com/cortexnotes/cortex-sync/internal/platform/txhook"
)

type kickingBlobQueue struct {
	BlobQueue
	kick func()
}

func (q kickingBlobQueue) EnqueueDeletion(ctx context.Context, keys ...string) error {
	if err := q.BlobQueue.EnqueueDeletion(ctx, keys...); err != nil {
		return err
	}
	txhook.AfterCommit(ctx, q.kick)
	return nil
}

type kickingOutbox struct {
	EmailOutboxRepository
	kick func(id int64)
}

func (o kickingOutbox) Enqueue(ctx context.Context, email *domain.OutboxEmail) error {
	if err := o.EmailOutboxRepository.Enqueue(ctx, email); err != nil {
		return err
	}
	id := email.ID
	txhook.AfterCommit(ctx, func() { o.kick(id) })
	return nil
}
