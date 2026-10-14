package domain

import (
	"time"

	"github.com/google/uuid"
)

type NoteShare struct {
	ID         string
	VaultID    uuid.UUID
	FilePath   string
	CreatedBy  uuid.UUID
	WrappedKey []byte
	Revision   int
	BlobKey    string
	SizeBytes  int64
	ExpiresAt  *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (s *NoteShare) Available(now time.Time) bool {
	return s.Revision > 0 && (s.ExpiresAt == nil || now.Before(*s.ExpiresAt))
}

type RemovedShare struct {
	VaultID   uuid.UUID
	BlobKey   string
	SizeBytes int64
}
