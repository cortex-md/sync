package domain

import (
	"time"

	"github.com/google/uuid"
)

type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	DeviceID  uuid.UUID
	TokenHash string
	FamilyID  uuid.UUID
	ExpiresAt time.Time
	Revoked   bool
	CreatedAt time.Time
}

type UserTokenKind string

const (
	UserTokenEmailVerify   UserTokenKind = "email_verify"
	UserTokenEmailChange   UserTokenKind = "email_change"
	UserTokenPasswordReset UserTokenKind = "password_reset"
)

type UserToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Kind      UserTokenKind
	TokenHash string
	Payload   string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}
