package domain

import (
	"encoding/json"
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

func (e *SyncEvent) PayloadJSON() ([]byte, error) {
	data := make(map[string]any, len(e.Metadata)+7)
	for k, v := range e.Metadata {
		data[k] = v
	}
	data["vault_uuid"] = e.VaultID.String()
	data["event_type"] = string(e.EventType)
	data["file_path"] = e.FilePath
	data["version"] = e.Version
	data["actor_id"] = e.ActorID.String()
	data["device_id"] = e.DeviceID.String()
	data["created_at"] = e.CreatedAt.UTC().Format(time.RFC3339)
	return json.Marshal(data)
}
