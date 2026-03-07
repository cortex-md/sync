package domain

import (
	"time"

	"github.com/google/uuid"
)

type CollabUpdate struct {
	ID        int64
	VaultID   uuid.UUID
	FilePath  string
	Data      []byte
	CreatedAt time.Time
}

type CollabDocument struct {
	VaultID        uuid.UUID
	FilePath       string
	CompactedState []byte
	StateVector    []byte
	UpdateCount    int
	UpdatedAt      time.Time
}
