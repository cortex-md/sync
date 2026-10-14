package fake

import (
	"context"
	"sync"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
	"github.com/google/uuid"
)

type DeviceRepository struct {
	mu      sync.RWMutex
	devices map[uuid.UUID]*domain.Device
	byToken map[string]uuid.UUID
	cursors map[[2]uuid.UUID]domain.DeviceVaultCursor
}

func NewDeviceRepository() *DeviceRepository {
	return &DeviceRepository{
		devices: make(map[uuid.UUID]*domain.Device),
		byToken: make(map[string]uuid.UUID),
	}
}

func (r *DeviceRepository) Create(_ context.Context, device *domain.Device) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.devices[device.ID]; exists {
		return domain.ErrAlreadyExists
	}
	stored := *device
	r.devices[device.ID] = &stored
	r.byToken[device.DeviceToken] = device.ID
	return nil
}

func (r *DeviceRepository) GetByID(_ context.Context, id uuid.UUID) (*domain.Device, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	device, exists := r.devices[id]
	if !exists {
		return nil, domain.ErrNotFound
	}
	result := *device
	return &result, nil
}

func (r *DeviceRepository) GetByToken(_ context.Context, tokenHash string) (*domain.Device, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, exists := r.byToken[tokenHash]
	if !exists {
		return nil, domain.ErrNotFound
	}
	device := r.devices[id]
	result := *device
	return &result, nil
}

func (r *DeviceRepository) ListByUserID(_ context.Context, userID uuid.UUID) ([]domain.Device, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.Device
	for _, d := range r.devices {
		if d.UserID == userID {
			result = append(result, *d)
		}
	}
	return result, nil
}

func (r *DeviceRepository) UpdateLastSeen(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	device, exists := r.devices[id]
	if !exists {
		return domain.ErrNotFound
	}
	device.LastSeenAt = time.Now()
	return nil
}

func (r *DeviceRepository) Revoke(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	device, exists := r.devices[id]
	if !exists {
		return domain.ErrNotFound
	}
	device.Revoked = true
	return nil
}

func (r *DeviceRepository) Update(_ context.Context, device *domain.Device) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.devices[device.ID]; !exists {
		return domain.ErrNotFound
	}
	stored := *device
	r.devices[device.ID] = &stored
	return nil
}

func (r *DeviceRepository) ListByIDs(_ context.Context, ids []uuid.UUID) ([]domain.Device, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.Device
	for _, id := range ids {
		if d, ok := r.devices[id]; ok {
			result = append(result, *d)
		}
	}
	return result, nil
}

func (r *DeviceRepository) UpsertVaultCursor(_ context.Context, deviceID, vaultID uuid.UUID, lastEventID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cursors == nil {
		r.cursors = make(map[[2]uuid.UUID]domain.DeviceVaultCursor)
	}
	key := [2]uuid.UUID{deviceID, vaultID}
	current := r.cursors[key]
	if lastEventID < current.LastEventID {
		lastEventID = current.LastEventID
	}
	r.cursors[key] = domain.DeviceVaultCursor{DeviceID: deviceID, VaultID: vaultID, LastEventID: lastEventID, SyncedAt: time.Now()}
	return nil
}

func (r *DeviceRepository) ListVaultCursorsByUser(_ context.Context, userID uuid.UUID) ([]domain.DeviceVaultCursor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.DeviceVaultCursor
	for _, c := range r.cursors {
		if d, ok := r.devices[c.DeviceID]; ok && d.UserID == userID {
			result = append(result, c)
		}
	}
	return result, nil
}

func (r *DeviceRepository) ListActiveByUsers(_ context.Context, userIDs []uuid.UUID) ([]domain.Device, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	wanted := make(map[uuid.UUID]bool, len(userIDs))
	for _, id := range userIDs {
		wanted[id] = true
	}
	var result []domain.Device
	for _, d := range r.devices {
		if wanted[d.UserID] && !d.Revoked {
			result = append(result, *d)
		}
	}
	return result, nil
}

func (r *DeviceRepository) ListVaultCursorsByVault(_ context.Context, vaultID uuid.UUID) ([]domain.DeviceVaultCursor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.DeviceVaultCursor
	for _, c := range r.cursors {
		if c.VaultID == vaultID {
			result = append(result, c)
		}
	}
	return result, nil
}
