package httpx

import (
	"time"

	"github.com/google/uuid"
)

type Claims struct {
	UserID   uuid.UUID
	Email    string
	DeviceID uuid.UUID
	IssuedAt time.Time
}
