package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type SubscriptionListener struct {
	pool       *pgxpool.Pool
	channel    string
	invalidate func(uuid.UUID)
}

func NewSubscriptionListener(pool *pgxpool.Pool, invalidate func(uuid.UUID)) *SubscriptionListener {
	return &SubscriptionListener{
		pool:       pool,
		channel:    "subscription_updates",
		invalidate: invalidate,
	}
}

type subscriptionNotifyPayload struct {
	UserID string `json:"user_id"`
}

func (l *SubscriptionListener) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := l.listen(ctx); err != nil {
			log.Error().Err(err).Msg("subscription pg notify listener disconnected")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (l *SubscriptionListener) listen(ctx context.Context) error {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring connection: %w", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, "LISTEN "+l.channel)
	if err != nil {
		return fmt.Errorf("listening on channel: %w", err)
	}

	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("waiting for notification: %w", err)
		}

		var payload subscriptionNotifyPayload
		if err := json.Unmarshal([]byte(notification.Payload), &payload); err != nil {
			log.Warn().Err(err).Str("payload", notification.Payload).Msg("invalid subscription pg notify payload")
			continue
		}
		userID, err := uuid.Parse(payload.UserID)
		if err != nil {
			log.Warn().Err(err).Str("user_id", payload.UserID).Msg("invalid subscription user id in pg notify")
			continue
		}
		if l.invalidate != nil {
			l.invalidate(userID)
		}
	}
}
