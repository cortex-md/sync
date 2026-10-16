package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type NotificationHandler func(ctx context.Context, payload string)

type Listener struct {
	pool        *pgxpool.Pool
	handlers    map[string]NotificationHandler
	reconnected []func(ctx context.Context)
	connected   bool
}

func NewListener(pool *pgxpool.Pool) *Listener {
	return &Listener{pool: pool, handlers: make(map[string]NotificationHandler)}
}

func (l *Listener) Handle(channel string, handler NotificationHandler) {
	l.handlers[channel] = handler
}

func (l *Listener) OnReconnect(fn func(ctx context.Context)) {
	l.reconnected = append(l.reconnected, fn)
}

func (l *Listener) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := l.listen(ctx); err != nil && ctx.Err() == nil {
			log.Error().Err(err).Msg("pg notify listener disconnected")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (l *Listener) listen(ctx context.Context) error {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring connection: %w", err)
	}
	defer conn.Release()

	for channel := range l.handlers {
		if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
			return fmt.Errorf("listening on channel %s: %w", channel, err)
		}
	}

	if l.connected {
		log.Info().Msg("pg notify listener reconnected, catching up")
		for _, fn := range l.reconnected {
			fn(ctx)
		}
	}
	l.connected = true

	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("waiting for notification: %w", err)
		}
		if handler, ok := l.handlers[notification.Channel]; ok {
			handler(ctx, notification.Payload)
		}
	}
}

type syncEventLoader interface {
	GetByID(ctx context.Context, id int64) (*domain.SyncEvent, error)
	ListAfter(ctx context.Context, afterID int64, limit int) ([]domain.SyncEvent, error)
}

const syncEventCatchUpPage = 500

type SyncEventRelay struct {
	events  syncEventLoader
	publish func(*domain.SyncEvent)
	last    atomic.Int64
}

func NewSyncEventRelay(events syncEventLoader, publish func(*domain.SyncEvent)) *SyncEventRelay {
	return &SyncEventRelay{events: events, publish: publish}
}

func (r *SyncEventRelay) Handler() NotificationHandler {
	inner := SyncEventHandler(r.events, func(event *domain.SyncEvent) {
		r.observe(event.ID)
		r.publish(event)
	})
	return inner
}

func (r *SyncEventRelay) CatchUp(ctx context.Context) {
	after := r.last.Load()
	if after == 0 {
		return
	}
	for ctx.Err() == nil {
		events, err := r.events.ListAfter(ctx, after, syncEventCatchUpPage)
		if err != nil {
			log.Warn().Err(err).Msg("sync event catch-up failed")
			return
		}
		for i := range events {
			r.observe(events[i].ID)
			r.publish(&events[i])
			after = events[i].ID
		}
		if len(events) < syncEventCatchUpPage {
			return
		}
	}
}

func (r *SyncEventRelay) observe(id int64) {
	for {
		current := r.last.Load()
		if id <= current || r.last.CompareAndSwap(current, id) {
			return
		}
	}
}

func SyncEventHandler(events syncEventLoader, publish func(*domain.SyncEvent)) NotificationHandler {
	return func(ctx context.Context, payload string) {
		var notification struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal([]byte(payload), &notification); err != nil {
			log.Warn().Err(err).Str("payload", payload).Msg("invalid sync event notification")
			return
		}

		event, err := events.GetByID(ctx, notification.ID)
		if err != nil {
			log.Warn().Err(err).Int64("event_id", notification.ID).Msg("failed to load sync event from pg notify")
			return
		}

		publish(event)
	}
}
