package domain

import (
	"time"

	"github.com/google/uuid"
)

type VaultRole string

const (
	VaultRoleOwner  VaultRole = "owner"
	VaultRoleAdmin  VaultRole = "admin"
	VaultRoleEditor VaultRole = "editor"
	VaultRoleViewer VaultRole = "viewer"
)

func (r VaultRole) CanWrite() bool {
	return r == VaultRoleOwner || r == VaultRoleAdmin || r == VaultRoleEditor
}

func (r VaultRole) CanManageMembers() bool {
	return r == VaultRoleOwner || r == VaultRoleAdmin
}

func (r VaultRole) CanDelete() bool {
	return r == VaultRoleOwner
}

type Vault struct {
	ID          uuid.UUID
	Name        string
	Description string
	OwnerID     uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type VaultMember struct {
	VaultID  uuid.UUID
	UserID   uuid.UUID
	Role     VaultRole
	JoinedAt time.Time
}
