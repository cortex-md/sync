package job

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/cortexnotes/cortex-sync/internal/platform/mail"
	"github.com/rs/zerolog/log"
)

const (
	mailLease       = 2 * time.Minute
	mailSweepBatch  = 20
	mailMaxAttempts = 6
	mailQueueSize   = 256
	mailSentKeep    = 7 * 24 * time.Hour
	mailDeadKeep    = 30 * 24 * time.Hour
)

var mailBackoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

type MailDispatcher struct {
	outbox  EmailOutboxRepository
	mailer  Mailer
	queue   chan int64
	wg      sync.WaitGroup
	mu      sync.Mutex
	timers  map[int64]*time.Timer
	stopped bool
}

func NewMailDispatcher(outbox EmailOutboxRepository, mailer Mailer) *MailDispatcher {
	return &MailDispatcher{outbox: outbox, mailer: mailer, queue: make(chan int64, mailQueueSize), timers: make(map[int64]*time.Timer)}
}

func (d *MailDispatcher) Kick(id int64) {
	select {
	case d.queue <- id:
	default:
		log.Warn().Int64("email_id", id).Msg("mail dispatcher: queue full, the next sweep will send it")
	}
}

func (d *MailDispatcher) Run(ctx context.Context) {
	d.wg.Add(1)
	defer d.wg.Done()
	if _, err := d.Sweep(ctx); err != nil && ctx.Err() == nil {
		log.Error().Err(err).Msg("mail dispatcher: boot sweep failed")
	}
	for {
		select {
		case <-ctx.Done():
			d.stopTimers()
			return
		case id := <-d.queue:
			d.dispatch(ctx, id)
		}
	}
}

func (d *MailDispatcher) Wait() {
	d.wg.Wait()
}

func (d *MailDispatcher) Sweep(ctx context.Context) (int64, error) {
	var total int64
	for ctx.Err() == nil {
		emails, err := d.outbox.ClaimDue(ctx, mailSweepBatch, mailLease)
		if err != nil {
			return total, err
		}
		for _, email := range emails {
			d.deliver(ctx, email)
		}
		total += int64(len(emails))
		if len(emails) < mailSweepBatch {
			break
		}
	}
	return total, ctx.Err()
}

func (d *MailDispatcher) PruneFinished(ctx context.Context) (int64, error) {
	now := time.Now()
	return d.outbox.DeleteFinished(ctx, now.Add(-mailSentKeep), now.Add(-mailDeadKeep))
}

func (d *MailDispatcher) dispatch(ctx context.Context, id int64) {
	email, err := d.outbox.Claim(ctx, id, mailLease)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			log.Warn().Err(err).Int64("email_id", id).Msg("mail dispatcher: claim failed")
		}
		return
	}
	d.deliver(ctx, *email)
}

func (d *MailDispatcher) deliver(ctx context.Context, email domain.OutboxEmail) {
	sendErr := d.mailer.Send(ctx, email)
	persistCtx := context.WithoutCancel(ctx)
	if sendErr == nil {
		if err := d.outbox.MarkSent(persistCtx, email.ID); err != nil {
			log.Error().Err(err).Int64("email_id", email.ID).Msg("mail dispatcher: mark sent failed")
		}
		return
	}

	logger := log.Warn().Err(sendErr).Int64("email_id", email.ID).Str("template", email.Template).Int("attempt", email.Attempts)
	if errors.Is(sendErr, mail.ErrPermanent) || email.Attempts >= mailMaxAttempts {
		logger.Msg("mail dispatcher: giving up")
		if err := d.outbox.MarkDead(persistCtx, email.ID, sendErr.Error()); err != nil {
			log.Error().Err(err).Int64("email_id", email.ID).Msg("mail dispatcher: mark dead failed")
		}
		return
	}

	delay := mailBackoff[min(max(email.Attempts-1, 0), len(mailBackoff)-1)]
	var retry *mail.RetryAfterError
	if errors.As(sendErr, &retry) && retry.After > delay {
		delay = retry.After
	}
	logger.Dur("retry_in", delay).Msg("mail dispatcher: send failed")
	if err := d.outbox.MarkRetry(persistCtx, email.ID, sendErr.Error(), time.Now().Add(delay)); err != nil {
		log.Error().Err(err).Int64("email_id", email.ID).Msg("mail dispatcher: mark retry failed")
		return
	}
	d.schedule(email.ID, delay)
}

func (d *MailDispatcher) schedule(id int64, delay time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	if existing, ok := d.timers[id]; ok {
		existing.Stop()
	}
	d.timers[id] = time.AfterFunc(delay+time.Second, func() {
		d.mu.Lock()
		delete(d.timers, id)
		d.mu.Unlock()
		d.Kick(id)
	})
}

func (d *MailDispatcher) stopTimers() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	for id, timer := range d.timers {
		timer.Stop()
		delete(d.timers, id)
	}
}
