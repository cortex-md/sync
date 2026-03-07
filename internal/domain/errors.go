package domain

import "errors"

var (
	ErrNotFound           = errors.New("not found")
	ErrAlreadyExists      = errors.New("already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrTokenExpired       = errors.New("token expired")
	ErrTokenRevoked       = errors.New("token revoked")
	ErrTokenReuseDetected = errors.New("token reuse detected")
	ErrDeviceRevoked      = errors.New("device revoked")
	ErrInsufficientRole   = errors.New("insufficient role")
	ErrVaultAccessDenied  = errors.New("vault access denied")
	ErrInviteExpired      = errors.New("invite expired")
	ErrConflict           = errors.New("conflict")
	ErrFileTooLarge       = errors.New("file too large")
	ErrInvalidInput       = errors.New("invalid input")
	ErrCollabRoomFull     = errors.New("collab room full")
	ErrCollabReadOnly     = errors.New("collab read only")
	ErrCollabNotActive    = errors.New("collab session not active")
)
