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
	VaultRoleGuest  VaultRole = "guest"
)

func (r VaultRole) CanWrite() bool {
	return r.Can(PermFileWrite)
}

func (r VaultRole) CanManageMembers() bool {
	return r.Can(PermMemberManage)
}

func (r VaultRole) CanDelete() bool {
	return r.Can(PermVaultDelete)
}

type Vault struct {
	ID                  uuid.UUID
	Name                string
	Description         string
	OwnerID             uuid.UUID
	Icon                string
	AllowedEmailDomains []string
	AllowGuests         bool
	AllowPublicLinks    bool
	StorageUsedBytes    int64
	StorageQuotaBytes   *int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type VaultSummary struct {
	Vault
	Role        VaultRole
	MemberCount int
	HasKey      bool
}

type PrincipalType string

const (
	PrincipalUser  PrincipalType = "user"
	PrincipalGroup PrincipalType = "group"
)

type AccessEntry struct {
	PrincipalType PrincipalType
	PrincipalID   uuid.UUID
	Level         AccessLevel
}

type PathRule struct {
	VaultID    uuid.UUID
	Path       string
	Restricted bool
	Entries    []AccessEntry
	UpdatedBy  *uuid.UUID
	UpdatedAt  time.Time
}

type VaultGroup struct {
	ID        uuid.UUID
	VaultID   uuid.UUID
	Name      string
	MemberIDs []uuid.UUID
	CreatedBy *uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

type DeviceVaultCursor struct {
	DeviceID    uuid.UUID
	VaultID     uuid.UUID
	LastEventID int64
	SyncedAt    time.Time
}

type VaultMember struct {
	VaultID  uuid.UUID
	UserID   uuid.UUID
	Role     VaultRole
	JoinedAt time.Time
}
