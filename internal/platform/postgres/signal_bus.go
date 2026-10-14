package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/platform/postgres/db"

	"github.com/cortexnotes/cortex-sync/internal/platform/bus"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

const (
	SignalChannel        = "cortex_signals"
	maxInlineSignalBytes = 7000
)

type SignalBus struct {
	bus.Handlers
	pool       *pgxpool.Pool
	instanceID string
}

func NewSignalBus(pool *pgxpool.Pool) *SignalBus {
	return &SignalBus{pool: pool, instanceID: uuid.NewString()}
}

func (b *SignalBus) InstanceID() string {
	return b.instanceID
}

type relayPointer struct {
	RelayID int64 `json:"r"`
}

func (b *SignalBus) Publish(ctx context.Context, signal bus.Signal) error {
	signal.Origin = b.instanceID
	payload, err := json.Marshal(signal)
	if err != nil {
		return err
	}

	conn := connFromContext(ctx, b.pool)
	if len(payload) > maxInlineSignalBytes {
		relayID, err := db.New(conn).InsertRelay(ctx, payload)
		if err != nil {
			return mapError(err)
		}
		payload, _ = json.Marshal(relayPointer{RelayID: relayID})
	}

	_, err = conn.Exec(ctx, `SELECT pg_notify($1, $2)`, SignalChannel, string(payload))
	return mapError(err)
}

func (b *SignalBus) Handler() NotificationHandler {
	return func(ctx context.Context, payload string) {
		raw := []byte(payload)

		var pointer relayPointer
		if err := json.Unmarshal(raw, &pointer); err == nil && pointer.RelayID > 0 {
			relayed, err := db.New(b.pool).GetRelay(ctx, pointer.RelayID)
			if err != nil {
				log.Warn().Err(err).Int64("relay_id", pointer.RelayID).Msg("failed to load relayed signal")
				return
			}
			raw = relayed
		}

		var signal bus.Signal
		if err := json.Unmarshal(raw, &signal); err != nil {
			log.Warn().Err(err).Msg("invalid signal payload")
			return
		}
		b.Dispatch(signal)
	}
}

func (b *SignalBus) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	n, err := db.New(connFromContext(ctx, b.pool)).DeleteRelayBefore(ctx, time.Now().Add(-olderThan))
	return n, mapError(err)
}
