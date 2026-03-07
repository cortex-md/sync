package postgres

import (
	"context"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SyncEventRepository struct {
	pool *pgxpool.Pool
}

func NewSyncEventRepository(pool *pgxpool.Pool) *SyncEventRepository {
	return &SyncEventRepository{pool: pool}
}

func (r *SyncEventRepository) Create(ctx context.Context, event *domain.SyncEvent) error {
	return connFromContext(ctx, r.pool).QueryRow(ctx,
		`INSERT INTO sync_events (vault_id, event_type, file_path, version, actor_id, device_id, metadata, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		event.VaultID, event.EventType, event.FilePath, event.Version,
		event.ActorID, event.DeviceID, event.Metadata, event.CreatedAt,
	).Scan(&event.ID)
}

func (r *SyncEventRepository) ListByVaultID(ctx context.Context, vaultID uuid.UUID, sinceID int64, limit int) ([]domain.SyncEvent, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, vault_id, event_type, file_path, version, actor_id, device_id, metadata, created_at
		 FROM sync_events WHERE vault_id = $1 AND id > $2 ORDER BY id LIMIT $3`,
		vaultID, sinceID, limit,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var events []domain.SyncEvent
	for rows.Next() {
		var e domain.SyncEvent
		if err := rows.Scan(&e.ID, &e.VaultID, &e.EventType, &e.FilePath, &e.Version,
			&e.ActorID, &e.DeviceID, &e.Metadata, &e.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (r *SyncEventRepository) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.pool.Exec(ctx,
		`DELETE FROM sync_events WHERE created_at < $1`,
		before,
	)
	if err != nil {
		return 0, mapError(err)
	}
	return result.RowsAffected(), nil
}
