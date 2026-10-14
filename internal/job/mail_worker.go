package job

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	mailBatchSize   = 20
	mailMaxAttempts = 10
	mailMaxBackoff  = 6 * time.Hour
)

var mailGiveUpAt = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

type MailWorker struct {
	tx       Transactor
	outbox   EmailOutboxRepository
	mailer   Mailer
	interval time.Duration
}

func NewMailWorker(tx Transactor, outbox EmailOutboxRepository, mailer Mailer, interval time.Duration) *MailWorker {
	return &MailWorker{tx: tx, outbox: outbox, mailer: mailer, interval: interval}
}

func (w *MailWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		for {
			processed, err := w.RunOnce(ctx)
			if err != nil {
				log.Error().Err(err).Msg("mail worker: batch failed")
				break
			}
			if processed < mailBatchSize {
				break
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *MailWorker) RunOnce(ctx context.Context) (int, error) {
	processed := 0
	err := w.tx.RunInTx(ctx, func(ctx context.Context) error {
		emails, err := w.outbox.ClaimDue(ctx, mailBatchSize)
		if err != nil {
			return err
		}
		processed = len(emails)
		for _, email := range emails {
			if err := w.mailer.Send(ctx, email); err != nil {
				next := retryAt(email.Attempts + 1)
				log.Warn().Err(err).Int64("email_id", email.ID).Str("template", email.Template).Int("attempt", email.Attempts+1).Msg("mail worker: send failed")
				if markErr := w.outbox.MarkFailed(ctx, email.ID, err.Error(), next); markErr != nil {
					return markErr
				}
				continue
			}
			if err := w.outbox.MarkSent(ctx, email.ID); err != nil {
				return err
			}
		}
		return nil
	})
	return processed, err
}

func retryAt(attempt int) time.Time {
	if attempt >= mailMaxAttempts {
		return mailGiveUpAt
	}
	backoff := time.Minute << (attempt - 1)
	if backoff > mailMaxBackoff {
		backoff = mailMaxBackoff
	}
	return time.Now().Add(backoff)
}
