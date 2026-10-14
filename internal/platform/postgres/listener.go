package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type NotificationHandler func(ctx context.Context, payload string)

type Listener struct {
	pool     *pgxpool.Pool
	handlers map[string]NotificationHandler
}

func NewListener(pool *pgxpool.Pool) *Listener {
	return &Listener{pool: pool, handlers: make(map[string]NotificationHandler)}
}

func (l *Listener) Handle(channel string, handler NotificationHandler) {
	l.handlers[channel] = handler
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
