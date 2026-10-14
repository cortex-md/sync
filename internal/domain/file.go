package domain

import (
	"time"

	"github.com/google/uuid"
)

type FileSnapshot struct {
	ID               uuid.UUID
	VaultID          uuid.UUID
	FilePath         string
	Version          int
	EncryptedBlobKey string
	SizeBytes        int64
	Checksum         string
	CreatedBy        uuid.UUID
	DeviceID         uuid.UUID
	CreatedAt        time.Time
}

type FileDelta struct {
	ID             uuid.UUID
	VaultID        uuid.UUID
	FilePath       string
	BaseVersion    int
	TargetVersion  int
	EncryptedDelta []byte
	SizeBytes      int64
	CreatedBy      uuid.UUID
	DeviceID       uuid.UUID
	CreatedAt      time.Time
}

type FileLatest struct {
	VaultID                 uuid.UUID
	FilePath                string
	CurrentVersion          int
	LatestSnapshotVersion   int
	Checksum                string
	SizeBytes               int64
	ContentType             string
	Deleted                 bool
	LastModifiedBy          uuid.UUID
	LastDeviceID            uuid.UUID
	CreatedBy               uuid.UUID
	DeltasSinceSnapshot     int
	DeltaBytesSinceSnapshot int64
	UpdatedAt               time.Time
	CreatedAt               time.Time
}

type HistoryEntry struct {
	FileSnapshot
	AuthorName      string
	AuthorAvatarKey string
	DeviceName      string
}

type BlobDeletion struct {
	ID       int64
	Key      string
	Attempts int
}

type FileListQuery struct {
	VaultID        uuid.UUID
	IncludeDeleted bool
	AfterPath      string
	Limit          int
	Scope          Scope
}
