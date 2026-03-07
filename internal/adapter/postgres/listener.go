package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/port"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type Listener struct {
	pool    *pgxpool.Pool
	broker  port.SSEBroker
	channel string
}

func NewListener(pool *pgxpool.Pool, broker port.SSEBroker) *Listener {
	return &Listener{
		pool:    pool,
		broker:  broker,
		channel: "sync_events",
	}
}

type notifyPayload struct {
	ID        int64  `json:"id"`
	VaultID   string `json:"vault_id"`
	EventType string `json:"event_type"`
	FilePath  string `json:"file_path"`
	Version   int    `json:"version"`
	ActorID   string `json:"actor_id"`
	DeviceID  string `json:"device_id"`
}

func (l *Listener) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := l.listen(ctx); err != nil {
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

	_, err = conn.Exec(ctx, "LISTEN "+l.channel)
	if err != nil {
		return fmt.Errorf("listening on channel: %w", err)
	}

	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("waiting for notification: %w", err)
		}

		var payload notifyPayload
		if err := json.Unmarshal([]byte(notification.Payload), &payload); err != nil {
			log.Warn().Err(err).Str("payload", notification.Payload).Msg("invalid pg notify payload")
			continue
		}

		vaultID, err := uuid.Parse(payload.VaultID)
		if err != nil {
			log.Warn().Err(err).Str("vault_id", payload.VaultID).Msg("invalid vault id in pg notify")
			continue
		}

		data, _ := json.Marshal(map[string]any{
			"file_path": payload.FilePath,
			"version":   payload.Version,
			"actor_id":  payload.ActorID,
			"device_id": payload.DeviceID,
		})

		l.broker.Publish(vaultID, port.SSEEvent{
			ID:        fmt.Sprintf("%d", payload.ID),
			EventType: payload.EventType,
			Data:      string(data),
		})
	}
}
