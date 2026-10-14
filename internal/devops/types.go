package devops

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Event struct {
	Type        string
	UserID      uuid.UUID
	Email       string
	DisplayName string
	OccurredAt  time.Time
	Fields      map[string]string
}

type Notifier interface {
	Notify(ctx context.Context, event Event) error
}
