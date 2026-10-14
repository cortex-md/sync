package realtime

import (
	"context"

	"github.com/google/uuid"
)

type StreamGuard func(ctx context.Context, vaultID, userID uuid.UUID) error

type CollabMessage struct {
	ClientID string
	Frame    *Outbound
}
