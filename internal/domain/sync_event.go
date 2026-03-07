package domain

import (
	"time"

	"github.com/google/uuid"
)

type EventType string

const (
	EventFileUpdated    EventType = "file_updated"
	EventFileDeleted    EventType = "file_deleted"
	EventFileRenamed    EventType = "file_renamed"
	EventFileCreated    EventType = "file_created"
	EventCollabActive   EventType = "collab_active"
	EventCollabInactive EventType = "collab_inactive"
)

type SyncEvent struct {
	ID        int64
	VaultID   uuid.UUID
	EventType EventType
	FilePath  string
	Version   int
	ActorID   uuid.UUID
	DeviceID  uuid.UUID
	Metadata  map[string]any
	CreatedAt time.Time
}
