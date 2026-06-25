package domain

import (
	"time"

	"github.com/google/uuid"
)

type Device struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	DeviceName      string
	DeviceType      string
	DeviceToken     string
	LastSeenAt      time.Time
	CreatedAt       time.Time
	Revoked         bool
	LastSyncEventID int64
}
