package domain

import (
	"time"

	"github.com/google/uuid"
)

type VaultInvite struct {
	ID                uuid.UUID
	VaultID           uuid.UUID
	InviterID         uuid.UUID
	InviteeEmail      string
	Role              VaultRole
	EncryptedVaultKey []byte
	Accepted          bool
	ExpiresAt         time.Time
	CreatedAt         time.Time
}

type VaultKey struct {
	VaultID      uuid.UUID
	UserID       uuid.UUID
	EncryptedKey []byte
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
