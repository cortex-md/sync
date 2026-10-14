package httpx

import (
	"github.com/google/uuid"
)

type Claims struct {
	UserID   uuid.UUID
	Email    string
	DeviceID uuid.UUID
}
