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
	TokenHash         string
	Grants            []InviteGrant
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

type VaultEncryption struct {
	VaultID      uuid.UUID
	Salt         []byte
	EncryptedVEK []byte
	CreatedAt    time.Time
}

type InviteDetails struct {
	VaultInvite
	VaultName    string
	InviterName  string
	InviterEmail string
}

type InviteGrant struct {
	Path     string `json:"path"`
	CanWrite bool   `json:"can_write"`
}
