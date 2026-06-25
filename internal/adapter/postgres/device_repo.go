package postgres

import (
	"context"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DeviceRepository struct {
	pool *pgxpool.Pool
}

func NewDeviceRepository(pool *pgxpool.Pool) *DeviceRepository {
	return &DeviceRepository{pool: pool}
}

func (r *DeviceRepository) Create(ctx context.Context, device *domain.Device) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO devices (id, user_id, device_name, device_type, device_token, last_seen_at, created_at, revoked, last_sync_event_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		device.ID, device.UserID, device.DeviceName, device.DeviceType, device.DeviceToken,
		device.LastSeenAt, device.CreatedAt, device.Revoked, device.LastSyncEventID,
	)
	return mapError(err)
}

func (r *DeviceRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Device, error) {
	var d domain.Device
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, device_name, device_type, device_token, last_seen_at, created_at, revoked, last_sync_event_id
		 FROM devices WHERE id = $1`, id,
	).Scan(&d.ID, &d.UserID, &d.DeviceName, &d.DeviceType, &d.DeviceToken, &d.LastSeenAt, &d.CreatedAt, &d.Revoked, &d.LastSyncEventID)
	if err != nil {
		return nil, mapError(err)
	}
	return &d, nil
}

func (r *DeviceRepository) GetByToken(ctx context.Context, tokenHash string) (*domain.Device, error) {
	var d domain.Device
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, device_name, device_type, device_token, last_seen_at, created_at, revoked, last_sync_event_id
		 FROM devices WHERE device_token = $1`, tokenHash,
	).Scan(&d.ID, &d.UserID, &d.DeviceName, &d.DeviceType, &d.DeviceToken, &d.LastSeenAt, &d.CreatedAt, &d.Revoked, &d.LastSyncEventID)
	if err != nil {
		return nil, mapError(err)
	}
	return &d, nil
}

func (r *DeviceRepository) ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Device, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, device_name, device_type, device_token, last_seen_at, created_at, revoked, last_sync_event_id
		 FROM devices WHERE user_id = $1 ORDER BY created_at`, userID,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var devices []domain.Device
	for rows.Next() {
		var d domain.Device
		if err := rows.Scan(&d.ID, &d.UserID, &d.DeviceName, &d.DeviceType, &d.DeviceToken, &d.LastSeenAt, &d.CreatedAt, &d.Revoked, &d.LastSyncEventID); err != nil {
			return nil, mapError(err)
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

func (r *DeviceRepository) UpdateLastSeen(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE devices SET last_seen_at = now() WHERE id = $1`, id,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *DeviceRepository) Revoke(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE devices SET revoked = true WHERE id = $1`, id,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *DeviceRepository) Update(ctx context.Context, device *domain.Device) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE devices SET user_id = $1, device_name = $2, device_type = $3, device_token = $4, last_seen_at = $5, revoked = $6
		 WHERE id = $7`,
		device.UserID, device.DeviceName, device.DeviceType, device.DeviceToken, device.LastSeenAt, device.Revoked, device.ID,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *DeviceRepository) UpdateSyncCursor(ctx context.Context, id uuid.UUID, lastSyncEventID int64) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE devices SET last_sync_event_id = $1 WHERE id = $2`,
		lastSyncEventID, id,
	)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
