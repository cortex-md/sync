package devops

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type NoopNotifier struct{}

func NewNoopNotifier() *NoopNotifier {
	return &NoopNotifier{}
}

func (n *NoopNotifier) Notify(_ context.Context, _ port.DevOpsEvent) error {
	return nil
}

type AsyncNotifier struct {
	target port.DevOpsNotifier
	events chan port.DevOpsEvent
	ctx    context.Context
}

func NewAsyncNotifier(ctx context.Context, target port.DevOpsNotifier, queueSize int) *AsyncNotifier {
	if queueSize <= 0 {
		queueSize = 1
	}
	notifier := &AsyncNotifier{
		target: target,
		events: make(chan port.DevOpsEvent, queueSize),
		ctx:    ctx,
	}
	go notifier.run()
	return notifier
}

func (n *AsyncNotifier) Notify(ctx context.Context, event port.DevOpsEvent) error {
	if n == nil || n.target == nil {
		return nil
	}
	select {
	case <-n.ctx.Done():
		return nil
	default:
	}
	select {
	case n.events <- event:
	case <-n.ctx.Done():
	default:
		zerolog.Ctx(ctx).Warn().
			Str("event_type", event.Type).
			Msg("devops notification queue full")
	}
	return nil
}

func (n *AsyncNotifier) run() {
	for {
		select {
		case <-n.ctx.Done():
			return
		case event := <-n.events:
			if err := n.target.Notify(context.Background(), event); err != nil {
				log.Warn().
					Err(err).
					Str("event_type", event.Type).
					Msg("devops notification failed")
			}
		}
	}
}
