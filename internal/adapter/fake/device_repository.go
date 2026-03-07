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

func (r *DeviceRepository) UpdateSyncCursor(_ context.Context, id uuid.UUID, lastSyncEventID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	device, exists := r.devices[id]
	if !exists {
		return domain.ErrNotFound
	}
	device.LastSyncEventID = lastSyncEventID
	return nil
}
